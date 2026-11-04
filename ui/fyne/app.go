// Package fyneapp owns the minimal Fyne desktop launch surface for
// SHEYTAN Music Visualizer.
//
// Phase 1 scope: one window, the exact product title, one placeholder
// that identifies this build as the Phase 1 foundation. No studio UI,
// navigation, timeline, library, player or visualizer controls — those
// are Phase 2 deliverables (see roadmap.md).
//
// Rule: never create goroutines that mutate Fyne widgets directly.
// Future asynchronous work must use Fyne's documented UI-thread
// mechanism (fyne.Do, where supported by the selected Fyne version)
// with a clear ownership rule.
package fyneapp

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/brand"
)

// Run starts the minimal Fyne application and blocks until the window
// is closed. It returns a non-nil error on real startup failures.
func Run() error {
	a := app.NewWithID(brand.AppID)
	w := a.NewWindow(brand.WindowTitle())

	title := widget.NewLabelWithStyle(
		brand.ProductName,
		fyne.TextAlignLeading,
		fyne.TextStyle{Bold: true},
	)
	note := widget.NewLabel("Phase 1 foundation build. The studio UI, media import, " +
		"analysis, visualizer engines, GPU rendering and MP4 export arrive in Phase 2. " +
		"See roadmap.md for the staged plan.")
	note.Wrapping = fyne.TextWrapWord
	legal := widget.NewLabel(brand.Copyright)
	legal.Wrapping = fyne.TextWrapWord

	w.SetContent(container.NewVBox(title, note, legal))
	w.Resize(fyne.NewSize(640, 420))
	w.ShowAndRun()
	return nil
}
