// studio_panel.go — the Studio destination: library, live preview
// canvas (the PixelSurface frame bridge), inspector controls,
// transport readout and the bounded play-event tail.
//
// Split composition (desktop/wide breakpoints): the library/inspector
// render as a side rail next to the main preview pane. Stacked
// composition (phone/tablet): one vertical scroll. The composition is
// decided per frame from the window width (layout.go breakpoints) —
// the control values are shell-owned, so user state survives
// breakpoint crossings.

package gogui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/go-gui-org/go-gui/gui"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/presentation"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/visualizer"
)

// Studio geometry (logical px; theme padding accounts for the rest).
const (
	studioRailWidthDp  = 360
	previewMinHeightDp = 260
	sliderMinWidthDp   = 220
	previewPaneInsets  = 32
)

// backgroundIntensityMax mirrors visualizer.MaxBackgroundIntensity for
// the slider mapping.
const backgroundIntensityMax = visualizer.MaxBackgroundIntensity

// panelStudio renders the Studio destination for the active breakpoint.
// The split composition honors the PERSISTED SplitVertical override
// (workspace.split.orientation — Row horizontal ⇄ Column vertical) and
// the persisted per-destination collapse state: a collapsed rail
// renders as a slim restore control instead of disappearing the
// feature (run 44648357437 item 4.3).
func (sh *shell) panelStudio() gui.View {
	if isSplitFor(sh.widthDp) {
		content := []gui.View{sh.studioMainPane()}
		if sh.tabs.SideCollapsed() {
			content = append(content, sh.collapsedRailStub())
		} else {
			content = append(content, sh.studioRail())
		}
		axis := gui.Row
		if sh.tabs.Layout.SplitVertical {
			axis = gui.Column
		}
		return axis(gui.ContainerCfg{
			ID:      "panel-studio",
			Sizing:  gui.FillFill,
			Padding: gui.PaddingNone,
			Spacing: gui.NoSpacing,
			Content: content,
		})
	}
	return sh.scrollPanel("panel-studio", sh.studioStackedContent())
}

// collapsedRailStub — the slim restore control shown while the side
// rail is collapsed. A collapsed panel must never orphan the feature:
// the restore affordance stays visible at the collapse site.
func (sh *shell) collapsedRailStub() gui.View {
	return gui.Column(gui.ContainerCfg{
		ID:      "studio-rail-collapsed",
		Sizing:  gui.FixedFit,
		Padding: gui.NewPadding(4, 8, 4, 8),
		Content: []gui.View{
			gui.TextButton("Restore panel", func(ctx gui.EventCtx) {
				ctx.Consume()
				sh.disp.Run(presentation.CmdPanelRestore)
			}),
		},
	})
}

// studioStackedContent — single-column composition (phone/tablet).
func (sh *shell) studioStackedContent() []gui.View {
	return append(sh.studioLibraryContent(), sh.studioMainContent()...)
}

// studioRail — library + inspector side rail (split composition).
func (sh *shell) studioRail() gui.View {
	content := append(sh.studioLibraryContent(), sh.studioInspectorContent()...)
	return gui.Column(gui.ContainerCfg{
		ID:         "studio-rail",
		Sizing:     gui.FixedFill,
		Width:      studioRailWidthDp,
		Padding:    gui.PaddingMedium,
		Spacing:    gui.SpacingSmall,
		Scrollable: true,
		ScrollMode: gui.ScrollVerticalOnly,
		Color:      sh.th.SurfaceColor(),
		Content:    content,
	})
}

// studioMainPane — preview + transport + play events (split
// composition main area).
func (sh *shell) studioMainPane() gui.View {
	return gui.Column(gui.ContainerCfg{
		ID:         "studio-main",
		Sizing:     gui.FillFill,
		Padding:    gui.PaddingMedium,
		Spacing:    gui.SpacingSmall,
		Scrollable: true,
		ScrollMode: gui.ScrollVerticalOnly,
		Content:    sh.studioMainContent(),
	})
}

// studioLibraryContent — the library block (track info, import entry,
// actions, progress, recent paths).
func (sh *shell) studioLibraryContent() []gui.View {
	progress, importing := sh.state.ImportSnapshot()
	content := []gui.View{
		sh.section("Library"),
		sh.line(formatTrackInfo(sh.state.Track())),
		sh.inputRow("studio-import-path", "Audio file path", sh.importPath,
			"Audio file path (wav/mp3/flac/ogg…)", func(s string, _ gui.EventCtx) { sh.importPath = s }),
		sh.buttonRow(
			btnDef{id: "studio-import-go", label: "Import", cmd: presentation.CmdImportAudio},
			btnDef{id: "studio-import-cancel", label: "Cancel import", fn: sh.cancelImport},
		),
	}
	if importing {
		content = append(content, sh.progressBar("studio-import-progress", progress))
	} else if len(sh.recentPaths) > 0 {
		list := make([]string, 0, len(sh.recentPaths)+1)
		list = append(list, "Recent import paths:")
		list = append(list, sh.recentPaths...)
		content = append(content, sh.caption(strings.Join(list, "\n")))
	}
	return content
}

// studioMainContent — preview + transport + play events.
func (sh *shell) studioMainContent() []gui.View {
	return []gui.View{
		sh.section("Preview"),
		sh.previewPane(),
		sh.line("Transport: " + transportLine(sh.state.Clock())),
		sh.section("Play events"),
		sh.caption(sh.playEventTailText()),
	}
}

// studioInspectorContent — mode, palette, sensitivity, glow,
// background layer and background intensity controls.
func (sh *shell) studioInspectorContent() []gui.View {
	return []gui.View{
		sh.section("Inspector"),
		sh.modeRow(),
		sh.paletteRow(),
		sh.sensitivityRow(),
		sh.glowRow(),
		sh.backgroundRow(),
		sh.backgroundIntensityRow(),
	}
}

// cancelImport forwards the import cancellation to the studio service.
func (sh *shell) cancelImport() {
	sh.svc.CancelImport()
	sh.state.SetStatus("Import cancellation requested")
}

// playEventTailText renders the bounded play-event tail.
func (sh *shell) playEventTailText() string {
	events := sh.state.PlayEvents()
	if len(events) == 0 {
		return "No play events yet — press Play to start a session (every request terminates in output.started, failed, or a clear validation outcome)."
	}
	return strings.Join(events, "\n")
}

// previewPane renders the live preview: the PixelSurface frame bridge
// or the honest empty state, framed by the glow chrome driven by the
// intensity preset. The pane size feeds the next PreviewFrame request
// (a resize just changes the request size; the renderer is never
// recreated).
func (sh *shell) previewPane() gui.View {
	paneW := sh.widthDp - previewPaneInsets
	if isSplitFor(sh.widthDp) {
		paneW = sh.widthDp - studioRailWidthDp - previewPaneInsets
	}
	if paneW < 160 {
		paneW = 160
	}
	paneH := float32(previewMinHeightDp)
	// Record the laid-out pane size for the pacer's next render request
	// (clamped to the bounded preview budget).
	sh.state.SetPaneSize(clampPreviewSize(int(paneW), int(paneH)))

	body := []gui.View{sh.previewBody(int(paneW), int(paneH))}
	return gui.Column(gui.ContainerCfg{
		ID:         "studio-preview",
		Sizing:     gui.FillFit,
		Padding:    gui.PaddingNone,
		Spacing:    gui.NoSpacing,
		SizeBorder: gui.BorderPx(2),
		ColorBorder: func() gui.Color {
			if glow := sh.th.GlowColor(); glow.A > 0 {
				return sh.th.GlowBorderColor()
			}
			return sh.th.DecorationBorderColor()
		}(),
		Content: body,
	})
}

// previewBody renders the image (the PixelSurface bridge) or the
// honest empty state.
func (sh *shell) previewBody(w, h int) gui.View {
	if sh.surfaceErr != nil {
		return sh.surfaceBox("studio-preview-error", []gui.View{
			sh.statusLine("Preview surface unavailable: "+sh.surfaceErr.Error()+" — transport, inspector and playback keep working.", "err"),
		})
	}
	if sh.preview.HasFrame() {
		return gui.Image(gui.ImageCfg{
			ID:      "studio-preview-image",
			Surface: sh.surface,
			Width:   float32(w),
			Height:  float32(h),
			A11YCfg: gui.A11YCfg{
				A11YLabel: "Live visualizer preview of the imported track",
			},
		})
	}
	clock := sh.state.Clock()
	return gui.Column(gui.ContainerCfg{
		ID:     "studio-preview-empty",
		Sizing: gui.FillFit,
		Width:  float32(w),
		Height: float32(h),
		Color:  sh.th.ObsidianColor(),
		HAlign: gui.HAlignCenter,
		VAlign: gui.VAlignMiddle,
		Content: []gui.View{
			gui.Text(gui.TextCfg{
				Text:      "No preview frames yet — import a track and press Play (" + clockStateLine(clock) + ")",
				TextStyle: sh.th.TextStyleBody(),
				Mode:      gui.TextModeWrap,
			}),
		},
	})
}

// modeRow — the visualizer mode radios (CmdModeSet).
func (sh *shell) modeRow() gui.View {
	modes := visualizer.AllModes
	opts := make([]radioOpt, 0, len(modes))
	for _, m := range modes {
		opts = append(opts, radioOpt{value: string(m), label: m.DisplayName()})
	}
	return sh.radioRow("studio-mode", "Mode", sh.modeValue, opts, func(v string, _ gui.EventCtx) {
		sh.modeValue = v
		sh.disp.Run(presentation.CmdModeSet)
	})
}

// paletteRow — the brand palette radios (CmdPaletteSet).
func (sh *shell) paletteRow() gui.View {
	opts := make([]radioOpt, 0, len(visualizer.Palettes))
	for i, p := range visualizer.Palettes {
		opts = append(opts, radioOpt{value: strconv.Itoa(i), label: p.Name})
	}
	return sh.radioRow("studio-palette", "Palette", sh.paletteValue, opts, func(v string, _ gui.EventCtx) {
		sh.paletteValue = v
		sh.disp.Run(presentation.CmdPaletteSet)
	})
}

// sensitivityRow — the sensitivity slider (CmdSensitivitySet).
func (sh *shell) sensitivityRow() gui.View {
	return sh.sliderRow("studio-sensitivity", "Sensitivity", sh.sens,
		fmt.Sprintf("%.2f", sh.svc.Config().Sensitivity), 0, 1, 0.01,
		func(v float32, ctx gui.EventCtx) {
			sh.sens = v
			sh.disp.Run(presentation.CmdSensitivitySet)
		})
}

// glowRow — the glow slider (0..1.5 contract range).
func (sh *shell) glowRow() gui.View {
	return sh.sliderRow("studio-glow", "Glow", sh.glow,
		fmt.Sprintf("%.2f", float64(sh.glow)*glowMax), 0, 1, 0.01,
		func(v float32, ctx gui.EventCtx) {
			sh.glow = v
			sh.applyGlow()
		})
}

// backgroundRow — the background composition layer radios incl. none
// (CmdBackgroundLayerSet).
func (sh *shell) backgroundRow() gui.View {
	modes := append([]visualizer.Mode{visualizer.ModeNone}, visualizer.AllModes...)
	opts := make([]radioOpt, 0, len(modes))
	for _, m := range modes {
		label := m.DisplayName()
		if m == visualizer.ModeNone {
			label = "None"
		}
		opts = append(opts, radioOpt{value: string(m), label: label})
	}
	return sh.radioRow("studio-background", "Background layer", sh.bgValue, opts, func(v string, _ gui.EventCtx) {
		sh.bgValue = v
		sh.disp.Run(presentation.CmdBackgroundLayerSet)
	})
}

// backgroundIntensityRow — the background layer intensity slider
// (0..0.6 contract bound from visualizer.MaxBackgroundIntensity).
func (sh *shell) backgroundIntensityRow() gui.View {
	return sh.sliderRow("studio-background-intensity", "Background intensity", sh.bgInt,
		fmt.Sprintf("%.2f", float64(sh.bgInt)*backgroundIntensityMax), 0, 1, 0.01,
		func(v float32, ctx gui.EventCtx) {
			sh.bgInt = v
			sh.applyBackgroundIntensity()
		})
}

// sliderRow renders a caption-labeled slider with its current value.
func (sh *shell) sliderRow(id, label string, value float32, valueText string, min, max, step float32, onChange func(float32, gui.EventCtx)) gui.View {
	return sh.fieldColumn(label+" ("+valueText+")", gui.Slider(gui.SliderCfg{
		ID:      id,
		Value:   value,
		Min:     min,
		Max:     max,
		Step:    step,
		Width:   sliderMinWidthDp,
		A11YCfg: gui.A11YCfg{A11YLabel: label},
		OnChange: func(v float32, ctx gui.EventCtx) {
			ctx.Consume()
			onChange(v, ctx)
		},
	}))
}
