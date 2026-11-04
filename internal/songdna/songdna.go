// Package songdna derives a deterministic per-track visual identity
// ("Song DNA") from measured audio features and a stable source
// fingerprint.
//
// Rules:
//   - The same track (content) + same settings always produce the same
//     DNA; nothing depends on wall-clock time, process state or global
//     randomness.
//   - The DNA only picks defaults within the black/red brand palette
//     and tunes motion/weighting parameters; it never invents genres,
//     artists or moods beyond what the measured features support.
//   - A user-controlled variation seed (0 = derived default) shifts
//     the artistic choices without breaking determinism.
package songdna

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/dsp"
)

// Version bumps when DNA derivation changes; it is stored in projects.
const Version = 1

// DNA is the per-track visual identity.
type DNA struct {
	Version       int     `json:"version"`
	Fingerprint   string  `json:"fingerprint"`
	Seed          uint64  `json:"seed"` // effective seed (user seed or derived)
	UserSeed      uint64  `json:"userSeed,omitempty"`
	Brightness    float64 `json:"brightness"`    // 0..1 from mean centroid
	Punchiness    float64 `json:"punchiness"`    // 0..1 from bass transients
	Warmth        float64 `json:"warmth"`        // 0..1 bass vs treble balance
	RhythmicDrive float64 `json:"rhythmicDrive"` // 0..1 onset density & tempo confidence
	Loudness      float64 `json:"loudness"`      // 0..1 mean RMS

	// Artistic defaults chosen deterministically from the features.
	DefaultMode     string  `json:"defaultMode"`     // visualizer mode id
	PaletteIndex    int     `json:"paletteIndex"`    // within brand palettes
	MotionStyle     int     `json:"motionStyle"`     // 0..3 motion character
	PulseResponse   float64 `json:"pulseResponse"`   // 0.6..1.6 beat reactivity
	Smoothing       float64 `json:"smoothing"`       // 0.15..0.5 suggested smoothing
	ParticleDensity float64 `json:"particleDensity"` // 0.2..1
	GlowIntensity   float64 `json:"glowIntensity"`   // 0.5..1.4
	SpectrumTilt    float64 `json:"spectrumTilt"`    // -0.3..0.3 perceptual weighting tilt
	Descriptor      string  `json:"descriptor"`      // measured-feature description
}

// Derive computes the DNA for a track fingerprint and its measured
// analysis result. userSeed 0 means "derive from content".
func Derive(fingerprint string, analysis *dsp.Result, userSeed uint64) *DNA {
	d := &DNA{
		Version:     Version,
		Fingerprint: fingerprint,
		UserSeed:    userSeed,
	}
	// Effective seed: content-derived, or the user's explicit seed.
	var seedRaw [32]byte
	h := sha256.New()
	h.Write([]byte("sheytan-songdna-v1\x00"))
	h.Write([]byte(fingerprint))
	copy(seedRaw[:], h.Sum(nil))
	contentSeed := binary.BigEndian.Uint64(seedRaw[:8])
	d.Seed = contentSeed
	if userSeed != 0 {
		d.Seed = userSeed
	}

	// Measured features normalized to 0..1.
	d.Brightness = clamp01(analysis.MeanCentroid / 4000.0)
	d.Warmth = clampShare(analysis.BassEnergy, analysis.TrebleEnergy)
	d.Loudness = clamp01(analysis.MeanRMS * 6)
	d.RhythmicDrive = clamp01(0.5*analysis.TempoConfidence +
		0.5*clamp01(float64(analysis.OnsetCount)/300.0))
	d.Punchiness = clamp01(analysis.BassEnergy * 2.2)

	rng := newRNG(d.Seed)

	// Default mode: brightness/rhythm suggest a mode; deterministic tiebreak.
	switch {
	case d.RhythmicDrive > 0.62 && rng.Float01() < 0.5:
		d.DefaultMode = "radial"
	case d.Brightness > 0.45:
		d.DefaultMode = "bars"
	default:
		if rng.Float01() < 0.35 {
			d.DefaultMode = "particles"
		} else {
			d.DefaultMode = "waveform"
		}
	}
	d.PaletteIndex = rng.Intn(4) // within brand palettes (visualizer pkg maps)
	d.MotionStyle = rng.Intn(4)
	d.PulseResponse = 0.6 + 1.0*clamp01(d.RhythmicDrive*0.7+d.Punchiness*0.5)
	d.Smoothing = 0.15 + 0.35*clamp01(1-d.RhythmicDrive)
	d.ParticleDensity = 0.2 + 0.8*clamp01(d.Punchiness*0.6+d.Loudness*0.5)
	d.GlowIntensity = 0.5 + 0.9*clamp01(d.Brightness*0.5+d.Loudness*0.7)
	d.SpectrumTilt = -0.3 + 0.6*clamp01(d.Brightness)

	d.Descriptor = fmt.Sprintf(
		"brightness %d%% · warmth %d%% · punch %d%% · drive %d%% · loudness %d%%",
		pct(d.Brightness), pct(d.Warmth), pct(d.Punchiness), pct(d.RhythmicDrive), pct(d.Loudness))
	return d
}

func pct(v float64) int { return int(math.Round(clamp01(v) * 100)) }

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// clampShare maps bass/treble energy shares to a warmth score.
func clampShare(bass, treble float64) float64 {
	total := bass + treble
	if total <= 1e-9 {
		return 0.5
	}
	return clamp01(bass / total)
}

// rng is a tiny deterministic SplitMix64 PRNG used only for artistic
// tiebreaks; the same seed always yields the same sequence.
type rng struct {
	state uint64
}

func newRNG(seed uint64) *rng { return &rng{state: seed} }

func (r *rng) next() uint64 {
	r.state += 0x9E3779B97F4A7C15
	z := r.state
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

func (r *rng) Float01() float64 { return float64(r.next()>>11) / float64(1<<53) }
func (r *rng) Intn(n int) int   { return int(r.next() % uint64(n)) }
