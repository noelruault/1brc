package asm

import "math/bits"

// Token is what a tokenizer recovers from one row: enough to hash the name in place and fold the value, without materialising a string.
// Start is load-bearing for the batch kernel and free for the staged ones, whose driver already holds the position; it is what a real aggregator needs to find the name bytes.
// Three int32s keep the token at 12 bytes, which is the batch kernel's own memory traffic.
type Token struct {
	Start   int32
	NameLen int32
	Tenths  int32
}

// Tokenizer consumes one row from the front of b and returns the row's token and its length in bytes.
// b must hold a complete row followed by at least 8 readable bytes, which is what TokenizeAll's tail path is for.
type Tokenizer func(b []byte) (Token, int)

// TokenizeStagedSWAR finds the separator with the SWAR word scan, then parses the temperature.
func TokenizeStagedSWAR(b []byte) (Token, int) {
	sep := SWARIndexSemicolon(b)
	tenths, next := BranchlessParseTemp(b[sep+1:])
	return Token{NameLen: int32(sep), Tenths: int32(tenths)}, sep + 1 + next
}

// TokenizeStagedNEON finds the separator with the 16-byte NEON compare, then parses the temperature.
func TokenizeStagedNEON(b []byte) (Token, int) {
	sep := NEONIndexSemicolon(b)
	tenths, next := BranchlessParseTemp(b[sep+1:])
	return Token{NameLen: int32(sep), Tenths: int32(tenths)}, sep + 1 + next
}

// TokenizeScalar is the reference shape: scalar scan, scalar parse, no over-fetch. TokenizeAll uses it for the final row.
func TokenizeScalar(b []byte) (Token, int) {
	sep := ScalarIndexSemicolon(b)
	if sep < 0 {
		return Token{}, 0
	}
	tenths, next, ok := ScalarParseTemp(b[sep+1:])
	if !ok {
		return Token{}, 0
	}
	return Token{NameLen: int32(sep), Tenths: int32(tenths)}, sep + 1 + next
}

// overfetchView extends buf into the OverfetchSlack bytes its caller guarantees are readable.
// Go bounds-checks on length, not capacity, so slack in the capacity alone does not keep an 8- or 16-byte load in bounds.
func overfetchView(buf []byte) []byte {
	return buf[:len(buf)+OverfetchSlack]
}

// batchWindow is the dual-needle scan width. 32 bytes covers ~2.3 rows of the 413-station corpus, which is the point: one transfer pair serves them all.
const batchWindow = 32

// TokenizeBatch tokenizes complete rows out of buf, gigatoken's two-phase shape: one 32-byte compare answers both needles for every row in the window.
// It stops at the last row a full 32-byte window can close, and returns how many rows it wrote and how many bytes it consumed, leaving the rest to its caller.
func TokenizeBatch(buf []byte, out []Token) (rows, consumed int) {
	work := overfetchView(buf)
	pendingSep := -1
	rowStart := 0
	for pos := 0; pos+batchWindow <= len(work); pos += batchWindow {
		semi, nl := neonDelimMask32(work[pos:])
		for semi|nl != 0 {
			s, l := 64, 64
			if semi != 0 {
				s = bits.TrailingZeros64(semi)
			}
			if nl != 0 {
				l = bits.TrailingZeros64(nl)
			}
			if s < l {
				// First ';' of the row wins, which is gen.Aggregate's rule and what every other kernel here does.
				// Without the guard a later ';' inside a name overwrites it and this kernel alone splits the row somewhere else.
				if pendingSep < 0 {
					pendingSep = pos + s>>1
				}
				semi &= semi - 1
				continue
			}
			end := pos + l>>1
			if pendingSep < 0 || rows == len(out) {
				return rows, consumed
			}
			tenths, _ := BranchlessParseTemp(work[pendingSep+1:])
			out[rows] = Token{Start: int32(rowStart), NameLen: int32(pendingSep - rowStart), Tenths: int32(tenths)}
			rows++
			consumed = end + 1
			rowStart = consumed
			pendingSep = -1
			nl &= nl - 1
		}
	}
	return rows, consumed
}

// TokenizeAll drives a per-row tokenizer over the whole buffer.
// buf must carry OverfetchSlack readable bytes past its length, so the last row's 8-byte temperature load stays in bounds.
func TokenizeAll(fn Tokenizer, buf []byte, out []Token) int {
	work, rows := overfetchView(buf), 0
	for pos := 0; pos < len(buf) && rows < len(out); rows++ {
		tok, n := fn(work[pos:])
		if n == 0 {
			return rows
		}
		tok.Start = int32(pos)
		out[rows] = tok
		pos += n
	}
	return rows
}

// TokenizeAllBatch runs the batch kernel and finishes the tail with the scalar path, which is the shape a real reader would use at the end of its last chunk.
func TokenizeAllBatch(buf []byte, out []Token) int {
	rows, consumed := TokenizeBatch(buf, out)
	if consumed >= len(buf) || rows == len(out) {
		return rows
	}
	tail := TokenizeAll(TokenizeScalar, buf[consumed:], out[rows:])
	// TokenizeAll numbers Start from the slice it was handed, so the tail's offsets have to be lifted back into the whole buffer.
	for i := rows; i < rows+tail; i++ {
		out[i].Start += int32(consumed)
	}
	return rows + tail
}
