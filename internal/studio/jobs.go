// jobs.go — studio-side wiring of the single bounded work scheduler
// (internal/jobs) and the bounded play-event dispatcher.
//
// Routing map (Section 2.2 queue classes):
//
//	media import (decode to canonical PCM)  -> AUDIO_ANALYSIS  (CPU-heavy)
//	DSP/Song-DNA analysis                   -> AUDIO_ANALYSIS  (CPU-heavy)
//	first-play preparation (device open +
//	canonical cache open)                   -> INTERACTIVE_IO
//	offline export                          -> EXPORT          (CPU-heavy, 1+1)
//	maintenance (cache cleanup etc.)        -> MAINTENANCE
//
// The real-time audio callback/device thread and the UI event loop are
// NOT routed through the scheduler (classes A/B): the transport keeps
// owning the audio clock, Fyne keeps owning the UI thread.
//
// The play-event sink previously received every event on a fresh
// goroutine; it now runs on ONE dispatcher goroutine with a bounded
// buffer. On overflow the event is delivered inline (never dropped
// silently, never an unbounded goroutine per event) — the
// play.no-track/play.failed/play.eos lifecycle contract is preserved.
package studio

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/audio"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/jobs"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/playback"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/songdna"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/visualizer"
)

// playEventBuffer bounds the queued play-lifecycle events between the
// transport/studio producers and the single sink dispatcher.
const playEventBuffer = 256

// initScheduler creates the process-wide bounded scheduler with its
// job journal under the app-owned cache directory and starts the
// bounded play-event dispatcher.
func (s *Studio) initScheduler() error {
	sched, err := jobs.New(jobs.Config{
		JournalPath:  filepath.Join(s.cacheDir, "job-journal.jsonl"),
		DrainTimeout: 5 * time.Second,
	})
	if err != nil {
		return err
	}
	s.sched = sched
	s.playEvents = make(chan playback.PlayEvent, playEventBuffer)
	s.playSinkStop = make(chan struct{})
	s.playSinkDone = make(chan struct{})
	go s.playEventLoop()
	return nil
}

// playEventLoop is the single bounded dispatcher for play lifecycle
// events. It exits when the studio closes.
func (s *Studio) playEventLoop() {
	defer close(s.playSinkDone)
	for {
		select {
		case ev := <-s.playEvents:
			s.deliverPlayEvent(ev)
		case <-s.playSinkStop:
			return
		}
	}
}

// deliverPlayEvent invokes the sink with containment (a panicking sink
// must never take down the dispatcher or the app).
func (s *Studio) deliverPlayEvent(ev playback.PlayEvent) {
	s.mu.RLock()
	sink := s.playSink
	s.mu.RUnlock()
	if sink == nil {
		return
	}
	defer func() { _ = recover() }()
	sink(ev)
}

// stopPlayEventLoop stops the dispatcher with a bounded wait.
func (s *Studio) stopPlayEventLoop() {
	select {
	case <-s.playSinkStop:
		return // already stopped
	default:
	}
	close(s.playSinkStop)
	select {
	case <-s.playSinkDone:
	case <-time.After(time.Second):
	}
}

// runImportJob is the import worker body (AUDIO_ANALYSIS lane): decode
// the source into the canonical PCM cache, swap the studio track, and
// start the follow-up analysis. The studio "importing" single-flight
// flag is released on every path.
func (s *Studio) runImportJob(ctx context.Context, path string, h *jobs.JobHandle) {
	defer func() {
		s.mu.Lock()
		s.importing = false
		s.importCancel = nil
		s.mu.Unlock()
	}()
	start := time.Now()
	track, err := audio.Import(ctx, path, s.cache, func(done, total uint64) {
		p := 0.0
		if total > 0 {
			p = float64(done) / float64(total) * 100
		}
		if h != nil {
			h.ReportProgress("decoding", done, total)
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
	// New track: the old transport's PCM source is stale. Drop it so
	// the next Play creates a transport for the NEW fingerprint
	// (stale-track playback fixture: a track switch must never keep
	// playing the previous track's cache).
	stale := s.transport
	s.transport = nil
	s.importing = false
	s.importCancel = nil
	s.mu.Unlock()
	if stale != nil {
		_ = stale.Close() // idempotent, thread-safe
	}
	s.emit(Event{
		Kind:    EventImportDone,
		Message: fmt.Sprintf("%s · %s · %s (imported in %s)", track.SourceName, track.Metadata.FormatText, track.Metadata.DurationText, time.Since(start).Round(time.Millisecond)),
		Payload: track,
	})
	s.startAnalysis(track)
}

// runAnalysisJob is the DSP/Song-DNA analysis worker body
// (AUDIO_ANALYSIS lane).
func (s *Studio) runAnalysisJob(ctx context.Context, h *jobs.JobHandle) {
	start := time.Now()
	track := s.Track()
	if track == nil {
		s.emit(Event{Kind: EventAnalysisFailed, Message: "analysis skipped: track is no longer loaded"})
		return
	}
	if h != nil {
		h.ReportProgress("analyzing", 0, 0) // indeterminate stage
	}
	res, err := s.analyzeTrack(ctx, track)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			s.emit(Event{Kind: EventAnalysisFailed, Message: "analysis cancelled"})
		} else {
			s.emit(Event{Kind: EventAnalysisFailed, Message: err.Error()})
		}
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
}

// runFirstPlayJob is the first-play preparation worker body
// (INTERACTIVE_IO lane): device open + canonical PCM cache open off
// the UI thread, with every outcome reported through the play sink.
func (s *Studio) runFirstPlayJob(ctx context.Context) {
	defer func() {
		s.mu.Lock()
		s.preparingPlay = false
		s.mu.Unlock()
		s.emit(Event{Kind: EventPlaybackChanged})
	}()
	// Open the audio device BEFORE taking the studio lock: ensureOutput
	// locks s.mu inside sync.Once.Do, and Go mutexes are not
	// reentrant — calling it while holding s.mu self-deadlocks the
	// first play (the app(6).log silent-death root cause).
	s.ensureOutput()
	s.mu.Lock()
	tr, err := s.newTransportLocked()
	var fp string
	if s.track != nil {
		fp = s.track.Fingerprint
	}
	s.mu.Unlock()
	if err != nil {
		s.emitPlayEvent(playback.PlayEvent{Kind: playback.EvFailed, Err: err,
			Detail: "stage=open-transport (audio device open or canonical PCM cache read)"})
		return
	}
	if err := tr.Play(); err != nil {
		if errors.Is(err, playback.ErrClosed) {
			s.emitPlayEvent(playback.PlayEvent{Kind: playback.EvSupersededPlay, Err: err,
				Detail: "a concurrent track import superseded this play request"})
			return
		}
		s.emitPlayEvent(playback.PlayEvent{Kind: playback.EvFailed, Err: err,
			Detail: "stage=play (device pipeline start)"})
		return
	}
	// Play entered Priming with the player spawned: the session/
	// generation ID is now allocated and non-zero.
	clock := tr.Clock()
	s.emitPlayEvent(playback.PlayEvent{Kind: playback.EvSessionCreated, Session: clock.Session,
		Detail: "fingerprint=" + fp})
	// Device truth (never claims a real device before one is proven).
	if s.DeviceReal() {
		s.emitPlayEvent(playback.PlayEvent{Kind: playback.EvDeviceSelected, Session: clock.Session,
			Detail: "backend=oto;device=real"})
	} else {
		s.emitPlayEvent(playback.PlayEvent{Kind: playback.EvDeviceSelected, Session: clock.Session,
			Detail: "backend=silent(fallback);reason=" + s.OutputReason()})
	}
}

// JobsOverview is the Job Center data model: live jobs plus lane
// metrics. The UI renders this; it never blocks the UI thread (all
// fields are copied snapshots).
type JobsOverview struct {
	Metrics jobs.Metrics
	Live    []jobs.JobSnapshot
}

// JobsOverview returns the current scheduler state for the Job Center
// and diagnostics surfaces.
func (s *Studio) JobsOverview() JobsOverview {
	if s.sched == nil {
		return JobsOverview{Metrics: jobs.Metrics{Lanes: map[jobs.Lane]jobs.LaneMetrics{}}}
	}
	return JobsOverview{Metrics: s.sched.Snapshot(), Live: s.sched.LiveJobs()}
}

// CancelJob cancels a scheduler job by ID (Job Center action). It
// returns false for unknown or already-terminal jobs.
func (s *Studio) CancelJob(id uint64) bool {
	if s.sched == nil {
		return false
	}
	return s.sched.Cancel(id)
}

// SetSchedulerPressure forwards a measured pressure level to the
// resource governor (audio underruns / UI frame-time rise). See
// jobs.Scheduler.SetPressure for the contract.
func (s *Studio) SetSchedulerPressure(level int) {
	if s.sched != nil {
		s.sched.SetPressure(level)
	}
}
