package asm

import (
	"encoding/binary"
	"math/bits"
)

// Every kernel below is checked against the Scalar* function for the same sub-problem, over the real corpus plus adversarial input.
// The scalar versions are the ground truth: obviously correct, never fast.

// ScalarIndexSemicolon returns the index of the first ';' in b, or -1.
func ScalarIndexSemicolon(b []byte) int {
	for i := range b {
		if b[i] == ';' {
			return i
		}
	}
	return -1
}

// ScalarParseTemp parses a one-decimal temperature at the start of b, returning tenths and the offset of the byte after the row's '\n'.
// It reports ok=false rather than guessing on anything the generator would never emit.
func ScalarParseTemp(b []byte) (tenths int64, next int, ok bool) {
	i := 0
	neg := false
	if i < len(b) && b[i] == '-' {
		neg = true
		i++
	}
	whole := int64(0)
	digits := 0
	for ; i < len(b) && b[i] >= '0' && b[i] <= '9'; i++ {
		whole = whole*10 + int64(b[i]-'0')
		digits++
	}
	if digits == 0 || digits > 2 || i+2 >= len(b) || b[i] != '.' {
		return 0, 0, false
	}
	if b[i+1] < '0' || b[i+1] > '9' || b[i+2] != '\n' {
		return 0, 0, false
	}
	tenths = whole*10 + int64(b[i+1]-'0')
	if neg {
		tenths = -tenths
	}
	return tenths, i + 3, true
}

// ScalarMaskName zeroes the bytes of w at and above byte position sep, keeping the name bytes a hash may see.
func ScalarMaskName(w uint64, sep int) uint64 {
	var out uint64
	for i := 0; i < sep && i < 8; i++ {
		out |= w & (0xFF << (8 * i))
	}
	return out
}

const (
	semicolonBroadcast = 0x3B3B3B3B3B3B3B3B
	newlineBroadcast   = 0x0A0A0A0A0A0A0A0A
	swarLow            = 0x0101010101010101
	swarHigh           = 0x8080808080808080
)

// swarMatch returns a word whose byte lanes have the high bit set where w equals the broadcast needle.
// A borrow chain can also set the high bit of a lane ABOVE a true match, never below one, so the lowest set bit is always the first match.
func swarMatch(w, needle uint64) uint64 {
	x := w ^ needle
	return (x - swarLow) & ^x & swarHigh
}

// SWARIndexSemicolon finds the first ';' eight bytes at a time with the zero-byte trick every top 1BRC entry uses (03-technique-recon.md, technique 2).
func SWARIndexSemicolon(b []byte) int {
	i := 0
	for ; i+8 <= len(b); i += 8 {
		if m := swarMatch(binary.LittleEndian.Uint64(b[i:]), semicolonBroadcast); m != 0 {
			return i + bits.TrailingZeros64(m)>>3
		}
	}
	for ; i < len(b); i++ {
		if b[i] == ';' {
			return i
		}
	}
	return -1
}

// merykitty's parse, reimplemented from the description in 03-technique-recon.md and verified there over all 1999 legal temperatures.
// The magic multiply sums 100*hundreds + 10*tens + units into bits 32-41 in one instruction.
const (
	parseDotBits  = 0x10101000
	parseDigitsHi = 0x0F000F0F00
	parseMagic    = 0x640a0001
)

// BranchlessParseTemp parses the temperature field at the start of b with no data-dependent branch, returning tenths and the offset of the byte after the row's '\n'.
// It loads 8 bytes unconditionally, so it requires len(b) >= 8; the caller's tail path owns anything shorter.
func BranchlessParseTemp(b []byte) (tenths int64, next int) {
	w := binary.LittleEndian.Uint64(b)
	dot := bits.TrailingZeros64(^w & parseDotBits)
	signed := int64(^w<<59) >> 63
	digits := int64((w & ^uint64(signed&0xFF)) << (28 - dot) & parseDigitsHi)
	abs := (digits * parseMagic >> 32) & 0x3FF
	return (abs ^ signed) - signed, dot>>3 + 3
}

// maskLUT is jerrinot's 9-entry table (03-technique-recon.md, technique 5), indexed by delimiter position.
var maskLUT = [9]uint64{
	0x0000000000000000,
	0x00000000000000FF,
	0x000000000000FFFF,
	0x0000000000FFFFFF,
	0x00000000FFFFFFFF,
	0x000000FFFFFFFFFF,
	0x0000FFFFFFFFFFFF,
	0x00FFFFFFFFFFFFFF,
	0xFFFFFFFFFFFFFFFF,
}

// ShiftMaskName computes the name mask by shifting. sep must be in [0,8].
func ShiftMaskName(w uint64, sep int) uint64 {
	return w & (^uint64(0) >> ((8 - sep) * 8))
}

// LUTMaskName looks the same mask up in maskLUT. sep must be in [0,8].
func LUTMaskName(w uint64, sep int) uint64 {
	return w & maskLUT[sep]
}
