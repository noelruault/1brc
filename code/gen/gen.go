package gen

import (
	"bufio"
	"fmt"
	"io"
	"math/rand/v2"
	"strings"
)

// Official413 returns the upstream 413-station key set: the one the leaderboard
// file was built from.
func Official413() []Station { return officialStations }

// Synthetic10k returns a 10,000-station key set that stresses what the 413-entry
// set cannot: names up to the 100-byte limit, multi-byte UTF-8, and a key count
// at the maximum the rules allow (README.md:421-423).
//
// It is NOT byte-identical to upstream's CreateMeasurements3, which slices names
// out of a concatenated corpus. Reproducing that exactly would buy nothing here:
// the point of this set is hash and key-count pressure, and any name set that
// spans the legal shape space delivers that. Recorded as a deviation in
// 02-baseline-data.txt.
func Synthetic10k() []Station {
	// A fixed alphabet with 1-, 2- and 3-byte runes, so generated names exercise
	// UTF-8 decoding and byte-length-vs-rune-length confusion.
	alphabet := []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 -'.éüñåöçÅ日本語κόσμε")
	r := rand.New(rand.NewPCG(0x1B4C10C0, 0x5EED10C0))

	seen := make(map[string]bool, 10000)
	out := make([]Station, 0, 10000)
	for len(out) < 10000 {
		// Byte length, not rune count: the limit in the rules is 100 bytes.
		want := 1 + r.IntN(100)
		var b strings.Builder
		for b.Len() < want {
			c := alphabet[r.IntN(len(alphabet))]
			if b.Len()+len(string(c)) > 100 {
				break
			}
			b.WriteRune(c)
		}
		name := strings.TrimSpace(b.String())
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, Station{Name: name, Mean: -20.0 + r.Float64()*50.0})
	}
	return out
}

// Write emits rows measurement lines drawn from stations to w.
//
// Unlike the upstream generator, which uses ThreadLocalRandom and so produces a
// file nobody can reproduce, this is seeded: the same seed and station set always
// yield byte-identical output. In a study where every number must be
// re-derivable, an unreproducible input file is not usable.
//
// The value is Gaussian around the station mean with the upstream standard
// deviation, then clamped into the legal range. Upstream does not clamp; with
// sigma=10 and means in [-14.4, 30.5] the bound sits beyond 6 sigma, so the clamp
// is effectively never taken but makes conformance with README.md:422 structural
// rather than statistical.
func Write(w io.Writer, stations []Station, rows int64, stddev float64, seed uint64) (bytesWritten int64, err error) {
	if len(stations) == 0 {
		return 0, fmt.Errorf("gen: empty station set")
	}
	r := rand.New(rand.NewPCG(seed, seed^0x9E3779B97F4A7C15))
	bw := bufio.NewWriterSize(w, 1<<22)

	names := make([][]byte, len(stations))
	for i, s := range stations {
		names[i] = []byte(s.Name + ";")
	}

	line := make([]byte, 0, 128)
	for i := int64(0); i < rows; i++ {
		idx := r.IntN(len(stations))
		t := RoundToTenths(r.NormFloat64()*stddev + stations[idx].Mean)
		if t < MinTenths {
			t = MinTenths
		} else if t > MaxTenths {
			t = MaxTenths
		}
		line = append(line[:0], names[idx]...)
		line = AppendTenths(line, t)
		line = append(line, '\n')
		if _, err := bw.Write(line); err != nil {
			return bytesWritten, err
		}
		bytesWritten += int64(len(line))
	}
	return bytesWritten, bw.Flush()
}
