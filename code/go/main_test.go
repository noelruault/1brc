package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unsafe"

	gen "github.com/noelruault/research/1brc/code/gen"
)

// defaults is what the binary runs with when nobody passes a flag, and the parse and the fold READ the shipped constants rather than restating them.
// They used to be restated and drifted: `branchless` stayed here after E-25 made `word` the default, so the two range-boundary sweeps — the only tests that find a byte-offset off-by-one — ran a parse the binary does not.
// Workers, buffer and bits stay small on purpose: those are about test speed, not about the shipped shape.
func defaults() config {
	return config{Workers: 4, BufKiB: 4096, SegKiB: 2048, Bits: 12, NoCache: false, Split: "static", Table: "combined", IO: "pread", Parse: defaultParse, Kernel: "row", Fold: defaultFold, Fill: defaultFill}
}

// strategies is every combination of the four open hypotheses' flags. Correctness must hold for all of them or a benchmark of one arm is measuring a bug.
func strategies() []config {
	var out []config
	for _, split := range []string{"static", "cursor"} {
		for _, layout := range []string{"combined", "split", "quot"} {
			for _, io := range []string{"pread", "mmap"} {
				// The parse sweep names -fold slice because the pointer arms have no arm for any parse but word, which is the guard in aggregateFile.
				for _, parse := range []string{"branchless", "scalar", "word"} {
					c := defaults()
					c.Split, c.Table, c.IO, c.Parse, c.Fold = split, layout, io, parse, "slice"
					out = append(out, c)
				}
				// The batch kernels have no scalar arm, so they are swept on their own rather than as a fifth dimension that would be half-empty.
				for _, k := range []string{"batch-swar", "batch-neon"} {
					c := defaults()
					c.Split, c.Table, c.IO, c.Parse, c.Kernel, c.Fold = split, layout, io, "branchless", k, "slice"
					out = append(out, c)
				}
				// The -fold arms are word-parse only by the guard in aggregateFile, so they are swept beside the parses rather than crossed with them.
				for _, fold := range []string{"hash", "ptr", "both"} {
					c := defaults()
					c.Split, c.Table, c.IO, c.Parse, c.Fold = split, layout, io, "word", fold
					out = append(out, c)
				}
				// The staged readers replace foldRange, so they are swept as their own arms; mmap has no read to stage and aggregateFile refuses the pair.
				if io == "pread" {
					for _, fill := range []string{"sync", "ahead"} {
						c := defaults()
						c.Split, c.Table, c.IO, c.Fill = split, layout, io, fill
						out = append(out, c)
					}
				}
			}
		}
	}
	return out
}

func (c config) label() string {
	return fmt.Sprintf("%s/%s/%s/%s/%s/%s/fill=%s", c.Split, c.Table, c.IO, c.Parse, c.Kernel, c.Fold, c.Fill)
}

// TestRunMatchesTheReferenceByteForByte is the whole correctness contract in miniature:
// the same input through this binary and through gen.Aggregate must produce identical bytes. check-correctness.sh runs the same comparison over 10,000,000 rows.
func TestRunMatchesTheReferenceByteForByte(t *testing.T) {
	body := strings.Join([]string{
		"Hamburg;12.0",
		"Washington, D.C.;-0.1",
		"Abha;-24.7",
		"Hamburg;-99.9",
		"Flores,  Petén;99.9",
		"Abha;0.0",
		"Hamburg;13.1",
		"Washington, D.C.;-0.0",
	}, "\n") + "\n"

	path := writeFile(t, body)
	want := referenceOutput(t, body)
	for _, cfg := range strategies() {
		t.Run(cfg.label(), func(t *testing.T) {
			var got bytes.Buffer
			if err := run(path, cfg, &got); err != nil {
				t.Fatal(err)
			}
			if got.String() != want {
				t.Fatalf("output differs from the reference\n got: %s\nwant: %s", got.String(), want)
			}
		})
	}
	// Abha's two readings are -24.7 and 0.0: the mean is exactly -12.35, a tie, and the challenge rounds ties toward positive infinity, so -12.3 and not -12.4.
	if !strings.HasPrefix(want, "{Abha=-24.7/-12.3/0.0, ") {
		t.Errorf("unexpected leading entry: %.40s", want)
	}
}

// TestEveryRangeBoundaryIsFoldedExactlyOnce is the test this design most needs: work is split on raw byte offsets, so a row straddling a boundary can be dropped or counted twice, and either bug is invisible on a file whose rows happen to align.
// Sweeping worker and buffer counts against a generated file puts a boundary inside a row at many different places.
func TestEveryRangeBoundaryIsFoldedExactlyOnce(t *testing.T) {
	var buf bytes.Buffer
	if _, err := gen.Write(&buf, gen.Official413(), 20000, 10.0, 7); err != nil {
		t.Fatal(err)
	}
	body := buf.String()
	path := writeFile(t, body)
	want := referenceOutput(t, body)

	for _, workers := range []int{1, 2, 3, 7, 15, 64, 257} {
		for _, bufKiB := range []int{1, 2, 7, 64} {
			for _, split := range []string{"static", "cursor"} {
				for _, io := range []string{"pread", "mmap"} {
					// The staged readers own the same byte-offset arithmetic as foldRange and get it from a separate loop, so they are swept here or the four traps are only guarded on one of the two. mmap refuses -fill, so it stays on the unstaged arm.
					fills := []string{"off"}
					if io == "pread" {
						fills = append(fills, "sync", "ahead")
					}
					// Both row loops, because they compute the row's address differently and this sweep is the only test that finds a byte-offset off-by-one. `hash` and `both` share these two loops, so two arms cover four.
					for _, fold := range []string{"slice", "ptr"} {
						for _, fill := range fills {
							cfg := defaults()
							cfg.Workers, cfg.BufKiB, cfg.SegKiB, cfg.Split, cfg.IO, cfg.Fold, cfg.Fill = workers, bufKiB, bufKiB, split, io, fold, fill
							name := fmt.Sprintf("%s/%s/%s/fill=%s/workers=%d/buf=%dKiB", split, io, fold, fill, workers, bufKiB)
							t.Run(name, func(t *testing.T) {
								var got bytes.Buffer
								if err := run(path, cfg, &got); err != nil {
									t.Fatal(err)
								}
								if got.String() != want {
									t.Fatalf("%s: output differs from the reference", name)
								}
							})
						}
					}
				}
			}
		}
	}
}

// TestARangeShorterThanARowOwnsNothing covers the degenerate end of the split: with more workers than rows, a range can contain no row start at all, and a range that folds "its" first row anyway counts a row that belongs to a later range.
func TestARangeShorterThanARowOwnsNothing(t *testing.T) {
	var body bytes.Buffer
	for i := range 10 {
		fmt.Fprintf(&body, "station-%d;%d.%d\n", i, i, i)
	}
	path := writeFile(t, body.String())
	want := referenceOutput(t, body.String())
	for _, workers := range []int{11, 40, 200, 1000} {
		for _, split := range []string{"static", "cursor"} {
			for _, io := range []string{"pread", "mmap"} {
				fills := []string{"off"}
				if io == "pread" {
					fills = append(fills, "sync", "ahead")
				}
				for _, fill := range fills {
					cfg := defaults()
					cfg.Workers, cfg.Split, cfg.IO, cfg.BufKiB, cfg.SegKiB, cfg.Fill = workers, split, io, 1, 1, fill
					name := fmt.Sprintf("%s/%s/fill=%s/workers=%d", split, io, fill, workers)
					t.Run(name, func(t *testing.T) {
						var got bytes.Buffer
						if err := run(path, cfg, &got); err != nil {
							t.Fatal(err)
						}
						if got.String() != want {
							t.Fatalf("%s:\n got: %s\nwant: %s", name, got.String(), want)
						}
					})
				}
			}
		}
	}
}

// TestFoldRejectsWhatTheReferenceRejects guards the divergence that byte-comparing clean data cannot catch: a fast implementation that silently accepts a malformed or out-of-range line would pass the 10m gate and be wrong on any file that has one.
//
// Each case runs TWICE: alone, where the row is inside the scalar tail, and followed by enough valid rows to push it into the branchless fast path. A check that only exists on one of the two paths is the bug this catches.
func TestFoldRejectsWhatTheReferenceRejects(t *testing.T) {
	padding := strings.Repeat("Hamburg;12.0\n", 40)
	for _, tc := range []struct{ name, body string }{
		{"no separator", "Hamburg 12.0\n"},
		// Pins the separator rule itself: with the FIRST ';' the temperature is "b;1.0" and both implementations reject the line, with the LAST ';' it parses as 1.0 and they diverge.
		{"separator inside the name", "a;b;1.0\n"},
		{"empty temperature", "Hamburg;\n"},
		{"not one decimal", "Hamburg;12\n"},
		{"two decimals", "Hamburg;12.00\n"},
		{"above range", "Hamburg;100.0\n"},
		{"below range", "Hamburg;-100.0\n"},
	} {
		for _, where := range []struct{ name, body string }{
			{"tail-path", tc.body},
			{"fast-path", tc.body + padding},
		} {
			t.Run(tc.name+"/"+where.name, func(t *testing.T) {
				theirs := referenceError(where.body)
				if theirs == nil {
					t.Fatalf("the reference accepted %q; this test's premise is wrong", tc.body)
				}
				path := writeFile(t, where.body)
				for _, cfg := range strategies() {
					if err := run(path, cfg, &bytes.Buffer{}); err == nil {
						t.Fatalf("%s accepted %q, reference said: %v", cfg.label(), tc.body, theirs)
					}
				}
			})
		}
	}
}

// TestHashPathsAgree pins the invariant that keeps the two fold paths from splitting one station across two buckets: the word-based hash and the byte-based hash must return the same value for the same name.
func TestHashPathsAgree(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for n := 1; n <= 40; n++ {
		for range 200 {
			name := make([]byte, n)
			for i := range name {
				name[i] = byte(1 + r.IntN(255))
				if name[i] == ';' || name[i] == '\n' {
					name[i] = 'x'
				}
			}
			row := append(append([]byte{}, name...), ";1.0\n"...)
			// The fast path reads a whole word from the row, so the bytes after the name are in it and must not reach the hash.
			for len(row) < 8 {
				row = append(row, 'Z')
			}
			w := uint64(0)
			for i := 7; i >= 0; i-- {
				w = w<<8 | uint64(row[i])
			}
			if got, want := hashWord(w, n), hashName(name); got != want {
				t.Fatalf("n=%d name=%q: hashWord=%#x hashName=%#x", n, name, got, want)
			}
			// The quotiented layout stores the masked word AS the bucket's key, so the two paths agreeing on the hash is no longer enough: disagreeing on the word would give one station two buckets, and the tail path is where the second one would come from.
			if got, want := maskWord(w, n), maskName(name); got != want {
				t.Fatalf("n=%d name=%q: maskWord=%#x maskName=%#x", n, name, got, want)
			}
		}
	}
}

// TestBranchlessParseMatchesTheReference runs every legal temperature through both parses and through the authoritative parser in gen, at both alignments the fold can hand them.
func TestBranchlessParseMatchesTheReference(t *testing.T) {
	for v := int(gen.MinTenths); v <= int(gen.MaxTenths); v++ {
		field := string(gen.AppendTenths(nil, gen.Tenths(v))) + "\n"
		padded := field + strings.Repeat("x", 16)
		gotFast, nextFast := parseTempBranchless([]byte(padded))
		gotScalar, nextScalar, ok := parseTempScalar([]byte(padded))
		if !ok {
			t.Fatalf("%q: scalar parse rejected it", field)
		}
		if int(gotFast) != v || int(gotScalar) != v {
			t.Fatalf("%q: branchless=%d scalar=%d want %d", field, gotFast, gotScalar, v)
		}
		if nextFast != len(field) || nextScalar != len(field) {
			t.Fatalf("%q: next branchless=%d scalar=%d want %d", field, nextFast, nextScalar, len(field))
		}
		if ref, ok := gen.ParseTenths([]byte(field[:len(field)-1])); !ok || int(ref) != v {
			t.Fatalf("%q: gen.ParseTenths disagrees: %d %v", field, ref, ok)
		}
	}
}

// TestParseTempWordAcceptsEveryLegalTemperature is the shape half of queue item 16: the word check must accept all 1,999 values the generator can write, with the same tenths and the same row end as the parse it folds in.
func TestParseTempWordAcceptsEveryLegalTemperature(t *testing.T) {
	for v := int(gen.MinTenths); v <= int(gen.MaxTenths); v++ {
		field := string(gen.AppendTenths(nil, gen.Tenths(v))) + "\n"
		padded := []byte(field + strings.Repeat("x", 16))
		got, next, ok := parseTempWord(padded)
		if !ok || int(got) != v || next != len(field) {
			t.Fatalf("%q: parseTempWord = (%d, %d, %v), want (%d, %d, true)", field, got, next, ok, v, len(field))
		}
	}
}

// TestParseTempWordIgnoresTheBytesPastTheField measures the claim the pointer walk's over-read guard rests on, rather than reasoning it: every lane at or past next is masked out of all four rejections, so what the 8-byte load picks up beyond a field cannot change the answer.
// That is why an unbounded load in foldRowsPtr produces the right output and is still a bug — the guard is memory safety, and no output test can stand in for it.
func TestParseTempWordIgnoresTheBytesPastTheField(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 11))
	for v := int(gen.MinTenths); v <= int(gen.MaxTenths); v++ {
		field := string(gen.AppendTenths(nil, gen.Tenths(v))) + "\n"
		var b [8]byte
		copy(b[:], field)
		base := binary.LittleEndian.Uint64(b[:])
		wantV, wantNext, wantOK := parseTempWordFrom(base)
		if !wantOK || wantNext != len(field) {
			t.Fatalf("%q: parseTempWordFrom = (%d, %d, %v), want next %d and ok", field, wantV, wantNext, wantOK, len(field))
		}
		keep := ^uint64(0) >> (64 - 8*wantNext)
		for range 20 {
			got, next, ok := parseTempWordFrom(base&keep | r.Uint64()&^keep)
			if got != wantV || next != wantNext || ok != wantOK {
				t.Fatalf("%q: lanes past the field changed the answer: (%d,%d,%v) not (%d,%d,%v)", field, got, next, ok, wantV, wantNext, wantOK)
			}
		}
	}
}

// TestParseTempWordMatchesTheByteCheck is the differential test queue item 16 turns on: parseTempWord replaces parseTempBranchless plus validTemp, so it may not accept or reject a single field they would not.
//
// The sweep is exhaustive over 6-byte fields in the alphabet the shape is built from, so the adversarial cases are proved rather than spot-checked: a name carrying the separator ("b;1.0"), a three-digit temperature ("100.0"), a sign with no digit, a doubled dot, a truncated field and a field with no '\n' are all inside it, as are the two lanes above the row that neither check may read.
// Both fillers must agree, which is what shows the over-read bytes change no verdict; the accepted count is pinned because a mutant that rejects everything passes every comparison in the loop.
func TestParseTempWordMatchesTheByteCheck(t *testing.T) {
	alphabet := []byte{'-', '0', '1', '9', '.', '\n', ';', ':', 0x00, 'b'}
	field := make([]byte, 8)
	checked, accepted := 0, 0
	var sweep func(i int, filler byte)
	sweep = func(i int, filler byte) {
		if i == 6 {
			field[6], field[7] = filler, filler
			wantV, wantNext := parseTempBranchless(field)
			wantOK := wantNext != 0 && validTemp(field, wantNext)
			gotV, gotNext, gotOK := parseTempWord(field)
			checked++
			if gotOK != wantOK {
				t.Fatalf("%q (filler %#02x): parseTempWord ok=%v, parseTempBranchless+validTemp ok=%v", field[:6], filler, gotOK, wantOK)
			}
			// The incumbent pair is what this arm replaces, so agreeing with it is necessary but not independent; parseTempScalar is a separate implementation of the same shape and is the oracle the board asked for.
			if scalarV, scalarNext, scalarOK := parseTempScalar(field); scalarOK != wantOK || (wantOK && (scalarV != wantV || scalarNext != wantNext)) {
				t.Fatalf("%q: parseTempScalar = (%d, %d, %v), parseTempBranchless+validTemp = (%d, %d, %v)", field[:6], scalarV, scalarNext, scalarOK, wantV, wantNext, wantOK)
			}
			if wantOK {
				accepted++
				if gotV != wantV || gotNext != wantNext {
					t.Fatalf("%q: parseTempWord = (%d, %d), parseTempBranchless = (%d, %d)", field[:6], gotV, gotNext, wantV, wantNext)
				}
			}
			return
		}
		for _, c := range alphabet {
			field[i] = c
			sweep(i+1, filler)
		}
	}
	for _, filler := range []byte{0xFF, 0x00} {
		sweep(0, filler)
	}
	// 2 x 10^6 fields, of which 2 x 1287 are temperatures: 9 shapes of `D.D\n` x 100 free trailing bytes, 36 of `-D.D\n` and `DD.D\n` x 10, and 27 of `-DD.D\n`, over the 3 digits in the alphabet.
	if checked != 2_000_000 || accepted != 2_574 {
		t.Fatalf("swept %d fields and accepted %d, want 2000000 and 2574", checked, accepted)
	}
}

// TestIndexDelimMatchesTheObviousScan covers the dual-needle SWAR scan at every length and offset around its 8-byte window, and specifically the case it exists for: a '\n' before the ';' must be reported as the first delimiter, not skipped.
func TestIndexDelimMatchesTheObviousScan(t *testing.T) {
	obvious := func(b []byte) (int, bool) {
		for i, c := range b {
			if c == ';' || c == '\n' {
				return i, c == ';'
			}
		}
		return -1, false
	}
	for n := range 40 {
		base := bytes.Repeat([]byte("a"), n)
		if got, _ := indexDelim(base); got != -1 {
			t.Fatalf("n=%d no delimiter: got %d", n, got)
		}
		for i := range n {
			for _, c := range []byte{';', '\n'} {
				b := append([]byte{}, base...)
				b[i] = c
				gotIdx, gotSemi := indexDelim(b)
				wantIdx, wantSemi := obvious(b)
				if gotIdx != wantIdx || gotSemi != wantSemi {
					t.Fatalf("n=%d i=%d c=%q: got (%d,%v) want (%d,%v)", n, i, c, gotIdx, gotSemi, wantIdx, wantSemi)
				}
			}
			// Both needles present, newline first: the shape a semicolon-only scan folds into the next row.
			if i+1 < n {
				b := append([]byte{}, base...)
				b[i], b[i+1] = '\n', ';'
				if gotIdx, gotSemi := indexDelim(b); gotIdx != i || gotSemi {
					t.Fatalf("n=%d i=%d newline before semicolon: got (%d,%v)", n, i, gotIdx, gotSemi)
				}
			}
		}
	}
}

// TestTableLayoutsAgree checks the two H5 layouts against each other and against the reference over the 413-station key set, so the layout benchmark is pricing identical work.
func TestTableLayoutsAgree(t *testing.T) {
	var buf bytes.Buffer
	if _, err := gen.Write(&buf, gen.Official413(), 5000, 10.0, 11); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	want, err := gen.Aggregate(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	for _, layout := range tableKinds() {
		for _, pk := range []parseKind{parseScalar, parseBranchless, parseWord} {
			tab := newTable(9, layout)
			if err := tab.fold(data, kernelRow, pk, foldSlice, 0); err != nil {
				t.Fatalf("layout=%d parse=%d: %v", layout, pk, err)
			}
			got := map[string]*gen.Accumulator{}
			tab.drain(got)
			if len(got) != len(want) {
				t.Fatalf("layout=%d parse=%d: %d stations, want %d", layout, pk, len(got), len(want))
			}
			// One BUCKET per station, not one per (station, temperature): the fast path hashes a whole word out of the row, so a hash that forgets to mask off the bytes at and above the separator still produces the right output through drain() while filling the table with a bucket per distinct temperature.
			if tab.size != len(want) {
				t.Fatalf("layout=%d parse=%d: %d buckets used for %d stations", layout, pk, tab.size, len(want))
			}
			for name, w := range want {
				g := got[name]
				if g == nil {
					t.Fatalf("layout=%d parse=%d: %q missing", layout, pk, name)
				}
				if *g != *w {
					t.Fatalf("layout=%d parse=%d: %q got %+v want %+v", layout, pk, name, *g, *w)
				}
			}
		}
	}
}

// TestTableProbesPastAFullBucketRun forces the linear probe to wrap and to walk a long run of occupied buckets, which the 413-station fixtures never do at nine bits.
func TestTableProbesPastAFullBucketRun(t *testing.T) {
	var body bytes.Buffer
	const stations = 200
	for i := range stations {
		fmt.Fprintf(&body, "station-%03d;%d.%d\n", i, i%90, i%10)
	}
	data := body.Bytes()
	want, err := gen.Aggregate(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	for _, layout := range tableKinds() {
		// Eight bits is 256 buckets for 200 keys: a 78% load factor, where linear probing runs are long.
		tab := newTable(8, layout)
		if err := tab.fold(data, kernelRow, parseBranchless, foldSlice, 0); err != nil {
			t.Fatal(err)
		}
		got := map[string]*gen.Accumulator{}
		tab.drain(got)
		if len(got) != len(want) {
			t.Fatalf("layout=%d: %d stations, want %d", layout, len(got), len(want))
		}
	}
}

// TestTableTooSmallErrorsInsteadOfHanging pins the one failure mode a linear probe has: with every bucket occupied the probe loop has no exit, so a table smaller than the key set must return an error rather than spin forever.
func TestTableTooSmallErrorsInsteadOfHanging(t *testing.T) {
	var body bytes.Buffer
	for i := range 200 {
		fmt.Fprintf(&body, "station-%03d;1.%d\n", i, i%10)
	}
	for _, layout := range tableKinds() {
		// Six bits is 64 buckets for 200 keys: it must fill and then fail.
		err := newTable(6, layout).fold(body.Bytes(), kernelRow, parseBranchless, foldSlice, 0)
		if err == nil {
			t.Fatalf("layout=%d: a 64-bucket table accepted 200 stations", layout)
		}
		if !strings.Contains(err.Error(), "buckets are occupied") {
			t.Fatalf("layout=%d: wrong error: %v", layout, err)
		}
	}
}

func TestRunReportsBadInput(t *testing.T) {
	cfg := defaults()
	if err := run(filepath.Join(t.TempDir(), "absent.txt"), cfg, &bytes.Buffer{}); err == nil {
		t.Fatal("missing input file did not produce an error")
	}
	if err := run(writeFile(t, "Hamburg;12.0"), cfg, &bytes.Buffer{}); err == nil {
		t.Fatal("input without a trailing newline did not produce an error")
	}
	for _, bad := range []config{
		{Workers: 0}, {Workers: 1, BufKiB: 0}, {Workers: 1, BufKiB: 4096, Parse: "quantum"},
		{Workers: 1, BufKiB: 4096, Parse: "scalar", IO: "carrier-pigeon"},
		{Workers: 1, BufKiB: 4096, Parse: "scalar", IO: "pread", Split: "vibes"},
		{Workers: 1, BufKiB: 4096, Parse: "scalar", IO: "pread", Split: "cursor", SegKiB: 0},
	} {
		if err := run(writeFile(t, "Hamburg;12.0\n"), bad, &bytes.Buffer{}); err == nil {
			t.Fatalf("%+v was accepted", bad)
		}
	}

	// These two start from a valid config and change ONE field, because every case above leaves -kernel empty and is rejected by that check whatever else is wrong with it.
	unknownFill, mappedFill := defaults(), defaults()
	unknownFill.Fill = "eventually"
	// A mapping has no read to stage: accepting this would time foldMapped and publish it as H-14's arm.
	mappedFill.IO, mappedFill.Fill = "mmap", "ahead"
	for _, bad := range []config{unknownFill, mappedFill} {
		if err := run(writeFile(t, "Hamburg;12.0\n"), bad, &bytes.Buffer{}); err == nil {
			t.Fatalf("%+v was accepted", bad)
		}
	}
}

func writeFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "measurements.txt")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func referenceOutput(t *testing.T, body string) string {
	t.Helper()
	stations, err := gen.Aggregate(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var want bytes.Buffer
	if err := gen.WriteResult(&want, stations); err != nil {
		t.Fatal(err)
	}
	return want.String()
}

func referenceError(body string) error {
	_, err := gen.Aggregate(strings.NewReader(body))
	return err
}

// TestTheDefaultReadBufferIsTheSweptMinimum pins the reversal E-23 and E-24 bought: 4 MiB was the default on E-06's slot-biased row, and reverting to it fails here.
// The neighbours are named because the sweep is what makes 1 MiB a minimum rather than a preference — 512 KiB and 2 MiB both lost, disjoint, on either side of it.
func TestTheDefaultReadBufferIsTheSweptMinimum(t *testing.T) {
	if defaultBufKiB != 1024 {
		t.Fatalf("defaultBufKiB = %d, want 1024: E-24 measured 4 MiB +6.56%%, 2 MiB +4.44%% and 512 KiB +3.97%% against it, all disjoint", defaultBufKiB)
	}
}

// TestTheDefaultParseFoldsTheCheckIntoTheWord pins E-25: reverting the default to the byte check costs the 4.97% of wall clock and 6.33% of user CPU that arm measured, disjoint, inside a 0.067% bracket.
// It pins the DEFAULT rather than the function, because parseTempBranchless and validTemp both stay in the binary as the arm this was measured against.
func TestTheDefaultParseFoldsTheCheckIntoTheWord(t *testing.T) {
	if defaultParse != "word" {
		t.Fatalf("defaultParse = %q, want \"word\": E-25 measured the byte check at 1.498 s and 1.499 s against 1.424 s, disjoint", defaultParse)
	}
	if pk, err := parseMode(defaultParse); err != nil || pk != parseWord {
		t.Fatalf("parseMode(%q) = (%d, %v), want (parseWord, nil)", defaultParse, pk, err)
	}
}

// TestTheDefaultFoldIsThePointerWalk pins E-27, and pins the arm it did NOT adopt as well: `both` does strictly less work per row and was measured 0.81% slower on wall while 0.11% cheaper on user CPU, ranges overlapping, so the fuse did not separate and the pre-registered bar was disjoint-or-not-kept.
func TestTheDefaultFoldIsThePointerWalk(t *testing.T) {
	if defaultFold != "ptr" {
		t.Fatalf("defaultFold = %q, want \"ptr\": E-27 measured the slice walk at 1.398 s and 1.399 s against 1.233 s, disjoint, in a 0.072%% bracket", defaultFold)
	}
	if fk, err := foldMode(defaultFold); err != nil || fk != foldPtr {
		t.Fatalf("foldMode(%q) = (%d, %v), want (foldPtr, nil)", defaultFold, fk, err)
	}
}

// TestTheDefaultReaderIsUnstagedPinsAKill is the one shape of default-pin this file did not have: the others pin an arm that WON, this pins the incumbent an arm failed to beat.
// E-29 measured `ahead` at +0.57%/+1.57% of wall against the two bracket slots with the ranges overlapping both, and `ahead` against `sync` — the same loop, differing by the fill goroutine alone — at +0.24%, so the overlap itself is what measured zero.
func TestTheDefaultReaderIsUnstagedPinsAKill(t *testing.T) {
	if defaultFill != "off" {
		t.Fatalf("defaultFill = %q, want \"off\": E-29 measured -fill ahead at 1.232 s against 1.225 s and 1.213 s, overlapping both, and -fill sync at 1.229 s", defaultFill)
	}
	if flk, err := fillMode(defaultFill); err != nil || flk != fillOff {
		t.Fatalf("fillMode(%q) = (%d, %v), want (fillOff, nil)", defaultFill, flk, err)
	}
}

// TestTheDefaultOversubscribesTheCores pins the decision E-17 bought, not the number it landed on: reverting the default to one worker per core fails here.
func TestTheDefaultOversubscribesTheCores(t *testing.T) {
	cores := runtime.NumCPU()
	if got := defaultWorkers(); got <= cores {
		t.Fatalf("defaultWorkers() = %d on %d cores, want more: E-17 measured one-per-core 7.5%% slower", got, cores)
	}
}

// TestIndexDelimAtAgreesWithIndexDelim runs the fused scan over the same corpus as the incumbent's own test and demands the same answer, plus the word it exists to hand back.
// The corpus places each needle at every offset, because a scan that peels its first word can only get the peel wrong at one of them.
func TestIndexDelimAtAgreesWithIndexDelim(t *testing.T) {
	check := func(b []byte) {
		t.Helper()
		wantIdx, wantSemi := indexDelim(b)
		gotIdx, gotSemi, w0 := indexDelimAt(unsafe.Pointer(unsafe.SliceData(b)), len(b))
		if gotIdx != wantIdx || gotSemi != wantSemi {
			t.Fatalf("%q: indexDelimAt = (%d,%v), indexDelim = (%d,%v)", b, gotIdx, gotSemi, wantIdx, wantSemi)
		}
		var want uint64
		if len(b) >= 8 {
			want = binary.LittleEndian.Uint64(b)
		}
		if w0 != want {
			t.Fatalf("%q: w0 = %#x, want %#x", b, w0, want)
		}
	}
	for n := range 40 {
		base := bytes.Repeat([]byte("a"), n)
		check(base)
		for i := range n {
			for _, c := range []byte{';', '\n'} {
				b := append([]byte{}, base...)
				b[i] = c
				check(b)
			}
			if i+1 < n {
				b := append([]byte{}, base...)
				b[i], b[i+1] = '\n', ';'
				check(b)
			}
		}
	}
}

// foldArm folds data with one -fold arm and returns the reference's own rendering of the result, so two arms are compared on the bytes the binary would print rather than on map identity.
//
// The bucket count comes back too, because drain merges by NAME: an arm that hashes one station two ways still prints the right answer and only shows up as extra entries in the table.
func foldArm(t *testing.T, fk foldKind, data []byte) (string, int, error) {
	t.Helper()
	tab := newTable(12, tableCombined)
	if err := tab.fold(data, kernelRow, parseWord, fk, 0); err != nil {
		return "", 0, err
	}
	got := map[string]*gen.Accumulator{}
	tab.drain(got)
	var out bytes.Buffer
	if err := gen.WriteResult(&out, got); err != nil {
		t.Fatal(err)
	}
	return out.String(), tab.size, nil
}

// TestFoldArmsAgree is the sibling check every new row splitter in this repo owes before it ships: the three -fold arms must fold and REJECT byte for byte what the incumbent loop does, and agree with the reference where it accepts.
// A ';' inside a name is in the corpus deliberately — that divergence has shipped three times here and no generated key set can express it — and so are two names sharing all eight hashed bytes, which is the only input that exercises the full compare the pointer walk builds with unsafe.Slice.
func TestFoldArmsAgree(t *testing.T) {
	pad := strings.Repeat("Hamburg;12.0\n", 24)
	for _, tc := range []struct{ name, body string }{
		{"clean", pad},
		{"separator inside the name", pad + "a;b;1.0\n" + pad},
		{"no separator at all", pad + "Hamburg 12.0\n" + pad},
		{"empty name", pad + ";1.0\n" + pad},
		{"names either side of the eight-byte hash", pad + "a;1.0\nabcdefg;2.0\nabcdefgh;3.0\nabcdefghi;4.0\n" + pad},
		{"two names sharing all eight hashed bytes", pad + "AbcdefgH1;1.0\nAbcdefgH2;-1.0\nAbcdefgH1;3.0\n" + pad},
		// One short name with four different temperatures is the only shape that catches a hash reading past the name: the output is right either way and the table holds four entries instead of one.
		{"the range ends", pad + "Abha;-99.9\nAbha;99.9\nAbha;0.0\nAbha;-0.0\n" + pad},
		{"out of range", pad + "Hamburg;100.0\n" + pad},
		{"two decimals", pad + "Hamburg;12.00\n" + pad},
		// A name past 119 bytes is the only input that makes the fast path's over-read guard fire and hand the row to the scalar tail; nothing shorter can reach it under maxRow.
		{"a name longer than the over-read guard allows", pad + strings.Repeat("L", 200) + ";1.0\n" + pad},
		// The same row LAST, where the guard is the only thing between the 8-byte load and the end of the chunk. Dropping it makes the slice arms panic; the pointer arm reads past the buffer in silence, which is why the guard carries a comment instead of a test.
		{"a long name at the very end of the chunk", pad + strings.Repeat("L", 200) + ";1.0\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := []byte(tc.body)
			want, wantSize, wantErr := foldArm(t, foldSlice, data)
			for _, fk := range []foldKind{foldHash, foldPtr, foldBoth} {
				got, gotSize, gotErr := foldArm(t, fk, data)
				if (gotErr == nil) != (wantErr == nil) {
					t.Fatalf("fold %d: err %v, incumbent err %v", fk, gotErr, wantErr)
				}
				if gotErr != nil && gotErr.Error() != wantErr.Error() {
					t.Fatalf("fold %d: err %q, incumbent err %q", fk, gotErr, wantErr)
				}
				if got != want {
					t.Fatalf("fold %d:\n got %q\nwant %q", fk, got, want)
				}
				if gotSize != wantSize {
					t.Fatalf("fold %d used %d buckets, the incumbent used %d: the two hashed the same name differently", fk, gotSize, wantSize)
				}
			}
			if wantErr != nil {
				if referenceError(tc.body) == nil {
					t.Fatalf("every arm rejected %q and the reference accepted it", tc.name)
				}
				return
			}
			if ref := referenceOutput(t, tc.body); ref != want {
				t.Fatalf("every arm agreed on %q and the reference says %q", want, ref)
			}
		})
	}
}

// tableKinds is every layout the binary can be asked for: correctness must hold for all of them or a benchmark of one is measuring a bug.
func tableKinds() []tableKind { return []tableKind{tableCombined, tableSplit, tableQuot} }

// TestQuotEntryIsThirtyTwoBytes pins H-13's whole premise. The hypothesis is that the probed array shrinks from 48 bytes per bucket to 32; a field added without shrinking another turns the arm into a measurement of nothing.
func TestQuotEntryIsThirtyTwoBytes(t *testing.T) {
	if got := unsafe.Sizeof(qentry{}); got != 32 {
		t.Fatalf("qentry is %d bytes, want 32 (entry is %d)", got, unsafe.Sizeof(entry{}))
	}
}

// TestQuotSeparatesNamesOneWordCannotTell is the test the generated corpus cannot be: no key set produces a name with a NUL byte, and the 413 set has no two names sharing eight bytes and a length.
// Both cases collide in the inline word alone, so this is what distinguishes the quotiented probe's two guards from a word compare that would merge two stations into one bucket.
func TestQuotSeparatesNamesOneWordCannotTell(t *testing.T) {
	cases := []struct {
		what string
		body string
	}{
		// Same first eight bytes, same length, different tail: only the full compare through keys can tell these apart.
		{"shared 8-byte prefix", "Villaviciosa1;1.0\nVillaviciosa2;2.0\nVillaviciosa1;3.0\n"},
		// Nine bytes is the shortest length the word cannot hold, so it is where the long/short boundary is decided rather than merely crossed.
		// The 413 set cannot express it: its one 8-byte-prefix collision is Alexandra/Alexandria, which nlen separates by length.
		{"shared 8-byte prefix at nine bytes", "Alexandra;1.0\nAlexandro;2.0\nAlexandra;3.0\n"},
		// "ab" and "ab\x00" mask to the same word; only the length separates them.
		{"NUL-padded short name", "ab;1.0\nab\x00;2.0\nab;3.0\n"},
		// A one-byte name against the empty name, which masks to zero exactly like an empty bucket.
		{"empty name", ";1.0\na;2.0\n;3.0\n"},
	}
	for _, c := range cases {
		data := []byte(c.body)
		want, err := gen.Aggregate(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("%s: reference: %v", c.what, err)
		}
		for _, layout := range tableKinds() {
			tab := newTable(9, layout)
			if err := tab.fold(data, kernelRow, parseWord, foldSlice, 0); err != nil {
				t.Fatalf("%s layout=%d: %v", c.what, layout, err)
			}
			got := map[string]*gen.Accumulator{}
			tab.drain(got)
			if len(got) != len(want) {
				t.Fatalf("%s layout=%d: %d stations, want %d", c.what, layout, len(got), len(want))
			}
			for name, w := range want {
				if g := got[name]; g == nil || *g != *w {
					t.Fatalf("%s layout=%d: %q got %+v want %+v", c.what, layout, name, g, w)
				}
			}
		}
	}
}

// TestUnknownTableLayoutIsRejected pins the guard that keeps a mistyped arm from being measured: before it, any string but "split" ran the incumbent, so `-table quto` would have published the incumbent's wall clock under the arm's name.
func TestUnknownTableLayoutIsRejected(t *testing.T) {
	path := writeFile(t, "a;1.0\n")
	for _, layout := range []string{"quto", "Quot", "", "combined "} {
		c := defaults()
		c.Table = layout
		if _, err := aggregateFile(path, c); err == nil {
			t.Fatalf("-table %q was accepted", layout)
		} else if !strings.Contains(err.Error(), "unknown -table") {
			t.Fatalf("-table %q: wrong error: %v", layout, err)
		}
	}
}
