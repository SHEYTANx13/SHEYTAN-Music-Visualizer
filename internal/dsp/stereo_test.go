package dsp

// stereo_test.go — property tests for the stereo layer over the
// deterministic corpus fixtures (§8.1 proxies):
//
//   - mono tone → width 0, correlation 1
//   - anti-phase → correlation -1, mid energy 0, L = R energy
//     (cancellation is NOT silence: side energy stays high, width 1)
//   - hard-left pan → L >> R, width > 0.3
//   - decorrelated wide stereo → correlation ≈ 0. Note on tolerance:
//     under the contract width = SE/(SE+ME), uncorrelated material
//     saturates at exactly 0.5 (side and mid RMS are equal for
//     independent channels); values above 0.5 require anti-phase
//     content (which reaches 1.0 — asserted here as the measure's
//     maximum). The original "> 0.8" expectation is therefore
//     mathematically unreachable for this fixture and was adapted.
//   - NaN/Inf inputs never reach stored series (sanitize).

import (
	"math"
	"math/rand"
	"testing"
)

// stereoAppendRuns feeds `frames` hop-grid windows of tr through
// ComputeStereoFrame/AppendStereoSeries and returns the series.
func stereoAppendRuns(tr StereoTrack, frames int) *StereoSeries {
	s := &StereoSeries{}
	l := make([]float64, FFTSize)
	r := make([]float64, FFTSize)
	for f := 0; f < frames; f++ {
		start := f * HopSize
		for i := 0; i < FFTSize && start+i < len(tr.L); i++ {
			l[i] = float64(tr.L[start+i])
			r[i] = float64(tr.R[start+i])
		}
		AppendStereoSeries(s, ComputeStereoFrame(l, r))
	}
	return s
}

func meanF32(v []float32) float64 {
	if len(v) == 0 {
		return 0
	}
	var s float64
	for _, x := range v {
		s += float64(x)
	}
	return s / float64(len(v))
}

func TestStereoMonoWidthCorrelation(t *testing.T) {
	t.Parallel()
	s := stereoAppendRuns(Tone(440, 1.5, 0.5), 64)
	if w := meanF32(s.Width); w > 0.01 {
		t.Fatalf("mono tone width = %.4f, want ~0", w)
	}
	if c := meanF32(s.Correlation); math.Abs(c-1) > 0.01 {
		t.Fatalf("mono tone correlation = %.4f, want ~1", c)
	}
	if m, l := meanF32(s.MEnergy), meanF32(s.LEnergy); math.Abs(m-l) > 0.02 {
		t.Fatalf("mono mid RMS %.4f should match channel RMS %.4f", m, l)
	}
}

func TestStereoAntiPhase(t *testing.T) {
	t.Parallel()
	// AntiPhase is a proxy: L = -R exactly.
	s := stereoAppendRuns(AntiPhase(1.5, 440, 0.5), 64)
	if c := meanF32(s.Correlation); c > -0.99 {
		t.Fatalf("anti-phase correlation = %.4f, want ~-1", c)
	}
	if m := meanF32(s.MEnergy); m > 0.001 {
		t.Fatalf("anti-phase mid energy = %.6f, want ~0 (cancellation)", m)
	}
	le, re := meanF32(s.LEnergy), meanF32(s.REnergy)
	if math.Abs(le-re) > 0.01 || le < 0.2 {
		t.Fatalf("anti-phase channel energies L=%.4f R=%.4f, want equal and loud", le, re)
	}
	// Cancellation ≠ silence: the side channel carries full energy and
	// the width measure saturates (its maximum).
	if se := meanF32(s.SEnergy); se < 0.2 {
		t.Fatalf("anti-phase side energy = %.4f, want ~channel level", se)
	}
	if w := meanF32(s.Width); w < 0.95 {
		t.Fatalf("anti-phase width = %.4f, want ~1 (measure maximum)", w)
	}
}

func TestStereoPannedLeft(t *testing.T) {
	t.Parallel()
	// Panned(-0.8) proxy: gl = amp, gr = amp·0.2.
	s := stereoAppendRuns(Panned(1.5, 440, -0.8, 0.5), 64)
	l, r := meanF32(s.LEnergy), meanF32(s.REnergy)
	if l <= 3*r {
		t.Fatalf("panned-left L=%.4f should dominate R=%.4f", l, r)
	}
	if w := meanF32(s.Width); w <= 0.3 {
		t.Fatalf("panned-left width = %.4f, want > 0.3", w)
	}
}

func TestStereoWideDecorrelated(t *testing.T) {
	t.Parallel()
	// Decorrelated-channel property, measured on a locally generated
	// (seeded, deterministic) noise pair. NOTE: the corpus WideStereo /
	// NoiseWhite / NoisePink fixtures instantiate their PRNG per sample
	// (whiteNoise(seed)(i) inside the fill loop), so those fixtures are
	// constant (DC) channels, not noise — corpus.go is read-only for
	// this layer, so the intended statistics are reproduced here.
	n := 2 * SampleRate
	tr := StereoTrack{SR: SampleRate, L: make([]float32, n), R: make([]float32, n)}
	rl := rand.New(rand.NewSource(101))
	rr := rand.New(rand.NewSource(202))
	for i := 0; i < n; i++ {
		tr.L[i] = float32(0.25 * rl.NormFloat64())
		tr.R[i] = float32(0.25 * rr.NormFloat64())
	}
	s := stereoAppendRuns(tr, 64)
	if c := meanF32(s.Correlation); math.Abs(c) > 0.1 {
		t.Fatalf("wide stereo correlation = %.4f, want ~0", c)
	}
	// Width saturates at the uncorrelated cap ≈ 0.5 (see file header);
	// the anti-phase test shows the measure's full range up to 1.0.
	if w := meanF32(s.Width); w < 0.45 || w > 0.55 {
		t.Fatalf("wide stereo width = %.4f, want ≈0.5 (SE/(SE+ME) cap for uncorrelated channels)", w)
	}
}

func TestStereoNaNSanitize(t *testing.T) {
	t.Parallel()
	l := []float64{math.NaN(), 0.5, -0.5, 0.25, math.Inf(1)}
	r := []float64{0.3, math.Inf(-1), math.NaN(), -0.2, 0.1}
	p := ComputeStereoFrame(l, r)
	for _, v := range []float64{p.L, p.R, p.M, p.S, p.Width, p.Correlation} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Fatalf("ComputeStereoFrame produced non-finite value %v", v)
		}
	}
	s := &StereoSeries{}
	AppendStereoSeries(s, p)
	arrs := [][]float32{s.LEnergy, s.REnergy, s.MEnergy, s.SEnergy, s.Width, s.Correlation}
	for i, arr := range arrs {
		if len(arr) != 1 {
			t.Fatalf("series array %d length = %d, want 1", i, len(arr))
		}
		v := float64(arr[0])
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Fatalf("series array %d holds non-finite %v", i, v)
		}
	}
	if c := float64(s.Correlation[0]); c < -1 || c > 1 {
		t.Fatalf("sanitized correlation %v out of range", c)
	}
	if w := float64(s.Width[0]); w < 0 || w > 1 {
		t.Fatalf("sanitized width %v out of range", w)
	}
}
