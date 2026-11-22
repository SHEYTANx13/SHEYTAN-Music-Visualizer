package dsp

// stereo.go — v3 stereo measurement layer (§ analysis_types.go
// "Stereo layer"). Per-hop windowed RMS of the pure channels plus mid /
// side, the side/(side+mid) width ratio and the windowed Pearson
// correlation of L and R.
//
// Units: all energies are linear RMS (≈0..1 for canonical ±1 PCM);
// Width is 0..1 (0 on mono material); Correlation is -1..1 (-1 marks
// anti-phase material). The caller supplies the frame's windowed
// samples (Hann applied upstream, matching the legacy Analyzer
// pipeline); "windowed RMS" means RMS over the supplied window.
//
// Determinism: pure function of the window samples; non-finite input
// samples are treated as 0 so no NaN/Inf can reach stored series.

import "math"

// StereoPoint is one analysis window's stereo measurement.
type StereoPoint struct {
	// L, R are the windowed RMS of the pure left / right channels
	// (linear, ≈0..1).
	L, R float64
	// M, S are the windowed RMS of mid ((L+R)/2) and side ((L-R)/2).
	M, S float64
	// Width is SE/(SE+ME) guarded to 0..1 (0 when the window is
	// silent or mono). Note the measure's range: uncorrelated (wide)
	// material saturates at 0.5; only anti-phase content approaches 1.
	Width float64
	// Correlation is the windowed Pearson correlation of L and R
	// (-1..1). Windows without level variance (silence / DC) report 0.
	Correlation float64
}

// ComputeStereoFrame measures one stereo window. l and r must hold the
// same window; a length mismatch is resolved to the shorter input.
func ComputeStereoFrame(l, r []float64) StereoPoint {
	n := len(l)
	if len(r) < n {
		n = len(r)
	}
	if n == 0 {
		return StereoPoint{}
	}
	var sumL, sumR, sumLL, sumRR, sumLR float64
	for i := 0; i < n; i++ {
		x, y := l[i], r[i]
		if !finite(x) {
			x = 0
		}
		if !finite(y) {
			y = 0
		}
		sumL += x
		sumR += y
		sumLL += x * x
		sumRR += y * y
		sumLR += x * y
	}
	fn := float64(n)
	mL := sumL / fn
	mR := sumR / fn

	le := math.Sqrt(sumLL / fn)
	re := math.Sqrt(sumRR / fn)

	// mid/side RMS via identities:
	//   Σ((x+y)/2)² = (Σx² + Σy² + 2Σxy) / 4
	//   Σ((x-y)/2)² = (Σx² + Σy² - 2Σxy) / 4
	msq := (sumLL + sumRR + 2*sumLR) / (4 * fn)
	ssq := (sumLL + sumRR - 2*sumLR) / (4 * fn)
	if msq < 0 {
		msq = 0
	}
	if ssq < 0 {
		ssq = 0
	}
	me := math.Sqrt(msq)
	se := math.Sqrt(ssq)

	width := 0.0
	if se+me > 0 {
		width = se / (se + me)
	}

	// Pearson correlation of the raw channel samples.
	corr := 0.0
	varL := sumLL/fn - mL*mL
	varR := sumRR/fn - mR*mR
	if varL > 0 && varR > 0 {
		cov := sumLR/fn - mL*mR
		corr = cov / math.Sqrt(varL*varR)
		if corr > 1 {
			corr = 1
		} else if corr < -1 {
			corr = -1
		}
	}

	return StereoPoint{
		L:           le,
		R:           re,
		M:           me,
		S:           se,
		Width:       clampUnit(width),
		Correlation: corr,
	}
}

// AppendStereoSeries appends one sanitized point to the six parallel
// StereoSeries arrays (energies/width 0..1, correlation -1..1).
func AppendStereoSeries(dst *StereoSeries, p StereoPoint) {
	dst.LEnergy = append(dst.LEnergy, sanitizeFloat32(p.L, 0, 1))
	dst.REnergy = append(dst.REnergy, sanitizeFloat32(p.R, 0, 1))
	dst.MEnergy = append(dst.MEnergy, sanitizeFloat32(p.M, 0, 1))
	dst.SEnergy = append(dst.SEnergy, sanitizeFloat32(p.S, 0, 1))
	dst.Width = append(dst.Width, sanitizeFloat32(p.Width, 0, 1))
	dst.Correlation = append(dst.Correlation, sanitizeFloat32(p.Correlation, -1, 1))
}

// finite reports whether x is usable (not NaN, not ±Inf).
func finite(x float64) bool {
	return !math.IsNaN(x) && !math.IsInf(x, 0)
}
