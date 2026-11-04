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
	"net/url"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/brand"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/buildinfo"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/export"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/playback"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/render"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/studio"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/visualizer"
)

// Run starts the full studio application (replaces the Phase 1
// placeholder). It blocks until the window is closed.
func Run() error {
	a := app.NewWithID(brand.AppID)
	w := a.NewWindow(brand.WindowTitle())
	w.SetPadded(false)

	svc, err := studio.New()
	if err != nil {
		return err
	}
	u := newStudioUI(w, svc)
	w.SetContent(u.build())
	w.Resize(fyne.NewSize(1440, 860))
	w.ShowAndRun()
	u.close()
	svc.Close()
	return nil
}

// studioUI holds the wired widgets and state.
type studioUI struct {
	win fyne.Window
	svc *studio.Studio

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

	// export dialog state
	exportDialog dialog.Dialog
	exportBar    *widget.ProgressBar
	exportLabel  *widget.Label
	exportJob    *export.Job

	renderer render.Renderer
	renderMu sync.Mutex
	closing  bool
}

func newStudioUI(w fyne.Window, svc *studio.Studio) *studioUI {
	// Honest backend selection: GPU when a real hardware adapter can
	// be proven (adapter + device), CPU otherwise, with the exact
	// fallback reason kept in rendererInfo for the About/status UI.
	r, info := render.SelectRenderer()
	return &studioUI{
		win:          w,
		svc:          svc,
		renderer:     r,
		rendererInfo: info,
	}
}

// build constructs the whole studio layout.
func (u *studioUI) build() fyne.CanvasObject {
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
	btnAbout := widget.NewButton("About", u.onAbout)

	top := container.NewBorder(nil, nil,
		container.NewHBox(brandMark, widget.NewLabel(" "), u.trackLabel),
		container.NewHBox(btnImport, btnOpen, btnSave, btnExport, btnAbout),
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
	left := container.NewVScroll(container.NewVBox(
		widget.NewLabelWithStyle("Library", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		formatInfo,
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Presets", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		presetsBox,
	))

	// ---- center preview (actual renderer output)
	bg := canvas.NewRectangle(hexToColor(brand.ColorObsidian))
	u.preview = canvas.NewImageFromImage(nil)
	u.preview.FillMode = canvas.ImageFillContain
	u.emptyState = widget.NewLabel("Import a track to see the visualizer.\nThe preview shows the actual renderer output.")
	u.emptyState.Alignment = fyne.TextAlignCenter
	u.emptyState.Wrapping = fyne.TextWrapWord
	previewStack := container.NewStack(bg,
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

	inspector := container.NewVScroll(container.NewVBox(
		widget.NewLabelWithStyle("Inspector", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		widget.NewForm(
			widget.NewFormItem("Mode", u.modeSelect),
			widget.NewFormItem("Sensitivity", u.sensSl),
			widget.NewFormItem("Smoothing", u.smoothSl),
			widget.NewFormItem("Glow", u.glowSl),
			widget.NewFormItem("Palette", u.paletteSl),
			widget.NewFormItem("Particle density", u.densitySl),
			widget.NewFormItem("Pulse response", u.pulseSl),
			widget.NewFormItem("Title", u.titleEntry),
			widget.NewFormItem("Subtitle", u.subEntry),
		),
		u.showTitleCb, u.watermarkCb,
		widget.NewSeparator(),
		u.dnaLabel,
	))

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

	transport := container.NewBorder(nil, nil,
		container.NewHBox(u.btnPlay, u.btnStop),
		container.NewHBox(widget.NewLabel("Vol"), u.volumeSl, u.analysisLB),
		container.NewHBox(u.timeCur, layout.NewSpacer(), u.scrubber, layout.NewSpacer(), u.timeTot),
	)

	bottom := container.NewVBox(
		widget.NewSeparator(),
		container.NewHBox(u.deviceLB, u.rendererLB),
		transport,
	)

	root := container.NewBorder(top, bottom, left, inspector, previewStack)
	u.syncInspector()
	u.subscribe()
	u.startRenderLoop()
	return root
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
		if u.exportDialog != nil {
			u.exportDialog.Hide()
			u.exportDialog = nil
		}
		u.dialogInfo("Export complete", ev.Message)
	case studio.EventExportFailed:
		u.status("Export failed: " + ev.Message)
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
	if u.svc.DeviceReal() {
		u.deviceLB.SetText(fmt.Sprintf("audio device: active (Oto) · playhead uncertainty ≤ %d ms",
			clock.Latency.Milliseconds()))
	} else {
		u.deviceLB.SetText("audio device: NOT AVAILABLE — silent preview mode (visualizer still runs)")
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
	d.Resize(fyne.NewSize(640, 420))
	d.Show()
}

// close stops the render loop and renderer.
func (u *studioUI) close() {
	u.renderMu.Lock()
	u.closing = true
	u.renderMu.Unlock()
	if u.renderer != nil {
		u.renderer.Close()
	}
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

func modeFromName(name string) visualizer.Mode {
	for _, m := range visualizer.AllModes {
		if m.DisplayName() == name {
			return m
		}
	}
	return visualizer.ModeBars
}
