package main

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	gen "github.com/noelruault/research/1brc/code/gen"
)

// defaults is what the binary runs with when nobody passes a flag; every test that is not about a strategy uses it, so a default that breaks correctness fails everything.
func defaults() config {
	return config{Workers: 4, BufKiB: 4096, SegKiB: 2048, Bits: 12, NoCache: false, Split: "static", Table: "combined", IO: "pread", Parse: "branchless", Kernel: "row"}
}

// strategies is every combination of the four open hypotheses' flags. Correctness must hold for all of them or a benchmark of one arm is measuring a bug.
func strategies() []config {
	var out []config
	for _, split := range []string{"static", "cursor"} {
		for _, layout := range []string{"combined", "split"} {
			for _, io := range []string{"pread", "mmap"} {
				for _, parse := range []string{"branchless", "scalar"} {
					c := defaults()
					c.Split, c.Table, c.IO, c.Parse = split, layout, io, parse
					out = append(out, c)
				}
				// The batch kernels have no scalar arm, so they are swept on their own rather than as a fifth dimension that would be half-empty.
				for _, k := range []string{"batch-swar", "batch-neon"} {
					c := defaults()
					c.Split, c.Table, c.IO, c.Kernel = split, layout, io, k
					out = append(out, c)
				}
			}
		}
	}
	return out
}

func (c config) label() string {
	return fmt.Sprintf("%s/%s/%s/%s/%s", c.Split, c.Table, c.IO, c.Parse, c.Kernel)
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
					cfg := defaults()
					cfg.Workers, cfg.BufKiB, cfg.SegKiB, cfg.Split, cfg.IO = workers, bufKiB, bufKiB, split, io
					name := fmt.Sprintf("%s/%s/workers=%d/buf=%dKiB", split, io, workers, bufKiB)
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
				cfg := defaults()
				cfg.Workers, cfg.Split, cfg.IO, cfg.BufKiB, cfg.SegKiB = workers, split, io, 1, 1
				name := fmt.Sprintf("%s/%s/workers=%d", split, io, workers)
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
	for _, split := range []bool{false, true} {
		for _, fast := range []bool{false, true} {
			tab := newTable(9, split)
			if err := tab.fold(data, kernelRow, fast, 0); err != nil {
				t.Fatalf("split=%v fast=%v: %v", split, fast, err)
			}
			got := map[string]*gen.Accumulator{}
			tab.drain(got)
			if len(got) != len(want) {
				t.Fatalf("split=%v fast=%v: %d stations, want %d", split, fast, len(got), len(want))
			}
			// One BUCKET per station, not one per (station, temperature): the fast path hashes a whole word out of the row, so a hash that forgets to mask off the bytes at and above the separator still produces the right output through drain() while filling the table with a bucket per distinct temperature.
			if tab.size != len(want) {
				t.Fatalf("split=%v fast=%v: %d buckets used for %d stations", split, fast, tab.size, len(want))
			}
			for name, w := range want {
				g := got[name]
				if g == nil {
					t.Fatalf("split=%v fast=%v: %q missing", split, fast, name)
				}
				if *g != *w {
					t.Fatalf("split=%v fast=%v: %q got %+v want %+v", split, fast, name, *g, *w)
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
	for _, split := range []bool{false, true} {
		// Eight bits is 256 buckets for 200 keys: a 78% load factor, where linear probing runs are long.
		tab := newTable(8, split)
		if err := tab.fold(data, kernelRow, true, 0); err != nil {
			t.Fatal(err)
		}
		got := map[string]*gen.Accumulator{}
		tab.drain(got)
		if len(got) != len(want) {
			t.Fatalf("split=%v: %d stations, want %d", split, len(got), len(want))
		}
	}
}

// TestTableTooSmallErrorsInsteadOfHanging pins the one failure mode a linear probe has: with every bucket occupied the probe loop has no exit, so a table smaller than the key set must return an error rather than spin forever.
func TestTableTooSmallErrorsInsteadOfHanging(t *testing.T) {
	var body bytes.Buffer
	for i := range 200 {
		fmt.Fprintf(&body, "station-%03d;1.%d\n", i, i%10)
	}
	for _, split := range []bool{false, true} {
		// Six bits is 64 buckets for 200 keys: it must fill and then fail.
		err := newTable(6, split).fold(body.Bytes(), kernelRow, true, 0)
		if err == nil {
			t.Fatalf("split=%v: a 64-bucket table accepted 200 stations", split)
		}
		if !strings.Contains(err.Error(), "buckets are occupied") {
			t.Fatalf("split=%v: wrong error: %v", split, err)
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

// TestTheDefaultOversubscribesTheCores pins the decision E-17 bought, not the number it landed on: reverting the default to one worker per core fails here.
func TestTheDefaultOversubscribesTheCores(t *testing.T) {
	cores := runtime.NumCPU()
	if got := defaultWorkers(); got <= cores {
		t.Fatalf("defaultWorkers() = %d on %d cores, want more: E-17 measured one-per-core 7.5%% slower", got, cores)
	}
}
