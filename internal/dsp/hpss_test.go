package dsp

// hpss_test.go — property tests for the median-filter HPSS layer:
// kick frames read percussive, pad frames read harmonic, shares sum to
// 1 (within float32 tolerance) once the ring is warm and stay exactly
// 0 before ≥ 8 frames are buffered, and two runs over the same input
// are byte-identical.

import (
	"math"
	"reflect"
	"testing"
)

// hpssAppendRuns pushes the spectra through a fresh decomposer and
// returns the appended series.
func hpssAppendRuns(mags [][]float64) *HPSSSeries {
	h := NewHPSS()
	s := &HPSSSeries{}
	for _, mag := range mags {
		h.Push(mag)
		AppendHPSSPoint(s, h.Decompose())
	}
	return s
}

func hpssMeans(s *HPSSSeries, from, to int) (h, p float64) {
	for i := from; i < to && i < len(s.Harmonic); i++ {
		h += float64(s.Harmonic[i])
		p += float64(s.Percussive[i])
	}
	n := to - from
	return h / float64(n), p / float64(n)
}

func TestHPSSKickPercussive(t *testing.T) {
	// KickLike proxy: low transient; at each onset the temporal median
	// is still cold (previous frames silent) so percussive evidence
	// must dominate harmonic evidence.
	kick := Silence(3.5, 1)
	KickLike(&kick, 0.5, 0.9)
	KickLike(&kick, 1.2, 0.9)
	KickLike(&kick, 1.9, 0.9)
	KickLike(&kick, 2.6, 0.9)
	s := hpssAppendRuns(stftMags(t, kick, 0))

	for _, at := range []float64{0.5, 1.2, 1.9, 2.6} {
		k := int(at*SampleRate/HopSize) - 1
		h, p := hpssMeans(s, k, k+6)
		if p <= h {
			t.Fatalf("kick at %.1f s: percussive mean %.3f should exceed harmonic mean %.3f", at, p, h)
		}
	}
}

func TestHPSSPadHarmonic(t *testing.T) {
	// PadLike proxy: sustained harmonic bed; the temporal median tracks
	// the steady spectrum while the frequency median smooths its peaks.
	pad := Silence(3, 1)
	PadLike(&pad, 0, 3, 0.4)
	s := hpssAppendRuns(stftMags(t, pad, 0))
	h, p := hpssMeans(s, 40, 240)
	if h <= p {
		t.Fatalf("pad: harmonic mean %.3f should exceed percussive mean %.3f", h, p)
	}
	if h < 0.5 {
		t.Fatalf("pad harmonic share %.3f unexpectedly low", h)
	}
}

func TestHPSSWarmupAndShareSum(t *testing.T) {
	mags := stftMags(t, Tone(220, 1, 0.5), 12)
	h := NewHPSS()
	s := &HPSSSeries{}
	for i, mag := range mags {
		h.Push(mag)
		p := h.Decompose()
		if i < hpssMinFrames-1 {
			if p.Harmonic != 0 || p.Percussive != 0 || p.Residual != 0 {
				t.Fatalf("frame %d (< %d buffered): shares %v, want all 0", i, hpssMinFrames, p)
			}
		} else {
			sum := p.Harmonic + p.Percussive + p.Residual
			if sum < 0.98 || sum > 1.02 {
				t.Fatalf("frame %d: share sum = %.4f, want 0.98..1.02", i, sum)
			}
			if p.Harmonic <= 0 || p.Residual >= 1 {
				t.Fatalf("frame %d: degenerate shares %+v", i, p)
			}
		}
		AppendHPSSPoint(s, p)
	}
	// silent input: shares stay 0 even when warm
	hEmpty := NewHPSS()
	zero := make([]float64, FFTSize/2+1)
	for i := 0; i < 10; i++ {
		hEmpty.Push(zero)
		if p := hEmpty.Decompose(); p.Harmonic != 0 || p.Percussive != 0 || p.Residual != 0 {
			t.Fatalf("silent frame %d: shares %v, want all 0", i, p)
		}
	}
}

func TestHPSSDeterministic(t *testing.T) {
	pad := Silence(2, 1)
	PadLike(&pad, 0, 2, 0.4)
	KickLike(&pad, 1.0, 0.8)
	mags := stftMags(t, pad, 60)
	a := hpssAppendRuns(mags)
	b := hpssAppendRuns(mags)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("two HPSS runs over identical input differ")
	}
	// sanity: finite values only
	for i := range a.Harmonic {
		for _, v := range [3]float64{float64(a.Harmonic[i]), float64(a.Percussive[i]), float64(a.Residual[i])} {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				t.Fatalf("non-finite HPSS value at frame %d", i)
			}
		}
	}
}
