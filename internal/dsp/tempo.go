package dsp

// tempo.go — onset detection and tempo estimation with explicit
// confidence. Everything is deterministic: pure functions over the
// measured analysis frames. BPM is an estimate; confidence below the
// threshold means "unreliable" and the UI must say so.

import "math"

// detectOnsets marks frames whose flux exceeds an adaptive threshold
// (local mean + k·σ over a sliding window) and stores Onset flags.
// Returns the onset count. Peak-picking uses a small local-maximum
// guard so one accented hit yields one event.
func detectOnsets(frames []Frame) int {
	if len(frames) < 8 {
		return 0
	}
	const win = 43 // ≈0.46 s context at 10.7 ms hops
	const k = 1.6
	onsets := 0
	for i := range frames {
		lo := i - win
		if lo < 0 {
			lo = 0
		}
		hi := i + win
		if hi > len(frames) {
			hi = len(frames)
		}
		var sum, sumsq float64
		n := 0
		for j := lo; j < hi; j++ {
			if j == i {
				continue
			}
			v := frames[j].Flux
			sum += v
			sumsq += v * v
			n++
		}
		if n == 0 {
			continue
		}
		mean := sum / float64(n)
		sd := math.Sqrt(sumsq/float64(n) - mean*mean)
		th := mean + k*sd
		localMax := true
		if i > 0 && frames[i-1].Flux >= frames[i].Flux {
			localMax = false
		}
		if i+1 < len(frames) && frames[i+1].Flux > frames[i].Flux {
			localMax = false
		}
		on := frames[i].Flux > th && frames[i].Flux > 0 && localMax
		frames[i].Onset = on
		if on {
			onsets++
		}
	}
	return onsets
}

// tempo candidates are searched in this BPM range.
const (
	minBPM = 60.0
	maxBPM = 190.0
)

// estimateTempo autocorrelates the onset/RMS envelope over candidate
// periods and reports the best BPM with a normalized confidence.
// Half/double-time ambiguity is resolved toward the center of the
// range and documented in the note.
func estimateTempo(frames []Frame, hopSec float64) (bpm, confidence float64, note string) {
	n := len(frames)
	if n < 64 { // < ~0.7 s: hopeless
		return 0, 0, "track too short for tempo estimation"
	}
	// Onset strength envelope: flux accentuated by onset flags.
	env := make([]float64, n)
	var emax float64
	for i, f := range frames {
		v := f.Flux
		if f.Onset {
			v += f.Flux + 0.1
		}
		env[i] = v
		if v > emax {
			emax = v
		}
	}
	if emax <= 0 {
		return 0, 0, "no rhythmic energy detected"
	}
	// A steady tone has a near-constant envelope: no rhythm to find.
	// Gate on the RMS envelope variation, which is robust against
	// numerical flux noise for constant content.
	var meanRMS, varRMS float64
	for _, f := range frames {
		meanRMS += f.RMS
	}
	meanRMS /= float64(n)
	for _, f := range frames {
		d := f.RMS - meanRMS
		varRMS += d * d
	}
	if meanRMS <= 0 || math.Sqrt(varRMS/float64(n)) < 0.05*meanRMS {
		return 0, 0, "no rhythmic energy variation (steady content)"
	}
	for i := range env {
		env[i] /= emax
	}

	type cand struct {
		bpm   float64
		score float64
	}
	best := cand{}
	// total signal power for score normalization
	var power float64
	for _, v := range env {
		power += v * v
	}
	if power <= 0 {
		return 0, 0, "no rhythmic energy detected"
	}

	// Scan candidate periods with 0.5 BPM resolution.
	for bpmCand := minBPM; bpmCand <= maxBPM; bpmCand += 0.5 {
		lag := int(math.Round(60.0 / bpmCand / hopSec))
		if lag < 2 || lag >= n-2 {
			continue
		}
		var acc float64
		for i := 0; i+lag < n; i++ {
			acc += env[i] * env[i+lag]
		}
		acc /= float64(n - lag)
		// Note: no subdivision bonus — it biases metronome-like
		// material toward the half-time reading; the ambiguity is
		// handled explicitly below instead.
		if acc > best.score {
			best = cand{bpm: bpmCand, score: acc}
		}
	}
	if best.score <= 0 {
		return 0, 0, "no periodic structure found"
	}
	bpm = best.bpm
	// half/double-time handling: a candidate below 90 BPM whose double
	// scores nearly as well is reported doubled (with a note).
	if bpm < 90 {
		double := bpm * 2
		if double <= maxBPM {
			lagD := int(math.Round(60.0 / double / hopSec))
			if lagD >= 2 && lagD < n-2 {
				var accD float64
				for i := 0; i+lagD < n; i++ {
					accD += env[i] * env[i+lagD]
				}
				accD /= float64(n - lagD)
				if accD >= 0.85*best.score {
					bpm = double
					note = "half-time reading doubled (beat ambiguity)"
				}
			}
		}
	}
	// confidence: normalized autocorrelation peak, squashed into 0..1
	confidence = best.score / (power / float64(n))
	if confidence > 1 {
		confidence = 1
	}
	// half/double-time note
	switch {
	case bpm < 85:
		note = "near lower range boundary; may be the double-time reading"
	case bpm > 165:
		note = "near upper range boundary; may be the half-time reading"
	default:
		note = "mid-range reading"
	}
	return bpm, confidence, note
}
