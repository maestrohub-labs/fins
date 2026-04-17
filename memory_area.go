package fins

import "fmt"

// MemoryArea identifies a named region of PLC memory plus its access width.
//
// Construct a MemoryArea either by using one of the predefined Area* values
// exported by this package, or — for EM banks — via EMBank.
type MemoryArea struct {
	Code byte   // FINS area code on the wire
	Name string // Human-readable label (for errors/logs)
	Bits bool   // True = bit-addressable, false = word-addressable
}

// String returns the human-readable label (falls back to hex wire code
// for synthesised areas without a Name set).
func (m MemoryArea) String() string {
	if m.Name == "" {
		return fmt.Sprintf("0x%02X", m.Code)
	}
	return m.Name
}

// Predefined memory areas — use these rather than raw byte codes.
//
// See Omron FINS protocol memory area codes (W342 / W421 reference manuals).
var (
	AreaCIOBit = MemoryArea{Code: 0x30, Name: "CIO", Bits: true}
	AreaWRBit  = MemoryArea{Code: 0x31, Name: "WR", Bits: true}
	AreaHRBit  = MemoryArea{Code: 0x32, Name: "HR", Bits: true}
	AreaARBit  = MemoryArea{Code: 0x33, Name: "AR", Bits: true}
	AreaDMBit  = MemoryArea{Code: 0x02, Name: "DM", Bits: true}

	AreaCIOWord = MemoryArea{Code: 0xB0, Name: "CIO", Bits: false}
	AreaWRWord  = MemoryArea{Code: 0xB1, Name: "WR", Bits: false}
	AreaHRWord  = MemoryArea{Code: 0xB2, Name: "HR", Bits: false}
	AreaARWord  = MemoryArea{Code: 0xB3, Name: "AR", Bits: false}
	AreaDMWord  = MemoryArea{Code: 0x82, Name: "DM", Bits: false}

	AreaTimerCounterFlag = MemoryArea{Code: 0x09, Name: "TIM_CT_FLAG", Bits: true}
	AreaTimerCounterPV   = MemoryArea{Code: 0x89, Name: "TIM_CT_PV", Bits: false}

	AreaTaskBit    = MemoryArea{Code: 0x06, Name: "TASK_BIT", Bits: true}
	AreaTaskStatus = MemoryArea{Code: 0x46, Name: "TASK_STATUS", Bits: false}

	AreaIndexRegisterPV = MemoryArea{Code: 0xDC, Name: "IR_PV", Bits: false}
	AreaDataRegisterPV  = MemoryArea{Code: 0xBC, Name: "DR_PV", Bits: false}

	AreaClockPulsesConditionFlagsBit = MemoryArea{Code: 0x07, Name: "CLOCK_FLAG", Bits: true}

	// AreaEMCurrentBankWord addresses the currently-selected EM bank
	// (word-level). Use EMBank for an explicit bank number.
	AreaEMCurrentBankWord = MemoryArea{Code: 0x98, Name: "EM_CURRENT", Bits: false}
)

// EMBank returns the MemoryArea for a specific EM (Expansion Memory) bank.
// bank must be in [0, 12]; bitLevel selects between bit- and word-level access.
//
// Wire codes: bits use 0x20..0x2C, words use 0xA0..0xAC.
func EMBank(bank int, bitLevel bool) (MemoryArea, error) {
	if bank < 0 || bank > 12 {
		return MemoryArea{}, fmt.Errorf("EM bank %d out of range (0..12)", bank)
	}
	if bitLevel {
		return MemoryArea{Code: byte(0x20 + bank), Name: fmt.Sprintf("EM%d", bank), Bits: true}, nil
	}
	return MemoryArea{Code: byte(0xA0 + bank), Name: fmt.Sprintf("EM%d", bank), Bits: false}, nil
}

// Deprecated byte-code constants preserved for source-level compatibility
// with code that pattern-matches on the wire code (e.g. simulators). The
// primary API now takes MemoryArea — use the Area* vars above.

// Deprecated: use AreaCIOBit.
const MemoryAreaCIOBit byte = 0x30

// Deprecated: use AreaWRBit.
const MemoryAreaWRBit byte = 0x31

// Deprecated: use AreaHRBit.
const MemoryAreaHRBit byte = 0x32

// Deprecated: use AreaARBit.
const MemoryAreaARBit byte = 0x33

// Deprecated: use AreaCIOWord.
const MemoryAreaCIOWord byte = 0xB0

// Deprecated: use AreaWRWord.
const MemoryAreaWRWord byte = 0xB1

// Deprecated: use AreaHRWord.
const MemoryAreaHRWord byte = 0xB2

// Deprecated: use AreaARWord.
const MemoryAreaARWord byte = 0xB3

// Deprecated: use AreaTimerCounterFlag.
const MemoryAreaTimerCounterCompletionFlag byte = 0x09

// Deprecated: use AreaTimerCounterPV.
const MemoryAreaTimerCounterPV byte = 0x89

// Deprecated: use AreaDMBit.
const MemoryAreaDMBit byte = 0x02

// Deprecated: use AreaDMWord.
const MemoryAreaDMWord byte = 0x82

// Deprecated: use AreaTaskBit.
const MemoryAreaTaskBit byte = 0x06

// Deprecated: use AreaTaskStatus.
const MemoryAreaTaskStatus byte = 0x46

// Deprecated: use AreaIndexRegisterPV.
const MemoryAreaIndexRegisterPV byte = 0xDC

// Deprecated: use AreaDataRegisterPV.
const MemoryAreaDataRegisterPV byte = 0xBC

// Deprecated: use AreaClockPulsesConditionFlagsBit.
const MemoryAreaClockPulsesConditionFlagsBit byte = 0x07
