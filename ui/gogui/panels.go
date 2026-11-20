// panels.go — the per-destination workspace panels of the Gio shell.
// Every destination is a scrollable surface inside the ONE window; the
// panels read only from the state snapshot (never blocking) and push
// user actions through the command dispatcher.

package gogui

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"path/filepath"
	"strings"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/brand"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/buildinfo"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/export"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/mediatools"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/presentation"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/visualizer"
)

// destinationPanel routes the active destination to its panel.
func (sh *shell) destinationPanel(gtx layout.Context) layout.Dimensions {
	switch sh.tabs.Layout.ActiveTab {
	case presentation.DestHome:
		return sh.panelHome(gtx)
	case presentation.DestStudio:
		return sh.panelStudio(gtx)
	case presentation.DestWorlds:
		return sh.panelUnavailable(gtx, presentation.DestWorlds)
	case presentation.DestTemplates:
		return sh.panelTemplates(gtx)
	case presentation.DestLibrary:
		return sh.panelLibrary(gtx)
	case presentation.DestExport:
		return sh.panelExport(gtx)
	case presentation.DestRequirements:
		return sh.panelRequirements(gtx)
	case presentation.DestDownloads:
		return sh.panelUnavailable(gtx, presentation.DestDownloads)
	case presentation.DestSystemFiles:
		return sh.panelSystemFiles(gtx)
	case presentation.DestSettings:
		return sh.panelSettings(gtx)
	case presentation.DestDiagnostics:
		return sh.panelDiagnostics(gtx)
	default:
		return sh.panelUnavailable(gtx, sh.tabs.Layout.ActiveTab)
	}
}

// scroll renders rows through a vertical material list (the work
// surface scrolls; nothing can push content off-window).
func (sh *shell) scroll(gtx layout.Context, l *widget.List, rows func(gtx layout.Context, i int) layout.Dimensions, n int) layout.Dimensions {
	l.Axis = layout.Vertical
	return material.List(sh.th.Theme(), l).Layout(gtx, n, rows)
}

// section renders a heading over the following rows.
func (sh *shell) section(gtx layout.Context, title string) layout.Dimensions {
	return layout.Inset{Top: unit.Dp(10), Bottom: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		l := material.H6(sh.th.Theme(), title)
		l.Color = sh.th.Accent()
		return l.Layout(gtx)
	})
}

// line renders one body text row.
func (sh *shell) line(gtx layout.Context, s string) layout.Dimensions {
	return layout.Inset{Top: unit.Dp(2), Bottom: unit.Dp(2)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return material.Body1(sh.th.Theme(), s).Layout(gtx)
	})
}

// caption renders one dim caption row.
func (sh *shell) caption(gtx layout.Context, s string) layout.Dimensions {
	return layout.Inset{Top: unit.Dp(1), Bottom: unit.Dp(1)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		l := material.Caption(sh.th.Theme(), s)
		l.Color = sh.th.TextDim()
		return l.Layout(gtx)
	})
}

// surfaceBox paints the panel content area with the deep charcoal
// surface plus the decoration accent line derived from the intensity
// preset (bounded at 0.16 opacity by the token contract).
func (sh *shell) surfaceBox(gtx layout.Context, w layout.Widget) layout.Dimensions {
	macro := op.Record(gtx.Ops)
	dims := w(gtx)
	call := macro.Stop()
	// Accent line at the top of the surface, beneath the content.
	clip.Rect{Max: image.Pt(dims.Size.X, gtx.Dp(unit.Dp(2)))}.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, sh.th.DecorationColor())
	defer call.Add(gtx.Ops)
	return dims
}

// statusColor maps honest status semantics onto the token status colors.
func (sh *shell) statusColor(kind string) color.NRGBA {
	switch kind {
	case "ok":
		return sh.th.StatusSuccess()
	case "warn":
		return sh.th.StatusWarning()
	case "err":
		return sh.th.StatusError()
	default:
		return sh.th.StatusInfo()
	}
}

// labeledRow renders a caption + control pair (form-like rows).
func (sh *shell) labeledRow(gtx layout.Context, label string, w layout.Widget) layout.Dimensions {
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Dp(unit.Dp(120))
			return sh.caption(gtx, label)
		}),
		layout.Rigid(w),
	)
}

// radioRow renders a labeled radio group row.
func (sh *shell) radioRow(gtx layout.Context, label string, group *widget.Enum, keys []string, labels []string) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return sh.caption(gtx, label)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			children := make([]layout.FlexChild, 0, len(keys))
			for i, k := range keys {
				name, key := labels[i], k
				children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Right: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return material.RadioButton(sh.th.Theme(), group, key, name).Layout(gtx)
					})
				}))
			}
			return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Start}.Layout(gtx, children...)
		}),
	)
}

// panelHome — overview: build identity, renderer evidence, data mode
// and quick actions.
func (sh *shell) panelHome(gtx layout.Context) layout.Dimensions {
	return sh.scroll(gtx, &sh.homeList, func(gtx layout.Context, i int) layout.Dimensions {
		switch i {
		case 0:
			return sh.section(gtx, "Home — "+brand.ProductName)
		case 1:
			return sh.surfaceBox(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(10), Right: unit.Dp(10), Bottom: unit.Dp(10), Left: unit.Dp(10)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return sh.line(gtx, sh.buildIdentityText()) }),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return sh.caption(gtx, sh.dataLayoutText()) }),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return sh.caption(gtx, sh.rendererText()) }),
					)
				})
			})
		case 2:
			return sh.section(gtx, "Session")
		case 3:
			return sh.line(gtx, "Status: "+sh.state.Status())
		case 4:
			return sh.line(gtx, "Transport: "+transportLine(sh.state.Clock()))
		case 5:
			return sh.section(gtx, "Quick actions")
		case 6:
			return sh.buttonRow(gtx, []btnDef{
				{clk: &sh.btnImport, txt: "Import audio…", cmd: presentation.CmdImportAudio},
				{clk: &sh.btnPlay, txt: transportButtonLabel(sh.state.Clock()), cmd: presentation.CmdPlayPause},
				{clk: &sh.btnExport, txt: "Export…", cmd: presentation.CmdExportStart},
				{clk: &sh.btnDiag, txt: "Diagnostics", fn: func() { sh.gotoDestination(presentation.DestDiagnostics) }},
			})
		case 7:
			return sh.caption(gtx, "Vertical-slice note: import/open/save use path entry (no native file picker in this build).")
		case 8:
			return sh.section(gtx, "Attribution")
		case 9:
			return sh.caption(gtx, strings.Join(sh.attrLines, "\n"))
		default:
			return layout.Dimensions{}
		}
	}, homePanelRows)
}

// panelTemplates — the built-in visualizer presets; click applies.
func (sh *shell) panelTemplates(gtx layout.Context) layout.Dimensions {
	return sh.scroll(gtx, &sh.templatesList, func(gtx layout.Context, i int) layout.Dimensions {
		if i == 0 {
			return sh.section(gtx, "Templates — built-in presets")
		}
		idx := i - 1
		if idx >= len(sh.presets) {
			return layout.Dimensions{}
		}
		p := sh.presets[idx]
		active := sh.state.ActivePreset() == p.Name
		return layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return sh.surfaceBox(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Top: unit.Dp(8), Right: unit.Dp(8), Bottom: unit.Dp(8), Left: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return sh.line(gtx, p.Name) }),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return sh.caption(gtx, presetSummary(p.Config))
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Top: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								b := material.Button(sh.th.Theme(), sh.presetClicks[p.Name], applyButtonLabel(p.Name, active))
								if !active {
									b.Background = sh.th.Volcanic()
								}
								return b.Layout(gtx)
							})
						}),
					)
				})
			})
		})
	}, len(sh.presets)+1)
}

// applyButtonLabel renders the template action label.
func applyButtonLabel(name string, active bool) string {
	if active {
		return "Applied: " + name
	}
	return "Apply " + name
}

// presetSummary renders the config facts a preset changes.
func presetSummary(c visualizer.Config) string {
	modeName := c.Mode.DisplayName()
	if modeName == "" {
		modeName = string(c.Mode)
	}
	bg := "none"
	if c.BackgroundLayer != visualizer.ModeNone && c.BackgroundLayer != "" {
		bg = string(c.BackgroundLayer)
	}
	return fmt.Sprintf("Mode %s · sensitivity %.1f · glow %.1f · palette %d · background layer %s",
		modeName, c.Sensitivity, c.Glow, c.Palette, bg)
}

// panelLibrary — supported formats + current track metadata + the
// open-project entry.
func (sh *shell) panelLibrary(gtx layout.Context) layout.Dimensions {
	return sh.scroll(gtx, &sh.libraryList, func(gtx layout.Context, i int) layout.Dimensions {
		switch i {
		case 0:
			return sh.section(gtx, "Library — imported songs and project files")
		case 1:
			return sh.line(gtx, formatTrackInfo(sh.state.Track()))
		case 2:
			return sh.section(gtx, "Supported input formats")
		case 3:
			var names []string
			for _, f := range sh.formats {
				ext := strings.Join(f.Extensions, " ")
				kind := "native"
				if !f.NativeDecoder {
					kind = "via local FFmpeg"
				}
				names = append(names, fmt.Sprintf("%s (%s — %s)", f.Container, ext, kind))
			}
			if len(names) == 0 {
				names = append(names, "no decoders registered")
			}
			return sh.caption(gtx, strings.Join(names, "\n"))
		case 4:
			return sh.section(gtx, "Open project")
		case 5:
			return sh.editorRow(gtx, &sh.projectPathEd, "path/to/project.sheytan.json")
		case 6:
			return sh.buttonRow(gtx, []btnDef{
				{clk: &sh.openProjBtn, txt: "Open project", cmd: presentation.CmdProjectOpen},
			})
		case 7:
			return sh.caption(gtx, "The path entry serves Save and Open project; the default Save path lands in the projects data directory.")
		default:
			return layout.Dimensions{}
		}
	}, libraryPanelRows)
}

// editorRow renders a single-line editor with a hint.
func (sh *shell) editorRow(gtx layout.Context, e *widget.Editor, hint string) layout.Dimensions {
	return layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return material.Editor(sh.th.Theme(), e, hint).Layout(gtx)
	})
}

// btnDef is one command bar / panel button definition. Either cmd or
// fn must be set: cmd dispatches a presentation command, fn runs a
// direct shell action (used for the few actions without a command ID,
// such as the media-tools download cancel).
type btnDef struct {
	clk *widget.Clickable
	txt string
	cmd presentation.CommandID
	fn  func()
}

// buttonRow renders buttons; clicking dispatches the command or runs
// the direct action.
func (sh *shell) buttonRow(gtx layout.Context, defs []btnDef) layout.Dimensions {
	children := make([]layout.FlexChild, 0, len(defs))
	for _, d := range defs {
		def := d
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Right: unit.Dp(8), Top: unit.Dp(4), Bottom: unit.Dp(4)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return material.Button(sh.th.Theme(), def.clk, def.txt).Layout(gtx)
			})
		}))
	}
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Start}.Layout(gtx, children...)
}

// panelExport — resolution/FPS/seconds/codec fields, start, progress,
// cancel and the honest FFmpeg availability line.
func (sh *shell) panelExport(gtx layout.Context) layout.Dimensions {
	sh.ensureFFmpegDetection()
	return sh.scroll(gtx, &sh.exportList, func(gtx layout.Context, i int) layout.Dimensions {
		running, stage, pct := sh.state.ExportSnapshot()
		switch i {
		case 0:
			return sh.section(gtx, "Export — deterministic offline MP4")
		case 1:
			return sh.caption(gtx, sh.state.FFmpegText())
		case 2:
			return sh.section(gtx, "Settings")
		case 3:
			resKeys := make([]string, len(export.Resolutions))
			resLabels := make([]string, len(export.Resolutions))
			for i, r := range export.Resolutions {
				resKeys[i] = r.ID
				resLabels[i] = fmt.Sprintf("%s (%dx%d)", r.Name, r.Width, r.Height)
			}
			return sh.radioRow(gtx, "Resolution", &sh.resEnum, resKeys, resLabels)
		case 4:
			fpsKeys := make([]string, len(export.FrameRates))
			fpsLabels := make([]string, len(export.FrameRates))
			for i, f := range export.FrameRates {
				fpsKeys[i] = fmt.Sprintf("%d", f)
				fpsLabels[i] = fmt.Sprintf("%d fps", f)
			}
			return sh.radioRow(gtx, "Frame rate", &sh.fpsEnum, fpsKeys, fpsLabels)
		case 5:
			return sh.radioRow(gtx, "Video codec", &sh.codecEnum, []string{"h264", "hevc"},
				[]string{"H.264 (AVC) — default", "H.265 (HEVC) — when the local FFmpeg provides libx265"})
		case 6:
			return sh.labeledRow(gtx, "Seconds", func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.X = gtx.Dp(unit.Dp(140))
				return sh.editorRow(gtx, &sh.secondsEd, "e.g. 30")
			})
		case 7:
			return sh.editorRow(gtx, &sh.outPathEd, "Output .mp4 path (absolute or relative)")
		case 8:
			return sh.buttonRow(gtx, []btnDef{
				{clk: &sh.exportStartBtn, txt: "Start export", cmd: presentation.CmdExportStart},
				{clk: &sh.exportCancelBtn, txt: "Cancel export", cmd: presentation.CmdExportCancel},
			})
		case 9:
			return sh.section(gtx, "Progress")
		case 10:
			bar := material.ProgressBar(sh.th.Theme(), float32(pct)/100)
			return layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(4)}.Layout(gtx, bar.Layout)
		case 11:
			return sh.line(gtx, exportProgressLine(running, stage, pct))
		case 12:
			return sh.caption(gtx, "Exports need the local FFmpeg encoder adapter; playback and the visualizers work without it. The requirement is surfaced in Requirements and in Diagnostics.")
		default:
			return layout.Dimensions{}
		}
	}, exportPanelRows)
}

// exportProgressLine renders the honest progress line.
func exportProgressLine(running bool, stage string, pct float64) string {
	if running {
		return fmt.Sprintf("Exporting… %s — %.0f%%", stage, pct)
	}
	if stage == "" {
		return "No export yet."
	}
	if pct >= 100 {
		return "Finished: " + stage
	}
	return "Last export: " + stage
}

// panelRequirements — the Media Tools & Codecs surface (mirrors the
// media-tools dialog text builders).
func (sh *shell) panelRequirements(gtx layout.Context) layout.Dimensions {
	sh.ensureMediaToolsSnapshot()
	sh.ensureFFmpegDetection()
	return sh.scroll(gtx, &sh.reqList, func(gtx layout.Context, i int) layout.Dimensions {
		snap := sh.state.MediaTools()
		switch i {
		case 0:
			return sh.section(gtx, "Requirements — Media Tools & Codecs")
		case 1:
			return sh.line(gtx, mediaToolsStatusText(snap))
		case 2:
			return sh.caption(gtx, mediaToolsPackText(snap))
		case 3:
			if snap.State == mediatools.StateDownloading && snap.Total > 0 {
				bar := material.ProgressBar(sh.th.Theme(), float32(snap.Downloaded)/float32(snap.Total))
				return layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(4)}.Layout(gtx, bar.Layout)
			}
			return sh.caption(gtx, mediaToolsStageText(snap))
		case 4:
			return sh.buttonRow(gtx, []btnDef{
				{clk: &sh.mtInstallBtn, txt: "Download & Install", cmd: presentation.CmdMediaToolsInstall},
				{clk: &sh.mtCancelBtn, txt: "Cancel download", fn: func() { sh.mediaTools.Cancel() }},
			})
		case 5:
			return sh.buttonRow(gtx, []btnDef{
				{clk: &sh.mtUpdateBtn, txt: "Check for updates", fn: sh.checkMediaToolsUpdate},
				{clk: &sh.mtRepairBtn, txt: "Repair / Reinstall", cmd: presentation.CmdMediaToolsInstall},
			})
		case 6:
			return sh.buttonRow(gtx, []btnDef{
				{clk: &sh.mtFolderBtn, txt: "Reveal logs", cmd: presentation.CmdLogsReveal},
			})
		case 7:
			return sh.caption(gtx, mediaToolsPrivacyText)
		default:
			return layout.Dimensions{}
		}
	}, requirementsPanelRows)
}

// checkMediaToolsUpdate probes the pinned pack upstream on a goroutine.
func (sh *shell) checkMediaToolsUpdate() {
	mt, st := sh.mediaTools, sh.state
	go func() {
		if _, err := mt.CheckForUpdate(context.Background()); err != nil {
			st.SetStatus("Media tools update check failed: " + err.Error())
			return
		}
		st.SetStatus("Media tools update check complete")
	}()
}

// mediaToolsStatusText mirrors the media-tools dialog status builder.
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
	return b
}

// mediaToolsPackText mirrors the media-tools dialog pack builder.
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

// mediaToolsStageText mirrors the media-tools dialog stage builder.
func mediaToolsStageText(s mediatools.Snapshot) string {
	if s.Stage != "" {
		return "Stage: " + s.Stage
	}
	if s.State == mediatools.StateReady {
		return "Ready — H.264 and H.265 (libx265) exports verified for this pack."
	}
	return ""
}

// mediaToolsPrivacyText is the privacy/provenance note.
const mediaToolsPrivacyText = "Privacy and provenance: the tool pack is fetched from the pinned upstream provider, " +
	"only when you request it, over HTTPS, and verified against a pinned SHA-256 digest before installation. " +
	"The application package contains no FFmpeg binaries; the GPL applies to the optional tool pack only. " +
	"Playback and the visualizers keep working without the tools."

// panelSystemFiles — selected-scope path analysis with the two-step
// authorization contract (summary shown BEFORE authorization).
func (sh *shell) panelSystemFiles(gtx layout.Context) layout.Dimensions {
	return sh.scroll(gtx, &sh.sysList, func(gtx layout.Context, i int) layout.Dimensions {
		switch i {
		case 0:
			return sh.section(gtx, "System Files — selected-scope analysis")
		case 1:
			return sh.caption(gtx, "Read-only, depth/byte/time bounded, anchored at the selected root, sensitive locations skipped by default. Nothing is uploaded; every open is openat-contained.")
		case 2:
			return sh.editorRow(gtx, &sh.sysPathEd, "File or folder to analyze")
		case 3:
			return sh.buttonRow(gtx, []btnDef{
				{clk: &sh.sysAnalyzeBtn, txt: analyzeButtonLabel(sh.state.SysFilesStep()), cmd: presentation.CmdAnalyzePath},
			})
		case 4:
			if lines := sh.state.SysFilesSummary(); sh.state.SysFilesStep() == 1 && len(lines) > 0 {
				return sh.surfaceBox(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: unit.Dp(8), Right: unit.Dp(8), Bottom: unit.Dp(8), Left: unit.Dp(8)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return sh.line(gtx, "Authorization required — exact bounds:")
							}),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions { return sh.caption(gtx, strings.Join(lines, "\n")) }),
						)
					})
				})
			}
			if sh.state.SysFilesStep() == 1 {
				return sh.line(gtx, "Review the authorization summary above, then confirm.")
			}
			return layout.Dimensions{}
		case 5:
			text := sh.state.SysFilesText()
			if text == "" && !sh.state.SysFilesAnalyzing() {
				return sh.caption(gtx, "No analysis yet.")
			}
			if sh.state.SysFilesAnalyzing() {
				return sh.line(gtx, "Analyzing (bounded walk)…")
			}
			return sh.caption(gtx, text)
		default:
			return layout.Dimensions{}
		}
	}, sysFilesPanelRows)
}

// analyzeButtonLabel reflects the two-step authorization flow.
func analyzeButtonLabel(step int) string {
	if step == 1 {
		return "Authorize & analyze"
	}
	return "Prepare analysis"
}

// panelSettings — intensity preset radios, reduced-motion/glow
// switches and the renderer preference radios.
func (sh *shell) panelSettings(gtx layout.Context) layout.Dimensions {
	return sh.scroll(gtx, &sh.settingsList, func(gtx layout.Context, i int) layout.Dimensions {
		switch i {
		case 0:
			return sh.section(gtx, "Settings")
		case 1:
			return sh.radioRow(gtx, "Theme intensity", &sh.intensityEnum,
				[]string{"subtle", "ritual", "infernal"},
				[]string{"Subtle (decoration 5%, glow 15%)", "Ritual (decoration 10%, glow 35%, embers)", "Infernal (decoration 16%, glow 60%, embers)"})
		case 2:
			return sh.switchRow(gtx, &sh.reducedMotionSw, "Reduced motion — slows the idle preview cadence and suppresses decorative animation")
		case 3:
			return sh.switchRow(gtx, &sh.reducedGlowSw, "Reduced glow — zeroes the decorative glow chrome")
		case 4:
			return sh.section(gtx, "Render path")
		case 5:
			return sh.radioRow(gtx, "Renderer preference", &sh.rendererPrefEnum,
				[]string{"auto", "gpu", "cpu"},
				[]string{"Auto — prefer a proven hardware adapter", "GPU — Vulkan (falls back to CPU with the exact reason)", "CPU — always available"})
		case 6:
			return sh.caption(gtx, "The preference is persisted in the config directory and revalidated on every launch; a manual GPU choice that cannot resolve falls back to the CPU renderer with the exact reason. The running renderer stays active until shutdown — no mid-frame swaps.")
		case 7:
			return sh.caption(gtx, "Current renderer: "+sh.rendererText())
		default:
			return layout.Dimensions{}
		}
	}, settingsPanelRows)
}

// switchRow renders a switch with a description.
func (sh *shell) switchRow(gtx layout.Context, sw *widget.Bool, desc string) layout.Dimensions {
	return layout.Flex{Axis: layout.Horizontal, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return material.Switch(sh.th.Theme(), sw, desc).Layout(gtx)
		}),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			return sh.caption(gtx, desc)
		}),
	)
}

// panelDiagnostics — self-check style: build identity, data layout,
// log path, renderer evidence, jobs overview.
func (sh *shell) panelDiagnostics(gtx layout.Context) layout.Dimensions {
	sh.pollJobs()
	return sh.scroll(gtx, &sh.diagList, func(gtx layout.Context, i int) layout.Dimensions {
		switch i {
		case 0:
			return sh.section(gtx, "Diagnostics")
		case 1:
			return sh.line(gtx, sh.buildIdentityText())
		case 2:
			return sh.line(gtx, sh.dataLayoutText())
		case 3:
			return sh.caption(gtx, "Log path: "+sh.logPathText())
		case 4:
			return sh.caption(gtx, "Renderer: "+sh.rendererText())
		case 5:
			return sh.caption(gtx, "Tab order: "+tabOrderText(sh.tabs.Layout.TabOrder)+
				"\nRecovered from corrupt layout: "+boolText(sh.tabs.Recovered))
		case 6:
			return sh.section(gtx, "Job center (scheduler snapshot)")
		case 7:
			return sh.caption(gtx, jobsOverviewText(sh.state))
		case 8:
			return sh.section(gtx, "Actions")
		case 9:
			return sh.buttonRow(gtx, []btnDef{
				{clk: &sh.copyDiagBtn, txt: "Copy diagnostics summary", cmd: presentation.CmdCopyToClipboard},
				{clk: &sh.logsRevealBtn, txt: "Reveal logs", cmd: presentation.CmdLogsReveal},
				{clk: &sh.layoutResetBtn, txt: "Reset workspace layout", cmd: presentation.CmdLayoutReset},
			})
		case 10:
			return sh.caption(gtx, "All inspection is local-only: no telemetry, no upload, no serial numbers or user identifiers.")
		default:
			return layout.Dimensions{}
		}
	}, diagnosticsPanelRows)
}

// jobsOverviewText renders the scheduler snapshot for diagnostics.
func jobsOverviewText(st *uiState) string {
	overview, ok := st.Jobs()
	if !ok {
		return "No scheduler snapshot yet."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "CPU budget %d · tokens used %d · aside %d · goroutines %d\n",
		overview.Metrics.CPUBudget, overview.Metrics.CPUTokensUsed, overview.Metrics.TokensAside, overview.Metrics.Goroutines)
	fmt.Fprintf(&b, "Disk: reserved %d MiB / budget %d MiB\n", overview.Metrics.DiskReservedMB, overview.Metrics.DiskBudgetMB)
	if len(overview.Live) == 0 {
		b.WriteString("Live jobs: none")
	} else {
		fmt.Fprintf(&b, "Live jobs: %d", len(overview.Live))
		for _, j := range overview.Live {
			fmt.Fprintf(&b, "\n  #%d %s lane=%s state=%s stage=%s %d/%d",
				j.ID, j.Type, j.Lane, j.State, j.Stage, j.ItemsDone, j.ItemsTotal)
		}
	}
	return b.String()
}

// panelUnavailable renders the honest empty state of a destination the
// catalog marks unavailable — never fake content.
func (sh *shell) panelUnavailable(gtx layout.Context, d presentation.Destination) layout.Dimensions {
	info, ok := InfoFor(d)
	if !ok {
		return sh.line(gtx, fmt.Sprintf("%s is not implemented in this build.", d))
	}
	return sh.scroll(gtx, &sh.emptyList, func(gtx layout.Context, i int) layout.Dimensions {
		switch i {
		case 0:
			return sh.section(gtx, info.Title)
		case 1:
			return sh.line(gtx, info.EmptyState)
		case 2:
			return sh.caption(gtx, info.Description)
		case 3:
			return sh.caption(gtx, "This surface ships as an honest empty state in this build; no placeholder content is fabricated.")
		default:
			return layout.Dimensions{}
		}
	}, emptyPanelRows)
}

// buildIdentityText renders the build identity block.
func (sh *shell) buildIdentityText() string {
	bi := buildinfo.Normalized()
	return fmt.Sprintf("Build: %s · commit %s · date %s · built by %s · %s",
		bi.Version, bi.Commit, bi.Date, bi.BuiltBy, bi.GoVersion)
}

// dataLayoutText renders the data mode/root block.
func (sh *shell) dataLayoutText() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Data mode: %s — root %s", sh.lay.Mode, sh.lay.DataRoot)
	if sh.lay.Reason != "" {
		fmt.Fprintf(&b, " (%s)", sh.lay.Reason)
	}
	fmt.Fprintf(&b, "\nconfig=%s logs=%s projects=%s exports=%s",
		sh.lay.Config, sh.lay.Logs, sh.lay.Projects, sh.lay.Exports)
	return b.String()
}

// logPathText renders the runtime log path (read-only info).
func (sh *shell) logPathText() string {
	return filepath.Join(sh.lay.Logs, logFileName)
}

// rendererText renders the renderer evidence line.
func (sh *shell) rendererText() string {
	info := sh.state.Renderer()
	return fmt.Sprintf("backend=%s gpuState=%s api=%s adapter=%s deviceType=%s class=%s%s",
		info.Backend, info.GpuState, info.BackendAPI, info.Adapter, info.DeviceType, info.DeviceClass, detailSuffix(info.Detail))
}

func detailSuffix(detail string) string {
	if detail == "" {
		return ""
	}
	return " — " + detail
}

func boolText(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

// tabOrderText renders the persisted tab order as an ID sequence.
func tabOrderText(order []presentation.Destination) string {
	parts := make([]string, len(order))
	for i, d := range order {
		parts[i] = string(d)
	}
	return strings.Join(parts, " → ")
}

// transportLine renders the transport readout used by the status strip
// and the Home overview (media time only — never the wall clock).
func transportLine(c clockInfo) string {
	return fmt.Sprintf("%s · %s / %s", c.State.String(), formatDuration(c.Position), formatDuration(c.Total))
}

// transportButtonLabel chooses the play/pause label from the clock.
func transportButtonLabel(c clockInfo) string {
	if c.State == statePlaying {
		return "Pause"
	}
	return "Play"
}

// panel row counts.
const (
	homePanelRows         = 10
	libraryPanelRows      = 8
	exportPanelRows       = 13
	requirementsPanelRows = 8
	sysFilesPanelRows     = 6
	settingsPanelRows     = 8
	diagnosticsPanelRows  = 11
	emptyPanelRows        = 4
	logFileName           = "app.log"
)
