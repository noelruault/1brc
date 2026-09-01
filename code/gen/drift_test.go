package gen

import (
	"math"
	"math/rand/v2"
	"testing"
)

// Why Tenths exists, measured rather than assumed.
//
// The tempting justification is that a float64 accumulator drifts too far over
// one station's share of a billion rows to recover the true one-decimal sum.
// That is false, and this test is what disproved it: the measured drift is
// ~9.2e-07, four orders of magnitude below the 0.05 rounding threshold, and the
// float64 sum recovers the exact tenths. Errors from ~2.4M additions cancel like
// a random walk (~sqrt(n)*eps*|sum| ~ 8e-06) rather than accumulating coherently
// (~n*eps*|sum| ~ 1e-02, the bound that motivated the wrong claim).
//
// The integer grid is still the right representation, for the reasons that
// survive measurement: it is exact by construction rather than by a margin that
// depends on the data distribution, and it makes the roundTowardPositive rule and
// the "-0.0" trap unreachable instead of merely unlikely. This test stays as the
// pin on that reasoning, so nobody re-derives the wrong justification.
func TestFloat64AccumulationDriftIsSmall(t *testing.T) {
	const rowsPerStation = 1_000_000_000 / 413 // the 1b file's share per station
	const bound = 1e-4                         // ~100x the measured drift, still far under 0.05

	r := rand.New(rand.NewPCG(0xD21F7, 0xD21F8))
	var exact Tenths
	var approx float64
	for i := 0; i < rowsPerStation; i++ {
		v := RoundToTenths(r.NormFloat64()*10.0 + 18.0)
		exact += v
		approx += float64(v) / 10.0
	}

	trueSum := float64(exact) / 10.0
	drift := math.Abs(approx - trueSum)
	recovered := Tenths(math.Floor(approx*10.0 + 0.5))

	t.Logf("rows=%d exact sum=%.1f float64 sum=%.10f drift=%.6g recovered=%d exact=%d",
		rowsPerStation, trueSum, approx, drift, recovered, exact)

	if drift > bound {
		t.Errorf("float64 drift is %.6g, above the %g recorded here; the comment on Tenths "+
			"cites a measured number and that number has moved", drift, bound)
	}
	if recovered != exact {
		t.Errorf("float64 accumulation recovered %d, exact is %d; drift now reaches the last "+
			"printed digit, which changes why Tenths exists", recovered, exact)
	}
}
