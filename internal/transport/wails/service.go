package wails

// service.go — the validated studio facade for the web shell.
//
// This file is deliberately free of any Wails imports: it holds only
// core packages, the embedded frontend and the standard library, so
// the facade and its /api/* mux compile and test headless (no tags,
// no display). The Wails application wiring lives in app.go under the
// purego build tag; see doc.go for the boundary rules.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/design"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/frontend/wails/dist"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/apppaths"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/buildinfo"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/export"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/logging"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/mediatools"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/presentation"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/render"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/studio"
)

// Facade tuning constants. The seek delta and volume ranges are shell
// policy: bounded, explicit and validated before anything reaches the
// studio core.
const (
	defaultSeekSeconds = 10.0
	maxSeekSeconds     = 3600.0
	defaultVolume      = 0.9
)

// Facade error kinds for FacadeError. The /api mux maps them onto HTTP
// statuses; the web UI surfaces the reason verbatim.
const (
	KindUnknownCommand     = "unknown-command"
	KindUnsupportedCommand = "unsupported-command"
	KindInvalidArgs        = "invalid-args"
	KindRejected           = "rejected"
)

// FacadeError is the typed error the facade returns for every refused
// web request. Kind tells the shell WHY the request failed:
//
//   - unknown-command:      the ID is not in presentation.ValidateCommand
//   - unsupported-command:  known command that this shell must not run
//     (native-shell action, filesystem/shell exposure, ...) with the
//     exact reason attached
//   - invalid-args:         the JSON arguments failed validation
//   - rejected:             valid request, core refused (e.g. no track)
type FacadeError struct {
	Command presentation.CommandID `json:"command,omitempty"`
	Kind    string                 `json:"kind"`
	Reason  string                 `json:"reason"`
}

// Error implements the error interface with an explicit, actionable
// one-line message.
func (e *FacadeError) Error() string {
	if e.Command != "" {
		return fmt.Sprintf("wails shell: command %q refused (%s): %s", e.Command, e.Kind, e.Reason)
	}
	return fmt.Sprintf("wails shell: request refused (%s): %s", e.Kind, e.Reason)
}

// unsupportedReasons lists every canonical command the web shell must
// NOT run, each with the boundary rule that forbids it. Known IDs not
// present in this map and not dispatched by RunCommand are a
// programming bug caught by the command-catalog test.
var unsupportedReasons = map[presentation.CommandID]string{
	presentation.CmdProjectSave:        "project save writes files; filesystem write APIs are never exposed to the web layer",
	presentation.CmdProjectOpen:        "project open reads user-selected files; file access is a native-shell action",
	presentation.CmdRendererSelect:     "renderer selection is a native-shell preference (SHEYTAN_GPU_PREFERENCE / native settings); the web shell reports evidence only",
	presentation.CmdModeSet:            "visualizer mode editing belongs to the live preview in the native shell; the web shell must not fake a visualization",
	presentation.CmdPaletteSet:         "palette editing belongs to the live preview in the native shell",
	presentation.CmdSensitivitySet:     "sensitivity editing belongs to the live preview in the native shell",
	presentation.CmdBackgroundLayerSet: "background layer editing belongs to the live preview in the native shell",
	presentation.CmdNavigate:           "tab navigation is handled inside the web shell itself; no Go round trip is needed",
	presentation.CmdTabsReorder:        "workspace layout persistence is a native-shell surface in this slice",
	presentation.CmdTabsReset:          "workspace layout persistence is a native-shell surface in this slice",
	presentation.CmdSplitOrientation:   "workspace layout persistence is a native-shell surface in this slice",
	presentation.CmdPanelCollapse:      "workspace layout persistence is a native-shell surface in this slice",
	presentation.CmdPanelRestore:       "workspace layout persistence is a native-shell surface in this slice",
	presentation.CmdLayoutReset:        "workspace layout persistence is a native-shell surface in this slice",
	presentation.CmdMediaToolsInstall:  "media-tool installation downloads archives and writes them under the data root; never exposed to the web layer",
	presentation.CmdMediaToolsOpen:     "revealing an OS directory requires native shell integration",
	presentation.CmdAnalyzePath:        "file-system analysis of user-supplied paths gives the web layer no path access by design",
	presentation.CmdHelpOpen:           "the help window is a native-shell surface in this slice",
	presentation.CmdLogsReveal:         "revealing the log directory in the OS file manager is a native-shell action",
	presentation.CmdCopyToClipboard:    "clipboard integration is not wired to the web layer in this slice",
	presentation.CmdAbout:              "the About surface is native-shell UI; build identity is served via /api/build-info",
}

// dispatchedCommands is the supported subset RunCommand implements.
// Keep in sync with the switch below; the command-catalog test proves
// the two views (dispatched + unsupported) are exhaustive over the
// canonical command set.
var dispatchedCommands = []presentation.CommandID{
	presentation.CmdImportAudio,
	presentation.CmdPlayPause,
	presentation.CmdStop,
	presentation.CmdSeekBack,
	presentation.CmdSeekForward,
	presentation.CmdSetVolume,
	presentation.CmdPresetApply,
	presentation.CmdIntensitySet,
	presentation.CmdReducedMotionSet,
	presentation.CmdReducedGlowSet,
	presentation.CmdExportStart,
	presentation.CmdExportCancel,
	presentation.CmdExportPreview,
	presentation.CmdExportSubtitle,
	presentation.CmdExportSubtitleClear,
	presentation.CmdDiagnosticsExport,
	presentation.CmdQuit,
}

// CommandInfo is one entry of the command catalog served to the web UI.
type CommandInfo struct {
	ID      presentation.CommandID `json:"id"`
	Keys    string                 `json:"keys,omitempty"`
	Reason  string                 `json:"reason,omitempty"`
	Refused bool                   `json:"refused"`
}

// CommandCatalog is the honest command surface: what the web shell can
// run, and what it must not run with the exact boundary reason.
type CommandCatalog struct {
	Supported   []CommandInfo `json:"supported"`
	Unsupported []CommandInfo `json:"unsupported"`
}

// SheytanService is the single facade instance handed to both the Wails
// service registry (app.go) and the /api/* mux. All methods are safe
// for concurrent web calls; mutable shell settings are guarded by mu.
type SheytanService struct {
	mu     sync.Mutex
	studio *studio.Studio

	// Shell-visible settings and the facade-set volume. The studio
	// applies the volume to the live transport when one exists; the
	// value below is what this shell set last, reported honestly in
	// the transport snapshot.
	settings presentation.SettingsDTO
	volume   float64
	quit     func()

	// tokens is the validated canonical document loaded once at
	// construction (a broken token document is a build-time bug).
	tokens design.TokenDocument

	// Renderer evidence is captured ONCE (SelectRenderer is safe
	// headless; the CPU fallback is honest) and closed immediately:
	// the web shell renders evidence, it does not render frames.
	rendererOnce sync.Once
	rendererInfo render.BackendInfo

	// media owns the app-managed FFmpeg/FFprobe download lifecycle for
	// the downloads pane (run 45532045379: real file size + real status
	// for updating engines). Built lazily on the resolved apppaths data
	// root - the same manager the go-gui shell constructs - so the
	// facade serves REAL progress bytes and states, never synthetic
	// download animation. All access goes through mediaManager (nil
	// never escapes; mediaMu guards construction and Install goroutine).
	mediaMu   sync.Mutex
	mediaOnce sync.Once
	media     *mediatools.Manager

	// system owns the system-profile cache and the last user-requested
	// network probe (service_system.go, run 45678113841). Instance
	// state, never package globals, so tests and shells stay isolated.
	systemOnce sync.Once
	system     *systemState

	// dialogs owns the native dialog capability (host.go): import.audio
	// and export.start ask the OS for a user-selected path through it.
	// The path never crosses into the web layer; without a registered
	// host the commands report an honest typed error instead.
	dialogs hostDialogs

	// subtitlePath is the user-selected subtitle file for export burn-in
	// (run 46168717789 editing tools). Chosen through the NATIVE dialog;
	// the path is consumed inside Go and never crosses into the web
	// layer (the UI sees "attached"/"none", not the path).
	subtitlePath string

	// rtLog is the shell's LIVE application log (run 46168717789 log
	// catcher): attached once from runShell via BuildApp. The
	// diagnostics surface reports its real writer state and the
	// diagnostics.export command bundles its file. nil = headless/test
	// context (honest resolution-based states, no invented log).
	rtLog *logging.Log
}

// NewService builds the facade over the given studio. A nil studio is
// a programmer error (there is no honest way to serve a core-less web
// shell) and panics like design.MustLoad does for broken tokens.
func NewService(st *studio.Studio) *SheytanService {
	if st == nil {
		panic("wails shell: NewService requires a non-nil *studio.Studio")
	}
	return &SheytanService{
		studio: st,
		settings: presentation.SettingsDTO{
			Intensity: design.MustLoad().DefaultIntensity,
		},
		volume: defaultVolume,
		tokens: design.MustLoad(),
	}
}

// setQuitHandler wires the app.quit command to the owning application.
// Called once from BuildApp; without it, app.quit reports an honest
// "no window host" error instead of pretending to quit.
func (s *SheytanService) setQuitHandler(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.quit = fn
}

// setDialogs wires the native dialog host (host.go). Called once from
// BuildApp; without it, import.audio and export.start report an honest
// "no native dialog host" error instead of pretending.
func (s *SheytanService) setDialogs(d hostDialogs) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dialogs = d
}

// GetBuildInfo returns the normalized build identity snapshot.
func (s *SheytanService) GetBuildInfo() presentation.BuildInfoDTO {
	i := buildinfo.Normalized()
	return presentation.BuildInfoDTO{
		Version:   i.Version,
		Date:      i.Date,
		BuiltBy:   i.BuiltBy,
		GoVersion: i.GoVersion,
	}
}

// GetTokens returns the canonical design tokens as raw JSON bytes
// (design.Raw() verbatim, never re-encoded). The error is part of the
// stable facade signature; Raw() cannot fail and always returns nil.
func (s *SheytanService) GetTokens() (string, error) {
	return string(design.Raw()), nil
}

// GetDestinationCatalog returns the canonical workspace destinations
// with display metadata, including honest empty states.
func (s *SheytanService) GetDestinationCatalog() []presentation.DestinationInfo {
	return presentation.DestinationCatalog()
}

// GetSettings returns the shell-visible settings snapshot.
func (s *SheytanService) GetSettings() presentation.SettingsDTO {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings
}

// SetIntensity validates name against the canonical intensity presets
// (subtle | ritual | infernal) and applies it to the shell settings.
// Unknown names are a typed error, never a silent default.
func (s *SheytanService) SetIntensity(name string) error {
	name = strings.ToLower(strings.TrimSpace(name))
	if _, ok := s.tokens.IntensityPresets[name]; !ok {
		names := make([]string, 0, len(s.tokens.IntensityPresets))
		for n := range s.tokens.IntensityPresets {
			names = append(names, n)
		}
		sort.Strings(names)
		return &FacadeError{
			Kind:   KindInvalidArgs,
			Reason: fmt.Sprintf("unknown intensity %q (want one of: %s)", name, strings.Join(names, ", ")),
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings.Intensity = name
	return nil
}

// SetReducedMotion stores the accessibility motion preference.
func (s *SheytanService) SetReducedMotion(enabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings.ReducedMotion = enabled
}

// SetReducedGlow stores the accessibility glow preference.
func (s *SheytanService) SetReducedGlow(enabled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings.ReducedGlow = enabled
}

// rendererEvidence captures the renderer evidence once and returns the
// cached snapshot as a versioned DTO. SelectRenderer is safe headless;
// when no proven hardware GPU exists the CPU fallback is reported with
// its exact reason — a shell must never upgrade a GPU state.
func (s *SheytanService) rendererEvidence() presentation.RendererDTO {
	s.rendererOnce.Do(func() {
		r, info := render.SelectRenderer()
		if r != nil {
			r.Close()
		}
		s.rendererInfo = info
	})
	info := s.rendererInfo
	return presentation.RendererDTO{
		Backend:     string(info.Backend),
		GpuState:    string(info.GpuState),
		BackendAPI:  info.BackendAPI,
		Adapter:     info.Adapter,
		DeviceType:  info.DeviceType,
		DeviceClass: info.DeviceClass,
		Detail:      info.Detail,
	}
}

// GetRendererEvidence returns the cached renderer evidence DTO.
func (s *SheytanService) GetRendererEvidence() presentation.RendererDTO {
	return s.rendererEvidence()
}

// GetTransportSnapshot returns the bounded transport state snapshot.
// With no loaded track the studio reports a stopped zero clock; the
// volume is the value last set through this shell.
func (s *SheytanService) GetTransportSnapshot() presentation.TransportDTO {
	clock := s.studio.Clock()
	s.mu.Lock()
	volume := s.volume
	s.mu.Unlock()
	return presentation.TransportDTO{
		State:      clock.State.String(),
		Position:   clock.Position,
		Total:      clock.Total,
		Latency:    clock.Latency,
		DeviceReal: clock.DeviceReal,
		Session:    clock.Session,
		Volume:     volume,
	}
}

// GetPresets returns the built-in visualizer presets (name + config).
func (s *SheytanService) GetPresets() []map[string]any {
	presets := s.studio.Presets()
	out := make([]map[string]any, 0, len(presets))
	for _, p := range presets {
		out = append(out, map[string]any{"name": p.Name, "config": p.Config})
	}
	return out
}

// ApplyPreset applies a named preset to the visualizer configuration.
// Unknown names are a typed error listing the available presets.
func (s *SheytanService) ApplyPreset(name string) error {
	if strings.TrimSpace(name) == "" {
		return &FacadeError{Kind: KindInvalidArgs, Reason: "preset name is required"}
	}
	if !s.studio.ApplyPreset(name) {
		names := make([]string, 0)
		for _, p := range s.studio.Presets() {
			names = append(names, p.Name)
		}
		sort.Strings(names)
		return &FacadeError{
			Kind:   KindInvalidArgs,
			Reason: fmt.Sprintf("unknown preset %q (available: %s)", name, strings.Join(names, ", ")),
		}
	}
	return nil
}

// GetLibrarySummary reports the library facts the web shell may know:
// registered formats, track presence (never the file path of the
// track), and the current project path.
func (s *SheytanService) GetLibrarySummary() map[string]any {
	formats := make([]map[string]any, 0)
	for _, f := range s.studio.SupportedFormats() {
		formats = append(formats, map[string]any{
			"container":     f.Container,
			"extensions":    f.Extensions,
			"classifier":    f.Classifier,
			"lossy":         f.Lossy,
			"nativeDecoder": f.NativeDecoder,
		})
	}
	track := s.studio.Track()
	name, duration := "", 0.0
	if track != nil {
		name, duration = track.SourceName, track.Duration
	}
	return map[string]any{
		"formats":              formats,
		"trackLoaded":          track != nil,
		"trackName":            name,
		"trackDurationSeconds": duration,
		"projectPath":          s.studio.ProjectPath(),
		"importing":            s.studio.Importing(),
		"playing":              s.studio.IsPlayingLike(),
	}
}

// GetDiagnostics aggregates the diagnostics surface: build identity,
// data layout (mode + root + log path), renderer evidence, shell
// settings, timestamp and the honest self-check list.
func (s *SheytanService) GetDiagnostics() presentation.DiagnosticsDTO {
	lay := apppaths.Resolve(apppaths.OSEnv())

	checks := make([]presentation.SelfCheckDTO, 0, 6)
	addCheck := func(name string, passed bool, detail string) {
		checks = append(checks, presentation.SelfCheckDTO{Name: name, Passed: passed, Detail: detail})
	}

	if _, err := design.Load(); err != nil {
		addCheck("design-tokens", false, err.Error())
	} else {
		addCheck("design-tokens", true, "canonical tokens decode and validate")
	}

	addCheck("data-layout", lay.DataRoot != "",
		fmt.Sprintf("mode=%s root=%s (%s)", lay.Mode, lay.DataRoot, lay.Reason))

	// Run 46168717789 log catcher: report the LIVE writer state. With
	// an attached log the check proves the file is real; the stderr
	// fallback (log directory unwritable) is an explicit FAIL-class
	// detail, never a silent fake path.
	logPath := ""
	if lg := s.runtimeLog(); lg != nil {
		logPath = lg.Path()
		if lg.IsFallback() {
			addCheck("log-writer", false,
				"runtime log could not be opened; records go to stderr only (nothing to attach) — check the data layout reason above")
		} else {
			addCheck("log-writer", true, "active log "+logPath)
		}
	}
	if logPath == "" {
		if dir, err := logging.Dir(); err != nil {
			addCheck("log-directory", false, err.Error())
		} else {
			logPath = filepath.Join(dir, logging.FileName)
			addCheck("log-directory", true, dir)
		}
	}

	rd := s.rendererEvidence()
	switch render.Backend(rd.Backend) {
	case render.BackendCPU, render.BackendGPU:
		addCheck("renderer-evidence", true,
			fmt.Sprintf("backend=%s gpu-state=%s", rd.Backend, rd.GpuState))
	default:
		addCheck("renderer-evidence", false, "unexpected backend "+rd.Backend)
	}

	// Optional FFmpeg environment: a PASS means the OPTIONAL dependency
	// state was HANDLED CORRECTLY (absence included) — it never implies
	// MP4 export capability.
	if env, err := export.DetectEnv(false); err != nil {
		addCheck("ffmpeg-env", true, "FFmpeg not available (optional): "+err.Error())
	} else {
		addCheck("ffmpeg-env", true, "FFmpeg available: "+env.Version)
	}

	addCheck("studio-core", s.studio != nil, "facade bound to a live studio instance")

	return presentation.DiagnosticsDTO{
		Build:      s.GetBuildInfo(),
		DataMode:   string(lay.Mode),
		DataRoot:   lay.DataRoot,
		LogPath:    logPath,
		Renderer:   rd,
		Settings:   s.GetSettings(),
		CheckedAt:  time.Now().UTC(),
		SelfChecks: checks,
	}
}

// mediaManager builds the mediatools manager once on the resolved data
// root. Construction is passive (state inspection only, no network, no
// goroutines), so first use is safe from any request goroutine.
func (s *SheytanService) mediaManager() *mediatools.Manager {
	s.mediaMu.Lock()
	defer s.mediaMu.Unlock()
	s.mediaOnce.Do(func() {
		lay := apppaths.Resolve(apppaths.OSEnv())
		s.media = mediatools.New(mediatools.Options{DataRoot: lay.DataRoot})
	})
	return s.media
}

// GetMediaToolsStatus returns the REAL app-managed media-tools snapshot:
// state, stage, downloaded bytes, total bytes and throughput for the
// pinned-pack update lifecycle. This is the downloads-pane data source
// (the go-gui shell serves the same snapshot from the same package).
func (s *SheytanService) GetMediaToolsStatus() mediatools.Snapshot {
	return s.mediaManager().Snapshot()
}

// CheckMediaToolsUpdate re-inspects the installed pack against the
// pinned manifest (no download) and returns the refreshed snapshot.
func (s *SheytanService) CheckMediaToolsUpdate() (mediatools.Snapshot, error) {
	snap, err := s.mediaManager().CheckForUpdate(context.Background())
	if err != nil {
		return snap, &FacadeError{Kind: KindRejected, Reason: err.Error()}
	}
	return snap, nil
}

// InstallMediaTools starts the pinned-pack download/verify/install in a
// background goroutine and returns immediately; progress flows through
// GetMediaToolsStatus (state=downloading, stage, bytes). Re-entrant
// calls are refused by the manager itself (already in progress).
func (s *SheytanService) InstallMediaTools() error {
	m := s.mediaManager()
	go func() {
		defer func() {
			if r := recover(); r != nil {
				// The manager cleans up its own state on panic; a
				// background panic must never take the shell down.
				log.Printf("wails shell: mediatools install panicked: %v", r)
			}
		}()
		_ = m.Install(context.Background())
	}()
	return nil
}

// CancelMediaToolsInstall cancels an in-flight install; the manager
// restores the previous stable state and reports it in the snapshot.
func (s *SheytanService) CancelMediaToolsInstall() {
	s.mediaManager().Cancel()
}

// GetCommands returns the honest command catalog: the supported subset
// (with desktop shortcuts) and every refused command with its boundary
// reason.
func (s *SheytanService) GetCommands() CommandCatalog {
	keys := map[presentation.CommandID]string{}
	for _, sc := range presentation.Shortcuts {
		keys[sc.Command] = sc.Keys
	}
	supported := make([]CommandInfo, 0, len(dispatchedCommands))
	for _, id := range dispatchedCommands {
		supported = append(supported, CommandInfo{ID: id, Keys: keys[id]})
	}
	sort.Slice(supported, func(i, j int) bool { return supported[i].ID < supported[j].ID })

	unsupported := make([]CommandInfo, 0, len(unsupportedReasons))
	for id, reason := range unsupportedReasons {
		unsupported = append(unsupported, CommandInfo{ID: id, Reason: reason, Refused: true})
	}
	sort.Slice(unsupported, func(i, j int) bool { return unsupported[i].ID < unsupported[j].ID })

	return CommandCatalog{Supported: supported, Unsupported: unsupported}
}

// commandArgs is the validated argument shape of RunCommand. Pointer
// fields distinguish "absent" from "set" so missing required values
// are rejected instead of silently defaulting. Width/Height/FPS and
// the codec/quality strings back the export.start command (export.NewJob
// validates them again as the authority - the facade validates EARLY
// so the web layer always gets a typed error, never a raw dump).
type commandArgs struct {
	Name          string   `json:"name"`
	Seconds       *float64 `json:"seconds"`
	Volume        *float64 `json:"volume"`
	Enabled       *bool    `json:"enabled"`
	Width         *int     `json:"width"`
	Height        *int     `json:"height"`
	FPS           *int     `json:"fps"`
	VideoCodec    string   `json:"videoCodec"`
	QualityPreset string   `json:"qualityPreset"`
	// Editing tools (run 46168717789): trim window and preview length.
	TrimStart    *float64 `json:"trimStart"`
	TrimDuration *float64 `json:"trimDuration"`
	Preview      *float64 `json:"preview"`
}

// parseCommandArgs decodes the JSON argument object. An empty payload
// is a valid empty argument set; anything that is not a JSON object is
// a typed error.
func parseCommandArgs(argsJSON string) (commandArgs, error) {
	var args commandArgs
	trimmed := strings.TrimSpace(argsJSON)
	if trimmed == "" {
		return args, nil
	}
	dec := json.NewDecoder(strings.NewReader(trimmed))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&args); err != nil {
		return commandArgs{}, &FacadeError{
			Kind:   KindInvalidArgs,
			Reason: fmt.Sprintf("args must be a JSON object with keys name/seconds/volume/enabled/width/height/fps/videoCodec/qualityPreset: %v", err),
		}
	}
	return args, nil
}

// argsError builds an invalid-args error for one command.
func argsError(cmd presentation.CommandID, format string, a ...any) error {
	return &FacadeError{Command: cmd, Kind: KindInvalidArgs, Reason: fmt.Sprintf(format, a...)}
}

// reject wraps a refused core call as a typed rejected error so the
// web layer always receives the actionable message (e.g. the no-track
// guidance), never a bare Go error dump.
func reject(cmd presentation.CommandID, err error) error {
	return &FacadeError{Command: cmd, Kind: KindRejected, Reason: err.Error()}
}

// RunCommand validates the command ID and dispatches the supported
// subset with JSON args. Unknown IDs and unsupported IDs return typed
// errors; supported commands validate their arguments before touching
// the studio.
func (s *SheytanService) RunCommand(id string, argsJSON string) error {
	cmd := presentation.CommandID(strings.TrimSpace(id))
	if err := presentation.ValidateCommand(cmd); err != nil {
		return &FacadeError{Command: cmd, Kind: KindUnknownCommand, Reason: err.Error()}
	}
	if reason, unsupported := unsupportedReasons[cmd]; unsupported {
		return &FacadeError{Command: cmd, Kind: KindUnsupportedCommand, Reason: reason}
	}

	args, err := parseCommandArgs(argsJSON)
	if err != nil {
		return err
	}

	switch cmd {
	case presentation.CmdPlayPause:
		if err := s.studio.PlayPause(); err != nil {
			return reject(cmd, err)
		}
	case presentation.CmdStop:
		if err := s.studio.Stop(); err != nil {
			return reject(cmd, err)
		}
	case presentation.CmdSeekBack, presentation.CmdSeekForward:
		seconds := defaultSeekSeconds
		if args.Seconds != nil {
			seconds = *args.Seconds
		}
		if seconds <= 0 || seconds > maxSeekSeconds {
			return argsError(cmd, "seconds must be in (0, %g], got %g", maxSeekSeconds, seconds)
		}
		delta := time.Duration(seconds * float64(time.Second))
		clock := s.studio.Clock()
		target := clock.Position
		if cmd == presentation.CmdSeekForward {
			target += delta
		} else {
			target -= delta
		}
		if target < 0 {
			target = 0
		}
		if clock.Total > 0 && target > clock.Total {
			target = clock.Total
		}
		if err := s.studio.Seek(target); err != nil {
			return reject(cmd, err)
		}
	case presentation.CmdSetVolume:
		if args.Volume == nil {
			return argsError(cmd, "volume is required (number in [0,1])")
		}
		v := *args.Volume
		if v < 0 || v > 1 {
			return argsError(cmd, "volume must be in [0,1], got %g", v)
		}
		s.studio.SetVolume(v)
		s.mu.Lock()
		s.volume = v
		s.mu.Unlock()
	case presentation.CmdPresetApply:
		if err := s.ApplyPreset(args.Name); err != nil {
			return err
		}
	case presentation.CmdIntensitySet:
		if args.Name == "" {
			return argsError(cmd, "intensity name is required (subtle|ritual|infernal)")
		}
		if err := s.SetIntensity(args.Name); err != nil {
			return err
		}
	case presentation.CmdReducedMotionSet:
		if args.Enabled == nil {
			return argsError(cmd, "enabled is required (true|false)")
		}
		s.SetReducedMotion(*args.Enabled)
	case presentation.CmdReducedGlowSet:
		if args.Enabled == nil {
			return argsError(cmd, "enabled is required (true|false)")
		}
		s.SetReducedGlow(*args.Enabled)
	case presentation.CmdImportAudio:
		return s.runImportAudio(cmd)
	case presentation.CmdExportStart:
		return s.runExportStart(cmd, args)
	case presentation.CmdExportCancel:
		return s.runExportCancel(cmd)
	case presentation.CmdExportPreview:
		return s.runExportPreview(cmd, args)
	case presentation.CmdExportSubtitle:
		return s.runExportSubtitleAttach(cmd)
	case presentation.CmdExportSubtitleClear:
		s.mu.Lock()
		s.subtitlePath = ""
		s.mu.Unlock()
		return nil
	case presentation.CmdDiagnosticsExport:
		return s.runDiagnosticsExport(cmd)
	case presentation.CmdQuit:
		s.mu.Lock()
		quit := s.quit
		s.mu.Unlock()
		if quit == nil {
			return &FacadeError{Command: cmd, Kind: KindRejected,
				Reason: "no window host registered (headless or test context cannot quit a process)"}
		}
		quit()
	default:
		// Exhaustiveness guard: dispatchedCommands and the switch must
		// stay in sync; unknown-but-canonical IDs are a programming bug.
		return &FacadeError{Command: cmd, Kind: KindUnsupportedCommand,
			Reason: "known command without a web dispatch (facade bug)"}
	}
	return nil
}

/* ------------------------------------------------------------------ */
/* /api/* HTTP mux over the same facade                                */
/* ------------------------------------------------------------------ */

// apiError is the JSON error body of every refused /api request.
type apiError struct {
	Error   string `json:"error"`
	Kind    string `json:"kind,omitempty"`
	Command string `json:"command,omitempty"`
}

// facadeStatus maps a FacadeError kind onto an HTTP status.
func facadeStatus(err error) (int, apiError) {
	var fe *FacadeError
	if errors.As(err, &fe) {
		status := http.StatusBadRequest
		if fe.Kind == KindRejected {
			status = http.StatusConflict
		}
		return status, apiError{Error: fe.Reason, Kind: fe.Kind, Command: string(fe.Command)}
	}
	return http.StatusInternalServerError, apiError{Error: err.Error()}
}

// writeJSON writes one JSON value with the given status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeFacadeError writes the typed error as JSON.
func writeFacadeError(w http.ResponseWriter, err error) {
	status, body := facadeStatus(err)
	writeJSON(w, status, body)
}

// commandRequest is the POST /api/command body.
type commandRequest struct {
	ID   string          `json:"id"`
	Args json.RawMessage `json:"args"`
}

// NewAssetHandler builds the complete asset-server handler: the /api/*
// endpoints over the SAME facade methods the Wails binding exposes,
// and the embedded frontend files for everything else (index.html is
// served for "/"). The returned handler is safe for concurrent use.
func NewAssetHandler(svc *SheytanService) http.Handler {
	api := http.NewServeMux()
	get := func(pattern string, fn func(http.ResponseWriter, *http.Request) error) {
		api.HandleFunc("GET "+pattern, func(w http.ResponseWriter, r *http.Request) {
			if err := fn(w, r); err != nil {
				writeFacadeError(w, err)
			}
		})
	}

	get("/api/build-info", func(w http.ResponseWriter, _ *http.Request) error {
		writeJSON(w, http.StatusOK, svc.GetBuildInfo())
		return nil
	})
	get("/api/tokens", func(w http.ResponseWriter, _ *http.Request) error {
		raw, err := svc.GetTokens()
		if err != nil {
			return err
		}
		writeJSON(w, http.StatusOK, json.RawMessage(raw))
		return nil
	})
	get("/api/destinations", func(w http.ResponseWriter, _ *http.Request) error {
		writeJSON(w, http.StatusOK, map[string]any{"destinations": svc.GetDestinationCatalog()})
		return nil
	})
	get("/api/settings", func(w http.ResponseWriter, _ *http.Request) error {
		writeJSON(w, http.StatusOK, svc.GetSettings())
		return nil
	})
	get("/api/renderer", func(w http.ResponseWriter, _ *http.Request) error {
		writeJSON(w, http.StatusOK, svc.GetRendererEvidence())
		return nil
	})
	get("/api/transport", func(w http.ResponseWriter, _ *http.Request) error {
		writeJSON(w, http.StatusOK, svc.GetTransportSnapshot())
		return nil
	})
	get("/api/presets", func(w http.ResponseWriter, _ *http.Request) error {
		writeJSON(w, http.StatusOK, map[string]any{"presets": svc.GetPresets()})
		return nil
	})
	get("/api/library", func(w http.ResponseWriter, _ *http.Request) error {
		writeJSON(w, http.StatusOK, svc.GetLibrarySummary())
		return nil
	})
	get("/api/diagnostics", func(w http.ResponseWriter, _ *http.Request) error {
		writeJSON(w, http.StatusOK, svc.GetDiagnostics())
		return nil
	})
	get("/api/commands", func(w http.ResponseWriter, _ *http.Request) error {
		writeJSON(w, http.StatusOK, svc.GetCommands())
		return nil
	})

	// App-managed media-tools lifecycle (downloads pane): real bytes and
	// real states for the pinned FFmpeg/FFprobe update.
	get("/api/mediatools", func(w http.ResponseWriter, _ *http.Request) error {
		writeJSON(w, http.StatusOK, svc.GetMediaToolsStatus())
		return nil
	})
	api.HandleFunc("POST /api/mediatools/check", func(w http.ResponseWriter, _ *http.Request) {
		snap, err := svc.CheckMediaToolsUpdate()
		if err != nil {
			writeFacadeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, snap)
	})
	api.HandleFunc("POST /api/mediatools/install", func(w http.ResponseWriter, _ *http.Request) {
		if err := svc.InstallMediaTools(); err != nil {
			writeFacadeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, svc.GetMediaToolsStatus())
	})
	api.HandleFunc("POST /api/mediatools/cancel", func(w http.ResponseWriter, _ *http.Request) {
		svc.CancelMediaToolsInstall()
		writeJSON(w, http.StatusOK, svc.GetMediaToolsStatus())
	})

	// System profile + secure network status (run 45678113841): the
	// REAL machine snapshot and the immutable origin/security policy.
	// The probe is USER-TRIGGERED only (POST) and bounded; nothing
	// here probes automatically or on a timer.
	get("/api/system", func(w http.ResponseWriter, _ *http.Request) error {
		writeJSON(w, http.StatusOK, svc.GetSystemProfile())
		return nil
	})
	get("/api/network", func(w http.ResponseWriter, _ *http.Request) error {
		writeJSON(w, http.StatusOK, svc.GetNetworkStatus())
		return nil
	})
	api.HandleFunc("POST /api/network/probe", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, svc.ProbeNetwork())
	})

	// Song DNA + export status surfaces (repair/run-46033044814): the
	// real measured DNA of the loaded track and the real export job
	// state. Both are read-only projections of studio state.
	get("/api/songdna", func(w http.ResponseWriter, _ *http.Request) error {
		writeJSON(w, http.StatusOK, svc.GetSongDNA())
		return nil
	})

	// Worlds tab (run 46168717789): the real per-track multi-scene
	// world plan (mvplan) with honest no-track/pending/ready states.
	get("/api/worlds", func(w http.ResponseWriter, _ *http.Request) error {
		writeJSON(w, http.StatusOK, svc.GetWorlds())
		return nil
	})
	get("/api/export", func(w http.ResponseWriter, _ *http.Request) error {
		writeJSON(w, http.StatusOK, svc.GetExportStatus())
		return nil
	})

	// Unknown /api paths are an explicit 404 JSON error, never an
	// HTML page (the web UI reports them honestly).
	api.HandleFunc("GET /api/", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotFound, apiError{Error: "unknown API endpoint", Kind: "not-found"})
	})
	api.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusMethodNotAllowed,
			apiError{Error: fmt.Sprintf("method %s not allowed on API endpoints", r.Method), Kind: "method-not-allowed"})
	})

	api.HandleFunc("POST /api/command", func(w http.ResponseWriter, r *http.Request) {
		var req commandRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest,
				apiError{Error: "body must be a JSON object {id, args}", Kind: KindInvalidArgs})
			return
		}
		if strings.TrimSpace(req.ID) == "" {
			writeJSON(w, http.StatusBadRequest,
				apiError{Error: "command id is required", Kind: KindInvalidArgs})
			return
		}
		if err := svc.RunCommand(req.ID, string(req.Args)); err != nil {
			writeFacadeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	// Everything that is not /api/* is a static frontend asset from
	// the embedded dist package (stdlib file server; "/" serves
	// index.html). In the real window the Wails middlewares intercept
	// /wails/* before this handler runs, so the runtime endpoints are
	// unaffected either way.
	static := http.FileServerFS(dist.FS())
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			api.ServeHTTP(w, r)
			return
		}
		static.ServeHTTP(w, r)
	})
}
