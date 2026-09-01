// Package asm holds the competing arm64 tokenizer kernels for the 1BRC hot loop, each paired with the scalar reference it is checked against.
//
// The kernels answer three hypotheses from 03-technique-recon.md: H2 (16-byte NEON compare vs 8-byte SWAR for the name scan), H3 (merykitty's branchless temperature parse vs scalar vs NEON) and H6 (shift-computed name masks vs lookup tables, one line at a time vs interleaved).
package asm

import (
	"bytes"
	"sync"

	gen "github.com/noelruault/research/1brc/code/gen"
)

// BenchRows is the corpus size every microbenchmark uses: ~7 MB, large enough to spill L2 so a kernel cannot win on an unrealistically hot buffer, small enough that the corpus is built once per process.
const BenchRows = 1 << 19

// Corpus is a measurement stream produced by the authoritative generator, so a kernel benchmark tokenizes exactly the bytes the real binary will see.
type Corpus struct {
	Bytes []byte
	Rows  int
}

// corpusOf generates rows for the given key set. The seed is fixed so every benchmark run in the study tokenizes the identical byte stream.
func corpusOf(stations []gen.Station, rows int, seed uint64) *Corpus {
	var buf bytes.Buffer
	buf.Grow(rows * 16)
	if _, err := gen.Write(&buf, stations, int64(rows), 10.0, seed); err != nil {
		panic("asm: corpus generation failed: " + err.Error())
	}
	b := buf.Bytes()
	return &Corpus{Bytes: b, Rows: bytes.Count(b, []byte{'\n'})}
}

// Corpus413 is the official 413-station key set: mean name length 8.0 bytes, 34.1% over 8 bytes, 1.0% over 16 (03-technique-recon.md).
var Corpus413 = sync.OnceValue(func() *Corpus {
	return corpusOf(gen.Official413(), BenchRows, 413)
})

// Corpus10k is the 10k-station variant, where names are longer and the 8-byte SWAR window misses far more often.
var Corpus10k = sync.OnceValue(func() *Corpus {
	return corpusOf(gen.Synthetic10k(), BenchRows, 10000)
})
