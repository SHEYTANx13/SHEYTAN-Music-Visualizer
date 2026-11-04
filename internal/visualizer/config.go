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
const (
	ModeBars      Mode = "bars"      // Spectrum Bars
	ModeRadial    Mode = "radial"    // Radial Spectrum
	ModeWaveform  Mode = "waveform"  // Waveform / Oscilloscope
	ModeParticles Mode = "particles" // Particle Field
)

// AllModes lists the modes in UI order.
var AllModes = []Mode{ModeBars, ModeRadial, ModeWaveform, ModeParticles}

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

	ShowTitle     bool   `json:"showTitle"`
	TitleText     string `json:"titleText"`
	SubtitleText  string `json:"subtitleText"`
	ShowWatermark bool   `json:"showWatermark"`
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
	}
}
