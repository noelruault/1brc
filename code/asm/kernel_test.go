package asm

import (
	"bytes"
	"encoding/binary"
	"math/rand/v2"
	"testing"

	gen "github.com/noelruault/research/1brc/code/gen"
)

// adversarialNames carries the input the corpus alone cannot produce: high-bit UTF-8 bytes (real station names have them), a name longer than both windows, and 0x00/0x3A/0x3C neighbours of ';' that a sloppy compare would confuse.
var adversarialNames = []string{
	"Abéché", "Flores,  Petén", "Washington, D.C.", "Las Palmas de Gran Canaria",
	"A", "AB", "ABCDEFG", "ABCDEFGH", "ABCDEFGHI", "ABCDEFGHIJKLMNO", "ABCDEFGHIJKLMNOP", "ABCDEFGHIJKLMNOPQ",
	"\x00\x00\x00\x00\x00\x00\x00\x00", "\xff\xff\xff\xff\xff\xff\xff\xff", "::::::::", "<<<<<<<<", ":;", "\x80\x80\x80\x80\x80\x80\x80\x80\x80",
}

func indexSemicolonVariants() map[string]func([]byte) int {
	return map[string]func([]byte) int{
		"swar":   SWARIndexSemicolon,
		"neon":   NEONIndexSemicolon,
		"stdlib": func(b []byte) int { return bytes.IndexByte(b, ';') },
	}
}

// TestIndexSemicolonMatchesScalar drives every variant over the adversarial names at every alignment, because a 16-byte kernel can only be wrong at a particular offset into its window.
func TestIndexSemicolonMatchesScalar(t *testing.T) {
	var inputs [][]byte
	for _, name := range adversarialNames {
		for pad := 0; pad < 20; pad++ {
			row := append(bytes.Repeat([]byte{'x'}, pad), name...)
			inputs = append(inputs, append(row, ";12.3\n"...))
			inputs = append(inputs, append(append([]byte{}, row...), "12.3\n"...)) // no separator at all
		}
	}
	for _, in := range inputs {
		want := ScalarIndexSemicolon(in)
		for name, fn := range indexSemicolonVariants() {
			if got := fn(in); got != want {
				t.Fatalf("%s(%q) = %d, scalar = %d", name, in, got, want)
			}
		}
	}
}

// TestIndexSemicolonFuzzAgainstScalar hunts the borrow-chain false positive the SWAR trick can produce, which only shows on random high-bit content.
func TestIndexSemicolonFuzzAgainstScalar(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	buf := make([]byte, 64)
	for iter := 0; iter < 200000; iter++ {
		n := 1 + r.IntN(len(buf))
		b := buf[:n]
		for i := range b {
			switch r.IntN(4) {
			case 0:
				b[i] = byte(0x3A + r.IntN(3)) // ':' ';' '<'
			case 1:
				b[i] = byte(0x80 + r.IntN(0x80))
			default:
				b[i] = byte(r.IntN(256))
			}
		}
		want := ScalarIndexSemicolon(b)
		for name, fn := range indexSemicolonVariants() {
			if got := fn(b); got != want {
				t.Fatalf("%s(%x) = %d, scalar = %d", name, b, got, want)
			}
		}
	}
}

// TestBranchlessParseTempEveryLegalValue covers all 1999 temperatures the generator can emit, both the value and the next-line offset, at every alignment the 8-byte load can see.
func TestBranchlessParseTempEveryLegalValue(t *testing.T) {
	for v := gen.MinTenths; v <= gen.MaxTenths; v++ {
		field := append(gen.AppendTenths(nil, v), '\n')
		// The parse over-fetches up to 4 bytes past '\n'; the real loop guarantees they are readable, so pad with the bytes a next row would start with.
		in := append(append([]byte{}, field...), "Abha;1.0\n"...)
		wantTenths, wantNext, ok := ScalarParseTemp(in)
		if !ok {
			t.Fatalf("scalar rejected %q, which the generator emits", field)
		}
		if wantTenths != int64(v) || wantNext != len(field) {
			t.Fatalf("scalar disagrees with the generator on %q: %d/%d want %d/%d", field, wantTenths, wantNext, v, len(field))
		}
		gotTenths, gotNext := BranchlessParseTemp(in)
		if gotTenths != wantTenths || gotNext != wantNext {
			t.Fatalf("BranchlessParseTemp(%q) = %d/%d, scalar = %d/%d", field, gotTenths, gotNext, wantTenths, wantNext)
		}
	}
}

func TestMaskNameVariantsAgree(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))
	for iter := 0; iter < 10000; iter++ {
		w := r.Uint64()
		for sep := 0; sep <= 8; sep++ {
			want := ScalarMaskName(w, sep)
			if got := ShiftMaskName(w, sep); got != want {
				t.Fatalf("ShiftMaskName(%016x, %d) = %016x, want %016x", w, sep, got, want)
			}
			if got := LUTMaskName(w, sep); got != want {
				t.Fatalf("LUTMaskName(%016x, %d) = %016x, want %016x", w, sep, got, want)
			}
		}
	}
}

// TestSWARMatchFalsePositivesStayAboveTheFirstMatch pins the property the SWAR scan's correctness rests on, separately from the scan, so a regression names the cause instead of an index.
func TestSWARMatchFalsePositivesStayAboveTheFirstMatch(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 6))
	for iter := 0; iter < 100000; iter++ {
		var w uint64
		b := make([]byte, 8)
		for i := range b {
			if r.IntN(3) == 0 {
				b[i] = ';'
			} else {
				b[i] = byte(r.IntN(256))
			}
		}
		w = binary.LittleEndian.Uint64(b)
		m := swarMatch(w, semicolonBroadcast)
		first := bytes.IndexByte(b, ';')
		if first < 0 {
			if m != 0 {
				t.Fatalf("swarMatch(%016x) = %016x with no ';' in %x", w, m, b)
			}
			continue
		}
		if lowest := firstSetBit(m) >> 3; lowest != first {
			t.Fatalf("swarMatch(%016x) lowest lane %d, first ';' at %d in %x", w, lowest, first, b)
		}
	}
}

// TestCorpusIsTheGeneratorsStream keeps the benchmark corpus honest: if it ever stops being a stream gen.Aggregate accepts, every ns/row in 04-asm-kernels.md is measured on bytes the real binary never sees.
func TestCorpusIsTheGeneratorsStream(t *testing.T) {
	for name, c := range map[string]*Corpus{"413": Corpus413(), "10k": Corpus10k()} {
		if c.Rows != BenchRows {
			t.Fatalf("%s corpus: %d rows, want %d", name, c.Rows, BenchRows)
		}
		got, err := gen.Aggregate(bytes.NewReader(c.Bytes))
		if err != nil {
			t.Fatalf("%s corpus rejected by gen.Aggregate: %v", name, err)
		}
		if len(got) == 0 {
			t.Fatalf("%s corpus aggregated to nothing", name)
		}
	}
}

// firstSetBit is a bit-by-bit scan, so the test does not depend on the same intrinsic the kernel uses.
func firstSetBit(m uint64) int {
	for i := 0; i < 64; i++ {
		if m&(1<<i) != 0 {
			return i
		}
	}
	return 64
}
