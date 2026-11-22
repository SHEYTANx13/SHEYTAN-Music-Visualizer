package dsp

// bands.go — v3 frequency-band layer (§ analysis_types.go "Frequency-
// band layer"): nine-band energy shares, per-band positive spectral
// flux and attack/release smoothed envelopes over the 20 Hz..16 kHz
// BandEdgeHz grid.
//
// Units: Energy shares are 0..1 (share of the frame's total spectrum
// energy; sums over bands ≤ 1). Flux is the positive magnitude delta
// summed over the band's bins and divided by the band bin count
// (bounded 0..1). Envelope is a per-band one-pole smoother with the
// documented attack (τ = 15 ms) and release (τ = 180 ms) time
// constants evaluated at hopSeconds = HopSize/SampleRate.
//
// Determinism: no map iteration, no randomness; non-finite bins are
// skipped so no NaN reaches stored series.

import "math"

// BandEnergyShare returns the share (0..1) of the spectrum's total
// energy that falls between loHz and hiHz. mag is a one-sided magnitude
// spectrum of the main STFT (FFTSize/2+1 bins at SampleRate). Bin
// energies (mag²) at the band edges are weighted by the linear overlap
// of each bin's ±0.5-bin cell, i.e. linear interpolation at the edges.
// Returns 0 on silence (no usable energy).
func BandEnergyShare(mag []float64, loHz, hiHz float64) float64 {
	return bandShareBins(mag, loHz, hiHz, float64(SampleRate)/float64(FFTSize))
}

// bandShareBins is BandEnergyShare with an explicit bin width so the
// multi-resolution layer can reuse it at other FFT sizes.
func bandShareBins(mag []float64, loHz, hiHz, binW float64) float64 {
	if len(mag) == 0 || hiHz <= loHz || binW <= 0 {
		return 0
	}
	var total float64
	for _, m := range mag {
		if finite(m) {
			total += m * m
		}
	}
	if total <= 0 {
		return 0
	}
	return clampUnit(bandEnergyCells(mag, loHz, hiHz, binW) / total)
}

// bandEnergyCells sums mag² weighted by each bin cell's overlap with
// [loHz, hiHz] (cell of bin i spans [i-0.5, i+0.5] bins).
func bandEnergyCells(mag []float64, loHz, hiHz, binW float64) float64 {
	loB := loHz / binW
	hiB := hiHz / binW
	iStart := int(math.Floor(loB - 0.5))
	if iStart < 0 {
		iStart = 0
	}
	iEnd := int(math.Ceil(hiB + 0.5))
	if iEnd > len(mag)-1 {
		iEnd = len(mag) - 1
	}
	var sum float64
	for i := iStart; i <= iEnd; i++ {
		m := mag[i]
		if !finite(m) {
			continue
		}
		w := math.Min(hiB, float64(i)+0.5) - math.Max(loB, float64(i)-0.5)
		if w <= 0 {
			continue
		}
		if w > 1 {
			w = 1
		}
		sum += m * m * w
	}
	return sum
}

// BandTracker accumulates the per-band Energy/Flux/Envelope timelines.
// One instance analyzes one stream; Update must be called once per
// analysis hop in order. It owns its output BandTimeline (see Series).
type BandTracker struct {
	tl           BandTimeline
	attackAlpha  float64 // one-pole coefficient on rising energy
	releaseAlpha float64 // one-pole coefficient on falling energy
	binLo        [BandCount]int
	binHi        [BandCount]int
	env          [BandCount]float64
}

// NewBandTracker creates a tracker with the documented envelope
// coefficients: attack τ = 15 ms, release τ = 180 ms, evaluated at
// hopSeconds = HopSize/SampleRate (α = 1 - exp(-hopSec/τ)).
func NewBandTracker() *BandTracker {
	bt := &BandTracker{}
	hopSec := float64(HopSize) / float64(SampleRate)
	bt.attackAlpha = 1 - math.Exp(-hopSec/0.015)
	bt.releaseAlpha = 1 - math.Exp(-hopSec/0.18)
	binW := float64(SampleRate) / float64(FFTSize)
	for b := 0; b < BandCount; b++ {
		lo := int(math.Ceil(BandEdgeHz[b]/binW - 1e-9))
		hi := int(math.Floor(BandEdgeHz[b+1]/binW + 1e-9))
		if hi < lo {
			hi = lo
		}
		bt.binLo[b], bt.binHi[b] = lo, hi
	}
	return bt
}

// Update consumes one frame's magnitude spectrum. prevMag is the
// previous frame's spectrum (nil on the first frame); the tracker does
// not retain it. frameIndex is advisory (position on the hop grid) —
// series values are appended in call order.
func (bt *BandTracker) Update(mag, prevMag []float64, frameIndex int) {
	_ = frameIndex
	for b := 0; b < BandCount; b++ {
		e := BandEnergyShare(mag, BandEdgeHz[b], BandEdgeHz[b+1])
		bt.tl.Energy[b] = append(bt.tl.Energy[b], sanitizeFloat32(e, 0, 1))
		bt.tl.Flux[b] = append(bt.tl.Flux[b], float32(bt.bandFlux(mag, prevMag, b)))

		env := bt.env[b]
		alpha := bt.releaseAlpha
		if e > env {
			alpha = bt.attackAlpha
		}
		env += alpha * (e - env)
		bt.env[b] = env
		bt.tl.Envelope[b] = append(bt.tl.Envelope[b], sanitizeFloat32(env, 0, 1))
	}
}

// bandFlux is the band's RELATIVE positive rise: the sum of positive
// magnitude deltas in the band divided by the band's previous magnitude
// sum (level-normalized, clamped 0..1). Per-bin-mean normalization
// would dilute broadband transients (a kick spreads its rise over many
// bins while a steady tone concentrates variation in few), so the
// measured quantity is how much the band ROSE relative to its own
// level. 0 when there is no previous frame or the band was silent
// below the measurement floor.
func (bt *BandTracker) bandFlux(mag, prevMag []float64, b int) float64 {
	if len(mag) == 0 || len(prevMag) == 0 {
		return 0
	}
	lo := bt.binLo[b]
	hi := bt.binHi[b]
	if lo >= len(mag) || lo >= len(prevMag) {
		return 0
	}
	if hi >= len(mag) {
		hi = len(mag) - 1
	}
	if hi >= len(prevMag) {
		hi = len(prevMag) - 1
	}
	if hi < lo {
		return 0
	}
	var sum, prevSum float64
	for i := lo; i <= hi; i++ {
		m, p := mag[i], prevMag[i]
		if !finite(m) || !finite(p) {
			continue
		}
		if d := m - p; d > 0 {
			sum += d
		}
		prevSum += p
	}
	// Measurement floor: bands below this total magnitude carry no
	// meaningful relative rise (noise-floor amplification guard).
	const prevFloor = 1e-7
	if prevSum <= prevFloor {
		return 0
	}
	return clampUnit(sum / prevSum)
}

// Series returns the accumulated timeline (valid after Updates; the
// returned pointer aliases tracker state).
func (bt *BandTracker) Series() *BandTimeline {
	return &bt.tl
}
