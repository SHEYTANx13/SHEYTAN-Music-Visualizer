package dsp

// harmony.go — musical harmony layer (v3 contract, see analysis_types.go
// Harmony): global tuning, Krumhansl-Schmuckler key estimation,
// chord segmentation with cosine template matching, repeated
// progressions and a per-hop tension series.
//
// Purity: DetectHarmony consumes already-measured chroma (and legacy
// frames only for a silence gate); no randomness, no wall clock, and
// every output assembly drains maps through sorted key slices.
//
// Honest uncertainty:
//   - Key is assigned only when the best KS correlation clears a 0.6
//     confidence floor; below it Key is "" while KeyConfidence and the
//     ranked KeyCandidates are still reported.
//   - Chords are reported only when cosine similarity ≥ 0.72 and the
//     segment is stable (≥ 0.8 s, or ≥ 2 beats in beat-synchronous
//     mode); high-entropy (atonal/noise) segments report root "N"
//     instead of a guessed chord.
//
// Time contract: event times are frame·hopSec (window start).

import (
	"math"
	"sort"
	"strings"
)

var mzPCNames = [12]string{"C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B"}

// Krumhansl-Kessler key profiles (tonic at index 0).
var mzKSMajor = [12]float64{6.35, 2.23, 3.48, 2.33, 4.38, 4.09, 2.52, 5.19, 2.39, 3.66, 2.29, 2.88}
var mzKSMinor = [12]float64{6.33, 2.68, 3.52, 5.38, 2.60, 3.53, 2.54, 4.75, 3.98, 2.69, 3.34, 3.17}

// Chord templates (root at index 0; binary-ish weighted).
var mzChordTemplates = []struct {
	quality string
	t       [12]float64
}{
	{"maj", [12]float64{1, 0, 0, 0, 1, 0, 0, 1, 0, 0, 0, 0}},
	{"min", [12]float64{1, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0, 0}},
	{"7", [12]float64{1, 0, 0, 0, 1, 0, 0, 1, 0, 0, 1, 0}},
	{"m7", [12]float64{1, 0, 0, 1, 0, 0, 0, 1, 0, 0, 1, 0}},
	{"dim", [12]float64{1, 0, 0, 1, 0, 0, 1, 0, 0, 0, 0, 0}},
	{"aug", [12]float64{1, 0, 0, 0, 1, 0, 0, 0, 1, 0, 0, 0}},
}

// mzPearson is the Pearson correlation coefficient (0 when undefined).
func mzPearson(a, b []float64) float64 {
	n := len(a)
	if n == 0 || len(b) != n {
		return 0
	}
	var ma, mb float64
	for i := range a {
		ma += a[i]
		mb += b[i]
	}
	ma /= float64(n)
	mb /= float64(n)
	var num, da, db float64
	for i := range a {
		dx := a[i] - ma
		dy := b[i] - mb
		num += dx * dy
		da += dx * dx
		db += dy * dy
	}
	if da <= 1e-15 || db <= 1e-15 {
		return 0
	}
	return num / math.Sqrt(da*db)
}

// mzNormEntropy is the normalized Shannon entropy of a non-negative
// vector (0..1; 1 = maximally spread / atonal).
func mzNormEntropy(v []float64) float64 {
	var sum float64
	for _, x := range v {
		if x > 0 {
			sum += x
		}
	}
	if sum <= 1e-12 {
		return 0
	}
	h := 0.0
	for _, x := range v {
		if x > 0 {
			p := x / sum
			h -= p * math.Log(p)
		}
	}
	return h / math.Log(float64(len(v)))
}

// mzRotate rotates a 12-bin profile so the tonic sits at pc.
func mzRotate(p [12]float64, pc int) [12]float64 {
	var out [12]float64
	for i := 0; i < 12; i++ {
		out[i] = p[(i+pc)%12]
	}
	return out
}

// ---------------------------------------------------------------------------
// Tuning + key
// ---------------------------------------------------------------------------

// mzTuning estimates the global tuning offset as the chroma-mass
// weighted median of per-frame offsets; confidence falls with the
// weighted spread.
func mzTuning(chroma []ChromaPoint, frames []Frame, useFrames bool) (offsetCents, conf float64) {
	type tw struct {
		off, w float64
	}
	var tws []tw
	var wSum float64
	for i := range chroma {
		if useFrames && i < len(frames) && frames[i].RMS < 0.002 {
			continue
		}
		m := 0.0
		for j := 0; j < 12; j++ {
			m += float64(chroma[i].Chroma[j])
		}
		if m <= 1e-6 {
			continue
		}
		off := float64(chroma[i].TuningOffsetCents)
		if math.IsNaN(off) || math.IsInf(off, 0) {
			continue
		}
		tws = append(tws, tw{off: off, w: m})
		wSum += m
	}
	if wSum <= 0 || len(tws) == 0 {
		return 0, 0
	}
	sort.Slice(tws, func(a, b int) bool { return tws[a].off < tws[b].off })
	half := wSum / 2
	var cum float64
	median := tws[len(tws)-1].off
	for _, x := range tws {
		cum += x.w
		if cum >= half {
			median = x.off
			break
		}
	}
	var mean float64
	for _, x := range tws {
		mean += x.off * x.w
	}
	mean /= wSum
	var vari float64
	for _, x := range tws {
		d := x.off - mean
		vari += x.w * d * d
	}
	std := math.Sqrt(vari / wSum)
	return sanitizeFloat64(median, -50, 50), clampUnit(1 - std/30)
}

// mzKeyScan correlates the time-averaged chroma against the 24 rotated
// KS profiles and returns the ranked candidates with confidences
// spread-normalized onto 0..1.
func mzKeyScan(avg []float64) (best KeyCandidate, cands []KeyCandidate, bestConf float64) {
	if len(avg) != 12 {
		return best, nil, 0
	}
	type kc struct {
		key KeyCandidate
		r   float64
	}
	var all []kc
	rMin := 2.0
	for tonic := 0; tonic < 12; tonic++ {
		pm := mzRotate(mzKSMajor, tonic)
		rm := mzPearson(avg, pm[:])
		rMin = math.Min(rMin, rm)
		all = append(all, kc{key: KeyCandidate{Key: mzPCNames[tonic] + " major"}, r: rm})
		pn := mzRotate(mzKSMinor, tonic)
		rn := mzPearson(avg, pn[:])
		rMin = math.Min(rMin, rn)
		all = append(all, kc{key: KeyCandidate{Key: mzPCNames[tonic] + " minor"}, r: rn})
	}
	// stable sort keeps major-before-minor, ascending-tonic order on ties
	sort.SliceStable(all, func(a, b int) bool { return all[a].r > all[b].r })
	for i := 0; i < len(all) && i < 5; i++ {
		conf := clampUnit((all[i].r - rMin) / (1 - rMin + 1e-9))
		all[i].key.Confidence = conf
		cands = append(cands, all[i].key)
		if i == 0 {
			best = all[i].key
			bestConf = conf
		}
	}
	return best, cands, bestConf
}

// ---------------------------------------------------------------------------
// Chords
// ---------------------------------------------------------------------------

// mzMeanChroma averages chroma frames [s, e).
func mzMeanChroma(chroma []ChromaPoint, s, e int) [12]float64 {
	var out [12]float64
	if s < 0 {
		s = 0
	}
	if e > len(chroma) {
		e = len(chroma)
	}
	n := 0
	for i := s; i < e; i++ {
		n++
		for j := 0; j < 12; j++ {
			out[j] += float64(chroma[i].Chroma[j])
		}
	}
	if n > 1 {
		for j := range out {
			out[j] /= float64(n)
		}
	}
	return out
}

// mzMatchChord matches a mean chroma against all rotated chord
// templates via cosine similarity. Returns root pc (-1 = none), quality
// and the similarity; root -1 for high-entropy (atonal) input.
func mzMatchChromaTemplate(c *[12]float64) (root int, quality string, sim float64) {
	ent := mzNormEntropy(c[:])
	if ent > 0.85 {
		return -1, "", 1 - ent
	}
	var sum float64
	for i := range c {
		sum += c[i] * c[i]
	}
	if sum <= 1e-12 {
		return -1, "", 0
	}
	norm := math.Sqrt(sum)
	bestR, bestQ, bestS := -1, "", 0.0
	for _, tpl := range mzChordTemplates {
		for r := 0; r < 12; r++ {
			var dot, tn float64
			for i := 0; i < 12; i++ {
				t := tpl.t[(i+r)%12]
				dot += c[i] * t
				tn += t * t
			}
			s := dot / (norm * math.Sqrt(tn))
			if s > bestS {
				bestS, bestR, bestQ = s, r, tpl.quality
			}
		}
	}
	if bestS < 0.72 {
		return -1, "", bestS
	}
	return bestR, bestQ, bestS
}

type mzCSeg struct {
	start, end int // frame indices
	root       int // -1 → "N"
	quality    string
	sim        float64
	ent        float64
}

// mzSegmentChroma returns chroma segment boundaries (frame indices).
// Beat-synchronous mode uses beat times as boundaries; otherwise fixed
// 1 s segments are split where the chroma L1 delta shows a hard novelty
// jump (≥ 0.35 — a real chord swap moves several pitch classes).
func mzSegmentChroma(chroma []ChromaPoint, beats []BeatEvent, hopSec float64) []int {
	n := len(chroma)
	bounds := []int{0}
	if len(beats) >= 2 && hopSec > 0 {
		seen := map[int]bool{0: true}
		for _, b := range beats {
			f := int(math.Round(b.Time / hopSec))
			if f > 0 && f < n && !seen[f] {
				seen[f] = true
				bounds = append(bounds, f)
			}
		}
	} else if hopSec > 0 {
		seg := int(1.0/hopSec + 0.5)
		if seg < 2 {
			seg = 2
		}
		for f := seg; f < n; f += seg {
			bounds = append(bounds, f)
		}
		// novelty splitting inside fixed segments
		for si := 0; si+1 < len(bounds); si++ {
			s, e := bounds[si], bounds[si+1]
			for f := s + 1; f < e; f++ {
				d := 0.0
				for j := 0; j < 12; j++ {
					d += math.Abs(float64(chroma[f].Chroma[j]) - float64(chroma[f-1].Chroma[j]))
				}
				if d >= 0.35 {
					bounds = append(bounds, f)
				}
			}
		}
		sort.Ints(bounds)
		// dedupe (sorted, keep first)
		out := bounds[:1]
		for _, b := range bounds[1:] {
			if b != out[len(out)-1] {
				out = append(out, b)
			}
		}
		bounds = out
	}
	if len(bounds) == 0 || bounds[len(bounds)-1] != n {
		bounds = append(bounds, n)
	}
	return bounds
}

// DetectChords segments chroma (beat-synchronously when beats are
// given, else fixed 1 s segments with novelty splitting) and matches
// each stable segment against weighted chord templates. Chords are
// accepted at cosine similarity ≥ 0.72; high-entropy segments report
// root "N" (no tonal center) rather than a guessed chord.
func DetectChords(chroma []ChromaPoint, beats []BeatEvent, hopSec float64) []ChordEvent {
	if len(chroma) == 0 {
		return nil
	}
	bounds := mzSegmentChroma(chroma, beats, hopSec)
	segs := make([]mzCSeg, 0, len(bounds)-1)
	for i := 0; i+1 < len(bounds); i++ {
		s, e := bounds[i], bounds[i+1]
		if e-s <= 0 {
			continue
		}
		mc := mzMeanChroma(chroma, s, e)
		root, qual, sim := mzMatchChromaTemplate(&mc)
		segs = append(segs, mzCSeg{start: s, end: e, root: root, quality: qual, sim: sim, ent: mzNormEntropy(mc[:])})
	}
	// merge adjacent segments with identical chord identity
	merged := make([]mzCSeg, 0, len(segs))
	for _, s := range segs {
		if len(merged) > 0 && merged[len(merged)-1].root == s.root && merged[len(merged)-1].quality == s.quality {
			merged[len(merged)-1].end = s.end
			continue
		}
		merged = append(merged, s)
	}
	// acceptance: real chords need similarity ≥ 0.72 (already enforced by
	// the matcher) and a stable run (≥ 0.8 s, or ≥ 2 beats); "N" runs
	// report measured atonality when equally stable.
	durSec := func(s mzCSeg) float64 { return float64(s.end-s.start) * hopSec }
	beatsIn := func(s mzCSeg) int {
		if len(beats) == 0 {
			return 0
		}
		c := 0
		for _, b := range beats {
			bt := b.Time
			if bt >= float64(s.start)*hopSec && bt < float64(s.end)*hopSec {
				c++
			}
		}
		return c
	}
	out := make([]ChordEvent, 0, len(merged))
	for _, s := range merged {
		if durSec(s) < 0.8 && beatsIn(s) < 2 {
			continue
		}
		ev := ChordEvent{
			Start: float64(s.start) * hopSec,
			End:   float64(s.end) * hopSec,
		}
		if s.root >= 0 {
			ev.Root = mzPCNames[s.root]
			ev.Quality = s.quality
			ev.Confidence = clampUnit(s.sim)
		} else {
			ev.Root = "N"
			ev.Quality = ""
			ev.Confidence = clampUnit(1 - s.ent)
		}
		out = append(out, ev)
	}
	return out
}

// ---------------------------------------------------------------------------
// Progressions
// ---------------------------------------------------------------------------

// mzProgressions finds chord sequences of ≥ 3 chords spanning ≥ 4 s
// that occur ≥ 2 times (normalized root/quality pattern match). Windows
// are matched longest-first; overlapping hits are skipped; capped at 8.
func mzProgressions(chords []ChordEvent, hopSec float64) []ProgressionSegment {
	var seq []ChordEvent
	for _, c := range chords {
		if c.Root != "N" && c.Quality != "" {
			seq = append(seq, c)
		}
	}
	if len(seq) < 3 {
		return nil
	}
	pat := func(i, l int) string {
		parts := make([]string, 0, l)
		for k := i; k < i+l; k++ {
			parts = append(parts, seq[k].Root+" "+seq[k].Quality)
		}
		return strings.Join(parts, "|")
	}
	locs := make(map[string][]int)
	for l := 3; l <= len(seq); l++ {
		for i := 0; i+l <= len(seq); i++ {
			k := pat(i, l)
			locs[k] = append(locs[k], i)
		}
	}
	// deterministic drain: longest windows first, then earliest start
	type cand struct {
		l, i int
	}
	var order []cand
	seen := make(map[string]bool)
	for l := len(seq); l >= 3; l-- {
		for i := 0; i+l <= len(seq); i++ {
			k := pat(i, l)
			if seen[k] {
				continue
			}
			seen[k] = true
			if len(locs[k]) >= 2 {
				order = append(order, cand{l: l, i: i})
			}
		}
	}
	var out []ProgressionSegment
	var used [][2]int
	overlaps := func(s, e int) bool {
		for _, u := range used {
			if s < u[1] && u[0] < e {
				return true
			}
		}
		return false
	}
	minConf := func(i, l int) float64 {
		m := 1.0
		for k := i; k < i+l; k++ {
			m = math.Min(m, seq[k].Confidence)
		}
		return m
	}
	for _, c := range order {
		if overlaps(c.i, c.i+c.l) {
			continue
		}
		occ := locs[pat(c.i, c.l)]
		first, last := occ[0], occ[len(occ)-1]
		span := seq[last+c.l-1].End - seq[first].Start
		if span < 4 {
			continue
		}
		names := make([]string, 0, c.l)
		for k := first; k < first+c.l; k++ {
			names = append(names, seq[k].Root+" "+seq[k].Quality)
		}
		out = append(out, ProgressionSegment{
			Start:      seq[first].Start,
			End:        seq[first+c.l-1].End,
			Chords:     names,
			Confidence: clampUnit(0.35 + 0.15*float64(len(occ)-2) + 0.3*minConf(first, c.l) + 0.1*float64(c.l-3)/5),
		})
		used = append(used, [2]int{c.i, c.i + c.l})
		if len(out) >= 8 {
			break
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// DetectHarmony
// ---------------------------------------------------------------------------

// DetectHarmony implements the musical harmony layer. Chroma frames
// matching the frame count are silence-gated by Frame.RMS. Chords are
// segmented without beats here (fixed 1 s + novelty); the orchestrator
// may call DetectChords directly with Rhythm.Beats for beat-synchronous
// chord boundaries and prefer that result.
func DetectHarmony(frames []Frame, chroma []ChromaPoint, hopSec float64) *Harmony {
	h := &Harmony{Chroma: chroma}
	if len(chroma) == 0 {
		return h
	}
	useFrames := len(frames) == len(chroma)

	h.TuningOffsetCents, h.TuningConfidence = mzTuning(chroma, frames, useFrames)

	// time-averaged chroma over frames with measurable mass
	var avg [12]float64
	cnt := 0
	for i := range chroma {
		if useFrames && frames[i].RMS < 0.002 {
			continue
		}
		m := 0.0
		for j := 0; j < 12; j++ {
			m += float64(chroma[i].Chroma[j])
		}
		if m <= 1e-6 {
			continue
		}
		cnt++
		for j := 0; j < 12; j++ {
			avg[j] += float64(chroma[i].Chroma[j])
		}
	}
	if cnt > 0 {
		for j := 0; j < 12; j++ {
			avg[j] /= float64(cnt)
		}
		_, kc, conf := mzKeyScan(avg[:])
		h.KeyCandidates = kc
		h.KeyConfidence = conf
		if conf >= 0.6 && len(kc) > 0 {
			h.Key = kc[0].Key
		}
	}

	// chords + boundaries (no beats available in this signature)
	h.Chords = DetectChords(chroma, nil, hopSec)
	for _, c := range h.Chords {
		h.ChordChanges = append(h.ChordChanges, c.Start)
	}
	h.Progressions = mzProgressions(h.Chords, hopSec)

	// tension: smoothed chroma entropy + smoothed chord-change rate
	n := len(chroma)
	ent := make([]float64, n)
	for i := range chroma {
		if useFrames && frames[i].RMS < 0.002 {
			continue
		}
		var c [12]float64
		for j := 0; j < 12; j++ {
			c[j] = float64(chroma[i].Chroma[j])
		}
		ent[i] = mzNormEntropy(c[:])
	}
	br := make([]float64, n)
	if hopSec > 0 {
		const half = 1.5
		hw := int(half / hopSec)
		for _, ct := range h.ChordChanges {
			cf := int(math.Round(ct / hopSec))
			for d := -hw; d <= hw; d++ {
				f := cf + d
				if f < 0 || f >= n {
					continue
				}
				w := 1 - math.Abs(float64(d))/half
				if w > 0 {
					br[f] += w
				}
			}
		}
	}
	h.Tension = make([]float32, n)
	for f := 0; f < n; f++ {
		var s float64
		c := 0
		for d := -2; d <= 2; d++ {
			j := f + d
			if j >= 0 && j < n {
				s += ent[j]
				c++
			}
		}
		e := 0.0
		if c > 0 {
			e = s / float64(c)
		}
		h.Tension[f] = sanitizeFloat32(clampUnit(0.55*e+0.45*clampUnit(br[f])), 0, 1)
	}
	return h
}
