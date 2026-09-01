package gen

import (
	"bytes"
	"strings"
	"testing"
)

func TestOfficialStationSetMatchesUpstream(t *testing.T) {
	s := Official413()
	if len(s) != 413 {
		t.Fatalf("got %d stations, want 413 (CreateMeasurements.java, commit db06419)", len(s))
	}
	if s[0].Name != "Abha" || s[0].Mean != 18.0 {
		t.Errorf("first station = %+v, want {Abha 18.0}", s[0])
	}
	if last := s[len(s)-1]; last.Name != "Zürich" || last.Mean != 9.3 {
		t.Errorf("last station = %+v, want {Zürich 9.3}", last)
	}
	assertLegalNames(t, s)
}

func TestSynthetic10kIsALegalMaximalKeySet(t *testing.T) {
	s := Synthetic10k()
	if len(s) != 10000 {
		t.Fatalf("got %d stations, want 10000 (the rules' maximum, README.md:423)", len(s))
	}
	seen := make(map[string]bool, len(s))
	maxLen := 0
	multibyte := 0
	for _, st := range s {
		if seen[st.Name] {
			t.Fatalf("duplicate station name %q", st.Name)
		}
		seen[st.Name] = true
		if len(st.Name) > maxLen {
			maxLen = len(st.Name)
		}
		if len(st.Name) != len([]rune(st.Name)) {
			multibyte++
		}
	}
	assertLegalNames(t, s)
	if maxLen < 90 {
		t.Errorf("longest name is %d bytes; the set is meant to press against the 100-byte limit", maxLen)
	}
	if multibyte == 0 {
		t.Error("no multi-byte names; the set is meant to exercise UTF-8 decoding")
	}
}

// The rules constrain names to 1..100 bytes with no ';' and no '\n'
// (README.md:421). A generator that violates them produces a file that is not a valid instance of the challenge.
func assertLegalNames(t *testing.T, stations []Station) {
	t.Helper()
	for _, st := range stations {
		switch {
		case len(st.Name) == 0:
			t.Error("empty station name")
		case len(st.Name) > 100:
			t.Errorf("station %q is %d bytes, over the 100-byte limit", st.Name, len(st.Name))
		case strings.ContainsAny(st.Name, ";\n"):
			t.Errorf("station %q contains ';' or '\\n'", st.Name)
		}
	}
}

// The whole reason this generator exists rather than upstream's is that the file must be re-derivable from a recorded command.
func TestWriteIsReproducibleAndSeedDependent(t *testing.T) {
	var a, b, c bytes.Buffer
	if _, err := Write(&a, Official413(), 5000, 10.0, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(&b, Official413(), 5000, 10.0, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(&c, Official413(), 5000, 10.0, 2); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Error("same seed produced different bytes; the file is not re-derivable")
	}
	if bytes.Equal(a.Bytes(), c.Bytes()) {
		t.Error("different seeds produced identical bytes; the seed is not wired through")
	}
}

func TestWrittenRowsSatisfyTheRules(t *testing.T) {
	var buf bytes.Buffer
	n, err := Write(&buf, Official413(), 20000, 10.0, 7)
	if err != nil {
		t.Fatal(err)
	}
	if n != int64(buf.Len()) {
		t.Errorf("reported %d bytes written, buffer holds %d", n, buf.Len())
	}
	out := buf.Bytes()
	if out[len(out)-1] != '\n' {
		t.Error("file does not end with a newline")
	}
	lines := bytes.Split(out[:len(out)-1], []byte("\n"))
	if len(lines) != 20000 {
		t.Fatalf("got %d lines, want 20000", len(lines))
	}
	known := make(map[string]bool)
	for _, s := range Official413() {
		known[s.Name] = true
	}
	for i, line := range lines {
		sep := bytes.IndexByte(line, ';')
		if sep < 0 {
			t.Fatalf("line %d %q has no ';'", i, line)
		}
		if !known[string(line[:sep])] {
			t.Fatalf("line %d: unknown station %q", i, line[:sep])
		}
		v, ok := ParseTenths(line[sep+1:])
		if !ok {
			t.Fatalf("line %d: %q is not a one-decimal temperature", i, line[sep+1:])
		}
		if v < minTenths || v > maxTenths {
			t.Fatalf("line %d: %d tenths is outside [-99.9, 99.9]", i, v)
		}
	}
}

// The clamp is unreachable on the official means (the bound sits beyond 6 sigma),
// so it is exercised here with a station parked at the edge. Without this, the clamp would be untested code claiming a guarantee.
func TestWriteClampsToLegalRange(t *testing.T) {
	var buf bytes.Buffer
	extreme := []Station{{Name: "Edge", Mean: 500.0}, {Name: "Cold", Mean: -500.0}}
	if _, err := Write(&buf, extreme, 2000, 10.0, 3); err != nil {
		t.Fatal(err)
	}
	sawHigh, sawLow := false, false
	for _, line := range bytes.Split(bytes.TrimRight(buf.Bytes(), "\n"), []byte("\n")) {
		v, ok := ParseTenths(line[bytes.IndexByte(line, ';')+1:])
		if !ok {
			t.Fatalf("unparseable line %q", line)
		}
		if v < minTenths || v > maxTenths {
			t.Fatalf("clamp failed: %q is outside [-99.9, 99.9]", line)
		}
		sawHigh = sawHigh || v == maxTenths
		sawLow = sawLow || v == minTenths
	}
	if !sawHigh || !sawLow {
		t.Errorf("clamp never engaged (high=%v low=%v); the test no longer covers it", sawHigh, sawLow)
	}
}

func TestAggregateAndWriteResult(t *testing.T) {
	in := strings.Join([]string{
		"Abha;-23.0",
		"Zürich;9.3",
		"Abha;59.2",
		"Abéché;-0.1",
		"Abha;18.0",
		"Abéché;0.0",
	}, "\n") + "\n"

	stations, err := Aggregate(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := WriteResult(&out, stations); err != nil {
		t.Fatal(err)
	}
	// Abha: min -23.0, max 59.2, sum 54.2 over 3 -> 18.066... -> 18.1
	// Abéché: min -0.1, max 0.0, sum -0.1 over 2. The exact quotient -0.05 is a tie, but float64 cannot hold it: -0.1/2 is a hair below -0.05, so the result is -0.1 rather than -0.0. The reference implementation's arithmetic chain lands in the same place, which is the point of reproducing it operation for operation instead of computing the "mathematically right" answer.
	want := "{Abha=-23.0/18.1/59.2, Abéché=-0.1/-0.1/0.0, Zürich=9.3/9.3/9.3}\n"
	if got := out.String(); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestAggregateRejectsMalformedInput(t *testing.T) {
	// "12.0" is the case that matters: it has no separator but IS a valid temperature, so only the separator check stops it. Without that check the name slice goes negative and the reference panics.
	for _, in := range []string{"no-separator\n", "12.0\n", "Abha;abc\n", "Abha;1\n", "Abha;\n"} {
		if _, err := Aggregate(strings.NewReader(in)); err == nil {
			t.Errorf("Aggregate(%q) returned no error; malformed input must not be silently aggregated", in)
		}
	}
}

// Regression: ParseTenths checks shape, not magnitude, so out-of-range values were aggregated into a plausible-looking result instead of stopping the run.
func TestAggregateRejectsOutOfRangeTemperatures(t *testing.T) {
	for _, in := range []string{"Abha;500.0\n", "Abha;-100.0\n", "Abha;100.0\n"} {
		if _, err := Aggregate(strings.NewReader(in)); err == nil {
			t.Errorf("Aggregate(%q) returned no error; the value is outside the legal range", in)
		}
	}
	for _, in := range []string{"Abha;99.9\n", "Abha;-99.9\n", "Abha;0.0\n"} {
		if _, err := Aggregate(strings.NewReader(in)); err != nil {
			t.Errorf("Aggregate(%q) rejected a legal boundary value: %v", in, err)
		}
	}
}

// End to end: what the generator writes, the reference must read back without complaint, with every station accounted for.
func TestGeneratedFileAggregatesCleanly(t *testing.T) {
	var buf bytes.Buffer
	if _, err := Write(&buf, Official413(), 50000, 10.0, 11); err != nil {
		t.Fatal(err)
	}
	stations, err := Aggregate(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if len(stations) != 413 {
		t.Errorf("aggregated %d stations from a 50k-row file, want all 413", len(stations))
	}
	total := int64(0)
	for _, a := range stations {
		total += a.Count
		if a.Min > a.Max {
			t.Errorf("min %d exceeds max %d", a.Min, a.Max)
		}
	}
	if total != 50000 {
		t.Errorf("counted %d rows, want 50000", total)
	}
	var out bytes.Buffer
	if err := WriteResult(&out, stations); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.HasPrefix(s, "{") || !strings.HasSuffix(s, "}\n") {
		t.Errorf("output is not the TreeMap shape: %.40q...", s)
	}
	if strings.Contains(s, "-0.0") {
		t.Error(`output contains "-0.0", which the reference never emits`)
	}
}
