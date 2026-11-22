package dsp

// spectral.go — v3 spectral descriptor layer (§ analysis_types.go
// "Spectral descriptor layer"): bandwidth, contrast, flatness,
// entropy, zero-crossing rate, harmonicity and inharmonicity per hop,
// plus the 12-bin chroma (HPCP-style) and the global tuning offset.
//
// Units:
//   - Bandwidth: Hz (second spectral moment around the centroid).
//   - Contrast: dB, 0..80 (mean over the 6 documented sub-bands of
//     dB(p95 mag) - dB(p10 mag)).
//   - Flatness: 0..1 (geometric/arithmetic mean ratio; 1 = noise-like).
//   - Entropy: 0..1 (Shannon entropy of the power spectrum normalized
//     by log(binCount)).
//   - ZCR: crossings per sample (0..1) from the supplied mono window.
//   - Harmonicity: 0..1 (share of frame power within ±1 bin of the
//     harmonics k·f0, k = 1..8; f0 from a harmonic product spectrum
//     searched over the documented fundamental range).
//   - Inharmonicity: 0..1 (fraction of the top-8 parabolic-refined
//     peaks not within 1.5% of any multiple of f0).
//   - Chroma: pitch-class weights normalized so values are ≤1 and the
//     sum ≤ 1. Peaks below 55 Hz / above 5000 Hz are excluded.
//   - Tuning offset: cents, -50..+50.
//
// mag inputs are one-sided magnitude spectra of the main STFT
// (FFTSize/2+1 bins at SampleRate); bin frequencies are derived from
// the spectrum length so the helpers stay correct at other sizes.
// Peak frequencies are parabolic-refined in the log-magnitude domain:
// at FFTSize 2048 the raw bin spacing (23.4 Hz) exceeds a semitone
// below ~500 Hz, so unrefined bin->pitch-class mapping would misplace
// low notes (a 440 Hz tone would land on bin 18.77 → 468.75 Hz = B).
// Refinement is what makes 440 Hz report pitch class A (see tests).
//
// Determinism: pure functions of the inputs; non-finite values are
// skipped so no NaN/Inf reaches stored series.

import (
	"math"
	"sort"
)

// SpectralPoint is one analysis window's spectral descriptors.
type SpectralPoint struct {
	Bandwidth     float64 // Hz
	Contrast      float64 // dB, 0..80
	Flatness      float64 // 0..1
	Entropy       float64 // 0..1
	ZCR           float64 // crossings/sample, 0..1
	Harmonicity   float64 // 0..1
	Inharmonicity float64 // 0..1
}

// spContrastRanges are the six documented contrast sub-bands (Hz).
var spContrastRanges = [6][2]float64{
	{20, 60}, {60, 250}, {250, 1000}, {1000, 4000}, {4000, 8000}, {8000, 16000},
}

// spectrumBinWidth derives the bin width (Hz) from a one-sided
// spectrum length (nBins = FFT/2 + 1 → binW = SampleRate/FFT).
func spectrumBinWidth(nBins int) float64 {
	if nBins > 1 {
		return float64(SampleRate) / float64(2*(nBins-1))
	}
	return float64(SampleRate) / float64(FFTSize)
}

// ComputeSpectralFrame measures one analysis window. mag is the
// window's one-sided magnitude spectrum; monoWindow is the (raw or
// windowed) mono sample window used for the zero-crossing rate.
func ComputeSpectralFrame(mag []float64, monoWindow []float64) SpectralPoint {
	p := SpectralPoint{}
	p.ZCR = spZCR(monoWindow)
	if len(mag) < 3 {
		return p
	}
	binW := spectrumBinWidth(len(mag))
	p.Bandwidth = spBandwidth(mag, binW)
	p.Contrast = spContrast(mag, binW)
	p.Flatness = spFlatness(mag)
	p.Entropy = spEntropy(mag)
	f0 := hpsFundamental(mag, binW, 60, 800)
	p.Harmonicity = harmonicPowerShare(mag, binW, f0)
	p.Inharmonicity = spInharmonicity(mag, binW, f0)
	return p
}

// AppendSpectralPoint appends one sanitized point to the seven
// parallel SpectralSeries arrays.
func AppendSpectralPoint(dst *SpectralSeries, p SpectralPoint) {
	dst.Bandwidth = append(dst.Bandwidth, sanitizeFloat32(p.Bandwidth, 0, float64(SampleRate)/2))
	dst.Contrast = append(dst.Contrast, sanitizeFloat32(p.Contrast, 0, 80))
	dst.Flatness = append(dst.Flatness, sanitizeFloat32(p.Flatness, 0, 1))
	dst.Entropy = append(dst.Entropy, sanitizeFloat32(p.Entropy, 0, 1))
	dst.ZCR = append(dst.ZCR, sanitizeFloat32(p.ZCR, 0, 1))
	dst.Harmonicity = append(dst.Harmonicity, sanitizeFloat32(p.Harmonicity, 0, 1))
	dst.Inharmonicity = append(dst.Inharmonicity, sanitizeFloat32(p.Inharmonicity, 0, 1))
}

// spBandwidth is the second spectral moment around the centroid (Hz),
// power-weighted. 0 on silence.
func spBandwidth(mag []float64, binW float64) float64 {
	var num, den float64 // num = Σ f·m², den = Σ m²
	for i, m := range mag {
		if !finite(m) {
			continue
		}
		ms := m * m
		num += float64(i) * binW * ms
		den += ms
	}
	if den <= 0 {
		return 0
	}
	centroid := num / den
	var moment float64
	for i, m := range mag {
		if !finite(m) {
			continue
		}
		d := float64(i)*binW - centroid
		moment += d * d * m * m
	}
	return math.Sqrt(moment / den)
}

// spContrast is the mean over the 6 sub-bands of dB(95th pct magnitude)
// - dB(10th pct magnitude), each range bounded to 0..80 dB. Empty or
// silent ranges contribute 0.
func spContrast(mag []float64, binW float64) float64 {
	sum := 0.0
	scratch := make([]float64, 0, len(mag))
	for _, r := range spContrastRanges {
		lo := int(math.Ceil(r[0]/binW - 1e-9))
		hi := int(math.Floor(r[1]/binW + 1e-9))
		if lo < 0 {
			lo = 0
		}
		if hi > len(mag)-1 {
			hi = len(mag) - 1
		}
		scratch = scratch[:0]
		for i := lo; i <= hi; i++ {
			m := mag[i]
			if finite(m) && m >= 0 {
				scratch = append(scratch, m)
			}
		}
		if len(scratch) == 0 {
			continue
		}
		sort.Float64s(scratch)
		p95 := spPercentile(scratch, 0.95)
		p10 := spPercentile(scratch, 0.10)
		db := 20 * (math.Log10(p95+1e-12) - math.Log10(p10+1e-12))
		if db < 0 {
			db = 0
		}
		if db > 80 {
			db = 80
		}
		sum += db
	}
	return sum / float64(len(spContrastRanges))
}

// spPercentile is the linearly interpolated q-quantile (0..1) of a
// sorted slice. Empty input → 0.
func spPercentile(sorted []float64, q float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n == 1 {
		return sorted[0]
	}
	pos := q * float64(n-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo < 0 {
		lo = 0
	}
	if hi > n-1 {
		hi = n - 1
	}
	frac := pos - float64(lo)
	return sorted[lo]*(1-frac) + sorted[hi]*frac
}

// spFlatness is the geometric/arithmetic mean ratio of the magnitudes
// (0..1; ≈1 for noise, ≈0 for tonal spectra). 0 on silence.
func spFlatness(mag []float64) float64 {
	var den float64
	for _, m := range mag {
		if finite(m) {
			den += m * m
		}
	}
	if den <= 0 {
		return 0
	}
	n := 0
	var logSum, linSum float64
	for _, m := range mag {
		if !finite(m) || m < 0 {
			continue
		}
		logSum += math.Log(m + 1e-12)
		linSum += m + 1e-12
		n++
	}
	if n == 0 || linSum <= 0 {
		return 0
	}
	g := math.Exp(logSum / float64(n))
	a := linSum / float64(n)
	if a <= 0 {
		return 0
	}
	return clampUnit(g / a)
}

// spEntropy is the Shannon entropy of the power spectrum normalized by
// log(binCount) (0..1; ≈1 for noise, ≈0 for a pure tone / silence).
func spEntropy(mag []float64) float64 {
	var den float64
	for _, m := range mag {
		if finite(m) {
			den += m * m
		}
	}
	if den <= 0 {
		return 0
	}
	h := 0.0
	for _, m := range mag {
		if !finite(m) || m <= 0 {
			continue
		}
		p := m * m / den
		if p > 0 {
			h -= p * math.Log(p)
		}
	}
	lnN := math.Log(float64(len(mag)))
	if lnN <= 0 {
		return 0
	}
	return clampUnit(h / lnN)
}

// spZCR is the zero-crossing rate of the supplied mono window
// (crossings per sample pair, 0..1). Non-finite samples are skipped.
func spZCR(monoWindow []float64) float64 {
	pairs := 0
	crossings := 0
	for i := 0; i+1 < len(monoWindow); i++ {
		x, y := monoWindow[i], monoWindow[i+1]
		if !finite(x) || !finite(y) {
			continue
		}
		pairs++
		if (x < 0 && y >= 0) || (x >= 0 && y < 0) {
			crossings++
		}
	}
	if pairs == 0 {
		return 0
	}
	return float64(crossings) / float64(pairs)
}

// hpsFundamental estimates f0 (Hz) via a harmonic product spectrum:
// for each candidate bin b in [f0Lo, f0Hi] it scores the log-sum of the
// magnitudes at k·b (k = 1..8) and returns the best candidate's
// frequency. Ties resolve to the lowest bin (deterministic). 0 when no
// candidate exists.
func hpsFundamental(mag []float64, binW, f0Lo, f0Hi float64) float64 {
	if len(mag) < 10 || binW <= 0 {
		return 0
	}
	bLo := int(math.Ceil(f0Lo / binW))
	if bLo < 1 {
		bLo = 1
	}
	bHi := int(math.Floor(f0Hi / binW))
	if bHi >= len(mag) {
		bHi = len(mag) - 1
	}
	last := len(mag) - 1
	best, bestScore := 0, math.Inf(-1)
	for b := bLo; b <= bHi; b++ {
		var sc float64
		for k := 1; k <= 8; k++ {
			idx := b * k
			if idx > last {
				idx = last
			}
			m := mag[idx]
			if !finite(m) || m < 0 {
				m = 0
			}
			sc += math.Log(m + 1e-10)
		}
		if sc > bestScore {
			bestScore = sc
			best = b
		}
	}
	if best <= 0 {
		return 0
	}
	return float64(best) * binW
}

// harmonicPowerShare is the share (0..1) of the frame's power within
// ±1 bin of the harmonics k·f0, k = 1..8 (overlapping windows are
// merged so no bin is counted twice). 0 when f0 is unknown or the
// frame is silent.
func harmonicPowerShare(mag []float64, binW, f0 float64) float64 {
	if f0 <= 0 || binW <= 0 || len(mag) < 3 {
		return 0
	}
	var den float64
	for _, m := range mag {
		if finite(m) {
			den += m * m
		}
	}
	if den <= 0 {
		return 0
	}
	kb := f0 / binW
	last := len(mag) - 1
	var num float64
	covered := -1 // last bin already counted (merges overlapping windows)
	for k := 1; k <= 8; k++ {
		c := int(math.Round(kb * float64(k)))
		lo, hi := c-1, c+1
		if lo < 0 {
			lo = 0
		}
		if hi > last {
			hi = last
		}
		if lo <= covered {
			lo = covered + 1
		}
		if lo > hi {
			continue
		}
		for i := lo; i <= hi; i++ {
			m := mag[i]
			if finite(m) {
				num += m * m
			}
		}
		if hi > covered {
			covered = hi
		}
	}
	return clampUnit(num / den)
}

// spInharmonicity is the fraction (0..1) of the top-8
// parabolic-refined spectral peaks that are NOT within 1.5% of any
// multiple of f0. Peaks below 10% of the frame's largest magnitude are
// ignored (measurement floor: sidelobe humps of a pure tone must not
// count as inharmonic partials). 0 when f0 is unknown or no peaks.
func spInharmonicity(mag []float64, binW, f0 float64) float64 {
	if f0 <= 0 || len(mag) < 3 {
		return 0
	}
	maxMag := 0.0
	for _, m := range mag {
		if finite(m) && m > maxMag {
			maxMag = m
		}
	}
	if maxMag <= 0 {
		return 0
	}
	const floor = 0.1 // fraction of maxMag
	var bins [8]int
	var vals [8]float64
	n := 0
	for i := 1; i+1 < len(mag); i++ {
		m := mag[i]
		if !finite(m) || m <= 0 {
			continue
		}
		if m < floor*maxMag {
			continue
		}
		if m > mag[i-1] && m >= mag[i+1] {
			if n < 8 || m > vals[n-1] {
				pos := n
				if pos > 7 {
					pos = 7
				}
				if n < 8 {
					n++
				}
				for pos > 0 && vals[pos-1] < m {
					vals[pos] = vals[pos-1]
					bins[pos] = bins[pos-1]
					pos--
				}
				vals[pos] = m
				bins[pos] = i
			}
		}
	}
	if n == 0 {
		return 0
	}
	matched := 0
	for k := 0; k < n; k++ {
		d := spRefinePeak(mag, bins[k])
		f := (float64(bins[k]) + d) * binW
		if spNearHarmonic(f, f0) {
			matched++
		}
	}
	return clampUnit(float64(n-matched) / float64(n))
}

// spNearHarmonic reports whether f lies within 1.5% of any multiple of
// f0 (k = 1.. while k·f0 stays within reach of f).
func spNearHarmonic(f, f0 float64) bool {
	if f <= 0 || f0 <= 0 {
		return false
	}
	for k := 1; ; k++ {
		h := float64(k) * f0
		if h > f*1.015 {
			return false
		}
		if math.Abs(f-h) <= 0.015*h {
			return true
		}
	}
}

// ComputeChromaPoint builds one frame's 12-bin chroma (C, C#, .. B;
// C = 0). Each parabolic-refined spectral peak contributes its
// magnitude to the pitch class of its refined frequency
// (semitone = 69 + 12·log2(f/440) + offsetCents/100; pitch class =
// (round(semitone) % 12 + 12) % 12). Peaks below 55 Hz or above
// 5000 Hz are excluded. Weights are normalized so every bin is ≤ 1 and
// the sum ≤ 1. Silent frames produce an all-zero point.
//
// Peak-based (HPCP-style) accumulation is deliberate: with FFTSize
// 2048 the bin spacing (23.4 Hz) exceeds a semitone below ~500 Hz, so
// accumulating raw bins would smear a 440 Hz tone across neighbouring
// pitch classes (its discrete spectral maximum is the 468.75 Hz bin).
func ComputeChromaPoint(mag []float64, tuningOffsetCents float64) ChromaPoint {
	var pc [12]float64
	if len(mag) >= 3 {
		binW := spectrumBinWidth(len(mag))
		var total float64
		for i := 1; i+1 < len(mag); i++ {
			m := mag[i]
			if !finite(m) || m <= 0 {
				continue
			}
			if !(m > mag[i-1] && m >= mag[i+1]) {
				continue
			}
			d := spRefinePeak(mag, i)
			f := (float64(i) + d) * binW
			if f < 55 || f > 5000 {
				continue
			}
			semis := 69 + 12*math.Log2(f/440) + tuningOffsetCents/100
			if !finite(semis) {
				continue
			}
			idx := ((int(math.Round(semis)) % 12) + 12) % 12
			pc[idx] += m
			total += m
		}
		if total > 0 {
			for k := 0; k < 12; k++ {
				pc[k] /= total
			}
		}
	}
	var out ChromaPoint
	for k := 0; k < 12; k++ {
		out.Chroma[k] = sanitizeFloat32(pc[k], 0, 1)
	}
	out.TuningOffsetCents = float32(spSanitize(tuningOffsetCents, -50, 50))
	return out
}

// EstimateTuningOffsetCents estimates the global tuning offset from
// one magnitude spectrum: it takes the top-20 parabolic-refined peaks
// (weighted by magnitude), computes each peak's deviation from the
// nearest equal-tempered semitone in cents, and returns the weighted
// median clamped to ±50. Returns 0 when the spectrum has no usable
// peaks (e.g. silence).
func EstimateTuningOffsetCents(mag []float64) float64 {
	if len(mag) < 3 {
		return 0
	}
	binW := spectrumBinWidth(len(mag))
	type dev struct {
		cents, w float64
	}
	var top [20]dev
	n := 0
	for i := 1; i+1 < len(mag); i++ {
		m := mag[i]
		if !finite(m) || m <= 0 {
			continue
		}
		if !(m > mag[i-1] && m >= mag[i+1]) {
			continue
		}
		d := spRefinePeak(mag, i)
		f := (float64(i) + d) * binW
		if f <= 0 {
			continue
		}
		semis := 69 + 12*math.Log2(f/440)
		if !finite(semis) {
			continue
		}
		c := (semis - math.Round(semis)) * 100
		// insertion into the top-20 by weight (descending); earlier
		// bins win ties (stable, deterministic).
		if n < 20 {
			n++
		} else if m <= top[n-1].w {
			continue
		}
		pos := n - 1
		for pos > 0 && top[pos-1].w < m {
			top[pos] = top[pos-1]
			pos--
		}
		top[pos] = dev{c, m}
	}
	if n == 0 {
		return 0
	}
	// weighted median of the deviations (sort by cents ascending)
	s := top[:n]
	sort.Slice(s, func(a, b int) bool { return s[a].cents < s[b].cents })
	var totalW, cum float64
	for _, d := range s {
		totalW += d.w
	}
	half := totalW / 2
	for _, d := range s {
		cum += d.w
		if cum >= half {
			return spSanitize(d.cents, -50, 50)
		}
	}
	return spSanitize(s[len(s)-1].cents, -50, 50)
}

// spRefinePeak parabolically refines the peak at bin i in the
// log-magnitude domain and returns the sub-bin offset δ ∈ [-0.5, 0.5]
// (0 when the neighborhood is not concave).
func spRefinePeak(mag []float64, i int) float64 {
	a := math.Log(mag[i-1] + 1e-12)
	b := math.Log(mag[i] + 1e-12)
	c := math.Log(mag[i+1] + 1e-12)
	den := a - 2*b + c
	if den >= -1e-30 {
		return 0
	}
	d := 0.5 * (a - c) / den
	if d > 0.5 {
		d = 0.5
	} else if d < -0.5 {
		d = -0.5
	}
	return d
}

// spSanitize returns min for NaN/±Inf, otherwise v clamped to
// [min, max] (float64 twin of sanitizeFloat32).
func spSanitize(v, min, max float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return min
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
