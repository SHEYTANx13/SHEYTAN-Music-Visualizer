// shell.go — Run: the ONE Go-Gui window lifecycle of the native shell.
// Owns the command bar, the workspace tab strip, destination routing,
// the status strip, the preview pacer goroutine and the smoke mode.
// studio.Studio and the render.Renderer live HERE (outside all
// layout/tab state) so layout changes never restart audio or recreate
// the renderer.

package gogui

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-gui-org/go-gui/gui"
	"github.com/go-gui-org/go-gui/gui/backend"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/design"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/apppaths"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/audio"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/brand"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/export"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/fileaccess"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/mediatools"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/presentation"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/render"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/studio"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/visualizer"
)

// execCommand is the single process-spawn seam (used ONLY by the
// reveal-in-folder portal action; overridable in tests).
var execCommand = exec.Command

// newShellWindow is the single window-construction seam. Run calls it
// EXACTLY ONCE; one_window_test.go enforces the one-call contract from
// source, and no other constructor of gui.Window may appear in this
// package's non-test files.
var newShellWindow = gui.NewWindow

// RunOptions configures Run.
type RunOptions struct {
	// SmokeFrames > 0 opens the real window, renders exactly N frames
	// through the real shell layout, then closes cleanly (CI/Xvfb
	// evidence; exit non-zero when the target is not reached).
	SmokeFrames int
	// Width/Height are the initial window size in logical pixels
	// (defaults 1280x800).
	Width  int
	Height int
}

// shell owns every control value of the window. All fields are owned
// by the go-gui frame thread (the view generator and the widget
// callbacks); the only cross-goroutine surfaces are uiState and the
// preview pacer's publish path.
type shell struct {
	th         *ShellTheme
	svc        *studio.Studio
	renderer   render.Renderer
	lay        apppaths.Layout
	store      *presentation.LayoutStore
	prefStore  *render.PreferenceStore
	mediaTools *mediatools.Manager
	fileAccess *fileaccess.Manager
	state      *uiState
	preview    *previewBridge
	surface    *gui.PixelSurface
	surfaceErr error
	disp       *dispatcher
	win        *gui.Window

	tabs      TabState
	attrLines []string
	presets   []visualizer.Preset
	formats   []audio.SourceFormat

	// frame geometry (frame thread only)
	widthDp float32

	// Control values (frame thread only). go-gui widgets are rebuilt
	// every frame from these values; callbacks write them and dispatch.
	volume         float32
	modeValue      string
	paletteValue   string
	bgValue        string
	intensityValue string
	prefValue      string
	resValue       string
	fpsValue       string
	codecValue     string
	sens           float32
	glow           float32
	bgInt          float32
	reducedMotion  bool
	reducedGlow    bool

	importPath  string
	projectPath string
	outPath     string
	sysPath     string
	seconds     string

	recentPaths    []string
	selectedPreset string
	pendingAuthReq *fileaccess.AuthorizationRequest

	// last-seen service config revision for change detection
	lastCfgRev uint64

	// smoke bookkeeping (frame thread only)
	smokeTarget   int
	smokeRendered int
	smokeClosed   bool

	// background-fetch guards (frame thread only)
	ffmpegRequested        bool
	mediaSnapshotRequested bool
	lastJobsPoll           time.Time
}

// Run starts the shell: it blocks until the window is closed and
// returns the final error (nil on a clean shutdown).
func Run(opts RunOptions) error {
	if opts.Width <= 0 {
		opts.Width = 1280
	}
	if opts.Height <= 0 {
		opts.Height = 800
	}
	if opts.SmokeFrames < 0 {
		opts.SmokeFrames = 0
	}

	doc := design.MustLoad()
	th := NewShellTheme(doc)
	// The go-gui app theme is installed BEFORE the window is created so
	// the first frame already paints in the dark polarity; the token
	// colors ride along through ShellTheme.
	gui.SetTheme(gui.ThemeDark)

	// Single apppaths authority; bootstrap creates the contract dirs.
	lay := apppaths.Resolve(apppaths.OSEnv())
	if err := apppaths.Bootstrap(lay); err != nil {
		return fmt.Errorf("gogui: data layout bootstrap: %w", err)
	}
	store := presentation.NewLayoutStore(filepath.Join(lay.Config, presentation.DefaultFilename))

	svc, err := studio.New()
	if err != nil {
		return fmt.Errorf("gogui: studio service: %w", err)
	}

	// ONE renderer for the whole process lifetime; closed ONLY at
	// shutdown (SelectRenderer falls back to CPU with honest info when
	// no real GPU adapter can be proven).
	renderer, rinfo := render.SelectRenderer()

	prefStore := render.OpenPreferenceStore(filepath.Join(lay.Config, "renderer-preference.json"))
	prefStatus := ""
	if pref, found, perr := prefStore.Load(); perr != nil {
		prefStatus = "stored renderer preference unreadable; auto remains active (" + perr.Error() + ")"
	} else if found {
		prefStatus = "stored renderer preference: " + string(pref) + " (revalidated every launch)"
	}

	mediaTools := mediatools.New(mediatools.Options{DataRoot: lay.DataRoot})
	fileAccess := fileaccess.NewManager()

	st := newUIState()
	st.SetRenderer(rinfo)
	st.SetConfig(svc.Config())
	st.SetTrack(svc.Track())
	if prefStatus != "" {
		st.SetStatus(prefStatus)
	}

	tabs, tabsErr := LoadTabs(store)
	if tabsErr != nil {
		st.SetStatus("Workspace layout could not be read; default applied: " + tabsErr.Error())
	}
	st.SetDestination(tabs.Layout.ActiveTab)

	// The PixelSurface is the frame bridge: the pacer goroutine publishes
	// renderer frames, the view shows it through gui.Image(Surface), and
	// every Update wakes the window on its own.
	surface, surfaceErr := gui.NewPixelSurface(gui.PixelSurfaceCfg{
		Width: previewDefaultWidth, Height: previewDefaultHeight,
	})

	sh := &shell{
		th:          th,
		svc:         svc,
		renderer:    renderer,
		lay:         lay,
		store:       store,
		prefStore:   prefStore,
		mediaTools:  mediaTools,
		fileAccess:  fileAccess,
		state:       st,
		preview:     newPreviewBridge(),
		surface:     surface,
		surfaceErr:  surfaceErr,
		tabs:        tabs,
		attrLines:   brand.AttributionBlock(),
		presets:     svc.Presets(),
		formats:     svc.SupportedFormats(),
		smokeTarget: opts.SmokeFrames,
	}
	sh.disp = newDispatcher(sh)
	sh.initControlValues()

	svc.Subscribe(st.OnStudioEvent)
	svc.SetPlayEventSink(st.OnPlayEvent)
	mediaTools.Subscribe(func(s mediatools.Snapshot) {
		// Manager goroutine: snapshot write only, then request a frame.
		st.SetMediaTools(s)
		st.invalidate()
	})

	// The ONE window construction of the process (newShellWindow seam).
	win := newShellWindow(gui.WindowCfg{
		Title:   brand.WindowTitle(),
		Width:   opts.Width,
		Height:  opts.Height,
		State:   sh,
		BgColor: th.ObsidianColor(),
		WMClass: "SHEYTAN-Music-Visualizer",
		OnInit: func(w *gui.Window) {
			sh.win = w
			st.setWindow(w)
			w.SetView(sh.view)
			sh.registerShortcuts(w)
		},
		OnEvent: func(e *gui.Event, w *gui.Window) {
			// OS app-quit joins the same close path as the Quit command:
			// one window, one shutdown. OnEvent receives only events the
			// widget tree left unhandled, which is exactly the quit case.
			if e.Type == gui.EventQuitRequested {
				e.IsHandled = true
				w.Close()
			}
		},
	})
	if sh.win == nil {
		sh.win = win
		st.setWindow(win)
	}
	if surfaceErr == nil {
		sh.seedSurface()
		sh.preview.Attach(surface)
	}

	// Preview pacer: the producer goroutine owns the frame cadence and
	// publishes through the PixelSurface; the window is never touched
	// from here except through the state bridge.
	pacerStop := make(chan struct{})
	pacerDone := make(chan struct{})
	go sh.previewLoop(pacerStop, pacerDone)

	// OS termination signals enter the same close path as the Quit
	// command: one window, one shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		if sig, ok := <-sigCh; ok {
			_ = sig
			win.Close()
		}
	}()

	if opts.SmokeFrames > 0 {
		watchdog := time.AfterFunc(smokeTimeout, func() {
			win.Close()
		})
		defer watchdog.Stop()
	}

	backend.Run(win) // blocks until the window closes
	close(pacerStop)
	<-pacerDone

	// Teardown: service first (cancels work, closes the transport), then
	// the renderer, then the frame bridge (only now — never on resize or
	// re-layout).
	svc.Close()
	renderer.Close()
	if surface != nil {
		surface.Close()
	}
	if err := store.Save(sh.tabs.Layout); err != nil {
		st.SetStatus("Layout could not be persisted: " + err.Error())
	}

	if opts.SmokeFrames > 0 {
		if sh.smokeRendered < opts.SmokeFrames {
			return fmt.Errorf("gogui: smoke timeout — only %d of %d frames rendered", sh.smokeRendered, opts.SmokeFrames)
		}
		fmt.Printf("smoke: %d frames rendered backend=%s gpu=%s\n",
			sh.smokeRendered, rinfo.Backend, rinfo.GpuState)
	}
	return nil
}

// initControlValues seeds the control values from the honest service
// defaults (called once, before the window opens).
func (sh *shell) initControlValues() {
	cfg := sh.svc.Config()
	sh.modeValue = string(cfg.Mode)
	sh.paletteValue = strconv.Itoa(cfg.Palette)
	sh.bgValue = backgroundLayerKey(cfg.BackgroundLayer)
	sh.intensityValue = sh.th.IntensityName()
	sh.prefValue = "auto"
	if pref, found, perr := sh.prefStore.Load(); perr == nil && found {
		switch pref {
		case render.PrefCPU:
			sh.prefValue = "cpu"
		case render.PrefVulkan, render.PrefDX12, render.PrefSoftware:
			sh.prefValue = "gpu"
		}
	}
	sh.resValue = export.Resolutions[1].ID // 1080p
	sh.fpsValue = "30"
	sh.codecValue = "h264"
	sh.seconds = ""
	sh.sens = sensitivityToSlider(cfg.Sensitivity)
	sh.glow = float32(cfg.Glow / glowMax)
	sh.bgInt = float32(cfg.BackgroundIntensity / backgroundIntensityMax)
	sh.volume = 1
	_, sh.lastCfgRev = sh.state.Config()
}

// syncControlsFromConfig flows a service-side config change (preset
// apply, project load) into the control values.
func (sh *shell) syncControlsFromConfig(cfg visualizer.Config) {
	sh.modeValue = string(cfg.Mode)
	sh.paletteValue = strconv.Itoa(cfg.Palette)
	sh.bgValue = backgroundLayerKey(cfg.BackgroundLayer)
	sh.sens = sensitivityToSlider(cfg.Sensitivity)
	sh.glow = float32(cfg.Glow / glowMax)
	sh.bgInt = float32(cfg.BackgroundIntensity / backgroundIntensityMax)
}

// backgroundLayerKey maps a config background layer to the control key.
func backgroundLayerKey(m visualizer.Mode) string {
	if m == "" {
		return string(visualizer.ModeNone)
	}
	return string(m)
}

// view is the shell's root view generator: command bar, tab strip,
// workspace, status strip. It runs again for each frame on the go-gui
// frame thread.
func (sh *shell) view(w *gui.Window) gui.View {
	// Pending clipboard write (queued by CmdCopyToClipboard): the frame
	// thread is the only writer of the OS clipboard.
	if txt := sh.state.TakeClipboard(); txt != "" {
		w.SetClipboard(txt)
	}

	// Smoke contract: count REAL presented frames (one FrameFn per
	// presented frame), keep the loop alive until the target is reached,
	// then close cleanly through the same path as Quit.
	if sh.smokeTarget > 0 && !sh.smokeClosed {
		sh.smokeRendered = int(w.FrameCount())
		if sh.smokeRendered >= sh.smokeTarget {
			sh.smokeClosed = true
			w.Close()
		} else {
			w.InvalidateLayout()
		}
	}

	ww, _ := w.WindowSize()
	sh.widthDp = float32(ww)

	// Service-side config changes (preset apply, project load) flow back
	// into the controls through the config revision.
	if _, rev := sh.state.Config(); rev != sh.lastCfgRev {
		cfg, _ := sh.state.Config()
		sh.lastCfgRev = rev
		sh.syncControlsFromConfig(cfg)
	}

	return gui.Column(gui.ContainerCfg{
		ID:      "shell-root",
		Sizing:  gui.FillFill,
		Padding: gui.PaddingNone,
		Spacing: gui.NoSpacing,
		Color:   sh.th.ObsidianColor(),
		Content: []gui.View{
			sh.commandBar(),
			tabStripView(&sh.tabs, sh.th, sh.activateDestination, sh.moveActiveTab),
			sh.destinationPanel(),
			sh.statusStrip(),
		},
	})
}

// activateDestination switches the active tab from the strip (the
// shared tab callback for the real shell).
func (sh *shell) activateDestination(d presentation.Destination) {
	sh.tabs.SetActive(d)
	sh.syncTabs()
}

// moveActiveTab reorders the ACTIVE destination within the persisted
// strip order (run 44648357437 item 4.3). delta<0 moves toward the
// strip start, delta>0 toward the end. The tab strip's ‹/› controls
// and the mod+alt+left/right chords both route through this one
// mutation, so the pointer/touch and keyboard paths cannot drift.
func (sh *shell) moveActiveTab(delta int) {
	if !sh.tabs.MoveActive(delta) {
		sh.state.SetStatus("Tab reorder: the active tab is already at that strip edge")
		return
	}
	sh.syncTabs()
	sh.state.SetStatus("Tab order updated — active: " + string(sh.tabs.Layout.ActiveTab))
}

// setSideCollapsed collapses/restores the side rail of the split
// composition for the ACTIVE destination. The state persists
// per-destination in the shared versioned layout document; the
// collapsed rail keeps a visible restore control at the collapse site.
func (sh *shell) setSideCollapsed(v bool) {
	sh.tabs.SetSideCollapsed(v)
	sh.syncTabs()
	if v {
		sh.state.SetStatus("Side panel collapsed for " + string(sh.tabs.Layout.ActiveTab) + " (restore: the button or mod+shift+B)")
	} else {
		sh.state.SetStatus("Side panel restored for " + string(sh.tabs.Layout.ActiveTab))
	}
}

// toggleSplitOrientation flips the persisted split-axis override
// (workspace.split.orientation): horizontal main-beside-rail ⇄ vertical
// main-above-rail. The responsive breakpoint still decides split-vs-
// stacked; within the split composition the user owns the axis.
func (sh *shell) toggleSplitOrientation() {
	sh.tabs.Layout.SplitVertical = !sh.tabs.Layout.SplitVertical
	sh.syncTabs()
	if sh.tabs.Layout.SplitVertical {
		sh.state.SetStatus("Split orientation: vertical (main above rail)")
	} else {
		sh.state.SetStatus("Split orientation: horizontal (main beside rail)")
	}
}

// commandBar — brand title + the top commands.
func (sh *shell) commandBar() gui.View {
	clk := sh.state.Clock()
	return gui.Row(gui.ContainerCfg{
		ID:          "command-bar",
		Sizing:      gui.FillFit,
		Padding:     gui.NewPadding(4, 8, 4, 8),
		Spacing:     gui.SpacingSmall,
		Color:       sh.th.SurfaceColor(),
		SizeBorder:  gui.BorderThin,
		ColorBorder: sh.th.DecorationBorderColor(),
		VAlign:      gui.VAlignMiddle,
		Content: []gui.View{
			gui.Text(gui.TextCfg{
				ID:        "brand-title",
				Text:      brand.ProductName,
				TextStyle: sh.th.TextStyleTitle(),
			}),
			sh.barButton("cmd-import", "Import", func(ctx gui.EventCtx) {
				ctx.Consume()
				sh.chooseImportPath()
			}),
			sh.barButton("cmd-open", "Open", func(ctx gui.EventCtx) {
				ctx.Consume()
				sh.activateDestination(presentation.DestLibrary)
			}),
			sh.barButton("cmd-save", "Save", func(ctx gui.EventCtx) {
				ctx.Consume()
				sh.disp.Run(presentation.CmdProjectSave)
			}),
			sh.barButton("cmd-export", "Export", func(ctx gui.EventCtx) {
				ctx.Consume()
				sh.activateDestination(presentation.DestExport)
			}),
			sh.barButtonPrimary("cmd-play", transportButtonLabel(clk), func(ctx gui.EventCtx) {
				ctx.Consume()
				sh.disp.Run(presentation.CmdPlayPause)
			}),
			sh.barButton("cmd-stop", "Stop", func(ctx gui.EventCtx) {
				ctx.Consume()
				sh.disp.Run(presentation.CmdStop)
			}),
			sh.barButton("cmd-seek-back", "−5s", func(ctx gui.EventCtx) {
				ctx.Consume()
				sh.disp.Run(presentation.CmdSeekBack)
			}),
			sh.barButton("cmd-seek-forward", "+5s", func(ctx gui.EventCtx) {
				ctx.Consume()
				sh.disp.Run(presentation.CmdSeekForward)
			}),
			gui.Slider(gui.SliderCfg{
				ID:    "cmd-volume",
				Value: sh.volume,
				Min:   0, Max: 1, Step: 0.01,
				Width:   140,
				A11YCfg: gui.A11YCfg{A11YLabel: "Playback volume"},
				OnChange: func(v float32, ctx gui.EventCtx) {
					ctx.Consume()
					if changedEnough(sh.volume, v) {
						sh.volume = v
						sh.disp.Run(presentation.CmdSetVolume)
					}
				},
			}),
			sh.barButton("cmd-media-tools", "Media Tools", func(ctx gui.EventCtx) {
				ctx.Consume()
				sh.disp.Run(presentation.CmdMediaToolsOpen)
			}),
			sh.barButton("cmd-help", "Help", func(ctx gui.EventCtx) {
				ctx.Consume()
				sh.disp.Run(presentation.CmdHelpOpen)
			}),
			sh.barButton("cmd-quit", "Quit", func(ctx gui.EventCtx) {
				ctx.Consume()
				sh.disp.Run(presentation.CmdQuit)
			}),
		},
	})
}

// changedEnough guards the volume dispatch against drag-step spam
// (1% steps, the slider's own granularity).
func changedEnough(last, v float32) bool {
	d := v - last
	if d < 0 {
		d = -d
	}
	return d >= 0.01
}

// barButton renders a secondary (volcanic) command-bar button.
func (sh *shell) barButton(id, label string, onClick func(gui.EventCtx)) gui.View {
	return gui.Button(gui.ButtonCfg{
		ID:      id,
		Label:   label,
		Colors:  gui.ColorSet{Base: sh.th.VolcanicColor()},
		OnClick: onClick,
	})
}

// barButtonPrimary renders the primary (accent) command-bar button.
func (sh *shell) barButtonPrimary(id, label string, onClick func(gui.EventCtx)) gui.View {
	return gui.Button(gui.ButtonCfg{
		ID:      id,
		Label:   label,
		Variant: gui.ButtonPrimary,
		OnClick: onClick,
	})
}

// statusStrip — status line, transport state + clock, renderer
// backend/state, data mode (honest, from the state snapshot).
func (sh *shell) statusStrip() gui.View {
	info := sh.state.Renderer()
	return gui.Row(gui.ContainerCfg{
		ID:          "status-strip",
		Sizing:      gui.FillFit,
		Padding:     gui.NewPadding(3, 8, 3, 8),
		Spacing:     gui.SpacingSmall,
		Color:       sh.th.SurfaceColor(),
		SizeBorder:  gui.BorderThin,
		ColorBorder: sh.th.DecorationBorderColor(),
		VAlign:      gui.VAlignMiddle,
		Content: []gui.View{
			gui.Column(gui.ContainerCfg{
				Sizing: gui.FillFit,
				Content: []gui.View{
					gui.Text(gui.TextCfg{
						Text:      "Status: " + sh.state.Status(),
						TextStyle: sh.th.TextStyleCaption(),
						Mode:      gui.TextModeWrap,
					}),
				},
			}),
			sh.statusCaption("Transport: " + transportLine(sh.state.Clock())),
			sh.statusCaption("renderer=" + string(info.Backend) + "/" + string(info.GpuState)),
			sh.statusCaption("data=" + string(sh.lay.Mode)),
		},
	})
}

// statusCaption renders one dim caption in the status strip.
func (sh *shell) statusCaption(s string) gui.View {
	return gui.Text(gui.TextCfg{
		Text:      s,
		TextStyle: sh.th.TextStyleCaption(),
	})
}

// syncTabs persists the tab state and reports navigation in the
// status strip. Saves are small atomic writes on the frame thread.
func (sh *shell) syncTabs() {
	sh.state.SetDestination(sh.tabs.Layout.ActiveTab)
	sh.state.SetStatus("Destination: " + string(sh.tabs.Layout.ActiveTab))
	sh.saveLayout()
}

// saveLayout persists the workspace layout through the shared store.
func (sh *shell) saveLayout() {
	if sh.store == nil {
		// Display-less unit tests build shells without a store; there is
		// nothing to persist. The real window path always carries one.
		return
	}
	if err := sh.store.Save(sh.tabs.Layout); err != nil {
		sh.state.SetStatus("Layout could not be persisted: " + err.Error())
	}
}

// rememberImportPath keeps a small bounded recent-paths list.
func (sh *shell) rememberImportPath(path string) {
	for _, p := range sh.recentPaths {
		if p == path {
			return
		}
	}
	sh.recentPaths = append([]string{path}, sh.recentPaths...)
	if len(sh.recentPaths) > recentPathsMax {
		sh.recentPaths = sh.recentPaths[:recentPathsMax]
	}
}

// ensureFFmpegDetection runs the (potentially blocking) FFmpeg probe
// once, on a goroutine, and stores the honest availability line.
func (sh *shell) ensureFFmpegDetection() {
	if sh.ffmpegRequested {
		return
	}
	sh.ffmpegRequested = true
	st := sh.state
	go func() {
		env, err := export.DetectEnv(false)
		if err != nil {
			st.SetFFmpegText("FFmpeg: NOT AVAILABLE — " + err.Error() +
				"\nExports need FFmpeg; playback and the visualizers keep working without it.")
			return
		}
		st.SetFFmpegText(fmt.Sprintf("FFmpeg: %s (source: %s)\nH.264: %s\nH.265: %s\nProven hardware encoders: %s",
			env.Version, env.Source,
			joinOrNone(env.CodecEncoderList(export.CodecH264)),
			joinOrNone(env.CodecEncoderList(export.CodecHEVC)),
			joinOrNone(env.HardwareEncoders)))
	}()
}

// ensureMediaToolsSnapshot fetches the media-tools snapshot once (the
// manager subscription keeps it fresh afterwards).
func (sh *shell) ensureMediaToolsSnapshot() {
	if sh.mediaSnapshotRequested {
		return
	}
	sh.mediaSnapshotRequested = true
	mt, st := sh.mediaTools, sh.state
	go func() {
		st.SetMediaTools(mt.Snapshot())
		st.invalidate()
	}()
}

// pollJobs refreshes the diagnostics scheduler snapshot at a slow,
// bounded cadence while the panel is visible.
func (sh *shell) pollJobs() {
	now := time.Now()
	if now.Sub(sh.lastJobsPoll) < jobsPollInterval {
		return
	}
	sh.lastJobsPoll = now
	sh.state.SetJobs(sh.svc.JobsOverview())
}

// previewLoop is the preview frame pacer goroutine. It renders
// studio.PreviewFrame through the shell renderer at the honest cadence
// and publishes through the PixelSurface; UI refreshes ride the surface
// wake (Studio tab) and the state bridge (every destination while
// playing).
func (sh *shell) previewLoop(stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	for {
		now := time.Now()
		playing := sh.svc.IsPlayingLike()
		allowed := sh.preview.ShouldRequestFrame(now, playing, sh.th.ReducedMotion())
		if allowed && sh.state.Destination() == presentation.DestStudio {
			w, h := sh.state.PaneSize()
			w, h = clampPreviewSize(w, h)
			if fr, err := sh.svc.PreviewFrame(w, h, sh.renderer); err == nil {
				sh.preview.Publish(fr, now)
			}
		}
		if playing && allowed {
			// Keep the transport clock and status strip fresh at the same
			// bounded cadence (the surface wake only covers the Studio
			// preview image; the layout refresh covers every readout).
			sh.state.invalidate()
		}
		next := now.Add(previewMinInterval)
		if !playing {
			next = sh.preview.NextIdleDeadline(now, sh.th.ReducedMotion())
		}
		wait := time.Until(next)
		if wait < time.Millisecond {
			wait = time.Millisecond
		}
		timer := time.NewTimer(wait)
		select {
		case <-stop:
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// seedSurface paints the deterministic obsidian empty-preview backdrop
// into the fresh surface (opaque pixels: premultiplied and straight
// alpha coincide), so the "no frames yet" pane shows the brand ground
// instead of transparency.
func (sh *shell) seedSurface() {
	seed := placeholderImage(previewDefaultWidth, previewDefaultHeight, sh.th.Obsidian())
	_ = sh.surface.Update(seed.Pix, seed.Stride)
}

// previewRequestSize clamps a pane size to the bounded preview budget.
// A resize just changes the next request; the renderer is never
// recreated.
func clampPreviewSize(w, h int) (int, int) {
	if w <= 0 || h <= 0 {
		w, h = 480, 270
	}
	if w > previewMaxWidthPx {
		h = h * previewMaxWidthPx / w
		w = previewMaxWidthPx
	}
	if h > previewMaxHeightPx {
		w = w * previewMaxHeightPx / h
		h = previewMaxHeightPx
	}
	if w < 16 {
		w = 16
	}
	if h < 16 {
		h = 16
	}
	return w, h
}

// constants shared with the panels.
const (
	recentPathsMax     = 5
	jobsPollInterval   = time.Second
	smokeTimeout       = 45 * time.Second
	previewMaxWidthPx  = 640
	previewMaxHeightPx = 360
)

// joinOrNone renders a capability list or an honest "none".
func joinOrNone(list []string) string {
	if len(list) == 0 {
		return "none"
	}
	return strings.Join(list, ", ")
}
