package dsp

// vocalflow.go — musical vocal-flow layer (v3 contract, see
// analysis_types.go VocalFlow).
//
// ACOUSTIC ESTIMATE — NOT lyric or syllable transcription. Phrases,
// syllable rates and density are acoustic proxies (presence-band energy,
// harmonicity, syllabic amplitude modulation) validated only against
// synthetic fixtures (corpus.go VocalLike); consumers must describe
// them as proxies.
//
// Evidence fusion (heuristic calibration; the weights are fixed here so
// results stay comparable across versions):
//
//	likelihood = 0.30·presence          (bands 5+6 share: 1-4 kHz formants)
//	           + 0.25·harmonicity       (spectral layer)
//	           + 0.20·(1-flatness)      (tonal vs noise-like)
//	           + 0.15·harmonicDominance (HPSS harmonic share)
//	           + 0.10·modulation        (±0.5 s depth of the smoothed
//	                             1-4 kHz energy = 3-8 Hz syllabic band)
//
// Honesty: AccentScore/Syncopation of a phrase require a beat grid;
// this signature receives none, so both stay 0 rather than fabricating
// an alignment (the orchestrator may post-fill them from Rhythm.Beats).

import "math"

// mzVocalGate is the phrase likelihood threshold.
const mzVocalGate = 0.45

// EstimateVocalFlow estimates vocal-like phrases and delivery flow from
// measured series. All series are hop-grid aligned (index i covers
// i·hopSec).
func EstimateVocalFlow(frames []Frame, bands *BandTimeline, hpss *HPSSSeries, spectral *SpectralSeries, hopSec float64) *VocalFlow {
	vf := &VocalFlow{}
	n := len(frames)
	if bands == nil || hopSec <= 0 || n == 0 {
		return vf
	}
	for b := 0; b < BandCount; b++ {
		if len(bands.Energy[b]) < n {
			n = len(bands.Energy[b])
		}
	}
	if n == 0 {
		return vf
	}
	var harmArr, flatArr, hdArr []float32
	if spectral != nil {
		harmArr, flatArr = spectral.Harmonicity, spectral.Flatness
	}
	if hpss != nil {
		hdArr = hpss.Harmonic
	}
	at := func(a []float32, i int) float64 {
		if i >= 0 && i < len(a) {
			return float64(a[i])
		}
		return 0
	}

	// presence: 1-4 kHz formant-band ABSOLUTE energy (bands 5+6 scaled
	// by the frame power, since bands.Energy stores SHARES of the frame
	// total). The syllabic carrier MUST be absolute: a share is
	// AM-invariant when the whole mix modulates together, which would
	// erase the syllabic modulation this layer depends on.
	presence := make([]float64, n)
	for f := 0; f < n; f++ {
		power := float64(frames[f].RMS) * float64(frames[f].RMS)
		presence[f] = (at(bands.Energy[5], f) + at(bands.Energy[6], f)) * power
	}
	// syllabic carrier: lightly smoothed presence
	mid := make([]float64, n)
	for f := 0; f < n; f++ {
		var s float64
		c := 0
		for d := -1; d <= 1; d++ {
			j := f + d
			if j >= 0 && j < n {
				s += presence[j]
				c++
			}
		}
		if c > 0 {
			mid[f] = s / float64(c)
		}
	}
	// syllabic modulation depth over ±0.5 s (robust percentile spread)
	win := int(0.5/hopSec + 0.5)
	if win < 4 {
		win = 4
	}
	mod := make([]float64, n)
	buf := make([]float64, 0, 2*win+1)
	for f := 0; f < n; f++ {
		lo, hi := f-win, f+win
		if lo < 0 {
			lo = 0
		}
		if hi > n-1 {
			hi = n - 1
		}
		buf = buf[:0]
		for j := lo; j <= hi; j++ {
			buf = append(buf, mid[j])
		}
		p90 := mzPercentile(buf, 0.9)
		p10 := mzPercentile(buf, 0.1)
		mod[f] = clampUnit((p90 - p10) / (p90 + p10 + 1e-9))
	}

	modAt := func(f int) bool { return f >= 0 && f < n && mod[f] >= 0.18 }
	// fused likelihood. The SYLLABIC MODULATION is the primary vocal
	// evidence: sustained tonal content (pads, strings) shares the
	// presence-band and harmonic evidence with voices, and only the
	// measured modulation separates vocal-like flow from a sustained
	// bed. Weights are documented heuristic calibration.
	// Formant evidence relative to the MID/TREBLE content (bands 3-8):
	// as a share of TOTAL energy, a bass-heavy mix hides the 1-4 kHz
	// formant band even under a real voice; relative to the mid band it
	// discriminates vocal material from sustained beds.
	formant := make([]float64, n)
	for f := 0; f < n; f++ {
		midTot := 0.0
		for b := 3; b < BandCount; b++ {
			midTot += at(bands.Energy[b], f)
		}
		formant[f] = (at(bands.Energy[6], f) + 0.5*at(bands.Energy[5], f)) / math.Max(0.05, midTot)
	}
	// presence share (of total energy) for the evidence counting below.
	presShare := make([]float64, n)
	for f := 0; f < n; f++ {
		tot := 0.0
		for b := 0; b < BandCount; b++ {
			tot += at(bands.Energy[b], f)
		}
		if tot > 1e-9 {
			presShare[f] = presence[f] / tot
		}
	}
	lik := make([]float64, n)
	for f := 0; f < n; f++ {
		if frames[f].RMS < 0.002 && presShare[f] < 1e-4 {
			continue // silence stays silent
		}
		lik[f] = clampUnit(0.35*mod[f] +
			0.25*formant[f] +
			0.2*at(harmArr, f) +
			0.1*(1-at(flatArr, f)) +
			0.1*at(hdArr, f))
	}
	// smoothing (3-tap mean)
	lS := make([]float64, n)
	for f := 0; f < n; f++ {
		var s float64
		c := 0
		for d := -1; d <= 1; d++ {
			j := f + d
			if j >= 0 && j < n {
				s += lik[j]
				c++
			}
		}
		if c > 0 {
			lS[f] = s / float64(c)
		}
	}
	vf.Likelihood = make([]float32, n)
	for f := range vf.Likelihood {
		vf.Likelihood[f] = sanitizeFloat32(lS[f], 0, 1)
	}

	// ----- phrases: contiguous runs above the gate, ≥ 0.4 s, gaps < 0.35 s
	gapF := int(0.5/hopSec + 0.5)
	minF := int(0.4/hopSec + 0.5)
	if minF < 1 {
		minF = 1
	}
	type rng struct{ fs, fe int }
	var runs []rng
	in := false
	fs := 0
	for f := 0; f <= n; f++ {
		on := f < n && lS[f] >= mzVocalGate && modAt(f)
		if on && !in {
			in, fs = true, f
		}
		if !on && in {
			in = false
			runs = append(runs, rng{fs, f})
		}
	}
	merged := make([]rng, 0, len(runs))
	for _, r := range runs {
		if len(merged) > 0 && r.fs-merged[len(merged)-1].fe < gapF {
			merged[len(merged)-1].fe = r.fe
			continue
		}
		merged = append(merged, r)
	}
	type phr struct {
		start, end int
		rate       float64
		conf       float64
		peaks      []int
		density    float64
	}
	trackEnd := float64(n) * hopSec
	var phrs []phr
	for _, r := range merged {
		if r.fe-r.fs < minF {
			continue
		}
		peaks := mzSyllablePeaks(mid, r.fs, r.fe, hopSec)
		durSec := float64(r.fe-r.fs) * hopSec
		rate := 0.0
		if durSec > 0 {
			rate = float64(len(peaks)) / durSec
		}
		phrs = append(phrs, phr{start: r.fs, end: r.fe, rate: rate, peaks: peaks})
	}
	var allPeakTimes []float64
	for i := range phrs {
		p := &phrs[i]
		startSec := float64(p.start) * hopSec
		endSec := float64(p.end) * hopSec
		var meanLik float64
		for f := p.start; f < p.end; f++ {
			meanLik += lS[f]
		}
		meanLik /= float64(p.end - p.start)
		// evidence count: presence / harmonicity / hpss / modulation
		ev := 0
		var pMean, hMean, hdMean, modMean, pMeanShare float64
		for f := p.start; f < p.end; f++ {
			pMean += presence[f]
			pMeanShare += presShare[f]
			hMean += at(harmArr, f)
			hdMean += at(hdArr, f)
			modMean += mod[f]
		}
		c := float64(p.end - p.start)
		pMean /= c
		pMeanShare /= c
		hMean /= c
		hdMean /= c
		modMean /= c
		if pMeanShare >= 0.08 {
			ev++
		}
		if hMean >= 0.4 {
			ev++
		}
		if hdMean >= 0.4 {
			ev++
		}
		if modMean >= 0.2 {
			ev++
		}
		// Confidence requires the modulation evidence: a sustained
		// tonal bed shares every other cue with a voice, so the
		// vocal-flow claim scales with the MEASURED syllabic
		// modulation depth (pads score near zero here and are
		// honestly rejected).
		modFactor := clampUnit(modMean / 0.3)
		// Syllable rate factor: a monotonic attack ramp produces one
		// "peak" and is not vocal flow; real syllabic delivery sustains
		// multiple events per second.
		rateFactor := clampUnit(p.rate / 2.5)
		conf := clampUnit((0.75*meanLik + 0.25*float64(ev)/4) * modFactor * rateFactor)
		// peak strength sum for the density dimension of transitions
		var strSum float64
		for _, pk := range p.peaks {
			strSum += mid[pk]
			allPeakTimes = append(allPeakTimes, float64(pk)*hopSec)
		}
		durSec := float64(p.end-p.start) * hopSec
		dens := 0.0
		if durSec > 0 {
			dens = strSum / durSec
		}
		p.conf = conf
		p.density = dens
		pause := trackEnd - endSec
		if i+1 < len(phrs) {
			pause = float64(phrs[i+1].start)*hopSec - endSec
		}
		if pause < 0 {
			pause = 0
		}
		vf.Phrases = append(vf.Phrases, VocalPhrase{
			Start:        startSec,
			End:          endSec,
			SyllableRate: sanitizeFloat64(p.rate, 0, 50),
			PauseAfter:   sanitizeFloat64(pause, 0, 1e9),
			AccentScore:  0, // no beat grid in this signature (see header)
			Syncopation:  0, // no beat grid in this signature (see header)
			Confidence:   conf,
		})
	}

	// ----- transitions between adjacent phrases (rate ratio first, then
	// strength density — the dominant dimension by |ratio-1| wins)
	for i := 1; i < len(phrs); i++ {
		a, b := phrs[i-1], phrs[i]
		if a.rate < 0.5 {
			continue
		}
		rr := b.rate / a.rate
		dr := 1.0
		if a.density > 1e-9 {
			dr = b.density / a.density
		}
		kind := ""
		mag := 0.0
		if math.Abs(rr-1) >= math.Abs(dr-1) {
			switch {
			case rr > 1.3:
				kind, mag = "slow-to-fast", rr-1
			case rr < 0.77:
				kind, mag = "fast-to-slow", 1-rr
			}
		} else {
			switch {
			case dr > 1.3:
				kind, mag = "denser", dr-1
			case dr < 0.77:
				kind, mag = "sparser", 1-dr
			}
		}
		if kind == "" {
			continue
		}
		vf.Transitions = append(vf.Transitions, FlowTransition{
			At:         float64(b.start) * hopSec,
			Kind:       kind,
			Magnitude:  sanitizeFloat64(mag, 0, 1e9),
			Confidence: clampUnit(math.Min(a.conf, b.conf)),
		})
	}

	// ----- syllable density on the hop grid (events/s, 0..12)
	vf.SyllableDensity = make([]float32, n)
	for _, pt := range allPeakTimes {
		lo := int((pt - 0.5) / hopSec)
		hi := int((pt + 0.5) / hopSec)
		if lo < 0 {
			lo = 0
		}
		if hi > n-1 {
			hi = n - 1
		}
		for f := lo; f <= hi; f++ {
			v := float64(vf.SyllableDensity[f]) + 1
			vf.SyllableDensity[f] = sanitizeFloat32(math.Min(v, 12), 0, 12)
		}
	}

	// overall confidence: mean phrase confidence (0 when none)
	if len(vf.Phrases) > 0 {
		var s float64
		for _, p := range vf.Phrases {
			s += p.Confidence
		}
		vf.OverallConfidence = clampUnit(s / float64(len(vf.Phrases)))
	}
	return vf
}

// mzSyllablePeaks finds syllable-like peaks of the modulation envelope
// inside [fs, fe): local maxima exceeding 1.3× the local mean (±0.5 s,
// clamped to the phrase) with ≥ 80 ms separation.
func mzSyllablePeaks(env []float64, fs, fe int, hopSec float64) []int {
	var peaks []int
	win := int(0.5/hopSec + 0.5)
	if win < 4 {
		win = 4
	}
	minSep := int(0.08/hopSec + 0.5)
	if minSep < 1 {
		minSep = 1
	}
	last := -1000000
	for f := fs; f < fe && f < len(env); f++ {
		v := env[f]
		if v <= 0 {
			continue
		}
		if f > fs && env[f-1] >= v {
			continue
		}
		if f+1 < fe && env[f+1] > v {
			continue
		}
		lo, hi := f-win, f+win
		if lo < fs {
			lo = fs
		}
		if hi > fe-1 {
			hi = fe - 1
		}
		var s float64
		c := 0
		for j := lo; j <= hi; j++ {
			s += env[j]
			c++
		}
		if c == 0 {
			continue
		}
		if v <= 1.22*s/float64(c) {
			continue
		}
		if f-last < minSep {
			continue
		}
		peaks = append(peaks, f)
		last = f
	}
	return peaks
}
