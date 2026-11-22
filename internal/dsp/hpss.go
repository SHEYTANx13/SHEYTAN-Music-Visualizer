package dsp

// hpss.go — v3 harmonic/percussive separation layer (§
// analysis_types.go "HPSS layer"): streaming median-filter HPSS
// evidence, NOT source separation. Values are energy-share evidence
// (0..1 of the frame's total magnitude energy; H+P+R = 1).
//
// Design:
//   - Harmonic evidence: per-bin temporal median over the last 31
//     magnitude frames (steady content survives the median; transients
//     do not).
//   - Percussive evidence: frequency-axis median with a 17-bin kernel
//     over the current frame (broadband transients rise above their
//     spectral neighbourhood; sustained peaks do not).
//   - Soft masks: wH = h/(h+p), wP = p/(h+p) per bin; harmonic share =
//     Σ(mag·wH)² / Σmag², percussive share = Σ(mag·wP)² / Σmag², and
//     residual = 1 - H - P (≥ 0 because (h²+p²)/(h+p)² ≤ 1 per bin),
//     so the three shares sum to exactly 1.
//   - Before ≥ 8 frames are buffered the decomposition is undefined
//     and Decompose returns all zeros.
//
// Memory: the ring stores 31 frames of FFTSize/2+1 magnitudes as
// float32 ≈ 127 KB for the 2048-point STFT; median scratch buffers are
// allocated once and reused (no per-call allocation).
//
// Determinism: medians sort fixed-size scratch buffers; no maps, no
// randomness; non-finite magnitudes are stored as 0.

import (
	"sort"
)

// HPSSPoint is one frame's HPSS evidence (shares sum to ≈1; all 0
// before the ring is warm).
type HPSSPoint struct {
	Harmonic   float64
	Percussive float64
	Residual   float64
}

const (
	// hpssRingFrames is the temporal median window (last N frames).
	hpssRingFrames = 31
	// hpssMinFrames is the warm-up: frames buffered before Decompose
	// produces non-zero shares.
	hpssMinFrames = 8
	// hpssFreqKernel is the frequency-axis median kernel width (bins).
	hpssFreqKernel = 17
	// hpssHalfKernel is (hpssFreqKernel-1)/2.
	hpssHalfKernel = 8
)

// HPSS is a streaming median-filter HPSS decomposer. One instance
// analyzes one stream; not safe for concurrent use.
type HPSS struct {
	nBins int
	ring  [][]float32 // hpssRingFrames slots, each nBins magnitudes
	pos   int         // next write slot
	count int         // frames buffered (0..hpssRingFrames)
	cur   []float32   // last pushed frame
	tCol  []float64   // temporal median scratch (reused)
	fRow  []float64   // frequency median scratch (reused)
}

// NewHPSS creates an idle decomposer; storage is allocated on the
// first Push (which fixes the spectrum length).
func NewHPSS() *HPSS {
	return &HPSS{}
}

// init (re)allocates the ring for the given spectrum length.
func (h *HPSS) init(n int) {
	h.nBins = n
	h.ring = make([][]float32, hpssRingFrames)
	for i := range h.ring {
		h.ring[i] = make([]float32, n)
	}
	h.cur = make([]float32, n)
	h.tCol = make([]float64, hpssRingFrames)
	h.fRow = make([]float64, hpssFreqKernel)
	h.pos, h.count = 0, 0
}

// Push appends one magnitude frame to the ring (it becomes the frame
// Decompose will analyze). A length change re-initializes the ring.
func (h *HPSS) Push(mag []float64) {
	if len(mag) != h.nBins {
		h.init(len(mag))
	}
	if h.nBins == 0 {
		return
	}
	slot := h.ring[h.pos]
	for i, m := range mag {
		if finite(m) && m > 0 {
			slot[i] = float32(m)
		} else {
			slot[i] = 0
		}
	}
	copy(h.cur, slot)
	h.pos = (h.pos + 1) % hpssRingFrames
	if h.count < hpssRingFrames {
		h.count++
	}
}

// Decompose analyzes the last pushed frame against the ring. Before
// hpssMinFrames frames are buffered (and for silent frames) it returns
// all zeros; otherwise H+P+R = 1.
func (h *HPSS) Decompose() HPSSPoint {
	if h.count < hpssMinFrames || h.nBins == 0 {
		return HPSSPoint{}
	}
	n := h.nBins
	var tE, pE, tot float64
	for i := 0; i < n; i++ {
		m := float64(h.cur[i])
		tot += m * m

		// temporal median of bin i over the buffered frames
		for j := 0; j < h.count; j++ {
			h.tCol[j] = float64(h.ring[j][i])
		}
		hv := hpssMedian(h.tCol[:h.count])

		// frequency median (kernel hpssFreqKernel) of the current
		// frame around bin i, clamped at the spectrum edges
		k := 0
		for d := -hpssHalfKernel; d <= hpssHalfKernel; d++ {
			idx := i + d
			if idx < 0 {
				idx = 0
			} else if idx >= n {
				idx = n - 1
			}
			h.fRow[k] = float64(h.cur[idx])
			k++
		}
		pv := hpssMedian(h.fRow)

		if hv+pv > 0 {
			wh := hv / (hv + pv)
			wp := pv / (hv + pv)
			tE += m * m * wh * wh
			pE += m * m * wp * wp
		}
	}
	if tot <= 0 {
		return HPSSPoint{}
	}
	hShare := clampUnit(tE / tot)
	pShare := clampUnit(pE / tot)
	r := 1 - hShare - pShare
	if r < 0 {
		r = 0
	}
	return HPSSPoint{Harmonic: hShare, Percussive: pShare, Residual: r}
}

// hpssMedian sorts x in place (small fixed-size scratch) and returns
// the middle element (upper median for even lengths).
func hpssMedian(x []float64) float64 {
	sort.Float64s(x)
	return x[len(x)/2]
}

// AppendHPSSPoint appends one sanitized point to the three parallel
// HPSSSeries arrays.
func AppendHPSSPoint(dst *HPSSSeries, p HPSSPoint) {
	dst.Harmonic = append(dst.Harmonic, sanitizeFloat32(p.Harmonic, 0, 1))
	dst.Percussive = append(dst.Percussive, sanitizeFloat32(p.Percussive, 0, 1))
	dst.Residual = append(dst.Residual, sanitizeFloat32(p.Residual, 0, 1))
}
