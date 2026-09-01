package asm

import (
	"testing"
)

func tokenizerVariants() map[string]Tokenizer {
	return map[string]Tokenizer{
		"staged-swar": TokenizeStagedSWAR,
		"staged-neon": TokenizeStagedNEON,
		"scalar":      TokenizeScalar,
	}
}

// TestTokenizersMatchTheScalarReference is the correctness gate every ns/row in 04-asm-kernels.md rests on: a kernel that is fast and wrong is a bug, not a result (spec.md:36).
func TestTokenizersMatchTheScalarReference(t *testing.T) {
	for cname, c := range map[string]*Corpus{"413": Corpus413(), "10k": Corpus10k()} {
		want := referenceTokens(t, c)
		for kname, fn := range tokenizerVariants() {
			got := make([]Token, c.Rows)
			if n := TokenizeAll(fn, c.Bytes, got); n != c.Rows {
				t.Fatalf("%s/%s: tokenized %d rows, corpus has %d", cname, kname, n, c.Rows)
			}
			assertTokensEqual(t, cname+"/"+kname, got, want)
		}
		got := make([]Token, c.Rows)
		if n := TokenizeAllBatch(c.Bytes, got); n != c.Rows {
			t.Fatalf("%s/batch: tokenized %d rows, corpus has %d", cname, n, c.Rows)
		}
		assertTokensEqual(t, cname+"/batch", got, want)
	}
}

// TestTokenizeBatchLeavesTheTailToItsCaller pins the batch kernel's contract, which TokenizeAllBatch depends on: it stops on a short window rather than reading past it.
func TestTokenizeBatchLeavesTheTailToItsCaller(t *testing.T) {
	c := Corpus413()
	out := make([]Token, c.Rows)
	rows, consumed := TokenizeBatch(c.Bytes, out)
	if rows == 0 || rows > c.Rows {
		t.Fatalf("batch wrote %d rows of %d", rows, c.Rows)
	}
	if consumed > len(c.Bytes) || consumed == 0 {
		t.Fatalf("batch consumed %d of %d bytes", consumed, len(c.Bytes))
	}
	if c.Bytes[consumed-1] != '\n' {
		t.Fatalf("batch stopped mid-row: byte %d is %q, want a newline", consumed-1, c.Bytes[consumed-1])
	}
	// TestBatchBenchLoopCoversEveryRow's driver is the benchmark's; this one only checks the single-call contract.
	// A caller with room for fewer rows than the buffer holds must get exactly that many, and no write past its slice.
	short := make([]Token, 7)
	if n, _ := TokenizeBatch(c.Bytes, short); n != len(short) {
		t.Fatalf("batch with room for %d wrote %d", len(short), n)
	}
}

// TestCorpusCarriesOverfetchSlack pins the precondition every kernel's 8- and 16-byte loads rely on. Without it the last row reads past the buffer.
func TestCorpusCarriesOverfetchSlack(t *testing.T) {
	for name, c := range map[string]*Corpus{"413": Corpus413(), "10k": Corpus10k()} {
		if cap(c.Bytes)-len(c.Bytes) < OverfetchSlack {
			t.Fatalf("%s corpus: %d bytes of slack, want %d", name, cap(c.Bytes)-len(c.Bytes), OverfetchSlack)
		}
	}
}

func referenceTokens(t *testing.T, c *Corpus) []Token {
	t.Helper()
	out := make([]Token, 0, c.Rows)
	for pos := 0; pos < len(c.Bytes); {
		sep := ScalarIndexSemicolon(c.Bytes[pos:])
		if sep < 0 {
			t.Fatalf("reference: no ';' at offset %d", pos)
		}
		tenths, next, ok := ScalarParseTemp(c.Bytes[pos+sep+1:])
		if !ok {
			t.Fatalf("reference: bad temperature at offset %d", pos+sep+1)
		}
		out = append(out, Token{Start: int32(pos), NameLen: int32(sep), Tenths: int32(tenths)})
		pos += sep + 1 + next
	}
	if len(out) != c.Rows {
		t.Fatalf("reference tokenized %d rows, corpus has %d", len(out), c.Rows)
	}
	return out
}

func assertTokensEqual(t *testing.T, label string, got, want []Token) {
	t.Helper()
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s: row %d = %+v, reference %+v", label, i, got[i], want[i])
		}
	}
}

// TestBatchBenchLoopCoversEveryRow reproduces BenchmarkTokenize/batch's exact driver loop and asserts it tokenizes the whole corpus against the reference.
// reportRows divides by c.Rows, so a driver that stopped early would publish a per-row cost for work it never did.
func TestBatchBenchLoopCoversEveryRow(t *testing.T) {
	for name, c := range map[string]*Corpus{"413": Corpus413(), "10k": Corpus10k()} {
		want := referenceTokens(t, c)
		out := make([]Token, 4096)
		seen := 0
		for pos := 0; pos < len(c.Bytes); {
			rows, consumed := TokenizeBatch(c.Bytes[pos:], out)
			if consumed == 0 {
				break
			}
			for i, got := range out[:rows] {
				got.Start += int32(pos)
				if got != want[seen+i] {
					t.Fatalf("%s: row %d = %+v, reference %+v", name, seen+i, got, want[seen+i])
				}
			}
			seen += rows
			pos += consumed
		}
		if seen != c.Rows {
			t.Fatalf("%s: batch driver tokenized %d rows, corpus has %d (%.2f%% of the work the ns/row divisor assumes)", name, seen, c.Rows, 100*float64(seen)/float64(c.Rows))
		}
	}
}
