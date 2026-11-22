package visualizer

import "github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/songdna"

// Palette is a brand-constrained color ramp. All palettes live inside
// the black/red hellish identity; red and ember dominate, warm white
// is reserved for peaks and text.
type Palette struct {
	Name   string
	Back   [3]float64 // background (obsidian)
	Low    [3]float64 // low energy color (deep crimson)
	Mid    [3]float64 // mid energy color (infernal red)
	High   [3]float64 // high energy color (ember)
	Peak   [3]float64 // peak/impact color (hot ember / warm white blend)
	Accent [3]float64 // line/accent color
}

// Palettes are the four brand palettes.
var Palettes = []Palette{
	{
		Name: "Hellfire",
		Back: rgb(0x080808), Low: rgb(0x9F0B18), Mid: rgb(0xD51D2A),
		High: rgb(0xFF453A), Peak: rgb(0xFF7043), Accent: rgb(0xF2EEEE),
	},
	{
		Name: "Crimson",
		Back: rgb(0x0A0607), Low: rgb(0x5C0A12), Mid: rgb(0xB3121F),
		High: rgb(0xE6402F), Peak: rgb(0xFFB4A0), Accent: rgb(0xE8DEDE),
	},
	{
		Name: "Obsidian",
		Back: rgb(0x050505), Low: rgb(0x3A0D12), Mid: rgb(0x8C1120),
		High: rgb(0xD5312C), Peak: rgb(0xF2EEEE), Accent: rgb(0xC9B8B8),
	},
	{
		Name: "Ember",
		Back: rgb(0x0B0705), Low: rgb(0x6E1208), Mid: rgb(0xC23A12),
		High: rgb(0xFF7043), Peak: rgb(0xFFD3A3), Accent: rgb(0xF2EEEE),
	},
}

func rgb(c uint32) [3]float64 {
	return [3]float64{
		float64((c>>16)&0xFF) / 255,
		float64((c>>8)&0xFF) / 255,
		float64(c&0xFF) / 255,
	}
}

// AudioState is the immutable, interpolated audio feature snapshot at
// one media timestamp. Renderers consume it read-only.
type AudioState struct {
	Time      float64   `json:"time"` // media seconds
	RMS       float64   `json:"rms"`
	Bass      float64   `json:"bass"`
	Mid       float64   `json:"mid"`
	Treble    float64   `json:"treble"`
	Centroid  float64   `json:"centroid"`
	Flux      float64   `json:"flux"`
	Onset     bool      `json:"onset"`
	BeatPhase float64   `json:"beatPhase"` // 0..1 inside beat (0 when unreliable)
	Bins      []float64 `json:"bins"`      // display spectrum 0..1
	// WaveWindow is the waveform envelope window around Time for the
	// waveform mode (may be empty for other modes).
	WaveWindow []float32 `json:"waveWindow,omitempty"`

	// ---- v3 composition state (priority-D): filled by the studio from
	// the Composition time base when the v3 analysis is available. All
	// fields carry their own availability/confidence semantics; legacy
	// producers leave them zero and the renderers gate on HasComposition.
	HasComposition bool `json:"hasComposition,omitempty"`
	// BandEnvelope / BandShare are the measured 9-band calibrated
	// envelopes (indexes match dsp.BandNames: 0 sub-bass .. 8 brilliance).
	BandEnvelope [9]float64 `json:"bandEnvelope,omitempty"`
	BandShare    [9]float64 `json:"bandShare,omitempty"`
	// Discrete transient evidence (NOT interpolated): strength 0..1 of
	// the nearest measured transient within the lookup window,
	// TransientBand = dominant band or -1 (broadband).
	Transient     float64 `json:"transient,omitempty"`
	TransientBand int     `json:"transientBand,omitempty"`
	// DownbeatSoon: a measured downbeat lands within the next half beat.
	DownbeatSoon bool `json:"downbeatSoon,omitempty"`
	// Harmony (discrete segments; no interpolation across boundaries).
	ChordRoot       string  `json:"chordRoot,omitempty"`
	ChordQuality    string  `json:"chordQuality,omitempty"`
	ChordConfidence float64 `json:"chordConfidence,omitempty"`
	KeyConfidence   float64 `json:"keyConfidence,omitempty"`
	Tension         float64 `json:"tension,omitempty"`
	// Vocal-flow state (acoustic estimates with confidence).
	VocalLikelihood   float64 `json:"vocalLikelihood,omitempty"`
	VocalDensity      float64 `json:"vocalDensity,omitempty"`
	VocalPhraseActive bool    `json:"vocalPhraseActive,omitempty"`
	// Structure: section covering the frame (-1 unknown) + label.
	SectionIndex int    `json:"sectionIndex,omitempty"`
	SectionLabel string `json:"sectionLabel,omitempty"`
	// Stereo + dynamics + availability.
	StereoWidth   float64 `json:"stereoWidth,omitempty"`
	Correlation   float64 `json:"correlation,omitempty"`
	MomentaryLUFS float64 `json:"momentaryLUFS,omitempty"`
	Confidence    float64 `json:"confidence,omitempty"`
}

// FrameInput is everything one deterministic render needs.
type FrameInput struct {
	FrameIndex int
	Time       float64 // media seconds; authoritative for animation
	Width      int
	Height     int
	Config     Config
	DNA        *songdna.DNA // may be nil (defaults apply)
	Audio      AudioState
}

// NumVizBins is the fixed perceptual bin count renderers consume.
const NumVizBins = 64

// InterpolateBins resamples a full analysis spectrum (log-spaced) into
// NumVizBins perceptual bins with tilt applied. Deterministic.
func InterpolateBins(full []float64, tilt float64) []float64 {
	out := make([]float64, NumVizBins)
	n := len(full)
	if n == 0 {
		return out
	}
	for b := 0; b < NumVizBins; b++ {
		// log-spaced band edges over the spectrum
		lo := binEdge(b, n)
		hi := binEdge(b+1, n)
		if hi <= lo {
			hi = lo + 1
		}
		var sum float64
		for i := lo; i < hi && i < n; i++ {
			sum += full[i]
		}
		v := sum / float64(hi-lo)
		// perceptual tilt: boost highs, cut lows (or inverse)
		tnorm := float64(b) / (NumVizBins - 1)
		v *= 1 + tilt*(tnorm*2-1)
		out[b] = clamp01(v)
	}
	return out
}

func binEdge(b, n int) int {
	// log map from viz-bin edge to analysis-bin index
	t := float64(b) / NumVizBins
	return int(t * t * float64(n-1))
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
