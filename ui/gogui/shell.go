// shell.go — Run: the ONE Gio window lifecycle of the native shell.
// Owns the command bar, the workspace tab strip, destination routing,
// the status strip and the smoke mode. studio.Studio and the
// render.Renderer live HERE (outside all layout/tab state) so layout
// changes never restart audio or recreate the renderer.

package gogui

import (
	"fmt"
	"image"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"gioui.org/app"
	"gioui.org/io/clipboard"
	"gioui.org/io/system"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

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

// RunOptions configures Run.
type RunOptions struct {
	// SmokeFrames > 0 opens the real window, renders exactly N frames
	// through the real shell layout, then closes cleanly (CI/Xvfb
	// evidence; exit non-zero when the target is not reached).
	SmokeFrames int
	// Width/Height are the initial window size in dp (defaults 1280x800).
	Width  int
	Height int
}

// shell owns every widget of the window. All fields are owned by the
// event-loop goroutine; the only cross-goroutine surface is uiState.
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
	disp       *dispatcher
	win        *app.Window

	tabs      TabState
	attrLines []string
	presets   []visualizer.Preset
	formats   []audio.SourceFormat

	// frame geometry (event loop only)
	widthDp         float32
	previewPaneSize image.Point

	// command bar
	btnImport widget.Clickable
	btnOpen   widget.Clickable
	btnSave   widget.Clickable
	btnExport widget.Clickable
	btnPlay   widget.Clickable
	btnStop   widget.Clickable
	btnBack   widget.Clickable
	btnFwd    widget.Clickable
	btnMedia  widget.Clickable
	btnHelp   widget.Clickable
	btnQuit   widget.Clickable
	btnDiag   widget.Clickable
	volume    widget.Float

	// tab strip
	tabClicks map[presentation.Destination]*widget.Clickable

	// panel lists
	homeList       widget.List
	studioList     widget.List
	studioRailList widget.List
	studioMainList widget.List
	templatesList  widget.List
	libraryList    widget.List
	exportList     widget.List
	reqList        widget.List
	sysList        widget.List
	settingsList   widget.List
	diagList       widget.List
	emptyList      widget.List

	// studio widgets
	importPathEd    widget.Editor
	importGoBtn     widget.Clickable
	importCancelBtn widget.Clickable
	recentPaths     []string
	projectPathEd   widget.Editor
	modeEnum        widget.Enum
	paletteEnum     widget.Enum
	bgEnum          widget.Enum
	sens            widget.Float
	glow            widget.Float
	bgInt           widget.Float

	// templates
	presetClicks   map[string]*widget.Clickable
	selectedPreset string

	// library (the shared project path entry serves save AND open)
	openProjBtn widget.Clickable

	// export
	resEnum         widget.Enum
	fpsEnum         widget.Enum
	codecEnum       widget.Enum
	secondsEd       widget.Editor
	outPathEd       widget.Editor
	exportStartBtn  widget.Clickable
	exportCancelBtn widget.Clickable

	// requirements
	mtInstallBtn widget.Clickable
	mtCancelBtn  widget.Clickable
	mtUpdateBtn  widget.Clickable
	mtRepairBtn  widget.Clickable
	mtFolderBtn  widget.Clickable

	// system files
	sysPathEd      widget.Editor
	sysAnalyzeBtn  widget.Clickable
	pendingAuthReq *fileaccess.AuthorizationRequest

	// settings
	intensityEnum    widget.Enum
	reducedMotionSw  widget.Bool
	reducedGlowSw    widget.Bool
	rendererPrefEnum widget.Enum

	// diagnostics
	copyDiagBtn    widget.Clickable
	logsRevealBtn  widget.Clickable
	layoutResetBtn widget.Clickable

	// last-seen widget values (event-loop only) for change detection
	lastVolume         float32
	lastModeValue      string
	lastPaletteValue   string
	lastBgValue        string
	lastIntensityValue string
	lastPrefValue      string
	lastResValue       string
	lastFpsValue       string
	lastCodecValue     string
	lastSens           float32
	lastGlow           float32
	lastBgInt          float32
	lastReducedMotion  bool
	lastReducedGlow    bool
	lastCfgRev         uint64

	// background-fetch guards (event loop only)
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

	sh := &shell{
		th:         th,
		svc:        svc,
		renderer:   renderer,
		lay:        lay,
		store:      store,
		prefStore:  prefStore,
		mediaTools: mediaTools,
		fileAccess: fileAccess,
		state:      st,
		preview:    newPreviewBridge(),
		tabs:       tabs,
		attrLines:  brand.AttributionBlock(),
		presets:    svc.Presets(),
		formats:    svc.SupportedFormats(),
	}
	sh.disp = newDispatcher(sh)
	sh.tabClicks = make(map[presentation.Destination]*widget.Clickable, len(presentation.Destinations))
	for _, d := range presentation.Destinations {
		sh.tabClicks[d] = &widget.Clickable{}
	}
	sh.presetClicks = make(map[string]*widget.Clickable, len(sh.presets))
	for _, p := range sh.presets {
		sh.presetClicks[p.Name] = &widget.Clickable{}
	}
	sh.initEditors()
	sh.initEnums()
	sh.volume.Value = 1
	sh.lastVolume = 1
	sh.lastCfgRev = cfgRevInitial

	svc.Subscribe(st.OnStudioEvent)
	svc.SetPlayEventSink(st.OnPlayEvent)
	mediaTools.Subscribe(func(s mediatools.Snapshot) {
		// Manager goroutine: snapshot write only, then request a frame.
		st.SetMediaTools(s)
		st.invalidate()
	})

	win := &app.Window{}
	win.Option(app.Title(brand.WindowTitle()), app.Size(unit.Dp(opts.Width), unit.Dp(opts.Height)))
	st.setWindow(win)

	// OS termination signals enter the same close path as the Quit
	// command: one window, one shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	go func() {
		if sig, ok := <-sigCh; ok {
			_ = sig
			win.Perform(system.ActionClose)
		}
	}()

	frames := 0
	loopDone := make(chan struct{})
	go func() {
		defer close(loopDone)
		var ops op.Ops
		smokeTarget := opts.SmokeFrames
		smokeClosed := false
		for {
			switch e := win.Event().(type) {
			case app.DestroyEvent:
				_ = e
				return
			case app.FrameEvent:
				gtx := app.NewContext(&ops, e)
				sh.widthDp = float32(e.Size.X) / gtx.Metric.PxPerDp
				sh.handleEvents(gtx)
				sh.layout(gtx)
				e.Frame(gtx.Ops)
				frames++
				switch {
				case smokeTarget > 0:
					if !smokeClosed && frames >= smokeTarget {
						smokeClosed = true
						win.Perform(system.ActionClose)
					}
				default:
					sh.pumpPreview(gtx)
				}
			}
		}
	}()

	if opts.SmokeFrames > 0 {
		watchdog := time.AfterFunc(smokeTimeout, func() {
			win.Perform(system.ActionClose)
		})
		defer watchdog.Stop()
	}

	app.Main()
	<-loopDone

	// Teardown: service first (cancels work, closes the transport),
	// then the renderer (only now — never on resize or re-layout).
	svc.Close()
	renderer.Close()
	if err := store.Save(sh.tabs.Layout); err != nil {
		st.SetStatus("Layout could not be persisted: " + err.Error())
	}

	if opts.SmokeFrames > 0 {
		if frames < opts.SmokeFrames {
			return fmt.Errorf("gogui: smoke timeout — only %d of %d frames rendered", frames, opts.SmokeFrames)
		}
		fmt.Printf("smoke: %d frames rendered backend=%s gpu=%s\n",
			frames, rinfo.Backend, rinfo.GpuState)
	}
	return nil
}

// initEditors configures the single-line path editors.
func (sh *shell) initEditors() {
	for _, e := range []*widget.Editor{
		&sh.importPathEd, &sh.projectPathEd, &sh.outPathEd,
		&sh.sysPathEd, &sh.secondsEd,
	} {
		e.SingleLine = true
	}
}

// initEnums sets the initial enum selections from the honest defaults.
func (sh *shell) initEnums() {
	cfg := sh.svc.Config()
	sh.modeEnum.Value = string(cfg.Mode)
	sh.paletteEnum.Value = strconv.Itoa(cfg.Palette)
	sh.bgEnum.Value = backgroundLayerKey(cfg.BackgroundLayer)
	sh.intensityEnum.Value = sh.th.IntensityName()
	sh.rendererPrefEnum.Value = "auto"
	if pref, found, perr := sh.prefStore.Load(); perr == nil && found {
		switch pref {
		case render.PrefCPU:
			sh.rendererPrefEnum.Value = "cpu"
		case render.PrefVulkan, render.PrefDX12, render.PrefSoftware:
			sh.rendererPrefEnum.Value = "gpu"
		}
	}
	sh.resEnum.Value = export.Resolutions[1].ID // 1080p
	sh.fpsEnum.Value = "30"
	sh.codecEnum.Value = "h264"
	sh.sens.Value = sensitivityToSlider(cfg.Sensitivity)
	sh.glow.Value = float32(cfg.Glow / glowMax)
	sh.bgInt.Value = float32(cfg.BackgroundIntensity / backgroundIntensityMax)
	sh.rememberLastValues()
}

// rememberLastValues snapshots the current widget values as the
// change-detection baseline.
func (sh *shell) rememberLastValues() {
	sh.lastVolume = sh.volume.Value
	sh.lastModeValue = sh.modeEnum.Value
	sh.lastPaletteValue = sh.paletteEnum.Value
	sh.lastBgValue = sh.bgEnum.Value
	sh.lastIntensityValue = sh.intensityEnum.Value
	sh.lastPrefValue = sh.rendererPrefEnum.Value
	sh.lastResValue = sh.resEnum.Value
	sh.lastFpsValue = sh.fpsEnum.Value
	sh.lastCodecValue = sh.codecEnum.Value
	sh.lastSens = sh.sens.Value
	sh.lastGlow = sh.glow.Value
	sh.lastBgInt = sh.bgInt.Value
	sh.lastReducedMotion = sh.reducedMotionSw.Value
	sh.lastReducedGlow = sh.reducedGlowSw.Value
}

// backgroundLayerKey maps a config background layer to the enum key.
func backgroundLayerKey(m visualizer.Mode) string {
	if m == "" {
		return string(visualizer.ModeNone)
	}
	return string(m)
}

// layout composes the whole window: command bar, tab strip, workspace,
// status strip.
func (sh *shell) layout(gtx layout.Context) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(sh.commandBar),
		layout.Rigid(sh.tabStrip),
		layout.Flexed(1, sh.destinationPanel),
		layout.Rigid(sh.statusStrip),
	)
}

// commandBar — brand title + the top commands.
func (sh *shell) commandBar(gtx layout.Context) layout.Dimensions {
	return sh.surfaceBox(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Left: unit.Dp(8), Right: unit.Dp(8), Top: unit.Dp(4), Bottom: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					t := material.H6(sh.th.Theme(), brand.ProductName)
					t.Color = sh.th.Text()
					return layout.Inset{Right: unit.Dp(12)}.Layout(gtx, t.Layout)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return sh.barButton(gtx, &sh.btnImport, "Import") }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return sh.barButton(gtx, &sh.btnOpen, "Open") }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return sh.barButton(gtx, &sh.btnSave, "Save") }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return sh.barButton(gtx, &sh.btnExport, "Export") }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return sh.barButtonPrimary(gtx, &sh.btnPlay, transportButtonLabel(sh.state.Clock()))
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return sh.barButton(gtx, &sh.btnStop, "Stop") }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return sh.barButton(gtx, &sh.btnBack, "−5s") }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return sh.barButton(gtx, &sh.btnFwd, "+5s") }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Left: unit.Dp(6), Right: unit.Dp(6)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min.X = gtx.Dp(unit.Dp(140))
						return material.Slider(sh.th.Theme(), &sh.volume).Layout(gtx)
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return sh.barButton(gtx, &sh.btnMedia, "Media Tools") }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return sh.barButton(gtx, &sh.btnHelp, "Help") }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return sh.barButton(gtx, &sh.btnQuit, "Quit") }),
			)
		})
	})
}

// barButton renders a secondary (volcanic) command-bar button.
func (sh *shell) barButton(gtx layout.Context, c *widget.Clickable, txt string) layout.Dimensions {
	b := material.Button(sh.th.Theme(), c, txt)
	b.Background = sh.th.Volcanic()
	b.Inset = layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(6), Left: unit.Dp(8), Right: unit.Dp(8)}
	return layout.Inset{Right: unit.Dp(4)}.Layout(gtx, b.Layout)
}

// barButtonPrimary renders the primary (accent) command-bar button.
func (sh *shell) barButtonPrimary(gtx layout.Context, c *widget.Clickable, txt string) layout.Dimensions {
	b := material.Button(sh.th.Theme(), c, txt)
	b.Inset = layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(6), Left: unit.Dp(10), Right: unit.Dp(10)}
	return layout.Inset{Right: unit.Dp(4)}.Layout(gtx, b.Layout)
}

// tabStrip — the 11 destinations in the persisted tab order.
func (sh *shell) tabStrip(gtx layout.Context) layout.Dimensions {
	return sh.surfaceBox(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Left: unit.Dp(8), Right: unit.Dp(8), Top: unit.Dp(2), Bottom: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			catalog := sh.tabs.OrderedCatalog()
			children := make([]layout.FlexChild, 0, len(catalog))
			for _, info := range catalog {
				info := info
				children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					active := sh.tabs.Layout.ActiveTab == info.ID
					b := material.Button(sh.th.Theme(), sh.tabClicks[info.ID], info.Title)
					if active {
						b.Background = sh.th.Accent()
					} else {
						b.Background = sh.th.Volcanic()
					}
					b.Inset = layout.Inset{Top: unit.Dp(5), Bottom: unit.Dp(5), Left: unit.Dp(8), Right: unit.Dp(8)}
					return b.Layout(gtx)
				}))
			}
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Start}.Layout(gtx, children...)
		})
	})
}

// statusStrip — transport state + clock, renderer backend/state, data
// mode (honest, from the state snapshot).
func (sh *shell) statusStrip(gtx layout.Context) layout.Dimensions {
	return sh.surfaceBox(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Left: unit.Dp(8), Right: unit.Dp(8), Top: unit.Dp(3), Bottom: unit.Dp(3)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return sh.caption(gtx, "Status: "+sh.state.Status())
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return sh.caption(gtx, transportLine(sh.state.Clock()))
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					info := sh.state.Renderer()
					return sh.caption(gtx, "renderer="+string(info.Backend)+"/"+string(info.GpuState))
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return sh.caption(gtx, "data="+string(sh.lay.Mode))
				}),
			)
		})
	})
}

// handleEvents processes every widget interaction of one frame and
// dispatches the mapped commands. It runs on the event loop only.
func (sh *shell) handleEvents(gtx layout.Context) {
	sh.handleKeys(gtx)

	// Pending clipboard write (queued by CmdCopyToClipboard).
	if txt := sh.state.TakeClipboard(); txt != "" {
		gtx.Execute(clipboard.WriteCmd{
			Type: "application/text",
			Data: io.NopCloser(strings.NewReader(txt)),
		})
	}

	// Command bar.
	if sh.btnImport.Clicked(gtx) {
		sh.disp.Run(presentation.CmdImportAudio)
	}
	if sh.btnOpen.Clicked(gtx) {
		sh.tabs.SetActive(presentation.DestLibrary)
		sh.syncTabs()
	}
	if sh.btnSave.Clicked(gtx) {
		sh.disp.Run(presentation.CmdProjectSave)
	}
	if sh.btnExport.Clicked(gtx) {
		sh.tabs.SetActive(presentation.DestExport)
		sh.syncTabs()
	}
	if sh.btnPlay.Clicked(gtx) {
		sh.disp.Run(presentation.CmdPlayPause)
	}
	if sh.btnStop.Clicked(gtx) {
		sh.disp.Run(presentation.CmdStop)
	}
	if sh.btnBack.Clicked(gtx) {
		sh.disp.Run(presentation.CmdSeekBack)
	}
	if sh.btnFwd.Clicked(gtx) {
		sh.disp.Run(presentation.CmdSeekForward)
	}
	if sh.btnMedia.Clicked(gtx) {
		sh.disp.Run(presentation.CmdMediaToolsOpen)
	}
	if sh.btnHelp.Clicked(gtx) {
		sh.disp.Run(presentation.CmdHelpOpen)
	}
	if sh.btnQuit.Clicked(gtx) {
		sh.disp.Run(presentation.CmdQuit)
	}
	if sh.volume.Value != sh.lastVolume {
		sh.disp.Run(presentation.CmdSetVolume)
		sh.lastVolume = sh.volume.Value
	}

	// Tab strip.
	for _, info := range sh.tabs.OrderedCatalog() {
		if sh.tabClicks[info.ID].Clicked(gtx) {
			sh.tabs.SetActive(info.ID)
			sh.syncTabs()
		}
	}

	// Panel buttons with direct commands.
	if sh.importGoBtn.Clicked(gtx) {
		sh.disp.Run(presentation.CmdImportAudio)
	}
	if sh.importCancelBtn.Clicked(gtx) {
		sh.cancelImport()
	}
	if sh.openProjBtn.Clicked(gtx) {
		sh.disp.Run(presentation.CmdProjectOpen)
	}
	if sh.exportStartBtn.Clicked(gtx) {
		sh.disp.Run(presentation.CmdExportStart)
	}
	if sh.exportCancelBtn.Clicked(gtx) {
		sh.disp.Run(presentation.CmdExportCancel)
	}
	if sh.mtInstallBtn.Clicked(gtx) {
		sh.disp.Run(presentation.CmdMediaToolsInstall)
	}
	if sh.mtCancelBtn.Clicked(gtx) {
		sh.mediaTools.Cancel()
	}
	if sh.mtUpdateBtn.Clicked(gtx) {
		sh.checkMediaToolsUpdate()
	}
	if sh.sysAnalyzeBtn.Clicked(gtx) {
		sh.disp.Run(presentation.CmdAnalyzePath)
	}
	if sh.copyDiagBtn.Clicked(gtx) {
		sh.disp.Run(presentation.CmdCopyToClipboard)
	}
	if sh.logsRevealBtn.Clicked(gtx) {
		sh.disp.Run(presentation.CmdLogsReveal)
	}
	if sh.layoutResetBtn.Clicked(gtx) {
		sh.disp.Run(presentation.CmdLayoutReset)
	}

	// Template presets.
	for name, clk := range sh.presetClicks {
		if clk.Clicked(gtx) {
			sh.selectedPreset = name
			sh.disp.Run(presentation.CmdPresetApply)
		}
	}

	// Enum-driven commands (value-compare change detection; the radio
	// layouts already consumed the pointer events this frame).
	if sh.modeEnum.Value != sh.lastModeValue {
		sh.lastModeValue = sh.modeEnum.Value
		sh.disp.Run(presentation.CmdModeSet)
	}
	if sh.paletteEnum.Value != sh.lastPaletteValue {
		sh.lastPaletteValue = sh.paletteEnum.Value
		sh.disp.Run(presentation.CmdPaletteSet)
	}
	if sh.bgEnum.Value != sh.lastBgValue {
		sh.lastBgValue = sh.bgEnum.Value
		sh.disp.Run(presentation.CmdBackgroundLayerSet)
	}
	if sh.intensityEnum.Value != sh.lastIntensityValue {
		sh.lastIntensityValue = sh.intensityEnum.Value
		sh.disp.Run(presentation.CmdIntensitySet)
	}
	if sh.rendererPrefEnum.Value != sh.lastPrefValue {
		sh.lastPrefValue = sh.rendererPrefEnum.Value
		sh.disp.Run(presentation.CmdRendererSelect)
	}
	if sh.resEnum.Value != sh.lastResValue {
		sh.lastResValue = sh.resEnum.Value
	}
	if sh.fpsEnum.Value != sh.lastFpsValue {
		sh.lastFpsValue = sh.fpsEnum.Value
	}
	if sh.codecEnum.Value != sh.lastCodecValue {
		sh.lastCodecValue = sh.codecEnum.Value
	}
	if sh.sens.Value != sh.lastSens {
		sh.lastSens = sh.sens.Value
		sh.disp.Run(presentation.CmdSensitivitySet)
	}
	if sh.glow.Value != sh.lastGlow {
		sh.lastGlow = sh.glow.Value
		sh.applyGlow()
	}
	if sh.bgInt.Value != sh.lastBgInt {
		sh.lastBgInt = sh.bgInt.Value
		sh.applyBackgroundIntensity()
	}
	if sh.reducedMotionSw.Value != sh.lastReducedMotion {
		sh.lastReducedMotion = sh.reducedMotionSw.Value
		sh.disp.Run(presentation.CmdReducedMotionSet)
	}
	if sh.reducedGlowSw.Value != sh.lastReducedGlow {
		sh.lastReducedGlow = sh.reducedGlowSw.Value
		sh.disp.Run(presentation.CmdReducedGlowSet)
	}

	// Service-side config changes (preset apply, project load) flow
	// back into the widgets through the config revision.
	if cfg, rev := sh.state.Config(); rev != sh.lastCfgRev {
		sh.lastCfgRev = rev
		sh.modeEnum.Value = string(cfg.Mode)
		sh.paletteEnum.Value = strconv.Itoa(cfg.Palette)
		sh.bgEnum.Value = backgroundLayerKey(cfg.BackgroundLayer)
		sh.sens.Value = sensitivityToSlider(cfg.Sensitivity)
		sh.glow.Value = float32(cfg.Glow / glowMax)
		sh.bgInt.Value = float32(cfg.BackgroundIntensity / backgroundIntensityMax)
		sh.rememberLastValues()
	}
}

// syncTabs persists the tab state and reports navigation in the
// status strip. Saves are small atomic writes on the event loop.
func (sh *shell) syncTabs() {
	sh.state.SetStatus("Destination: " + string(sh.tabs.Layout.ActiveTab))
	sh.saveLayout()
}

// saveLayout persists the workspace layout through the shared store.
func (sh *shell) saveLayout() {
	if err := sh.store.Save(sh.tabs.Layout); err != nil {
		sh.state.SetStatus("Layout could not be persisted: " + err.Error())
	}
}

// gotoDestination switches the active tab programmatically.
func (sh *shell) gotoDestination(d presentation.Destination) {
	sh.tabs.SetActive(d)
	sh.syncTabs()
}

// navigateIndex applies the mod+1..9,0 navigation selection.
func (sh *shell) navigateIndex() {
	// The keyboard path applies navigation directly (shortcutFor
	// carries the digit); this handler covers the command-table case.
	sh.state.SetStatus("Navigate: use mod+1..9,0 or the tab strip")
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

// applyBackgroundIntensity pushes the background intensity slider.
func (sh *shell) applyBackgroundIntensity() {
	cfg := sh.svc.Config()
	cfg.BackgroundIntensity = float64(sh.bgInt.Value) * backgroundIntensityMax
	cfg.Clamp()
	sh.svc.SetConfig(cfg)
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

// pumpPreview renders one preview frame when pacing allows and keeps
// the frame loop alive at the honest cadence.
func (sh *shell) pumpPreview(gtx layout.Context) {
	if sh.tabs.Layout.ActiveTab != presentation.DestStudio {
		return
	}
	playing := sh.svc.IsPlayingLike()
	now := time.Now()
	if !sh.preview.ShouldRequestFrame(now, playing, sh.th.ReducedMotion()) {
		// Schedule the next check at the exact interval end — no busy
		// polling, no wall-clock parsing.
		gtx.Execute(op.InvalidateCmd{At: sh.preview.NextIdleDeadline(now, sh.th.ReducedMotion())})
		return
	}
	w, h := sh.previewRequestSize()
	fr, err := sh.svc.PreviewFrame(w, h, sh.renderer)
	if err == nil && fr != nil {
		sh.preview.Accept(fr, now)
	}
	sh.win.Invalidate()
}

// previewRequestSize derives the render target from the last laid-out
// pane size, clamped to the bounded preview budget. A resize just
// changes the next request; the renderer is never recreated.
func (sh *shell) previewRequestSize() (int, int) {
	size := sh.previewPaneSize
	if size.X <= 0 || size.Y <= 0 {
		size = image.Pt(480, 270)
	}
	w, h := size.X, size.Y
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
	cfgRevInitial      = ^uint64(0)
)

// joinOrNone renders a capability list or an honest "none".
func joinOrNone(list []string) string {
	if len(list) == 0 {
		return "none"
	}
	return strings.Join(list, ", ")
}
