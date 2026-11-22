// Package visualizer defines the shared visualizer contract: modes,
// configuration, palettes, presets and the per-frame input/state that
// both the live preview and the offline export consume.
//
// The same visualizer logic, configuration and frame state drive the
// Fyne preview and the MP4 export frames; only the backend (CPU or
// GPU) differs. No renderer may read the wall clock — all animation
// derives from media time and deterministic state.
package visualizer

// Mode identifies a visualizer mode.
type Mode string

// The available modes. IDs are stable and used in projects/presets.
// Run 43612608237 added four genuinely distinct renderers (spectrogram
// waterfall, geometric pulse tunnel, stereo wave ribbons, frequency
// bloom); the original four remain stable and unchanged in behaviour.
const (
	ModeBars        Mode = "bars"        // Spectrum Bars
	ModeRadial      Mode = "radial"      // Radial Spectrum
	ModeWaveform    Mode = "waveform"    // Waveform / Oscilloscope
	ModeParticles   Mode = "particles"   // Particle Field
	ModeSpectrogram Mode = "spectrogram" // Spectrogram Waterfall
	ModeTunnel      Mode = "tunnel"      // Geometric Pulse Tunnel
	ModeRibbons     Mode = "ribbons"     // Stereo Wave Ribbons
	ModeBloom       Mode = "bloom"       // Frequency Bloom
)

// AllModes lists the modes in UI order.
var AllModes = []Mode{
	ModeBars, ModeRadial, ModeWaveform, ModeParticles,
	ModeSpectrogram, ModeTunnel, ModeRibbons, ModeBloom,
}

// ModeCategory groups modes for the collection UI. Every mode belongs
// to exactly one category; the collection integrity tests assert the
// mapping is total and stable.
type ModeCategory string

// Collection categories (run 43612608237 visualizer library UX).
const (
	CategorySpectrum    ModeCategory = "Spectrum"
	CategoryWaveform    ModeCategory = "Waveform"
	CategoryRadial      ModeCategory = "Radial"
	CategorySpatial     ModeCategory = "Spatial"
	CategorySpectrogram ModeCategory = "Spectrogram"
	CategoryPerformance ModeCategory = "Minimal/Performance"
)

// ModeCategoryOf returns the collection category for a mode ("" when
// the mode is unknown; tests treat an empty category as an integrity
// failure).
func ModeCategoryOf(m Mode) ModeCategory {
	switch m {
	case ModeBars:
		return CategorySpectrum
	case ModeSpectrogram:
		return CategorySpectrogram
	case ModeWaveform, ModeRibbons:
		return CategoryWaveform
	case ModeRadial, ModeBloom:
		return CategoryRadial
	case ModeParticles, ModeTunnel:
		return CategorySpatial
	}
	return ""
}

// ModeNone disables the background composition layer.
const ModeNone Mode = "none"

// MaxBackgroundIntensity bounds the background layer alpha factor.
// The background must stay a backdrop: the primary mode always reads
// clearly on top of it.
const MaxBackgroundIntensity = 0.6

// DisplayName returns the human name of a mode.
func (m Mode) DisplayName() string {
	switch m {
	case ModeBars:
		return "Spectrum Bars"
	case ModeRadial:
		return "Radial Spectrum"
	case ModeWaveform:
		return "Waveform"
	case ModeParticles:
		return "Particle Field"
	case ModeSpectrogram:
		return "Spectrogram Waterfall"
	case ModeTunnel:
		return "Pulse Tunnel"
	case ModeRibbons:
		return "Stereo Wave Ribbons"
	case ModeBloom:
		return "Frequency Bloom"
	default:
		return string(m)
	}
}

// ValidMode reports whether m is one of the known modes.
func ValidMode(m Mode) bool {
	for _, v := range AllModes {
		if v == m {
			return true
		}
	}
	return false
}

// Config is the full user-editable visualizer configuration. It is
// serialized into projects and presets; unknown fields from future
// versions are ignored on load (project package uses lenient decode).
type Config struct {
	Mode            Mode    `json:"mode"`
	Sensitivity     float64 `json:"sensitivity"`     // 0.2..3, gain on audio metrics
	Smoothing       float64 `json:"smoothing"`       // 0..0.9 attack/release smoothing
	Glow            float64 `json:"glow"`            // 0..1.5 glow amount
	Palette         int     `json:"palette"`         // 0..3, brand palettes
	ParticleDensity float64 `json:"particleDensity"` // 0..1 (particles mode)
	SpectrumTilt    float64 `json:"spectrumTilt"`    // -0.3..0.3 perceptual tilt
	PulseResponse   float64 `json:"pulseResponse"`   // 0.5..1.8 beat impact
	MotionStyle     int     `json:"motionStyle"`     // 0..3 artistic variation
	VariationSeed   uint64  `json:"variationSeed"`   // user-controlled variation

	// Layered composition (roadmap Phase 2 #6): an optional dimmed
	// secondary mode rendered BEHIND the primary mode by the CPU
	// renderer. "none" disables the layer. Preview and export share
	// the same field through the same Config.
	BackgroundLayer     Mode    `json:"backgroundLayer"`
	BackgroundIntensity float64 `json:"backgroundIntensity"` // 0..0.6 when a layer is set

	ShowTitle     bool   `json:"showTitle"`
	TitleText     string `json:"titleText"`
	SubtitleText  string `json:"subtitleText"`
	ShowWatermark bool   `json:"showWatermark"`

	// ---- v3 composition response (priority-D §7.4). The UI exposes a
	// manageable set of grouped knobs, never the raw DSP internals.
	BassImpact          float64 `json:"bassImpact"`          // 0..2 sub/bass geometry weight
	TransientIntensity  float64 `json:"transientIntensity"`  // 0..2 kick/snare/hat reaction
	VocalSensitivity    float64 `json:"vocalSensitivity"`    // 0..2 vocal-reactive layer gain
	StereoWidthGain     float64 `json:"stereoWidthGain"`     // 0..1 spatial spread
	SectionIntensity    float64 `json:"sectionIntensity"`    // 0..1 section-transition reaction
	BeatEmphasis        float64 `json:"beatEmphasis"`        // 0..1.5 beat/downbeat pulse
	ChordReactivity     float64 `json:"chordReactivity"`     // 0..1 harmonic palette rotation
	ConfidenceThreshold float64 `json:"confidenceThreshold"` // 0..0.9; below it features gate off
	// Layer enablement (default-on where measured evidence exists).
	VocalLayerEnabled bool `json:"vocalLayerEnabled"`
	RoleLayersEnabled bool `json:"roleLayersEnabled"`
}

// Clamp normalizes every field into its valid range.
func (c *Config) Clamp() {
	c.Mode = normalizeMode(c.Mode)
	c.Sensitivity = clampRange(c.Sensitivity, 0.2, 3, 1.0)
	c.Smoothing = clampRange(c.Smoothing, 0, 0.9, 0.3)
	c.Glow = clampRange(c.Glow, 0, 1.5, 0.7)
	if c.Palette < 0 || c.Palette > 3 {
		c.Palette = 0
	}
	c.ParticleDensity = clampRange(c.ParticleDensity, 0, 1, 0.6)
	c.SpectrumTilt = clampRange(c.SpectrumTilt, -0.3, 0.3, 0)
	c.PulseResponse = clampRange(c.PulseResponse, 0.5, 1.8, 1.0)
	if c.MotionStyle < 0 || c.MotionStyle > 3 {
		c.MotionStyle = 0
	}
	// v3 composition response knobs.
	c.BassImpact = clampRange(c.BassImpact, 0, 2, 1.0)
	c.TransientIntensity = clampRange(c.TransientIntensity, 0, 2, 1.0)
	c.VocalSensitivity = clampRange(c.VocalSensitivity, 0, 2, 1.0)
	c.StereoWidthGain = clampRange(c.StereoWidthGain, 0, 1, 0.5)
	c.SectionIntensity = clampRange(c.SectionIntensity, 0, 1, 0.6)
	c.BeatEmphasis = clampRange(c.BeatEmphasis, 0, 1.5, 0.8)
	c.ChordReactivity = clampRange(c.ChordReactivity, 0, 1, 0.4)
	c.ConfidenceThreshold = clampRange(c.ConfidenceThreshold, 0, 0.9, 0.3)
	// Layered composition: an empty/unknown layer is "none"; a
	// background layer equal to the primary mode is pointless and
	// normalized to "none"; intensity is only meaningful when a
	// layer is active.
	if !ValidMode(c.BackgroundLayer) || c.BackgroundLayer == c.Mode {
		c.BackgroundLayer = ModeNone
	}
	if c.BackgroundLayer == ModeNone {
		c.BackgroundIntensity = 0
	} else {
		c.BackgroundIntensity = clampRange(c.BackgroundIntensity, 0, MaxBackgroundIntensity, 0.35)
	}
}

func normalizeMode(m Mode) Mode {
	if ValidMode(m) {
		return m
	}
	return ModeBars
}

func clampRange(v, lo, hi, def float64) float64 {
	if v != v { // NaN → default
		return def
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// Preset is a named, serialized configuration.
type Preset struct {
	Name   string `json:"name"`
	Config Config `json:"config"`
}

// Presets returns the built-in presets. They are real serialized
// settings, not hard-coded screenshots.
func Presets() []Preset {
	return []Preset{
		{
			Name: "Hellfire Pulse",
			Config: Config{
				Mode: ModeBars, Sensitivity: 1.3, Smoothing: 0.22, Glow: 1.1,
				Palette: 0, ParticleDensity: 0.7, SpectrumTilt: -0.1,
				PulseResponse: 1.6, MotionStyle: 1, ShowTitle: true, ShowWatermark: true,
			},
		},
		{
			Name: "Crimson Orbit",
			Config: Config{
				Mode: ModeRadial, Sensitivity: 1.0, Smoothing: 0.3, Glow: 0.9,
				Palette: 1, ParticleDensity: 0.4, SpectrumTilt: 0,
				PulseResponse: 1.3, MotionStyle: 0, ShowTitle: true, ShowWatermark: true,
			},
		},
		{
			Name: "Obsidian Wave",
			Config: Config{
				Mode: ModeWaveform, Sensitivity: 0.9, Smoothing: 0.42, Glow: 0.55,
				Palette: 2, ParticleDensity: 0.25, SpectrumTilt: 0.1,
				PulseResponse: 0.9, MotionStyle: 2, ShowTitle: true, ShowWatermark: false,
			},
		},
		{
			Name: "Ember Storm",
			Config: Config{
				Mode: ModeParticles, Sensitivity: 1.15, Smoothing: 0.25, Glow: 1.0,
				Palette: 3, ParticleDensity: 0.85, SpectrumTilt: 0,
				PulseResponse: 1.5, MotionStyle: 3, ShowTitle: true, ShowWatermark: true,
			},
		},
		// Run 43612608237: one preset per new mode keeps the
		// collection discoverable (the registry test pins that
		// every shipped mode is demonstrated by at least one
		// preset) and gives each new renderer a tuned entry
		// point instead of raw defaults.
		{
			Name: "Ashfall Waterfall",
			Config: Config{
				Mode: ModeSpectrogram, Sensitivity: 1.2, Smoothing: 0.2, Glow: 0.6,
				Palette: 2, ParticleDensity: 0.3, SpectrumTilt: -0.1,
				PulseResponse: 0.8, MotionStyle: 0, ShowTitle: true, ShowWatermark: true,
			},
		},
		{
			Name: "Obsidian Descent",
			Config: Config{
				Mode: ModeTunnel, Sensitivity: 1.25, Smoothing: 0.28, Glow: 1.1,
				Palette: 0, ParticleDensity: 0.5, SpectrumTilt: 0,
				PulseResponse: 1.6, MotionStyle: 1, ShowTitle: true, ShowWatermark: true,
			},
		},
		{
			Name: "Twin Ribbons",
			Config: Config{
				Mode: ModeRibbons, Sensitivity: 1.0, Smoothing: 0.35, Glow: 0.7,
				Palette: 1, ParticleDensity: 0.25, SpectrumTilt: 0.1,
				PulseResponse: 1.0, MotionStyle: 2, ShowTitle: true, ShowWatermark: false,
			},
		},
		{
			Name: "Aurora Bloom",
			Config: Config{
				Mode: ModeBloom, Sensitivity: 1.1, Smoothing: 0.4, Glow: 1.2,
				Palette: 3, ParticleDensity: 0.4, SpectrumTilt: 0,
				PulseResponse: 1.2, MotionStyle: 0, ShowTitle: true, ShowWatermark: true,
			},
		},
	}
}
