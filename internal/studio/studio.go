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
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/apppaths"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/audio"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/content"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/dsp"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/export"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/jobs"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/mvplan"
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

	track    *audio.ImportedTrack
	analysis *dsp.Result
	// fullAnalysis is the v3 analysis document (stereo, bands, rhythm,
	// harmony, vocal-flow, roles, structure, dynamics). nil for legacy
	// results; the composition lookup derives from it.
	fullAnalysis *dsp.Analysis
	composition  *visualizer.Composition
	dna          *songdna.DNA
	// worldPlan is the per-track premade music-video world (mvplan)
	// derived from the v3 analysis + DNA; nil for legacy-only analyses.
	worldPlan *mvplan.Plan
	// worldPlanPath is the on-disk location the current plan was
	// persisted to ("" when none was persisted yet). One JSON document
	// per track fingerprint, overwritten on rebuild (bounded).
	worldPlanPath string
	vizConfig     visualizer.Config
	transport     *playback.Transport

	// Audio output opens lazily on the first Play and is published
	// through an atomic pointer: the sync.Once body performs NO
	// studio-mutex locking, so ensureOutput can never self-deadlock
	// under a held s.mu (the app(6).log first-Play freeze root cause).
	outputOnce  sync.Once
	outputState atomic.Pointer[outputState]

	analysisCancel context.CancelFunc
	// analysisDone is the explicit terminal-state signal of the analysis
	// worker (closed exactly when its Run body returns and every cache
	// file handle it owned is closed). It is pre-armed by the import
	// worker BEFORE the import-done event fires, so an observer that
	// awaits cache-file quiescence after an import can never miss the
	// window (deterministic close acknowledgement, never a timing sleep).
	analysisDone   chan struct{}
	exportJob      *export.Job
	projectPath    string
	projectDoc     *project.Project
	lastSourcePath string

	// Content service (run-43767556517 §3.12): when a library is
	// attached, project documents saved INSIDE the library root are
	// managed through the validated content boundary (stable typed IDs,
	// revision checks, trash/restore, undo/redo); documents saved
	// outside keep the plain atomic-save behavior unchanged.
	contentLib     *content.Library
	contentID      string
	contentRev     uint64
	userCustomized bool
	importing      bool
	importCancel   context.CancelFunc

	// Manual tempo/offset correction (roadmap Phase 2 #3). The
	// override bypasses the measured-tempo confidence gate because
	// it is an explicit user decision; 0 = measured BPM.
	tempoOverrideBPM  float64
	syncOffsetSeconds float64

	// playSink (v3 §C) receives structured transport lifecycle events
	// (play.state.changed, play.eos, ...) and is attached to every
	// transport as soon as one is created. The studio also emits its own
	// pre-transport lifecycle events (play.no-track, play.validation,
	// play.session.created, play.device.selected, play.failed) through
	// the same sink so every Play request terminates in a logged
	// outcome (V5 §3.6). Delivery runs on ONE bounded dispatcher
	// goroutine (see jobs.go), never one goroutine per event.
	playSink func(playback.PlayEvent)

	// Bounded work orchestration (run-44010666873 §2): all long-running
	// non-real-time work flows through the single app-owned scheduler.
	sched        *jobs.Scheduler
	playEvents   chan playback.PlayEvent
	playSinkStop chan struct{}
	playSinkDone chan struct{}

	// preparingPlay marks an in-flight FIRST play (device open + PCM
	// cache open run off the UI thread); a second click during
	// preparation is a harmless no-op instead of a duplicate session.
	preparingPlay bool

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
	// Bounded work orchestration (run-44010666873 §2): the single
	// scheduler owns every long-running non-real-time lane. A journal
	// failure degrades to an in-memory scheduler (work still bounded
	// and cancellable) with the error surfaced to the caller later via
	// diagnostics; it never blocks the launch.
	_ = s.initScheduler()
	// Device deferral (run 43090342995 startup contract): opening the
	// audio device (WASAPI/ALSA/oto context) is deferred to the first
	// playback attempt, so the first visual frame never waits on device
	// enumeration. DeviceReal() stays false until a device is proven —
	// an uninitialized state is never reported as real.
	s.vizConfig = defaultConfig()
	return s, nil
}

// outputState is the immutable audio-device snapshot published once
// by ensureOutput (real device, or the honest silent fallback with the
// exact reason).
type outputState struct {
	out    playback.Output
	real   bool
	errStr string
}

// ensureOutput lazily opens the audio device exactly once: real device
// when possible, silent fallback otherwise (honest state, surfaced via
// DeviceReal/OutputReason). The Once body takes NO studio locks — it
// publishes through the atomic pointer — so this function is safe to
// call with or without s.mu held.
func (s *Studio) ensureOutput() {
	s.outputOnce.Do(func() {
		st := &outputState{}
		out, err := playback.NewOtoOutput()
		if err == nil {
			st.out, st.real = out, true
		} else {
			st.out, st.errStr = playback.NewSilentOutput(), err.Error()
		}
		s.outputState.Store(st)
	})
}

// currentOutput returns the published output state (nil before the
// first Play initialized the device).
func (s *Studio) currentOutput() *outputState { return s.outputState.Load() }

// OutputStarted reports whether the device has been initialized at all
// (lazy startup: an untouched device is not a failure).
func (s *Studio) OutputStarted() bool {
	return s.currentOutput() != nil
}

// OutputReason reports why a real device is (or is not yet) in use.
func (s *Studio) OutputReason() string {
	st := s.currentOutput()
	if st == nil {
		return "device opens with the first playback (not yet initialized)"
	}
	if st.real {
		return "active (Oto)"
	}
	if st.errStr != "" {
		return st.errStr
	}
	return "silent preview mode"
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

// WorldPlan returns the per-track premade music-video world plan
// (nil when the v3 analysis is unavailable, e.g. legacy-only fallback).
// The plan is deterministic for the loaded track: same fingerprint,
// same world.
func (s *Studio) WorldPlan() *mvplan.Plan {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.worldPlan
}

// WorldPlanPath returns the on-disk path the current world plan was
// persisted to ("" when none was persisted yet). The JSON document is
// the deliverable a per-track premade music video (or a Blender round
// trip via --world-export-obj) consumes.
func (s *Studio) WorldPlanPath() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.worldPlanPath
}

// persistWorldPlan writes one plan JSON document under
// world-plans/<fingerprint>.json in the app data dir (next to the PCM
// cache). One file per track, overwritten on rebuild: the world-plans
// directory is bounded by the number of analyzed tracks, never by
// analysis count or time. Safe to call off the studio lock.
func (s *Studio) persistWorldPlan(p *mvplan.Plan) (string, error) {
	if p == nil || p.TrackFingerprint == "" {
		return "", fmt.Errorf("studio: world plan has no fingerprint")
	}
	fp := p.TrackFingerprint
	for _, r := range fp {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return "", fmt.Errorf("studio: track fingerprint contains an unusable character %q", r)
		}
	}
	dir := filepath.Join(filepath.Dir(s.cacheDir), "world-plans")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("studio: world-plan dir: %w", err)
	}
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return "", fmt.Errorf("studio: world-plan JSON: %w", err)
	}
	path := filepath.Join(dir, fp+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return "", fmt.Errorf("studio: world-plan write: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("studio: world-plan finalize: %w", err)
	}
	return path, nil
}

// DeviceReal reports whether a real audio device is proven in use
// (never true for the silent fallback).
func (s *Studio) DeviceReal() bool {
	st := s.currentOutput()
	return st != nil && st.real
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

// ImportFile imports a track through the bounded AUDIO_ANALYSIS lane
// (CPU-heavy decode) and starts analysis on success. Cancel via
// CancelImport. Enqueue is non-blocking; a saturated lane returns an
// actionable typed error instead of queueing without bound.
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
	if s.sched == nil {
		// Defensive fallback (scheduler init failed): run the same body
		// on one goroutine so an import is never silently impossible.
		go func() {
			defer cancel()
			s.runImportJob(ctx, path, nil)
		}()
		return nil
	}
	r := s.sched.Enqueue(jobs.Submit{
		Type: "import", Lane: jobs.LaneAudioAnalysis,
		IdempotencyKey: "studio:import", Priority: jobs.PriorityHigh,
		SourceFP: path, Cancellable: true,
		Run: func(jctx context.Context, h *jobs.JobHandle) error {
			// Bridge the job context to the studio import-cancel path so
			// CancelImport and scheduler cancellation stay equivalent.
			s.runImportJob(jctx, path, h)
			return nil
		},
	})
	if !r.Accepted() {
		cancel()
		s.mu.Lock()
		s.importing = false
		s.importCancel = nil
		s.mu.Unlock()
		if r.Outcome == jobs.OutcomeRejectedFull {
			return errors.New("studio: the analysis queue is busy — cancel a running job or retry shortly")
		}
		return errors.New("studio: import was not accepted: " + string(r.Outcome))
	}
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

// startAnalysis runs DSP analysis through the bounded AUDIO_ANALYSIS
// lane with a cancellation path (run 43541087097 lifecycle contract:
// every worker has an owner, a cancel signal and an EXPLICIT terminal
// acknowledgement; Close cancels a running analysis and then JOINS it
// instead of orphaning it). Single-flight is enforced by cancelling the
// previous analysis before enqueueing — the scheduler-level
// idempotency key was removed deliberately: a constant key folded a
// just-cancelled-but-still-pending prior analysis into the new
// request, silently dropping the new analysis (and, across track
// switches, analyzing the WRONG track). The worker closes the
// adopted terminal-state channel exactly once, after every cache file
// handle it owns is closed.
func (s *Studio) startAnalysis(track *audio.ImportedTrack) {
	s.emit(Event{Kind: EventAnalysisStarted, Message: "analyzing"})
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	// A previous analysis (superseded by a new import) is cancelled
	// first so at most one analysis worker is ever live.
	if s.analysisCancel != nil {
		s.analysisCancel()
	}
	s.analysisCancel = cancel
	// Adopt the pre-armed terminal-state channel when one is live
	// (the import worker arms it before the import-done event so
	// quiescence awaiters can never miss the window); never close a
	// channel a previous worker already closed.
	done := s.analysisDone
	if done == nil {
		done = make(chan struct{})
	} else {
		select {
		case <-done:
			done = make(chan struct{})
		default:
		}
	}
	s.analysisDone = done
	s.mu.Unlock()
	if s.sched == nil {
		go func() {
			defer cancel()
			defer close(done)
			s.runAnalysisJob(ctx, nil)
		}()
		return
	}
	r := s.sched.Enqueue(jobs.Submit{
		Type: "analysis", Lane: jobs.LaneAudioAnalysis,
		Priority: jobs.PriorityNormal,
		SourceFP: track.Fingerprint, Cancellable: true,
		Run: func(jctx context.Context, h *jobs.JobHandle) error {
			defer close(done)
			s.runAnalysisJob(jctx, h)
			return nil
		},
	})
	if !r.Accepted() {
		cancel()
		// No worker will ever own this channel: close it so
		// awaiters observe the rejected analysis as terminal.
		close(done)
		s.emit(Event{Kind: EventAnalysisFailed,
			Message: "analysis queue is busy — cancel a running job or retry shortly"})
	}
}

// WaitForAnalysisIdle awaits the terminal state of the analysis worker
// that owns the current terminal-state signal. It returns nil once
// that worker returned and every cache file handle it owned is closed;
// an armed-but-never-started signal (rejected enqueue) resolves
// immediately because the rejection closes it. nil signal = no analysis
// was ever armed (idle). This is the explicit close acknowledgement the
// lifecycle contract requires — callers must NEVER replace it with a
// timing sleep.
func (s *Studio) WaitForAnalysisIdle(ctx context.Context) error {
	s.mu.RLock()
	done := s.analysisDone
	s.mu.RUnlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// dnaSeedFor reads the project seed preference.
func (s *Studio) dnaSeedFor() uint64 {
	if s.projectDoc != nil {
		return s.projectDoc.SongDNASeed
	}
	return 0
}

// analyzeTrack streams the canonical PCM through the v3 full analyzer
// (priority-C/D wiring): one pass produces the legacy Result (frames,
// waveform, coarse tempo) AND every measured layer. Bounded buffers;
// honours ctx between chunks so a cancelled or superseded analysis
// stops promptly without corrupting state. The completed document is
// cached as a versioned sidecar keyed by the content fingerprint:
// a matching (version, fingerprint) sidecar is reused, a mismatched
// one is invalidated and regenerated - never reinterpreted.
func (s *Studio) analyzeTrack(ctx context.Context, track *audio.ImportedTrack) (*dsp.Result, error) {
	if a := s.loadCachedAnalysis(track.Fingerprint); a != nil {
		s.mu.Lock()
		s.fullAnalysis = a
		s.composition = visualizer.NewComposition(a)
		s.mu.Unlock()
		return a.Legacy, nil
	}
	f, err := s.cache.Reader(track.Fingerprint)
	if err != nil {
		return nil, fmt.Errorf("studio: open canonical pcm: %w", err)
	}
	defer f.Close()
	full, err := dsp.AnalyzeFullReader(ctx, f, track.Fingerprint, "canonical-pcm")
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, err
		}
		// Hard analysis error: fall back to the legacy pipeline so the
		// app stays usable, and record the limitation honestly.
		f2, ferr := s.cache.Reader(track.Fingerprint)
		if ferr != nil {
			return nil, fmt.Errorf("studio: open canonical pcm: %w", err)
		}
		defer f2.Close()
		res, lerr := legacyAnalyze(ctx, f2, int64(track.Frames))
		if lerr != nil {
			return nil, err
		}
		s.mu.Lock()
		s.fullAnalysis = nil
		s.composition = nil
		s.worldPlan = nil
		s.mu.Unlock()
		return res, nil
	}
	if err := full.Validate(); err != nil {
		return nil, fmt.Errorf("studio: analysis validation: %w", err)
	}
	s.mu.Lock()
	s.fullAnalysis = full
	s.composition = visualizer.NewComposition(full)
	s.mu.Unlock()
	s.storeCachedAnalysis(track.Fingerprint, full)
	return full.Legacy, nil
}

// legacyAnalyze is the pre-v3 path (frames + waveform + coarse tempo).
func legacyAnalyze(ctx context.Context, f io.Reader, frames int64) (*dsp.Result, error) {
	a := dsp.NewAnalyzer()
	byteBuf := make([]byte, 96000*4) // 96000 interleaved samples
	f32Buf := make([]float32, 96000)
	targetBytes := frames * 8
	var read int64
	for read < targetBytes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
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

// analysisSidecarPath is the versioned analysis cache sidecar.
func (s *Studio) analysisSidecarPath(fp string) string {
	return filepath.Join(s.cache.Dir(), fp+".analysis.json")
}

// loadCachedAnalysis loads the analysis sidecar when its schema version
// and fingerprint match. Any mismatch means INVALIDATION: the file is
// removed (never reinterpreted with new semantics) and the caller
// re-analyzes.
func (s *Studio) loadCachedAnalysis(fp string) *dsp.Analysis {
	path := s.analysisSidecarPath(fp)
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	a, err := dsp.LoadAnalysis(b)
	if err != nil {
		// Safe invalidation: remove the incompatible sidecar.
		_ = os.Remove(path)
		return nil
	}
	if a.SourceFingerprint != fp {
		_ = os.Remove(path)
		return nil
	}
	return a
}

// storeCachedAnalysis atomically persists the analysis document.
func (s *Studio) storeCachedAnalysis(fp string, a *dsp.Analysis) {
	blob, err := a.Serialize()
	if err != nil {
		return
	}
	path := s.analysisSidecarPath(fp)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, blob, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

// Playback -------------------------------------------------------------

// Typed, actionable Play errors (V5 §3.1/§3.6: a bare technical
// string is not an actionable message).
var (
	// ErrNoTrack: no playable audio asset is selected.
	ErrNoTrack = errors.New("studio: no track loaded — import or select a track first")
	// ErrImportInProgress: an import is still running for the selected track.
	ErrImportInProgress = errors.New("studio: import still running — wait for it to finish, then press Play")
)

// emitPlayEvent delivers a studio-level play lifecycle event through
// the bounded dispatcher (the sink must never block the caller — V5
// §3.6). The dispatcher is one goroutine with a bounded buffer; on
// overflow the event is delivered inline with containment — never
// dropped silently, never a fresh goroutine per event.
func (s *Studio) emitPlayEvent(ev playback.PlayEvent) {
	if ev.At.IsZero() {
		ev.At = time.Now()
	}
	select {
	case s.playEvents <- ev:
		return
	default:
	}
	// Buffer overflow: bounded inline delivery keeps the lifecycle
	// contract (play events are never silently dropped).
	s.deliverPlayEvent(ev)
}

// PlayPause toggles playback of the current track.
//
// Contract (V5 §3.2/§3.3/§3.6): the request is validated first (track
// present, import finished); the FIRST play of a track opens the audio
// device and the canonical PCM cache OFF the UI thread (no UI-thread
// blocking for audio open — V5 non-negotiable #6); every outcome is
// reported through the play event sink (play.no-track / play.validation
// / play.session.created / play.device.selected / play.output.started
// / play.failed), so no request can silently vanish. Subsequent
// play/pause/seek/stop calls run synchronously on the existing
// transport (non-blocking transport operations).
func (s *Studio) PlayPause() error {
	s.mu.RLock()
	track := s.track
	importing := s.importing
	tr := s.transport
	preparing := s.preparingPlay
	s.mu.RUnlock()
	if importing {
		s.emitPlayEvent(playback.PlayEvent{Kind: playback.EvValidation,
			Detail: "import of the selected track is still running"})
		return ErrImportInProgress
	}
	if track == nil {
		s.emitPlayEvent(playback.PlayEvent{Kind: playback.EvNoTrack,
			Detail: "no playable track selected — import or select a track first"})
		return ErrNoTrack
	}
	if tr == nil {
		if preparing {
			// double click while the first play is still preparing: no
			// duplicate session, no error
			s.emitPlayEvent(playback.PlayEvent{Kind: playback.EvValidation,
				Detail: "playback is already starting"})
			return nil
		}
		return s.startPlaybackAsync()
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

// startPlaybackAsync opens the audio device and the canonical PCM
// cache and starts the FIRST play on the bounded INTERACTIVE_IO lane,
// keeping the UI thread free of device-open blocking. The worker
// reports every outcome through the play event sink; a concurrent
// track import supersedes the attempt cleanly (the transport is closed
// by the import path and the play reports play.superseded instead of
// failing).
func (s *Studio) startPlaybackAsync() error {
	s.mu.Lock()
	if s.preparingPlay {
		s.mu.Unlock()
		return nil
	}
	s.preparingPlay = true
	s.mu.Unlock()
	if s.sched == nil {
		go s.runFirstPlayJob(context.Background())
		return nil
	}
	r := s.sched.Enqueue(jobs.Submit{
		Type: "first-play", Lane: jobs.LaneInteractiveIO,
		IdempotencyKey: "studio:first-play", Priority: jobs.PriorityHigh,
		Cancellable: true,
		Run: func(jctx context.Context, h *jobs.JobHandle) error {
			s.runFirstPlayJob(jctx)
			return nil
		},
	})
	if !r.Accepted() && !r.WasCoalesced() {
		s.mu.Lock()
		s.preparingPlay = false
		s.mu.Unlock()
		s.emitPlayEvent(playback.PlayEvent{Kind: playback.EvFailed,
			Err:    errors.New("interactive queue busy"),
			Detail: "stage=enqueue (interactive I/O lane saturated — cancel a running job or retry shortly)"})
	}
	return nil
}

// newTransportLocked creates the transport for the current track.
// ensureOutput MUST have already run (see startPlaybackAsync): it locks
// s.mu inside sync.Once.Do and must never be called under this lock.
func (s *Studio) newTransportLocked() (*playback.Transport, error) {
	if s.transport != nil {
		_ = s.transport.Close()
		s.transport = nil
	}
	pcm := s.cache.RawPath(s.track.Fingerprint)
	st := s.currentOutput()
	if st == nil {
		// Defensive: the async first-play path always runs ensureOutput
		// before this lock; a nil state here is an internal ordering bug,
		// reported honestly instead of dereferenced.
		return nil, errors.New("studio: audio output not initialized before transport creation")
	}
	tr, err := playback.NewTransport(st.out, pcm)
	if err != nil {
		// Actionable cache-miss diagnosis (V5 §3.1 fixture C): the
		// canonical PCM cache for the selected fingerprint is gone.
		if _, serr := os.Stat(pcm); serr != nil {
			return nil, fmt.Errorf("studio: audio cache missing for this track — re-import it (fingerprint %s)", s.track.Fingerprint)
		}
		return nil, err
	}
	if s.playSink != nil {
		tr.SetEventSink(s.playSink)
	}
	s.transport = tr
	return tr, nil
}

// SetPlayEventSink installs the structured play lifecycle sink (v3 §C
// diagnostics). It is applied to the live transport immediately (when
// one exists) and to every transport created later. Events carry the
// playback session/generation ID, state, position and error; the sink
// itself must not block (it is invoked on the transport goroutine
// trampoline).
func (s *Studio) SetPlayEventSink(f func(playback.PlayEvent)) {
	s.mu.Lock()
	s.playSink = f
	tr := s.transport
	s.mu.Unlock()
	if tr != nil {
		tr.SetEventSink(f)
	}
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
	// PRIORITY-D §7.1: the composition lookup is the COMMON time base —
	// the exact same state feeds preview and export (export goes through
	// AudioStateProvider -> this function), so the two can never
	// disagree. Legacy audio fields above stay untouched for existing
	// renderers.
	s.mu.RLock()
	comp := s.composition
	s.mu.RUnlock()
	if comp != nil {
		cs := comp.StateAt(te)
		st.HasComposition = true
		st.BandEnvelope = cs.BandEnvelope
		st.BandShare = cs.BandShare
		st.Transient = cs.Transient
		st.TransientBand = cs.TransientBand
		st.DownbeatSoon = cs.DownbeatSoon
		st.ChordRoot = cs.ChordRoot
		st.ChordQuality = cs.ChordQuality
		st.ChordConfidence = cs.ChordConfidence
		st.KeyConfidence = cs.KeyConfidence
		st.Tension = cs.Tension
		st.VocalLikelihood = cs.VocalLikelihood
		st.VocalDensity = cs.VocalDensity
		st.VocalPhraseActive = cs.VocalPhraseActive
		st.SectionIndex = cs.SectionIndex
		st.SectionLabel = cs.SectionLabel
		st.StereoWidth = cs.StereoWidth
		st.Correlation = cs.Correlation
		st.MomentaryLUFS = cs.MomentaryLUFS
		st.Confidence = cs.Confidence
		// Measured beat phase replaces the synthetic phase when the
		// measured grid is reliable (the grid IS the measurement; the
		// phase above was derived from the same tempo estimate).
		if cs.BeatPhase >= 0 {
			st.BeatPhase = cs.BeatPhase
		}
	}
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
	if s.manageContentAt(filepath.Dir(path)) {
		if err := s.saveThroughContentService(p, path); err != nil {
			s.emit(Event{Kind: EventProjectFailed, Message: err.Error()})
			return err
		}
		s.emit(Event{Kind: EventProjectSaved, Message: filepath.Base(s.projectPathLocked())})
		return nil
	}
	if err := project.Save(p, path); err != nil {
		s.emit(Event{Kind: EventProjectFailed, Message: err.Error()})
		return err
	}
	s.mu.Lock()
	s.projectPath = path
	s.contentID, s.contentRev = "", 0 // unmanaged document
	s.mu.Unlock()
	s.emit(Event{Kind: EventProjectSaved, Message: filepath.Base(path)})
	return nil
}

// manageContentAt reports whether the directory is the attached content
// library root (managed content lives there and only there).
func (s *Studio) manageContentAt(dir string) bool {
	s.mu.RLock()
	lib := s.contentLib
	s.mu.RUnlock()
	if lib == nil || dir == "" {
		return false
	}
	return dir == lib.Root()
}

// saveThroughContentService persists the document through the validated
// content boundary: an existing managed entity is updated under an
// exact revision precondition; a first save creates the entity with a
// stable typed ID. Conflicts surface as errors — never last-write-wins.
func (s *Studio) saveThroughContentService(p *project.Project, path string) error {
	s.mu.RLock()
	lib, id, rev := s.contentLib, s.contentID, s.contentRev
	s.mu.RUnlock()
	if lib == nil {
		return errors.New("studio: content service not attached")
	}
	if id != "" {
		e, err := lib.Update(id, rev, func(doc *project.Project) error {
			doc.Name = p.Name
			doc.MediaRef = p.MediaRef
			doc.Visualizer = p.Visualizer
			doc.SongDNASeed = p.SongDNASeed
			doc.Export = p.Export
			doc.TimelineMarkers = p.TimelineMarkers
			doc.TempoOverrideBPM = p.TempoOverrideBPM
			doc.SyncOffsetSeconds = p.SyncOffsetSeconds
			return nil
		})
		if errors.Is(err, content.ErrUnknownEntity) || errors.Is(err, content.ErrEntityTrashed) {
			// The managed entity disappeared (purged/restored outside
			// this editor): fall through to a fresh Create below.
			id = ""
		} else if err != nil {
			return err
		} else {
			s.mu.Lock()
			s.projectPath, s.contentID, s.contentRev = e.Path, e.ID, e.Revision
			s.mu.Unlock()
			return nil
		}
	}
	name := strings.TrimSuffix(filepath.Base(path), ".sheytan.json")
	e, err := lib.Create(content.CreateRequest{
		Name:        name,
		SourcePath:  p.MediaRef.AbsPath,
		Fingerprint: p.MediaRef.Fingerprint,
		Container:   p.MediaRef.Container,
		Duration:    p.MediaRef.Duration,
		Settings:    p,
	})
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.projectPath, s.contentID, s.contentRev = e.Path, e.ID, e.Revision
	s.mu.Unlock()
	return nil
}

// projectPathLocked returns the current managed path (lock held by caller).
func (s *Studio) projectPathLocked() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.projectPath
}

// AttachContentLibrary connects the studio to the validated content
// service for the given library root (UI-independent; pass nil to detach).
func (s *Studio) AttachContentLibrary(lib *content.Library) {
	s.mu.Lock()
	s.contentLib = lib
	s.contentID, s.contentRev = "", 0
	s.mu.Unlock()
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
	s.contentID, s.contentRev = p.ContentID, p.Revision
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

// StartExport launches an export job through the bounded EXPORT lane
// (1 waiting / 1 active globally). The lane bound replaces the old
// ad-hoc single-flight flag as the hard ceiling; the flag stays as
// the fast, user-actionable check.
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
	if s.sched == nil {
		go func() {
			res, err := job.Run(context.Background())
			s.reportExportResult(res, err)
		}()
		return nil
	}
	r := s.sched.Enqueue(jobs.Submit{
		Type: "export", Lane: jobs.LaneExport,
		IdempotencyKey: "studio:export", Priority: jobs.PriorityNormal,
		SourceFP: track.Fingerprint, Cancellable: true,
		EstDiskMB: 2048, // conservative temporary-disk reservation
		Run: func(jctx context.Context, h *jobs.JobHandle) error {
			res, err := job.Run(jctx)
			s.reportExportResult(res, err)
			if err != nil {
				return err
			}
			return nil
		},
	})
	if !r.Accepted() && !r.WasCoalesced() {
		s.mu.Lock()
		s.exportJob = nil
		s.mu.Unlock()
		s.emit(Event{Kind: EventExportFailed,
			Message: "the export queue is busy — cancel the running export or retry shortly"})
	}
	return nil
}

// reportExportResult emits the terminal export events.
func (s *Studio) reportExportResult(res *export.Result, err error) {
	if err != nil {
		s.emit(Event{Kind: EventExportFailed, Message: err.Error()})
		return
	}
	s.emit(Event{Kind: EventExportDone,
		Message: fmt.Sprintf("%s · %dx%d @%dfps · %.1fs · validated (%s)",
			filepath.Base(res.OutputPath), res.Width, res.Height, res.FPS, res.Duration, res.Backend.Backend),
		Payload: res})
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

// IsPlayingLike reports whether audio is actively being consumed, so
// the UI render loop can run full rate while playing and throttle when
// idle. Truthful when no transport exists yet (false = idle).
func (s *Studio) IsPlayingLike() bool {
	s.mu.RLock()
	tr := s.transport
	s.mu.RUnlock()
	if tr == nil {
		return false
	}
	return tr.IsPlaying()
}

// Importing reports whether an import/analysis is currently running,
// so the shutdown path can offer cancellation instead of destroying it.
func (s *Studio) Importing() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.importing
}

// Close releases every owned worker and resource in dependency-safe
// order (run 43541087097 shutdown contract): cancel the running
// import, cancel a running analysis, cancel and stop the export job
// (reaping the FFmpeg child), then stop and close the transport, and
// finally drain the bounded scheduler and stop the play-event
// dispatcher. Idempotent: every stage is guarded, so the appclose
// Release hook and the defensive post-window close can both call it.
func (s *Studio) Close() {
	s.mu.Lock()
	importCancel := s.importCancel
	analysisCancel := s.analysisCancel
	job := s.exportJob
	tr := s.transport
	sched := s.sched
	s.importCancel = nil
	s.analysisCancel = nil
	s.exportJob = nil
	s.transport = nil
	s.mu.Unlock()
	if importCancel != nil {
		importCancel()
	}
	if analysisCancel != nil {
		analysisCancel()
	}
	// Deterministic join of the analysis worker (bounded): cancellation
	// stops the reader between chunks and its deferred file close runs
	// before the terminal-state channel closes, so when this join
	// returns the worker owns NO cache file handle. This replaces the
	// old hope-based sequencing that left an open .f32 handle behind on
	// Windows (where an unjoined handle blocks cache-file removal).
	s.mu.RLock()
	analysisDone := s.analysisDone
	s.mu.RUnlock()
	if analysisDone != nil {
		select {
		case <-analysisDone:
		case <-time.After(5 * time.Second):
			// Never reachable while the scheduler is healthy: the
			// cancelled worker closes the channel promptly. Surfaced
			// through diagnostics rather than hanging Close.
		}
	}
	if job != nil && job.Running() {
		job.Cancel()
	}
	if tr != nil {
		_ = tr.Close()
	}
	// Bounded scheduler drain: cancellable stragglers are cancelled at
	// the deadline; Shutdown returns within its documented bounds.
	if sched != nil {
		sched.Shutdown(3 * time.Second)
	}
	s.stopPlayEventLoop()
}
