package fins

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestAreaConstants_Codes pins the wire-level byte codes for every
// predefined MemoryArea. These codes come from the Omron FINS protocol
// (W342 / W421 reference manuals) — any change here breaks the wire.
func TestAreaConstants_Codes(t *testing.T) {
	cases := []struct {
		name     string
		area     MemoryArea
		wantCode byte
		wantBits bool
	}{
		{"AreaCIOBit", AreaCIOBit, 0x30, true},
		{"AreaWRBit", AreaWRBit, 0x31, true},
		{"AreaHRBit", AreaHRBit, 0x32, true},
		{"AreaARBit", AreaARBit, 0x33, true},
		{"AreaDMBit", AreaDMBit, 0x02, true},
		{"AreaCIOWord", AreaCIOWord, 0xB0, false},
		{"AreaWRWord", AreaWRWord, 0xB1, false},
		{"AreaHRWord", AreaHRWord, 0xB2, false},
		{"AreaARWord", AreaARWord, 0xB3, false},
		{"AreaDMWord", AreaDMWord, 0x82, false},
		{"AreaTimerCounterFlag", AreaTimerCounterFlag, 0x09, true},
		{"AreaTimerCounterPV", AreaTimerCounterPV, 0x89, false},
		{"AreaTaskBit", AreaTaskBit, 0x06, true},
		{"AreaTaskStatus", AreaTaskStatus, 0x46, false},
		{"AreaIndexRegisterPV", AreaIndexRegisterPV, 0xDC, false},
		{"AreaDataRegisterPV", AreaDataRegisterPV, 0xBC, false},
		{"AreaClockPulsesConditionFlagsBit", AreaClockPulsesConditionFlagsBit, 0x07, true},
		{"AreaEMCurrentBankWord", AreaEMCurrentBankWord, 0x98, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.wantCode, tc.area.Code, "wire code")
			assert.Equal(t, tc.wantBits, tc.area.Bits, "Bits flag")
			assert.NotEmpty(t, tc.area.Name, "Name should be set")
		})
	}
}

// TestEMBank_ValidRange pins the EM bank wire codes: bits 0x20..0x2C,
// words 0xA0..0xAC.
func TestEMBank_ValidRange(t *testing.T) {
	for bank := 0; bank <= 12; bank++ {
		t.Run("bit", func(t *testing.T) {
			ma, err := EMBank(bank, true)
			assert.NoError(t, err)
			assert.Equal(t, byte(0x20+bank), ma.Code)
			assert.True(t, ma.Bits)
		})
		t.Run("word", func(t *testing.T) {
			ma, err := EMBank(bank, false)
			assert.NoError(t, err)
			assert.Equal(t, byte(0xA0+bank), ma.Code)
			assert.False(t, ma.Bits)
		})
	}
}

// TestEMBank_OutOfRange asserts banks outside [0, 12] return an error
// (and no partial MemoryArea leaks through).
func TestEMBank_OutOfRange(t *testing.T) {
	cases := []int{-1, 13, 255, -100}
	for _, bank := range cases {
		_, err := EMBank(bank, true)
		assert.Error(t, err, "bank %d should be rejected", bank)
		_, err = EMBank(bank, false)
		assert.Error(t, err, "bank %d should be rejected", bank)
	}
}

// TestTypedArea_MatchesDeprecatedConstants proves the typed Area* values
// produce the same wire byte as the deprecated MemoryArea* constants — i.e.
// migrating from the old API does not change the bytes on the wire.
func TestTypedArea_MatchesDeprecatedConstants(t *testing.T) {
	cases := []struct {
		typed      MemoryArea
		legacyCode byte
	}{
		{AreaCIOBit, MemoryAreaCIOBit},
		{AreaWRBit, MemoryAreaWRBit},
		{AreaHRBit, MemoryAreaHRBit},
		{AreaARBit, MemoryAreaARBit},
		{AreaDMBit, MemoryAreaDMBit},
		{AreaCIOWord, MemoryAreaCIOWord},
		{AreaWRWord, MemoryAreaWRWord},
		{AreaHRWord, MemoryAreaHRWord},
		{AreaARWord, MemoryAreaARWord},
		{AreaDMWord, MemoryAreaDMWord},
		{AreaTimerCounterFlag, MemoryAreaTimerCounterCompletionFlag},
		{AreaTimerCounterPV, MemoryAreaTimerCounterPV},
		{AreaTaskBit, MemoryAreaTaskBit},
		{AreaTaskStatus, MemoryAreaTaskStatus},
		{AreaIndexRegisterPV, MemoryAreaIndexRegisterPV},
		{AreaDataRegisterPV, MemoryAreaDataRegisterPV},
		{AreaClockPulsesConditionFlagsBit, MemoryAreaClockPulsesConditionFlagsBit},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.legacyCode, tc.typed.Code, "%s wire byte mismatch", tc.typed.Name)
	}
}

// TestMemoryArea_String covers the String() stringer: Named areas return
// their Name; anonymous synthesised MemoryArea values return a hex code.
func TestMemoryArea_String(t *testing.T) {
	assert.Equal(t, "DM", AreaDMWord.String())
	assert.Equal(t, "EM5", func() string {
		ma, _ := EMBank(5, false)
		return ma.String()
	}())
	// Synthesised MemoryArea without a name.
	anon := MemoryArea{Code: 0x77}
	assert.Equal(t, "0x77", anon.String())
}

// TestIncompatibleArea_Read asserts that read methods reject a MemoryArea
// whose Bits flag does not match the method's requirement. This demonstrates
// the compile-time-checked typed API upgrading to runtime validation too.
func TestIncompatibleArea_Read(t *testing.T) {
	c := newBlackholeClient(t)
	defer c.Close()
	ctx := t.Context()

	// ReadBytes requires a word area; pass a bit area.
	_, err := c.ReadBytes(ctx, AreaDMBit, 0, 1)
	assert.ErrorAs(t, err, new(IncompatibleMemoryAreaError))

	// ReadBits requires a bit area; pass a word area.
	_, err = c.ReadBits(ctx, AreaDMWord, 0, 0, 1)
	assert.ErrorAs(t, err, new(IncompatibleMemoryAreaError))

	// SetBit / ResetBit / ToggleBit require a bit area.
	err = c.SetBit(ctx, AreaDMWord, 0, 0)
	assert.ErrorAs(t, err, new(IncompatibleMemoryAreaError))
	err = c.ResetBit(ctx, AreaDMWord, 0, 0)
	assert.ErrorAs(t, err, new(IncompatibleMemoryAreaError))
	err = c.ToggleBit(ctx, AreaDMWord, 0, 0)
	assert.ErrorAs(t, err, new(IncompatibleMemoryAreaError))

	// WriteBytes requires word; WriteBits requires bit.
	err = c.WriteBytes(ctx, AreaDMBit, 0, []byte{0, 1})
	assert.ErrorAs(t, err, new(IncompatibleMemoryAreaError))
	err = c.WriteBits(ctx, AreaDMWord, 0, 0, []bool{true})
	assert.ErrorAs(t, err, new(IncompatibleMemoryAreaError))
}
