package dsp

// roles.go — instrument-role layer (v3 contract, see analysis_types.go
// RoleInterval and the Role* constants).
//
// Heuristic EVIDENCE RULES (documented per role; not source separation —
// see RoleNote). All roles may legitimately be empty: silence, ambience
// and synthetic tones carry no role evidence, and the layer reports
// nothing rather than guessing.
//
//      kick-like    low-band transient (bands 0/1) with percussive share ≥ 0.4
//      bass-like    sustained ≥ 0.5 s of bands 0-2 ≥ 35% of frame energy,
//                   harmonic dominance ≥ 0.5 and low flux
//      snare-like   transients in bands 5/6 or broadband (-1)
//      hihat-like   transients in bands 7/8 whose band envelope decays fast
//                   (≤ 0.6 of peak within ~85 ms — sharpness proxy)
//      cymbal-like  band 8 energy share ≥ 0.12 sustained/decaying ≥ 0.6 s
//      percussion-like dense broadband transients (≥ 4 within ±0.5 s)
//      keys-like    harmonic mid-band attacks (transient bands 3-6 with
//                   harmonic dominance ≥ 0.5) followed by decay
//      strings-like slow sustained mid harmonics (bands 3-6 ≥ 40% share,
//                   harmonic ≥ 0.5, low flux) ≥ 1 s
//      pad-like     sustained ≥ 2 s broad harmonic bed (harmonic ≥ 0.5,
//                   low flux, ≥ 4 bands with ≥ 5% share)
//      vocal-like   presence-band share ≥ 0.12 with harmonicity ≥ 0.6
//                   (simple inline rule, independent of vocalflow.go)
//
// Intervals with evidence gaps ≤ 0.3 s merge; the minimum reportable
// interval is 0.25 s; at most 400 intervals per role (strongest kept).
// Confidence scales with evidence strength; Evidence is ASCII English.

import (
	"math"
	"sort"
)

const (
	mzRoleMergeGap   = 0.3  // s of evidence gap that still merges
	mzRoleMinDur     = 0.25 // s minimum reportable interval
	mzRoleMaxPerRole = 400
)

// mzRoleSpan is one raw evidence span for a role.
type mzRoleSpan struct {
	start, end float64
	str        float64 // mean evidence strength of the span
	n          int     // evidence count
}

// mzRunTracker accumulates one frame-run role's evidence.
type mzRunTracker struct {
	start  int
	active bool
	vals   []float64
	minDur float64
	spans  []mzRoleSpan
}

func (rt *mzRunTracker) push(f int, ok bool, str float64, hopSec float64) {
	if ok {
		if !rt.active {
			rt.active = true
			rt.start = f
			rt.vals = rt.vals[:0]
		}
		rt.vals = append(rt.vals, str)
		return
	}
	rt.flush(f, hopSec)
}

func (rt *mzRunTracker) flush(e int, hopSec float64) {
	if !rt.active {
		return
	}
	rt.active = false
	dur := float64(e-rt.start) * hopSec
	if dur < rt.minDur || len(rt.vals) == 0 {
		return
	}
	var m float64
	for _, v := range rt.vals {
		m += v
	}
	m /= float64(len(rt.vals))
	rt.spans = append(rt.spans, mzRoleSpan{
		start: float64(rt.start) * hopSec,
		end:   float64(e) * hopSec,
		str:   clampUnit(m),
		n:     1,
	})
}

// mzMergeRoleSpans sorts spans by start and merges gaps ≤ gapSec.
func mzMergeRoleSpans(spans []mzRoleSpan, gapSec float64) []mzRoleSpan {
	if len(spans) == 0 {
		return nil
	}
	sort.SliceStable(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	out := make([]mzRoleSpan, 0, len(spans))
	for _, s := range spans {
		if len(out) > 0 && s.start-out[len(out)-1].end <= gapSec {
			o := &out[len(out)-1]
			if s.end > o.end {
				o.end = s.end
			}
			o.str = (o.str*float64(o.n) + s.str*float64(s.n)) / float64(o.n+s.n)
			o.n += s.n
			continue
		}
		out = append(out, s)
	}
	return out
}

// mzRoleIntervals converts merged spans to intervals with confidence.
func mzRoleIntervals(spans []mzRoleSpan, role, evidence string, base float64) []RoleInterval {
	out := make([]RoleInterval, 0, len(spans))
	for _, s := range spans {
		start, end := s.start, s.end
		if end-start < mzRoleMinDur {
			end = start + mzRoleMinDur
		}
		conf := clampUnit(base + (1-base)*clampUnit(s.str))
		out = append(out, RoleInterval{
			Role:       role,
			Start:      sanitizeFloat64(start, 0, 1e9),
			End:        sanitizeFloat64(end, 0, 1e9),
			Confidence: conf,
			Evidence:   evidence,
		})
	}
	return out
}

// EstimateRoles maps measured evidence to instrument-role intervals.
// rhythm may be nil (transient-derived roles then stay empty); hpss and
// spectral are optional but the rules that need them skip honestly when
// the series are absent.
func EstimateRoles(bands *BandTimeline, hpss *HPSSSeries, rhythm *Rhythm, spectral *SpectralSeries, hopSec float64, frames []Frame) []RoleInterval {
	all := make([]RoleInterval, 0, 32)
	if bands == nil || hopSec <= 0 {
		return all
	}
	n := len(bands.Energy[0])
	if n == 0 {
		return all
	}
	active := make([]bool, n)
	for i := 0; i < n; i++ {
		e := 0.0
		for b := 0; b < BandCount; b++ {
			e += float64(bands.Energy[b][i])
		}
		active[i] = e >= 1e-4
	}
	var harmArr []float32
	if hpss != nil {
		harmArr = hpss.Harmonic
	}
	var harmSpec []float32
	if spectral != nil {
		harmSpec = spectral.Harmonicity
	}
	at := func(a []float32, i int) float64 {
		if i >= 0 && i < len(a) {
			return float64(a[i])
		}
		return 0
	}

	// ----- transient-derived roles
	var kick, snare, hihat, percSp, keys, cymSpans []mzRoleSpan
	var bbTimes []float64
	if rhythm != nil {
		// Band shares at the onset frame decide the transient family:
		// the dominant-band label alone misroutes broadband transients
		// (a kick's window leakage spans all bands; a hat's noise is
		// broadband by nature).
		shareAt := func(pf int) (low, mid, high float64) {
			if pf < 0 || pf >= n {
				return 0, 0, 0
			}
			tot := 0.0
			for b := 0; b < BandCount; b++ {
				tot += float64(bands.Energy[b][pf])
			}
			if tot <= 1e-9 {
				return 0, 0, 0
			}
			low = (float64(bands.Energy[0][pf]) + float64(bands.Energy[1][pf])) / tot
			mid = (float64(bands.Energy[5][pf]) + float64(bands.Energy[6][pf])) / tot
			high = (float64(bands.Energy[7][pf]) + float64(bands.Energy[8][pf])) / tot
			return low, mid, high
		}
		for _, tr := range rhythm.Transients {
			pf := int(math.Round(tr.Time / hopSec))
			low, mid, high := shareAt(pf)
			switch {
			case low >= 0.5:
				// kick-like: low-frequency dominance at the onset frame.
				// HPSS percussiveness is NOT required: a kick is a
				// pitch-swept tonal burst that median-filter HPSS
				// legitimately reads as harmonic; the measured
				// low-band dominance plus the transient itself is the
				// evidence (not source separation).
				kick = append(kick, mzRoleSpan{start: tr.Time - 0.03, end: tr.Time + 0.12, str: tr.Strength, n: 1})
			case (tr.Band == 7 || tr.Band == 8 || (tr.Band == -1 && high >= 0.3 && low < 0.4)) && high >= 0.25:
				// sharpness proxy: RAW band energy decay within ~85 ms
				// (the release-smoothed envelope hides true decay:
				// exp(-85 ms / 180 ms release) = 0.62 sits exactly on
				// the boundary, so the unsmoothed energy is measured)
				short := true
				if pf >= 0 && pf < n {
					for _, b := range []int{7, 8} {
						// Energy[b] is a SHARE of the frame total, which is
						// amplitude-invariant (a decaying hat keeps its
						// spectral shape, so its share does not decay); scale
						// by the frame power to get ABSOLUTE band energy.
						head, tail := 0.0, 0.0
						for d := 0; d <= 4 && pf+d < n; d++ {
							p := float64(frames[pf+d].RMS)
							v := at(bands.Energy[b], pf+d) * p * p
							if v > head {
								head = v
							}
						}
						for d := 8; d <= 11 && pf+d < n; d++ {
							p := float64(frames[pf+d].RMS)
							v := at(bands.Energy[b], pf+d) * p * p
							if v > tail {
								tail = v
							}
						}
						if head > 1e-6 && tail > 0.4*head {
							short = false
						}
					}
				}
				if short {
					hihat = append(hihat, mzRoleSpan{start: tr.Time - 0.01, end: tr.Time + 0.06, str: tr.Strength, n: 1})
				} else {
					cymSpans = append(cymSpans, mzRoleSpan{start: tr.Time, end: tr.Time + 1.2, str: tr.Strength, n: 1})
				}
			case (tr.Band == 5 || tr.Band == 6 || tr.Band == -1) && low < 0.45 && mid+high >= 0.2:
				snare = append(snare, mzRoleSpan{start: tr.Time - 0.02, end: tr.Time + 0.15, str: tr.Strength, n: 1})
				if tr.Band == -1 {
					bbTimes = append(bbTimes, tr.Time)
				}
			case tr.Band >= 3 && tr.Band <= 6:
				if at(harmArr, pf) >= 0.5 && tr.Percussive < 0.5 {
					keys = append(keys, mzRoleSpan{start: tr.Time - 0.01, end: tr.Time + 0.6, str: tr.Strength, n: 1})
				}
			}
		}
		// percussion-like: dense broadband transients (≥ 4 in ±0.5 s)
		for _, t := range bbTimes {
			c := 0
			for _, u := range bbTimes {
				if math.Abs(u-t) <= 0.5 {
					c++
				}
			}
			if c >= 4 {
				percSp = append(percSp, mzRoleSpan{start: t - 0.5, end: t + 0.5, str: 0.6, n: 1})
			}
		}
	}

	// ----- frame-run roles (bass / cymbal / strings / pad / vocal)
	rtBass := &mzRunTracker{minDur: 0.5}
	rtCym := &mzRunTracker{minDur: 0.6}
	rtStr := &mzRunTracker{minDur: 1.0}
	rtPad := &mzRunTracker{minDur: 2.0}
	rtVoc := &mzRunTracker{minDur: 0.5}
	for f := 0; f <= n; f++ {
		if f >= n || !active[f] {
			rtBass.push(f, false, 0, hopSec)
			rtCym.push(f, false, 0, hopSec)
			rtStr.push(f, false, 0, hopSec)
			rtPad.push(f, false, 0, hopSec)
			rtVoc.push(f, false, 0, hopSec)
			continue
		}
		total, low, mid, bril, pres := 0.0, 0.0, 0.0, 0.0, 0.0
		spread := 0
		for b := 0; b < BandCount; b++ {
			e := float64(bands.Energy[b][f])
			total += e
			if b <= 2 {
				low += e
			}
			if b >= 3 && b <= 6 {
				mid += e
			}
			if b == 8 {
				bril = e
			}
			if e >= 0.05 {
				spread++
			}
			if b == 5 || b == 6 {
				pres += e
			}
		}
		if total <= 1e-9 {
			rtBass.push(f, false, 0, hopSec)
			rtCym.push(f, false, 0, hopSec)
			rtStr.push(f, false, 0, hopSec)
			rtPad.push(f, false, 0, hopSec)
			rtVoc.push(f, false, 0, hopSec)
			continue
		}
		// Sustained-content evidence is the HPSS harmonic share (the
		// temporal median separates steady tonal energy from
		// transients). A relative-flux gate cannot do this: a steady
		// tone that is not periodic in the hop grid has genuinely
		// fluctuating window magnitudes, so its measured flux is O(1).
		hd := at(harmArr, f)
		rtBass.push(f, low/total >= 0.35 && hd >= 0.5, low/total, hopSec)
		rtCym.push(f, bril >= 0.12*total, bril/total, hopSec)
		rtStr.push(f, mid/total >= 0.4 && hd >= 0.5, mid/total, hopSec)
		rtPad.push(f, hd >= 0.5 && spread >= 3, hd, hopSec)
		rtVoc.push(f, pres/total >= 0.12 && at(harmSpec, f) >= 0.6, pres/total, hopSec)
	}
	rtBass.flush(n, hopSec)
	rtCym.flush(n, hopSec)
	rtStr.flush(n, hopSec)
	rtPad.flush(n, hopSec)
	rtVoc.flush(n, hopSec)

	kickI := mzRoleIntervals(mzMergeRoleSpans(kick, mzRoleMergeGap), RoleKick, "low-band transient + percussive", 0.35)
	bassI := mzRoleIntervals(mzMergeRoleSpans(rtBass.spans, mzRoleMergeGap), RoleBass, "sustained low-band energy + harmonic", 0.3)
	snareI := mzRoleIntervals(mzMergeRoleSpans(snare, mzRoleMergeGap), RoleSnare, "mid-band transient + broadband", 0.35)
	hihatI := mzRoleIntervals(mzMergeRoleSpans(hihat, mzRoleMergeGap), RoleHiHat, "high-band short transient + fast decay", 0.35)
	cymAll := append(append([]mzRoleSpan{}, rtCym.spans...), cymSpans...)
	cymI := mzRoleIntervals(mzMergeRoleSpans(cymAll, mzRoleMergeGap), RoleCymbal, "sustained brilliance decay", 0.3)
	percI := mzRoleIntervals(mzMergeRoleSpans(percSp, mzRoleMergeGap), RolePerc, "dense broadband transients", 0.35)
	keysI := mzRoleIntervals(mzMergeRoleSpans(keys, mzRoleMergeGap), RoleKeys, "harmonic mid attack + decay", 0.35)
	strI := mzRoleIntervals(mzMergeRoleSpans(rtStr.spans, mzRoleMergeGap), RoleStrings, "slow sustained mid harmonics", 0.3)
	padI := mzRoleIntervals(mzMergeRoleSpans(rtPad.spans, mzRoleMergeGap), RolePad, "sustained broad harmonic bed", 0.3)
	vocI := mzRoleIntervals(mzMergeRoleSpans(rtVoc.spans, mzRoleMergeGap), RoleVocal, "presence-band + harmonic", 0.3)

	perRole := [][]RoleInterval{kickI, bassI, snareI, hihatI, cymI, percI, keysI, strI, padI, vocI}
	for _, list := range perRole {
		if len(list) > mzRoleMaxPerRole {
			// keep the strongest 400, restore chronological order
			sort.SliceStable(list, func(i, j int) bool { return list[i].Confidence > list[j].Confidence })
			list = list[:mzRoleMaxPerRole]
			sort.SliceStable(list, func(i, j int) bool { return list[i].Start < list[j].Start })
		}
		all = append(all, list...)
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Start != all[j].Start {
			return all[i].Start < all[j].Start
		}
		return all[i].Role < all[j].Role
	})
	return all
}
