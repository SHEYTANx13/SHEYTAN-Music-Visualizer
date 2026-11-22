package dsp

// loudness.go — v3 dynamics layer (§ analysis_types.go "Dynamics /
// loudness layer"): ITU-R BS.1770-5 loudness, EBU Tech 3342 loudness
// range, sample/true peak, crest, clipping blocks and measured silence
// intervals.
//
// Reference: ITU-R BS.1770-5, "Measurement of loudness". The
// K-weighting cascade is stage 1 (high-shelf, +4 dB asymptote)
// followed by stage 2 (RLB high-pass), with the 48 kHz exact
// coefficients from the standard (BS.1770-5 Annex, pre-warpless
// bilinear design). Channel weights G = [1, 1] (left, right); block
// loudness = -0.691 + 10·log10(Σ_ch G_ch · z_ch) where z_ch is the
// mean square of the K-filtered channel over the block.
//
// Measurements (all deterministic, bounded memory):
//   - Momentary: 400 ms blocks evaluated every 100 ms (BS.1770 gating
//     blocks). Short-term: 3 s window evaluated every 100 ms.
//   - Integrated: BS.1770-5 two-stage gating — absolute gate -70 LUFS,
//     then relative gate (gated mean - 10 LU), energy means.
//   - LRA (EBU Tech 3342): 3 s blocks on a 1 s grid, absolute gate
//     -70 LUFS, relative gate (integrated - 20 LU), range =
//     95th - 10th percentile of the gated distribution.
//   - SamplePeak: max |x| in dBFS. TruePeak: 49-tap windowed-sinc 4×
//     polyphase FIR interpolation (kernel built once), reported in
//     dBTP with the -70 measurement floor.
//   - CrestFactorDB = SamplePeak - 20·log10(track RMS) (0 on silence).
//   - Clipping: per 400 ms block, flagged when it contains ≥ 3
//     consecutive samples with |x| ≥ 0.999 in either channel;
//     ClippingRatio = flagged blocks / total 400 ms blocks.
//   - MomentaryLUFS / ShortTermLUFS are decimated to the main hop
//     grid (HopSize/SampleRate): value at frame f is the last block
//     ending at or before f·hopSeconds, clamped with clampLUFSCeil
//     (-70 before the first block exists).
//   - SilenceIntervals: runs of momentary blocks whose loudness is
//     below SilenceThresholdLUFS (-60 LUFS). The 400 ms momentary grid
//     is used (not the 3 s short-term window) because a short-term
//     window cannot drop below -60 LUFS inside a pause shorter than
//     itself; interval bounds are the first fully-silent block's start
//     and the last fully-silent block's end.
//
// Any Process chunking produces identical state transitions (state is
// per-sample), so results are chunk-independent (verified by tests).

import (
	"math"
	"sort"
)

// K-weighting biquad coefficients — ITU-R BS.1770-5, 48 kHz exact.
// Stage 1: high-shelf (+4 dB asymptote above ~2 kHz).
var kShelfB = [3]float64{1.53512485958697, -2.69169618940638, 1.19839281085285}
var kShelfA = [3]float64{1, -1.69065929318241, 0.73248077421585}

// Stage 2: RLB high-pass (~38 Hz, -0.17 dB asymptote below).
var kHPFB = [3]float64{1, -2, 1}
var kHPFA = [3]float64{1, -1.99004715443333, 0.99007225036621}

const (
	loudBlockSamples    = 19200 // 400 ms block at 48 kHz (BS.1770 block)
	loudSubBlockSamples = 4800  // 100 ms evaluation hop at 48 kHz
	loudSTSubBlocks     = 30    // 3 s short-term window = 30 × 100 ms
	loudMomentaryBlocks = 4     // 400 ms momentary = 4 × 100 ms
	loudSRBlocks        = 10    // sub-blocks per second (LRA grid)
	loudClipThreshold   = 0.999 // |x| at/beyond this counts as clipped
	loudClipRun         = 3     // consecutive clipped samples to flag a block
	loudTruePeakTaps    = 49    // interpolation kernel taps (windowed sinc)
)

// biquad is a direct-form-1 second-order section.
type biquad struct {
	b0, b1, b2     float64
	a1, a2         float64
	x1, x2, y1, y2 float64
}

func (q *biquad) step(x float64) float64 {
	y := q.b0*x + q.b1*q.x1 + q.b2*q.x2 - q.a1*q.y1 - q.a2*q.y2
	q.x2, q.x1 = q.x1, x
	q.y2, q.y1 = q.y1, y
	return y
}

// LoudnessMeter accumulates BS.1770-5 dynamics over an interleaved
// stereo stream. One instance analyzes one stream; not safe for
// concurrent use.
type LoudnessMeter struct {
	shelf [2]biquad // stage 1 per channel
	hpf   [2]biquad // stage 2 per channel

	sumL, sumR    float64                  // current sub-block filtered-square sums
	ringL, ringR  [loudSTSubBlocks]float64 // completed sub-block sums
	ringPos       int                      // next ring write slot
	ringCount     int                      // completed sub-blocks (0..30)
	perCh         int                      // samples per channel processed
	mZ, stZ, lraZ []float64                // block mean-square sums (L+R)

	samplePeak       float64        // max |x| (linear)
	tpMax            float64        // max |4x-oversampled x| (linear)
	tpHistL, tpHistR [13]float64    // polyphase FIR history (x[i-k])
	tpF              [4][13]float64 // polyphase kernel (built once)
	sqAll            float64        // raw-sample square sum (track RMS)

	frameCount    int
	frameCountSet bool

	runL, runR    int  // consecutive clipped samples in current block
	blockClip     bool // current 400 ms block flagged
	blocksTotal   int
	blocksClipped int

	// pending sample carry: Process may be called with any chunking,
	// including odd-length chunks; the first sample of a stereo pair
	// is held until its partner arrives in a later chunk.
	pendL     float64
	pendValid bool
}

// NewLoudnessMeter creates a meter with the BS.1770-5 K-weighting
// cascade and the (once-built) true-peak polyphase kernel.
func NewLoudnessMeter() *LoudnessMeter {
	m := &LoudnessMeter{}
	for ch := 0; ch < 2; ch++ {
		m.shelf[ch] = biquad{b0: kShelfB[0], b1: kShelfB[1], b2: kShelfB[2], a1: kShelfA[1], a2: kShelfA[2]}
		m.hpf[ch] = biquad{b0: kHPFB[0], b1: kHPFB[1], b2: kHPFB[2], a1: kHPFA[1], a2: kHPFA[2]}
	}
	m.tpF = truePeakFilter
	return m
}

// SetFrameCount declares the hop-grid length the decimated
// MomentaryLUFS / ShortTermLUFS series must have (call before Finish).
// When unset, Finish infers it from the processed duration.
func (m *LoudnessMeter) SetFrameCount(n int) {
	m.frameCount = n
	m.frameCountSet = true
}

// Process consumes interleaved stereo samples (L, R, L, R, ...). Any
// chunking is allowed — including odd-length chunks: a trailing
// unpaired sample is carried over to the next call (a lone final
// sample at stream end is ignored). Chunks never resample the stream;
// identical sample sequences produce identical state transitions.
func (m *LoudnessMeter) Process(interleaved []float64) {
	for _, x := range interleaved {
		if !m.pendValid {
			m.pendL = x
			m.pendValid = true
			continue
		}
		m.processPair(m.pendL, x)
		m.pendValid = false
	}
}

// processPair consumes one complete (L, R) sample pair.
func (m *LoudnessMeter) processPair(l, r float64) {
	if !finite(l) {
		l = 0
	}
	if !finite(r) {
		r = 0
	}

	// sample peak + track RMS
	if a := math.Abs(l); a > m.samplePeak {
		m.samplePeak = a
	}
	if a := math.Abs(r); a > m.samplePeak {
		m.samplePeak = a
	}
	m.sqAll += l*l + r*r

	// true peak (4× oversampled, both channels)
	m.tpStep(&m.tpHistL, l)
	m.tpStep(&m.tpHistR, r)

	// K-weighting: stage 1 shelf then stage 2 high-pass
	yl := m.hpf[0].step(m.shelf[0].step(l))
	yr := m.hpf[1].step(m.shelf[1].step(r))
	m.sumL += yl * yl
	m.sumR += yr * yr
	m.perCh++

	// clipping runs (counted within each 400 ms block)
	if math.Abs(l) >= loudClipThreshold {
		m.runL++
	} else {
		m.runL = 0
	}
	if math.Abs(r) >= loudClipThreshold {
		m.runR++
	} else {
		m.runR = 0
	}
	if m.runL >= loudClipRun || m.runR >= loudClipRun {
		m.blockClip = true
	}

	// 100 ms sub-block boundary: publish momentary / short-term
	if m.perCh%loudSubBlockSamples == 0 {
		m.ringL[m.ringPos] = m.sumL
		m.ringR[m.ringPos] = m.sumR
		m.ringPos = (m.ringPos + 1) % loudSTSubBlocks
		if m.ringCount < loudSTSubBlocks {
			m.ringCount++
		}
		m.sumL, m.sumR = 0, 0
		if m.ringCount >= loudMomentaryBlocks {
			m.mZ = append(m.mZ, m.windowZ(loudMomentaryBlocks))
		}
		if m.ringCount >= loudSTSubBlocks {
			z := m.windowZ(loudSTSubBlocks)
			m.stZ = append(m.stZ, z)
			// LRA grid: 3 s blocks every 1 s
			if m.perCh%(loudSRBlocks*loudSubBlockSamples) == 0 {
				m.lraZ = append(m.lraZ, z)
			}
		}
	}

	// 400 ms block boundary: finalize clipping bookkeeping
	if m.perCh%loudBlockSamples == 0 {
		m.blocksTotal++
		if m.blockClip {
			m.blocksClipped++
		}
		m.blockClip = false
		m.runL, m.runR = 0, 0
	}
}

// windowZ sums the last n completed 100 ms sub-blocks and returns the
// window's combined (L+R) mean square. Caller guarantees ringCount ≥ n.
func (m *LoudnessMeter) windowZ(n int) float64 {
	var l, r float64
	for k := 0; k < n; k++ {
		i := (m.ringPos - 1 - k + loudSTSubBlocks) % loudSTSubBlocks
		l += m.ringL[i]
		r += m.ringR[i]
	}
	den := float64(n * loudSubBlockSamples)
	return (l + r) / den
}

// tpStep runs the 4× polyphase interpolator for one input sample of
// one channel and updates the true-peak maximum.
func (m *LoudnessMeter) tpStep(hist *[13]float64, x float64) {
	for k := 12; k > 0; k-- {
		hist[k] = hist[k-1]
	}
	hist[0] = x
	for p := 0; p < 4; p++ {
		hp := &m.tpF[p]
		acc := 0.0
		for k := 0; k < 13; k++ {
			acc += hp[k] * hist[k]
		}
		if a := math.Abs(acc); a > m.tpMax {
			m.tpMax = a
		}
	}
}

// truePeakFilter is the 49-tap windowed-sinc 4× polyphase kernel,
// built once at package init (deterministic). Phase p's output is
// Σ_k truePeakFilter[p][k]·x[i-k]; per-phase normalization gives each
// phase unity DC gain so constant signals interpolate exactly.
var truePeakFilter = buildTruePeakFilter()

func buildTruePeakFilter() (ph [4][13]float64) {
	// h[n] = sinc((n-24)/4) · Hann window over [-24, 24]; taps 49, the
	// 3 trailing zeros pad the polyphase split to 13 taps per phase.
	var h [52]float64
	const center = 24
	for n := 0; n < loudTruePeakTaps; n++ {
		t := float64(n-center) / 4.0
		var s float64
		if t == 0 {
			s = 1
		} else {
			s = math.Sin(math.Pi*t) / (math.Pi * t)
		}
		w := 0.5 * (1 + math.Cos(math.Pi*float64(n-center)/24.0))
		h[n] = s * w
	}
	for p := 0; p < 4; p++ {
		sum := 0.0
		for k := 0; k < 13; k++ {
			sum += h[p+4*k]
		}
		if sum > 0 {
			for k := 0; k < 13; k++ {
				ph[p][k] = h[p+4*k] / sum
			}
		}
	}
	return ph
}

// Finish closes the final partial block and returns the summary.
func (m *LoudnessMeter) Finish() LoudnessSummary {
	if m.perCh > 0 && m.perCh%loudBlockSamples != 0 {
		m.blocksTotal++
		if m.blockClip {
			m.blocksClipped++
		}
		m.blockClip = false
	}

	var s LoudnessSummary
	s.IntegratedLUFS = gateIntegratedLUFS(m.mZ)
	s.LoudnessRangeLU = lraFromBlocks(m.lraZ, s.IntegratedLUFS)

	if m.samplePeak > 0 {
		s.SamplePeakDBFS = clampLUFSCeil(20 * math.Log10(m.samplePeak))
	} else {
		s.SamplePeakDBFS = -70
	}
	if m.tpMax > 0 {
		s.TruePeakDBFS = clampLUFSCeil(20 * math.Log10(m.tpMax))
	} else {
		s.TruePeakDBFS = -70
	}
	if m.perCh > 0 && m.samplePeak > 0 {
		rms := math.Sqrt(m.sqAll / float64(2*m.perCh))
		if rms > 0 {
			s.CrestFactorDB = 20 * math.Log10(m.samplePeak/rms)
		}
	}
	if m.blocksTotal > 0 {
		s.ClippingFrames = m.blocksClipped
		s.ClippingRatio = float64(m.blocksClipped) / float64(m.blocksTotal)
	}

	n := m.frameCount
	if !m.frameCountSet {
		hopSec := float64(HopSize) / float64(SampleRate)
		if m.perCh > 0 {
			n = int(float64(m.perCh)/float64(SampleRate)/hopSec) + 1
		}
	}
	if n < 0 {
		n = 0
	}
	s.MomentaryLUFS = decimateLUFS(m.mZ, n, loudMomentaryBlocks)
	s.ShortTermLUFS = decimateLUFS(m.stZ, n, loudSTSubBlocks)
	s.SilenceIntervals = silenceIntervals(m.mZ)
	return s
}

// lufsOf converts a combined mean square to LUFS with the -0.691
// offset, clamped to the -70 measurement floor (finite always).
func lufsOf(z float64) float64 {
	return clampLUFSCeil(-0.691 + 10*math.Log10(z))
}

// gateIntegratedLUFS applies BS.1770-5 gating to the momentary block
// mean squares: absolute gate (-70 LUFS), then relative gate
// (gated energy mean - 10 LU). Returns -70 when nothing survives.
func gateIntegratedLUFS(z []float64) float64 {
	absGate := math.Pow(10, (-70+0.691)/10)
	var sum float64
	n := 0
	for _, zv := range z {
		if zv > absGate {
			sum += zv
			n++
		}
	}
	if n == 0 {
		return -70
	}
	g := lufsOf(sum / float64(n))
	relGate := math.Pow(10, (g-10+0.691)/10)
	sum, n = 0, 0
	for _, zv := range z {
		if zv > absGate && zv > relGate {
			sum += zv
			n++
		}
	}
	if n == 0 {
		return g
	}
	return lufsOf(sum / float64(n))
}

// lraFromBlocks computes the EBU Tech 3342 loudness range over the 3 s
// block mean squares: absolute gate -70 LUFS, relative gate
// (integrated - 20 LU), then 95th-10th percentile of the gated
// loudness distribution. 0 when unmeasurable.
func lraFromBlocks(z []float64, integrated float64) float64 {
	absGate := math.Pow(10, (-70+0.691)/10)
	relGate := math.Pow(10, (integrated-20+0.691)/10)
	kept := make([]float64, 0, len(z))
	for _, zv := range z {
		if zv > absGate && zv > relGate {
			kept = append(kept, lufsOf(zv))
		}
	}
	if len(kept) == 0 {
		return 0
	}
	lra := loudPercentile(kept, 0.95) - loudPercentile(kept, 0.10)
	if lra < 0 {
		lra = 0
	}
	return lra
}

// loudPercentile returns the q-quantile (0..1) of vals (sorts a copy;
// deterministic; empty → 0).
func loudPercentile(vals []float64, q float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	s := make([]float64, len(vals))
	copy(s, vals)
	sort.Float64s(s)
	i := int(q * float64(len(s)-1))
	if i < 0 {
		i = 0
	}
	if i > len(s)-1 {
		i = len(s) - 1
	}
	return s[i]
}

// decimateLUFS maps block mean squares onto the hop grid: frame f gets
// the last block ending at or before f·hopSeconds (clamped to the -70
// floor; -70 before any block exists). windowBlocks is the block
// length in 100 ms sub-blocks.
func decimateLUFS(z []float64, frames, windowBlocks int) []float32 {
	out := make([]float32, frames)
	hopSec := float64(HopSize) / float64(SampleRate)
	for f := 0; f < frames; f++ {
		// completed 100 ms sub-blocks at frame f's grid time
		k := int(math.Floor(float64(f)*hopSec*10 + 1e-9))
		if k < windowBlocks {
			out[f] = -70
			continue
		}
		idx := k - windowBlocks
		if idx >= len(z) {
			idx = len(z) - 1
		}
		if idx < 0 {
			out[f] = -70
			continue
		}
		out[f] = float32(lufsOf(z[idx]))
	}
	return out
}

// silenceIntervals merges runs of momentary blocks below the -60 LUFS
// silence gate into measured intervals. Block j spans
// [j·0.1 s, (j+4)·0.1 s], so the interval is the first silent block's
// start through the last silent block's end.
func silenceIntervals(z []float64) []SilenceInterval {
	var out []SilenceInterval
	open := -1
	minL := 0.0
	flush := func(last int) {
		if open >= 0 {
			out = append(out, SilenceInterval{
				Start:   float64(open) * 0.1,
				End:     float64(last+loudMomentaryBlocks) * 0.1,
				MinLUFS: minL,
			})
			open = -1
		}
	}
	for j, zv := range z {
		l := lufsOf(zv)
		if l < SilenceThresholdLUFS {
			if open < 0 {
				open = j
				minL = l
			} else if l < minL {
				minL = l
			}
		} else {
			flush(j - 1)
		}
	}
	flush(len(z) - 1)
	return out
}
