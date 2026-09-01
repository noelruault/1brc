package main

import (
	"encoding/binary"
	"fmt"
	"math/bits"

	asm "github.com/noelruault/research/1brc/code/asm"
)

// kernel selects how a buffer is split into rows. The three arms are what `go-v2-kernels` measures against each other end to end.
type kernel int

const (
	kernelRow kernel = iota
	kernelBatchSWAR
	kernelBatchNEON
)

// batchWindowNEON is the width of one asm.DelimMask32 compare, and batchWindowSWAR is one 64-bit word.
// Both loops stop batchTailSlack bytes early so that the branchless parse's unconditional 8-byte load, which may start on the last separator a window reports, still lands inside the buffer.
const (
	batchWindowNEON = 32
	batchWindowSWAR = 8
	batchTailSlack  = 8
)

const swarLow7Bits = 0x7F7F7F7F7F7F7F7F

// zeroByteMask returns a mask with bit 8k+7 set for every zero byte of w, and no other bits.
//
// indexDelim's cheaper `(w-low) &^ w & high` form is not usable here. Its borrow chain can set a lane's high bit above a real match, which is harmless when only the LOWEST set bit is read and is a wrong row boundary when every bit is drained, which is what a batch kernel does.
func zeroByteMask(w uint64) uint64 {
	return ^(((w & swarLow7Bits) + swarLow7Bits) | w) & swarHigh
}

// foldBatchSWAR is the batch shape without the vector unit: one pass over the buffer, both needles drained from each 8-byte word in address order, no per-row rescan and no assembly call.
// It returns the offset of the first row it did not fold, which its caller finishes with the scalar tail.
func (t *table) foldBatchSWAR(data []byte, base int64) (int, error) {
	pendingSep, rowStart := -1, 0
	for pos := 0; pos+batchWindowSWAR+batchTailSlack <= len(data); pos += batchWindowSWAR {
		w := binary.LittleEndian.Uint64(data[pos:])
		semi, nl := zeroByteMask(w^semicolonBroadcast), zeroByteMask(w^newlineBroadcast)
		for semi|nl != 0 {
			s, l := 64, 64
			if semi != 0 {
				s = bits.TrailingZeros64(semi)
			}
			if nl != 0 {
				l = bits.TrailingZeros64(nl)
			}
			if s < l {
				if pendingSep < 0 {
					pendingSep = pos + s>>3
				}
				semi &= semi - 1
				continue
			}
			next, err := t.foldBatchRow(data, base, rowStart, pendingSep, pos+l>>3)
			if err != nil {
				return 0, err
			}
			rowStart, pendingSep = next, -1
			nl &= nl - 1
		}
	}
	return rowStart, nil
}

// foldBatchNEON is foldBatchSWAR over a 32-byte window: one asm.DelimMask32 call answers both needles for every row in the window, which is the amortisation 04-asm-kernels.md measured as the largest microbenchmark win in the study.
// asm.DelimMask32's syndrome gives lane k the bit at 2k, so an index is a trailing-zero count shifted by one rather than by three.
func (t *table) foldBatchNEON(data []byte, base int64) (int, error) {
	pendingSep, rowStart := -1, 0
	for pos := 0; pos+batchWindowNEON+batchTailSlack <= len(data); pos += batchWindowNEON {
		semi, nl := asm.DelimMask32(data[pos:])
		for semi|nl != 0 {
			s, l := 64, 64
			if semi != 0 {
				s = bits.TrailingZeros64(semi)
			}
			if nl != 0 {
				l = bits.TrailingZeros64(nl)
			}
			if s < l {
				if pendingSep < 0 {
					pendingSep = pos + s>>1
				}
				semi &= semi - 1
				continue
			}
			next, err := t.foldBatchRow(data, base, rowStart, pendingSep, pos+l>>1)
			if err != nil {
				return 0, err
			}
			rowStart, pendingSep = next, -1
			nl &= nl - 1
		}
	}
	return rowStart, nil
}

// foldBatchRow folds the row [rowStart,end] that the drain has just closed, and returns where the next row starts.
//
// pendingSep is the FIRST ';' seen since rowStart, which is gen.Aggregate's rule; a row whose newline arrives with no separator pending is the malformed shape the reference rejects, and so does this.
// The rejections mirror the per-row path exactly, because a kernel that accepts a row the other kernels reject changes the answer rather than the speed.
func (t *table) foldBatchRow(data []byte, base int64, rowStart, pendingSep, end int) (int, error) {
	if pendingSep < 0 {
		return 0, rowError(base+int64(rowStart), data[rowStart:])
	}
	field := data[pendingSep+1:]
	v, next := parseTempBranchless(field)
	if next == 0 || !validTemp(field, next) || !inRange(v) {
		return 0, rowError(base+int64(rowStart), data[rowStart:])
	}
	// The parse is NOT checked against end: validTemp already requires a '\n' at pendingSep+next with only digits before it, and end is the lowest undrained newline above pendingSep, so they are the same byte.
	name := data[rowStart:pendingSep]
	if !t.update(hashWord(binary.LittleEndian.Uint64(data[rowStart:]), len(name)), name, v) {
		return 0, t.fullError(base + int64(rowStart))
	}
	return end + 1, nil
}

func kernelMode(name string) (kernel, error) {
	switch name {
	case "row":
		return kernelRow, nil
	case "batch-swar":
		return kernelBatchSWAR, nil
	case "batch-neon":
		return kernelBatchNEON, nil
	}
	return 0, fmt.Errorf("unknown -kernel %q, want row, batch-swar or batch-neon", name)
}
