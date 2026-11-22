package dsp

// structure.go — structural section detection (§6.9): a time-aligned
// map of candidate sections (intro, build-up, drop, high/low energy,
// break, outro, repeats, transitions) derived from MEASURED novelty,
// dynamics, density and harmonic change. Semantic labels such as
// verse/chorus are deliberately NOT claimed: without lyric evidence
// they would be guesses. A stable neutral fallback ("unknown",
// "transition") is always available.
//
// Method: one-second feature blocks (nine-band energy profile + RMS +
// flux + chroma means), checkerboard-kernel novelty on the block
// self-similarity for change points, then measured per-segment stats
// for labels. Deterministic; bounded work (O(B^2) with B = duration in
// seconds, capped).

import (
	"math"
	"sort"
)

// structureBlockSec is the feature-block resolution (1 s).
const structureBlockSec = 1.0

// structureFeatureDims is the per-block feature vector length:
// 9 band energies + mean RMS + mean flux + 12 chroma means.
const structureFeatureDims = BandCount + 2 + 12

// maxSections bounds the returned sections.
const maxSections = 200

// DetectStructure labels candidate structural sections. Inputs may be
// partially nil (chroma nil → dims zeroed; rhythm nil → density
// evidence omitted); the layer degrades to dynamics-only evidence and
// reports lower confidence rather than failing.
func DetectStructure(frames []Frame, chroma []ChromaPoint, bands *BandTimeline, rhythm *Rhythm, hopSec float64) []SectionEvent {
	if len(frames) == 0 || hopSec <= 0 {
		return nil
	}
	blocksPerSec := int(0.5 + 1.0/hopSec)
	if blocksPerSec < 1 {
		blocksPerSec = 1
	}
	nBlocks := len(frames) / blocksPerSec
	if nBlocks < 3 {
		return nil
	}

	// 1. Feature blocks.
	feat := make([][structureFeatureDims]float64, nBlocks)
	for b := 0; b < nBlocks; b++ {
		f0 := b * blocksPerSec
		f1 := f0 + blocksPerSec
		c := float64(f1 - f0)
		if bands != nil {
			for band := 0; band < BandCount; band++ {
				var s float64
				for f := f0; f < f1; f++ {
					s += float64(bands.Energy[band][f])
				}
				feat[b][band] = s / c
			}
		}
		var rms, flux float64
		for f := f0; f < f1; f++ {
			rms += frames[f].RMS
			flux += frames[f].Flux
		}
		feat[b][BandCount] = rms / c
		feat[b][BandCount+1] = flux / c
		if chroma != nil {
			for j := 0; j < 12; j++ {
				var s float64
				for f := f0; f < f1 && f < len(chroma); f++ {
					s += float64(chroma[f].Chroma[j])
				}
				feat[b][BandCount+2+j] = s / c
			}
		}
	}
	// Raw block RMS (measured dynamics cue) captured BEFORE the
	// per-dimension normalization below.
	blockRMS := make([]float64, nBlocks)
	for b := 0; b < nBlocks; b++ {
		blockRMS[b] = feat[b][BandCount]
	}
	// Normalize each dimension by its global max (guard zero) so no
	// single dimension dominates the similarity.
	for d := 0; d < structureFeatureDims; d++ {
		mx := 0.0
		for b := 0; b < nBlocks; b++ {
			if feat[b][d] > mx {
				mx = feat[b][d]
			}
		}
		if mx > 0 {
			for b := 0; b < nBlocks; b++ {
				feat[b][d] /= mx
			}
		}
	}

	// 2. Checkerboard novelty (4 s half-window) on block means: dance
	// music changes sections every 4-8 s, so an 8 s kernel smears the
	// transitions the composition engine needs.
	half := 4
	if half*2 > nBlocks {
		half = nBlocks / 4
		if half < 1 {
			half = 1
		}
	}
	nov := make([]float64, nBlocks)
	for i := 0; i < nBlocks; i++ {
		// Edge-clamped windows: novelty must be computable across the
		// WHOLE track (a strict [half, n-half] range would blind the
		// detector to late transitions). Both windows keep at least
		// two blocks.
		lo := i - half
		if lo < 0 {
			lo = 0
		}
		hi := i + half
		if hi > nBlocks {
			hi = nBlocks
		}
		if i-lo < 2 || hi-i < 2 {
			nov[i] = 0
			continue
		}
		var a, b2 [structureFeatureDims]float64
		for k := lo; k < i; k++ {
			for d := 0; d < structureFeatureDims; d++ {
				a[d] += feat[k][d]
			}
		}
		for k := i; k < hi; k++ {
			for d := 0; d < structureFeatureDims; d++ {
				b2[d] += feat[k][d]
			}
		}
		// Magnitude-aware novelty: Euclidean distance between the two
		// MEAN-per-block vectors, normalized by total magnitude. A pure
		// cosine metric is blind to loud-vs-quiet transitions whose
		// timbre is identical (drop -> break with the same kick
		// spectrum); the Euclidean form measures both direction and
		// energy change.
		na := float64(i - lo)
		nb := float64(hi - i)
		var dist float64
		for d := 0; d < structureFeatureDims; d++ {
			ma := a[d] / na
			mb := b2[d] / nb
			dist += (ma - mb) * (ma - mb)
		}
		dist = math.Sqrt(dist)
		var mag float64
		for d := 0; d < structureFeatureDims; d++ {
			ma := a[d] / na
			mb := b2[d] / nb
			mag += math.Sqrt(ma*ma) + math.Sqrt(mb*mb)
		}
		if mag <= 1e-9 {
			nov[i] = 0
		} else {
			nov[i] = clampUnit(dist / mag)
		}
	}

	// 3. Change points: novelty above mean+1.5*std, local max, minimum
	// separation of 8 s.
	var mean, std float64
	for _, v := range nov {
		mean += v
	}
	mean /= float64(nBlocks)
	for _, v := range nov {
		d := v - mean
		std += d * d
	}
	std = math.Sqrt(std / float64(nBlocks))
	thresh := mean + 0.8*std
	// minSep is in BLOCKS and one block IS one second: 3 blocks = 3 s.
	minSep := 3
	type cand struct {
		block    int
		direct   bool // measured dynamics cue (silence run / RMS step)
		strength float64
	}
	var cands []cand
	for i := 1; i < nBlocks-1; i++ {
		if nov[i] < thresh || nov[i] < nov[i-1] || nov[i] < nov[i+1] {
			continue
		}
		cands = append(cands, cand{block: i, strength: nov[i]})
	}
	// 2b. DIRECT measured dynamics cues: a silence run is a structural
	// boundary by itself, and an energy STEP (transient music builds
	// and drops between adjacent seconds) is a boundary the smoothed
	// novelty kernel can under-weight.
	maxRMS := 0.0
	for _, r := range blockRMS {
		if r > maxRMS {
			maxRMS = r
		}
	}
	runStart := -1
	for b := 0; b <= nBlocks; b++ {
		quiet := b < nBlocks && blockRMS[b] < 0.12*maxRMS
		if quiet && runStart < 0 {
			runStart = b
		}
		if !quiet && runStart >= 0 {
			if b-runStart >= 2 {
				cands = append(cands, cand{block: runStart, direct: true, strength: 1})
				cands = append(cands, cand{block: b, direct: true, strength: 1})
			}
			runStart = -1
		}
	}
	for b := 1; b < nBlocks; b++ {
		r0, r1 := blockRMS[b-1], blockRMS[b]
		if (r1 > 1.8*r0 && r1 >= 0.3*maxRMS) || (r0 > 1.8*r1 && r0 >= 0.3*maxRMS) {
			cands = append(cands, cand{block: b, direct: true, strength: 1})
		}
	}
	// Order candidates deterministically: direct cues first at equal
	// blocks (they are precise), then by block.
	sort.SliceStable(cands, func(i, k int) bool {
		if cands[i].block != cands[k].block {
			return cands[i].block < cands[k].block
		}
		return cands[i].direct && !cands[k].direct
	})
	var cps []int
	last := -minSep
	lastDirect := false
	for _, c := range cands {
		// Direct cues are precise measured boundaries: two direct cues
		// only need 2 s separation (dance sections can be that short),
		// while a direct cue vs a (imprecise) novelty peak needs the
		// full minSep and the peak is replaced.
		sep := minSep
		if c.direct && lastDirect {
			sep = 2
		}
		if c.block-last < sep {
			if c.direct && len(cps) > 0 && !lastDirect {
				cps[len(cps)-1] = c.block
				last = c.block
				lastDirect = true
			}
			continue
		}
		cps = append(cps, c.block)
		last = c.block
		lastDirect = c.direct
	}

	// 4. Segments + measured stats.
	type segStat struct {
		start, end  int // block indices
		rms         float64
		flux        float64
		silenceFrac float64
		density     float64
	}
	var segs []segStat
	bounds := append([]int{0}, cps...)
	for i := 0; i < len(bounds); i++ {
		s := bounds[i]
		e := nBlocks
		if i+1 < len(bounds) {
			e = bounds[i+1]
		}
		if e-s < 2 {
			if i == 0 {
				// Degenerate FIRST segment merges into the next one:
				// the first section must always start at 0.
				if i+1 < len(bounds) {
					bounds[i+1] = 0
				}
			}
			continue // absorb degenerate segments
		}
		var st segStat
		st.start, st.end = s, e
		c := float64(e - s)
		for b := s; b < e; b++ {
			st.rms += feat[b][BandCount]
			st.flux += feat[b][BandCount+1]
		}
		st.rms /= c
		st.flux /= c
		// Silence measured over EVERY frame in the block, not one
		// sampled frame per second (a sampled frame can land between
		// transients and misread a loud section as silence).
		sil := 0
		for f := s * blocksPerSec; f < e*blocksPerSec && f < len(frames); f++ {
			if frames[f].RMS < 0.002 {
				sil++
			}
		}
		st.silenceFrac = float64(sil) / float64(e*blocksPerSec-s*blocksPerSec)
		if rhythm != nil {
			dur := c * structureBlockSec
			for _, tr := range rhythm.Transients {
				b := int(tr.Time / structureBlockSec)
				if b >= s && b < e {
					st.density++
				}
			}
			if dur > 0 {
				st.density /= dur
			}
		}
		segs = append(segs, st)
	}

	// Track-median RMS/flux/density for high/low comparisons.
	var rmss, fluxes, denss []float64
	for i := range segs {
		rmss = append(rmss, segs[i].rms)
		fluxes = append(fluxes, segs[i].flux)
		denss = append(denss, segs[i].density)
	}
	medianRMS := structureMedian(rmss)
	medianFlux := structureMedian(fluxes)
	medianDensity := structureMedian(denss)

	// 5. Labels.
	out := make([]SectionEvent, 0, len(segs))
	type sig struct {
		key uint64
	}
	prevSigs := map[sig]int{}
	for i := range segs {
		st := &segs[i]
		startSec := float64(st.start) * structureBlockSec
		endSec := float64(st.end) * structureBlockSec
		dur := endSec - startSec
		label := SectionUnknown
		conf := 0.3

		rising, falling := structureTrend(feat, st.start, st.end, BandCount)
		// Perceived energy in transient music comes from density and
		// flux as much as from RMS, so the labels use all three
		// measured dimensions.
		highEvidence := st.rms > 1.15*medianRMS ||
			(st.flux > 1.5*medianFlux && st.density > 1.5*medianDensity)
		lowEvidence := st.rms < 0.8*medianRMS && st.flux < 0.8*medianFlux
		switch {
		case st.silenceFrac >= 0.8 && dur >= 2:
			label, conf = SectionBreak, clampUnit(0.5+0.4*st.silenceFrac)
		case i == 0 && (rising > 0.15 || st.flux > 1.5*medianFlux) && st.rms < medianRMS:
			label, conf = SectionIntro, clampUnit(0.4+0.3*math.Max(rising, 0.3))
		case i == len(segs)-1 && falling > 0.15 && st.rms < medianRMS && st.flux < medianFlux:
			label, conf = SectionOutro, clampUnit(0.4+0.3*falling)
		case i > 0 && segs[i-1].rms < 0.7*medianRMS &&
			(st.rms > 1.3*medianRMS || (st.flux > 2*medianFlux && st.density > medianDensity)):
			label, conf = SectionDrop, clampUnit(0.45+0.3*st.rms/math.Max(medianRMS, 1e-9))
		case rising >= 0.25 && st.density > 0 && st.rms <= 1.05*medianRMS:
			label, conf = SectionBuild, clampUnit(0.35+0.35*rising)
		case highEvidence:
			label, conf = SectionHigh, clampUnit(0.35+0.3*math.Max(st.rms/medianRMS-1, st.flux/medianFlux-1))
		case lowEvidence:
			label, conf = SectionLow, clampUnit(0.35+0.4*(1-st.rms/math.Max(medianRMS, 1e-9)))
		}
		// Repeat detection: quantized signature equal to an earlier
		// segment's signature.
		k := structureSignature(feat, st.start, st.end)
		if j, ok := prevSigs[sig{k}]; ok && dur >= 2 {
			if label == SectionUnknown {
				label = SectionRepeat
				conf = 0.5
			} else {
				conf = clampUnit(conf + 0.1)
			}
			_ = j
		} else {
			prevSigs[sig{k}] = i
		}
		out = append(out, SectionEvent{
			Start: startSec, End: endSec,
			Label: label, Confidence: clampUnit(conf),
		})
	}
	// 6. Bound and return.
	if len(out) > maxSections {
		out = out[:maxSections]
	}
	return out
}

// candsContains reports whether block b is already a change point.
func candsContains(cps []int, b int) bool {
	for _, v := range cps {
		if v == b {
			return true
		}
	}
	return false
}

// structureCosine returns the cosine similarity of two vectors.
func structureCosine(a, b []float64) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na <= 0 || nb <= 0 {
		return 0
	}
	return dot / math.Sqrt(na*nb)
}

// structureTrend estimates the normalized rise (or fall) of the RMS
// dimension across the segment: >0 rising, <0 falling.
func structureTrend(feat []([structureFeatureDims]float64), s, e, rmsDim int) (rise, fall float64) {
	n := e - s
	if n < 3 {
		return 0, 0
	}
	// Least-squares slope over block RMS.
	var sx, sy, sxy, sxx float64
	for k := 0; k < n; k++ {
		x := float64(k)
		y := feat[s+k][rmsDim]
		sx += x
		sy += y
		sxy += x * y
		sxx += x * x
	}
	den := float64(n)*sxx - sx*sx
	if den <= 0 {
		return 0, 0
	}
	slope := (float64(n)*sxy - sx*sy) / den
	mean := sy / float64(n)
	if mean <= 0 {
		return 0, 0
	}
	norm := slope * float64(n) / mean
	if norm > 0 {
		return clampUnit(norm), 0
	}
	return 0, clampUnit(-norm)
}

// structureMedian returns the median of a copied slice.
func structureMedian(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	c := make([]float64, len(v))
	copy(c, v)
	// insertion sort (deterministic; n is small)
	for i := 1; i < len(c); i++ {
		for j := i; j > 0 && c[j] < c[j-1]; j-- {
			c[j], c[j-1] = c[j-1], c[j]
		}
	}
	return c[len(c)/2]
}

// structureSignature quantizes a segment's mean feature vector into a
// 64-bit signature (4 bits per dimension, 16 dims sampled) for repeat
// detection.
func structureSignature(feat []([structureFeatureDims]float64), s, e int) uint64 {
	var mean [structureFeatureDims]float64
	c := float64(e - s)
	for b := s; b < e; b++ {
		for d := 0; d < structureFeatureDims; d++ {
			mean[d] += feat[b][d]
		}
	}
	for d := range mean {
		mean[d] /= c
	}
	var key uint64
	// Sample 16 evenly spaced dimensions.
	for k := 0; k < 16; k++ {
		d := k * structureFeatureDims / 16
		q := int(mean[d] * 15)
		if q > 15 {
			q = 15
		}
		key = key<<4 | uint64(q)
	}
	return key
}
