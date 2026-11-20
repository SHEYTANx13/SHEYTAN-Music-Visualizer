// studio_panel.go — the Studio destination: library, live preview
// canvas (the CPU frame bridge), inspector controls, transport readout
// and the bounded play-event tail.
//
// Split composition (desktop/wide breakpoints): the library/inspector
// render as a side rail next to the main preview pane. Stacked
// composition (phone/tablet): one vertical scroll. The composition is
// decided per frame from the window width — widgets are shared
// instances, so user state survives breakpoint crossings.

package gogui

import (
	"fmt"
	"image"
	"strconv"
	"strings"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/presentation"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/visualizer"
)

// Row counts for the two compositions.
const (
	studioStackedRows = 17
	studioRailRows    = 12
	studioMainRows    = 5
)

// panelStudio renders the Studio destination for the active breakpoint.
func (sh *shell) panelStudio(gtx layout.Context) layout.Dimensions {
	if isSplitFor(sh.widthDp) {
		return layout.Flex{Axis: layout.Horizontal}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return sh.scroll(gtx, &sh.studioMainList, sh.studioMainRows, studioMainRows)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				width := gtx.Dp(unit.Dp(studioRailWidthDp))
				gtx.Constraints = layout.Constraints{
					Min: image.Pt(width, 0),
					Max: image.Pt(width, gtx.Constraints.Max.Y),
				}
				return sh.scroll(gtx, &sh.studioRailList, sh.studioRailRows, studioRailRows)
			}),
		)
	}
	return sh.scroll(gtx, &sh.studioList, sh.studioStackedRows, studioStackedRows)
}

// studioStackedRows — single-column composition (phone/tablet).
func (sh *shell) studioStackedRows(gtx layout.Context, i int) layout.Dimensions {
	switch i {
	case 0:
		return sh.section(gtx, "Library")
	case 1:
		return sh.line(gtx, formatTrackInfo(sh.state.Track()))
	case 2:
		return sh.editorRow(gtx, &sh.importPathEd, "Audio file path (wav/mp3/flac/ogg…)")
	case 3:
		return sh.buttonRow(gtx, []btnDef{
			{clk: &sh.importGoBtn, txt: "Import", cmd: presentation.CmdImportAudio},
			{clk: &sh.importCancelBtn, txt: "Cancel import", fn: func() { sh.cancelImport() }},
		})
	case 4:
		return sh.importStatus(gtx)
	case 5:
		return sh.section(gtx, "Preview")
	case 6:
		return sh.previewPane(gtx)
	case 7:
		return sh.line(gtx, "Transport: "+transportLine(sh.state.Clock()))
	case 8:
		return sh.section(gtx, "Play events")
	case 9:
		return sh.caption(gtx, sh.playEventTailText())
	case 10:
		return sh.section(gtx, "Inspector")
	case 11:
		return sh.modeRow(gtx)
	case 12:
		return sh.paletteRow(gtx)
	case 13:
		return sh.sensitivityRow(gtx)
	case 14:
		return sh.glowRow(gtx)
	case 15:
		return sh.backgroundRow(gtx)
	case 16:
		return sh.backgroundIntensityRow(gtx)
	default:
		return layout.Dimensions{}
	}
}

// studioRailRows — library + inspector side rail (split composition).
func (sh *shell) studioRailRows(gtx layout.Context, i int) layout.Dimensions {
	switch i {
	case 0:
		return sh.section(gtx, "Library")
	case 1:
		return sh.line(gtx, formatTrackInfo(sh.state.Track()))
	case 2:
		return sh.editorRow(gtx, &sh.importPathEd, "Audio file path (wav/mp3/flac/ogg…)")
	case 3:
		return sh.buttonRow(gtx, []btnDef{
			{clk: &sh.importGoBtn, txt: "Import", cmd: presentation.CmdImportAudio},
			{clk: &sh.importCancelBtn, txt: "Cancel import", fn: func() { sh.cancelImport() }},
		})
	case 4:
		return sh.importStatus(gtx)
	case 5:
		return sh.section(gtx, "Inspector")
	case 6:
		return sh.modeRow(gtx)
	case 7:
		return sh.paletteRow(gtx)
	case 8:
		return sh.sensitivityRow(gtx)
	case 9:
		return sh.glowRow(gtx)
	case 10:
		return sh.backgroundRow(gtx)
	case 11:
		return sh.backgroundIntensityRow(gtx)
	default:
		return layout.Dimensions{}
	}
}

// studioMainRows — preview + transport + play events (split composition).
func (sh *shell) studioMainRows(gtx layout.Context, i int) layout.Dimensions {
	switch i {
	case 0:
		return sh.section(gtx, "Preview")
	case 1:
		return sh.previewPane(gtx)
	case 2:
		return sh.line(gtx, "Transport: "+transportLine(sh.state.Clock()))
	case 3:
		return sh.section(gtx, "Play events")
	case 4:
		return sh.caption(gtx, sh.playEventTailText())
	default:
		return layout.Dimensions{}
	}
}

// importStatus renders the import progress and the recent-paths list.
func (sh *shell) importStatus(gtx layout.Context) layout.Dimensions {
	progress, importing := sh.state.ImportSnapshot()
	var w layout.Widget = func(gtx layout.Context) layout.Dimensions {
		if importing {
			bar := material.ProgressBar(sh.th.Theme(), float32(progress)/100)
			return layout.Inset{Top: unit.Dp(4), Bottom: unit.Dp(4)}.Layout(gtx, bar.Layout)
		}
		if len(sh.recentPaths) == 0 {
			return layout.Dimensions{}
		}
		list := make([]string, 0, len(sh.recentPaths))
		list = append(list, "Recent import paths:")
		list = append(list, sh.recentPaths...)
		return sh.caption(gtx, strings.Join(list, "\n"))
	}
	return w(gtx)
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

// previewPane renders the live preview: the blitted renderer frame or
// the honest empty state, surrounded by the glow chrome driven by the
// intensity preset. The laid-out pane size feeds the next PreviewFrame
// request (a resize just changes the request size; the renderer is
// never recreated).
func (sh *shell) previewPane(gtx layout.Context) layout.Dimensions {
	height := gtx.Dp(unit.Dp(previewMinHeightDp))
	size := image.Pt(gtx.Constraints.Max.X, height)
	sh.previewPaneSize = size
	// Obsidian backdrop.
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, sh.th.Obsidian())
	imgOp, ok := sh.preview.Image()
	if ok {
		img := widget.Image{Src: imgOp, Fit: widget.Contain, Position: layout.Center, Scale: 1}
		inner := gtx
		inner.Constraints = layout.Exact(size)
		dims := img.Layout(inner)
		// Glow chrome (zeroed by the reduced-glow switch).
		if glow := sh.th.GlowColor(); glow.A > 0 {
			clip.Rect{Max: image.Pt(size.X, gtx.Dp(unit.Dp(2)))}.Push(gtx.Ops).Pop()
			paint.Fill(gtx.Ops, glow)
			clip.Rect{Max: image.Pt(gtx.Dp(unit.Dp(2)), size.Y)}.Push(gtx.Ops).Pop()
			paint.Fill(gtx.Ops, glow)
		}
		return dims
	}
	clock := sh.state.Clock()
	inner := gtx
	inner.Constraints = layout.Exact(size)
	return layout.Center.Layout(inner, func(gtx layout.Context) layout.Dimensions {
		return material.Body1(sh.th.Theme(),
			"No preview frames yet — import a track and press Play ("+clockStateLine(clock)+")").Layout(gtx)
	})
}

// modeRow — the visualizer mode radios (CmdModeSet).
func (sh *shell) modeRow(gtx layout.Context) layout.Dimensions {
	modes := visualizer.AllModes
	keys := make([]string, len(modes))
	labels := make([]string, len(modes))
	for i, m := range modes {
		keys[i] = string(m)
		labels[i] = m.DisplayName()
	}
	return sh.radioRow(gtx, "Mode", &sh.modeEnum, keys, labels)
}

// paletteRow — the brand palette radios (CmdPaletteSet).
func (sh *shell) paletteRow(gtx layout.Context) layout.Dimensions {
	keys := make([]string, len(visualizer.Palettes))
	labels := make([]string, len(visualizer.Palettes))
	for i, p := range visualizer.Palettes {
		keys[i] = strconv.Itoa(i)
		labels[i] = p.Name
	}
	return sh.radioRow(gtx, "Palette", &sh.paletteEnum, keys, labels)
}

// sensitivityRow — the sensitivity slider (CmdSensitivitySet).
func (sh *shell) sensitivityRow(gtx layout.Context) layout.Dimensions {
	return sh.sliderRow(gtx, "Sensitivity", &sh.sens, fmt.Sprintf("%.2f", sh.svc.Config().Sensitivity))
}

// glowRow — the glow slider (0..1.5 contract range).
func (sh *shell) glowRow(gtx layout.Context) layout.Dimensions {
	return sh.sliderRow(gtx, "Glow", &sh.glow, fmt.Sprintf("%.2f", float64(sh.glow.Value)*glowMax))
}

// backgroundRow — the background composition layer radios incl. none
// (CmdBackgroundLayerSet).
func (sh *shell) backgroundRow(gtx layout.Context) layout.Dimensions {
	modes := append([]visualizer.Mode{visualizer.ModeNone}, visualizer.AllModes...)
	keys := make([]string, len(modes))
	labels := make([]string, len(modes))
	for i, m := range modes {
		keys[i] = string(m)
		if m == visualizer.ModeNone {
			labels[i] = "None"
		} else {
			labels[i] = m.DisplayName()
		}
	}
	return sh.radioRow(gtx, "Background layer", &sh.bgEnum, keys, labels)
}

// backgroundIntensityRow — the background layer intensity slider
// (0..0.6 contract bound from visualizer.MaxBackgroundIntensity).
func (sh *shell) backgroundIntensityRow(gtx layout.Context) layout.Dimensions {
	return sh.sliderRow(gtx, "Background intensity", &sh.bgInt,
		fmt.Sprintf("%.2f", float64(sh.bgInt.Value)*backgroundIntensityMax))
}

// sliderRow renders a caption-labeled slider with its current value.
func (sh *shell) sliderRow(gtx layout.Context, label string, f *widget.Float, value string) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return sh.caption(gtx, label+" ("+value+")")
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Dp(unit.Dp(sliderMinWidthDp))
			return material.Slider(sh.th.Theme(), f).Layout(gtx)
		}),
	)
}

// backgroundIntensityMax mirrors visualizer.MaxBackgroundIntensity for
// the slider mapping.
const (
	backgroundIntensityMax = visualizer.MaxBackgroundIntensity
	studioRailWidthDp      = 360
	previewMinHeightDp     = 260
	sliderMinWidthDp       = 220
)
