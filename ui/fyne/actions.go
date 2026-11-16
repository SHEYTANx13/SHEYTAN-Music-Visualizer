// actions.go — studio UI actions: import/project dialogs, the export
// dialog with honest progress, the preview render loop and small
// widget helpers shared with studio.go.

package fyneapp

import (
	"fmt"
	"image"
	"net/url"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/brand"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/export"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/export/estimate"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/logging"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/playback"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/render"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/sysprofile"
)

// onPlayPause toggles playback (wired to the transport button).
func (u *studioUI) onPlayPause() {
	// v3 §C structured diagnostics: the request is logged before the
	// service call (the transport's own event stream — play.preroll.*,
	// play.state.changed, play.eos, … — is wired in Run()).
	if u.log != nil {
		if clock := u.svc.Clock(); clock.State == playback.StatePlaying {
			u.log.Info("play.pause.requested", "session", clock.Session,
				"position", clock.Position.String())
		} else {
			u.log.Info("play.requested", "session", clock.Session,
				"position", clock.Position.String())
		}
	}
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
		// No FFmpeg anywhere: playback and the visualizers still work;
		// exports need the tools. Offer the in-app Media Tools & Codecs
		// manager (run 43712071111) instead of a dead end.
		u.logWarn("export unavailable: no FFmpeg resolved", "err", err.Error())
		msg := widget.NewLabel(err.Error() + "\n\nExports need FFmpeg (encoder adapter). Playback and the visualizers keep working without it.\n" +
			"You can install the pinned, verified tool pack from inside the app (Media Tools & Codecs), place your own ffmpeg+ffprobe pair in the app's tools/ffmpeg/bin folder, or install FFmpeg on your PATH.")
		msg.Wrapping = fyne.TextWrapWord
		scroll := container.NewVScroll(msg)
		open := widget.NewButton("Open Media Tools & Codecs…", func() { u.onMediaTools() })
		d := dialog.NewCustom("Export needs media tools", "Close",
			container.NewVBox(scroll, open), u.win)
		u.showCappedDialog(d, scroll)
		return
	}
	export.ProofHardwareEncoders(env)
	u.logInfo("export dialog opened", "ffmpeg_source", env.Source, "ffmpeg_version", env.Version)
	u.showExportDialog(env)
}

// showExportDialog builds the export dialog with real settings (run
// 43712071111: H.264/H.265 codec choice with only encoders the local
// build actually has, exact frame rates incl. common fractional rates
// and a custom rate, audio bitrate, a pre-export size estimate and a
// scrollable, viewport-capped dialog — no control can overflow the
// tab area at any window size).
func (u *studioUI) showExportDialog(env *export.Environment) {
	resNames := make([]string, len(export.Resolutions))
	for i, r := range export.Resolutions {
		resNames[i] = fmt.Sprintf("%s — %dx%d", r.Name, r.Width, r.Height)
	}
	fpsNames := make([]string, len(export.CommonFrameRates))
	for i, c := range export.CommonFrameRates {
		fpsNames[i] = c.Label
	}
	customRes, customFPS := "Custom…", "Custom…"
	resNames = append(resNames, customRes)
	fpsNames = append(fpsNames, customFPS)

	// Codec choices: H.265 only when the local build has a real HEVC
	// encoder (libx265 or a proven hardware HEVC encoder).
	hevcAvailable := env.Encoders["libx265"] || len(env.HEVCEncoders(env)) > 0
	codecNames := []string{"H.264 (AVC) — default"}
	if hevcAvailable {
		codecNames = append(codecNames, "H.265 (HEVC)")
	}

	qualNames := []string{"high (best quality)", "balanced (recommended)", "compat (smaller files)"}
	audioNames := []string{"AAC 192 kbps (default)", "AAC 128 kbps", "AAC 256 kbps", "AAC 320 kbps"}

	resSel := widget.NewSelect(resNames, nil)
	resSel.SetSelectedIndex(u.memoryAwareDefaultResolution())
	fpsSel := widget.NewSelect(fpsNames, nil)
	fpsSel.SetSelectedIndex(3) // 30 fps
	codecSel := widget.NewSelect(codecNames, nil)
	codecSel.SetSelectedIndex(0)
	qualSel := widget.NewSelect(qualNames, nil)
	qualSel.SetSelectedIndex(1)
	audioSel := widget.NewSelect(audioNames, nil)
	audioSel.SetSelectedIndex(0)

	wEntry := widget.NewEntry()
	wEntry.SetPlaceHolder("width (even, e.g. 1920)")
	hEntry := widget.NewEntry()
	hEntry.SetPlaceHolder("height (even, e.g. 1080)")
	fpsEntry := widget.NewEntry()
	fpsEntry.SetPlaceHolder("e.g. 29.97 or 24000/1001")
	for _, e := range []*widget.Entry{wEntry, hEntry, fpsEntry} {
		e.Hide()
	}
	resSel.OnChanged = func(sel string) {
		if sel == customRes {
			wEntry.Show()
			hEntry.Show()
		} else {
			wEntry.Hide()
			hEntry.Hide()
		}
	}
	fpsSel.OnChanged = func(sel string) {
		if sel == customFPS {
			fpsEntry.Show()
		} else {
			fpsEntry.Hide()
		}
	}

	// Encoder choices follow the codec selection (auto by default).
	encSel := widget.NewSelect([]string{"auto (best available)"}, nil)
	encSel.SetSelectedIndex(0)
	refreshEncoders := func(codec string) {
		names := []string{"auto (best available)"}
		if codec == export.CodecHEVC {
			if env.Encoders["libx265"] {
				names = append(names, "libx265 (software)")
			}
			names = append(names, env.HEVCEncoders(env)...)
		} else {
			if env.Encoders["libx264"] {
				names = append(names, "libx264 (software)")
			}
			names = append(names, env.H264HardwareEncoders(env)...)
		}
		encSel.Options = names
		encSel.SetSelectedIndex(0)
	}
	refreshEncoders(export.CodecH264)
	codecSel.OnChanged = func(sel string) {
		if sel == "H.265 (HEVC)" {
			refreshEncoders(export.CodecHEVC)
		} else {
			refreshEncoders(export.CodecH264)
		}
	}

	outEntry := widget.NewEntry()
	outEntry.SetPlaceHolder("Output .mp4 path (absolute or relative)")

	// Capability line: honest, detected — now including HEVC.
	caps := fmt.Sprintf("FFmpeg: %s (source: %s)\nH.264: %s\nH.265: %s\nAAC: %s\nProven hardware: %s",
		env.Version, env.Source,
		joinOrNone(env.CodecEncoderList(export.CodecH264)),
		joinOrNone(env.CodecEncoderList(export.CodecHEVC)),
		yesNo(env.Encoders["aac"]),
		joinOrNone(env.HardwareEncoders))
	info := widget.NewLabel(caps)
	info.Wrapping = fyne.TextWrapWord
	if !hevcAvailable {
		info.SetText(caps + "\nH.265 unavailable: this FFmpeg build has no libx265 and no proven hardware HEVC encoder.")
	}

	estimateLB := widget.NewLabel("Size estimate: run Estimate (encodes a short sample of the actual content).")
	estimateLB.Wrapping = fyne.TextWrapWord

	bar := widget.NewProgressBar()
	u.exportBar = bar
	u.exportLabel = widget.NewLabel("")
	u.exportLabel.Wrapping = fyne.TextWrapWord

	cancelBtn := widget.NewButton("Cancel", func() {
		if u.exportJob != nil {
			u.exportJob.Cancel()
		}
	})
	var estimateBtn *widget.Button
	estimateBtn = widget.NewButton("Estimate Size", func() {
		u.runSizeEstimate(env, resSel, fpsSel, fpsEntry, codecSel, qualSel, audioSel, wEntry, hEntry, estimateLB, estimateBtn)
	})
	var startBtn *widget.Button
	startBtn = widget.NewButton("Start Export", func() {
		w, h := 0, 0
		if resSel.SelectedIndex() == len(resNames)-1 {
			w, h = atoiOr(wEntry.Text, -1), atoiOr(hEntry.Text, -1)
			if w <= 0 || h <= 0 || w%2 != 0 || h%2 != 0 {
				u.dialogError("Export", "custom resolution needs positive EVEN width and height")
				return
			}
		} else {
			r := export.Resolutions[resSel.SelectedIndex()]
			w, h = r.Width, r.Height
		}
		var rate export.FrameRate
		var err error
		if fpsSel.SelectedIndex() == len(fpsNames)-1 {
			rate, err = export.ParseFrameRate(fpsEntry.Text)
			if err != nil {
				u.dialogError("Export", err.Error())
				return
			}
		} else {
			rate = export.CommonFrameRates[fpsSel.SelectedIndex()].Rate
		}
		codec := export.CodecH264
		if codecSel.SelectedIndex() == 1 {
			codec = export.CodecHEVC
		}
		enc := "auto"
		if encSel.SelectedIndex() > 0 {
			enc = encSel.Options[encSel.SelectedIndex()]
			if i := indexOr(enc, " ("); i > 0 {
				enc = enc[:i]
			}
		}
		audio := []string{"192k", "128k", "256k", "320k"}[audioSel.SelectedIndex()]
		out := outEntry.Text
		if out == "" {
			u.dialogError("Export", "choose an output file first")
			return
		}
		req := export.Request{
			OutputPath:    out,
			Width:         w,
			Height:        h,
			FPSRate:       rate,
			FPS:           int(rate.Float()),
			VideoCodec:    codec,
			QualityPreset: []string{"high", "balanced", "compat"}[qualSel.SelectedIndex()],
			Encoder:       enc,
			AudioBitrate:  audio,
			PreferGPU:     true,
		}
		if jerr := u.svc.StartExport(req); jerr != nil {
			u.dialogError("Export", jerr.Error())
			return
		}
		u.exportJob = u.svc.ExportJob()
		startBtn.Disable()
		estimateBtn.Disable()
		info.SetText("Exporting… progress below is real (rendered frames / encoder output).")
	})

	form := widget.NewForm(
		widget.NewFormItem("Video codec", codecSel),
		widget.NewFormItem("Resolution", resSel),
		widget.NewFormItem("Custom size", container.NewVBox(wEntry, hEntry)),
		widget.NewFormItem("Frame rate", fpsSel),
		widget.NewFormItem("Custom fps", fpsEntry),
		widget.NewFormItem("Encoder", encSel),
		widget.NewFormItem("Quality", qualSel),
		widget.NewFormItem("Audio", audioSel),
		widget.NewFormItem("Output file", outEntry),
	)
	audioNote := widget.NewLabel("Audio: AAC in MP4 (tested muxer/encoder combination). Other codecs are not offered for MP4 rather than failing after a long export.")
	audioNote.Wrapping = fyne.TextWrapWord
	content := container.NewVBox(
		info,
		form,
		audioNote,
		widget.NewSeparator(),
		estimateLB,
		bar,
		u.exportLabel,
		container.NewHBox(startBtn, estimateBtn, cancelBtn),
	)
	scroll := container.NewVScroll(content)
	d := dialog.NewCustom("Export MP4 — "+brand.ProductName, "Close", scroll, u.win)
	u.showCappedDialog(d, scroll)
	d.SetOnClosed(func() {
		u.exportDialog = nil
		u.exportBar = nil
		u.exportLabel = nil
		// Keep the job reference while an export is running so the
		// Cancel control stays reachable when the dialog is reopened,
		// and so progress keeps flowing to a reopened dialog. Closing
		// the dialog never abandons a running export silently.
		if u.exportJob != nil && u.exportJob.Running() {
			u.status("Export continues in background — reopen Export MP4 to watch or cancel it")
			return
		}
		u.exportJob = nil
	})
	u.exportDialog = d
}

// runSizeEstimate estimates the output size by encoding a short sample
// of the ACTUAL visualizer and audio with the selected settings (CRF
// sizes cannot be known from settings alone) and extrapolating a range.
func (u *studioUI) runSizeEstimate(env *export.Environment, resSel, fpsSel *widget.Select, fpsEntry *widget.Entry,
	codecSel, qualSel, audioSel *widget.Select, wEntry, hEntry *widget.Entry, out *widget.Label, btn *widget.Button) {
	track := u.svc.Track()
	if track == nil {
		u.dialogError("Size estimate", "import a track first")
		return
	}
	w, h := 0, 0
	if resSel.SelectedIndex() == len(resSel.Options)-1 {
		w, h = atoiOr(wEntry.Text, -1), atoiOr(hEntry.Text, -1)
	} else {
		r := export.Resolutions[resSel.SelectedIndex()]
		w, h = r.Width, r.Height
	}
	var rate export.FrameRate
	var err error
	if fpsSel.SelectedIndex() == len(fpsSel.Options)-1 {
		rate, err = export.ParseFrameRate(fpsEntry.Text)
	} else {
		rate = export.CommonFrameRates[fpsSel.SelectedIndex()].Rate
	}
	if w <= 0 || h <= 0 || err != nil || !rate.Valid() {
		out.SetText("Size estimate: fix the resolution/frame rate first.")
		return
	}
	codec := export.CodecH264
	if codecSel.SelectedIndex() == 1 {
		codec = export.CodecHEVC
	}
	quality := []string{"high", "balanced", "compat"}[qualSel.SelectedIndex()]
	audio := []string{"192k", "128k", "256k", "320k"}[audioSel.SelectedIndex()]

	btn.Disable()
	out.SetText("Size estimate: encoding a short sample of the actual content…")
	go func() {
		defer func() {
			if r := recover(); r != nil {
				u.recoverWorker("export-estimate", "sample encode", r)
			}
		}()
		rng, note := u.svc.EstimateBySample(export.Request{
			Width: w, Height: h, FPSRate: rate, VideoCodec: codec,
			QualityPreset: quality, AudioBitrate: audio, Encoder: "auto",
		}, track)
		fyne.Do(func() {
			btn.Enable()
			if rng.Low <= 0 {
				out.SetText("Size estimate: unavailable (" + note + ")")
				return
			}
			out.SetText(fmt.Sprintf("Size estimate: %s – %s (expected ~%s)\nMethod: %s\n%s",
				estimate.HumanBytes(rng.Low), estimate.HumanBytes(rng.High), estimate.HumanBytes(rng.Expected), rng.Method, rng.Note))
			u.logInfo("size estimate computed", "low", rng.Low, "expected", rng.Expected, "high", rng.High, "method", rng.Method)
		})
	}()
}

// atoiOr parses an integer with a fallback.
func atoiOr(s string, fallback int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return fallback
	}
	return n
}

// indexOr finds a substring index with -1 fallback.
func indexOr(s, sub string) int {
	i := strings.Index(s, sub)
	if i < 0 {
		return -1
	}
	return i
}

// previewBufCount is the frame-copy ring depth for the preview loop.
// The canvas retains the currently displayed image; two extra slots
// guarantee a buffer is only overwritten after multiple newer frames
// have been presented, so neither the canvas nor an in-flight paint
// can observe a torn frame.
const previewBufCount = 3

// startRenderLoop repaints the preview at ~30 fps from the audio
// clock. The loop owns the renderer; the image is converted and
// swapped on the UI thread via fyne.Do.
func (u *studioUI) startRenderLoop() {
	go func() {
		// Panic boundary (run 43712071111): a panic in the render loop (or
		// inside the wgpu bindings) must never terminate the app. The
		// recovered stack is preserved in a bounded crash report under the
		// crashes/ directory and the runtime log; the renderer is released
		// on THIS goroutine (its owner) and the loop exits cleanly. The
		// preview stays frozen for the session instead of crashing the app.
		defer func() {
			if r := recover(); r != nil {
				stack := debug.Stack()
				crashPath, werr := logging.WriteCrashReport(u.lay.Crashes,
					"render-loop", "preview render loop", r, stack)
				u.logErr("recovered panic in render loop (app continues; preview loop stopped)",
					"recovered", fmt.Sprint(r),
					"crash_report", crashPath,
					"crash_report_err", errString(werr))
				u.renderMu.Lock()
				rend := u.renderer
				u.renderer = nil
				u.renderMu.Unlock()
				if rend != nil {
					rend.Close() // this goroutine owns the renderer
				}
			}
		}()
		// Loop lifecycle (run 43712071111): closed exactly once on every
		// exit path so joinRenderLoop can wait for full teardown before
		// the logger closes (no log write or fyne.Do outlives the loop).
		defer close(u.loopDone)
		idleSkip := 0
		ticker := time.NewTicker(time.Second / 30)
		defer ticker.Stop()
		var previewBufs [previewBufCount]*image.RGBA
		var previewIdx int
		nextPreviewBuffer := func(b image.Rectangle) *image.RGBA {
			previewIdx = (previewIdx + 1) % previewBufCount
			buf := previewBufs[previewIdx]
			if buf == nil || buf.Bounds() != b {
				buf = image.NewRGBA(b)
				previewBufs[previewIdx] = buf
			}
			return buf
		}
	forRunning:
		for {
			// FIRST-FRAME DEFERRAL (run 43090342995): the initial
			// renderer selection happens HERE — on this loop goroutine,
			// after the window is already visible — instead of blocking
			// the pre-show startup path. The window shows the honest
			// "probing" state until selection completes.
			u.selectOnce.Do(func() {
				pref := render.PrefAuto
				if u.storedPrefFound {
					pref = u.storedPref
				}
				r, info := render.SelectRendererExplicit(pref)
				u.renderMu.Lock()
				u.renderer = r
				u.rendererInfo = info
				u.renderMu.Unlock()
				pol := sysprofile.ModePolicyFor(u.policyMode, sysprofile.Collect(info))
				u.logInfo("renderer selected (background)",
					"preference", string(pref),
					"backend", string(info.Backend),
					"gpu_state", string(info.GpuState),
					"adapter", info.Adapter,
					"device_class", info.DeviceClass,
					"detail", info.Detail,
					"policy_mode", string(u.currentPolicyMode()),
					"policy_workers", pol.Workers,
					"policy_source", pol.Source)
				fyne.Do(func() {
					u.rendererLB.SetText("renderer: " + rendererSummary(info))
				})
			})
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
				// Loop-owned teardown (run 43712071111): close the renderer
				// HERE, on the loop goroutine, at a frame boundary — never
				// from the appclose teardown goroutine (that raced the
				// in-flight frame and could crash inside the bindings).
				if r != nil {
					r.Close()
					u.renderMu.Lock()
					u.renderer = nil
					u.renderMu.Unlock()
				}
				return
			}
			if r == nil || u.win == nil {
				continue forRunning
			}
			<-ticker.C
			// Idle throttle (run 43090342995 performance contract):
			// while nothing is playing the preview repaints at ~6 fps
			// instead of 30 — no tight polling loop, and playback (or an
			// in-flight scrub) restores the full 30 fps rate immediately.
			if !u.svc.IsPlayingLike() && !u.isScrubbing() {
				idleSkip++
				if idleSkip < 5 {
					continue forRunning
				}
			}
			idleSkip = 0
			fr, err := u.svc.PreviewFrame(960, 540, r)
			if err != nil || fr == nil || fr.Image == nil {
				continue
			}
			img := fr.Image
			// Copy into the ring instead of allocating ~2 MiB per frame
			// (run 43541087097 hot-path repair: ~60 MiB/s of allocation
			// churn at 30 fps previously; the renderer reuses its own
			// buffer, so a private copy is still required — but a rotating
			// one, not a fresh heap object per frame).
			cp := nextPreviewBuffer(img.Bounds())
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
	if !u.isScrubbing() && clock.Total > 0 {
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

// logWarn is the warn-level variant of logInfo/logErr.
func (u *studioUI) logWarn(msg string, args ...any) {
	if u.log != nil {
		u.log.Warn(msg, args...)
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
