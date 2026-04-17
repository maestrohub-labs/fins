package fins

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
)

// fakeFinsResponder listens on a random UDP port and echoes a FINS response
// with the given end code and data payload for every request it receives,
// copying the request's SID so the client can route the reply.
//
// Useful for exercising corner cases the bundled UDPServerSimulator doesn't
// express — specifically, responses with truncated data sections.
type fakeFinsResponder struct {
	conn    *net.UDPConn
	endCode uint16
	data    []byte
	done    chan struct{}
}

func newFakeFinsResponder(t *testing.T, endCode uint16, data []byte) *fakeFinsResponder {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	assert.Nil(t, err)

	r := &fakeFinsResponder{
		conn:    conn,
		endCode: endCode,
		data:    data,
		done:    make(chan struct{}),
	}

	go func() {
		defer close(r.done)
		buf := make([]byte, 65535)
		for {
			n, remote, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if n < 12 {
				continue
			}
			sid := buf[9]
			// Response layout: 10-byte header, 2 command-code bytes, 2 end-code
			// bytes, then payload. We preserve the SID (byte 9) and set the
			// ICF response bit (bit 6 on byte 0).
			resp := make([]byte, 14+len(data))
			resp[0] = 0xC0
			resp[2] = 0x02
			resp[9] = sid
			resp[10] = 0x01 // CommandCodeMemoryAreaRead high byte
			resp[11] = 0x01 // low byte
			resp[12] = byte(endCode >> 8)
			resp[13] = byte(endCode)
			copy(resp[14:], data)
			_, _ = conn.WriteToUDP(resp, remote)
		}
	}()
	return r
}

func (r *fakeFinsResponder) Addr() *net.UDPAddr { return r.conn.LocalAddr().(*net.UDPAddr) }

func (r *fakeFinsResponder) Close() {
	_ = r.conn.Close()
	<-r.done
}

// TestReadBits_TruncatedResponse_NoPanic drives ReadBits against a fake
// PLC that returns a FINS response with zero data bytes regardless of the
// requested count. Before the length guard was added, this panicked with
// "index out of range [0] with length 0". After the fix, the call must
// return a ResponseLengthError without touching r.data.
func TestReadBits_TruncatedResponse_NoPanic(t *testing.T) {
	srv := newFakeFinsResponder(t, EndCodeNormalCompletion, nil) // 0 data bytes
	defer srv.Close()

	clientAddr := NewUDPAddress("127.0.0.1", 0, 0, 2, 0)
	plcAddr := NewUDPAddress("127.0.0.1", srv.Addr().Port, 0, 10, 0)
	c, err := NewUDPClient(context.Background(), clientAddr, plcAddr)
	assert.Nil(t, err)
	defer c.Close()
	c.SetTimeoutMs(500)

	assert.NotPanics(t, func() {
		bits, err := c.ReadBits(context.Background(), AreaDMBit, 0, 0, 10)
		assert.Nil(t, bits)
		var lenErr ResponseLengthError
		assert.True(t, errors.As(err, &lenErr),
			"expected ResponseLengthError, got %v", err)
	})
}

// TestReadBits_ShortResponse_NoPanic covers the "short but non-zero" case —
// requested 10 bits, PLC returns 3. Must return ResponseLengthError rather
// than silently returning partial data.
func TestReadBits_ShortResponse_NoPanic(t *testing.T) {
	srv := newFakeFinsResponder(t, EndCodeNormalCompletion, []byte{1, 0, 1})
	defer srv.Close()

	clientAddr := NewUDPAddress("127.0.0.1", 0, 0, 2, 0)
	plcAddr := NewUDPAddress("127.0.0.1", srv.Addr().Port, 0, 10, 0)
	c, err := NewUDPClient(context.Background(), clientAddr, plcAddr)
	assert.Nil(t, err)
	defer c.Close()
	c.SetTimeoutMs(500)

	assert.NotPanics(t, func() {
		bits, err := c.ReadBits(context.Background(), AreaDMBit, 0, 0, 10)
		assert.Nil(t, bits)
		var lenErr ResponseLengthError
		assert.True(t, errors.As(err, &lenErr),
			"expected ResponseLengthError for 3-byte response to 10-bit request, got %v", err)
	})
}

// TestReadBits_IgnoredEndCode_TruncatedResponse_NoPanic covers the subtle
// reachability path: the client is configured to ignore a particular end
// code. The PLC returns that code *with no data*. checkResponse accepts
// the ignored code, so readBits proceeds into the data loop — which must
// still be guarded against the short payload.
func TestReadBits_IgnoredEndCode_TruncatedResponse_NoPanic(t *testing.T) {
	const ignoredCode uint16 = 0x1003 // EndCodeElementsDataDontMatch
	srv := newFakeFinsResponder(t, ignoredCode, nil)
	defer srv.Close()

	clientAddr := NewUDPAddress("127.0.0.1", 0, 0, 2, 0)
	plcAddr := NewUDPAddress("127.0.0.1", srv.Addr().Port, 0, 10, 0)
	c, err := NewUDPClient(context.Background(), clientAddr, plcAddr)
	assert.Nil(t, err)
	defer c.Close()
	c.SetTimeoutMs(500)
	c.SetIgnoreErrorCodes([]uint16{ignoredCode})

	assert.NotPanics(t, func() {
		bits, err := c.ReadBits(context.Background(), AreaDMBit, 0, 0, 5)
		assert.Nil(t, bits)
		var lenErr ResponseLengthError
		assert.True(t, errors.As(err, &lenErr),
			"expected ResponseLengthError even for ignored end code with truncated data, got %v", err)
	})
}
