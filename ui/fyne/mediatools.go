package fyneapp

// mediatools.go — the Media Tools & Codecs surface (run 43712071111):
// the in-app manager for the optional FFmpeg tool pack, attached to
// the main window like Help/Diagnostics (single-window contract). The
// app package ships NO FFmpeg binaries; playback and the visualizers
// keep working without the tools, while FFmpeg-dependent exports need
// the app-managed pack, an app-local portable pair or a system FFmpeg.
// Every state change is marshaled to the UI thread via fyne.Do; every
// worker goroutine has a panic boundary.

import (
	"context"
	"fmt"
	"runtime/debug"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/export"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/logging"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/mediatools"
)

// onMediaTools opens the Media Tools & Codecs dialog.
func (u *studioUI) onMediaTools() {
	u.logInfo("media-tools opened")
	u.showMediaToolsDialog()
}

// showMediaToolsDialog builds the Media Tools & Codecs surface.
func (u *studioUI) showMediaToolsDialog() {
	if u.mediaTools == nil {
		u.dialogError("Media Tools", "the media tools manager is unavailable in this session")
		return
	}
	snap := u.mediaTools.Snapshot()

	status := widget.NewLabel(mediaToolsStatusText(snap))
	status.Wrapping = fyne.TextWrapWord
	packInfo := widget.NewLabel(mediaToolsPackText(snap))
	packInfo.Wrapping = fyne.TextWrapWord
	progress := widget.NewProgressBar()
	stage := widget.NewLabel("")
	stage.Wrapping = fyne.TextWrapWord

	refresh := func(s mediatools.Snapshot) {
		status.SetText(mediaToolsStatusText(s))
		packInfo.SetText(mediaToolsPackText(s))
		if s.State == mediatools.StateDownloading {
			if s.Total > 0 {
				progress.SetValue(float64(s.Downloaded) / float64(s.Total))
			} else {
				progress.SetValue(0)
			}
			stage.SetText(fmt.Sprintf("Downloading… %s / %s (%.1f MiB/s)",
				mediatools.HumanBytes(s.Downloaded), mediatools.HumanBytes(s.Total), s.BytesPerSecond/(1<<20)))
		} else {
			progress.SetValue(0)
			stage.SetText(mediatooolsStageText(s))
		}
	}
	refresh(snap)
	u.mediaTools.Subscribe(func(s mediatools.Snapshot) {
		fyne.Do(func() { refresh(s) })
	})

	runInstall := func() {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					u.recoverWorker("media-tools-install", "Install", r)
				}
			}()
			// Install runs with the manager's own cancellable context
			// (Cancel button -> Manager.Cancel).
			if err := u.mediaTools.Install(context.Background()); err != nil {
				u.logWarn("media tools install finished with error", "err", err.Error())
				return
			}
			u.logInfo("media tools installed", "version", u.mediaTools.PackInfo().Version)
			// The tool environment changed: force a re-detection so the
			// export dialog sees the new pair immediately.
			export.SetPathOverride("")
			_, _ = export.DetectEnv(true)
		}()
	}

	installBtn := widget.NewButton("Download & Install", runInstall)
	cancelBtn := widget.NewButton("Cancel Download", func() { u.mediaTools.Cancel() })
	updateBtn := widget.NewButton("Check for Updates", func() {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					u.recoverWorker("media-tools-check", "CheckForUpdate", r)
				}
			}()
			s, err := u.mediaTools.CheckForUpdate(context.Background())
			if err != nil {
				u.logWarn("media tools check failed", "err", err.Error())
			}
			_ = s
		}()
	})
	repairBtn := widget.NewButton("Repair / Reinstall", runInstall)
	folderBtn := widget.NewButton("Open Tool Folder", func() { u.openPathInOS(mediatools.CurrentBin(u.lay.DataRoot)) })
	licensesBtn := widget.NewButton("View Licenses / Provenance", u.showMediaToolsLicenses)
	removeBtn := widget.NewButton("Remove App-Managed Tools", func() {
		dialog.NewConfirm("Remove app-managed tools?",
			"The app-managed FFmpeg pair under your data directory will be deleted.\n"+
				"A system FFmpeg on PATH or an app-local portable pair is not touched.\n\nRemove?",
			func(ok bool) {
				if !ok {
					return
				}
				go func() {
					defer func() {
						if r := recover(); r != nil {
							u.recoverWorker("media-tools-remove", "Remove", r)
						}
					}()
					if err := u.mediaTools.Remove(); err != nil {
						u.logWarn("media tools remove failed", "err", err.Error())
					}
					_, _ = export.DetectEnv(true)
				}()
			}, u.win).Show()
	})

	privacy := widget.NewLabel("Privacy and provenance: the tool pack is fetched from the pinned upstream provider shown above, " +
		"only when you request it, over HTTPS, and verified against a pinned SHA-256 digest before installation. " +
		"The SHEYTAN application package contains no FFmpeg binaries; the GPL applies to the optional tool pack only. " +
		"Playback and the visualizers keep working without the tools.")
	privacy.Wrapping = fyne.TextWrapWord

	content := container.NewVBox(
		widget.NewLabelWithStyle("Media Tools & Codecs — SHEYTAN Music Visualizer", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		status,
		widget.NewSeparator(),
		packInfo,
		widget.NewSeparator(),
		progress,
		stage,
		widget.NewSeparator(),
		container.NewHBox(installBtn, cancelBtn),
		container.NewHBox(updateBtn, repairBtn),
		container.NewHBox(folderBtn, licensesBtn, removeBtn),
		widget.NewSeparator(),
		privacy,
	)
	scroll := container.NewVScroll(content)
	d := dialog.NewCustom("Media Tools & Codecs", "Close", scroll, u.win)
	u.showCappedDialog(d, scroll)
	d.SetOnClosed(func() { u.reconcileFullscreen("media-tools-closed") })
}

// showMediaToolsLicenses shows the pack's license/provenance documents.
func (u *studioUI) showMediaToolsLicenses() {
	if u.mediaTools == nil {
		return
	}
	pack := u.mediaTools.PackInfo()
	text := fmt.Sprintf("Tool pack: FFmpeg %s (%s)\nLicense class: %s\nSource: %s\nBuild release: %s\nUpstream asset: %s\nPinned SHA-256: %s\n\n%s\n\nThe pack's own COPYING/license documents and the provenance record are copied to:\n%s\n\nSHEYTAN Music Visualizer itself contains no FFmpeg code and ships no FFmpeg binaries. The GPL applies to the optional tool pack only — this is not legal advice.",
		pack.Version, pack.Platform, pack.LicenseClass, pack.SourceURL, pack.BuildURL, pack.URL, pack.SHA256, pack.LicenseNotice, mediatools.LicensesDir(u.lay.DataRoot))
	label := widget.NewLabel(text)
	label.Wrapping = fyne.TextWrapWord
	scroll := container.NewVScroll(label)
	d := dialog.NewCustom("Media Tools — Licenses & Provenance", "Close", scroll, u.win)
	u.showCappedDialog(d, scroll)
}

// openPathInOS reveals a path in the operating system file manager.
func (u *studioUI) openPathInOS(path string) {
	u.logInfo("open-path requested", "dir", path)
	if u.app == nil {
		u.dialogInfo("Tool folder", path)
		return
	}
	if err := u.app.OpenURL(parseURL("file://" + path)); err != nil {
		u.dialogInfo("Tool folder", path+"\n\n(the OS could not open it: "+err.Error()+")")
	}
}

// recoverWorker is the shared panic boundary for UI-spawned workers.
func (u *studioUI) recoverWorker(subsystem, operation string, r any) {
	stack := debug.Stack()
	crashPath, werr := logging.WriteCrashReport(u.lay.Crashes, subsystem, operation, r, stack)
	u.logErr("recovered panic in UI worker (app continues)",
		"subsystem", subsystem, "operation", operation,
		"recovered", fmt.Sprint(r),
		"crash_report", crashPath, "crash_report_err", errString(werr))
	fyne.Do(func() {
		u.status(subsystem + " failed — recovered; see the runtime log")
	})
}

func mediaToolsStatusText(s mediatools.Snapshot) string {
	var b string
	switch s.State {
	case mediatools.StateReady:
		b = "Installed — Ready"
	case mediatools.StateNotInstalled:
		b = "Not installed"
	case mediatools.StateUpdateAvailable:
		b = "Update available"
	case mediatools.StateRepairNeeded:
		b = "Repair needed"
	case mediatools.StateUnsupportedPlatform:
		b = "Unsupported platform"
	case mediatools.StateFailed:
		b = "Failed"
	case mediatools.StateDownloading:
		b = "Downloading…"
	case mediatools.StateVerifying:
		b = "Verifying…"
	case mediatools.StateInstalling:
		b = "Installing…"
	}
	if s.Detail != "" {
		b += "\n" + s.Detail
	}
	if s.LastError != "" {
		b += "\nLast error: " + s.LastError
	}
	if env, err := export.DetectEnv(false); err == nil {
		b += fmt.Sprintf("\nResolved FFmpeg in use: %s (source: %s, %s)", env.Version, env.Source, env.FFmpegPath)
	} else {
		b += "\nResolved FFmpeg in use: none — exports need the tools; playback/visualizers work without them"
	}
	return b
}

func mediaToolsPackText(s mediatools.Snapshot) string {
	if s.Pack == nil {
		return "No pinned pack for this platform."
	}
	p := s.Pack
	enc := "not verified yet (verified after installation)"
	if s.Installed != nil && len(s.Installed.Manifest.Encoders) > 0 {
		enc = fmt.Sprintf("verified: %v", s.Installed.Manifest.Encoders)
	}
	return fmt.Sprintf("Pinned pack: FFmpeg %s (%s)\nDownload: ~%d MiB compressed, ~%d MiB installed (estimates)\nLicense: %s\nSource: %s\nRequired encoders after install: %v\nEncoders: %s",
		p.Version, p.Platform, p.ApproxDownloadMB, p.ApproxInstalledMB, p.LicenseClass, p.SourceURL, p.RequiredEncoders, enc)
}

func mediatooolsStageText(s mediatools.Snapshot) string {
	if s.Stage != "" {
		return "Stage: " + s.Stage
	}
	if s.State == mediatools.StateReady {
		return "Ready — H.264 and H.265 (libx265) exports verified for this pack."
	}
	return ""
}
