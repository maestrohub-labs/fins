package fins

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestFinsClient(t *testing.T) {
	clientAddr := NewUDPAddress("", 9602, 0, 2, 0)
	plcAddr := NewUDPAddress("", 9607, 0, 10, 0)

	c, e := NewUDPClient(context.Background(), clientAddr, plcAddr)
	assert.Nil(t, e)

	for i := 0; i < 10; i++ {
		fmt.Println(i)
		test1(t, c)
		time.Sleep(time.Second)
	}
}
func TestFinsClientRace(t *testing.T) {
	clientAddr := NewUDPAddress("", 9602, 0, 2, 0)
	plcAddr := NewUDPAddress("", 9600, 0, 10, 0)

	c, e := NewUDPClient(context.Background(), clientAddr, plcAddr)
	assert.Nil(t, e)

	for i := 0; i < 10; i++ {
		raceTest(c, i > 5)
	}
}

func test1(t *testing.T, c *UDPClient) {
	defer c.Close()
	ctx := context.Background()
	s, e := NewUDPServerSimulator(c.plcAddr)
	assert.Nil(t, e)
	defer func() {
		s.Close()
		<-s.Done()
	}()

	toWrite := []uint16{5, 4, 3, 2, 1}

	// ------------- Test Words
	err := c.WriteWords(ctx, MemoryAreaDMWord, 100, toWrite)
	assert.Nil(t, err)

	vals, err := c.ReadWords(ctx, MemoryAreaDMWord, 100, 5)
	assert.Nil(t, err)
	assert.Equal(t, toWrite, vals)

	// test setting response timeout
	c.SetTimeoutMs(50)
	_, err = c.ReadWords(ctx, MemoryAreaDMWord, 100, 5)
	assert.Nil(t, err)

	// ------------- Test Strings
	err = c.WriteString(ctx, MemoryAreaDMWord, 10, "ф1234")
	assert.Nil(t, err)

	v, err := c.ReadString(ctx, MemoryAreaDMWord, 12, 1)
	assert.Nil(t, err)
	assert.Equal(t, "12", v)

	v, err = c.ReadString(ctx, MemoryAreaDMWord, 10, 3)
	assert.Nil(t, err)
	assert.Equal(t, "ф1234", v)

	v, err = c.ReadString(ctx, MemoryAreaDMWord, 10, 5)
	assert.Nil(t, err)
	assert.Equal(t, "ф1234", v)

	// ------------- Test Bytes
	err = c.WriteBytes(ctx, MemoryAreaDMWord, 10, []byte{0x00, 0x00, 0xC1, 0xA0})
	assert.Nil(t, err)

	b, err := c.ReadBytes(ctx, MemoryAreaDMWord, 10, 2)
	assert.Nil(t, err)
	assert.Equal(t, []byte{0x00, 0x00, 0xC1, 0xA0}, b)

	buf := make([]byte, 8, 8)
	binary.LittleEndian.PutUint64(buf[:], math.Float64bits(-20))
	err = c.WriteBytes(ctx, MemoryAreaDMWord, 10, buf)
	assert.Nil(t, err)

	b, err = c.ReadBytes(ctx, MemoryAreaDMWord, 10, 4)
	assert.Nil(t, err)
	assert.Equal(t, []byte{0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x34, 0xc0}, b)

	// ------------- Test Bits
	err = c.WriteBits(ctx, MemoryAreaDMBit, 10, 2, []bool{true, false, true})
	assert.Nil(t, err)

	bs, err := c.ReadBits(ctx, MemoryAreaDMBit, 10, 2, 3)
	assert.Nil(t, err)
	assert.Equal(t, []bool{true, false, true}, bs)

	bs, err = c.ReadBits(ctx, MemoryAreaDMBit, 10, 1, 5)
	assert.Nil(t, err)
	assert.Equal(t, []bool{false, true, false, true, false}, bs)
}

func raceTest(c *UDPClient, testClose bool) {
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(ii int) {
			defer wg.Done()
			words, err := c.ReadWords(context.Background(), MemoryAreaDMWord, 100, 10)
			if err != nil {
				fmt.Println(ii, err.Error())
			} else {
				fmt.Println(ii, "read success", words)
			}
		}(i)
	}
	if testClose {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Close()
		}()
	}
	wg.Wait()
}

func TestClient_bytesToUint16s(t *testing.T) {
	var c UDPClient
	v := []uint16{24, 567}
	assert.Equal(t, v, c.bytesToUint16s(c.uint16sToBytes(v)))
}

func Test_atomicValue(t *testing.T) {
	var v atomic.Value
	m, ok := v.Load().(map[uint16]int)
	assert.Equal(t, false, ok)
	assert.Equal(t, 0, m[1])
}

// newBlackholeClient builds a client that targets a UDP port with nothing
// listening on it. Writes succeed; responses never arrive — so every read
// blocks until its context or the response timeout fires.
func newBlackholeClient(t *testing.T) *UDPClient {
	t.Helper()
	clientAddr := NewUDPAddress("127.0.0.1", 0, 0, 2, 0)
	// Port 1 is reserved/rarely-listened; the test host firewall or OS drops
	// any incoming packet. We use a per-test-randomised high port instead to
	// avoid relying on privileged-port behaviour.
	port := 40000 + rand.Intn(20000)
	plcAddr := NewUDPAddress("127.0.0.1", port, 0, 10, 0)
	c, err := NewUDPClient(context.Background(), clientAddr, plcAddr)
	assert.Nil(t, err)
	// Keep the library-default fallback high so only the ctx governs the test.
	c.SetTimeoutMs(10_000)
	return c
}

func TestReadWords_AlreadyCancelledContext(t *testing.T) {
	c := newBlackholeClient(t)
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	_, err := c.ReadWords(ctx, MemoryAreaDMWord, 0, 1)
	elapsed := time.Since(start)

	assert.True(t, errors.Is(err, context.Canceled), "expected context.Canceled, got %v", err)
	assert.Less(t, elapsed, 50*time.Millisecond, "pre-cancelled ctx should short-circuit fast")
}

func TestReadWords_ContextCancellation(t *testing.T) {
	c := newBlackholeClient(t)
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := c.ReadWords(ctx, MemoryAreaDMWord, 0, 1)
	elapsed := time.Since(start)

	assert.True(t, errors.Is(err, context.Canceled), "expected context.Canceled, got %v", err)
	assert.GreaterOrEqual(t, elapsed, 40*time.Millisecond, "should not return before cancel fires")
	assert.Less(t, elapsed, 250*time.Millisecond, "should return within 200ms of cancel")
}

func TestReadWords_ContextDeadline(t *testing.T) {
	c := newBlackholeClient(t)
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := c.ReadWords(ctx, MemoryAreaDMWord, 0, 1)
	elapsed := time.Since(start)

	assert.True(t, errors.Is(err, context.DeadlineExceeded), "expected context.DeadlineExceeded, got %v", err)
	assert.GreaterOrEqual(t, elapsed, 40*time.Millisecond)
	assert.Less(t, elapsed, 250*time.Millisecond)
}

func TestConcurrentContexts(t *testing.T) {
	// 32 concurrent reads against a blackhole, half cancelled at random times;
	// the surviving half must complete (with ResponseTimeoutError or ctx error).
	// After all finish, the SID map must have no leaked entries.
	c := newBlackholeClient(t)
	defer c.Close()
	// Keep fallback short so the non-cancelled half also terminates.
	c.SetTimeoutMs(200)

	const N = 32
	var wg sync.WaitGroup
	wg.Add(N)

	cancellers := make([]context.CancelFunc, N)
	ctxs := make([]context.Context, N)
	for i := 0; i < N; i++ {
		ctxs[i], cancellers[i] = context.WithCancel(context.Background())
	}

	var panics atomic.Int32
	for i := 0; i < N; i++ {
		go func(idx int) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					panics.Add(1)
				}
			}()
			_, _ = c.ReadWords(ctxs[idx], MemoryAreaDMWord, 0, 1)
		}(i)
	}
	// Cancel half at random times between 10–80ms.
	for i := 0; i < N/2; i++ {
		idx := i
		go func() {
			time.Sleep(time.Duration(10+rand.Intn(70)) * time.Millisecond)
			cancellers[idx]()
		}()
	}

	wg.Wait()

	// No panics.
	assert.Equal(t, int32(0), panics.Load(), "no goroutine should panic")

	// Cleanup the rest.
	for i := N / 2; i < N; i++ {
		cancellers[i]()
	}

	// Verify no SID leaks: after a short settle, all 256 SID slots should be nil.
	time.Sleep(50 * time.Millisecond)
	c.resp.m.Lock()
	leaked := 0
	for i := range c.resp.rs {
		if c.resp.rs[i] != nil {
			leaked++
		}
	}
	c.resp.m.Unlock()
	assert.Equal(t, 0, leaked, "no SID slot should be leaked after reads return")
}

// clientOp describes one public method invocation. Used by the table-driven
// tests below to assert every public method honours ctx cancellation, not
// only ReadWords.
type clientOp struct {
	name string
	do   func(ctx context.Context, c *UDPClient) error
}

var allPublicOps = []clientOp{
	{"ReadWords", func(ctx context.Context, c *UDPClient) error {
		_, err := c.ReadWords(ctx, MemoryAreaDMWord, 0, 1)
		return err
	}},
	{"ReadBytes", func(ctx context.Context, c *UDPClient) error {
		_, err := c.ReadBytes(ctx, MemoryAreaDMWord, 0, 1)
		return err
	}},
	{"ReadBits", func(ctx context.Context, c *UDPClient) error {
		_, err := c.ReadBits(ctx, MemoryAreaDMBit, 0, 0, 1)
		return err
	}},
	{"ReadString", func(ctx context.Context, c *UDPClient) error {
		_, err := c.ReadString(ctx, MemoryAreaDMWord, 0, 1)
		return err
	}},
	{"ReadClock", func(ctx context.Context, c *UDPClient) error {
		_, err := c.ReadClock(ctx)
		return err
	}},
	{"WriteWords", func(ctx context.Context, c *UDPClient) error {
		return c.WriteWords(ctx, MemoryAreaDMWord, 0, []uint16{1})
	}},
	{"WriteBytes", func(ctx context.Context, c *UDPClient) error {
		return c.WriteBytes(ctx, MemoryAreaDMWord, 0, []byte{0, 1})
	}},
	{"WriteString", func(ctx context.Context, c *UDPClient) error {
		return c.WriteString(ctx, MemoryAreaDMWord, 0, "xy")
	}},
	{"WriteBits", func(ctx context.Context, c *UDPClient) error {
		return c.WriteBits(ctx, MemoryAreaDMBit, 0, 0, []bool{true})
	}},
	{"SetBit", func(ctx context.Context, c *UDPClient) error {
		return c.SetBit(ctx, MemoryAreaDMBit, 0, 0)
	}},
	{"ResetBit", func(ctx context.Context, c *UDPClient) error {
		return c.ResetBit(ctx, MemoryAreaDMBit, 0, 0)
	}},
	{"ToggleBit", func(ctx context.Context, c *UDPClient) error {
		return c.ToggleBit(ctx, MemoryAreaDMBit, 0, 0)
	}},
}

// TestAllMethods_PreCancelledContext asserts every public method short-circuits
// promptly when handed a context that is already cancelled — no UDP write is
// issued and ctx.Err() is returned.
func TestAllMethods_PreCancelledContext(t *testing.T) {
	c := newBlackholeClient(t)
	defer c.Close()

	for _, op := range allPublicOps {
		t.Run(op.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()

			start := time.Now()
			err := op.do(ctx, c)
			elapsed := time.Since(start)

			assert.True(t, errors.Is(err, context.Canceled), "expected context.Canceled, got %v", err)
			assert.Less(t, elapsed, 50*time.Millisecond, "pre-cancelled ctx should short-circuit fast")
		})
	}
}

// TestAllMethods_MidFlightCancellation asserts every public method unblocks
// within ~200ms of a mid-flight cancel — meaning the library-default timeout
// (set high above) is not what's returning the call; the ctx is.
func TestAllMethods_MidFlightCancellation(t *testing.T) {
	c := newBlackholeClient(t)
	defer c.Close()

	for _, op := range allPublicOps {
		t.Run(op.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			go func() {
				time.Sleep(50 * time.Millisecond)
				cancel()
			}()

			start := time.Now()
			err := op.do(ctx, c)
			elapsed := time.Since(start)

			assert.True(t, errors.Is(err, context.Canceled), "expected context.Canceled, got %v", err)
			assert.GreaterOrEqual(t, elapsed, 40*time.Millisecond, "should not return before cancel fires")
			assert.Less(t, elapsed, 250*time.Millisecond, "should return within 200ms of cancel")
		})
	}
}

// TestAllMethods_ContextDeadline asserts every public method honours a
// context deadline (distinct from explicit cancellation — returns
// context.DeadlineExceeded instead of context.Canceled).
func TestAllMethods_ContextDeadline(t *testing.T) {
	c := newBlackholeClient(t)
	defer c.Close()

	for _, op := range allPublicOps {
		t.Run(op.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()

			start := time.Now()
			err := op.do(ctx, c)
			elapsed := time.Since(start)

			assert.True(t, errors.Is(err, context.DeadlineExceeded), "expected context.DeadlineExceeded, got %v", err)
			assert.GreaterOrEqual(t, elapsed, 40*time.Millisecond)
			assert.Less(t, elapsed, 250*time.Millisecond)
		})
	}
}

func TestNewUDPClient_ParentContextCancellation(t *testing.T) {
	// Cancelling the parent ctx passed to NewUDPClient should unblock
	// in-flight reads the same way Close() does.
	parentCtx, parentCancel := context.WithCancel(context.Background())
	clientAddr := NewUDPAddress("127.0.0.1", 0, 0, 2, 0)
	port := 40000 + rand.Intn(20000)
	plcAddr := NewUDPAddress("127.0.0.1", port, 0, 10, 0)
	c, err := NewUDPClient(parentCtx, clientAddr, plcAddr)
	assert.Nil(t, err)
	c.SetTimeoutMs(10_000)

	// Fire a read that would otherwise block for 10s.
	var readErr error
	done := make(chan struct{})
	go func() {
		_, readErr = c.ReadWords(context.Background(), MemoryAreaDMWord, 0, 1)
		close(done)
	}()

	// Let the read kick in and register a pending SID.
	time.Sleep(30 * time.Millisecond)
	parentCancel()

	select {
	case <-done:
		// The read should end with a ClientClosedError (parentCtx cancellation
		// propagates to c.ctx, same code path as Close).
		assert.Error(t, readErr)
	case <-time.After(500 * time.Millisecond):
		buf := make([]byte, 1<<16)
		n := runtime.Stack(buf, true)
		t.Fatalf("read did not unblock within 500ms after parent ctx cancel; stacks:\n%s", buf[:n])
	}
	c.Close()
}
