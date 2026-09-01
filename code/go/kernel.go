package main

import (
	"encoding/binary"
	"math/bits"

	gen "github.com/noelruault/research/1brc/code/gen"
)

// The kernels here are reimplemented rather than imported from 1brc/code/asm, which is the kernel-research module and links Plan 9 assembly this ticket deliberately does not ship (`go-v1-parallel`: "no asm yet").
// Provenance of both constants blocks, and which upstream author they come from, is in LICENSES.md.

// maxRow is the longest row the fast path is willing to assume fits: 100 name bytes (the challenge's limit), ';', "-99.9", '\n', and headroom.
// The fast path only runs while a whole row plus its over-fetch is inside the buffer, which is what lets every load below be unconditional.
const maxRow = 128

const (
	semicolonBroadcast = 0x3B3B3B3B3B3B3B3B
	newlineBroadcast   = 0x0A0A0A0A0A0A0A0A
	swarLow            = 0x0101010101010101
	swarHigh           = 0x8080808080808080
)

// indexDelim finds the first ';' or '\n' eight bytes at a time and reports which one it found.
//
// A borrow chain can set a lane's high bit ABOVE a match but never below one, so the lowest set bit is always the first match.
// Both needles are scanned, not just ';', because a row with no separator would otherwise have its name run into the NEXT row and be folded as a station that does not exist: the reference rejects that row, so this has to as well. It costs four more integer ops per word and is the price of agreeing with the reference on malformed input.
func indexDelim(b []byte) (idx int, semi bool) {
	i := 0
	for ; i+8 <= len(b); i += 8 {
		w := binary.LittleEndian.Uint64(b[i:])
		xs := w ^ semicolonBroadcast
		xn := w ^ newlineBroadcast
		m := ((xs-swarLow)&^xs | (xn-swarLow)&^xn) & swarHigh
		if m != 0 {
			k := i + bits.TrailingZeros64(m)>>3
			return k, b[k] == ';'
		}
	}
	for ; i < len(b); i++ {
		if b[i] == ';' || b[i] == '\n' {
			return i, b[i] == ';'
		}
	}
	return -1, false
}

const (
	parseDotBits  = 0x10101000
	parseDigitsHi = 0x0F000F0F00
	parseMagic    = 0x640a0001
)

// parseTempBranchless parses a one-decimal temperature at the start of b with no data-dependent branch, returning tenths and the offset of the byte after the row's '\n'.
// It loads 8 bytes unconditionally, so len(b) >= 8. It reports next == 0 for a field that cannot be a temperature at all, and otherwise the caller still has to check that the byte it landed on really is a '\n'.
func parseTempBranchless(b []byte) (tenths int32, next int) {
	w := binary.LittleEndian.Uint64(b)
	dot := bits.TrailingZeros64(^w & parseDotBits)
	// The mask only has bits at 12, 20 and 28, so dot is one of those or 64 when no byte in 1-3 can be the '.'; 64 would shift by a negative amount and panic, and it means the field is not a temperature.
	if dot > 28 {
		return 0, 0
	}
	signed := int64(^w<<59) >> 63
	digits := int64((w & ^uint64(signed&0xFF)) << (28 - dot) & parseDigitsHi)
	abs := (digits * parseMagic >> 32) & 0x3FF
	return int32((abs ^ signed) - signed), dot>>3 + 3
}

const (
	digitHighNibbles = 0xF0F0F0F0F0F0F0F0
	digitLowNibbles  = 0x0F0F0F0F0F0F0F0F
	digitThrees      = 0x3030303030303030
	digitSixes       = 0x0606060606060606
	dotThenNewline   = uint64('.') | uint64('\n')<<16
	dotNewlineMask   = uint64(0xFF) | uint64(0xFF)<<16
)

// nonDigitMask sets bits in the high nibble of every lane of w that is not an ASCII digit, and leaves every digit lane zero.
//
// The low-nibble add cannot carry into the next lane: masking to 0x0F first bounds each lane at 0x15, so the eight tests stay independent.
// A lane is a digit exactly when its high nibble is 3 and its low nibble is under 10, which is why both halves are needed: 0x3A passes the first test and 0x2F the second.
func nonDigitMask(w uint64) uint64 {
	return (w^digitThrees)&digitHighNibbles | ((w&digitLowNibbles)+digitSixes)&digitHighNibbles
}

// parseTempWord is parseTempBranchless with validTemp's format rejection folded into the same 8-byte word, so the shape is established from bits already in a register instead of from four to six dependent byte compares behind an unpredictable three-way switch on next.
//
// It must accept and reject byte for byte what parseTempBranchless plus validTemp accept and reject, and TestParseTempWordMatchesTheByteCheck pins that over every 6-byte string in the alphabet the shape is built from.
// The four rejections are ORed into one accumulator so no test can branch: the '.' and '\n' at their fixed offsets from the dot lane, the digit lanes, the sign byte when the parse read one, and the digit COUNT, which is the check that rejects "100.0" (three digits and no sign) and "-.5" (a sign and none).
func parseTempWord(b []byte) (tenths int32, next int, ok bool) {
	w := binary.LittleEndian.Uint64(b)
	dot := bits.TrailingZeros64(^w & parseDotBits)
	if dot > 28 {
		return 0, 0, false
	}
	signed := int64(^w<<59) >> 63
	digits := int64((w & ^uint64(signed&0xFF)) << (28 - dot) & parseDigitsHi)
	abs := (digits * parseMagic >> 32) & 0x3FF

	shift := uint(dot - 4)
	neg := uint64(signed) & 1
	bad := (w ^ dotThenNewline<<shift) & (dotNewlineMask << shift)
	// The lane just under the dot and the lane at neg are the whole digit run for a run of one or two: at length one they are the same lane, at length two they are its ends.
	bad |= nonDigitMask(w) & (uint64(0xF0)<<(shift+8) | uint64(0xF0)<<(shift-8) | uint64(0xF0)<<(8*neg))
	bad |= (w ^ '-') & 0xFF & uint64(signed)
	bad |= uint64(dot>>3-int(neg)-1) &^ 1
	return int32((abs ^ signed) - signed), dot>>3 + 3, bad == 0
}

// parseTempScalar is the branchy alternative H3 left alive, and also the tail path: it never over-reads, so it is what closes a buffer's last rows.
func parseTempScalar(b []byte) (tenths int32, next int, ok bool) {
	i, neg := 0, false
	if i < len(b) && b[i] == '-' {
		neg = true
		i++
	}
	whole, digits := int32(0), 0
	for ; i < len(b) && b[i] >= '0' && b[i] <= '9'; i++ {
		whole = whole*10 + int32(b[i]-'0')
		digits++
	}
	if digits == 0 || digits > 2 || i+2 >= len(b) || b[i] != '.' {
		return 0, 0, false
	}
	if b[i+1] < '0' || b[i+1] > '9' || b[i+2] != '\n' {
		return 0, 0, false
	}
	tenths = whole*10 + int32(b[i+1]-'0')
	if neg {
		tenths = -tenths
	}
	return tenths, i + 3, true
}

const hashMultiplier = 0x9E3779B97F4A7C15

// hashWord hashes a name from the first 8 bytes of the word it starts in, masking off everything at or above nameLen.
// 05-go-techniques.md measured that 8 bytes leave exactly one collision across the 413 official stations and none across the 10k set, and the table resolves it with a full compare.
func hashWord(w uint64, nameLen int) uint64 {
	if nameLen < 8 {
		w &= ^uint64(0) >> ((8 - nameLen) * 8)
	}
	return (w ^ w>>29) * hashMultiplier
}

// hashName is hashWord for a caller that has the name but not the word, and it MUST agree with hashWord byte for byte: a station hashed two ways would occupy two buckets and split its own aggregate.
// TestHashPathsAgree pins that.
func hashName(name []byte) uint64 {
	var w uint64
	if len(name) >= 8 {
		w = binary.LittleEndian.Uint64(name)
		return hashWord(w, 8)
	}
	for i := len(name) - 1; i >= 0; i-- {
		w = w<<8 | uint64(name[i])
	}
	return hashWord(w, len(name))
}

func isDigit(c byte) bool { return c-'0' <= 9 }

// validTemp reports whether the field really is the `-?\d{1,2}\.\d\n` that parseTempBranchless assumes and cannot itself check; next is the parse's own answer for where the row ends, which fixes the shape.
//
// It exists because the parse is happy to read a name containing the separator ("b;1.0", from a row whose real name is "a;b") and a three-digit temperature ("100.0") as plausible values, and the reference rejects both. Without this the two implementations would disagree only on rows no byte-comparison of clean data ever sees.
func validTemp(b []byte, next int) bool {
	switch next {
	case 4:
		return isDigit(b[0]) && b[1] == '.' && isDigit(b[2]) && b[3] == '\n'
	case 5:
		return (isDigit(b[0]) || b[0] == '-') && isDigit(b[1]) && b[2] == '.' && isDigit(b[3]) && b[4] == '\n'
	case 6:
		return b[0] == '-' && isDigit(b[1]) && isDigit(b[2]) && b[3] == '.' && isDigit(b[4]) && b[5] == '\n'
	}
	return false
}

// inRange reports whether tenths is a temperature the generator could have written; the reference rejects anything else, so this binary has to as well or the two disagree only on files nobody byte-compares.
func inRange(tenths int32) bool {
	return tenths >= int32(gen.MinTenths) && tenths <= int32(gen.MaxTenths)
}
