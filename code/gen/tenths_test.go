package gen

import (
	"fmt"
	"math"
	"regexp"
	"testing"
	"unicode/utf16"
)

var oneDecimal = regexp.MustCompile(`^-?\d+\.\d$`)

// Every legal temperature must print with exactly one fractional digit and never as "-0.0"; those are the two shape guarantees the output format rests on.
func TestAppendTenthsShape(t *testing.T) {
	for v := minTenths; v <= maxTenths; v++ {
		got := string(AppendTenths(nil, v))
		if !oneDecimal.MatchString(got) {
			t.Fatalf("AppendTenths(%d) = %q, want -?\\d+\\.\\d", v, got)
		}
		if got == "-0.0" {
			t.Fatalf("AppendTenths(%d) produced -0.0, which no correct output contains", v)
		}
		if back, ok := ParseTenths([]byte(got)); !ok || back != v {
			t.Fatalf("ParseTenths(%q) = %d, %v; want %d, true", got, back, ok, v)
		}
	}
}

func TestAppendTenthsKnownValues(t *testing.T) {
	for _, tc := range []struct {
		in   Tenths
		want string
	}{
		{0, "0.0"}, {-1, "-0.1"}, {1, "0.1"}, {-999, "-99.9"}, {999, "99.9"},
		{100, "10.0"}, {-100, "-10.0"}, {99, "9.9"}, {-99, "-9.9"},
	} {
		if got := string(AppendTenths(nil, tc.in)); got != tc.want {
			t.Errorf("AppendTenths(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Regression: a two-digit assumption here printed 1000 tenths as ":0.0", corrupting output instead of failing.
func TestAppendTenthsBeyondLegalRange(t *testing.T) {
	for _, tc := range []struct {
		in   Tenths
		want string
	}{
		{1000, "100.0"}, {-1000, "-100.0"}, {5000, "500.0"}, {12345, "1234.5"}, {-12345, "-1234.5"},
	} {
		got := string(AppendTenths(nil, tc.in))
		if got != tc.want {
			t.Errorf("AppendTenths(%d) = %q, want %q", tc.in, got, tc.want)
		}
		if !oneDecimal.MatchString(got) {
			t.Errorf("AppendTenths(%d) = %q, not a one-decimal number", tc.in, got)
		}
	}
}

// The challenge mandates roundTowardPositive (README.md:426). Go's math.Round is half-away-from-zero, so it must NOT be used; this pins both the correct answer and the fact that the obvious stdlib call is the wrong one.
func TestRoundToTenthsTiesTowardPositive(t *testing.T) {
	for _, tc := range []struct {
		in            float64
		want          Tenths
		mathRoundWant Tenths
	}{
		{-2.45, -24, -25},    // -2.45*10 is exactly -24.5: a true tie, and the two rules disagree
		{-24.55, -245, -246}, // likewise
		{2.45, 25, 25},       // positive ties agree
		// -0.05 looks like a tie and is not: the nearest float64 to -0.05 is slightly below it, so -0.05*10 is -0.5000000000000000277 and both rules round to -0.1. Whether a decimal is a tie depends on its float64 representation, not on how it is written.
		{-0.05, -1, -1},
		{0.05, 1, 1},
	} {
		if got := RoundToTenths(tc.in); got != tc.want {
			t.Errorf("RoundToTenths(%v) = %d, want %d", tc.in, got, tc.want)
		}
		if got := Tenths(math.Round(tc.in * 10.0)); got != tc.mathRoundWant {
			t.Errorf("math.Round(%v*10) = %d, want %d (guards the premise of this test)", tc.in, got, tc.mathRoundWant)
		}
	}
}

// Measured divergence: formatting a float64 mean with %.1f, the obvious Go approach, emits "-0.0" where the reference emits "0.0".
func TestFloatFormattingEmitsNegativeZero(t *testing.T) {
	if got := fmt.Sprintf("%.1f", -0.04); got != "-0.0" {
		t.Fatalf(`%%.1f of -0.04 = %q, want "-0.0" (premise of the integer-tenths design)`, got)
	}
	if got := string(AppendTenths(nil, RoundToTenths(-0.04))); got != "0.0" {
		t.Fatalf("integer path gave %q, want \"0.0\"", got)
	}
}

// Java orders station names by UTF-16 code unit; Go's sort.Strings orders by
// UTF-8 byte. They agree across the BMP and disagree above U+FFFF, because a supplementary character encodes as a high surrogate (0xD800-0xDBFF) in UTF-16 but as bytes 0xF0+ in UTF-8. Pinned so a future "arbitrary station name" test does not read the divergence as a bug in our sort.
func TestUTF16OrderDivergesAboveBMP(t *testing.T) {
	bmp, supp := "￿-station", "\U0001F525station"

	if !(bmp < supp) {
		t.Fatalf("UTF-8 byte order: expected %q < %q", bmp, supp)
	}
	if !(compareUTF16(bmp, supp) > 0) {
		t.Fatalf("UTF-16 order: expected %q > %q", bmp, supp)
	}
	for _, pair := range [][2]string{{"Abha", "Abidjan"}, {"Abidjan", "Abéché"}, {"Zagreb", "Zürich"}} {
		byteOrder, u16 := pair[0] < pair[1], compareUTF16(pair[0], pair[1]) < 0
		if byteOrder != u16 {
			t.Errorf("BMP pair %q/%q: byte order %v, UTF-16 order %v; expected agreement", pair[0], pair[1], byteOrder, u16)
		}
	}
}

func compareUTF16(a, b string) int {
	x, y := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	for i := 0; i < len(x) && i < len(y); i++ {
		if x[i] != y[i] {
			return int(x[i]) - int(y[i])
		}
	}
	return len(x) - len(y)
}

func TestParseTenthsRejectsMalformed(t *testing.T) {
	for _, in := range []string{"", "-", "1", "1.", ".1", "1.23", "1,2", "abc", "-.1", "1.a", "--1.0", "1.0 "} {
		if got, ok := ParseTenths([]byte(in)); ok {
			t.Errorf("ParseTenths(%q) = %d, true; want rejected", in, got)
		}
	}
}

// Mean reproduces round(round(sum)/count) from the reference implementation.
func TestMean(t *testing.T) {
	for _, tc := range []struct {
		sum   Tenths
		count int64
		want  Tenths
	}{
		{180, 10, 18},   // 18.0 over 10 rows -> 1.8
		{0, 1, 0},       // no -0.0
		{-1, 1, -1},     // -0.1
		{5, 2, 3},       // 0.5/2 = 0.25 -> tie at 2.5 tenths, toward positive -> 0.3
		{-5, 2, -2},     // -0.25 -> tie at -2.5 tenths, toward positive -> -0.2
		{-999, 1, -999}, // range edge
		{999, 1, 999},
	} {
		if got := Mean(tc.sum, tc.count); got != tc.want {
			t.Errorf("Mean(%d, %d) = %d, want %d", tc.sum, tc.count, got, tc.want)
		}
	}
}
