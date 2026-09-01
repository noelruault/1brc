// Package gen produces 1BRC measurement files and the reference aggregation
// they are checked against. Both live here because they must agree on one
// thing: the exact arithmetic of a one-fractional-digit temperature.
package gen

import "math"

// Tenths is a temperature in tenths of a degree.
//
// The challenge guarantees every input value has exactly one fractional digit
// (01-definition.md, README.md:422), so the whole problem lives on an integer
// grid and never needs float64. Aggregating in float64 instead costs accuracy
// that shows up in the last printed digit: summing ~2.4M one-decimal values per
// station drifts by ~1e-2, which is close enough to the 0.05 rounding threshold
// to flip a station's mean.
type Tenths int64

// Temperature bounds from README.md:422, inclusive: every legal value is a whole
// number of tenths in [-999, 999].
const (
	minTenths Tenths = -999
	maxTenths Tenths = 999
)

// RoundToTenths rounds v to the nearest tenth with ties going toward positive
// infinity, matching the challenge's required rounding direction (README.md:426)
// and Java's Math.round, which is floor(x+0.5).
//
// Go's math.Round rounds half away from zero and disagrees on every negative
// tie: math.Round(-24.5) is -25, floor(-24.5+0.5) is -24. Using math.Round here
// would corrupt one station in roughly every data set.
func RoundToTenths(v float64) Tenths {
	return Tenths(math.Floor(v*10.0 + 0.5))
}

// AppendTenths appends t as a decimal with exactly one fractional digit.
//
// The sign is taken from the integer, so "-0.0" cannot be produced. Formatting
// the equivalent float64 with %.1f would print "-0.0" for any small negative
// value, which no correct output ever contains.
func AppendTenths(dst []byte, t Tenths) []byte {
	if t < 0 {
		dst = append(dst, '-')
		t = -t
	}
	whole, frac := int64(t)/10, int64(t)%10
	if whole >= 10 {
		dst = append(dst, byte('0'+whole/10))
	}
	return append(dst, byte('0'+whole%10), '.', byte('0'+frac))
}

// ParseTenths reads a temperature of the form -?\d+\.\d without going through
// float64. Returns false if b is not exactly that shape; the caller decides
// whether a malformed line is a bug in the data or in the reader.
func ParseTenths(b []byte) (Tenths, bool) {
	neg := false
	if len(b) > 0 && b[0] == '-' {
		neg, b = true, b[1:]
	}
	if len(b) < 3 || b[len(b)-2] != '.' {
		return 0, false
	}
	frac := b[len(b)-1]
	if frac < '0' || frac > '9' {
		return 0, false
	}
	var whole int64
	for _, c := range b[:len(b)-2] {
		if c < '0' || c > '9' {
			return 0, false
		}
		whole = whole*10 + int64(c-'0')
	}
	t := Tenths(whole*10 + int64(frac-'0'))
	if neg {
		t = -t
	}
	return t, true
}

// Mean returns the station mean in tenths, reproducing the reference
// implementation's arithmetic operation for operation
// (CalculateAverage_baseline.java:88 then :41):
//
//	round( ( round(sum) / count ) )
//
// The inner round is already applied: sumTenths is the exact one-decimal sum,
// which is what Math.round(sum*10.0) is trying to recover from a drifting
// float64 accumulator. The remaining float64 steps are kept in the same order
// and the same precision as the Java chain so the last digit agrees.
func Mean(sumTenths Tenths, count int64) Tenths {
	sum := float64(sumTenths) / 10.0
	return RoundToTenths(sum / float64(count))
}
