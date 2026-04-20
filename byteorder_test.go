package fins

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSetByteOrder_SwitchAfterConstruct regresses the v0.1.0-mh.2 bug where
// SetByteOrder(binary.LittleEndian) panicked after NewUDPClient had stored
// binary.BigEndian via atomic.Value — the two have different concrete types,
// so atomic.Value.Store rejected the second call.
func TestSetByteOrder_SwitchAfterConstruct(t *testing.T) {
	clientAddr := NewUDPAddress("127.0.0.1", 0, 0, 2, 0)
	plcAddr := NewUDPAddress("127.0.0.1", 9600, 0, 10, 0)
	c, err := NewUDPClient(context.Background(), clientAddr, plcAddr)
	assert.Nil(t, err)
	defer c.Close()

	// Default should be BigEndian: 0x0102 -> [0x01, 0x02].
	assert.Equal(t, []byte{0x01, 0x02}, c.uint16sToBytes([]uint16{0x0102}))

	assert.NotPanics(t, func() {
		c.SetByteOrder(binary.LittleEndian)
	})

	// After the switch: 0x0102 -> [0x02, 0x01].
	assert.Equal(t, []byte{0x02, 0x01}, c.uint16sToBytes([]uint16{0x0102}))
	assert.Equal(t, []uint16{0x0102}, c.bytesToUint16s([]byte{0x02, 0x01}))

	// And back again — switching repeatedly must also not panic.
	assert.NotPanics(t, func() {
		c.SetByteOrder(binary.BigEndian)
	})
	assert.Equal(t, []byte{0x01, 0x02}, c.uint16sToBytes([]uint16{0x0102}))
}

// TestSetByteOrder_NilFallback covers the zero-value UDPClient case: no
// SetByteOrder call has been made, so the internal atomic pointer is nil.
// Reads must fall back to BigEndian rather than deref-panicking.
func TestSetByteOrder_NilFallback(t *testing.T) {
	var c UDPClient
	assert.Equal(t, []byte{0x01, 0x02}, c.uint16sToBytes([]uint16{0x0102}))
	assert.Equal(t, []uint16{0x0102}, c.bytesToUint16s([]byte{0x01, 0x02}))
}

// TestSetByteOrder_NilArgument documents that a nil ByteOrder is silently
// ignored (previous behaviour preserved).
func TestSetByteOrder_NilArgument(t *testing.T) {
	clientAddr := NewUDPAddress("127.0.0.1", 0, 0, 2, 0)
	plcAddr := NewUDPAddress("127.0.0.1", 9600, 0, 10, 0)
	c, err := NewUDPClient(context.Background(), clientAddr, plcAddr)
	assert.Nil(t, err)
	defer c.Close()

	c.SetByteOrder(binary.LittleEndian)
	c.SetByteOrder(nil) // must not clobber the previous value or panic

	assert.Equal(t, []byte{0x02, 0x01}, c.uint16sToBytes([]uint16{0x0102}))
}
