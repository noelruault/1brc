package asm

import (
	"fmt"
	"testing"

	gen "github.com/noelruault/research/1brc/code/gen"
)

// nameLengths reports the shape of a key set's names, which is what decides how often an 8-byte SWAR window needs a second load and a 16-byte NEON window a second window.
func nameLengths(stations []gen.Station) (mean float64, min, max int, over8, over16, over32 float64) {
	sum, min, max := 0, 1<<30, 0
	var o8, o16, o32 int
	for _, s := range stations {
		n := len(s.Name)
		sum += n
		if n < min {
			min = n
		}
		if n > max {
			max = n
		}
		if n > 8 {
			o8++
		}
		if n > 16 {
			o16++
		}
		if n > 32 {
			o32++
		}
	}
	c := float64(len(stations))
	return float64(sum) / c, min, max, 100 * float64(o8) / c, 100 * float64(o16) / c, 100 * float64(o32) / c
}

// TestNameLengthDistribution pins the two figures 03-technique-recon.md publishes for the official key set, and the one 04-asm-kernels.md publishes for the synthetic 10k set.
// They are the premise of H2 and of every "10k" verdict, so they are re-derivable by running this test rather than by rerunning a throwaway probe.
func TestNameLengthDistribution(t *testing.T) {
	mean, min, max, over8, over16, _ := nameLengths(gen.Official413())
	got := fmt.Sprintf("mean=%.1f min=%d max=%d >8B=%.1f%% >16B=%.1f%%", mean, min, max, over8, over16)
	if want := "mean=8.0 min=3 max=26 >8B=34.1% >16B=1.0%"; got != want {
		t.Fatalf("413 key set: %s, but 03-technique-recon.md publishes %s", got, want)
	}
	mean, _, max, _, _, over32 := nameLengths(gen.Synthetic10k())
	got = fmt.Sprintf("mean=%.1f max=%d >32B=%.1f%%", mean, max, over32)
	if want := "mean=51.1 max=100 >32B=68.9%"; got != want {
		t.Fatalf("synthetic 10k key set: %s, but 04-asm-kernels.md publishes %s", got, want)
	}
}
