package render

// testbins_test.go — shared display-less test fixture, profile-neutral.

import (
	"math"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/visualizer"
)

func testBins() []float64 {
	bins := make([]float64, visualizer.NumVizBins)
	for i := range bins {
		bins[i] = 0.5 + 0.4*sin2(float64(i))
	}
	return bins
}

// sin2 is the shared deterministic fixture oscillator.
func sin2(x float64) float64 {
	return float64(math.Sin(x/3) * math.Sin(x/7+1))
}
