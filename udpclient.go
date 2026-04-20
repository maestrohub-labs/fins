package fins

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultResponseTimeoutMillisecond uint = 20 // ms
)

// UDPClient Omron FINS client
// this is concurrent safe
type UDPClient struct {
	localAddr UDPAddress
	plcAddr   UDPAddress
	// config
	responseTimeout  atomic.Int64
	byteOrder        atomic.Pointer[binary.ByteOrder]
	readGoroutineNum atomic.Int32

	commLogger

	sid  atomicByte
	resp syncRespSlice

	sf      singleflightOne // avoid Close call twice
	closing atomic.Bool
	closed  atomic.Bool // latched true by Close; never reset
	wg      sync.WaitGroup

	// parentCtx is the context passed to NewUDPClient. It governs the lifetime
	// of the client: when parentCtx is cancelled, the internal per-connection
	// ctx (c.ctx) is cancelled too, unblocking the read loop and any in-flight
	// requests.
	parentCtx context.Context

	m      sync.Mutex
	conn   *net.UDPConn
	ctx    context.Context
	cancel context.CancelFunc

	ignoreErrorCode atomic.Value // map[uint16]struct{}{}

	// lightweight telemetry — see Stats().
	inFlightRequests atomic.Int64
	lifetimeRequests atomic.Uint64
	lifetimeTimeouts atomic.Uint64
}

// Stats is a snapshot of client-level counters intended for export to
// a metrics system.
type Stats struct {
	// InFlightRequests is the number of outstanding sendCommand calls
	// at the time Stats() was taken.
	InFlightRequests int
	// LifetimeRequests counts every sendCommand invocation since the
	// client was created (both successful and failed).
	LifetimeRequests uint64
	// LifetimeTimeouts counts every sendCommand that returned
	// ResponseTimeoutError since the client was created.
	LifetimeTimeouts uint64
}

// Stats returns a snapshot of the client's lightweight telemetry. Cheap
// to call — reads three atomic counters. Safe to call concurrently and
// after Close().
func (c *UDPClient) Stats() Stats {
	return Stats{
		InFlightRequests: int(c.inFlightRequests.Load()),
		LifetimeRequests: c.lifetimeRequests.Load(),
		LifetimeTimeouts: c.lifetimeTimeouts.Load(),
	}
}

// NewUDPClient creates a new Omron FINS client.
//
// The provided ctx governs the lifetime of the client. Cancelling ctx
// cancels the internal long-lived context (same effect as calling Close),
// which tears down the UDP read loop and causes in-flight requests to
// return. If ctx is nil, context.Background() is used.
func NewUDPClient(ctx context.Context, localAddr, plcAddr UDPAddress) (*UDPClient, error) {
	if plcAddr.udpAddress == nil {
		return nil, &net.OpError{Op: "dial", Net: "udp", Err: errors.New("missing address")}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	c := &UDPClient{
		localAddr: localAddr,
		plcAddr:   plcAddr,
		parentCtx: ctx,
	}
	c.SetTimeoutMs(defaultResponseTimeoutMillisecond)
	c.SetReadPacketErrorLogger(&stdoutLogger{})
	c.SetByteOrder(binary.BigEndian)
	c.SetReadGoroutineNum(8)

	c.setConnAndCtx(nil)
	return c, nil
}

// ReadWords Reads words from the PLC data area
func (c *UDPClient) ReadWords(ctx context.Context, ma MemoryArea, address uint16, readCount uint16) ([]uint16, error) {
	readBytes, err := c.ReadBytes(ctx, ma, address, readCount)
	if err != nil {
		return nil, err
	}
	return c.bytesToUint16s(readBytes), nil
}

// ReadBytes Reads bytes from the PLC data area
// note: readCount is count of uint16, not count of byte, so len(return) is 2*readCount
func (c *UDPClient) ReadBytes(ctx context.Context, ma MemoryArea, address uint16, readCount uint16) ([]byte, error) {
	return wrapRead(ctx, c, func() ([]byte, error) {
		return c.readBytes(ctx, ma, address, readCount)
	})
}

// ReadString Reads a string from the PLC data area
// note: readCount is count of uint16, not len of string or count of byte
func (c *UDPClient) ReadString(ctx context.Context, ma MemoryArea, address uint16, readCount uint16) (string, error) {
	data, err := c.ReadBytes(ctx, ma, address, readCount)
	if err != nil {
		return "", err
	}
	n := bytes.IndexByte(data, 0)
	if n != -1 {
		data = data[:n]
	}
	return string(data), nil
}

// ReadBits Reads bits from the PLC data area
// note: readCount is count of bool, so len(return) is readCount
func (c *UDPClient) ReadBits(ctx context.Context, ma MemoryArea, address uint16, bitOffset byte, readCount uint16) ([]bool, error) {
	return wrapRead(ctx, c, func() ([]bool, error) {
		return c.readBits(ctx, ma, address, bitOffset, readCount)
	})
}

// MaxWordsPerFrame is the largest word count that fits in a single FINS
// request frame. ReadWordsBatch / ReadBytesBatch chunk requests larger
// than this value.
const MaxWordsPerFrame = 999

// ReadWordsBatch reads `count` contiguous words starting at `addr`, chunking
// across multiple FINS frames when count exceeds MaxWordsPerFrame.
//
// Chunks are issued sequentially (one at a time) to avoid SID exhaustion
// under concurrent callers. If the caller's context is cancelled partway
// through, already-read chunks are discarded and ctx.Err() is returned.
func (c *UDPClient) ReadWordsBatch(ctx context.Context, ma MemoryArea, addr uint16, count int) ([]uint16, error) {
	if count < 0 {
		return nil, fmt.Errorf("ReadWordsBatch: count %d must be non-negative", count)
	}
	if count == 0 {
		return []uint16{}, nil
	}
	if int(addr)+count > 1<<16 {
		return nil, fmt.Errorf("ReadWordsBatch: addr %d + count %d overflows the 16-bit address space", addr, count)
	}

	result := make([]uint16, 0, count)
	offset := addr
	remaining := count
	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		chunk := uint16(MaxWordsPerFrame)
		if remaining < MaxWordsPerFrame {
			chunk = uint16(remaining)
		}
		words, err := c.ReadWords(ctx, ma, offset, chunk)
		if err != nil {
			return nil, err
		}
		result = append(result, words...)
		offset += chunk
		remaining -= int(chunk)
	}
	return result, nil
}

// ReadBytesBatch is the byte-returning analogue of ReadWordsBatch.
// count is a count of 16-bit words — matching ReadBytes semantics — so the
// returned slice has length 2*count.
func (c *UDPClient) ReadBytesBatch(ctx context.Context, ma MemoryArea, addr uint16, count int) ([]byte, error) {
	if count < 0 {
		return nil, fmt.Errorf("ReadBytesBatch: count %d must be non-negative", count)
	}
	if count == 0 {
		return []byte{}, nil
	}
	if int(addr)+count > 1<<16 {
		return nil, fmt.Errorf("ReadBytesBatch: addr %d + count %d overflows the 16-bit address space", addr, count)
	}

	result := make([]byte, 0, 2*count)
	offset := addr
	remaining := count
	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		chunk := uint16(MaxWordsPerFrame)
		if remaining < MaxWordsPerFrame {
			chunk = uint16(remaining)
		}
		b, err := c.ReadBytes(ctx, ma, offset, chunk)
		if err != nil {
			return nil, err
		}
		result = append(result, b...)
		offset += chunk
		remaining -= int(chunk)
	}
	return result, nil
}

// ReadClock Reads the PLC clock
func (c *UDPClient) ReadClock(ctx context.Context) (t *time.Time, err error) {
	return wrapRead(ctx, c, func() (*time.Time, error) {
		r, e := c.sendCommandAndCheckResponse(ctx, clockReadCommand())
		if e != nil {
			return nil, e
		}
		return decodeClock(r.data)
	})
}

// WriteWords Writes words to the PLC data area
func (c *UDPClient) WriteWords(ctx context.Context, ma MemoryArea, address uint16, data []uint16) error {
	return c.WriteBytes(ctx, ma, address, c.uint16sToBytes(data))
}

// WriteBytes Writes bytes array to the PLC data area
// Example:
//
//	WriteBytes(A, 100, []byte{0x01}) will set A100=256  [01 00]
//	WriteBytes(A, 100, []byte{0x01,0x02}) will set A100=256+2 [01 01]
//	WriteBytes(A, 100, []byte{0x01,0x02,0x01}) will set A100=256+2, A101=256  [01 01 01 00]
//
// Warning:
//
//	if len(b) is not even, I append 0 to the end of b, cause low byte of last memory will be set to 0
//	 A200=1(0x00 0x01), call WriteBytes(A, 100, []byte{0x01}), A200 will be 256(0x01,0x00)
func (c *UDPClient) WriteBytes(ctx context.Context, ma MemoryArea, address uint16, b []byte) error {
	if len(b) == 0 {
		return EmptyWriteRequestError{}
	}
	if len(b)%2 != 0 {
		b = append(b, 0)
	}
	return c.wrapOperate(ctx, func() error {
		if err := checkIsWordArea(ma); err != nil {
			return err
		}
		c.logger().Debug("fins write bytes",
			slog.String("area", ma.Name),
			slog.Int("addr", int(address)),
			slog.Int("bytes", len(b)),
		)
		command := writeCommand(memAddr(ma.Code, address), uint16(len(b)/2), b)
		return c.checkResponse(c.sendCommand(ctx, command))
	})
}

// WriteString Writes a string to the PLC data area
// Example:
//
//	WriteString(A, 100, "12") will set A100=[0x31,0x32]
//	WriteString(A, 100, "1") will set A100=[0x31,0x00]
//
// Warning:
//
//	same as WriteBytes, if len([]byte(s)) is not even, I append 0 to the end of b, cause low byte of last memory will be set to 0
func (c *UDPClient) WriteString(ctx context.Context, ma MemoryArea, address uint16, s string) error {
	return c.WriteBytes(ctx, ma, address, []byte(s))
}

// WriteBits Writes bits to the PLC data area
// Example:
//
//	WriteBits(A, 100, 0, []bool{true,true}) will set A100=256  [00 03]
//	WriteBits(A, 100, 0, []bool{true,true,true,true,true,true,true,true,true,true,true,true,true,true,true,true,true}) will set A100=65535,A101=1  [FF FF 00 01]
func (c *UDPClient) WriteBits(ctx context.Context, ma MemoryArea, address uint16, bitOffset byte, data []bool) error {
	return c.wrapOperate(ctx, func() error {
		if err := checkIsBitArea(ma); err != nil {
			return err
		}
		c.logger().Debug("fins write bits",
			slog.String("area", ma.Name),
			slog.Int("addr", int(address)),
			slog.Int("bitOffset", int(bitOffset)),
			slog.Int("count", len(data)),
		)
		l := uint16(len(data))
		bts := make([]byte, 0, l)
		for i := 0; i < int(l); i++ {
			var d byte
			if data[i] {
				d = 0x01
			}
			bts = append(bts, d)
		}
		command := writeCommand(memAddrWithBitOffset(ma.Code, address, bitOffset), l, bts)

		return c.checkResponse(c.sendCommand(ctx, command))
	})
}

// SetBit Sets a bit in the PLC data area
func (c *UDPClient) SetBit(ctx context.Context, ma MemoryArea, address uint16, bitOffset byte) error {
	return c.bitTwiddle(ctx, ma, address, bitOffset, 0x01)
}

// ResetBit Resets a bit in the PLC data area
// Example:
//
//	ResetBit(A, 100, 0) will set A100.0=0  [00 01] -> [00 00]
//	ResetBit(A, 100, 16) will set A101.0=0  [00 00 00 01] -> [00 00 00 00]
func (c *UDPClient) ResetBit(ctx context.Context, ma MemoryArea, address uint16, bitOffset byte) error {
	return c.bitTwiddle(ctx, ma, address, bitOffset, 0x00)
}

// ToggleBit Toggles a bit in the PLC data area.
//
// Implemented as a client-side read-modify-write: reads the current value
// then writes the inverse. Not atomic w.r.t. concurrent writers — if the
// same bit might be written by another party, coordinate externally.
func (c *UDPClient) ToggleBit(ctx context.Context, ma MemoryArea, address uint16, bitOffset byte) error {
	return c.wrapOperate(ctx, func() error {
		b, err := c.readBits(ctx, ma, address, bitOffset, 1)
		if err != nil {
			return err
		}
		t := byte(0x01)
		if b[0] {
			t = 0x00
		}
		return c._bitTwiddle(ctx, ma, address, bitOffset, t)
	})
}

// SetByteOrder
// Set byte order
// Default value: binary.BigEndian
//
// Safe to call repeatedly with any binary.ByteOrder implementation
// (BigEndian, LittleEndian, or a custom one). Concurrent callers
// observe updates atomically.
func (c *UDPClient) SetByteOrder(o binary.ByteOrder) {
	if o != nil {
		c.byteOrder.Store(&o)
	}
}

// SetTimeoutMs
// Set response timeout duration (ms). This is the fallback timeout applied
// when the caller's context has no deadline. When the caller provides a
// context with a deadline, the tighter of the two applies.
// Default value: 20ms.
// A timeout of zero can be used to block indefinitely (still abortable via ctx).
func (c *UDPClient) SetTimeoutMs(t uint) {
	c.responseTimeout.Store(int64(time.Duration(t) * time.Millisecond))
}

// SetReadGoroutineNum
// Note: won't stop running goroutine
func (c *UDPClient) SetReadGoroutineNum(count uint8) {
	if count > 0 {
		c.readGoroutineNum.Store(int32(count))
	}
}

// Close shuts the Omron FINS client down. In-flight Read*/Write* calls
// unblock and return ClientClosedError. Subsequent calls on this client
// also return ClientClosedError — Close is terminal, not a transient
// disconnect. Close itself is idempotent: safe to call concurrently and
// repeatedly. Always returns nil today; the error return is reserved
// for future transport variants.
func (c *UDPClient) Close() error {
	c.sf.do(c.wrapClose)
	return nil
}

// SetIgnoreErrorCodes
// Set ignore error codes
func (c *UDPClient) SetIgnoreErrorCodes(codes []uint16) {
	mp := map[uint16]struct{}{}
	for _, code := range codes {
		mp[code] = struct{}{}
	}
	c.ignoreErrorCode.Store(mp)
}

// ============== private ==============
func (c *UDPClient) initConnAndStartReadLoop() error {
	if c.closed.Load() {
		return ClientClosedError{}
	}
	if c.closing.Load() {
		return ClientClosingError{}
	}
	c.m.Lock()
	defer c.m.Unlock()
	if c.conn != nil {
		return nil
	}
	conn, er := net.DialUDP("udp", c.localAddr.udpAddress, c.plcAddr.udpAddress)
	if er != nil {
		return er
	}
	if conn == nil {
		return &net.OpError{Op: "dial", Net: "udp", Err: errors.New("dail return nil conn and nil error")}
	}
	c.setConnAndCtx(conn)

	rn := int(c.readGoroutineNum.Load())
	c.wg.Add(rn)
	for i := 0; i < rn; i++ {
		go func() {
			defer c.wg.Done()
			c.readLoop(c.ctx)
		}()
	}

	return nil
}

func (c *UDPClient) readLoop(ctx context.Context) {
	var buf = make([]byte, udpPacketMaxSize)
	done := ctx.Done()
	for {
		select {
		case <-done:
			return
		default:
			conn := c.getConn()
			if conn == nil { // what happened ?
				c.printFinsPacketError("unknown error: conn is nil in readLoop")
				waitMoment(ctx, time.Millisecond*100)
				continue
			}

			n, _, err := conn.ReadFromUDP(buf)
			if err != nil || n < minResponsePacketSize {
				c.handleReadError(ctx, n, err, buf)
				continue
			}

			respPacket := make([]byte, n)
			copy(respPacket, buf)
			c.printPacket("read", respPacket)
			c.sendToSpecificRespChan(decodeResponse(respPacket))
		}
	}
}

func (c *UDPClient) sendToSpecificRespChan(ans *response) {
	ch := c.resp.getW(ans.header.serviceID)
	if ch == nil {
		c.printFinsPacketError("fins client: no resp chan for sid %d. maybe receive goroutine wait timeout", ans.header.serviceID)
		return
	}

	timeout := time.Duration(c.responseTimeout.Load())
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case ch <- ans:
	case <-c.ctx.Done(): // c.ctx is read only between initConnAndStartReadLoop and Close
		c.printFinsPacketError("fins client: failed to send resp to chan resp. ctx.Done()")
	case <-timer.C:
		c.printFinsPacketError("wait until timeout %s. still no goroutine to receive resp", timeout)
	}
}
func (c *UDPClient) handleReadError(ctx context.Context, n int, err error, buf []byte) {
	if errors.Is(err, net.ErrClosed) {
		return
	}
	msg := fmt.Sprintf("fins client: failed to read fins response packet from: %s: ", c.plcAddr.udpAddress)
	if n < minResponsePacketSize && n > 0 {
		c.printFinsPacketError(msg+"MinResponsePacketSize is %d bytes, got %d bytes: % X", minResponsePacketSize, n, buf[:n])
	} else if n <= 0 {
		c.printFinsPacketError(msg+"ReadFromUDP return %d", n)
	}
	if err != nil {
		c.printFinsPacketError("fins client: failed to ReadFromUDP: %s", err.Error())
	}
	waitMoment(ctx, time.Millisecond*100)
}

func (c *UDPClient) createRequest(command []byte) (byte, []byte) {
	sid := c.sid.increment()
	header := defaultCommandHeader(c.localAddr.deviceAddress, c.plcAddr.deviceAddress, sid)
	bts := encodeHeader(header)
	bts = append(bts, command...)
	return sid, bts
}

func (c *UDPClient) sendCommand(ctx context.Context, command []byte) (*response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	conn := c.getConn()
	if conn == nil {
		return nil, ClientClosedError{}
	}

	c.lifetimeRequests.Add(1)
	c.inFlightRequests.Add(1)
	defer c.inFlightRequests.Add(-1)

	sid, reqPacket := c.createRequest(command)

	respCh := make(chan *response)
	c.resp.set(sid, respCh)
	defer func() {
		c.resp.set(sid, nil)
	}()

	c.printPacket("write", reqPacket)
	_, err := conn.Write(reqPacket)
	if err != nil {
		return nil, err
	}
	var timeoutChan <-chan time.Time
	d := time.Duration(c.responseTimeout.Load())
	if d > 0 {
		timer := time.NewTimer(d)
		defer timer.Stop()
		timeoutChan = timer.C
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.ctx.Done(): // c.ctx is read only between initConnAndStartReadLoop and Close
		return nil, ClientClosedError{}
	case respV := <-respCh:
		return respV, nil
	case <-timeoutChan:
		c.lifetimeTimeouts.Add(1)
		return nil, ResponseTimeoutError{d} // can not actually happen if d == 0
	}
}

func (c *UDPClient) sendCommandAndCheckResponse(ctx context.Context, command []byte) (*response, error) {
	resp, err := c.sendCommand(ctx, command)
	if err != nil {
		return nil, err
	}
	er := c.checkResponse(resp, err)
	if er != nil {
		return nil, er
	}
	return resp, nil
}

func (c *UDPClient) bitTwiddle(ctx context.Context, ma MemoryArea, address uint16, bitOffset byte, value byte) error {
	return c.wrapOperate(ctx, func() error {
		if err := checkIsBitArea(ma); err != nil {
			return err
		}
		return c._bitTwiddle(ctx, ma, address, bitOffset, value)
	})
}

func (c *UDPClient) _bitTwiddle(ctx context.Context, ma MemoryArea, address uint16, bitOffset byte, value byte) error {
	mem := memoryAddress{ma.Code, address, bitOffset}
	command := writeCommand(mem, 1, []byte{value})
	return c.checkResponse(c.sendCommand(ctx, command))
}

func (c *UDPClient) readBits(ctx context.Context, ma MemoryArea, address uint16, bitOffset byte, readCount uint16) ([]bool, error) {
	if err := checkIsBitArea(ma); err != nil {
		return nil, err
	}
	c.logger().Debug("fins read bits",
		slog.String("area", ma.Name),
		slog.Int("addr", int(address)),
		slog.Int("bitOffset", int(bitOffset)),
		slog.Int("count", int(readCount)),
	)
	command := readCommand(memAddrWithBitOffset(ma.Code, address, bitOffset), readCount)
	r, err := c.sendCommandAndCheckResponse(ctx, command)
	if err != nil {
		return nil, err
	}

	// Guard: the wire-level spec says one byte per bit in the response.
	// A misbehaving PLC (or a configured-ignored end code paired with a
	// truncated payload) would otherwise panic with an index out of range.
	if len(r.data) < int(readCount) {
		return nil, ResponseLengthError{want: int(readCount), got: len(r.data)}
	}

	result := make([]bool, readCount)
	for i := 0; i < int(readCount); i++ {
		result[i] = r.data[i]&0x01 > 0
	}
	return result, nil
}
func (c *UDPClient) readBytes(ctx context.Context, ma MemoryArea, address uint16, readCount uint16) ([]byte, error) {
	if err := checkIsWordArea(ma); err != nil {
		return nil, err
	}
	c.logger().Debug("fins read bytes",
		slog.String("area", ma.Name),
		slog.Int("addr", int(address)),
		slog.Int("count", int(readCount)),
	)
	addr := memAddr(ma.Code, address)
	command := readCommand(addr, readCount)
	r, e := c.sendCommandAndCheckResponse(ctx, command)
	if e != nil {
		return nil, e
	}
	if len(r.data) != int(readCount)*2 {
		return nil, ResponseLengthError{want: int(readCount) * 2, got: len(r.data)}
	}
	return r.data, nil
}

func (c *UDPClient) wrapOperate(ctx context.Context, do func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if c.closed.Load() {
		return ClientClosedError{}
	}
	c.wg.Add(1)
	defer c.wg.Done()
	err := c.initConnAndStartReadLoop()
	if err != nil {
		return err
	}
	return do()
}

func (c *UDPClient) getConn() *net.UDPConn {
	c.m.Lock()
	defer c.m.Unlock()
	return c.conn
}

func (c *UDPClient) setConnAndCtx(conn *net.UDPConn) {
	c.conn = conn
	parent := c.parentCtx
	if parent == nil {
		parent = context.Background()
	}
	if conn == nil {
		// No active connection — expose parentCtx directly; cancel is a no-op.
		c.ctx, c.cancel = parent, func() {}
	} else {
		c.ctx, c.cancel = context.WithCancel(parent)
	}
}
func (c *UDPClient) closeConn() {
	c.m.Lock()
	defer c.m.Unlock()
	if c.conn == nil {
		return
	}
	c.cancel()
	c.conn.Close()
}

func (c *UDPClient) wrapClose() {
	// closed is latched before closing so any concurrent wrapRead/wrapOperate
	// that raced past the initial closed check still observes ClientClosedError
	// via initConnAndStartReadLoop's own closed check.
	c.closed.Store(true)
	c.closing.Store(true)
	defer c.closing.Store(false)
	c.closeConn()
	c.wg.Wait()
	// if c.wg.Wait() returns, no goroutine is using this client any more,
	// and since c.closed is latched true no new goroutine can use it either —
	// so setConnAndCtx runs safely without holding c.m.
	c.setConnAndCtx(nil)
}

func (c *UDPClient) loadByteOrder() binary.ByteOrder {
	if p := c.byteOrder.Load(); p != nil {
		return *p
	}
	return binary.BigEndian
}

func (c *UDPClient) uint16sToBytes(us []uint16) []byte {
	bts := make([]byte, 2*len(us))
	order := c.loadByteOrder()
	for i := 0; i < len(us); i++ {
		order.PutUint16(bts[i*2:i*2+2], us[i])
	}
	return bts
}

func (c *UDPClient) bytesToUint16s(bs []byte) []uint16 {
	order := c.loadByteOrder()
	data := make([]uint16, len(bs)/2)
	for i := 0; i < len(bs)/2; i++ {
		data[i] = order.Uint16(bs[i*2 : i*2+2])
	}
	return data
}

func (c *UDPClient) checkResponse(r *response, err error) error {
	if err != nil {
		return err
	}
	if r.endCode == EndCodeNormalCompletion {
		return nil
	}
	m, _ := c.ignoreErrorCode.Load().(map[uint16]struct{})
	if _, ok := m[r.endCode]; ok {
		c.logger().Debug("fins: ignoring end code",
			slog.Int("endCode", int(r.endCode)),
		)
		return nil
	}
	c.logger().Warn("fins: non-zero end code",
		slog.Int("endCode", int(r.endCode)),
		slog.String("msg", EndCodeToMsg(r.endCode)),
	)
	return EndCodeError{r.endCode}
}

func wrapRead[T any](ctx context.Context, c *UDPClient, do func() (T, error)) (result T, err error) {
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if c.closed.Load() {
		return result, ClientClosedError{}
	}
	c.wg.Add(1)
	defer c.wg.Done()
	if err = c.initConnAndStartReadLoop(); err != nil {
		return result, err
	}
	return do()
}
func decodeClock(data []byte) (*time.Time, error) {
	if len(data) < 6 {
		return nil, fmt.Errorf("failed to decode colck: data length should be 6, got: %d", len(data))
	}
	year, err := decodeBCD(data[0:1])
	if err != nil {
		return nil, fmt.Errorf("failed to decode year from %X: %w", data[0:1], err)
	}
	if year < 50 {
		year += 2000
	} else {
		year += 1900
	}
	month, err := decodeBCD(data[1:2])
	if err != nil {
		return nil, fmt.Errorf("failed to decode month from %X: %w", data[1], err)
	}
	day, err := decodeBCD(data[2:3])
	if err != nil {
		return nil, fmt.Errorf("failed to decode day from %X: %w", data[2], err)
	}
	hour, err := decodeBCD(data[3:4])
	if err != nil {
		return nil, fmt.Errorf("failed to decode hour from %X: %w", data[3], err)
	}
	minute, err := decodeBCD(data[4:5])
	if err != nil {
		return nil, fmt.Errorf("failed to decode minute from %X: %w", data[4], err)
	}
	second, err := decodeBCD(data[5:6])
	if err != nil {
		return nil, fmt.Errorf("failed to decode second from % X: %w", data[5:6], err)
	}
	tt := time.Date(int(year), time.Month(month), int(day), int(hour), int(minute), int(second), 0, time.Local)
	return &tt, nil
}
