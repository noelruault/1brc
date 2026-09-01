package asm

import (
	"math/rand/v2"
	"testing"
)

// Every kernel here is called DIRECTLY, never through a func value.
// An indirect call costs more than the kernels it would be hiding, so a table-driven benchmark of these would be a benchmark of the call.
// That is also why the delimiter scan is ranked inside the tokenizers rather than on its own: at row scope the scan and the parse share one loop, and 02-baseline.md already measured a bare scan at memory bandwidth.

// maskWords is a small L1-resident word stream, which is the condition jerrinot's open hypothesis is about: with one line in flight there is nothing to hide a table load behind.
func maskWords() []uint64 {
	r := rand.New(rand.NewPCG(7, 8))
	words := make([]uint64, 256)
	for i := range words {
		words[i] = r.Uint64()
	}
	return words
}

// sink is a package-level store so no benchmark's work can be proved dead.
var sink uint64

func eachCorpus(b *testing.B, fn func(b *testing.B, c *Corpus)) {
	for _, tc := range []struct {
		name string
		c    *Corpus
	}{{"413", Corpus413()}, {"10k", Corpus10k()}} {
		b.Run(tc.name, func(b *testing.B) { fn(b, tc.c) })
	}
}

func reportRows(b *testing.B, c *Corpus, passes int) {
	b.SetBytes(int64(len(c.Bytes)))
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(passes*c.Rows), "ns/row")
}

// BenchmarkTokenize ranks whole-row tokenizers and, because staged-swar and staged-neon differ ONLY in the separator scan, answers H2 at row scope.
func BenchmarkTokenize(b *testing.B) {
	b.Run("scalar", func(b *testing.B) {
		eachCorpus(b, func(b *testing.B, c *Corpus) {
			work, passes, acc := overfetchView(c.Bytes), 0, int64(0)
			for b.Loop() {
				for pos := 0; pos < len(c.Bytes); {
					tok, n := TokenizeScalar(work[pos:])
					acc += int64(tok.Tenths) + int64(tok.NameLen) + int64(pos)
					pos += n
				}
				passes++
			}
			reportRows(b, c, passes)
			sink = uint64(acc)
		})
	})
	b.Run("staged-swar", func(b *testing.B) {
		eachCorpus(b, func(b *testing.B, c *Corpus) {
			work, passes, acc := overfetchView(c.Bytes), 0, int64(0)
			for b.Loop() {
				for pos := 0; pos < len(c.Bytes); {
					tok, n := TokenizeStagedSWAR(work[pos:])
					acc += int64(tok.Tenths) + int64(tok.NameLen) + int64(pos)
					pos += n
				}
				passes++
			}
			reportRows(b, c, passes)
			sink = uint64(acc)
		})
	})
	b.Run("staged-neon", func(b *testing.B) {
		eachCorpus(b, func(b *testing.B, c *Corpus) {
			work, passes, acc := overfetchView(c.Bytes), 0, int64(0)
			for b.Loop() {
				for pos := 0; pos < len(c.Bytes); {
					tok, n := TokenizeStagedNEON(work[pos:])
					acc += int64(tok.Tenths) + int64(tok.NameLen) + int64(pos)
					pos += n
				}
				passes++
			}
			reportRows(b, c, passes)
			sink = uint64(acc)
		})
	})
	// The batch kernel drains into an L2-resident token buffer, the shape a two-phase design would use; a corpus-sized token array would measure memory traffic instead of the kernel.
	b.Run("batch", func(b *testing.B) {
		eachCorpus(b, func(b *testing.B, c *Corpus) {
			out := make([]Token, 4096)
			passes, acc := 0, int64(0)
			for b.Loop() {
				for pos := 0; pos < len(c.Bytes); {
					rows, consumed := TokenizeBatch(c.Bytes[pos:], out)
					if consumed == 0 {
						break
					}
					for _, t := range out[:rows] {
						acc += int64(t.Tenths) + int64(t.NameLen) + int64(t.Start)
					}
					pos += consumed
				}
				passes++
			}
			reportRows(b, c, passes)
			sink = uint64(acc)
		})
	})
}

// BenchmarkParseTemp is H3: the separator scan is held fixed at SWAR in both loops, so the spread is the parse.
func BenchmarkParseTemp(b *testing.B) {
	b.Run("scalar", func(b *testing.B) {
		eachCorpus(b, func(b *testing.B, c *Corpus) {
			work, passes, acc := overfetchView(c.Bytes), 0, int64(0)
			for b.Loop() {
				for pos := 0; pos < len(c.Bytes); {
					sep := SWARIndexSemicolon(work[pos:])
					t, next, _ := ScalarParseTemp(work[pos+sep+1:])
					acc += t
					pos += sep + 1 + next
				}
				passes++
			}
			reportRows(b, c, passes)
			sink = uint64(acc)
		})
	})
	b.Run("branchless", func(b *testing.B) {
		eachCorpus(b, func(b *testing.B, c *Corpus) {
			work, passes, acc := overfetchView(c.Bytes), 0, int64(0)
			for b.Loop() {
				for pos := 0; pos < len(c.Bytes); {
					sep := SWARIndexSemicolon(work[pos:])
					t, next := BranchlessParseTemp(work[pos+sep+1:])
					acc += t
					pos += sep + 1 + next
				}
				passes++
			}
			reportRows(b, c, passes)
			sink = uint64(acc)
		})
	})
}

// BenchmarkNEONTransferFloor adds one 16-byte compare, one narrow and one vector-to-general move per row to the staged-swar loop, and nothing else.
// Its delta over BenchmarkTokenize/staged-swar is what a per-row NEON kernel pays before doing any useful work, which is what bounds a NEON temperature parse (H3).
func BenchmarkNEONTransferFloor(b *testing.B) {
	eachCorpus(b, func(b *testing.B, c *Corpus) {
		work, passes, acc := overfetchView(c.Bytes), 0, uint64(0)
		for b.Loop() {
			for pos := 0; pos < len(c.Bytes); {
				acc ^= neonTransferProbe(work[pos:])
				sep := SWARIndexSemicolon(work[pos:])
				_, next := BranchlessParseTemp(work[pos+sep+1:])
				pos += sep + 1 + next
			}
			passes++
		}
		reportRows(b, c, passes)
		sink = acc
	})
}

// BenchmarkMaskName is H6: the chain runs acc -> word -> separator position -> mask -> acc, which is the real loop's dependency, so a table lookup's address is not known early.
// The position is kept in [1,8] because a zero mask would break the chain and hand the table a predictable address on those iterations.
func BenchmarkMaskName(b *testing.B) {
	words := maskWords()
	b.Run("shift/single", func(b *testing.B) {
		acc := uint64(0)
		for b.Loop() {
			for i := range words {
				w := words[i] ^ acc
				acc = ShiftMaskName(w, int(w&7)+1)
			}
		}
		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*len(words)), "ns/row")
		sink = acc
	})
	b.Run("lut/single", func(b *testing.B) {
		acc := uint64(0)
		for b.Loop() {
			for i := range words {
				w := words[i] ^ acc
				acc = LUTMaskName(w, int(w&7)+1)
			}
		}
		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*len(words)), "ns/row")
		sink = acc
	})
	b.Run("shift/interleaved2", func(b *testing.B) {
		x, y := uint64(0), uint64(1)
		for b.Loop() {
			for i := 0; i+1 < len(words); i += 2 {
				wx, wy := words[i]^x, words[i+1]^y
				x = ShiftMaskName(wx, int(wx&7)+1)
				y = ShiftMaskName(wy, int(wy&7)+1)
			}
		}
		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*len(words)), "ns/row")
		sink = x ^ y
	})
	b.Run("lut/interleaved2", func(b *testing.B) {
		x, y := uint64(0), uint64(1)
		for b.Loop() {
			for i := 0; i+1 < len(words); i += 2 {
				wx, wy := words[i]^x, words[i+1]^y
				x = LUTMaskName(wx, int(wx&7)+1)
				y = LUTMaskName(wy, int(wy&7)+1)
			}
		}
		b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*len(words)), "ns/row")
		sink = x ^ y
	})
}
