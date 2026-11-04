// Package fyneui owns the minimal Phase 1 desktop surface.
//
// Phase 1 contract: ONE minimal Fyne window with the exact product title and
// a clear placeholder identifying this as the Phase 1 foundation. No studio
// navigation, editor, timeline, library, player, visualizer controls, or
// finished visual design — that is Phase 2 work (see roadmap.md).
//
// Threading rule: no goroutine mutates Fyne widgets directly. This package
// creates no goroutines at all; if Phase 2 needs asynchronous work it must
// use the Fyne UI-thread mechanism documented for the chosen Fyne version
// (fyne.Do where supported) with a clear ownership rule.
package fyneui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/brand"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/buildinfo"
)

// NewWindow builds the minimal Phase 1 window for the given app. The window
// title uses the exact product name; the content is a centered placeholder
// plus the current build identity. Kept deliberately small and responsive.
func NewWindow(a fyne.App) fyne.Window {
	w := a.NewWindow(brand.ProductName)

	title := widget.NewLabel(brand.BrandTag + "  " + brand.ProductName)
	title.TextStyle = fyne.TextStyle{Bold: true}

	phase := widget.NewLabel(brand.PhasePlaceholder)
	phase.Wrapping = fyne.TextWrapWord

	build := widget.NewLabel("build " + buildinfo.Get().Version)
	footer := widget.NewLabel(brand.CopyrightRange)
	footer.TextStyle = fyne.TextStyle{Monospace: true}

	w.SetContent(container.NewCenter(container.NewVBox(
		title,
		phase,
		build,
		footer,
	)))
	w.Resize(fyne.NewSize(640, 320))
	return w
}

// Run starts the minimal desktop application and blocks until the window is
// closed. The returned error is non-nil only if the app could not be created;
// window runtime itself is managed by the Fyne event loop.
func Run() error {
	a := app.NewWithID(brand.AppIdentifier)
	if a == nil {
		return errAppUnavailable
	}
	w := NewWindow(a)
	w.ShowAndRun()
	return nil
}

type windowError string

func (e windowError) Error() string { return string(e) }

const errAppUnavailable windowError = "fyne: desktop app could not be created"
