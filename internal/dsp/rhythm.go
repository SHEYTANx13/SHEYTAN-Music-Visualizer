package dsp

// rhythm.go — musical rhythm layer (v3 contract, see analysis_types.go).
//
// DetectRhythm is a pure function over already-measured series (legacy
// frames + the nine-band timeline): multi-band transient detection,
// tempo candidates (autocorrelation + comb filtering), beat grid with
// phase search, meter/downbeat estimation, syncopation, accent
// pattern, local tempo segments and rhythmic motifs.
//
// Determinism: no wall clock, no global randomness, no map iteration
// in output assembly (grouping maps are always drained through sorted
// key slices). Identical inputs produce DeepEqual-identical outputs.
//
// Honest uncertainty:
//   - TempoConfidence < 0.35 → no beat grid: Beats nil, BeatPhase all
//     -1, TempoReliable false (same gate as the legacy Result).
//   - best meter confidence < 0.3 → no downbeats; Meters stay
//     candidates only.
//   - silence (Frame.RMS < 0.002 and ≈0 band energy) never yields
//     transients or beats.
//
// Time contract: every emitted time is frame·hopSec (window start).

import (
	"math"
	"sort"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// Shared musical-layer helpers
// ---------------------------------------------------------------------------

// mzPercentile returns the q-quantile (0..1) of vals. Deterministic:
// sorts a copy; empty input → 0.
func mzPercentile(vals []float64, q float64) float64 {
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

// mzBandFluxNorms returns a per-band flux scale (95th percentile over
// active frames) so flux values from different bands and different
// recordings are comparable. Bands with no measurable flux get 0.
func mzBandFluxNorms(bands *BandTimeline, n int, active []bool) []float64 {
	norms := make([]float64, BandCount)
	if bands == nil {
		return norms
	}
	for b := 0; b < BandCount; b++ {
		var vals []float64
		fl := bands.Flux[b]
		for i := 0; i < n && i < len(fl); i++ {
			if active != nil && !active[i] {
				continue
			}
			if v := float64(fl[i]); v > 0 {
				vals = append(vals, v)
			}
		}
		norms[b] = mzPercentile(vals, 0.95)
	}
	return norms
}

// mzNeg1Series returns a float32 series filled with -1 (the "no usable
// value" marker for hop-grid series such as BeatPhase).
func mzNeg1Series(n int) []float32 {
	s := make([]float32, n)
	for i := range s {
		s[i] = -1
	}
	return s
}

// ---------------------------------------------------------------------------
// Multi-band transient detection
// ---------------------------------------------------------------------------

type mzOnset struct {
	frame int
	band  int
	flux  float64 // normalized band flux at the onset frame
}

// mzBandOnsets picks per-band onset frames: normalized flux crossing an
// adaptive threshold (1.5× local mean over ±20 frames, excluding ±3
// around the candidate, plus a small global floor of 0.05) while being
// a local maximum within ±3 frames. Only active (non-silent) frames.
func mzBandOnsets(flux []float32, norm float64, active []bool, n int, band int) []mzOnset {
	out := make([]mzOnset, 0, 8)
	if norm <= 0 || len(flux) == 0 {
		return out
	}
	const ctx, guard = 20, 3
	at := func(i int) float64 { return float64(flux[i]) / norm }
	for f := 0; f < n && f < len(flux); f++ {
		if !active[f] {
			continue
		}
		v := at(f)
		if v <= 0 {
			continue
		}
		lo, hi := f-ctx, f+ctx
		if lo < 0 {
			lo = 0
		}
		if hi > n-1 {
			hi = n - 1
		}
		var sum float64
		cnt := 0
		for j := lo; j <= hi; j++ {
			if !active[j] || (j >= f-guard && j <= f+guard) {
				continue
			}
			sum += at(j)
			cnt++
		}
		if cnt == 0 {
			continue
		}
		thr := 1.5*(sum/float64(cnt)) + 0.05
		if v <= thr {
			continue
		}
		isMax := true
		for k := 1; k <= guard && isMax; k++ {
			if f-k >= 0 && active[f-k] && at(f-k) >= v {
				isMax = false
			}
			if f+k <= n-1 && active[f+k] && at(f+k) > v {
				isMax = false
			}
		}
		if isMax {
			out = append(out, mzOnset{frame: f, band: band, flux: v})
		}
	}
	return out
}

// mzDetectTransients merges per-band onsets into transient events.
// Onsets within 3 frames (~32 ms) of each other join one event anchored
// at its strongest frame. Band is the dominant contributing band, or -1
// when ≥ 4 bands contribute ≥ 20% of the strongest band's flux
// (broadband). Strength is calibrated against the event-flux 95th
// percentile; Percussive measures flux concentration (sharp attack vs
// local smear, 0..1). Events closer than 50 ms keep only the stronger.
func mzDetectTransients(bands *BandTimeline, norms []float64, active []bool, n int, hopSec float64, frames []Frame) []TransientEvent {
	var onsets []mzOnset
	for b := 0; b < BandCount; b++ {
		onsets = append(onsets, mzBandOnsets(bands.Flux[b], norms[b], active, n, b)...)
	}
	// Absolute attack gate: a transient is a rise in measured energy
	// above the RECENT TROUGH (min RMS over the previous ~10 frames),
	// not merely over the previous frame — consecutive identical hits
	// (a kick train) never rise 2% frame-over-frame, yet each is a real
	// transient; a steady tone's window-slide variation never rises 15%
	// above its local minimum.
	rmsRise := func(f int) bool {
		if f <= 0 || f >= len(frames) {
			return false
		}
		lo := f - 10
		if lo < 0 {
			lo = 0
		}
		trough := frames[f].RMS
		for j := lo; j < f; j++ {
			if frames[j].RMS < trough {
				trough = frames[j].RMS
			}
		}
		if trough < 0.004 {
			return frames[f].RMS > 0.01 // onset from silence
		}
		return frames[f].RMS >= trough*1.15
	}
	filtered := onsets[:0]
	for _, o := range onsets {
		if rmsRise(o.frame) {
			filtered = append(filtered, o)
		}
	}
	onsets = filtered
	if len(onsets) == 0 {
		return nil
	}
	sort.SliceStable(onsets, func(i, j int) bool {
		if onsets[i].frame != onsets[j].frame {
			return onsets[i].frame < onsets[j].frame
		}
		return onsets[i].band < onsets[j].band
	})
	type grp struct {
		peak    int
		peakFl  float64
		last    int
		total   float64
		bandSum [BandCount]float64
	}
	groups := make([]grp, 0, 16)
	for _, o := range onsets {
		if len(groups) > 0 && o.frame-groups[len(groups)-1].last <= 3 {
			g := &groups[len(groups)-1]
			g.total += o.flux
			g.bandSum[o.band] += o.flux
			g.last = o.frame
			if o.flux > g.peakFl {
				g.peak, g.peakFl = o.frame, o.flux
			}
		} else {
			groups = append(groups, grp{peak: o.frame, peakFl: o.flux, last: o.frame, total: o.flux})
			groups[len(groups)-1].bandSum[o.band] += o.flux
		}
	}
	totalAt := func(f int) float64 {
		s := 0.0
		for b := 0; b < BandCount; b++ {
			if norms[b] > 0 && f < len(bands.Flux[b]) {
				s += float64(bands.Flux[b][f]) / norms[b]
			}
		}
		return s
	}
	fluxes := make([]float64, len(groups))
	for i := range groups {
		fluxes[i] = groups[i].total
	}
	p95 := mzPercentile(fluxes, 0.95)
	rises := make([]float64, len(groups))
	if len(frames) > 0 {
		for i := range groups {
			pk := groups[i].peak
			if pk > 0 && pk < len(frames) {
				rises[i] = frames[pk].RMS - frames[pk-1].RMS
			}
		}
	}
	riseP95 := mzPercentile(rises, 0.95)
	events := make([]TransientEvent, 0, len(groups))
	for i := range groups {
		g := &groups[i]
		// percussive share: peak flux vs local smear (±6 frames, no ±1)
		var ctx float64
		ctxN := 0
		for d := -6; d <= 6; d++ {
			if d == 0 || d == -1 || d == 1 {
				continue
			}
			j := g.peak + d
			if j >= 0 && j < n {
				ctx += totalAt(j)
				ctxN++
			}
		}
		smear := 0.0
		if ctxN > 0 {
			smear = ctx / float64(ctxN)
		}
		peakT := totalAt(g.peak)
		perc := clampUnit((peakT - smear) / (peakT + 1e-9))
		band := -1
		maxSum, maxBand := 0.0, 0
		for b := 0; b < BandCount; b++ {
			if g.bandSum[b] > maxSum {
				maxSum, maxBand = g.bandSum[b], b
			}
		}
		if maxSum > 0 {
			band = maxBand
			hits := 0
			for b := 0; b < BandCount; b++ {
				if g.bandSum[b] >= 0.2*maxSum {
					hits++
				}
			}
			if hits >= 4 {
				band = -1 // broadband
			}
		}
		// Strength from the measured RMS RISE at the onset: an
		// amplitude-proportional absolute measure. Relative flux
		// saturates at 1 for any onset from near-silence (every attack
		// would read "maximum strength"), which would erase accent
		// patterns and instrument dynamics; the RMS rise keeps them.
		st := 1.0
		if p95 > 0 {
			st = clampUnit(g.total / p95)
		}
		if len(frames) > 0 {
			rise := 0.0
			if g.peak > 0 && g.peak < len(frames) {
				rise = frames[g.peak].RMS - frames[g.peak-1].RMS
			}
			if rise > 0 {
				st = clampUnit(rise / riseP95)
			}
		}
		events = append(events, TransientEvent{
			Time:       float64(g.peak) * hopSec,
			Strength:   st,
			Band:       band,
			Percussive: perc,
		})
	}
	// enforce ≥ 50 ms separation, keeping the stronger event
	out := make([]TransientEvent, 0, len(events))
	for _, e := range events {
		if len(out) > 0 && e.Time-out[len(out)-1].Time < 0.05 {
			if e.Strength > out[len(out)-1].Strength {
				out[len(out)-1] = e
			}
			continue
		}
		out = append(out, e)
	}
	return out
}

// ---------------------------------------------------------------------------
// Tempo scan (autocorrelation + comb filter)
// ---------------------------------------------------------------------------

type mzTempoResult struct {
	bpm       float64
	conf      float64
	cands     []TempoCandidate
	ambiguous bool
}

// mzTempoScan autocorrelates the onset envelope (0..1) over 60..190 BPM
// in 0.5 BPM steps, adding comb-filter support at lag, 2·lag, 3·lag
// with decaying weights 1, 0.5, 0.25. A mild log-Gaussian prior centred
// on 120 BPM (standard tempo-octave disambiguation heuristic) selects
// the primary reading; the raw periodicity of the primary lag is the
// reported confidence. Candidate confidences are shares normalized to
// sum ≤ 1 (relative readings); TempoConfidence stays absolute, so
// Rhythm.TempoConfidence may exceed Candidates[0].Confidence.
func mzTempoScan(en []float64, hopSec float64) mzTempoResult {
	var res mzTempoResult
	n := len(en)
	if n < 32 || hopSec <= 0 {
		return res
	}
	var sq float64
	for _, v := range en {
		sq += v * v
	}
	meanSq := sq / float64(n)
	if meanSq <= 1e-12 {
		return res
	}
	acr := func(lag int) float64 {
		if lag < 2 || lag >= n-2 {
			return 0
		}
		var acc float64
		for i := 0; i+lag < n; i++ {
			acc += en[i] * en[i+lag]
		}
		return acc / float64(n-lag) / meanSq
	}
	type raw struct {
		bpm, comb, score float64
	}
	var all []raw
	for bpm := 60.0; bpm <= 190.0; bpm += 0.5 {
		lag := int(math.Round(60 / bpm / hopSec))
		if lag < 4 || lag >= n-2 {
			continue
		}
		comb := acr(lag)
		wsum := 1.0
		for k, w := range [...]float64{0.5, 0.25} {
			l2 := lag * (k + 2)
			if l2 < n-2 {
				comb += w * acr(l2)
				wsum += w
			}
		}
		comb /= wsum
		prior := math.Exp(-0.5 * math.Pow(math.Log2(bpm/120.0)/1.2, 2))
		all = append(all, raw{bpm: bpm, comb: comb, score: comb * prior})
	}
	if len(all) == 0 {
		return res
	}
	best := 0
	for i := range all {
		if all[i].score > all[best].score+1e-12 {
			best = i
		}
	}
	res.bpm = all[best].bpm
	res.conf = clampUnit(all[best].comb)
	// candidate peaks: local maxima of the score curve (earliest frame
	// of a plateau wins), ranked by score, top 3 with ≥ 6 BPM separation
	var peaks []int
	for i := range all {
		leftOK := i == 0 || all[i].score >= all[i-1].score
		rightOK := i == len(all)-1 || all[i].score > all[i+1].score
		if leftOK && rightOK {
			peaks = append(peaks, i)
		}
	}
	sort.SliceStable(peaks, func(a, b int) bool { return all[peaks[a]].score > all[peaks[b]].score })
	picked := make([]int, 0, 3)
	for _, pi := range peaks {
		ok := true
		for _, pj := range picked {
			if math.Abs(all[pi].bpm-all[pj].bpm) < 6 {
				ok = false
				break
			}
		}
		if ok {
			picked = append(picked, pi)
			if len(picked) == 3 {
				break
			}
		}
	}
	hasPrimary := false
	for _, pi := range picked {
		if pi == best {
			hasPrimary = true
			break
		}
	}
	if !hasPrimary {
		picked = append([]int{best}, picked...)
		if len(picked) > 3 {
			picked = picked[:3]
		}
	}
	var sum float64
	for _, pi := range picked {
		sum += math.Max(0, all[pi].comb)
	}
	if sum <= 1e-9 {
		return res
	}
	primaryShare := math.Max(0, all[best].comb) / sum
	for _, pi := range picked {
		c := TempoCandidate{BPM: all[pi].bpm, Confidence: math.Max(0, all[pi].comb) / sum}
		if pi == best {
			c.Relation = "primary"
		} else {
			switch {
			case math.Abs(c.BPM-res.bpm/2) <= 3:
				c.Relation = "half"
			case math.Abs(c.BPM-res.bpm*2) <= 3:
				c.Relation = "double"
			}
			if (c.Relation == "half" || c.Relation == "double") &&
				c.Confidence >= 0.6*primaryShare {
				res.ambiguous = true
			}
		}
		res.cands = append(res.cands, c)
	}
	return res
}

// ---------------------------------------------------------------------------
// Local tempo segments
// ---------------------------------------------------------------------------

// mzTempoSegments runs the tempo scan over 10 s windows (5 s step);
// windows whose local BPM deviates > 8% from the global reading with
// window confidence ≥ 0.4 become segments. Adjacent windows with
// near-equal tempo merge.
func mzTempoSegments(en []float64, hopSec float64, globalBPM float64) []TempoSegment {
	win := int(10/hopSec + 0.5)
	step := int(5/hopSec + 0.5)
	n := len(en)
	if win <= 0 || step <= 0 || n < win || globalBPM <= 0 {
		return nil
	}
	var segs []TempoSegment
	for start := 0; start+win <= n; start += step {
		tr := mzTempoScan(en[start:start+win], hopSec)
		if tr.conf >= 0.4 && tr.bpm > 0 && math.Abs(tr.bpm-globalBPM)/globalBPM > 0.08 {
			segs = append(segs, TempoSegment{
				Start:      float64(start) * hopSec,
				End:        float64(start+win) * hopSec,
				BPM:        sanitizeFloat64(tr.bpm, 1, 400),
				Confidence: tr.conf,
			})
		}
	}
	out := make([]TempoSegment, 0, len(segs))
	for _, s := range segs {
		if len(out) > 0 && s.Start <= out[len(out)-1].End+1e-9 && math.Abs(s.BPM-out[len(out)-1].BPM) <= 4 {
			out[len(out)-1].End = s.End
			continue
		}
		out = append(out, s)
	}
	return out
}

// sanitizeFloat64 returns 0 for NaN/Inf, otherwise the value clamped to
// [min, max] when the range is valid.
func sanitizeFloat64(v float64, min, max float64) float64 {
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

// ---------------------------------------------------------------------------
// Meter, phrases, motifs
// ---------------------------------------------------------------------------

// mzMeterScan scores meter hypotheses {4/4, 3/4, 6/8} by accent-weighted
// beat strength at candidate downbeat positions vs the remaining beats.
// Confidence maps the accent ratio (downbeat mean / other mean) onto
// 0..1; uniform accents (ratio 1) score 0 — honest "no downbeat
// evidence". 6/8 is scored on position-0 accents only (its beat-pair
// secondary accent is a documented limitation).
func mzMeterScan(beats []BeatEvent, transients []TransientEvent) (meters []MeterCandidate, best MeterCandidate, bestOffset int, ok bool) {
	type mh struct {
		name  string
		beats int
	}
	hyps := []mh{{"4/4", 4}, {"3/4", 3}, {"6/8", 6}}
	type mres struct {
		c      MeterCandidate
		offset int
	}
	// Accumulate the measured transient strength around each beat
	// position (±0.15 beat): the accent evidence is the transient
	// energy itself, not the envelope sample at the beat grid point.
	beat := 0.0
	if len(beats) >= 2 {
		beat = beats[1].Time - beats[0].Time
	}
	maxIdx := 0
	for _, b := range beats {
		if b.Index > maxIdx {
			maxIdx = b.Index
		}
	}
	beatStrength := make([]float64, maxIdx+1)
	if beat > 0 && len(transients) > 0 {
		for _, b := range beats {
			s := 0.0
			for _, tr := range transients {
				if math.Abs(tr.Time-b.Time) <= 0.15*beat {
					s += tr.Strength
				}
			}
			beatStrength[b.Index] = s
		}
	}
	var ms []mres
	for _, m := range hyps {
		bestRatio, bestOff := 0.0, 0
		for o := 0; o < m.beats; o++ {
			var dSum, oSum float64
			dN, oN := 0, 0
			for _, b := range beats {
				pos := ((b.Index-o)%m.beats + m.beats) % m.beats
				if pos == 0 {
					dSum += beatStrength[b.Index]
					dN++
				} else {
					oSum += beatStrength[b.Index]
					oN++
				}
			}
			if dN == 0 || oN == 0 {
				continue
			}
			ratio := (dSum / float64(dN)) / (oSum/float64(oN) + 1e-9)
			if ratio > bestRatio {
				bestRatio, bestOff = ratio, o
			}
		}
		if bestRatio > 0 {
			ms = append(ms, mres{
				c:      MeterCandidate{Name: m.name, Beats: m.beats, Confidence: clampUnit((bestRatio - 1) * 2)},
				offset: bestOff,
			})
		}
	}
	// stable sort keeps the fixed hypothesis order on ties (4/4 first)
	sort.SliceStable(ms, func(i, j int) bool { return ms[i].c.Confidence > ms[j].c.Confidence })
	for i := range ms {
		meters = append(meters, ms[i].c)
	}
	if len(ms) > 0 {
		best, bestOffset, ok = ms[0].c, ms[0].offset, true
	}
	return meters, best, bestOffset, ok
}

// mzPhraseBoundaries groups bars into 4-bar phrases and reports bar
// times where the concatenated accent profile changes (cosine novelty
// > 0.5, local maximum). Sparse by construction, capped at 32.
func mzPhraseBoundaries(beats []BeatEvent, meter, offset int, firstBeatSec, periodSec float64) []float64 {
	if meter <= 0 || len(beats) < 2 || periodSec <= 0 {
		return nil
	}
	maxIdx := beats[len(beats)-1].Index
	str := make([]float64, maxIdx+1)
	have := make([]bool, maxIdx+1)
	for _, b := range beats {
		if b.Index >= 0 && b.Index <= maxIdx {
			str[b.Index] = b.Strength
			have[b.Index] = true
		}
	}
	bars := (maxIdx + 1 - offset) / meter
	if bars < 9 {
		return nil
	}
	prof := make([]float64, 0, bars*meter)
	barStart := make([]float64, bars)
	for b := 0; b < bars; b++ {
		barStart[b] = firstBeatSec + float64(offset+b*meter)*periodSec
		for k := 0; k < meter; k++ {
			idx := offset + b*meter + k
			if idx >= 0 && idx <= maxIdx && have[idx] {
				prof = append(prof, str[idx])
			} else {
				prof = append(prof, 0)
			}
		}
	}
	cosDist := func(a, b []float64) float64 {
		var d, na, nb float64
		for i := range a {
			d += a[i] * b[i]
			na += a[i] * a[i]
			nb += b[i] * b[i]
		}
		if na <= 1e-12 || nb <= 1e-12 {
			return 0
		}
		return 1 - d/math.Sqrt(na*nb)
	}
	dists := make([]float64, bars)
	for i := 4; i+4 <= bars; i++ {
		dists[i] = cosDist(prof[(i-4)*meter:i*meter], prof[i*meter:(i+4)*meter])
	}
	var out []float64
	for i := 4; i+4 <= bars; i++ {
		d := dists[i]
		if d <= 0.5 {
			continue
		}
		if i-1 >= 4 && dists[i-1] > d {
			continue
		}
		if i+1 < bars && dists[i+1] > d {
			continue
		}
		out = append(out, barStart[i])
		if len(out) >= 32 {
			break
		}
	}
	return out
}

// mzMotifs hashes per-bar transient inter-onset intervals quantized to a
// 16-step bar grid; bar patterns occurring ≥ 2 times become motifs.
// Output sorted by confidence (desc) then start (asc), capped at 8.
func mzMotifs(transients []TransientEvent, barStarts []float64, barDur, trackEnd float64) []RhythmicMotif {
	if len(barStarts) < 2 || barDur <= 0 {
		return nil
	}
	step := barDur / 16
	if step <= 0 {
		return nil
	}
	type occ struct {
		times []float64
		strS  float64
		strN  int
	}
	occMap := make(map[string]*occ)
	for _, bs := range barStarts {
		be := bs + barDur
		if trackEnd > 0 && be > trackEnd {
			be = trackEnd
		}
		var bar []TransientEvent
		for _, tr := range transients {
			if tr.Time >= bs && tr.Time < be {
				bar = append(bar, tr)
			}
		}
		if len(bar) < 3 {
			continue
		}
		parts := make([]string, 0, len(bar)-1)
		var sSum float64
		for i := 1; i < len(bar); i++ {
			ioi := bar[i].Time - bar[i-1].Time
			q := int(math.Round(ioi / step))
			if q < 1 {
				q = 1
			}
			if q > 16 {
				q = 16
			}
			parts = append(parts, strconv.Itoa(q))
			sSum += bar[i].Strength
		}
		key := strings.Join(parts, ",")
		o := occMap[key]
		if o == nil {
			o = &occ{}
			occMap[key] = o
		}
		o.times = append(o.times, bs)
		o.strS += sSum
		o.strN += len(bar) - 1
	}
	keys := make([]string, 0, len(occMap))
	for k := range occMap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var motifs []RhythmicMotif
	for _, k := range keys {
		o := occMap[k]
		if len(o.times) < 2 {
			continue
		}
		times := make([]float64, len(o.times))
		copy(times, o.times)
		sort.Float64s(times)
		meanStr := 0.0
		if o.strN > 0 {
			meanStr = o.strS / float64(o.strN)
		}
		motifs = append(motifs, RhythmicMotif{
			Start:       times[0],
			End:         times[0] + barDur,
			Occurrences: times,
			Confidence:  clampUnit(0.2 + 0.15*float64(len(times)-1) + 0.45*meanStr),
		})
	}
	sort.SliceStable(motifs, func(i, j int) bool {
		if motifs[i].Confidence != motifs[j].Confidence {
			return motifs[i].Confidence > motifs[j].Confidence
		}
		return motifs[i].Start < motifs[j].Start
	})
	if len(motifs) > 8 {
		motifs = motifs[:8]
	}
	return motifs
}

// ---------------------------------------------------------------------------
// DetectRhythm
// ---------------------------------------------------------------------------

// DetectRhythm implements the musical rhythm layer over measured frames
// and the nine-band timeline. See the file header for the honesty and
// time contracts.
func DetectRhythm(frames []Frame, bands *BandTimeline, hopSec float64) *Rhythm {
	r := &Rhythm{}
	n := len(frames)
	if bands == nil || hopSec <= 0 || n == 0 {
		return r
	}
	phaseN := n
	for b := 0; b < BandCount; b++ {
		if len(bands.Flux[b]) < n {
			n = len(bands.Flux[b])
		}
		if len(bands.Energy[b]) < n {
			n = len(bands.Energy[b])
		}
	}
	if n == 0 {
		return r
	}
	// Silence-aware activity gate: skip frames with no measurable energy.
	active := make([]bool, n)
	for i := 0; i < n; i++ {
		e := 0.0
		for b := 0; b < BandCount; b++ {
			e += float64(bands.Energy[b][i])
		}
		active[i] = frames[i].RMS >= 0.002 || e >= 1e-4
	}
	norms := mzBandFluxNorms(bands, n, active)

	r.Transients = mzDetectTransients(bands, norms, active, n, hopSec, frames)
	r.Density = float64(len(r.Transients)) / (float64(n) * hopSec)

	// Onset-strength envelope: sum over bands of normalized flux,
	// percussive-weighted (transient frames carry full weight, background
	// frames half — a documented proxy because HPSS is not in scope here).
	env := make([]float64, n)
	for f := 0; f < n; f++ {
		s := 0.0
		for b := 0; b < BandCount; b++ {
			if norms[b] > 0 && f < len(bands.Flux[b]) {
				s += float64(bands.Flux[b][f]) / norms[b]
			}
		}
		env[f] = s
	}
	pos := make([]float64, 0, n)
	for _, v := range env {
		if v > 0 {
			pos = append(pos, v)
		}
	}
	if eP95 := mzPercentile(pos, 0.95); eP95 > 0 {
		for f := range env {
			env[f] *= 0.5 + 0.5*clampUnit(env[f]/eP95)
		}
	}
	emax := 0.0
	for _, v := range env {
		if v > emax {
			emax = v
		}
	}
	r.BeatPhase = mzNeg1Series(phaseN)
	if emax <= 0 {
		return r
	}
	// Onset-evidence gate: normalized flux can amplify a steady tone's
	// window-slide variation into a busy envelope; the number of
	// MEASURED transients is the honest evidence of attack structure.
	// Fewer than four distinct attacks over a multi-second track means
	// any autocorrelation peak would be fitted to noise.
	if len(r.Transients) < 4 && float64(n)*hopSec >= 2 {
		r.TempoConfidence = clampUnit(float64(len(r.Transients)) / 8)
		r.TempoReliable = false
		return r
	}
	en := make([]float64, n)
	for f := range env {
		en[f] = env[f] / emax
	}
	// rhythmic-variation gate: a flat envelope carries no measurable pulse
	var mSum, mSq float64
	for _, v := range en {
		mSum += v
		mSq += v * v
	}
	mean := mSum / float64(n)
	std := math.Sqrt(math.Max(0, mSq/float64(n)-mean*mean))
	if std < 0.02 {
		return r // steady content → honest "no tempo"
	}

	tr := mzTempoScan(en, hopSec)
	r.BPM = sanitizeFloat64(tr.bpm, 0, 400)
	r.TempoConfidence = tr.conf
	r.TempoReliable = tr.conf >= 0.35
	r.Candidates = tr.cands
	r.HalfDoubleAmbiguous = tr.ambiguous
	if tr.conf < 0.35 || tr.bpm <= 0 {
		return r // no usable beat grid: Beats stay nil, phases -1
	}

	// ----- beat grid: best phase by cross-correlation with an impulse train
	pInt := int(math.Round(60.0 / tr.bpm / hopSec))
	if pInt < 4 || pInt >= n {
		return r
	}
	maxWin := func(f int) float64 {
		best := 0.0
		for d := -2; d <= 2; d++ {
			j := f + d
			if j >= 0 && j < n && en[j] > best {
				best = en[j]
			}
		}
		return best
	}
	bestP, bestScore := 0, -1.0
	for p := 0; p < pInt; p++ {
		var s float64
		for f := p; f < n; f += pInt {
			s += maxWin(f)
		}
		if s > bestScore+1e-12 {
			bestScore, bestP = s, p
		}
	}
	// Emit beats only where there is musical activity: a beat needs local
	// envelope evidence or a nearby transient; after ≥ 3 inactive beats the
	// grid run pauses and resumes automatically at the next active beat.
	var beats []BeatEvent
	trIdx := 0
	lastActive := -1000000
	lastBeatFrame := bestP
	for k := 0; ; k++ {
		f := bestP + k*pInt
		if f >= n {
			break
		}
		lastBeatFrame = f
		t := float64(f) * hopSec
		st := maxWin(f)
		for trIdx < len(r.Transients) && r.Transients[trIdx].Time < t-0.15 {
			trIdx++
		}
		near := trIdx < len(r.Transients) && r.Transients[trIdx].Time <= t+0.15
		isActive := st >= 0.05 || near
		if !isActive {
			if !(lastActive >= 0 && k-lastActive <= 2) {
				continue // silent region: no fabricated beats
			}
		} else {
			lastActive = k
		}
		beats = append(beats, BeatEvent{Time: t, Strength: clampUnit(st), Index: k})
	}
	r.Beats = beats

	// phase timeline (hop grid): phase within the current beat 0..1
	t0 := float64(bestP) * hopSec
	periodSec := float64(pInt) * hopSec
	lastT := float64(lastBeatFrame) * hopSec
	for f := 0; f < phaseN; f++ {
		t := float64(f) * hopSec
		if t >= t0-periodSec && t <= lastT+periodSec {
			ph := math.Mod((t-t0)/periodSec, 1)
			if ph < 0 {
				ph += 1
			}
			r.BeatPhase[f] = sanitizeFloat32(ph, 0, 1)
		}
	}

	// ----- meter + downbeats
	bestMBeats := 0
	if len(beats) >= 4 {
		meters, bestM, bestOff, ok := mzMeterScan(beats, r.Transients)
		r.Meters = meters
		if ok && bestM.Confidence >= 0.3 {
			bestMBeats = bestM.Beats
			r.DownbeatBeatIndex = bestOff
			m := bestM.Beats
			for _, b := range beats {
				pos := ((b.Index-bestOff)%m + m) % m
				bar := (b.Index - bestOff) / m
				if bar < 0 {
					bar = 0
				}
				b.Bar = bar
				if pos == 0 {
					r.Downbeats = append(r.Downbeats, BeatEvent{Time: b.Time, Strength: b.Strength, Index: b.Index, Bar: bar})
					r.BarTimes = append(r.BarTimes, b.Time)
				}
			}
			// accent pattern: normalized per-beat-position energy profile
			acc := make([]float64, m)
			accN := make([]int, m)
			for _, b := range beats {
				p := ((b.Index-bestOff)%m + m) % m
				acc[p] += b.Strength
				accN[p]++
			}
			maxA := 0.0
			for i := range acc {
				if accN[i] > 0 {
					acc[i] /= float64(accN[i])
				}
				if acc[i] > maxA {
					maxA = acc[i]
				}
			}
			if maxA > 0 {
				r.AccentPattern = make([]float32, m)
				for i := range acc {
					r.AccentPattern[i] = sanitizeFloat32(acc[i]/maxA, 0, 1)
				}
				r.AccentConfidence = bestM.Confidence
			}
			// phrase boundaries from 4-bar accent-profile novelty
			if len(r.Downbeats) > 0 && periodSec > 0 {
				r.PhraseBoundaries = mzPhraseBoundaries(beats, m, bestOff, t0, periodSec)
			}
		}
	}

	// ----- syncopation: energy-weighted share of transient strength
	// lying outside the ±0.15-beat windows around grid positions
	if len(beats) > 0 {
		var tot, off float64
		for _, tr := range r.Transients {
			u := (tr.Time - t0) / periodSec
			d := math.Abs(u - math.Round(u))
			tot += tr.Strength
			if d > 0.15 {
				off += tr.Strength
			}
		}
		if tot > 0 {
			r.Syncopation = clampUnit(off / tot)
		}
	}

	// ----- motifs: per-bar IOI patterns on a 16-step grid
	var barStarts []float64
	barDur := 0.0
	if len(r.Downbeats) >= 2 {
		for _, d := range r.Downbeats {
			barStarts = append(barStarts, d.Time)
		}
		barDur = periodSec * float64(bestMBeats)
	} else if len(beats) >= 8 {
		// pseudo-bars of 4 beats when meter is uncertain
		for i := 0; i+4 <= len(beats); i += 4 {
			barStarts = append(barStarts, beats[i].Time)
		}
		barDur = 4 * periodSec
	}
	if len(barStarts) >= 2 && barDur > 0 {
		r.Motifs = mzMotifs(r.Transients, barStarts, barDur, float64(phaseN)*hopSec)
	}

	// ----- local tempo segments
	r.TempoSegments = mzTempoSegments(en, hopSec, r.BPM)

	return r
}
