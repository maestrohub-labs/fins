package fins

import (
	"context"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestStats_Zero verifies Stats() on a fresh client reports zeros.
func TestStats_Zero(t *testing.T) {
	c, _, cleanup := simBatchClient(t)
	defer cleanup()

	s := c.Stats()
	assert.Equal(t, 0, s.InFlightRequests)
	assert.Equal(t, uint64(0), s.LifetimeRequests)
	assert.Equal(t, uint64(0), s.LifetimeTimeouts)
}

// TestStats_LifetimeRequests verifies the counter increments once per
// sendCommand call. A WriteWords + ReadWords pair is two sendCommands.
func TestStats_LifetimeRequests(t *testing.T) {
	c, _, cleanup := simBatchClient(t)
	defer cleanup()

	err := c.WriteWords(context.Background(), AreaDMWord, 0, []uint16{1, 2, 3})
	assert.Nil(t, err)
	_, err = c.ReadWords(context.Background(), AreaDMWord, 0, 3)
	assert.Nil(t, err)

	s := c.Stats()
	assert.Equal(t, uint64(2), s.LifetimeRequests)
	assert.Equal(t, uint64(0), s.LifetimeTimeouts)
	assert.Equal(t, 0, s.InFlightRequests, "no in-flight after sync calls return")
}

// TestStats_BatchReadCounts verifies a batch helper increments
// LifetimeRequests once per chunk.
func TestStats_BatchReadCounts(t *testing.T) {
	c, _, cleanup := simBatchClient(t)
	defer cleanup()

	// Pre-fill so the reads succeed.
	err := c.WriteWords(context.Background(), AreaDMWord, 0, make([]uint16, MaxWordsPerFrame))
	assert.Nil(t, err)
	before := c.Stats().LifetimeRequests

	// count=2500 = 3 chunks = 3 ReadWords calls.
	_, err = c.ReadWordsBatch(context.Background(), AreaDMWord, 0, 2500)
	// Allow PLC-side errors for addresses we didn't pre-fill; the lifetime
	// counter still increments per chunk regardless of success.
	_ = err
	after := c.Stats().LifetimeRequests
	assert.Equal(t, uint64(3), after-before, "expected 3 sendCommand calls for 2500-word batch")
}

// TestStats_LifetimeTimeouts increments once per sendCommand that exits
// via the library-default timeout. Uses a blackhole so every call times out.
func TestStats_LifetimeTimeouts(t *testing.T) {
	clientAddr := NewUDPAddress("127.0.0.1", 0, 0, 2, 0)
	plcAddr := NewUDPAddress("127.0.0.1", 40000+rand.Intn(20000), 0, 10, 0)
	c, err := NewUDPClient(context.Background(), clientAddr, plcAddr)
	assert.Nil(t, err)
	defer c.Close()
	c.SetTimeoutMs(50)

	_, err = c.ReadWords(context.Background(), AreaDMWord, 0, 1)
	assert.Error(t, err)
	_, err = c.ReadWords(context.Background(), AreaDMWord, 0, 1)
	assert.Error(t, err)

	s := c.Stats()
	assert.Equal(t, uint64(2), s.LifetimeTimeouts)
	assert.Equal(t, uint64(2), s.LifetimeRequests)
}

// TestStats_InFlightRequests verifies InFlightRequests reflects live
// pending sendCommand calls.
func TestStats_InFlightRequests(t *testing.T) {
	clientAddr := NewUDPAddress("127.0.0.1", 0, 0, 2, 0)
	plcAddr := NewUDPAddress("127.0.0.1", 40000+rand.Intn(20000), 0, 10, 0)
	c, err := NewUDPClient(context.Background(), clientAddr, plcAddr)
	assert.Nil(t, err)
	defer c.Close()
	c.SetTimeoutMs(10_000) // block so we can observe in-flight

	const N = 5
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			_, _ = c.ReadWords(ctx, AreaDMWord, 0, 1)
		}()
	}

	// Give the goroutines time to register SIDs.
	time.Sleep(50 * time.Millisecond)
	mid := c.Stats()
	assert.Equal(t, N, mid.InFlightRequests,
		"expected %d in-flight during concurrent blocked reads", N)

	wg.Wait()
	final := c.Stats()
	assert.Equal(t, 0, final.InFlightRequests, "all requests should drain after ctx timeouts")
}

// TestStats_AfterClose verifies Stats() is safe to call on a closed client.
func TestStats_AfterClose(t *testing.T) {
	c, _, cleanup := simBatchClient(t)
	cleanup() // closes the client
	assert.NotPanics(t, func() { _ = c.Stats() })
}
