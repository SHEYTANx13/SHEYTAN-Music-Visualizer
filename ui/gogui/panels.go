// panels.go — the per-destination workspace panels of the Go-Gui shell
// and the shared view helpers. Every destination is a scrollable
// surface inside the ONE window; the panels read only from the state
// snapshot (never blocking) and push user actions through the command
// dispatcher. All widget callbacks run on the go-gui frame thread and
// consume the events they handle.

package gogui

import (
	"context"
	"fmt"
	"image/color"
	"path/filepath"
	"strings"

	"github.com/go-gui-org/go-gui/gui"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/audio"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/brand"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/buildinfo"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/export"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/mediatools"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/presentation"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/visualizer"
)

// tabStripView builds the workspace tab strip: the 11 destinations in
// the persisted order, one button each. Extracted as a standalone
// function (not a shell method) so the test window can render it
// without the service stack; the shell passes its own activation
// callback. The strip wraps at narrow widths (compact navigation at
// phone breakpoints) instead of pushing controls off-window.
//
// Reorder (run 44648357437 item 4.3): the ‹/› controls move the ACTIVE
// tab through the persisted order — full-size theme buttons (touch-safe
// targets) with explicit A11Y labels; the mod+alt+left/right chords are
// the keyboard alternative. Both share the MoveActive state mutation.
// tabStripPadding resolves the tab strip's responsive padding from
// the token-driven spacing profile (roomier on touch classes).
func tabStripPadding(th *ShellTheme) gui.Padding {
	sp := th.Spacing()
	return gui.NewPadding(sp.RowPadV, sp.BarPadH, sp.RowPadV, sp.BarPadH)
}

// panelPadding resolves the scroll panel and surface box padding
// (professional density on desktop/wide, roomy on touch classes).
func (sh *shell) panelPadding() gui.Padding {
	sp := sh.th.Spacing()
	return gui.NewPadding(sp.PanelPadV, sp.PanelPadH, sp.PanelPadV, sp.PanelPadH)
}

// rowPadding resolves the vertical padding of compact control rows.
func (sh *shell) rowPadding() gui.Padding {
	sp := sh.th.Spacing()
	return gui.NewPadding(sp.RowPadV, 0, sp.RowPadV, 0)
}

func tabStripView(tabs *TabState, th *ShellTheme, onTab func(presentation.Destination), onMove func(int)) gui.View {
	catalog := tabs.OrderedCatalog()
	controls := make([]gui.View, 0, len(catalog)+2)
	for _, info := range catalog {
		info := info
		dest := info.ID
		active := tabs.Layout.ActiveTab == dest
		controls = append(controls, gui.Button(gui.ButtonCfg{
			ID:        "tab-" + string(dest),
			Label:     info.Title,
			MinHeight: th.TouchTargetDp(),
			Colors: func() gui.ColorSet {
				if active {
					return gui.ColorSet{Base: th.AccentColor()}
				}
				return gui.ColorSet{Base: th.VolcanicColor()}
			}(),
			A11YCfg: gui.A11YCfg{
				A11YLabel: info.Title,
				A11YDescription: func() string {
					if active {
						return "active destination"
					}
					return ""
				}(),
			},
			OnClick: func(ctx gui.EventCtx) {
				ctx.Consume()
				onTab(dest)
			},
		}))
	}
	controls = append(controls,
		gui.Button(gui.ButtonCfg{
			ID:        "tab-move-left",
			Label:     "‹",
			MinHeight: th.TouchTargetDp(),
			MinWidth:  th.TouchTargetDp(),
			A11YCfg: gui.A11YCfg{
				A11YLabel:       "Move active tab left",
				A11YDescription: "Reorders the active destination one position toward the strip start",
			},
			OnClick: func(ctx gui.EventCtx) {
				ctx.Consume()
				onMove(-1)
			},
		}),
		gui.Button(gui.ButtonCfg{
			ID:        "tab-move-right",
			Label:     "›",
			MinHeight: th.TouchTargetDp(),
			MinWidth:  th.TouchTargetDp(),
			A11YCfg: gui.A11YCfg{
				A11YLabel:       "Move active tab right",
				A11YDescription: "Reorders the active destination one position toward the strip end",
			},
			OnClick: func(ctx gui.EventCtx) {
				ctx.Consume()
				onMove(1)
			},
		}))
	return gui.Wrap(gui.ContainerCfg{
		ID:          "tab-strip",
		Sizing:      gui.FillFit,
		Padding:     tabStripPadding(th),
		Spacing:     gui.SpacingSmall,
		Color:       th.SurfaceColor(),
		SizeBorder:  gui.BorderThin,
		ColorBorder: th.DecorationBorderColor(),
		Content:     controls,
	})
}

// destinationPanel routes the active destination to its panel. The
// panel fills the remaining window height; its content scrolls.
func (sh *shell) destinationPanel() gui.View {
	switch sh.tabs.Layout.ActiveTab {
	case presentation.DestHome:
		return sh.scrollPanel("panel-home", sh.panelHome())
	case presentation.DestStudio:
		return sh.panelStudio()
	case presentation.DestWorlds:
		return sh.scrollPanel("panel-worlds", sh.panelUnavailable(presentation.DestWorlds))
	case presentation.DestTemplates:
		return sh.scrollPanel("panel-templates", sh.panelTemplates())
	case presentation.DestLibrary:
		return sh.scrollPanel("panel-library", sh.panelLibrary())
	case presentation.DestExport:
		return sh.scrollPanel("panel-export", sh.panelExport())
	case presentation.DestRequirements:
		return sh.scrollPanel("panel-requirements", sh.panelRequirements())
	case presentation.DestDownloads:
		return sh.scrollPanel("panel-downloads", sh.panelUnavailable(presentation.DestDownloads))
	case presentation.DestSystemFiles:
		return sh.scrollPanel("panel-system-files", sh.panelSystemFiles())
	case presentation.DestSettings:
		return sh.scrollPanel("panel-settings", sh.panelSettings())
	case presentation.DestDiagnostics:
		return sh.scrollPanel("panel-diagnostics", sh.panelDiagnostics())
	default:
		return sh.scrollPanel("panel-unknown", sh.panelUnavailable(sh.tabs.Layout.ActiveTab))
	}
}

// scrollPanel wraps panel content in the vertical scroll surface. The
// container needs a non-empty ID (the scroll state key) and clips and
// scrolls vertically only — the workspace scrolls; nothing can push
// content off-window.
func (sh *shell) scrollPanel(id string, content []gui.View) gui.View {
	return gui.Column(gui.ContainerCfg{
		ID:         id,
		Sizing:     gui.FillFill,
		Padding:    sh.panelPadding(),
		Spacing:    gui.SpacingSmall,
		Scrollable: true,
		ScrollMode: gui.ScrollVerticalOnly,
		Content:    content,
	})
}

// section renders a heading over the following rows.
func (sh *shell) section(title string) gui.View {
	return gui.Text(gui.TextCfg{
		Text:      title,
		TextStyle: sh.th.TextStyleSection(),
	})
}

// line renders one body text row.
func (sh *shell) line(s string) gui.View {
	return gui.Text(gui.TextCfg{
		Text:      s,
		TextStyle: sh.th.TextStyleBody(),
		Mode:      gui.TextModeWrap,
	})
}

// caption renders one dim caption row.
func (sh *shell) caption(s string) gui.View {
	return gui.Text(gui.TextCfg{
		Text:      s,
		TextStyle: sh.th.TextStyleCaption(),
		Mode:      gui.TextModeWrap,
	})
}

// statusLine renders one caption row in an explicit status color.
func (sh *shell) statusLine(s string, kind string) gui.View {
	return gui.Text(gui.TextCfg{
		Text:      s,
		TextStyle: sh.th.TextStyleStatus(sh.statusColor(kind)),
		Mode:      gui.TextModeWrap,
	})
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

// surfaceBox paints the panel content area with the deep charcoal
// surface plus the decoration accent edge derived from the intensity
// preset (bounded at 0.16 opacity by the token contract).
func (sh *shell) surfaceBox(id string, content []gui.View) gui.View {
	return gui.Column(gui.ContainerCfg{
		ID:          id,
		Sizing:      gui.FillFit,
		Padding:     sh.panelPadding(),
		Spacing:     gui.SpacingSmall,
		Color:       sh.th.SurfaceColor(),
		SizeBorder:  gui.BorderThin,
		ColorBorder: sh.th.DecorationBorderColor(),
		Content:     content,
	})
}

// fieldColumn renders a caption label over its control rows.
func (sh *shell) fieldColumn(label string, controls ...gui.View) gui.View {
	content := make([]gui.View, 0, len(controls)+1)
	content = append(content, sh.caption(label))
	content = append(content, controls...)
	return gui.Column(gui.ContainerCfg{
		Sizing:  gui.FillFit,
		Padding: sh.rowPadding(),
		Spacing: gui.SpacingTight,
		Content: content,
	})
}

// inputRow renders a labeled single-line path/value entry.
func (sh *shell) inputRow(id, label, value, placeholder string, onChange func(string, gui.EventCtx)) gui.View {
	return gui.Input(gui.InputCfg{
		ID:          id,
		Label:       label,
		Text:        value,
		Placeholder: placeholder,
		Width:       420,
		A11YCfg:     gui.A11YCfg{A11YLabel: label},
		OnTextChanged: func(s string, ctx gui.EventCtx) {
			ctx.Consume()
			onChange(s, ctx)
		},
		OnTextCommit: func(s string, _ gui.InputCommitReason, ctx gui.EventCtx) {
			ctx.Consume()
			onChange(s, ctx)
		},
	})
}

// btnDef is one command bar / panel button definition. Either cmd or
// fn must be set: cmd dispatches a presentation command, fn runs a
// direct shell action (used for the few actions without a command ID,
// such as the media-tools download cancel).
type btnDef struct {
	id    string
	label string
	cmd   presentation.CommandID
	fn    func()
}

// buttonRow renders buttons; clicking dispatches the command or runs
// the direct action. Every button consumes its click.
func (sh *shell) buttonRow(defs ...btnDef) gui.View {
	buttons := make([]gui.View, 0, len(defs))
	for _, d := range defs {
		d := d
		buttons = append(buttons, gui.Button(gui.ButtonCfg{
			ID:        d.id,
			Label:     d.label,
			MinHeight: sh.th.TouchTargetDp(),
			OnClick: func(ctx gui.EventCtx) {
				ctx.Consume()
				if d.fn != nil {
					d.fn()
					return
				}
				sh.disp.Run(d.cmd)
			},
		}))
	}
	return gui.Row(gui.ContainerCfg{
		Sizing:  gui.FillFit,
		Padding: gui.NewPadding(4, 0, 4, 0),
		Spacing: gui.SpacingSmall,
		Content: buttons,
	})
}

// progressBar renders the honest percent readout bar.
func (sh *shell) progressBar(id string, pct float64) gui.View {
	p := float32(pct) / 100
	if p < 0 {
		p = 0
	}
	if p > 1 {
		p = 1
	}
	return gui.ProgressBar(gui.ProgressBarCfg{
		ID:       id,
		Percent:  p,
		TextShow: true,
		Sizing:   gui.FillFit,
		A11YCfg:  gui.A11YCfg{A11YLabel: fmt.Sprintf("%s progress", id)},
	})
}

// radioOpt is one radio option.
type radioOpt struct {
	value string
	label string
}

// radioRow renders a labeled radio group row (values are the stable
// keys; labels are what the user reads).
func (sh *shell) radioRow(field, label, current string, opts []radioOpt, onPick func(string, gui.EventCtx)) gui.View {
	controls := make([]gui.View, 0, len(opts))
	for _, o := range opts {
		o := o
		controls = append(controls, gui.Radio(gui.RadioCfg{
			ID:       field + "-" + o.value,
			Label:    o.label,
			Selected: current == o.value,
			OnClick: func(ctx gui.EventCtx) {
				ctx.Consume()
				onPick(o.value, ctx)
			},
		}))
	}
	return sh.fieldColumn(label, gui.Row(gui.ContainerCfg{
		Sizing:  gui.FillFit,
		Padding: gui.PaddingNone,
		Spacing: gui.SpacingMedium,
		Content: controls,
	}))
}

// switchRow renders a switch with a description.
func (sh *shell) switchRow(id, label string, selected bool, desc string, onFlip func(bool, gui.EventCtx)) gui.View {
	return gui.Row(gui.ContainerCfg{
		Sizing:  gui.FillFit,
		Padding: sh.rowPadding(),
		Spacing: gui.SpacingMedium,
		VAlign:  gui.VAlignMiddle,
		Content: []gui.View{
			gui.Switch(gui.SwitchCfg{
				ID:       id,
				Label:    label,
				Selected: selected,
				OnClick: func(ctx gui.EventCtx) {
					ctx.Consume()
					onFlip(!selected, ctx)
				},
			}),
			gui.Column(gui.ContainerCfg{
				Sizing: gui.FillFit,
				Content: []gui.View{
					sh.caption(desc),
				},
			}),
		},
	})
}

// selectRow renders a labeled dropdown with stable option values.
func (sh *shell) selectRow(id, label string, opts []radioOpt, current string, onPick func(string, gui.EventCtx)) gui.View {
	options := make([]gui.SelectOption, 0, len(opts))
	for _, o := range opts {
		options = append(options, gui.NewSelectOption(o.label, o.value))
	}
	return sh.fieldColumn(label, gui.Select(gui.SelectCfg{
		ID:       id,
		Options:  options,
		Selected: []string{current},
		OnSelect: func(vals []string, ctx gui.EventCtx) {
			ctx.Consume()
			if len(vals) == 0 {
				return
			}
			onPick(vals[0], ctx)
		},
	}))
}

// panelHome — overview: build identity, renderer evidence, data mode
// and quick actions.
func (sh *shell) panelHome() []gui.View {
	return []gui.View{
		sh.section("Home — " + brand.ProductName),
		sh.surfaceBox("home-identity", []gui.View{
			sh.line(sh.buildIdentityText()),
			sh.caption(sh.dataLayoutText()),
			sh.caption(sh.rendererText()),
		}),
		sh.section("Session"),
		sh.line("Status: " + sh.state.Status()),
		sh.line("Transport: " + transportLine(sh.state.Clock())),
		sh.section("Quick actions"),
		sh.buttonRow(
			btnDef{id: "home-quick-import", label: "Import audio…", cmd: presentation.CmdImportAudio},
			btnDef{id: "home-quick-play", label: transportButtonLabel(sh.state.Clock()), cmd: presentation.CmdPlayPause},
			btnDef{id: "home-quick-export", label: "Export…", cmd: presentation.CmdExportStart},
			btnDef{id: "home-quick-diagnostics", label: "Diagnostics", fn: func() {
				sh.gotoDestination(presentation.DestDiagnostics)
			}},
		),
		sh.caption("Vertical-slice note: import/open/save use the native file dialog (with the in-window browser as the platform fallback) plus path entry."),
		sh.section("Attribution"),
		sh.caption(strings.Join(sh.attrLines, "\n")),
	}
}

// gotoDestination switches the active tab programmatically.
func (sh *shell) gotoDestination(d presentation.Destination) {
	sh.tabs.SetActive(d)
	sh.syncTabs()
}

// panelTemplates — the built-in visualizer presets; click applies.
func (sh *shell) panelTemplates() []gui.View {
	out := []gui.View{sh.section("Templates — built-in presets")}
	active := sh.state.ActivePreset()
	for _, p := range sh.presets {
		p := p
		name := p.Name
		isActive := active == name
		out = append(out, sh.surfaceBox("tpl-"+name, []gui.View{
			sh.line(name),
			sh.caption(presetSummary(p.Config)),
			gui.Button(gui.ButtonCfg{
				ID:        "tpl-apply-" + name,
				Label:     applyButtonLabel(name, isActive),
				MinHeight: sh.th.TouchTargetDp(),
				Colors: func() gui.ColorSet {
					if !isActive {
						return gui.ColorSet{Base: sh.th.VolcanicColor()}
					}
					return gui.ColorSet{}
				}(),
				OnClick: func(ctx gui.EventCtx) {
					ctx.Consume()
					sh.selectedPreset = name
					sh.disp.Run(presentation.CmdPresetApply)
				},
			}),
		}))
	}
	return out
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
// open-project entries.
func (sh *shell) panelLibrary() []gui.View {
	return []gui.View{
		sh.section("Library — imported songs and project files"),
		sh.line(formatTrackInfo(sh.state.Track())),
		sh.section("Supported input formats"),
		sh.caption(libraryFormatsText(sh.formats)),
		sh.section("Open project"),
		sh.inputRow("library-project-path", "Project path", sh.projectPath,
			"path/to/project.sheytan.json", func(s string, _ gui.EventCtx) { sh.projectPath = s }),
		sh.buttonRow(
			btnDef{id: "library-open-native", label: "Choose project file…", fn: sh.chooseProjectPath},
			btnDef{id: "library-open-path", label: "Open from path", cmd: presentation.CmdProjectOpen},
		),
		sh.caption("The native picker falls back to the in-window file browser where the platform has no native dialog; the path entry stays available as the manual fallback. The same entries serve Save (command bar) and Open project."),
	}
}

// libraryFormatsText renders the supported-format list.
func libraryFormatsText(formats []audio.SourceFormat) string {
	var names []string
	for _, f := range formats {
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
	return strings.Join(names, "\n")
}

// panelExport — resolution/FPS/seconds/codec fields, start, progress,
// cancel and the honest FFmpeg availability line.
func (sh *shell) panelExport() []gui.View {
	sh.ensureFFmpegDetection()
	running, stage, pct := sh.state.ExportSnapshot()
	resOpts := make([]radioOpt, 0, len(export.Resolutions))
	for _, r := range export.Resolutions {
		resOpts = append(resOpts, radioOpt{
			value: r.ID,
			label: fmt.Sprintf("%s (%dx%d)", r.Name, r.Width, r.Height),
		})
	}
	fpsOpts := make([]radioOpt, 0, len(export.FrameRates))
	for _, f := range export.FrameRates {
		fpsOpts = append(fpsOpts, radioOpt{
			value: fmt.Sprintf("%d", f),
			label: fmt.Sprintf("%d fps", f),
		})
	}
	return []gui.View{
		sh.section("Export — deterministic offline MP4"),
		sh.caption(sh.state.FFmpegText()),
		sh.section("Settings"),
		sh.selectRow("export-resolution", "Resolution", resOpts, sh.resValue, func(v string, _ gui.EventCtx) {
			sh.resValue = v
		}),
		sh.selectRow("export-fps", "Frame rate", fpsOpts, sh.fpsValue, func(v string, _ gui.EventCtx) {
			sh.fpsValue = v
		}),
		sh.selectRow("export-codec", "Video codec", []radioOpt{
			{value: "h264", label: "H.264 (AVC) — default"},
			{value: "hevc", label: "H.265 (HEVC) — when the local FFmpeg provides libx265"},
		}, sh.codecValue, func(v string, _ gui.EventCtx) { sh.codecValue = v }),
		sh.inputRow("export-seconds", "Seconds", sh.seconds, "e.g. 30",
			func(s string, _ gui.EventCtx) { sh.seconds = s }),
		sh.inputRow("export-output-path", "Output .mp4 path (absolute or relative)", sh.outPath,
			"Output .mp4 path (absolute or relative)", func(s string, _ gui.EventCtx) { sh.outPath = s }),
		sh.buttonRow(
			btnDef{id: "export-choose-output", label: "Choose output file…", fn: sh.chooseExportPath},
			btnDef{id: "export-start", label: "Start export", cmd: presentation.CmdExportStart},
			btnDef{id: "export-cancel", label: "Cancel export", cmd: presentation.CmdExportCancel},
		),
		sh.section("Progress"),
		sh.progressBar("export-progress", pct),
		sh.line(exportProgressLine(running, stage, pct)),
		sh.caption("Exports need the local FFmpeg encoder adapter; playback and the visualizers work without it. The requirement is surfaced in Requirements and in Diagnostics."),
	}
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
func (sh *shell) panelRequirements() []gui.View {
	sh.ensureMediaToolsSnapshot()
	sh.ensureFFmpegDetection()
	snap := sh.state.MediaTools()
	views := []gui.View{
		sh.section("Requirements — Media Tools & Codecs"),
		sh.line(mediaToolsStatusText(snap)),
		sh.caption(mediaToolsPackText(snap)),
	}
	if snap.State == mediatools.StateDownloading && snap.Total > 0 {
		views = append(views, sh.progressBar("mt-download",
			float64(snap.Downloaded)/float64(snap.Total)*100))
	} else {
		views = append(views, sh.caption(mediaToolsStageText(snap)))
	}
	views = append(views,
		sh.buttonRow(
			btnDef{id: "mt-install", label: "Download & Install", cmd: presentation.CmdMediaToolsInstall},
			btnDef{id: "mt-cancel", label: "Cancel download", fn: func() { sh.mediaTools.Cancel() }},
		),
		sh.buttonRow(
			btnDef{id: "mt-update", label: "Check for updates", fn: sh.checkMediaToolsUpdate},
			btnDef{id: "mt-repair", label: "Repair / Reinstall", cmd: presentation.CmdMediaToolsInstall},
		),
		sh.buttonRow(
			btnDef{id: "mt-reveal-logs", label: "Reveal logs", cmd: presentation.CmdLogsReveal},
		),
		sh.caption(mediaToolsPrivacyText),
	)
	return views
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
func (sh *shell) panelSystemFiles() []gui.View {
	views := []gui.View{
		sh.section("System Files — selected-scope analysis"),
		sh.caption("Read-only, depth/byte/time bounded, anchored at the selected root, sensitive locations skipped by default. Nothing is uploaded; every open is openat-contained."),
		sh.inputRow("sys-path", "File or folder to analyze", sh.sysPath,
			"File or folder to analyze", func(s string, _ gui.EventCtx) { sh.sysPath = s }),
		sh.buttonRow(btnDef{
			id:    "sys-analyze",
			label: analyzeButtonLabel(sh.state.SysFilesStep()),
			cmd:   presentation.CmdAnalyzePath,
		}),
	}
	if lines := sh.state.SysFilesSummary(); sh.state.SysFilesStep() == 1 && len(lines) > 0 {
		views = append(views, sh.surfaceBox("sys-authorization", []gui.View{
			sh.line("Authorization required — exact bounds:"),
			sh.caption(strings.Join(lines, "\n")),
		}))
	} else if sh.state.SysFilesStep() == 1 {
		views = append(views, sh.line("Review the authorization summary above, then confirm."))
	}
	text := sh.state.SysFilesText()
	switch {
	case text == "" && !sh.state.SysFilesAnalyzing():
		views = append(views, sh.caption("No analysis yet."))
	case sh.state.SysFilesAnalyzing():
		views = append(views, sh.line("Analyzing (bounded walk)…"))
	default:
		views = append(views, sh.caption(text))
	}
	return views
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
func (sh *shell) panelSettings() []gui.View {
	return []gui.View{
		sh.section("Settings"),
		sh.radioRow("settings-intensity", "Theme intensity", sh.intensityValue,
			[]radioOpt{
				{value: "subtle", label: "Subtle (decoration 5%, glow 15%)"},
				{value: "ritual", label: "Ritual (decoration 10%, glow 35%, embers)"},
				{value: "infernal", label: "Infernal (decoration 16%, glow 60%, embers)"},
			},
			func(v string, _ gui.EventCtx) {
				sh.intensityValue = v
				sh.disp.Run(presentation.CmdIntensitySet)
			}),
		sh.switchRow("settings-reduced-motion", "Reduced motion", sh.reducedMotion,
			"Reduced motion — slows the idle preview cadence and suppresses decorative animation",
			func(v bool, _ gui.EventCtx) {
				sh.reducedMotion = v
				sh.disp.Run(presentation.CmdReducedMotionSet)
			}),
		sh.switchRow("settings-reduced-glow", "Reduced glow", sh.reducedGlow,
			"Reduced glow — zeroes the decorative glow chrome",
			func(v bool, _ gui.EventCtx) {
				sh.reducedGlow = v
				sh.disp.Run(presentation.CmdReducedGlowSet)
			}),
		sh.section("Render path"),
		sh.radioRow("settings-renderer", "Renderer preference", sh.prefValue,
			[]radioOpt{
				{value: "auto", label: "Auto — prefer a proven hardware adapter"},
				{value: "gpu", label: "GPU — Vulkan (falls back to CPU with the exact reason)"},
				{value: "cpu", label: "CPU — always available"},
			},
			func(v string, _ gui.EventCtx) {
				sh.prefValue = v
				sh.disp.Run(presentation.CmdRendererSelect)
			}),
		sh.caption("The preference is persisted in the config directory and revalidated on every launch; a manual GPU choice that cannot resolve falls back to the CPU renderer with the exact reason. The running renderer stays active until shutdown — no mid-frame swaps."),
		sh.caption("Current renderer: " + sh.rendererText()),
	}
}

// panelDiagnostics — self-check style: build identity, data layout,
// log path, renderer evidence, jobs overview.
func (sh *shell) panelDiagnostics() []gui.View {
	sh.pollJobs()
	return []gui.View{
		sh.section("Diagnostics"),
		sh.line(sh.buildIdentityText()),
		sh.line(sh.dataLayoutText()),
		sh.caption("Log path: " + sh.logPathText()),
		sh.caption("Renderer: " + sh.rendererText()),
		sh.caption("Tab order: " + tabOrderText(sh.tabs.Layout.TabOrder) +
			"\nRecovered from corrupt layout: " + boolText(sh.tabs.Recovered)),
		sh.section("Job center (scheduler snapshot)"),
		sh.caption(jobsOverviewText(sh.state)),
		sh.section("Actions"),
		sh.buttonRow(
			btnDef{id: "diag-copy", label: "Copy diagnostics summary", cmd: presentation.CmdCopyToClipboard},
			btnDef{id: "diag-reveal-logs", label: "Reveal logs", cmd: presentation.CmdLogsReveal},
			btnDef{id: "diag-layout-reset", label: "Reset workspace layout", cmd: presentation.CmdLayoutReset},
		),
		sh.caption("All inspection is local-only: no telemetry, no upload, no serial numbers or user identifiers."),
	}
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
func (sh *shell) panelUnavailable(d presentation.Destination) []gui.View {
	info, ok := InfoFor(d)
	if !ok {
		return []gui.View{sh.line(fmt.Sprintf("%s is not implemented in this build.", d))}
	}
	return []gui.View{
		sh.section(info.Title),
		sh.statusLine(info.EmptyState, "warn"),
		sh.caption(info.Description),
		sh.caption("This surface ships as an honest empty state in this build; no placeholder content is fabricated."),
	}
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

// logFileName is the runtime log file name (diagnostics surface).
const logFileName = "app.log"
