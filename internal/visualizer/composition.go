package visualizer

// composition.go — the §7.1 COMMON TIME BASE: every measured feature
// of the v3 analysis is addressable by media timestamp, and the SAME
// lookup drives the live preview and the offline export (both consume
// CompositionState through visualizer.AudioState, so the two can never
// disagree by construction).
//
// Interpolation contract:
//   - continuous measurements (band envelopes, stereo, loudness,
//     tension, vocal likelihood) interpolate linearly on the hop grid;
//   - DISCRETE events (transient onsets, chord changes, section
//     changes, beats) are never interpolated: they are looked up as
//     events in a bounded time window and delivered as discrete flags
//     and strengths;
//   - low-confidence features are delivered WITH their confidence so
//     the renderer can gate its reaction (§7.3); unknown is never
//     silently mapped to zero where zero has no measurement meaning.

import (
	"math"
	"sort"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/dsp"
)

// Composition wraps one immutable v3 analysis document for timestamp
// lookups. It is safe for concurrent use after construction (all data
// is read-only).
type Composition struct {
	a         *dsp.Analysis
	bandEnv   [dsp.BandCount][]float32
	bandShare [dsp.BandCount][]float32
	// transient index: times sorted with their event indices.
	transientTimes []float64
	frameCount     int
	hopSec         float64
	duration       float64
	beatCount      int
}

// NewComposition builds a lookup over the analysis. nil analysis yields
// a valid, all-unavailable composition (the renderer falls back to the
// legacy audio-driven behavior with confidence 0).
func NewComposition(a *dsp.Analysis) *Composition {
	c := &Composition{a: a}
	if a == nil {
		return c
	}
	c.hopSec = a.HopSeconds
	c.duration = a.Duration
	c.frameCount = a.FrameCount
	if a.Bands != nil {
		for b := 0; b < dsp.BandCount; b++ {
			c.bandEnv[b] = a.Bands.Envelope[b]
			c.bandShare[b] = a.Bands.Energy[b]
		}
	}
	if a.Rhythm != nil {
		c.transientTimes = make([]float64, len(a.Rhythm.Transients))
		for i, tr := range a.Rhythm.Transients {
			c.transientTimes[i] = tr.Time
		}
		c.beatCount = len(a.Rhythm.Beats)
	}
	return c
}

// CompositionState is one media timestamp's composition snapshot.
type CompositionState struct {
	// Band envelopes (calibrated measurements, 0..1) and shares.
	BandEnvelope [dsp.BandCount]float64
	BandShare    [dsp.BandCount]float64
	// Discrete transient evidence: strength of the nearest transient
	// within TransientWindowSec, or 0. TransientBand is its dominant
	// band (-1 broadband); discrete events are NOT interpolated.
	Transient     float64
	TransientBand int
	// Beat/downbeat phase from the measured grid (0..1, or -1 when no
	// reliable grid exists — never fabricated from tempo alone).
	BeatPhase    float64
	DownbeatSoon bool
	// Harmony state (discrete segments; no interpolation across a
	// chord boundary).
	ChordRoot       string
	ChordQuality    string
	ChordConfidence float64
	KeyConfidence   float64
	Tension         float64
	// Vocal-flow state.
	VocalLikelihood   float64
	VocalDensity      float64
	VocalPhraseActive bool
	// Structure: the section covering t (index into the analysis
	// document; -1 when unknown) and its neutral label.
	SectionIndex int
	SectionLabel string
	// Stereo and dynamics.
	StereoWidth   float64
	Correlation   float64
	MomentaryLUFS float64
	// Availability: 0..1 overall feature confidence (a coarse product
	// of the layer confidences present at t; 0 = no usable analysis).
	Confidence float64
}

// TransientWindowSec bounds the discrete transient lookup.
const TransientWindowSec = 0.08

// StateAt resolves the composition snapshot at media time t (seconds).
// Deterministic: identical inputs produce identical states, independent
// of call order.
func (c *Composition) StateAt(t float64) CompositionState {
	var st CompositionState
	st.SectionIndex = -1
	st.TransientBand = -1
	st.BeatPhase = -1
	if c == nil || c.a == nil || c.frameCount == 0 || c.hopSec <= 0 {
		return st
	}
	a := c.a

	// Frame position on the hop grid (window-start contract).
	idx := int(t / c.hopSec)
	if idx < 0 {
		idx = 0
	}
	if idx >= c.frameCount {
		idx = c.frameCount - 1
	}
	next := idx + 1
	if next >= c.frameCount {
		next = idx
	}
	frac := t/c.hopSec - float64(idx)
	if frac < 0 {
		frac = 0
	} else if frac > 1 {
		frac = 1
	}

	// Continuous band measurements, linearly interpolated.
	for b := 0; b < dsp.BandCount; b++ {
		env := c.bandEnv[b]
		if len(env) > 0 {
			st.BandEnvelope[b] = lerpF32(env[idx], env[next], frac)
		}
		sh := c.bandShare[b]
		if len(sh) > 0 {
			st.BandShare[b] = lerpF32(sh[idx], sh[next], frac)
		}
	}

	// Discrete transients: binary search the nearest event; deliver
	// strength only inside the window (never smeared by interpolation).
	if len(c.transientTimes) > 0 && a.Rhythm != nil {
		i := sort.SearchFloat64s(c.transientTimes, t)
		best := -1
		bestDist := TransientWindowSec
		for _, j := range []int{i - 1, i} {
			if j >= 0 && j < len(c.transientTimes) {
				d := math.Abs(c.transientTimes[j] - t)
				if d <= bestDist {
					bestDist = d
					best = j
				}
			}
		}
		if best >= 0 {
			tr := a.Rhythm.Transients[best]
			st.Transient = tr.Strength * (1 - bestDist/TransientWindowSec)
			st.TransientBand = tr.Band
		}
	}

	// Beat phase: measured grid only (confidence-gated upstream; the
	// rhythm layer already refuses to emit beats when unreliable).
	if a.Rhythm != nil && len(a.Rhythm.BeatPhase) > 0 {
		ph := float64(a.Rhythm.BeatPhase[idx])
		if ph >= 0 {
			ph2 := float64(a.Rhythm.BeatPhase[next])
			if ph2 >= 0 {
				st.BeatPhase = lerp(ph, ph2, frac)
			} else {
				st.BeatPhase = ph
			}
		}
	}
	if a.Rhythm != nil && len(a.Rhythm.Downbeats) > 0 {
		// Downbeat-soon: a downbeat within the next half beat distance
		// measured from the grid (discrete evidence, no fabrication).
		beat := 0.5
		if len(a.Rhythm.Beats) >= 2 {
			beat = a.Rhythm.Beats[1].Time - a.Rhythm.Beats[0].Time
		}
		for _, d := range a.Rhythm.Downbeats {
			if d.Time >= t && d.Time <= t+0.5*beat {
				st.DownbeatSoon = true
				break
			}
			if d.Time > t+0.5*beat {
				break
			}
		}
	}

	// Harmony: the chord segment covering t (discrete lookup).
	if a.Harmony != nil {
		for _, ch := range a.Harmony.Chords {
			if t >= ch.Start && t < ch.End {
				st.ChordRoot = ch.Root
				st.ChordQuality = ch.Quality
				st.ChordConfidence = ch.Confidence
				break
			}
			if ch.Start > t {
				break
			}
		}
		st.KeyConfidence = a.Harmony.KeyConfidence
		if len(a.Harmony.Tension) > 0 {
			st.Tension = lerpF32(a.Harmony.Tension[idx], a.Harmony.Tension[next], frac)
		}
	}

	// Vocal flow: likelihood + density interpolated; phrase activity is
	// a discrete interval lookup.
	if a.Vocal != nil {
		if len(a.Vocal.Likelihood) > 0 {
			st.VocalLikelihood = lerpF32(a.Vocal.Likelihood[idx], a.Vocal.Likelihood[next], frac)
		}
		if len(a.Vocal.SyllableDensity) > 0 {
			st.VocalDensity = lerpF32(a.Vocal.SyllableDensity[idx], a.Vocal.SyllableDensity[next], frac)
		}
		for _, p := range a.Vocal.Phrases {
			if t >= p.Start && t < p.End {
				st.VocalPhraseActive = true
				break
			}
			if p.Start > t {
				break
			}
		}
	}

	// Structure: the section covering t (index lookup, neutral label).
	for i, sec := range a.Structure {
		if t >= sec.Start && t < sec.End {
			st.SectionIndex = i
			st.SectionLabel = sec.Label
			break
		}
		if sec.Start > t {
			break
		}
	}

	// Stereo + dynamics.
	if a.Stereo != nil {
		if len(a.Stereo.Width) > 0 {
			st.StereoWidth = lerpF32(a.Stereo.Width[idx], a.Stereo.Width[next], frac)
		}
		if len(a.Stereo.Correlation) > 0 {
			st.Correlation = lerpF32(a.Stereo.Correlation[idx], a.Stereo.Correlation[next], frac)
		}
	}
	if a.Dynamics != nil && len(a.Dynamics.MomentaryLUFS) > 0 {
		st.MomentaryLUFS = lerpF32(a.Dynamics.MomentaryLUFS[idx], a.Dynamics.MomentaryLUFS[next], frac)
	}

	// Overall confidence: a coarse, honest availability product.
	conf := 1.0
	if a.Rhythm == nil || !a.Rhythm.TempoReliable {
		conf *= 0.8
	}
	if a.Harmony == nil || a.Harmony.KeyConfidence < 0.3 {
		conf *= 0.85
	}
	if a.Vocal == nil || a.Vocal.OverallConfidence < 0.3 {
		conf *= 0.9
	}
	st.Confidence = conf
	return st
}

// Duration returns the analysis duration (0 when absent).
func (c *Composition) Duration() float64 {
	if c == nil || c.a == nil {
		return 0
	}
	return c.duration
}

// BeatCount returns the measured beat count (0 when unavailable).
func (c *Composition) BeatCount() int {
	return c.beatCount
}

func lerpF32(a, b float32, t float64) float64 {
	return lerp(float64(a), float64(b), t)
}

func lerp(a, b, t float64) float64 {
	return a + (b-a)*t
}
