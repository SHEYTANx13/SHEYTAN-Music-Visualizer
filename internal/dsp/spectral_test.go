package dsp

// spectral_test.go — property tests for the spectral descriptor layer
// over corpus fixtures: flatness separates noise from tones, entropy
// separates noise from silence, ZCR matches the waveform rate, chroma
// identifies pitch classes (single tone and triad via the ChordChange
// proxy), the tuning estimator recovers cents offsets, and harmonicity
// separates a harmonic stack from noise. NaN inputs must never reach
// stored series.

import (
	"math"
	"testing"
)

// spectralAppendRuns computes spectral points for the first `frames`
// hop-grid windows and appends them to a series.
func spectralAppendRuns(t *testing.T, tr StereoTrack, frames int) *SpectralSeries {
	t.Helper()
	mags := stftMags(t, tr, frames)
	mono := monoF64(tr)
	sp := &SpectralSeries{}
	for i, mag := range mags {
		start := i * HopSize
		AppendSpectralPoint(sp, ComputeSpectralFrame(mag, mono[start:start+FFTSize]))
	}
	return sp
}

func meanSeriesF32(v []float32) float64 {
	if len(v) == 0 {
		return 0
	}
	var s float64
	for _, x := range v {
		s += float64(x)
	}
	return s / float64(len(v))
}

func TestSpectralFlatnessNoiseVsTone(t *testing.T) {
	t.Parallel()
	noise := spectralAppendRuns(t, NoiseWhite(2, 3), 40)
	tone := spectralAppendRuns(t, Tone(440, 2, 0.5), 40)
	nf, tf := meanSeriesF32(noise.Flatness), meanSeriesF32(tone.Flatness)
	if nf <= 5*tf {
		t.Fatalf("noise flatness %.6f should exceed 5× tone flatness %.6g", nf, tf)
	}
}

func TestSpectralEntropy(t *testing.T) {
	t.Parallel()
	// silence proxy: all-zero spectrum → entropy exactly 0
	mags := stftMags(t, Silence(1, 1), 2)
	p := ComputeSpectralFrame(mags[0], nil)
	if p.Entropy != 0 {
		t.Fatalf("silence entropy = %v, want 0", p.Entropy)
	}
	if p.Flatness != 0 || p.Bandwidth != 0 || p.Harmonicity != 0 || p.Inharmonicity != 0 {
		t.Fatalf("silence descriptors should all be 0, got %+v", p)
	}
	noise := spectralAppendRuns(t, NoiseWhite(2, 3), 40)
	if e := meanSeriesF32(noise.Entropy); e <= 0.8 {
		t.Fatalf("noise entropy = %.4f, want > 0.8", e)
	}
}

func TestSpectralZCR(t *testing.T) {
	t.Parallel()
	// 6 kHz tone proxy: 2 crossings per period = 2·6000/48000 = 0.25.
	tr := Tone(6000, 1, 0.4)
	mags := stftMags(t, tr, 3)
	mono := monoF64(tr)
	w := mono[512 : 512+FFTSize] // matches frame 1
	p := ComputeSpectralFrame(mags[1], w)
	if p.ZCR < 0.2 || p.ZCR > 0.3 {
		t.Fatalf("6 kHz tone ZCR = %.4f, want 0.25 ±20%%", p.ZCR)
	}
}

func TestChromaA440(t *testing.T) {
	t.Parallel()
	// 440 Hz tone proxy → pitch class 9 (A) must dominate.
	mags := stftMags(t, Tone(440, 1.5, 0.5), 30)
	var sum float64
	for _, mag := range mags {
		sum += float64(ComputeChromaPoint(mag, 0).Chroma[9])
	}
	avg := sum / float64(len(mags))
	if avg <= 0.5 {
		t.Fatalf("440 Hz chroma[A] = %.4f, want > 0.5", avg)
	}
}

func TestChromaCTriad(t *testing.T) {
	t.Parallel()
	// ChordChange proxy: C major triad (C4, E4, G4 sines).
	tr := Silence(2, 1)
	ChordChange(&tr, 0.1, 1.5, 0.5, []float64{261.6256}, true)
	mags := stftMags(t, tr, 0)
	frame := 70 // ≈0.75 s, inside the chord, past the attack ramp
	cp := ComputeChromaPoint(mags[frame], 0)

	// find the three strongest pitch classes
	idx := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}
	vals := func(i int) float64 { return float64(cp.Chroma[idx[i]]) }
	for a := 0; a < 12; a++ {
		for b := a + 1; b < 12; b++ {
			if vals(b) > vals(a) {
				idx[a], idx[b] = idx[b], idx[a]
			}
		}
	}
	top3 := map[int]bool{idx[0]: true, idx[1]: true, idx[2]: true}
	for _, want := range []int{0, 4, 7} { // C, E, G
		if !top3[want] {
			t.Fatalf("C major triad chroma top-3 = %v, want C(0), E(4), G(7); chroma = %v", []int{idx[0], idx[1], idx[2]}, cp.Chroma)
		}
	}
}

func TestTuningOffsets(t *testing.T) {
	t.Parallel()
	mag440 := stftMags(t, Tone(440, 1, 0.5), 25)[20]
	off440 := EstimateTuningOffsetCents(mag440)
	if math.Abs(off440) > 3 {
		t.Fatalf("440 Hz tuning offset = %.2f cents, want |off| ≤ 3", off440)
	}
	mag445 := stftMags(t, Tone(445, 1, 0.5), 25)[20]
	off445 := EstimateTuningOffsetCents(mag445)
	if off445 < 10 || off445 > 30 {
		t.Fatalf("445 Hz tuning offset = %.2f cents, want +10..+30 (theory +19.6)", off445)
	}
}

func TestHarmonicityStackVsNoise(t *testing.T) {
	t.Parallel()
	stack := spectralAppendRuns(t, HarmonicStack(220, 8, 2, 0.5), 40)
	noise := spectralAppendRuns(t, NoiseWhite(2, 11), 40)
	sh, nh := meanSeriesF32(stack.Harmonicity), meanSeriesF32(noise.Harmonicity)
	if sh <= 2*nh {
		t.Fatalf("harmonic stack harmonicity %.4f should exceed 2× noise harmonicity %.4f", sh, nh)
	}
	if nh > 0.2 {
		t.Fatalf("noise harmonicity %.4f should stay small", nh)
	}
}

func TestSpectralNaNSafe(t *testing.T) {
	t.Parallel()
	mag := make([]float64, FFTSize/2+1)
	mag[10] = math.NaN()
	mag[100] = 5 // one finite peak so the descriptor paths all execute
	win := []float64{math.NaN(), 1, -1, 0.5, -0.5}
	p := ComputeSpectralFrame(mag, win)
	for name, v := range map[string]float64{
		"bandwidth": p.Bandwidth, "contrast": p.Contrast, "flatness": p.Flatness,
		"entropy": p.Entropy, "zcr": p.ZCR, "harmonicity": p.Harmonicity,
		"inharmonicity": p.Inharmonicity,
	} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Fatalf("%s = %v is not finite for NaN input", name, v)
		}
	}
	sp := &SpectralSeries{}
	AppendSpectralPoint(sp, p)
	for i, arr := range [][]float32{sp.Bandwidth, sp.Contrast, sp.Flatness, sp.Entropy, sp.ZCR, sp.Harmonicity, sp.Inharmonicity} {
		if len(arr) != 1 {
			t.Fatalf("spectral array %d length = %d, want 1", i, len(arr))
		}
		if math.IsNaN(float64(arr[0])) || math.IsInf(float64(arr[0]), 0) {
			t.Fatalf("spectral array %d holds non-finite value", i)
		}
	}
	// chroma path: NaN magnitudes skipped, NaN tuning sanitized
	cp := ComputeChromaPoint(mag, math.NaN())
	for k, v := range cp.Chroma {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			t.Fatalf("chroma[%d] non-finite for NaN input", k)
		}
	}
	if math.IsNaN(float64(cp.TuningOffsetCents)) {
		t.Fatal("chroma tuning offset non-finite for NaN input")
	}
}
