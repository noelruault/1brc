package main

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gen "github.com/noelruault/research/1brc/code/gen"
)

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

	path := filepath.Join(t.TempDir(), "measurements.txt")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	var got bytes.Buffer
	if err := run(path, &got); err != nil {
		t.Fatal(err)
	}

	want := referenceOutput(t, body)
	if got.String() != want {
		t.Fatalf("output differs from the reference\n got: %s\nwant: %s", got.String(), want)
	}
	// Abha's two readings are -24.7 and 0.0: the mean is exactly -12.35, a tie, and the
	// challenge rounds ties toward positive infinity, so -12.3 and not -12.4.
	if !strings.HasPrefix(got.String(), "{Abha=-24.7/-12.3/0.0, ") {
		t.Errorf("unexpected leading entry: %.40s", got.String())
	}
}

// TestAggregateRejectsWhatTheReferenceRejects guards the divergence that byte-comparing clean data cannot catch: a fast implementation that silently accepts a malformed or out-of-range line would pass the 10m gate and be wrong on any file that has one.
func TestAggregateRejectsWhatTheReferenceRejects(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"no separator", "Hamburg 12.0\n"},
		// Pins the separator rule itself: with the FIRST ';' the temperature is "b;1.0" and both
		// implementations reject the line, with the LAST ';' it parses as 1.0 and they diverge.
		{"separator inside the name", "a;b;1.0\n"},
		{"empty temperature", "Hamburg;\n"},
		{"not one decimal", "Hamburg;12\n"},
		{"two decimals", "Hamburg;12.00\n"},
		{"above range", "Hamburg;100.0\n"},
		{"below range", "Hamburg;-100.0\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, ours := aggregate(bufio.NewReader(strings.NewReader(tc.body)))
			_, theirs := gen.Aggregate(strings.NewReader(tc.body))
			if ours == nil {
				t.Fatalf("accepted %q, reference said: %v", tc.body, theirs)
			}
			if theirs == nil {
				t.Fatalf("reference accepted %q but we rejected it: %v", tc.body, ours)
			}
		})
	}
}

func TestRunReportsAMissingFile(t *testing.T) {
	if err := run(filepath.Join(t.TempDir(), "absent.txt"), &bytes.Buffer{}); err == nil {
		t.Fatal("missing input file did not produce an error")
	}
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
