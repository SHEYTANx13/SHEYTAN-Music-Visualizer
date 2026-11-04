// actions.go — studio UI actions: import/project dialogs, the export
// dialog with honest progress, the preview render loop and small
// widget helpers shared with studio.go.

package fyneapp

import (
	"fmt"
	"image"
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
	resSel.SetSelectedIndex(1) // 1080p default
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
		for range ticker.C {
			u.renderMu.Lock()
			if u.closing {
				u.renderMu.Unlock()
				return
			}
			u.renderMu.Unlock()
			if u.win == nil {
				return
			}
			fr, err := u.svc.PreviewFrame(960, 540, u.renderer)
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
			})
		}
	}()
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
