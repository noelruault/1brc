package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"

	gen "github.com/noelruault/research/1brc/code/gen"
)

var allKernels = []struct {
	name string
	k    kernel
}{
	{"row", kernelRow},
	{"batch-swar", kernelBatchSWAR},
	{"batch-neon", kernelBatchNEON},
}

// TestZeroByteMaskIsExact pins the reason foldBatchSWAR does not reuse indexDelim's cheaper mask.
// indexDelim's `(w-low) &^ w & high` is only correct at its LOWEST set bit; a batch kernel drains every bit, so a mask with a false positive above a real match would report a row boundary that is not there.
func TestZeroByteMaskIsExact(t *testing.T) {
	want := func(w uint64) uint64 {
		var m uint64
		for i := range 8 {
			if byte(w>>(8*i)) == 0 {
				m |= 0x80 << (8 * i)
			}
		}
		return m
	}
	r := rand.New(rand.NewPCG(1, 2))
	for i := range 200000 {
		w := r.Uint64()
		// Bias hard towards words that contain zero and 0x01 bytes, which is where the borrow chain misbehaves.
		if i%2 == 0 {
			var b [8]byte
			for j := range b {
				b[j] = []byte{0x00, 0x01, 0x80, 0xFF, ';', '\n', 'a'}[r.IntN(7)]
			}
			w = binary.LittleEndian.Uint64(b[:])
		}
		if got := zeroByteMask(w); got != want(w) {
			t.Fatalf("zeroByteMask(%#016x) = %#016x, want %#016x", w, got, want(w))
		}
	}
}

// TestBorrowChainMaskWouldBeWrongHere is the negative control for the test above: it shows the cheap mask really does set bits at non-matching bytes, so the choice of the exact form is load-bearing rather than defensive.
func TestBorrowChainMaskWouldBeWrongHere(t *testing.T) {
	cheap := func(w uint64) uint64 {
		x := w ^ semicolonBroadcast
		return (x - swarLow) &^ x & swarHigh
	}
	// ';' xors to 0x00 and borrows; ':' xors to 0x01, so it lands on 0xFF after the borrow and survives the &^, which is a set bit for a byte that is not a separator.
	w := binary.LittleEndian.Uint64([]byte(";:aaaaaa"))
	exact := zeroByteMask(w ^ semicolonBroadcast)
	if exact != 0x80 {
		t.Fatalf("the exact mask marks %#016x for %q, want only lane 0", exact, ";:aaaaaa")
	}
	if cheap(w) == exact {
		t.Fatalf("the cheap mask agreed with the exact one on the word chosen to separate them, so this test proves nothing")
	}
}

// foldWith folds data with one kernel and returns the drained aggregate plus the number of buckets used.
func foldWith(t *testing.T, k kernel, data []byte) (map[string]*gen.Accumulator, int, error) {
	t.Helper()
	tab := newTable(12, tableCombined)
	if err := tab.fold(data, k, parseBranchless, foldSlice, 0); err != nil {
		return nil, 0, err
	}
	got := map[string]*gen.Accumulator{}
	tab.drain(got)
	return got, tab.size, nil
}

// TestKernelsAgreeWithTheReference folds the same rows with every kernel at every alignment a window can land on, because the batch kernels carry state across window boundaries and a row that straddles one is the shape a single-alignment test never builds.
func TestKernelsAgreeWithTheReference(t *testing.T) {
	var buf bytes.Buffer
	if _, err := gen.Write(&buf, gen.Official413(), 3000, 10.0, 7); err != nil {
		t.Fatal(err)
	}
	body := buf.Bytes()

	// Prepending one row of total length 6..46 slides every later row across both the 8-byte and the 32-byte window grid.
	for pad := 6; pad <= 46; pad++ {
		prefix := fmt.Appendf(nil, "%s;1.0\n", strings.Repeat("Z", pad-5))
		if len(prefix) != pad {
			t.Fatalf("pad=%d: built a %d-byte prefix row", pad, len(prefix))
		}
		data := append(append([]byte{}, prefix...), body...)
		want, err := gen.Aggregate(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("pad=%d: %v", pad, err)
		}
		for _, kk := range allKernels {
			got, size, err := foldWith(t, kk.k, data)
			if err != nil {
				t.Fatalf("pad=%d %s: %v", pad, kk.name, err)
			}
			if len(got) != len(want) {
				t.Fatalf("pad=%d %s: %d stations, want %d", pad, kk.name, len(got), len(want))
			}
			if size != len(want) {
				t.Fatalf("pad=%d %s: %d buckets used for %d stations", pad, kk.name, size, len(want))
			}
			for name, w := range want {
				g := got[name]
				if g == nil {
					t.Fatalf("pad=%d %s: %q missing", pad, kk.name, name)
				}
				if *g != *w {
					t.Fatalf("pad=%d %s: %q got %+v want %+v", pad, kk.name, name, *g, *w)
				}
			}
		}
	}
}

// TestKernelsRejectExactlyWhatTheReferenceRejects is the test the corpus cannot express.
// gen.Write never emits a name containing ';', a row without a separator or an out-of-range temperature, so every agreement test above passes with any of these three handled differently by one kernel.
func TestKernelsRejectExactlyWhatTheReferenceRejects(t *testing.T) {
	cases := []struct {
		name string
		row  string
	}{
		{"semicolon inside the name", "Ab;cd;1.0\n"},
		{"no separator at all", "AbcdefghijklmnopqrstuvwX1.0\n"},
		// A separator-less line that is ITSELF a valid temperature. With no pending separator the batch drain would parse from data[0] and then slice the name to a negative bound, so the guard that rejects this is what stands between a malformed file and a panic.
		{"a bare temperature and no station", "12.3\n"},
		{"temperature above the legal range", "Abcdefghij;150.0\n"},
		{"temperature below the legal range", "Abcdefghij;-150.0\n"},
		{"no decimal digit", "Abcdefghij;12\n"},
		{"two decimal digits", "Abcdefghij;12.34\n"},
	}
	// Padding keeps the bad row inside the batch loop's range rather than in the scalar tail, which is the only place the batch drain is exercised at all.
	// Both placements are tried because the batch drain reaches for data[pendingSep+1:], and a first row makes that data[0:] — a different, and unsafe, byte to land on.
	pad := strings.Repeat("Padstation;1.0\n", 8)
	for _, c := range cases {
		for _, placement := range []struct {
			where string
			data  []byte
		}{
			{"first row", []byte(c.row + pad + pad)},
			{"mid buffer", []byte(pad + c.row + pad)},
		} {
			if _, err := gen.Aggregate(bytes.NewReader(placement.data)); err == nil {
				t.Fatalf("%s/%s: the reference ACCEPTED %q, so this case tests nothing", c.name, placement.where, c.row)
			}
			for _, kk := range allKernels {
				if _, _, err := foldWith(t, kk.k, placement.data); err == nil {
					t.Fatalf("%s/%s: %s accepted %q, which the reference rejects", c.name, placement.where, kk.name, c.row)
				}
			}
		}
	}
}

// TestBatchKernelsEndTheNameAtTheFIRSTSemicolon pins the divergence this study has now shipped three times.
// It asserts the name BOUNDARY through the aggregate: a row whose name legitimately ends at the first ';' is accepted, and the station it lands under is the short name, never the long one.
func TestBatchKernelsEndTheNameAtTheFIRSTSemicolon(t *testing.T) {
	// "Ab" is a station; the trailing ";cd" makes the temperature field unparseable, so every kernel must REJECT. A kernel taking the LAST ';' would see name "Ab;cd" and a clean "1.0", and accept.
	data := []byte(strings.Repeat("Padstation;1.0\n", 8) + "Ab;cd;1.0\n" + strings.Repeat("Padstation;1.0\n", 8))
	for _, kk := range allKernels {
		got, _, err := foldWith(t, kk.k, data)
		if err == nil {
			t.Fatalf("%s accepted a row whose first field is %q: it ended the name at the LAST ';', got %v", kk.name, "Ab", got)
		}
	}
}

// TestBatchKernelsHandOffToTheTailExactlyOnce folds every prefix of a file that ends on a row boundary, which walks the handoff point between a batch loop and foldTail across every row and every window offset.
// A handoff that is off by one row either drops that row or folds it twice, and both show up as a count mismatch against the reference.
func TestBatchKernelsHandOffToTheTailExactlyOnce(t *testing.T) {
	var body bytes.Buffer
	for i := range 60 {
		fmt.Fprintf(&body, "st%d;%d.%d\n", i%7, i%50, i%10)
	}
	data := body.Bytes()

	ends := []int{}
	for i, b := range data {
		if b == '\n' {
			ends = append(ends, i+1)
		}
	}
	for _, end := range ends {
		prefix := data[:end]
		want, err := gen.Aggregate(bytes.NewReader(prefix))
		if err != nil {
			t.Fatalf("end=%d: %v", end, err)
		}
		for _, kk := range allKernels {
			got, _, err := foldWith(t, kk.k, prefix)
			if err != nil {
				t.Fatalf("end=%d %s: %v", end, kk.name, err)
			}
			if len(got) != len(want) {
				t.Fatalf("end=%d %s: %d stations, want %d", end, kk.name, len(got), len(want))
			}
			for name, w := range want {
				if g := got[name]; g == nil || *g != *w {
					t.Fatalf("end=%d %s: %q got %+v want %+v", end, kk.name, name, g, *w)
				}
			}
		}
	}
}

// TestBatchKernelRejectsAScalarParse pins the flag combination that would otherwise measure the wrong thing: a batch arm always parses branchlessly, so -parse scalar with it is a lie the benchmark would record as a result.
func TestBatchKernelRejectsAScalarParse(t *testing.T) {
	for _, name := range []string{"batch-swar", "batch-neon"} {
		cfg := config{Workers: 1, BufKiB: 1024, Bits: 12, Split: "static", Table: "combined", IO: "pread", Parse: "scalar", Kernel: name, Fold: "slice", Fill: defaultFill}
		_, err := aggregateFile("does-not-matter", cfg)
		if err == nil || !strings.Contains(err.Error(), "no scalar parse arm") {
			t.Fatalf("-kernel %s -parse scalar: got %v, want the no-scalar-arm error", name, err)
		}
	}
}
