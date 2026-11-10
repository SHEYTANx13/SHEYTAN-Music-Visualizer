// actions.go — studio UI actions: import/project dialogs, the export
// dialog with honest progress, the preview render loop and small
// widget helpers shared with studio.go.

package fyneapp

import (
	"fmt"
	"image"
	"net/url"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/brand"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/export"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/playback"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/render"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/sysprofile"
)

// onPlayPause toggles playback (wired to the transport button).
func (u *studioUI) onPlayPause() {
	if err := u.svc.PlayPause(); err != nil {
		u.dialogError("Playback", err.Error())
	}
}

// onImport opens the audio file dialog and starts the import.
func (u *studioUI) onImport() {
	fd := dialog.NewFileOpen(func(rc fyne.URIReadCloser, err error) {
		if err != nil {
			u.dialogError("Import", err.Error())
			return
		}
		if rc == nil {
			return // cancelled
		}
		defer rc.Close()
		path := rc.URI().Path()
		if path == "" {
			u.dialogError("Import", "the selected file has no local path")
			return
		}
		u.svc.UpdateMediaSource(path)
		if ierr := u.svc.ImportFile(path); ierr != nil {
			u.dialogError("Import", ierr.Error())
		}
	}, u.win)
	fd.SetFilter(storage.NewExtensionFileFilter([]string{".wav", ".wave", ".mp3", ".flac", ".ogg", ".oga"}))
	fd.Resize(fyne.NewSize(700, 500))
	fd.SetOnClosed(func() { u.reconcileFullscreen("import-dialog-closed") })
	fd.Show()
}

// onSaveProject saves the current project.
func (u *studioUI) onSaveProject() {
	if u.svc.Track() == nil {
		u.dialogError("Save Project", "nothing to save — import a track first")
		return
	}
	fd := dialog.NewFileSave(func(wc fyne.URIWriteCloser, err error) {
		if err != nil {
			u.dialogError("Save Project", err.Error())
			return
		}
		if wc == nil {
			return
		}
		path := wc.URI().Path()
		wc.Close()
		if err := u.svc.SaveProject(path); err != nil {
			u.dialogError("Save Project", err.Error())
		}
	}, u.win)
	fd.SetFileName("project.sheytan.json")
	fd.Resize(fyne.NewSize(700, 500))
	fd.SetOnClosed(func() { u.reconcileFullscreen("save-project-dialog-closed") })
	fd.Show()
}

// onOpenProject loads a project file.
func (u *studioUI) onOpenProject() {
	fd := dialog.NewFileOpen(func(rc fyne.URIReadCloser, err error) {
		if err != nil {
			u.dialogError("Open Project", err.Error())
			return
		}
		if rc == nil {
			return
		}
		path := rc.URI().Path()
		rc.Close()
		if lerr := u.svc.LoadProject(path); lerr != nil {
			u.dialogError("Open Project", lerr.Error())
		}
	}, u.win)
	fd.SetFilter(storage.NewExtensionFileFilter([]string{".json"}))
	fd.Resize(fyne.NewSize(700, 500))
	fd.SetOnClosed(func() { u.reconcileFullscreen("open-project-dialog-closed") })
	fd.Show()
}

// onExport opens the export settings dialog.
func (u *studioUI) onExport() {
	if u.svc.Track() == nil {
		u.dialogError("Export", "import a track before exporting")
		return
	}
	env, err := export.DetectEnv(false)
	if err != nil {
		u.dialogError("Export requires FFmpeg",
			err.Error()+"\n\nSHEYTAN uses your locally installed FFmpeg as the encoder adapter. "+
				"No binary is downloaded or bundled. Install FFmpeg (for example a GPL or LGPL build of FFmpeg 6+) and make sure ffmpeg and ffprobe are on PATH.")
		return
	}
	export.ProofHardwareEncoders(env)
	u.logInfo("export dialog opened", "ffmpeg_source", env.Source, "ffmpeg_version", env.Version)
	u.showExportDialog(env)
}

// showExportDialog builds the export dialog with real settings.
func (u *studioUI) showExportDialog(env *export.Environment) {
	var resSel *widget.Select
	var fpsSel *widget.Select
	var qualSel *widget.Select
	var encSel *widget.Select
	var outEntry *widget.Entry
	var bar *widget.ProgressBar
	var info *widget.Label

	names := make([]string, len(export.Resolutions))
	for i, r := range export.Resolutions {
		names[i] = fmt.Sprintf("%s — %dx%d", r.Name, r.Width, r.Height)
	}
	fpsNames := []string{"24 fps", "30 fps", "60 fps"}
	encNames := []string{"auto (best available)", "libx264 (software)"}
	encNames = append(encNames, env.HardwareEncoders...)

	resSel = widget.NewSelect(names, nil)
	resSel.SetSelectedIndex(u.memoryAwareDefaultResolution()) // honest available-memory default (still fully manual)
	fpsSel = widget.NewSelect(fpsNames, nil)
	fpsSel.SetSelectedIndex(1)
	qualSel = widget.NewSelect([]string{"high (CRF 18)", "balanced (CRF 21)", "compat (CRF 23)"}, nil)
	qualSel.SetSelectedIndex(0)
	encSel = widget.NewSelect(encNames, nil)
	if len(encNames) > 1 {
		encSel.SetSelectedIndex(0)
	}
	outEntry = widget.NewEntry()
	outEntry.SetPlaceHolder("Output .mp4 path (absolute or relative)")

	// ffmpeg capability line (honest, detected)
	caps := fmt.Sprintf("FFmpeg: %s\nH.264 encoders: %s\nAAC audio encoder: %s\nHardware encoders proven: %s",
		env.Version,
		joinOrNone(env.EncoderList(false)),
		yesNo(env.Encoders["aac"]),
		joinOrNone(env.HardwareEncoders))
	info = widget.NewLabel(caps)
	info.Wrapping = fyne.TextWrapWord

	bar = widget.NewProgressBar()
	u.exportBar = bar
	u.exportLabel = widget.NewLabel("")
	u.exportLabel.Wrapping = fyne.TextWrapWord

	cancelBtn := widget.NewButton("Cancel", func() {
		if u.exportJob != nil {
			u.exportJob.Cancel()
		}
	})
	var startBtn *widget.Button
	startBtn = widget.NewButton("Start Export", func() {
		r := export.Resolutions[resSel.SelectedIndex()]
		fps := export.FrameRates[fpsSel.SelectedIndex()]
		quality := []string{"high", "balanced", "compat"}[qualSel.SelectedIndex()]
		enc := "auto"
		if encSel.SelectedIndex() > 0 {
			enc = encNames[encSel.SelectedIndex()]
		}
		out := outEntry.Text
		if out == "" {
			u.dialogError("Export", "choose an output file first")
			return
		}
		req := export.Request{
			OutputPath:    out,
			Width:         r.Width,
			Height:        r.Height,
			FPS:           fps,
			QualityPreset: quality,
			Encoder:       enc,
			PreferGPU:     true,
		}
		if jerr := u.svc.StartExport(req); jerr != nil {
			u.dialogError("Export", jerr.Error())
			return
		}
		u.exportJob = u.svc.ExportJob()
		startBtn.Disable()
		info.SetText("Exporting… progress below is real (rendered frames / encoder output).")
	})

	form := widget.NewForm(
		widget.NewFormItem("Resolution", resSel),
		widget.NewFormItem("Frame rate", fpsSel),
		widget.NewFormItem("Quality", qualSel),
		widget.NewFormItem("H.264 encoder", encSel),
		widget.NewFormItem("Output file", outEntry),
	)
	content := container.NewVBox(
		info,
		form,
		bar,
		u.exportLabel,
		container.NewHBox(startBtn, cancelBtn),
	)
	d := dialog.NewCustom("Export MP4 — "+brand.ProductName, "Close", content, u.win)
	d.Resize(fyne.NewSize(640, 520))
	d.SetOnClosed(func() {
		u.exportDialog = nil
		u.exportBar = nil
		u.exportLabel = nil
		u.exportJob = nil
	})
	u.exportDialog = d
	d.Show()
}

// startRenderLoop repaints the preview at ~30 fps from the audio
// clock. The loop owns the renderer; the image is converted and
// swapped on the UI thread via fyne.Do.
func (u *studioUI) startRenderLoop() {
	go func() {
		ticker := time.NewTicker(time.Second / 30)
		defer ticker.Stop()
	forRunning:
		for {
			// Renderer preference changes are handled by THIS goroutine
			// between frames, so a renderer can never be swapped or
			// closed mid-flight. Newest request supersedes (buffered).
			select {
			case pref := <-u.rendererReq:
				r, info := render.SelectRendererExplicit(pref)
				u.renderMu.Lock()
				old := u.renderer
				u.renderer = r
				u.rendererInfo = info
				u.renderMu.Unlock()
				if old != nil {
					old.Close() // no frame in flight: this goroutine owns both
				}
				u.logInfo("renderer switched",
					"preference", string(pref),
					"backend", string(info.Backend),
					"gpu_state", string(info.GpuState),
					"adapter", info.Adapter,
					"device_class", info.DeviceClass,
					"detail", info.Detail)
			default:
			}
			u.renderMu.Lock()
			closing := u.closing
			r := u.renderer
			u.renderMu.Unlock()
			if closing {
				return
			}
			if r == nil || u.win == nil {
				continue forRunning
			}
			<-ticker.C
			fr, err := u.svc.PreviewFrame(960, 540, r)
			if err != nil || fr == nil || fr.Image == nil {
				continue
			}
			img := fr.Image
			// keep a copy for the canvas because the renderer reuses
			// the buffer for the next frame
			cp := image.NewRGBA(img.Bounds())
			copy(cp.Pix, img.Pix)
			info := fr.Info
			clock := fr.Clock
			fyne.Do(func() {
				u.preview.Image = cp
				u.preview.Refresh()
				u.refreshTransportFrom(clock, info)
				// Live honest state: the EXECUTING renderer's evidence
				// (e.g. GpuVerified after the first real frame, or
				// GpuFallback after device loss) drives the UI labels.
				u.renderMu.Lock()
				u.rendererInfo = info
				u.renderMu.Unlock()
				u.rendererLB.SetText("renderer: " + rendererSummary(info))
			})
		}
	}()
}

// memoryAwareDefaultResolution returns the default export resolution
// index derived from the CURRENTLY AVAILABLE memory (never total
// memory alone). Unknown availability keeps the shipped 1080p default.
// The user keeps full manual control — this only preselects. The
// profile collection runs on this dialog's UI-thread moment and only
// reads sysfs/registry-class native sources (fast, local).
func (u *studioUI) memoryAwareDefaultResolution() int {
	probe := sysprofile.Collect(u.currentRendererInfo())
	budget := sysprofile.MemoryBudgetFor(probe)
	for i, res := range export.Resolutions {
		if res.Height == budget.RecommendedExportHeight {
			return i
		}
	}
	return 1 // 1080p default index
}

// refreshTransportFrom updates the transport widgets from a clock
// snapshot (called on the UI thread from the render loop).
func (u *studioUI) refreshTransportFrom(clock playback.ClockInfo, info render.BackendInfo) {
	if clock.State == playback.StatePlaying {
		u.btnPlay.SetText("⏸ Pause")
	} else {
		u.btnPlay.SetText("▶ Play")
	}
	if !u.scrubbing && clock.Total > 0 {
		u.scrubber.SetValue(1000 * clock.Position.Seconds() / clock.Total.Seconds())
	}
	sec := int(clock.Position.Seconds())
	u.timeCur.SetText(fmt.Sprintf("%d:%02d", sec/60, sec%60))
	_ = info
}

// logInfo/logErr are nil-safe runtime-log helpers (the UI stays fully
// functional even when logging fell back to stderr).
func (u *studioUI) logInfo(msg string, args ...any) {
	if u.log != nil {
		u.log.Info(msg, args...)
	}
}

func (u *studioUI) logErr(msg string, args ...any) {
	if u.log != nil {
		u.log.Error(msg, args...)
	}
}

// reconcileFullscreen re-applies the persisted fullscreen intent after
// a dialog closed. Event-driven and debounced inside the controller —
// never a loop, never focus stealing.
func (u *studioUI) reconcileFullscreen(reason string) {
	if u.fs != nil {
		u.fs.Reconcile(reason)
	}
}

// onHelp shows the Help / Diagnostics surface: the resolved data mode
// with its reason, the exact runtime log path, the Open Logs Folder
// and Copy Log Path actions, and a fullscreen toggle. These are the
// user paths that make runtime logs attachable without searching
// hidden system folders.
func (u *studioUI) onHelp() {
	u.logInfo("help-diagnostics opened")
	modeLine := fmt.Sprintf("Data mode: %s", u.lay.Mode)
	reasonLine := "Location rule: " + u.lay.Reason
	rootLine := "Data root: " + u.lay.DataRoot
	logLine := "Runtime log: " + u.logFilePath()

	logLabel := widget.NewLabel(logLine)
	logLabel.Wrapping = fyne.TextWrapWord
	logLabel.TextStyle = fyne.TextStyle{Monospace: true}
	rootLabel := widget.NewLabel(modeLine + "\n" + reasonLine + "\n" + rootLine)
	rootLabel.Wrapping = fyne.TextWrapWord

	openBtn := widget.NewButton("Open Logs Folder", func() {
		u.openLogsFolder()
	})
	copyBtn := widget.NewButton("Copy Log Path", func() {
		u.setClipboard(u.logFilePath())
		u.status("Log path copied to clipboard: " + u.logFilePath())
		u.logInfo("log path copied to clipboard")
	})
	fsBtn := widget.NewButton("Toggle Fullscreen (F11)", func() {
		u.fs.Toggle("help-dialog")
	})

	hint := widget.NewLabel("Attach app.log when reporting a problem. " +
		"The log contains UTC timestamps, severity and subsystem events — never audio content or credentials.")
	hint.Wrapping = fyne.TextWrapWord

	content := container.NewVBox(
		rootLabel, logLabel, widget.NewSeparator(),
		container.NewHBox(openBtn, copyBtn, fsBtn),
		hint,
	)
	d := dialog.NewCustom("Help / Diagnostics — "+brand.ProductName, "Close", content, u.win)
	d.Resize(fyne.NewSize(680, 380))
	d.SetOnClosed(func() { u.reconcileFullscreen("help-dialog-closed") })
	d.Show()
}

// openLogsFolder asks the operating system to reveal the runtime log
// directory. Fyne v2.8.2's App.OpenURL accepts a *net/url.URL — NOT a
// fyne.URI — so the Fyne file URI is validated by parsing its String()
// value into a real URL first (url.Parse also percent-escapes spaces
// and other reserved characters). Every failure path stays honest:
// the exact error is logged and surfaced in a dialog that always shows
// the usable absolute path, so the action never fails silently and
// never spawns a shell or a second window.
func (u *studioUI) openLogsFolder() {
	u.logInfo("open-logs-folder requested", "dir", u.lay.Logs)
	if u.app == nil {
		u.dialogInfo("Logs folder", u.lay.Logs)
		return
	}
	parsed, err := url.Parse(storage.NewFileURI(u.lay.Logs).String())
	if err != nil {
		u.logErr("open-logs-folder: log path is not a valid URL", "error", err.Error())
		u.dialogInfo("Logs folder", u.lay.Logs+"\n\n(OS open skipped — the location could not be converted to a URL: "+err.Error()+")")
		return
	}
	if oerr := u.app.OpenURL(parsed); oerr != nil {
		u.logErr("open-logs-folder failed", "error", oerr.Error())
		u.dialogInfo("Logs folder", u.lay.Logs+"\n\n(The operating system could not open it: "+oerr.Error()+")")
	}
}

// setClipboard copies via the app clipboard (Window.Clipboard is
// deprecated upstream) with a window fallback when no app is held.
func (u *studioUI) setClipboard(text string) {
	if u.app != nil {
		u.app.Clipboard().SetContent(text)
		return
	}
	u.win.Clipboard().SetContent(text)
}

// logFilePath returns the path shown to users: the real file when file
// logging works, an explanatory fallback otherwise.
func (u *studioUI) logFilePath() string {
	if u.logFile != nil && !u.logFile.IsFallback() && u.logFile.Path() != "" {
		return u.logFile.Path()
	}
	return "(file log unavailable — stderr fallback active; data root: " + u.lay.DataRoot + ")"
}

// dialogError shows an error dialog.
func (u *studioUI) dialogError(title, msg string) {
	dialog.NewError(fmt.Errorf("%s", msg), u.win).Show()
	_ = title
}

// dialogInfo shows an information dialog.
func (u *studioUI) dialogInfo(title, msg string) {
	dialog.NewInformation(title, msg, u.win).Show()
}

// yesNo renders a bool.
func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "NO"
}

// joinOrNone renders a string list.
func joinOrNone(list []string) string {
	if len(list) == 0 {
		return "(none)"
	}
	out := ""
	for i, s := range list {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}
