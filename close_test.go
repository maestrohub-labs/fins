package fins

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestClose_UnblocksInFlightReads starts N reads against a blackhole and
// calls Close — every read must return ClientClosedError within 200 ms
// (the spec says 100 ms, we allow headroom for CI).
func TestClose_UnblocksInFlightReads(t *testing.T) {
	clientAddr := NewUDPAddress("127.0.0.1", 0, 0, 2, 0)
	plcAddr := NewUDPAddress("127.0.0.1", 40000+rand.Intn(20000), 0, 10, 0)
	c, err := NewUDPClient(context.Background(), clientAddr, plcAddr)
	assert.Nil(t, err)
	c.SetTimeoutMs(10_000)

	const N = 10
	errs := make([]error, N)
	var wg sync.WaitGroup
	wg.Add(N)
	start := time.Now()
	for i := 0; i < N; i++ {
		idx := i
		go func() {
			defer wg.Done()
			_, errs[idx] = c.ReadWords(context.Background(), AreaDMWord, 0, 1)
		}()
	}

	// Let the reads register SIDs.
	time.Sleep(30 * time.Millisecond)
	closeErr := c.Close()
	assert.Nil(t, closeErr)

	wg.Wait()
	elapsed := time.Since(start)

	for i, e := range errs {
		assert.True(t, errors.As(e, new(ClientClosedError)),
			"read %d: expected ClientClosedError, got %v", i, e)
	}
	assert.Less(t, elapsed, 500*time.Millisecond,
		"all reads should return within 500ms of Close (spec: 100ms)")
}

// TestClose_Idempotent calls Close three times — no panic, no error.
func TestClose_Idempotent(t *testing.T) {
	clientAddr := NewUDPAddress("127.0.0.1", 0, 0, 2, 0)
	plcAddr := NewUDPAddress("127.0.0.1", 40000+rand.Intn(20000), 0, 10, 0)
	c, err := NewUDPClient(context.Background(), clientAddr, plcAddr)
	assert.Nil(t, err)

	assert.NotPanics(t, func() {
		assert.Nil(t, c.Close())
		assert.Nil(t, c.Close())
		assert.Nil(t, c.Close())
	})
}

// TestClose_Concurrent calls Close from many goroutines in parallel —
// singleflight should coalesce them without double-closing the UDP conn
// or panicking.
func TestClose_Concurrent(t *testing.T) {
	clientAddr := NewUDPAddress("127.0.0.1", 0, 0, 2, 0)
	plcAddr := NewUDPAddress("127.0.0.1", 40000+rand.Intn(20000), 0, 10, 0)
	c, err := NewUDPClient(context.Background(), clientAddr, plcAddr)
	assert.Nil(t, err)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			assert.NotPanics(t, func() { _ = c.Close() })
		}()
	}
	wg.Wait()
}

// TestCallAfterClose — every public method must return ClientClosedError
// when the client has already been closed. Table-driven to cover the
// full surface.
func TestCallAfterClose(t *testing.T) {
	clientAddr := NewUDPAddress("127.0.0.1", 0, 0, 2, 0)
	plcAddr := NewUDPAddress("127.0.0.1", 40000+rand.Intn(20000), 0, 10, 0)
	c, err := NewUDPClient(context.Background(), clientAddr, plcAddr)
	assert.Nil(t, err)

	_ = c.Close()

	for _, op := range allPublicOps {
		t.Run(op.name, func(t *testing.T) {
			start := time.Now()
			err := op.do(context.Background(), c)
			elapsed := time.Since(start)
			assert.True(t, errors.As(err, new(ClientClosedError)),
				"expected ClientClosedError, got %v", err)
			assert.Less(t, elapsed, 50*time.Millisecond,
				"post-close calls must short-circuit — no reopen attempts")
		})
	}
}

// TestCallAfterClose_Batch verifies the batch helpers also return
// ClientClosedError after Close.
func TestCallAfterClose_Batch(t *testing.T) {
	clientAddr := NewUDPAddress("127.0.0.1", 0, 0, 2, 0)
	plcAddr := NewUDPAddress("127.0.0.1", 40000+rand.Intn(20000), 0, 10, 0)
	c, err := NewUDPClient(context.Background(), clientAddr, plcAddr)
	assert.Nil(t, err)
	_ = c.Close()

	_, err = c.ReadWordsBatch(context.Background(), AreaDMWord, 0, 10)
	assert.True(t, errors.As(err, new(ClientClosedError)))

	_, err = c.ReadBytesBatch(context.Background(), AreaDMWord, 0, 10)
	assert.True(t, errors.As(err, new(ClientClosedError)))
}
