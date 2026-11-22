package dsp

// bands_test.go — property tests for the nine-band layer over corpus
// fixtures: tone placement per band, transient flux dominance in the
// low bands, envelope attack/release asymmetry and series integrity.
// The STFT helper mirrors the production pipeline (Hann window,
// FFTSize 2048, hop 512) so the fixtures are measured the way the
// analyzer will measure them.

import (
	"math"
	"math/cmplx"
	"testing"

	"gonum.org/v1/gonum/dsp/fourier"
)

// stftMags returns the production-style magnitude spectra: Hann-
// windowed 2048-point FFT of the mono mix on the hop grid.
func stftMags(t *testing.T, tr StereoTrack, maxFrames int) [][]float64 {
	t.Helper()
	fft := fourier.NewFFT(FFTSize)
	mono := monoF64(tr)
	total := 0
	if len(mono) >= FFTSize {
		total = (len(mono)-FFTSize)/HopSize + 1
	}
	if maxFrames > 0 && total > maxFrames {
		total = maxFrames
	}
	out := make([][]float64, 0, total)
	win := make([]float64, FFTSize)
	coef := make([]complex128, FFTSize/2+1)
	for f := 0; f < total; f++ {
		start := f * HopSize
		for i := 0; i < FFTSize; i++ {
			win[i] = mono[start+i] * hannWindow[i]
		}
		coef = fft.Coefficients(coef, win)
		mag := make([]float64, FFTSize/2+1)
		for i, c := range coef {
			mag[i] = cmplx.Abs(c)
		}
		out = append(out, mag)
	}
	return out
}

// monoF64 is the mono mid mix as float64.
func monoF64(tr StereoTrack) []float64 {
	m := make([]float64, len(tr.L))
	for i := range tr.L {
		m[i] = (float64(tr.L[i]) + float64(tr.R[i])) * 0.5
	}
	return m
}

// runBandTracker feeds the spectra through a tracker in order.
func runBandTracker(mags [][]float64) *BandTimeline {
	bt := NewBandTracker()
	var prev []float64
	for i, mag := range mags {
		bt.Update(mag, prev, i)
		prev = mag
	}
	return bt.Series()
}

func meanBand(tl *BandTimeline, b int) float64 {
	var s float64
	for _, v := range tl.Energy[b] {
		s += float64(v)
	}
	return s / float64(len(tl.Energy[b]))
}

func TestBandToneShares(t *testing.T) {
	// 55 Hz tone proxy: nearly all energy lands in sub-bass+bass
	// (bands 0+1 cover 20..120 Hz).
	low := runBandTracker(stftMags(t, Tone(55, 2, 0.6), 0))
	lowShare := meanBand(low, 0) + meanBand(low, 1)
	if lowShare <= 0.6 {
		t.Fatalf("55 Hz tone bands 0+1 share = %.3f, want > 0.6", lowShare)
	}

	// 1500 Hz tone proxy: dominant in upper-mids (band 5, 1..2 kHz).
	mid := runBandTracker(stftMags(t, Tone(1500, 2, 0.5), 0))
	m5 := meanBand(mid, 5)
	if m5 <= 0.8 {
		t.Fatalf("1500 Hz tone band 5 share = %.3f, want > 0.8", m5)
	}
	for b := 0; b < BandCount; b++ {
		if b != 5 && meanBand(mid, b) > m5 {
			t.Fatalf("band %d share %.3f beats band 5 (%.3f) for a 1500 Hz tone", b, meanBand(mid, b), m5)
		}
	}

	// 10 kHz tone proxy: dominant in brilliance (band 8, 8..16 kHz).
	high := runBandTracker(stftMags(t, Tone(10000, 2, 0.4), 0))
	h8 := meanBand(high, 8)
	if h8 <= 0.8 {
		t.Fatalf("10 kHz tone band 8 share = %.3f, want > 0.8", h8)
	}
	for b := 0; b < BandCount; b++ {
		if b != 8 && meanBand(high, b) > h8 {
			t.Fatalf("band %d share %.3f beats band 8 (%.3f) for a 10 kHz tone", b, meanBand(high, b), h8)
		}
	}
}

func TestBandKickFluxDominatesLowBands(t *testing.T) {
	// KickLike proxy: low-frequency transient with pitch drop.
	kick := Silence(3.5, 1)
	KickLike(&kick, 0.5, 0.9)
	KickLike(&kick, 1.2, 0.9)
	KickLike(&kick, 1.9, 0.9)
	KickLike(&kick, 2.6, 0.9)
	// Steady tone proxy: no transients, near-zero low-band flux.
	tone := Tone(100, 3.5, 0.5)

	kickTL := runBandTracker(stftMags(t, kick, 0))
	toneTL := runBandTracker(stftMags(t, tone, 0))

	lowFlux := func(tl *BandTimeline) (max float64) {
		for f := 0; f < len(tl.Flux[0]); f++ {
			v := float64(tl.Flux[0][f]) + float64(tl.Flux[1][f])
			if v > max {
				max = v
			}
		}
		return max
	}
	kMax := lowFlux(kickTL)
	tMax := lowFlux(toneTL)
	if kMax <= 0.3 {
		t.Fatalf("kick low-band flux max = %.3f, want > 0.3", kMax)
	}
	if tMax > 0.2*kMax {
		t.Fatalf("steady tone low-band flux max = %.3f, want < 20%% of kick's %.3f", tMax, kMax)
	}
	// per-onset flux must beat the tone's loudest frame: the kick's
	// energy arrives within a few hops (0.5 s → hop frames around 46).
	for _, at := range []float64{0.5, 1.2, 1.9, 2.6} {
		k := int(at*SampleRate/HopSize) - 1
		var mean float64
		for f := k; f < k+6 && f < len(kickTL.Flux[0]); f++ {
			mean += float64(kickTL.Flux[0][f]) + float64(kickTL.Flux[1][f])
		}
		mean /= 6
		if mean <= tMax {
			t.Fatalf("kick at %.1f s: onset low-band flux mean %.3f should exceed tone max %.3f", at, mean, tMax)
		}
	}
}

func TestBandEnvelopeAttackRelease(t *testing.T) {
	bt := NewBandTracker()
	zero := make([]float64, FFTSize/2+1)
	// loud content confined to band 5 (1000..2000 Hz → bins 43..85)
	loud := make([]float64, FFTSize/2+1)
	for i := 50; i <= 80; i++ {
		loud[i] = 10
	}
	bt.Update(zero, nil, 0)
	bt.Update(loud, zero, 1)
	bt.Update(zero, loud, 2)
	tl := bt.Series()

	e := float64(tl.Energy[5][1])
	envUp := float64(tl.Envelope[5][1])
	envDown := float64(tl.Envelope[5][2])
	// attack α = 1-exp(-hopSec/0.015) ≈ 0.51 → ≥ 40% of the step in one hop
	if envUp < 0.4*e {
		t.Fatalf("envelope attack %.3f captured < 40%% of energy step %.3f in one hop", envUp, e)
	}
	// release α = 1-exp(-hopSec/0.18) ≈ 0.058 → keeps > 90% one hop later
	if envDown < 0.9*envUp {
		t.Fatalf("envelope release %.3f dropped more than 10%% of %.3f in one hop (release must be slower than attack)", envDown, envUp)
	}
	if envDown >= envUp {
		t.Fatalf("envelope should decay after the step ends: %.3f → %.3f", envUp, envDown)
	}
}

func TestBandSeriesIntegrity(t *testing.T) {
	kick := Silence(2, 1)
	KickLike(&kick, 0.5, 0.8)
	SnareLike(&kick, 1.0, 0.6)
	HatLike(&kick, 1.5, 0.5)
	tl := runBandTracker(stftMags(t, kick, 40))

	for b := 0; b < BandCount; b++ {
		for name, arr := range map[string][]float32{
			"energy":   tl.Energy[b],
			"flux":     tl.Flux[b],
			"envelope": tl.Envelope[b],
		} {
			if len(arr) != 40 {
				t.Fatalf("band %d %s length = %d, want 40", b, name, len(arr))
			}
			for f, v := range arr {
				x := float64(v)
				if math.IsNaN(x) || math.IsInf(x, 0) || x < 0 || x > 1 {
					t.Fatalf("band %d %s[%d] = %v out of 0..1", b, name, f, x)
				}
			}
		}
	}

	// BandEnergyShare direct properties: silence → 0; full-band energy → 1.
	zeros := make([]float64, FFTSize/2+1)
	if got := BandEnergyShare(zeros, 20, 60); got != 0 {
		t.Fatalf("BandEnergyShare(silence) = %v, want 0", got)
	}
	full := make([]float64, FFTSize/2+1)
	for i := 43; i <= 85; i++ {
		full[i] = 1
	}
	if got := BandEnergyShare(full, 1000, 2000); got < 0.99 {
		t.Fatalf("BandEnergyShare(band 5 content, 1000..2000) = %.4f, want ~1 (edge cells weigh linearly)", got)
	}
	if got := BandEnergyShare(full, 20, 60); got > 0.001 {
		t.Fatalf("BandEnergyShare(band 5 content, 20..60) = %.4f, want ~0", got)
	}
}
