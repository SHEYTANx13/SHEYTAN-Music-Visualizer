// Package studio is the application service layer: it owns media
// import, the canonical cache, analysis, playback, the preview
// renderer, project persistence and export jobs, and exposes an
// event-driven API for the Fyne UI. It contains no Fyne types.
//
// Concurrency contract: all mutating methods are safe for concurrent
// use; long operations run in goroutines owned by this package with
// context cancellation, and emit Events. The UI bridges events onto
// the Fyne thread itself.
package studio

import (
	"context"
	"errors"
	"fmt"
	"image"
	"math"
	"path/filepath"
	"sync"
	"time"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/apppaths"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/audio"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/dsp"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/export"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/playback"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/project"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/render"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/songdna"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/visualizer"
)

// EventKind identifies a studio event for the UI.
type EventKind int

const (
	EventImportStarted EventKind = iota
	EventImportProgress
	EventImportDone
	EventImportFailed
	EventAnalysisStarted
	EventAnalysisProgress
	EventAnalysisDone
	EventAnalysisFailed
	EventPlaybackChanged
	EventExportProgress
	EventExportDone
	EventExportFailed
	EventProjectSaved
	EventProjectLoaded
	EventProjectFailed
)

// Event is a UI-bound notification.
type Event struct {
	Kind    EventKind
	Message string
	// Progress 0..100 when applicable.
	Progress float64
	// Payload carries event-specific data (type-asserted by the UI).
	Payload any
}

// Studio is the application service. One instance per process.
type Studio struct {
	mu sync.RWMutex

	cacheDir string
	cache    *audio.Cache

	track      *audio.ImportedTrack
	analysis   *dsp.Result
	dna        *songdna.DNA
	vizConfig  visualizer.Config
	transport  *playback.Transport
	output     playback.Output
	deviceReal bool

	analysisCancel context.CancelFunc
	exportJob      *export.Job
	projectPath    string
	projectDoc     *project.Project
	lastSourcePath string
	userCustomized bool
	importing      bool
	importCancel   context.CancelFunc

	// Manual tempo/offset correction (roadmap Phase 2 #3). The
	// override bypasses the measured-tempo confidence gate because
	// it is an explicit user decision; 0 = measured BPM.
	tempoOverrideBPM  float64
	syncOffsetSeconds float64

	subscriber func(Event)
}

// New creates the studio: cache dirs, decoders, device output.
func New() (*Studio, error) {
	audio.RegisterAll()
	audio.RegisterFFmpegOptional() // optional M4A/AAC family; no-op when FFmpeg is absent
	dir, err := DataDir()
	if err != nil {
		return nil, err
	}
	cacheDir := filepath.Join(dir, "pcm-cache")
	cache, err := audio.NewCache(cacheDir)
	if err != nil {
		return nil, err
	}
	s := &Studio{cacheDir: cacheDir, cache: cache}
	// device: real when possible, silent otherwise (honest state)
	out, err := playback.NewOtoOutput()
	if err == nil {
		s.output = out
		s.deviceReal = true
	} else {
		s.output = playback.NewSilentOutput()
	}
	s.vizConfig = defaultConfig()
	return s, nil
}

// DataDir resolves the app-owned cache directory through the SINGLE
// apppaths authority (portable data/, installed per-user location,
// visible fallback). The explicit SHEYTAN_DATA_DIR override remains
// honored (it selects the whole data root, exactly as the resolver
// defines it) so tests and CI keep working.
func DataDir() (string, error) {
	lay := apppaths.Resolve(apppaths.OSEnv())
	if err := apppaths.Bootstrap(lay); err != nil {
		return "", err
	}
	return lay.Cache, nil
}

// Subscribe registers the event sink (UI bridge).
func (s *Studio) Subscribe(fn func(Event)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subscriber = fn
}

func (s *Studio) emit(ev Event) {
	s.mu.RLock()
	fn := s.subscriber
	s.mu.RUnlock()
	if fn != nil {
		fn(ev)
	}
}

func defaultConfig() visualizer.Config {
	return visualizer.Config{
		Mode: visualizer.ModeBars, Sensitivity: 1.0, Smoothing: 0.3,
		Glow: 0.7, Palette: 0, ParticleDensity: 0.6, PulseResponse: 1.2,
		ShowTitle: true, ShowWatermark: true,
	}
}

// Track accessors ----------------------------------------------------

// Track returns the current imported track (nil when none).
func (s *Studio) Track() *audio.ImportedTrack {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.track
}

// Analysis returns the current analysis (nil when pending).
func (s *Studio) Analysis() *dsp.Result {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.analysis
}

// DNA returns the current Song DNA (nil when pending).
func (s *Studio) DNA() *songdna.DNA {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.dna
}

// DeviceReal reports whether a real audio device is in use.
func (s *Studio) DeviceReal() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.deviceReal
}

// SupportedFormats lists the registered input formats.
func (s *Studio) SupportedFormats() []audio.SourceFormat {
	return audio.SupportedFormats()
}

// Config returns the current visualizer configuration.
func (s *Studio) Config() visualizer.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.vizConfig
}

// SetConfig updates the visualizer configuration (clamped).
func (s *Studio) SetConfig(c visualizer.Config) {
	c.Clamp()
	s.mu.Lock()
	s.vizConfig = c
	s.mu.Unlock()
}

// TempoOverride returns the manual tempo correction (0 = measured).
func (s *Studio) TempoOverride() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tempoOverrideBPM
}

// SetTempoOverride sets the manual tempo correction. 0 restores the
// measured BPM; a nonzero value is clamped into the 30..300 BPM
// range and deliberately bypasses the tempo-confidence gate (the
// user decided). Returns the applied value.
func (s *Studio) SetTempoOverride(bpm float64) float64 {
	if bpm != 0 {
		bpm = clampRange(bpm, 30, 300, 0)
		if bpm == 0 {
			bpm = 120 // clamp fallback: a musical default
		}
	}
	s.mu.Lock()
	s.tempoOverrideBPM = bpm
	s.mu.Unlock()
	return bpm
}

// SyncOffset returns the manual sync correction in seconds.
func (s *Studio) SyncOffset() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.syncOffsetSeconds
}

// SetSyncOffset sets the manual audio/visual sync correction
// (±5 s clamped). Analysis features are read at (t - offset).
func (s *Studio) SetSyncOffset(seconds float64) {
	if seconds != seconds { // NaN
		seconds = 0
	}
	if seconds < -5 {
		seconds = -5
	}
	if seconds > 5 {
		seconds = 5
	}
	s.mu.Lock()
	s.syncOffsetSeconds = seconds
	s.mu.Unlock()
}

func clampRange(v, lo, hi, def float64) float64 {
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

// Import --------------------------------------------------------------

// ImportFile imports a track in a background goroutine and starts
// analysis on success. Cancel via CancelImport.
func (s *Studio) ImportFile(path string) error {
	s.mu.Lock()
	if s.importing {
		s.mu.Unlock()
		return errors.New("studio: an import is already running")
	}
	s.importing = true
	s.lastSourcePath = path // remembered for project relinking
	ctx, cancel := context.WithCancel(context.Background())
	s.importCancel = cancel
	s.mu.Unlock()

	s.emit(Event{Kind: EventImportStarted, Message: filepath.Base(path)})
	go func() {
		defer func() {
			s.mu.Lock()
			s.importing = false
			s.mu.Unlock()
		}()
		start := time.Now()
		track, err := audio.Import(ctx, path, s.cache, func(done, total uint64) {
			p := 0.0
			if total > 0 {
				p = float64(done) / float64(total) * 100
			}
			s.emit(Event{Kind: EventImportProgress, Progress: p})
		})
		if err != nil {
			if errors.Is(err, context.Canceled) {
				s.emit(Event{Kind: EventImportFailed, Message: "import cancelled"})
				return
			}
			s.emit(Event{Kind: EventImportFailed, Message: err.Error()})
			return
		}
		s.mu.Lock()
		s.track = track
		s.analysis = nil
		s.dna = nil
		s.mu.Unlock()
		s.emit(Event{
			Kind:    EventImportDone,
			Message: fmt.Sprintf("%s · %s · %s (imported in %s)", track.SourceName, track.Metadata.FormatText, track.Metadata.DurationText, time.Since(start).Round(time.Millisecond)),
			Payload: track,
		})
		s.startAnalysis(track)
	}()
	return nil
}

// CancelImport cancels a running import.
func (s *Studio) CancelImport() {
	s.mu.Lock()
	if s.importCancel != nil {
		s.importCancel()
	}
	s.mu.Unlock()
}

// startAnalysis runs DSP analysis in a background goroutine.
func (s *Studio) startAnalysis(track *audio.ImportedTrack) {
	s.emit(Event{Kind: EventAnalysisStarted, Message: "analyzing"})
	go func() {
		start := time.Now()
		res, err := s.analyzeTrack(track)
		if err != nil {
			s.emit(Event{Kind: EventAnalysisFailed, Message: err.Error()})
			return
		}
		s.mu.Lock()
		s.analysis = res
		cfg := s.vizConfig
		seed := s.dnaSeedFor()
		s.mu.Unlock()
		dna := songdna.Derive(track.Fingerprint, res, seed)
		s.mu.Lock()
		s.dna = dna
		// adopt DNA artistic defaults when the user has not customized
		if !s.userCustomized {
			cfg.Mode = visualizer.Mode(dna.DefaultMode)
			cfg.ParticleDensity = dna.ParticleDensity
			cfg.PulseResponse = dna.PulseResponse
			cfg.Smoothing = dna.Smoothing
			cfg.SpectrumTilt = dna.SpectrumTilt
			if track.Metadata.TagTitle == "" {
				cfg.TitleText = track.SourceName
			}
			s.vizConfig = cfg
		}
		s.mu.Unlock()
		s.emit(Event{Kind: EventAnalysisDone, Message: fmt.Sprintf("analysis done in %s — %s", time.Since(start).Round(time.Millisecond), dna.Descriptor)})
		s.emit(Event{Kind: EventPlaybackChanged})
	}()
}

// dnaSeedFor reads the project seed preference.
func (s *Studio) dnaSeedFor() uint64 {
	if s.projectDoc != nil {
		return s.projectDoc.SongDNASeed
	}
	return 0
}

// analyzeTrack streams the canonical PCM through the analyzer with
// bounded buffers.
func (s *Studio) analyzeTrack(track *audio.ImportedTrack) (*dsp.Result, error) {
	f, err := s.cache.Reader(track.Fingerprint)
	if err != nil {
		return nil, fmt.Errorf("studio: open canonical pcm: %w", err)
	}
	defer f.Close()
	a := dsp.NewAnalyzer()
	byteBuf := make([]byte, 96000*4) // 96000 interleaved samples
	f32Buf := make([]float32, 96000)
	targetBytes := int64(track.Frames) * 8
	var read int64
	for read < targetBytes {
		want := int64(len(byteBuf))
		if rem := targetBytes - read; rem < want {
			want = rem
		}
		n, rerr := f.Read(byteBuf[:want])
		if n > 0 {
			m := audio.DecodeFloat32LE(byteBuf[:n], f32Buf)
			a.Process(f32Buf[:m])
			read += int64(n)
		}
		if rerr != nil {
			break
		}
	}
	return a.Finish(), nil
}

// Playback -------------------------------------------------------------

// PlayPause toggles playback of the current track.
func (s *Studio) PlayPause() error {
	s.mu.RLock()
	track := s.track
	s.mu.RUnlock()
	if track == nil {
		return errors.New("studio: no track loaded")
	}
	s.mu.RLock()
	tr := s.transport
	s.mu.RUnlock()
	if tr == nil {
		s.mu.Lock()
		var err error
		tr, err = s.newTransportLocked()
		s.mu.Unlock()
		if err != nil {
			return err
		}
	}
	if tr.IsPlaying() {
		err := tr.Pause()
		s.emit(Event{Kind: EventPlaybackChanged})
		return err
	}
	// keep overlay title in sync with the track
	cfg := s.Config()
	if cfg.TitleText == "" {
		cfg.TitleText = track.SourceName
		s.SetConfig(cfg)
	}
	err := tr.Play()
	s.emit(Event{Kind: EventPlaybackChanged})
	return err
}

// newTransportLocked creates the transport for the current track.
func (s *Studio) newTransportLocked() (*playback.Transport, error) {
	if s.transport != nil {
		_ = s.transport.Close()
		s.transport = nil
	}
	tr, err := playback.NewTransport(s.output, s.cache.RawPath(s.track.Fingerprint))
	if err != nil {
		return nil, err
	}
	s.transport = tr
	return tr, nil
}

// Stop stops playback.
func (s *Studio) Stop() error {
	s.mu.RLock()
	tr := s.transport
	s.mu.RUnlock()
	if tr == nil {
		return nil
	}
	err := tr.Stop()
	s.emit(Event{Kind: EventPlaybackChanged})
	return err
}

// Seek seeks the current track.
func (s *Studio) Seek(d time.Duration) error {
	s.mu.RLock()
	tr := s.transport
	s.mu.RUnlock()
	if tr == nil {
		return nil
	}
	err := tr.Seek(d)
	s.emit(Event{Kind: EventPlaybackChanged})
	return err
}

// SetVolume sets playback volume 0..1.
func (s *Studio) SetVolume(v float64) {
	s.mu.RLock()
	tr := s.transport
	s.mu.RUnlock()
	if tr != nil {
		tr.SetVolume(v)
	}
}

// Clock returns the playback clock snapshot (zero when no track).
func (s *Studio) Clock() playback.ClockInfo {
	s.mu.RLock()
	tr := s.transport
	s.mu.RUnlock()
	if tr == nil {
		return playback.ClockInfo{State: playback.StateStopped}
	}
	return tr.Clock()
}

// Preview ---------------------------------------------------------------

// PreviewFrame renders the preview frame for the current playback
// position (called from the UI's render tick at ~30 fps).
func (s *Studio) PreviewFrame(w, h int, r render.Renderer) (*FrameResult, error) {
	clock := s.Clock()
	s.mu.RLock()
	track := s.track
	analysis := s.analysis
	dna := s.dna
	cfg := s.vizConfig
	s.mu.RUnlock()
	if track == nil {
		return nil, nil
	}
	audioState := s.audioStateAt(analysis, clock.Position.Seconds())
	in := visualizer.FrameInput{
		FrameIndex: int(clock.Position.Seconds() * 30),
		Time:       clock.Position.Seconds(),
		Width:      w, Height: h,
		Config: cfg, DNA: dna, Audio: audioState,
	}
	img, err := r.RenderFrame(in)
	if err != nil {
		return nil, err
	}
	return &FrameResult{Image: img, Info: r.Info(), Clock: clock}, nil
}

// FrameResult is one preview frame plus status.
type FrameResult struct {
	Image *image.RGBA
	Clock playback.ClockInfo
	Info  render.BackendInfo
}

// audioStateAt interpolates analysis frames at time t (deterministic).
// The manual sync offset shifts the analysis timeline: features are
// read at (t - syncOffset). The manual tempo override (when set)
// replaces the measured BPM for the beat phase and deliberately
// bypasses the confidence gate (explicit user correction).
func (s *Studio) audioStateAt(analysis *dsp.Result, t float64) visualizer.AudioState {
	s.mu.RLock()
	tempoOverride, syncOffset := s.tempoOverrideBPM, s.syncOffsetSeconds
	s.mu.RUnlock()

	te := t - syncOffset
	st := visualizer.AudioState{Time: t}
	if analysis == nil || len(analysis.Frames) == 0 {
		return st
	}
	t = te
	idx := int(t / analysis.HopSeconds)
	if idx < 0 {
		idx = 0
	}
	if idx >= len(analysis.Frames) {
		idx = len(analysis.Frames) - 1
	}
	f := analysis.Frames[idx]
	st.RMS, st.Bass, st.Mid, st.Treble = f.RMS, f.Bass, f.Mid, f.Treble
	st.Centroid, st.Flux, st.Onset = f.Centroid, f.Flux, f.Onset
	st.Bins = visualizer.InterpolateBins(f.Bins, 0)
	bpm := analysis.BPM
	reliable := analysis.TempoReliable
	if tempoOverride > 0 {
		bpm = tempoOverride
		reliable = true // user override: explicit decision wins
	}
	if reliable && bpm > 0 {
		beat := 60.0 / bpm
		st.BeatPhase = math.Mod(t, beat) / beat
		if st.BeatPhase < 0 {
			st.BeatPhase += 1
		}
	}
	return st
}

// AudioStateAt exposes deterministic audio state for export jobs.
func (s *Studio) AudioStateAt(t float64) visualizer.AudioState {
	s.mu.RLock()
	analysis := s.analysis
	s.mu.RUnlock()
	return s.audioStateAt(analysis, t)
}

// WaveformWindow returns a bounded waveform envelope window centered
// on t (±2 s), used by the waveform mode.
func (s *Studio) WaveformWindow(t float64) []float32 {
	s.mu.RLock()
	analysis := s.analysis
	s.mu.RUnlock()
	if analysis == nil || len(analysis.Waveform) == 0 {
		return nil
	}
	const windowSec = 4.0
	pts := int(windowSec * dsp.WaveformPointsPerSecond)
	if pts > len(analysis.Waveform) {
		pts = len(analysis.Waveform)
	}
	center := int(t * dsp.WaveformPointsPerSecond)
	lo := center - pts/2
	out := make([]float32, pts)
	for i := 0; i < pts; i++ {
		idx := lo + i
		if idx < 0 || idx >= len(analysis.Waveform) {
			continue
		}
		out[i] = analysis.Waveform[idx]
	}
	return out
}

// Projects --------------------------------------------------------------

// SaveProject writes the current state as a project file.
func (s *Studio) SaveProject(path string) error {
	s.mu.RLock()
	track := s.track
	cfg := s.vizConfig
	s.mu.RUnlock()
	if track == nil {
		return errors.New("studio: nothing to save — import a track first")
	}
	p := project.NewDefault(track.SourceName, s.trackSourcePath(track), track.Fingerprint,
		track.Metadata.Container, track.Duration)
	s.mu.RLock()
	tempoOverride, syncOffset := s.tempoOverrideBPM, s.syncOffsetSeconds
	s.mu.RUnlock()
	p.TempoOverrideBPM = tempoOverride
	p.SyncOffsetSeconds = syncOffset
	p.Visualizer = project.VisualizerSettings{
		Mode: string(cfg.Mode), Sensitivity: cfg.Sensitivity, Smoothing: cfg.Smoothing,
		Glow: cfg.Glow, Palette: cfg.Palette, ParticleDensity: cfg.ParticleDensity,
		SpectrumTilt: cfg.SpectrumTilt, PulseResponse: cfg.PulseResponse,
		MotionStyle: cfg.MotionStyle, VariationSeed: cfg.VariationSeed,
		ShowTitle: cfg.ShowTitle, TitleText: cfg.TitleText,
		SubtitleText: cfg.SubtitleText, ShowWatermark: cfg.ShowWatermark,
		BackgroundLayer:     string(cfg.BackgroundLayer),
		BackgroundIntensity: cfg.BackgroundIntensity,
	}
	if err := project.Save(p, path); err != nil {
		s.emit(Event{Kind: EventProjectFailed, Message: err.Error()})
		return err
	}
	s.mu.Lock()
	s.projectPath = path
	s.mu.Unlock()
	s.emit(Event{Kind: EventProjectSaved, Message: filepath.Base(path)})
	return nil
}

// trackSourcePath prefers the original import path when remembered.
func (s *Studio) trackSourcePath(t *audio.ImportedTrack) string {
	if t == nil {
		return ""
	}
	if s.lastSourcePath != "" {
		return s.lastSourcePath
	}
	return t.SourceName
}

// UpdateMediaSource records the full source path on the track
// metadata store for project relinking.
func (s *Studio) UpdateMediaSource(fullPath string) {
	s.mu.Lock()
	s.lastSourcePath = fullPath
	s.mu.Unlock()
}

// LoadProject loads a project file and relinks its media.
func (s *Studio) LoadProject(path string) error {
	p, err := project.Load(path)
	if err != nil {
		s.emit(Event{Kind: EventProjectFailed, Message: err.Error()})
		return err
	}
	dir := filepath.Dir(path)
	mediaPath, err := p.ResolveMedia(dir)
	missing := err != nil
	if missing && s.lastSourcePath != "" {
		mediaPath = s.lastSourcePath
		missing = false
	}
	if missing {
		s.emit(Event{Kind: EventProjectFailed,
			Message: "project media is missing — import the track and relink (saved settings kept)"})
		return ErrProjectMediaMissing
	}
	s.mu.Lock()
	s.projectPath = path
	s.projectDoc = p
	cfg := s.vizConfig
	cfg.Mode = visualizer.Mode(p.Visualizer.Mode)
	cfg.Sensitivity = p.Visualizer.Sensitivity
	cfg.Smoothing = p.Visualizer.Smoothing
	cfg.Glow = p.Visualizer.Glow
	cfg.Palette = p.Visualizer.Palette
	cfg.ParticleDensity = p.Visualizer.ParticleDensity
	cfg.SpectrumTilt = p.Visualizer.SpectrumTilt
	cfg.PulseResponse = p.Visualizer.PulseResponse
	cfg.MotionStyle = p.Visualizer.MotionStyle
	cfg.VariationSeed = p.Visualizer.VariationSeed
	cfg.ShowTitle = p.Visualizer.ShowTitle
	cfg.TitleText = p.Visualizer.TitleText
	cfg.SubtitleText = p.Visualizer.SubtitleText
	cfg.ShowWatermark = p.Visualizer.ShowWatermark
	cfg.BackgroundLayer = visualizer.Mode(p.Visualizer.BackgroundLayer)
	cfg.BackgroundIntensity = p.Visualizer.BackgroundIntensity
	cfg.Clamp()
	s.vizConfig = cfg
	s.tempoOverrideBPM = p.TempoOverrideBPM
	s.syncOffsetSeconds = p.SyncOffsetSeconds
	s.userCustomized = true
	s.mu.Unlock()
	s.emit(Event{Kind: EventProjectLoaded, Message: filepath.Base(path)})
	if !missing {
		return s.ImportFile(mediaPath)
	}
	return nil
}

// ErrProjectMediaMissing marks the relink case.
var ErrProjectMediaMissing = errors.New("studio: project media missing")

// ProjectPath returns the current project file path.
func (s *Studio) ProjectPath() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.projectPath
}

// Export -------------------------------------------------------------

// StartExport launches an export job in a background goroutine.
func (s *Studio) StartExport(req export.Request) error {
	s.mu.Lock()
	if s.exportJob != nil && s.exportJob.Running() {
		s.mu.Unlock()
		return errors.New("studio: an export is already running")
	}
	cfg := s.vizConfig
	dna := s.dna
	track := s.track
	s.mu.Unlock()
	if track == nil {
		return errors.New("studio: no track loaded")
	}
	req.Config = cfg
	req.DNA = dna
	req.AudioWavPath = s.cache.WavPath(track.Fingerprint)
	req.AudioDuration = track.Duration
	req.AudioStateProvider = s.AudioStateAt
	req.WaveformWindowProvider = s.WaveformWindow
	job, err := export.NewJob(req)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.exportJob = job
	s.mu.Unlock()
	s.emit(Event{Kind: EventExportProgress, Message: "export started", Progress: 0})
	go func() {
		res, err := job.Run(context.Background())
		if err != nil {
			s.emit(Event{Kind: EventExportFailed, Message: err.Error()})
			return
		}
		s.emit(Event{Kind: EventExportDone,
			Message: fmt.Sprintf("%s · %dx%d @%dfps · %.1fs · validated (%s)",
				filepath.Base(res.OutputPath), res.Width, res.Height, res.FPS, res.Duration, res.Backend.Backend),
			Payload: res})
	}()
	return nil
}

// ExportJob returns the current export job (nil when none).
func (s *Studio) ExportJob() *export.Job {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.exportJob
}

// Presets returns the built-in presets.
func (s *Studio) Presets() []visualizer.Preset {
	return visualizer.Presets()
}

// ApplyPreset applies a named preset to the visualizer config.
func (s *Studio) ApplyPreset(name string) bool {
	for _, p := range visualizer.Presets() {
		if p.Name == name {
			cfg := p.Config
			s.mu.RLock()
			track := s.track
			s.mu.RUnlock()
			if track != nil && cfg.ShowTitle && cfg.TitleText == "" {
				cfg.TitleText = track.SourceName
			}
			s.SetConfig(cfg)
			s.mu.Lock()
			s.userCustomized = true
			s.mu.Unlock()
			return true
		}
	}
	return false
}

// Close releases playback resources.
func (s *Studio) Close() {
	s.mu.Lock()
	tr := s.transport
	s.transport = nil
	s.mu.Unlock()
	if tr != nil {
		_ = tr.Close()
	}
}
