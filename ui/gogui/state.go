// state.go — the mutex-protected UI state snapshot bridging studio
// service events (and every other background source) into the Gio
// event loop.
//
// Concurrency contract: OnStudioEvent / OnPlayEvent / Set* are called
// from service goroutines; the Gio loop calls the read accessors every
// frame. No gio type and no widget is ever touched here — the bridge
// only mutates snapshot values and asks the window for the next frame
// via win.Invalidate() (safe for concurrent use).

package gogui

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gioui.org/app"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/audio"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/mediatools"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/playback"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/render"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/studio"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/visualizer"
)

// playEventTail is the bounded play-event log shown in the studio panel.
const playEventTail = 8

// Small local aliases keep the panel code readable without weakening
// the core-boundary (they are pure type aliases, never new behavior).
type clockInfo = playback.ClockInfo

const statePlaying = playback.StatePlaying

// formatDuration renders media time as m:ss (never a wall clock).
func formatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	m := int(d / time.Minute)
	s := int((d % time.Minute) / time.Second)
	return fmt.Sprintf("%d:%02d", m, s)
}

// trackSnapshot is the shell-safe copy of the imported track metadata.
type trackSnapshot struct {
	Name        string
	Fingerprint string
	Duration    float64
	Meta        audio.Metadata
	Warnings    []string
	Present     bool
}

// uiState is the snapshot the window renders. All mutations go through
// the mutex; readers copy the values they need.
type uiState struct {
	mu  sync.Mutex
	win *app.Window

	clock   playback.ClockInfo
	track   trackSnapshot
	cfg     visualizer.Config
	cfgRev  uint64 // bumped when the service changed the config (preset/project load)
	status  string
	lastEvt string

	importProgress float64
	importing      bool

	exportRunning bool
	exportStage   string
	exportPct     float64

	playEvents []string

	jobs     studio.JobsOverview
	jobsHave bool

	mediaTools mediatools.Snapshot

	ffmpegText string

	sysFilesText      string
	sysFilesStep      int // 0 = entry, 1 = awaiting confirmation
	sysFilesSummary   []string
	sysFilesAnalyzing bool

	renderer render.BackendInfo

	activePreset string

	clipboardText string // pending clipboard write, consumed by the loop
}

func newUIState() *uiState { return &uiState{} }

// setWindow attaches the window (called once, before the loop starts).
func (s *uiState) setWindow(w *app.Window) {
	s.mu.Lock()
	s.win = w
	s.mu.Unlock()
}

// invalidate asks the window for the next frame (no-op when absent —
// tests run display-less).
func (s *uiState) invalidate() {
	s.mu.Lock()
	w := s.win
	s.mu.Unlock()
	if w != nil {
		w.Invalidate()
	}
}

// OnStudioEvent is the studio.Subscribe bridge (service goroutine).
func (s *uiState) OnStudioEvent(ev studio.Event) {
	s.mu.Lock()
	switch ev.Kind {
	case studio.EventImportStarted:
		s.importing = true
		s.importProgress = 0
		s.status = "Importing " + ev.Message
	case studio.EventImportProgress:
		s.importing = true
		s.importProgress = ev.Progress
	case studio.EventImportDone:
		s.importing = false
		s.importProgress = 100
		s.status = "Import complete: " + ev.Message
	case studio.EventImportFailed:
		s.importing = false
		s.status = "Import failed: " + ev.Message
	case studio.EventAnalysisStarted:
		s.status = "Analyzing " + ev.Message
	case studio.EventAnalysisDone:
		s.status = "Analysis complete"
	case studio.EventAnalysisFailed:
		s.status = "Analysis failed: " + ev.Message
	case studio.EventPlaybackChanged:
		s.status = "Transport: " + ev.Message
	case studio.EventExportProgress:
		s.exportRunning = true
		s.exportStage = ev.Message
		s.exportPct = ev.Progress
	case studio.EventExportDone:
		s.exportRunning = false
		s.exportPct = 100
		s.exportStage = "done: " + ev.Message
		s.status = "Export finished: " + ev.Message
	case studio.EventExportFailed:
		s.exportRunning = false
		s.status = "Export failed: " + ev.Message
	case studio.EventProjectSaved:
		s.status = "Project saved: " + ev.Message
	case studio.EventProjectLoaded:
		s.status = "Project loaded: " + ev.Message
	case studio.EventProjectFailed:
		s.status = "Project failed: " + ev.Message
	default:
		s.status = ev.Message
	}
	s.lastEvt = fmt.Sprintf("%d: %s", int(ev.Kind), ev.Message)
	s.mu.Unlock()
	s.invalidate()
}

// OnPlayEvent is the structured play lifecycle sink (transport
// goroutine). It keeps the bounded tail for the studio panel.
func (s *uiState) OnPlayEvent(ev playback.PlayEvent) {
	line := fmt.Sprintf("%s · %s · %s/%s", ev.Kind, ev.State.String(),
		ev.Position.Truncate(time.Millisecond).String(),
		ev.Total.Truncate(time.Millisecond).String())
	if ev.Detail != "" {
		line += " · " + ev.Detail
	}
	if ev.Err != nil {
		line += " · " + ev.Err.Error()
	}
	s.mu.Lock()
	s.playEvents = append(s.playEvents, line)
	if len(s.playEvents) > playEventTail {
		s.playEvents = s.playEvents[len(s.playEvents)-playEventTail:]
	}
	s.mu.Unlock()
	s.invalidate()
}

// SetClock refreshes the transport clock snapshot (event loop).
func (s *uiState) SetClock(c playback.ClockInfo) {
	s.mu.Lock()
	s.clock = c
	s.mu.Unlock()
}

// Clock returns the transport clock snapshot.
func (s *uiState) Clock() playback.ClockInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clock
}

// SetTrack refreshes the track snapshot (event loop, after import/load).
func (s *uiState) SetTrack(t *audio.ImportedTrack) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t == nil {
		s.track = trackSnapshot{}
		return
	}
	s.track = trackSnapshot{
		Name:        t.SourceName,
		Fingerprint: t.Fingerprint,
		Duration:    t.Duration,
		Meta:        t.Metadata,
		Warnings:    t.Warnings,
		Present:     true,
	}
}

// Track returns the track snapshot copy.
func (s *uiState) Track() trackSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.track
}

// SetConfig refreshes the visualizer config snapshot and bumps the
// revision (the panel widgets follow the service-side change).
func (s *uiState) SetConfig(c visualizer.Config) {
	s.mu.Lock()
	s.cfg = c
	s.cfgRev++
	s.mu.Unlock()
}

// Config returns the config snapshot and its revision.
func (s *uiState) Config() (visualizer.Config, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg, s.cfgRev
}

// SetStatus sets the status strip line (event loop or bridge).
func (s *uiState) SetStatus(msg string) {
	s.mu.Lock()
	s.status = msg
	s.mu.Unlock()
}

// Status returns the status strip line.
func (s *uiState) Status() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// LastEvent returns the last studio event summary line.
func (s *uiState) LastEvent() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastEvt
}

// ImportSnapshot returns the import progress state.
func (s *uiState) ImportSnapshot() (progress float64, importing bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.importProgress, s.importing
}

// ExportSnapshot returns the export progress state.
func (s *uiState) ExportSnapshot() (running bool, stage string, pct float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exportRunning, s.exportStage, s.exportPct
}

// PlayEvents returns a copy of the bounded play-event tail.
func (s *uiState) PlayEvents() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.playEvents))
	copy(out, s.playEvents)
	return out
}

// SetJobs refreshes the jobs overview (diagnostics poll).
func (s *uiState) SetJobs(o studio.JobsOverview) {
	s.mu.Lock()
	s.jobs = o
	s.jobsHave = true
	s.mu.Unlock()
}

// Jobs returns the jobs overview copy and whether one exists.
func (s *uiState) Jobs() (studio.JobsOverview, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.jobs, s.jobsHave
}

// SetMediaTools refreshes the media tools snapshot.
func (s *uiState) SetMediaTools(snap mediatools.Snapshot) {
	s.mu.Lock()
	s.mediaTools = snap
	s.mu.Unlock()
}

// MediaTools returns the media tools snapshot.
func (s *uiState) MediaTools() mediatools.Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mediaTools
}

// SetFFmpegText refreshes the FFmpeg availability line.
func (s *uiState) SetFFmpegText(t string) {
	s.mu.Lock()
	s.ffmpegText = t
	s.mu.Unlock()
}

// FFmpegText returns the FFmpeg availability line.
func (s *uiState) FFmpegText() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ffmpegText
}

// SetSysFilesText refreshes the System Files analysis output.
func (s *uiState) SetSysFilesText(t string) {
	s.mu.Lock()
	s.sysFilesText = t
	s.mu.Unlock()
}

// SysFilesText returns the System Files analysis output.
func (s *uiState) SysFilesText() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sysFilesText
}

// SysFilesStep returns the two-step authorization step (0 entry,
// 1 confirm-before-analyze).
func (s *uiState) SysFilesStep() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sysFilesStep
}

// SetSysFilesStep updates the authorization step.
func (s *uiState) SetSysFilesStep(v int) {
	s.mu.Lock()
	s.sysFilesStep = v
	s.mu.Unlock()
}

// SetSysFilesSummary stores the authorization summary lines shown
// before the user confirms the bounded walk.
func (s *uiState) SetSysFilesSummary(lines []string) {
	s.mu.Lock()
	s.sysFilesSummary = lines
	s.mu.Unlock()
}

// SysFilesSummary returns the stored summary lines.
func (s *uiState) SysFilesSummary() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sysFilesSummary
}

// SetSysFilesAnalyzing updates the analyzing flag.
func (s *uiState) SetSysFilesAnalyzing(v bool) {
	s.mu.Lock()
	s.sysFilesAnalyzing = v
	s.mu.Unlock()
}

// SysFilesAnalyzing returns the analyzing flag.
func (s *uiState) SysFilesAnalyzing() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sysFilesAnalyzing
}

// SetRenderer refreshes the renderer evidence snapshot.
func (s *uiState) SetRenderer(info render.BackendInfo) {
	s.mu.Lock()
	s.renderer = info
	s.mu.Unlock()
}

// Renderer returns the renderer evidence snapshot.
func (s *uiState) Renderer() render.BackendInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.renderer
}

// SetActivePreset records the last applied preset name (templates).
func (s *uiState) SetActivePreset(name string) {
	s.mu.Lock()
	s.activePreset = name
	s.mu.Unlock()
}

// ActivePreset returns the last applied preset name.
func (s *uiState) ActivePreset() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activePreset
}

// RequestClipboard stores the pending clipboard text.
func (s *uiState) RequestClipboard(text string) {
	s.mu.Lock()
	s.clipboardText = text
	s.mu.Unlock()
}

// TakeClipboard consumes the pending clipboard text (empty when none).
func (s *uiState) TakeClipboard() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.clipboardText
	s.clipboardText = ""
	return t
}

// formatTrackInfo renders the library metadata block for the studio
// and library panels.
func formatTrackInfo(t trackSnapshot) string {
	if !t.Present {
		return "No track imported."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Name: %s\n", t.Name)
	fmt.Fprintf(&b, "Container/codec: %s / %s (%s)\n", t.Meta.Container, t.Meta.Codec, t.Meta.Classifier)
	fmt.Fprintf(&b, "Sample rate: %d Hz · channels: %d · bit depth: %d\n", t.Meta.SampleRate, t.Meta.Channels, t.Meta.BitDepth)
	fmt.Fprintf(&b, "Duration: %.1f s · size: %d bytes\n", t.Duration, t.Meta.FileSize)
	if t.Meta.TagArtist != "" || t.Meta.TagTitle != "" {
		fmt.Fprintf(&b, "Tags: artist=%q title=%q\n", t.Meta.TagArtist, t.Meta.TagTitle)
	}
	fmt.Fprintf(&b, "Fingerprint: %s", t.Fingerprint)
	return b.String()
}

// shortName returns the base name of a path for display.
func shortName(p string) string { return filepath.Base(p) }
