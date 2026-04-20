package fins

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestToggleBit_ActuallyFlips regresses the v0.1.0-mh.3 bug where ToggleBit
// performed a read-modify-write that wrote the value it had just read,
// leaving the bit unchanged. The library does its own read-modify-write
// (it does not rely on a FINS-native toggle action code), so the only way
// to verify correctness is end-to-end: seed a known bit, toggle, read back.
func TestToggleBit_ActuallyFlips(t *testing.T) {
	c, _, cleanup := simBatchClient(t)
	defer cleanup()

	ctx := context.Background()
	const addr uint16 = 50
	const off byte = 3

	// Case 1: bit starts cleared.
	assert.Nil(t, c.ResetBit(ctx, AreaDMBit, addr, off))
	bs, err := c.ReadBits(ctx, AreaDMBit, addr, off, 1)
	assert.Nil(t, err)
	assert.Equal(t, []bool{false}, bs, "precondition: bit cleared")

	assert.Nil(t, c.ToggleBit(ctx, AreaDMBit, addr, off))
	bs, err = c.ReadBits(ctx, AreaDMBit, addr, off, 1)
	assert.Nil(t, err)
	assert.Equal(t, []bool{true}, bs, "toggle from false should yield true")

	// Case 2: toggle again, bit starts set.
	assert.Nil(t, c.ToggleBit(ctx, AreaDMBit, addr, off))
	bs, err = c.ReadBits(ctx, AreaDMBit, addr, off, 1)
	assert.Nil(t, err)
	assert.Equal(t, []bool{false}, bs, "toggle from true should yield false")

	// Case 3: explicitly seed true and toggle — catches the case where a
	// future bug might only get case 1 right by coincidence.
	assert.Nil(t, c.SetBit(ctx, AreaDMBit, addr, off))
	bs, err = c.ReadBits(ctx, AreaDMBit, addr, off, 1)
	assert.Nil(t, err)
	assert.Equal(t, []bool{true}, bs, "precondition: bit set")

	assert.Nil(t, c.ToggleBit(ctx, AreaDMBit, addr, off))
	bs, err = c.ReadBits(ctx, AreaDMBit, addr, off, 1)
	assert.Nil(t, err)
	assert.Equal(t, []bool{false}, bs, "toggle from explicit true should yield false")
}

// TestToggleBit_DoesNotTouchNeighbours proves the FINS write targets exactly
// one bit — toggling bit N must not disturb bits N-1 or N+1.
func TestToggleBit_DoesNotTouchNeighbours(t *testing.T) {
	c, _, cleanup := simBatchClient(t)
	defer cleanup()

	ctx := context.Background()
	const addr uint16 = 80
	// Set up a known 3-bit pattern starting at addr.0: [true, false, true].
	// Then toggle the middle bit only. Expect [true, true, true].
	assert.Nil(t, c.WriteBits(ctx, AreaDMBit, addr, 0, []bool{true, false, true}))

	assert.Nil(t, c.ToggleBit(ctx, AreaDMBit, addr, 1))

	bs, err := c.ReadBits(ctx, AreaDMBit, addr, 0, 3)
	assert.Nil(t, err)
	assert.Equal(t, []bool{true, true, true}, bs)
}
