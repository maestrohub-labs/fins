package fins

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// simBatchClient wires up a working UDPServerSimulator on a random port
// and returns a client + server + cleanup. DM-area behaviour is good enough
// for contiguous reads up to 16384 words.
func simBatchClient(t *testing.T) (*UDPClient, *UDPServer, func()) {
	t.Helper()
	// Random free-ish high ports; picking two so the client and server
	// don't clash.
	clientAddr := NewUDPAddress("127.0.0.1", 0, 0, 2, 0)
	plcAddr := NewUDPAddress("127.0.0.1", 19600+int(time.Now().UnixNano()%1000), 0, 10, 0)

	s, err := NewUDPServerSimulator(plcAddr)
	assert.Nil(t, err)

	c, err := NewUDPClient(context.Background(), clientAddr, plcAddr)
	assert.Nil(t, err)
	c.SetTimeoutMs(500)

	cleanup := func() {
		c.Close()
		s.Close()
		<-s.Done()
	}
	return c, s, cleanup
}

// fillWords writes a known pattern to the simulator so reads can verify it.
// Returns the pattern it wrote.
func fillWords(t *testing.T, c *UDPClient, addr uint16, count int) []uint16 {
	t.Helper()
	pattern := make([]uint16, count)
	for i := range pattern {
		pattern[i] = uint16(i * 7) // arbitrary deterministic pattern
	}
	// Write in chunks of MaxWordsPerFrame so the simulator accepts it.
	ctx := context.Background()
	for off := 0; off < count; off += MaxWordsPerFrame {
		end := off + MaxWordsPerFrame
		if end > count {
			end = count
		}
		err := c.WriteWords(ctx, AreaDMWord, addr+uint16(off), pattern[off:end])
		assert.Nil(t, err)
	}
	return pattern
}

func TestReadWordsBatch_UnderLimit(t *testing.T) {
	c, _, cleanup := simBatchClient(t)
	defer cleanup()

	pattern := fillWords(t, c, 0, 500)

	got, err := c.ReadWordsBatch(context.Background(), AreaDMWord, 0, 500)
	assert.Nil(t, err)
	assert.Equal(t, 500, len(got))
	assert.Equal(t, pattern, got)
}

func TestReadWordsBatch_ExactLimit(t *testing.T) {
	c, _, cleanup := simBatchClient(t)
	defer cleanup()

	pattern := fillWords(t, c, 0, MaxWordsPerFrame)

	got, err := c.ReadWordsBatch(context.Background(), AreaDMWord, 0, MaxWordsPerFrame)
	assert.Nil(t, err)
	assert.Equal(t, MaxWordsPerFrame, len(got))
	assert.Equal(t, pattern, got)
}

func TestReadWordsBatch_OverLimit(t *testing.T) {
	c, _, cleanup := simBatchClient(t)
	defer cleanup()

	const total = 2500 // 999 + 999 + 502
	pattern := fillWords(t, c, 0, total)

	got, err := c.ReadWordsBatch(context.Background(), AreaDMWord, 0, total)
	assert.Nil(t, err)
	assert.Equal(t, total, len(got))
	assert.Equal(t, pattern, got)

	// Spot-check boundary elements to catch off-by-one in chunk stitching.
	assert.Equal(t, pattern[MaxWordsPerFrame-1], got[MaxWordsPerFrame-1])
	assert.Equal(t, pattern[MaxWordsPerFrame], got[MaxWordsPerFrame])
	assert.Equal(t, pattern[2*MaxWordsPerFrame-1], got[2*MaxWordsPerFrame-1])
	assert.Equal(t, pattern[2*MaxWordsPerFrame], got[2*MaxWordsPerFrame])
	assert.Equal(t, pattern[total-1], got[total-1])
}

// TestReadWordsBatch_OverLimitAtOffset covers the non-zero starting address
// case. Addresses 1000..1000+2500-1 across three chunks.
func TestReadWordsBatch_OverLimitAtOffset(t *testing.T) {
	c, _, cleanup := simBatchClient(t)
	defer cleanup()

	const total = 2500
	const base uint16 = 1000
	pattern := fillWords(t, c, base, total)

	got, err := c.ReadWordsBatch(context.Background(), AreaDMWord, base, total)
	assert.Nil(t, err)
	assert.Equal(t, pattern, got)
}

func TestReadWordsBatch_Zero(t *testing.T) {
	c, _, cleanup := simBatchClient(t)
	defer cleanup()

	got, err := c.ReadWordsBatch(context.Background(), AreaDMWord, 0, 0)
	assert.Nil(t, err)
	assert.Equal(t, 0, len(got))
}

func TestReadWordsBatch_NegativeCount(t *testing.T) {
	c, _, cleanup := simBatchClient(t)
	defer cleanup()

	_, err := c.ReadWordsBatch(context.Background(), AreaDMWord, 0, -1)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "non-negative")
}

func TestReadWordsBatch_AddressOverflow(t *testing.T) {
	c, _, cleanup := simBatchClient(t)
	defer cleanup()

	// addr 65000 + count 1000 = 66000, overflows uint16 space.
	_, err := c.ReadWordsBatch(context.Background(), AreaDMWord, 65000, 1000)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "overflows")

	// addr 0 + count 65537 overflows.
	_, err = c.ReadWordsBatch(context.Background(), AreaDMWord, 0, 65537)
	assert.Error(t, err)

	// addr 0 + count 65536 (exactly fills the space) is allowed to pass
	// the overflow check — it may still fail at the PLC, but not at our
	// preflight.
	// (We don't actually run this against the simulator since DM area
	// is only 32KiB; test the preflight separately.)
}

// TestReadWordsBatch_ContextCancelledMidBatch uses a blackhole client so
// the first chunk blocks forever. Cancelling ctx 50ms in must cause the
// batch to return ctx.Err() — proving ctx propagates through both the
// inter-chunk check and the in-flight ReadWords call.
func TestReadWordsBatch_ContextCancelledMidBatch(t *testing.T) {
	c := newBlackholeClient(t)
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := c.ReadWordsBatch(ctx, AreaDMWord, 0, 2500)
	elapsed := time.Since(start)

	assert.True(t, errors.Is(err, context.Canceled), "expected context.Canceled, got %v", err)
	assert.Less(t, elapsed, 250*time.Millisecond)
}

func TestReadBytesBatch_UnderLimit(t *testing.T) {
	c, _, cleanup := simBatchClient(t)
	defer cleanup()

	pattern := fillWords(t, c, 0, 400)

	got, err := c.ReadBytesBatch(context.Background(), AreaDMWord, 0, 400)
	assert.Nil(t, err)
	assert.Equal(t, 800, len(got)) // 400 words = 800 bytes

	// Decode returned bytes to words for an easy equivalence check.
	decoded := c.bytesToUint16s(got)
	assert.Equal(t, pattern, decoded)
}

func TestReadBytesBatch_OverLimit(t *testing.T) {
	c, _, cleanup := simBatchClient(t)
	defer cleanup()

	const total = 2500
	pattern := fillWords(t, c, 0, total)

	got, err := c.ReadBytesBatch(context.Background(), AreaDMWord, 0, total)
	assert.Nil(t, err)
	assert.Equal(t, 2*total, len(got))
	assert.Equal(t, pattern, c.bytesToUint16s(got))
}

// TestReadBytesBatch_ContextCancelledMidBatch mirrors the words-level ctx
// cancellation test to confirm the byte-level helper behaves the same way.
func TestReadBytesBatch_ContextCancelledMidBatch(t *testing.T) {
	c := newBlackholeClient(t)
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err := c.ReadBytesBatch(ctx, AreaDMWord, 0, 2500)
	assert.True(t, errors.Is(err, context.Canceled), "expected context.Canceled, got %v", err)
}
