package gen

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"sort"
)

// Accumulator holds one station's running aggregate, entirely in integer tenths.
type Accumulator struct {
	Min, Max Tenths
	Sum      Tenths
	Count    int64
}

// Aggregate is the trivially-correct reference: read every line, split on the
// first ';', accumulate. It is deliberately the slow, obvious implementation.
// Its only job is to be right, so the fast contenders have something to be
// checked against; anything clever in here would be a place for a bug to hide.
//
// Station names are compared as raw bytes. For the BMP characters the challenge's
// data sets use, byte order over UTF-8 matches Java's UTF-16 code-unit order, so
// the emitted order is the reference order. They diverge only above U+FFFF; see
// 01-definition.md.
func Aggregate(r io.Reader) (map[string]*Accumulator, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<20)

	stations := make(map[string]*Accumulator, 16384)
	line := int64(0)
	for sc.Scan() {
		line++
		b := sc.Bytes()
		i := bytes.IndexByte(b, ';')
		if i < 0 {
			return nil, fmt.Errorf("line %d: no ';' separator", line)
		}
		t, ok := ParseTenths(b[i+1:])
		if !ok {
			return nil, fmt.Errorf("line %d: %q is not a one-decimal temperature", line, b[i+1:])
		}
		// ParseTenths validates shape, not magnitude. The range is a rule about
		// the data (README.md:422), so it is enforced here where the line number
		// is known, rather than silently aggregated into a result that looks
		// plausible.
		if t < MinTenths || t > MaxTenths {
			return nil, fmt.Errorf("line %d: %q is outside [-99.9, 99.9]", line, b[i+1:])
		}
		name := string(b[:i])
		a := stations[name]
		if a == nil {
			stations[name] = &Accumulator{Min: t, Max: t, Sum: t, Count: 1}
			continue
		}
		if t < a.Min {
			a.Min = t
		}
		if t > a.Max {
			a.Max = t
		}
		a.Sum += t
		a.Count++
	}
	return stations, sc.Err()
}

// WriteResult emits the challenge's output line: a Java TreeMap.toString of
// name=min/mean/max, sorted by name, followed by a newline
// (01-definition.md, CalculateAverage_baseline.java:105).
func WriteResult(w io.Writer, stations map[string]*Accumulator) error {
	names := make([]string, 0, len(stations))
	for n := range stations {
		names = append(names, n)
	}
	sort.Strings(names)

	bw := bufio.NewWriterSize(w, 1<<20)
	bw.WriteByte('{')
	for i, n := range names {
		if i > 0 {
			bw.WriteString(", ")
		}
		a := stations[n]
		buf := make([]byte, 0, 64)
		buf = append(buf, n...)
		buf = append(buf, '=')
		buf = AppendTenths(buf, a.Min)
		buf = append(buf, '/')
		buf = AppendTenths(buf, Mean(a.Sum, a.Count))
		buf = append(buf, '/')
		buf = AppendTenths(buf, a.Max)
		bw.Write(buf)
	}
	bw.WriteString("}\n")
	return bw.Flush()
}
