// Package project owns the versioned JSON project/preset format:
// safe schema validation, atomic save (temp + rename), lenient loading
// of unknown future fields, media relinking, and the built-in named
// presets.
//
// Projects reference source media by path and fingerprint — never the
// audio bytes themselves.
package project

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CurrentVersion is the project schema version written by this build.
const CurrentVersion = 1

// MinSupportedVersion is the oldest schema this build loads.
const MinSupportedVersion = 1

// Errors surfaced to the UI.
var (
	ErrCorrupt      = errors.New("project: file is corrupt or not a project")
	ErrTooNew       = errors.New("project: file was written by a newer version")
	ErrMediaMissing = errors.New("project: source media file is missing")
)

// Project is the full on-disk project document.
type Project struct {
	Version int `json:"version"`

	Name string `json:"name"`

	// Media: source reference + identity. RelPath is preferred for
	// portability; AbsPath is the fallback used when the relative
	// path does not resolve.
	MediaRef MediaRef `json:"media"`

	// Visualizer settings (visualizer.Config shape, stored inline to
	// keep the schema single-document).
	Visualizer VisualizerSettings `json:"visualizer"`

	// SongDNA user variation seed (0 = derived from content).
	SongDNASeed uint64 `json:"songDnaSeed,omitempty"`

	// Export defaults.
	Export ExportDefaults `json:"export"`

	// TimelineMarkers are user-placed chapter marks (seconds).
	TimelineMarkers []float64 `json:"timelineMarkers,omitempty"`

	// TempoOverrideBPM is the manual tempo correction (roadmap
	// Phase 2 #3). 0 = use the measured BPM; 30..300 = user value
	// that bypasses the tempo-confidence gate (user decision).
	TempoOverrideBPM float64 `json:"tempoOverrideBpm,omitempty"`
	// SyncOffsetSeconds is the manual audio/visual sync correction:
	// analysis features are read at (t - SyncOffsetSeconds).
	// Clamped to ±5 s.
	SyncOffsetSeconds float64 `json:"syncOffsetSeconds,omitempty"`
}

// MediaRef points at the source track.
type MediaRef struct {
	FileName    string  `json:"fileName"`          // base name (display)
	AbsPath     string  `json:"absPath,omitempty"` // absolute path as imported
	RelPath     string  `json:"relPath,omitempty"` // relative to the project file when possible
	Fingerprint string  `json:"fingerprint"`       // content fingerprint
	Container   string  `json:"container"`
	Duration    float64 `json:"duration"`
}

// VisualizerSettings mirrors visualizer.Config fields (kept local to
// avoid a package cycle at the JSON schema level; validated against
// the same ranges).
type VisualizerSettings struct {
	Mode            string  `json:"mode"`
	Sensitivity     float64 `json:"sensitivity"`
	Smoothing       float64 `json:"smoothing"`
	Glow            float64 `json:"glow"`
	Palette         int     `json:"palette"`
	ParticleDensity float64 `json:"particleDensity"`
	SpectrumTilt    float64 `json:"spectrumTilt"`
	PulseResponse   float64 `json:"pulseResponse"`
	MotionStyle     int     `json:"motionStyle"`
	VariationSeed   uint64  `json:"variationSeed"`

	ShowTitle     bool   `json:"showTitle"`
	TitleText     string `json:"titleText"`
	SubtitleText  string `json:"subtitleText"`
	ShowWatermark bool   `json:"showWatermark"`

	// Layered composition (mirrors visualizer.Config).
	BackgroundLayer     string  `json:"backgroundLayer,omitempty"`
	BackgroundIntensity float64 `json:"backgroundIntensity,omitempty"`
}

// ExportDefaults carries the remembered export settings.
type ExportDefaults struct {
	Width         int    `json:"width"`
	Height        int    `json:"height"`
	FPS           int    `json:"fps"`
	QualityPreset string `json:"qualityPreset"` // high | balanced | compat
	Encoder       string `json:"encoder"`       // auto | libx264 | h264_nvenc | ...
}

// Validate checks the document and clamps values into range. It
// returns an error for structurally invalid projects (bad version,
// unknown mode, non-finite numbers).
func (p *Project) Validate() error {
	if p.Version < MinSupportedVersion {
		return fmt.Errorf("%w: version %d", ErrCorrupt, p.Version)
	}
	if p.Version > CurrentVersion {
		return fmt.Errorf("%w: version %d > supported %d", ErrTooNew, p.Version, CurrentVersion)
	}
	if p.MediaRef.Fingerprint == "" {
		return fmt.Errorf("%w: missing media fingerprint", ErrCorrupt)
	}
	if !validMode(p.Visualizer.Mode) {
		return fmt.Errorf("%w: unknown visualizer mode %q", ErrCorrupt, p.Visualizer.Mode)
	}
	p.Visualizer.Sensitivity = clamp(p.Visualizer.Sensitivity, 0.2, 3, 1)
	p.Visualizer.Smoothing = clamp(p.Visualizer.Smoothing, 0, 0.9, 0.3)
	p.Visualizer.Glow = clamp(p.Visualizer.Glow, 0, 1.5, 0.7)
	if p.Visualizer.Palette < 0 || p.Visualizer.Palette > 3 {
		p.Visualizer.Palette = 0
	}
	p.Visualizer.ParticleDensity = clamp(p.Visualizer.ParticleDensity, 0, 1, 0.6)
	p.Visualizer.SpectrumTilt = clamp(p.Visualizer.SpectrumTilt, -0.3, 0.3, 0)
	p.Visualizer.PulseResponse = clamp(p.Visualizer.PulseResponse, 0.5, 1.8, 1)
	if p.Visualizer.MotionStyle < 0 || p.Visualizer.MotionStyle > 3 {
		p.Visualizer.MotionStyle = 0
	}
	// export defaults
	p.Export.Width = clampInt(p.Export.Width, 16, 7680, 1920)
	p.Export.Height = clampInt(p.Export.Height, 16, 4320, 1080)
	switch p.Export.FPS {
	case 24, 30, 60:
	default:
		p.Export.FPS = 30
	}
	switch strings.ToLower(p.Export.QualityPreset) {
	case "high", "balanced", "compat":
		p.Export.QualityPreset = strings.ToLower(p.Export.QualityPreset)
	default:
		p.Export.QualityPreset = "high"
	}
	if p.Export.Encoder == "" {
		p.Export.Encoder = "auto"
	}
	// Layered composition mirror fields.
	switch p.Visualizer.BackgroundLayer {
	case "", "none", "bars", "radial", "waveform", "particles":
	default:
		p.Visualizer.BackgroundLayer = "none"
	}
	p.Visualizer.BackgroundIntensity = clamp(p.Visualizer.BackgroundIntensity, 0, 0.6, 0)
	if p.Visualizer.BackgroundLayer == "none" || p.Visualizer.BackgroundLayer == "" {
		p.Visualizer.BackgroundIntensity = 0
	}
	// Manual tempo/offset correction.
	if p.TempoOverrideBPM != 0 {
		p.TempoOverrideBPM = clamp(p.TempoOverrideBPM, 30, 300, 0)
	}
	p.SyncOffsetSeconds = clamp(p.SyncOffsetSeconds, -5, 5, 0)
	return nil
}

func validMode(m string) bool {
	switch m {
	case "bars", "radial", "waveform", "particles":
		return true
	}
	return false
}

func clamp(v, lo, hi, def float64) float64 {
	if v != v { // NaN
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

func clampInt(v, lo, hi, def int) int {
	if v < lo || v > hi {
		return def
	}
	return v
}

// Save atomically writes the project (temp file + rename in the same
// directory).
func Save(p *Project, path string) error {
	if err := p.Validate(); err != nil {
		return fmt.Errorf("project: refusing to save invalid project: %w", err)
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("project: marshal: %w", err)
	}
	b = append(b, '\n')
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".sheytan-project-*.tmp")
	if err != nil {
		return fmt.Errorf("project: temp file: %w", err)
	}
	name := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(name)
		return fmt.Errorf("project: write: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(name)
		return fmt.Errorf("project: sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return fmt.Errorf("project: close: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return fmt.Errorf("project: rename: %w", err)
	}
	return nil
}

// Load reads and validates a project file. Unknown future fields are
// ignored by json.Unmarshal; version gating happens in Validate.
func Load(path string) (*Project, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var p Project
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return &p, nil
}

// ResolveMedia returns the best available path for the media reference
// relative to the project file location: project dir + RelPath, then
// AbsPath. It verifies existence; relink (returned error) is the
// caller's UI action.
func (p *Project) ResolveMedia(projectDir string) (string, error) {
	candidates := make([]string, 0, 3)
	if p.MediaRef.RelPath != "" {
		if filepath.IsAbs(p.MediaRef.RelPath) {
			candidates = append(candidates, p.MediaRef.RelPath)
		} else {
			candidates = append(candidates, filepath.Join(projectDir, p.MediaRef.RelPath))
		}
	}
	if p.MediaRef.AbsPath != "" {
		candidates = append(candidates, p.MediaRef.AbsPath)
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c, nil
		}
	}
	return "", ErrMediaMissing
}

// NewDefault creates a validated default project for a track.
func NewDefault(name, mediaPath, fingerprint, container string, duration float64) *Project {
	p := &Project{
		Version: CurrentVersion,
		Name:    name,
		MediaRef: MediaRef{
			FileName:    filepath.Base(mediaPath),
			AbsPath:     mediaPath,
			Fingerprint: fingerprint,
			Container:   container,
			Duration:    duration,
		},
		Visualizer: VisualizerSettings{
			Mode: "bars", Sensitivity: 1.0, Smoothing: 0.3, Glow: 0.7,
			Palette: 0, ParticleDensity: 0.6, PulseResponse: 1.2,
			ShowTitle: true, ShowWatermark: true,
		},
		Export: ExportDefaults{Width: 1920, Height: 1080, FPS: 30, QualityPreset: "high", Encoder: "auto"},
	}
	return p
}
