// studio.go — the SHEYTAN studio shell: top bar, left library/presets
// rail, central live visualizer (actual renderer output), right
// inspector with real controls, and bottom transport driven by the
// audio clock. Dialogs and the render loop live in actions.go.
//
// UI-thread rules: goroutines owned by the studio service emit events;
// every widget mutation from those events goes through fyne.Do. Widget
// callbacks contain no business logic — they only call the studio
// service.

package fyneapp

import (
	"fmt"
	"image/color"
	"log/slog"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/appclose"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/apppaths"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/brand"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/buildinfo"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/export"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/fullscreen"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/logging"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/perf"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/playback"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/render"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/studio"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/sysprofile"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/visualizer"
)

// Run starts the full studio application (replaces the Phase 1
// placeholder). It blocks until the window is closed.
//
// Runtime-logging contract: the data layout is resolved and the
// runtime log opened BEFORE any subsystem or window construction, and
// a broken log file never aborts the launch (stderr fallback).
func Run() error {
	// Startup instrumentation (run 42837709122 §5.1): the recorder is
	// anchored at the true OS process start (procfs / GetProcessTimes;
	// honest fallback documented in internal/perf) so the first mark
	// already carries the full cold-start elapsed time.
	startup := perf.NewRecorder()
	startup.Mark("runtime-entry")

	lg, lay, logErr := initRuntime()
	defer lg.Close()
	startup.Mark("logger-and-data-paths")
	log := lg.Logger()
	if logErr != nil {
		// Already logged to the stderr fallback by initRuntime/logging.
		// The startup failure path below remains fully visible.
		log.Warn("runtime file log unavailable; using stderr fallback", "err", logErr.Error())
	} else {
		log.Info("runtime log opened", "path", lg.Path())
	}
	// Process-start identity + resolved layout (run-42921967187
	// logging contract): the very first structured records carry the
	// product version/commit and the exact app-data/log directories,
	// so a support bundle can be attributed without any other source.
	bi := buildinfo.Normalized()
	log.Info("application starting",
		"version", bi.Version,
		"commit", bi.Commit,
		"built_by", bi.BuiltBy,
		"built_date", bi.Date,
		"data_mode", lay.Mode,
		"data_root", lay.DataRoot,
		"log_dir", lay.Logs)

	a := app.NewWithID(brand.AppID)
	w := a.NewWindow(brand.WindowTitle())
	w.SetPadded(false)
	startup.Mark("app-shell-created")

	svc, err := studio.New()
	if err != nil {
		log.Error("studio service initialization failed", "err", err.Error())
		return err
	}
	startup.Mark("studio-service-and-audio-init")
	u := newStudioUI(w, svc)
	u.app = a
	u.log = log
	u.lay = lay
	u.logFile = lg
	// Persisted renderer preference (revalidated EVERY launch —
	// hardware and drivers change; an unresolvable manual GPU choice
	// falls back to CPU with the exact reason recorded by the selector).
	u.prefStore = render.OpenPreferenceStore(prefStorePath(lay))
	u.startup = startup
	// Persisted resource policy mode (default Auto). Loaded before
	// the first bounded decision point and logged with its source.
	u.policyStore = OpenPolicyStore(policyStorePath(lay))
	if mode, found, perr := u.policyStore.Load(); perr != nil {
		u.policyMode = sysprofile.ModeAuto
		log.Warn("stored system policy unreadable; auto remains active",
			"err", perr.Error(), "path", policyStorePath(lay))
	} else if found {
		u.policyMode = mode
	} else {
		u.policyMode = sysprofile.ModeAuto
	}
	u.rendererReq = make(chan render.Preference, 1)
	// The persisted preference is READ here (fast file read) but applied
	// in the background selection inside the render loop, so neither the
	// GPU probe nor the explicit selection can delay the first frame.
	if stored, found, perr := u.prefStore.Load(); perr != nil {
		log.Warn("stored renderer preference unreadable; auto remains active",
			"err", perr.Error(), "path", prefStorePath(lay))
	} else if found {
		u.storedPref = stored
		u.storedPrefFound = true
	}
	u.fs = fullscreen.New(fsWindow{w: w},
		fullscreen.NewFileStore(windowStateStorePath(lay)), log)

	// ONE authoritative shutdown path (run 43090342995 lifecycle): the
	// close intercept, Ctrl+Q and OS termination all enter the same
	// state machine. Teardown stages run OFF the Fyne event loop with
	// bounded deadlines; the explicit Window.Close() happens only after
	// cleanup completed, and the UI stays responsive meanwhile.
	u.closeCtl = appclose.New(appclose.Hooks{
		Snapshot: func() appclose.WorkReport {
			// The studio service is the single synchronized owner of
			// the export job; reading it there (instead of the
			// dialog-scoped UI field) keeps shutdown correct even
			// when the export dialog was closed mid-run.
			job := u.svc.ExportJob()
			return appclose.WorkReport{
				Playback: u.svc.Clock().State == playback.StatePlaying,
				Export:   job != nil && job.Running(),
				Startup:  u.svc.Importing(),
			}
		},
		Confirm: func(report appclose.WorkReport, proceed func()) {
			u.showCloseConfirmation(report, proceed)
		},
		CancelWork: func() error {
			// Stop playback, cancel import/analysis and the queued or
			// running export. Each is app-owned; none is force-killed.
			u.svc.CancelImport()
			_ = u.svc.Stop()
			if job := u.svc.ExportJob(); job != nil && job.Running() {
				job.Cancel()
			}
			return nil
		},
		Flush: func() error {
			// Persist the fullscreen intent and write the final shutdown
			// record BEFORE the logger closes (log-flush contract).
			u.fs.SyncObserved("close-intercept")
			u.logInfo("shutdown", "stage", "flush", "clean", true)
			return nil
		},
		Release: func() error {
			u.close()
			u.svc.Close()
			return nil
		},
		Finalize: func() {
			// The intercept replaced the automatic close, so the window
			// MUST be closed explicitly once cleanup completed — exactly
			// once, on the event loop via fyne.Do.
			fyne.Do(u.win.Close)
		},
	}, 8*time.Second, func(msg string, args ...any) { u.logInfo(msg, args...) })
	w.SetCloseIntercept(func() {
		u.closeCtl.RequestClose("titlebar")
	})

	// File menu: the documented Fyne surface for a Quit action. Quit
	// enters the SAME close state machine as the titlebar intercept.
	w.SetMainMenu(fyne.NewMainMenu(fyne.NewMenu("File",
		fyne.NewMenuItem("Quit", func() {
			u.closeCtl.RequestClose("menu-quit")
		}),
	)))

	w.SetContent(u.build())
	startup.Mark("ui-shell-built")
	installTransportKeys(w, u)
	w.Resize(fyne.NewSize(1440, 860))

	// Fullscreen restore happens AFTER the window is shown and the
	// event loop is running (the correct Fyne moment — never before
	// the window is ready, never via a guessed monitor resize).
	a.Lifecycle().SetOnStarted(func() {
		// First event-loop iteration ≈ first usable window: the
		// honest "first visible/usable main window" stage.
		startup.Mark("first-window-usable")
		u.fs.ApplyPersistedAtStartup("launch-restore")
		// One structured startup record — stages, anchor provenance
		// and the never-polled steady-state runtime snapshot.
		startup.Mark("startup-recorded")
		log.Info("startup stages complete", u.startup.KVPairs()...)
		log.Info("runtime snapshot",
			"gomaxprocs", runtime.GOMAXPROCS(0),
			"num_cpu", runtime.NumCPU(),
			"heap_alloc_bytes", heapAllocBytes(),
			"gc_cycles", numGCPackages())
		startup.Close()
	})

	log.Info("window shown",
		"renderer", rendererSummary(u.rendererInfo),
		"fullscreen_desired", u.fs.Desired(),
		"fullscreen_observed", u.fs.Observed())
	w.ShowAndRun()

	// The window is closed: the close controller already ran cancel →
	// flush → release → finalize (exactly once). Only the final evidence
	// record and the log close remain here; a second teardown would be a
	// contract violation, so the controller result is authoritative.
	res := u.closeCtl.Result()
	u.fs.SyncObserved("shutdown")
	log.Info("shutdown",
		"clean", res.FlushErr == nil && res.CancelErr == nil && res.ReleaseErr == nil && !res.TimedOut,
		"timed_out", res.TimedOut,
		"cancel_err", errString(res.CancelErr),
		"flush_err", errString(res.FlushErr),
		"release_err", errString(res.ReleaseErr),
		// duration_ms truncates sub-millisecond teardowns to 0
		// (the run-43541087097 Windows regression); duration
		// carries the precise value (>= MinCompletedDuration).
		"duration", res.Duration.String(),
		"duration_ms", res.Duration.Milliseconds(),
		"fullscreen_observed", u.fs.Observed())
	svc.Close() // idempotent; defensive final release of the transport
	return nil
}

// errString renders an error for the shutdown evidence record.
func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// installTransportKeys wires the keyboard transport shortcuts
// (roadmap Phase 2 #1: keyboard navigation beyond tab order):
//
//	Space        play/pause toggle
//	Left/Right   seek 5 s backward/forward (clamped to the track)
//	F11          fullscreen toggle (single controller, persisted intent)
//	Escape       exit fullscreen (only while fullscreen is observed;
//	             dialogs consume Escape first while one is open)
//
// The shortcuts dispatch through the same service actions as the
// transport buttons — no duplicate logic, no direct widget writes.
func installTransportKeys(w fyne.Window, u *studioUI) {
	w.Canvas().SetOnTypedKey(func(e *fyne.KeyEvent) {
		switch e.Name {
		case fyne.KeySpace:
			u.onPlayPause()
		case fyne.KeyLeft:
			u.seekBy(-5 * time.Second)
		case fyne.KeyRight:
			u.seekBy(5 * time.Second)
		case fyne.KeyF11:
			u.fs.Toggle("f11")
		case fyne.KeyEscape:
			// Explicit user exit: the controller clears the persisted
			// intent so no later reconcile re-enters fullscreen.
			if u.fs.Observed() {
				u.fs.Exit("escape")
			}
		}
	})
}

// seekBy seeks relative to the current playhead, clamped to
// [0, total]. No-op when no track is loaded (total 0).
func (u *studioUI) seekBy(delta time.Duration) {
	clk := u.svc.Clock()
	if clk.Total <= 0 {
		return
	}
	target := clk.Position + delta
	if target < 0 {
		target = 0
	}
	if target > clk.Total {
		target = clk.Total
	}
	_ = u.svc.Seek(target)
}

// studioUI holds the wired widgets and state.
type studioUI struct {
	win fyne.Window
	svc *studio.Studio

	// runtime diagnostics state (Phase: runtime logging users can attach)
	app     fyne.App
	log     *slog.Logger
	lay     apppaths.Layout
	logFile *logging.Log
	fs      *fullscreen.Controller

	// top bar
	trackLabel  *widget.Label
	statusLabel *widget.Label

	// honest renderer identity for the selected backend
	rendererInfo render.BackendInfo

	// center preview
	preview    *canvas.Image
	emptyState *widget.Label

	// transport
	btnPlay    *widget.Button
	btnStop    *widget.Button
	timeCur    *widget.Label
	timeTot    *widget.Label
	scrubber   *widget.Slider
	scrubbing  bool
	lastScrub  time.Time
	volumeSl   *widget.Slider
	analysisLB *widget.Label
	deviceLB   *widget.Label
	rendererLB *widget.Label

	// inspector
	modeSelect  *widget.Select
	sensSl      *widget.Slider
	smoothSl    *widget.Slider
	glowSl      *widget.Slider
	paletteSl   *widget.Slider
	densitySl   *widget.Slider
	pulseSl     *widget.Slider
	titleEntry  *widget.Entry
	subEntry    *widget.Entry
	showTitleCb *widget.Check
	watermarkCb *widget.Check
	dnaLabel    *widget.Label

	// layered composition + manual tempo/offset correction
	bgLayerSelect *widget.Select
	bgIntensitySl *widget.Slider
	tempoEntry    *widget.Entry
	offsetEntry   *widget.Entry

	// export dialog state
	exportDialog dialog.Dialog
	exportBar    *widget.ProgressBar
	exportLabel  *widget.Label
	exportJob    *export.Job

	renderer render.Renderer
	renderMu sync.Mutex
	closing  bool

	// System Integration state (run 42598929646): the persisted
	// renderer preference store, the in-main-window dialog, and the
	// request channel through which ALL renderer lifecycle changes are
	// performed by the render loop goroutine (no mid-frame swaps).
	prefStore     *render.PreferenceStore
	sysDialog     dialog.Dialog
	rendererReq   chan render.Preference
	rendererReqMu sync.Mutex

	// Resource policy state (run 42837709122 §4.3): the persisted
	// mode store and the live mode snapshot. Guidance is read at
	// bounded decision points; the mutex only guards the snapshot.
	policyStore *PolicyStore
	policyMu    sync.Mutex
	policyMode  sysprofile.PerformanceMode

	// Startup instrumentation (run 42837709122 §5.1): one recorder
	// anchored at the true OS process start, marked along the
	// startup path, logged once after the window is usable.
	startup *perf.Recorder

	// Close/shutdown authority (run 43090342995 lifecycle contract):
	// ONE state machine behind the close intercept, Ctrl+Q, and OS
	// termination. It owns confirm-once, bounded cancel/flush/release
	// and the explicit Window.Close() finalization.
	closeCtl  *appclose.Controller
	closeOnce sync.Once

	// renderer probe deferral state (run 43090342995 startup contract)
	storedPref      render.Preference
	storedPrefFound bool
	selectOnce      sync.Once

	// Responsive regions (run 43090342995 UI/UX contract): ONE set of
	// shared widget instances, recomposed per breakpoint by the
	// responsive root. regions maps the active composition's named
	// regions (header/transport/preview/inspector/library/actions) for
	// the viewport tests.
	regions map[string]fyne.CanvasObject

	// shared region objects, built once and re-parented per breakpoint
	regionHeader     fyne.CanvasObject
	regionTransportC fyne.CanvasObject
	regionTransportF fyne.CanvasObject
	regionPreview    fyne.CanvasObject
	regionLibraryS   fyne.CanvasObject
	libraryContent   fyne.CanvasObject
	inspectorContent fyne.CanvasObject
	actionButtons    []*widget.Button
	rootResp         *responsiveRoot
}

func newStudioUI(w fyne.Window, svc *studio.Studio) *studioUI {
	// Renderer probe deferral (run 43090342995 startup contract): the
	// GPU/adapter enumeration (wgpu instance) runs in the render-loop
	// goroutine AFTER the window is visible, never before the first
	// frame. Until then the shell shows the honest "probing" state.
	return &studioUI{
		win:          w,
		svc:          svc,
		rendererInfo: probingRendererInfo(),
	}
}

// probingRendererInfo is the truthful pre-selection state: no backend
// has been chosen yet, nothing is claimed.
func probingRendererInfo() render.BackendInfo {
	return render.BackendInfo{
		Detail: "renderer probe deferred off the first-frame path; selecting in the background",
	}
}

// build constructs the shared widget set and returns the responsive
// root that recomposes it per breakpoint (run 43090342995 UI/UX).
func (u *studioUI) build() fyne.CanvasObject {
	u.buildRegions()
	u.rootResp = newResponsiveRoot(u, fyne.NewSize(1440, 860))
	u.regions = map[string]fyne.CanvasObject{}
	u.applyRegions(u.rootResp.bp)
	u.syncInspector()
	u.subscribe()
	u.startRenderLoop()
	return u.rootResp
}

// buildRegions creates every shared widget once. Compositions re-parent
// these instances per breakpoint; no widget tree is rebuilt for a
// breakpoint change, so user state survives resizes.
func (u *studioUI) buildRegions() {
	// ---- top bar
	brandMark := canvas.NewText(brand.BrandTag, hexToColor(brand.ColorInfernalRed))
	brandMark.TextStyle = fyne.TextStyle{Bold: true}
	brandMark.TextSize = 20
	u.trackLabel = widget.NewLabel("No track imported")
	u.trackLabel.TextStyle = fyne.TextStyle{Bold: true}
	u.statusLabel = widget.NewLabel("Ready — import audio to begin")
	u.statusLabel.TextStyle = fyne.TextStyle{Italic: true}

	btnImport := widget.NewButton("Import Audio…", u.onImport)
	btnOpen := widget.NewButton("Open Project…", u.onOpenProject)
	btnSave := widget.NewButton("Save Project", u.onSaveProject)
	btnExport := widget.NewButton("Export MP4…", u.onExport)
	btnSystem := widget.NewButton("System Integration", u.onSystemIntegration)
	btnHelp := widget.NewButton("Help / Diagnostics", u.onHelp)
	btnAbout := widget.NewButton("About", u.onAbout)
	u.actionButtons = []*widget.Button{btnImport, btnOpen, btnSave, btnExport, btnSystem, btnHelp, btnAbout}

	// Long titles must never push the layout: single-line ellipsis
	// truncation (wrapping + truncation together is a known Fyne RichText
	// crash combination, so truncation is used alone here).
	u.trackLabel.Truncation = fyne.TextTruncateEllipsis
	u.statusLabel.Truncation = fyne.TextTruncateEllipsis

	u.regionHeader = container.NewBorder(nil, nil,
		container.NewHBox(brandMark, widget.NewLabel(" "), u.trackLabel),
		nil,
		u.statusLabel,
	)

	// ---- left rail: formats + presets
	formatInfo := widget.NewLabel("")
	formatInfo.Wrapping = fyne.TextWrapWord
	formatInfo.TextStyle = fyne.TextStyle{Monospace: true}
	names := ""
	for i, f := range u.svc.SupportedFormats() {
		if i > 0 {
			names += ", "
		}
		names += "." + f.Extensions[0]
	}
	formatInfo.SetText("Supported input:\n" + names)

	presetsBox := container.NewVBox()
	for _, p := range u.svc.Presets() {
		name := p.Name
		presetsBox.Add(widget.NewButton(name, func() {
			if u.svc.ApplyPreset(name) {
				u.syncInspector()
				u.status("Preset applied: " + name)
			}
		}))
	}
	u.libraryContent = container.NewVBox(
		widget.NewLabelWithStyle("Library", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		formatInfo,
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Presets", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		presetsBox,
	)
	u.regionLibraryS = container.NewVScroll(u.libraryContent)

	// ---- center preview (actual renderer output)
	bg := canvas.NewRectangle(hexToColor(brand.ColorObsidian))
	u.preview = canvas.NewImageFromImage(nil)
	u.preview.FillMode = canvas.ImageFillContain
	u.emptyState = widget.NewLabel("Import a track to see the visualizer.\nThe preview shows the actual renderer output.")
	u.emptyState.Alignment = fyne.TextAlignCenter
	u.emptyState.Wrapping = fyne.TextWrapWord
	u.regionPreview = container.NewStack(bg,
		container.NewCenter(container.NewVBox(u.emptyState)),
		container.NewStack(u.preview))

	// ---- right inspector
	u.modeSelect = widget.NewSelect(modeNames(), func(s string) {
		cfg := u.svc.Config()
		cfg.Mode = modeFromName(s)
		u.svc.SetConfig(cfg)
	})
	u.sensSl = u.newSlider(0.2, 3.0, func(v float64) {
		cfg := u.svc.Config()
		cfg.Sensitivity = v
		u.svc.SetConfig(cfg)
	})
	u.smoothSl = u.newSlider(0, 0.9, func(v float64) {
		cfg := u.svc.Config()
		cfg.Smoothing = v
		u.svc.SetConfig(cfg)
	})
	u.glowSl = u.newSlider(0, 1.5, func(v float64) {
		cfg := u.svc.Config()
		cfg.Glow = v
		u.svc.SetConfig(cfg)
	})
	u.paletteSl = u.newSlider(0, 3, func(v float64) {
		cfg := u.svc.Config()
		cfg.Palette = int(v)
		u.svc.SetConfig(cfg)
	})
	u.densitySl = u.newSlider(0, 1, func(v float64) {
		cfg := u.svc.Config()
		cfg.ParticleDensity = v
		u.svc.SetConfig(cfg)
	})
	u.pulseSl = u.newSlider(0.5, 1.8, func(v float64) {
		cfg := u.svc.Config()
		cfg.PulseResponse = v
		u.svc.SetConfig(cfg)
	})
	u.bgLayerSelect = widget.NewSelect(layerNames(), func(sel string) {
		cfg := u.svc.Config()
		cfg.BackgroundLayer = layerFromName(sel)
		u.svc.SetConfig(cfg)
	})
	u.bgIntensitySl = u.newSlider(0, visualizer.MaxBackgroundIntensity, func(v float64) {
		cfg := u.svc.Config()
		cfg.BackgroundIntensity = v
		u.svc.SetConfig(cfg)
	})
	u.tempoEntry = widget.NewEntry()
	u.tempoEntry.SetPlaceHolder("0 = measured BPM")
	u.tempoEntry.OnChanged = func(txt string) {
		if v, err := strconv.ParseFloat(strings.TrimSpace(txt), 64); err == nil {
			u.svc.SetTempoOverride(v)
		}
	}
	u.offsetEntry = widget.NewEntry()
	u.offsetEntry.SetPlaceHolder("\u00b15.0 s")
	u.offsetEntry.OnChanged = func(txt string) {
		if v, err := strconv.ParseFloat(strings.TrimSpace(txt), 64); err == nil {
			u.svc.SetSyncOffset(v)
		}
	}
	u.titleEntry = widget.NewEntry()
	u.titleEntry.SetPlaceHolder("Title overlay text")
	u.titleEntry.OnChanged = func(s string) {
		cfg := u.svc.Config()
		cfg.TitleText = s
		u.svc.SetConfig(cfg)
	}
	u.subEntry = widget.NewEntry()
	u.subEntry.SetPlaceHolder("Subtitle (artist)")
	u.subEntry.OnChanged = func(s string) {
		cfg := u.svc.Config()
		cfg.SubtitleText = s
		u.svc.SetConfig(cfg)
	}
	u.showTitleCb = widget.NewCheck("Show title overlay", func(on bool) {
		cfg := u.svc.Config()
		cfg.ShowTitle = on
		u.svc.SetConfig(cfg)
	})
	u.watermarkCb = widget.NewCheck("SHEYTAN™ watermark", func(on bool) {
		cfg := u.svc.Config()
		cfg.ShowWatermark = on
		u.svc.SetConfig(cfg)
	})
	u.dnaLabel = widget.NewLabel("Song DNA: —")
	u.dnaLabel.Wrapping = fyne.TextWrapWord
	u.dnaLabel.TextStyle = fyne.TextStyle{Italic: true}

	u.inspectorContent = container.NewVBox(
		widget.NewLabelWithStyle("Inspector", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewForm(
			widget.NewFormItem("Mode", u.modeSelect),
			widget.NewFormItem("Sensitivity", u.sensSl),
			widget.NewFormItem("Smoothing", u.smoothSl),
			widget.NewFormItem("Glow", u.glowSl),
			widget.NewFormItem("Palette", u.paletteSl),
			widget.NewFormItem("Particle density", u.densitySl),
			widget.NewFormItem("Pulse response", u.pulseSl),
			widget.NewFormItem("Background layer", u.bgLayerSelect),
			widget.NewFormItem("Layer intensity", u.bgIntensitySl),
			widget.NewFormItem("Title", u.titleEntry),
			widget.NewFormItem("Subtitle", u.subEntry),
			widget.NewFormItem("Tempo override", u.tempoEntry),
			widget.NewFormItem("Sync offset", u.offsetEntry),
		),
		u.showTitleCb, u.watermarkCb,
		widget.NewSeparator(),
		u.dnaLabel,
	)

	// ---- bottom transport
	u.timeCur = widget.NewLabel("0:00")
	u.timeTot = widget.NewLabel("0:00")
	u.scrubber = widget.NewSlider(0, 1000)
	u.scrubber.Step = 1
	u.scrubber.OnChanged = func(v float64) {
		// Throttled live seek; programmatic SetValue (render loop) does
		// not fire OnChanged in Fyne, so every change here is user drag.
		now := time.Now()
		if now.Sub(u.lastScrub) < 60*time.Millisecond {
			return
		}
		u.lastScrub = now
		total := u.svc.Clock().Total
		_ = u.svc.Seek(time.Duration(v / 1000 * float64(total)))
	}
	u.btnPlay = widget.NewButton("▶ Play", u.onPlayPause)
	u.btnStop = widget.NewButton("■ Stop", func() { _ = u.svc.Stop() })
	u.volumeSl = u.newSlider(0, 1, func(v float64) { u.svc.SetVolume(v) })
	u.volumeSl.SetValue(0.9)
	u.analysisLB = widget.NewLabel("analysis: —")
	u.deviceLB = widget.NewLabel("")
	u.deviceLB.TextStyle = fyne.TextStyle{Italic: true}
	u.rendererLB = widget.NewLabel("")
	u.rendererLB.TextStyle = fyne.TextStyle{Italic: true}
	u.rendererLB.SetText("renderer: " + rendererSummary(u.rendererInfo))

	// Full (desktop/wide) transport: one row, three zones.
	u.regionTransportF = container.NewVBox(
		widget.NewSeparator(),
		container.NewHBox(u.deviceLB, u.rendererLB),
		container.NewBorder(nil, nil,
			container.NewHBox(u.btnPlay, u.btnStop),
			container.NewHBox(widget.NewLabel("Vol"), u.volumeSl, u.analysisLB),
			container.NewHBox(u.timeCur, layout.NewSpacer(), u.scrubber, layout.NewSpacer(), u.timeTot),
		),
	)

	// Compact (phone) transport: stacked rows; status labels truncate so
	// nothing can push the layout wide.
	u.deviceLB.Truncation = fyne.TextTruncateEllipsis
	u.rendererLB.Truncation = fyne.TextTruncateEllipsis
	u.regionTransportC = container.NewVBox(
		widget.NewSeparator(),
		container.NewHBox(u.deviceLB, u.rendererLB),
		container.NewHBox(u.btnPlay, u.btnStop, widget.NewLabel(" "), u.timeCur, u.timeTot),
		u.scrubber,
		container.NewHBox(widget.NewLabel("Vol"), u.volumeSl, u.analysisLB),
	)
}

// applyRegions publishes the named regions of an active composition for
// the viewport tests (bounds/overlap assertions).
func (u *studioUI) applyRegions(bp breakpoint) {
	switch bp {
	case bpPhone, bpCompact:
		u.regions = map[string]fyne.CanvasObject{
			"header":    u.regionHeader,
			"transport": u.regionTransportC,
			"preview":   u.regionPreview,
			"inspector": u.inspectorContent,
			"library":   u.libraryContent,
		}
	default:
		u.regions = map[string]fyne.CanvasObject{
			"header":    u.regionHeader,
			"transport": u.regionTransportF,
			"preview":   u.regionPreview,
			"inspector": u.inspectorContent,
			"library":   u.libraryContent,
		}
	}
}

// compose builds the layout for one breakpoint out of the shared
// regions. All compositions use built-in layout primitives plus the two
// unit-tested helpers (minSizeWrapper, maxWidthLayout); no absolute
// placement anywhere.
func (u *studioUI) compose(bp breakpoint) fyne.CanvasObject {
	u.applyRegions(bp)
	switch bp {
	case bpPhone:
		return u.composePhone()
	case bpCompact:
		return u.composeCompact()
	case bpDesktop:
		return u.composeDesktop()
	default:
		return u.composeWide()
	}
}

// actionGrid lays the seven primary actions as a fixed-cell grid:
// touch-sized, wrapping rows, impossible to overflow horizontally.
func (u *studioUI) actionGrid(cell fyne.Size) fyne.CanvasObject {
	objs := make([]fyne.CanvasObject, 0, len(u.actionButtons))
	for _, b := range u.actionButtons {
		objs = append(objs, b)
	}
	return container.NewGridWrap(cell, objs...)
}

// composePhone: one primary column; transport fixed at the bottom; the
// focal preview keeps a minimum height; actions/library/inspector stack
// into a single vertical scroll.
func (u *studioUI) composePhone() fyne.CanvasObject {
	center := container.NewScroll(container.NewVBox(
		newMinSizeWrapper(u.regionPreview, 280, 200),
		widget.NewSeparator(),
		u.actionGrid(fyne.NewSize(146, 40)),
		widget.NewSeparator(),
		u.inspectorContent,
		widget.NewSeparator(),
		u.libraryContent,
	))
	return container.NewBorder(u.regionHeader, u.regionTransportC, nil, nil, center)
}

// composeCompact: preview and a scrollable side panel share the width;
// the split is draggable so the user controls the balance.
func (u *studioUI) composeCompact() fyne.CanvasObject {
	side := container.NewScroll(container.NewVBox(
		u.actionGrid(fyne.NewSize(140, 38)),
		widget.NewSeparator(),
		u.libraryContent,
		widget.NewSeparator(),
		u.inspectorContent,
	))
	split := container.NewHSplit(newMinSizeWrapper(u.regionPreview, 220, 150), side)
	split.Offset = 0.55
	return container.NewBorder(u.regionHeader, u.regionTransportC, nil, nil, split)
}

// composeDesktop: the classic three-region workspace with the full
// action top bar (seven buttons fit from 1024 up).
func (u *studioUI) composeDesktop() fyne.CanvasObject {
	actionObjs := make([]fyne.CanvasObject, 0, len(u.actionButtons))
	for _, b := range u.actionButtons {
		actionObjs = append(actionObjs, b)
	}
	actionRow := container.NewHBox(actionObjs...)
	top := container.NewBorder(nil, nil,
		container.NewHBox(u.regionHeader.(*fyne.Container).Objects[0], widget.NewLabel(" "), u.trackLabel),
		actionRow,
		u.statusLabel,
	)
	return container.NewBorder(top, u.regionTransportF, u.regionLibraryS,
		container.NewVScroll(u.inspectorContent), u.regionPreview)
}

// composeWide: the desktop composition capped at 1680 px and centered
// over a full-bleed obsidian background (ultrawide contract: no control
// stretches across the whole monitor).
func (u *studioUI) composeWide() fyne.CanvasObject {
	bg := canvas.NewRectangle(hexToColor(brand.ColorObsidian))
	return newMaxWidthContainer(u.composeDesktop(), bg, wideContentCap)
}

// newSlider builds a slider with a fine step.
func (u *studioUI) newSlider(min, max float64, on func(float64)) *widget.Slider {
	s := widget.NewSlider(min, max)
	s.Step = (max - min) / 100
	s.OnChanged = on
	return s
}

// syncInspector pushes the current config into the widgets.
func (u *studioUI) syncInspector() {
	cfg := u.svc.Config()
	u.modeSelect.SetSelected(cfg.Mode.DisplayName())
	u.sensSl.SetValue(cfg.Sensitivity)
	u.smoothSl.SetValue(cfg.Smoothing)
	u.glowSl.SetValue(cfg.Glow)
	u.paletteSl.SetValue(float64(cfg.Palette))
	u.densitySl.SetValue(cfg.ParticleDensity)
	u.pulseSl.SetValue(cfg.PulseResponse)
	u.bgLayerSelect.SetSelected(layerDisplayName(cfg.BackgroundLayer))
	u.bgIntensitySl.SetValue(cfg.BackgroundIntensity)
	u.tempoEntry.SetText(formatTempo(u.svc.TempoOverride()))
	u.offsetEntry.SetText(fmt.Sprintf("%+.1f", u.svc.SyncOffset()))
	u.titleEntry.SetText(cfg.TitleText)
	u.subEntry.SetText(cfg.SubtitleText)
	u.showTitleCb.SetChecked(cfg.ShowTitle)
	u.watermarkCb.SetChecked(cfg.ShowWatermark)
	u.refreshDNA()
}

// subscribe wires studio events onto the Fyne thread.
func (u *studioUI) subscribe() {
	u.svc.Subscribe(func(ev studio.Event) {
		fyne.Do(func() { u.onEvent(ev) })
	})
}

// onEvent updates the UI for one studio event (Fyne thread).
func (u *studioUI) onEvent(ev studio.Event) {
	switch ev.Kind {
	case studio.EventImportStarted:
		u.status("Importing…")
		u.emptyState.Hide()
	case studio.EventImportProgress:
		u.status(fmt.Sprintf("Importing… %.0f%%", ev.Progress))
	case studio.EventImportDone:
		u.emptyState.Hide()
		u.refreshTrack()
		u.status(ev.Message)
	case studio.EventImportFailed:
		u.status("Import failed: " + ev.Message)
		u.dialogError("Import failed", ev.Message)
	case studio.EventAnalysisStarted:
		u.analysisLB.SetText("analysis: running…")
	case studio.EventAnalysisDone:
		u.analysisLB.SetText("analysis: done")
		u.refreshDNA()
		u.status(ev.Message)
	case studio.EventAnalysisFailed:
		u.analysisLB.SetText("analysis: FAILED — " + ev.Message)
	case studio.EventPlaybackChanged:
		u.refreshTransport()
	case studio.EventExportProgress:
		if u.exportBar != nil {
			p := ev.Progress
			if p <= 0 && u.exportJob != nil {
				info := u.exportJob.Info()
				if info.FramesTotal > 0 {
					p = float64(info.FramesRendered) / float64(info.FramesTotal)
				}
			}
			u.exportBar.SetValue(p)
		}
		if u.exportLabel != nil && ev.Message != "" {
			u.exportLabel.SetText(ev.Message)
		}
	case studio.EventExportDone:
		u.status("Export complete: " + ev.Message)
		u.logInfo("export finished", "result", "success", "detail", ev.Message)
		u.exportJob = nil
		if u.exportDialog != nil {
			u.exportDialog.Hide()
			u.exportDialog = nil
		}
		u.dialogInfo("Export complete", ev.Message)
	case studio.EventExportFailed:
		u.status("Export failed: " + ev.Message)
		u.logErr("export failed", "detail", ev.Message)
		u.exportJob = nil
		if u.exportDialog != nil {
			u.exportDialog.Hide()
			u.exportDialog = nil
		}
		u.dialogError("Export failed", ev.Message)
	case studio.EventProjectSaved:
		u.status("Project saved: " + ev.Message)
	case studio.EventProjectLoaded:
		u.status("Project loaded: " + ev.Message)
		u.syncInspector()
	case studio.EventProjectFailed:
		u.status("Project error: " + ev.Message)
	}
}

// refreshTrack updates the top bar with track details.
func (u *studioUI) refreshTrack() {
	tr := u.svc.Track()
	if tr == nil {
		return
	}
	u.trackLabel.SetText(fmt.Sprintf("%s — %s · %s",
		tr.SourceName, tr.Metadata.FormatText, tr.Metadata.DurationText))
	u.timeTot.SetText(tr.Metadata.DurationText)
	cfg := u.svc.Config()
	if cfg.TitleText == "" {
		cfg.TitleText = tr.SourceName
		u.svc.SetConfig(cfg)
		u.syncInspector()
	}
}

// refreshDNA updates the Song DNA line (measured values only).
func (u *studioUI) refreshDNA() {
	dna := u.svc.DNA()
	if dna == nil {
		return
	}
	mode := visualizer.Mode(dna.DefaultMode).DisplayName()
	u.dnaLabel.SetText(fmt.Sprintf("Song DNA: %s\nsuggested mode: %s", dna.Descriptor, mode))
}

// refreshTransport updates play button + clock labels.
func (u *studioUI) refreshTransport() {
	clock := u.svc.Clock()
	if clock.State == playback.StatePlaying {
		u.btnPlay.SetText("⏸ Pause")
	} else {
		u.btnPlay.SetText("▶ Play")
	}
	if !u.scrubbing {
		if clock.Total > 0 {
			u.scrubber.SetValue(1000 * clock.Position.Seconds() / clock.Total.Seconds())
		} else {
			u.scrubber.SetValue(0)
		}
	}
	sec := int(clock.Position.Seconds())
	u.timeCur.SetText(fmt.Sprintf("%d:%02d", sec/60, sec%60))
	switch {
	case u.svc.DeviceReal():
		u.deviceLB.SetText(fmt.Sprintf("audio device: active (Oto) · playhead uncertainty ≤ %d ms",
			clock.Latency.Milliseconds()))
	default:
		reason := u.svc.OutputReason()
		if u.svc.OutputStarted() {
			u.deviceLB.SetText("audio device: NOT AVAILABLE — silent preview mode (visualizer still runs) · " + reason)
		} else {
			u.deviceLB.SetText("audio device: " + reason)
		}
	}
}

// status updates the status line.
func (u *studioUI) status(s string) { u.statusLabel.SetText(s) }

// parseURL builds a *url.URL for the About hyperlinks; the brand URL
// constants are pinned and always parseable, but an invalid value
// cannot crash the About dialog.
func parseURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		return &url.URL{Scheme: "https", Host: raw}
	}
	return u
}

// rendererSummary renders the honest backend identity in one line.
func rendererSummary(info render.BackendInfo) string {
	if info.Backend == "" {
		return "probing… (selection runs off the first-frame path)"
	}
	s := string(info.Backend) + " / " + string(info.GpuState)
	if info.BackendAPI != "" {
		s += " / " + info.BackendAPI
	}
	if info.Adapter != "" {
		s += " / " + info.Adapter
	}
	if info.Detail != "" {
		s += " — " + info.Detail
	}
	return s
}

// onAbout shows the product identity, build metadata and the two-party
// legal attribution (licensor and project home) with exact URLs.
func (u *studioUI) onAbout() {
	head := widget.NewLabelWithStyle(brand.ProductName, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	build := widget.NewLabel("Build: " + buildinfo.Line())
	build.TextStyle = fyne.TextStyle{Monospace: true}
	renderer := widget.NewLabel("Renderer: " + rendererSummary(u.rendererInfo))
	renderer.Wrapping = fyne.TextWrapWord

	content := container.NewVBox(
		head,
		build,
		renderer,
		widget.NewSeparator(),
		widget.NewLabel(brand.Copyright),
		container.NewHBox(
			widget.NewLabel("Licensor / rights holder: Parsaetak —"),
			widget.NewHyperlink(brand.LicensorURL, parseURL(brand.LicensorURL)),
		),
		container.NewHBox(
			widget.NewLabel("Product / brand / project home: SHEYTAN —"),
			widget.NewHyperlink(brand.ProjectURL, parseURL(brand.ProjectURL)),
		),
		widget.NewLabel(brand.TrademarkStatement),
	)
	content.Objects[0].(*widget.Label).Wrapping = fyne.TextWrapWord
	for _, o := range content.Objects {
		if l, ok := o.(*widget.Label); ok {
			l.Wrapping = fyne.TextWrapWord
		}
	}
	d := dialog.NewCustom("About "+brand.ProductName, "Close", content, u.win)
	u.showCappedDialog(d, content)
}

// close stops the render loop and renderer. Idempotent: safe under the
// close controller's Release stage and any defensive repeat.
func (u *studioUI) close() {
	u.closeOnce.Do(func() {
		u.renderMu.Lock()
		u.closing = true
		u.renderMu.Unlock()
		u.renderMu.Lock()
		r := u.renderer
		u.renderMu.Unlock()
		if r != nil {
			r.Close()
		}
	})
}

// showCappedDialog shows a custom dialog sized by dialogSizeFor so it
// always fits the current window (phone-safe dialogs, no off-screen
// close controls).
func (u *studioUI) showCappedDialog(d dialog.Dialog, content fyne.CanvasObject) {
	_, area := u.win.Canvas().InteractiveArea()
	d.Resize(dialogSizeFor(content.MinSize(), area))
	d.Show()
}

// showCloseConfirmation presents the ONE confirmation surface for an
// active-work close request. It explains exactly what will be cancelled
// and offers staying in the app; duplicate requests never stack dialogs
// (the controller guarantees a single Confirm call).
func (u *studioUI) showCloseConfirmation(report appclose.WorkReport, proceed func()) {
	msg := "Work is still active:\n\n- " + report.Describe() +
		"\n\nClose the app anyway?"
	d := dialog.NewConfirm("Close SHEYTAN Music Visualizer?", msg, func(ok bool) {
		if ok {
			proceed()
		} else {
			u.closeCtl.CancelClose("user chose to stay")
		}
	}, u.win)
	d.SetDismissText("Stay in app")
	d.SetConfirmText("Close anyway")
	d.Show()
}

// --- helpers ------------------------------------------------------------

func hexToColor(c string) color.Color {
	v := uint32(0)
	for _, ch := range c[1:] {
		v <<= 4
		switch {
		case ch >= '0' && ch <= '9':
			v |= uint32(ch - '0')
		case ch >= 'a' && ch <= 'f':
			v |= uint32(ch-'a') + 10
		case ch >= 'A' && ch <= 'F':
			v |= uint32(ch-'A') + 10
		}
	}
	return color.NRGBA{R: uint8((v >> 16) & 0xFF), G: uint8((v >> 8) & 0xFF), B: uint8(v & 0xFF), A: 255}
}

func helveticaBold() (f fyne.TextStyle) {
	f.Bold = true
	return
}

func modeNames() []string {
	out := make([]string, 0, len(visualizer.AllModes))
	for _, m := range visualizer.AllModes {
		out = append(out, m.DisplayName())
	}
	return out
}

// layerNames lists the background composition layer choices: None
// first, then every mode.
func layerNames() []string {
	return append([]string{"None"}, modeNames()...)
}

func layerFromName(name string) visualizer.Mode {
	if name == "None" {
		return visualizer.ModeNone
	}
	return modeFromName(name)
}

func layerDisplayName(m visualizer.Mode) string {
	if m == visualizer.ModeNone || m == "" {
		return "None"
	}
	return m.DisplayName()
}

func formatTempo(bpm float64) string {
	if bpm <= 0 {
		return ""
	}
	return strconv.FormatFloat(bpm, 'f', -1, 64)
}

func modeFromName(name string) visualizer.Mode {
	for _, m := range visualizer.AllModes {
		if m.DisplayName() == name {
			return m
		}
	}
	return visualizer.ModeBars
}

// heapAllocBytes reports the current Go heap allocation (one-shot
// runtime read; never polled, never per-frame).
func heapAllocBytes() uint64 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// numGCPackages reports the completed GC cycle count (one-shot read).
func numGCPackages() uint32 {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.NumGC
}
