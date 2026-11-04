// Package fyneapp owns the minimal Fyne desktop launch surface for
// SHEYTAN Music Visualizer.
//
// Phase 2 scope: the full studio shell lives in studio.go and
// actions.go (top bar, library, live visualizer preview, inspector,
// transport, export dialog). The Phase 1 placeholder window was
// replaced by the studio in Phase 2.
//
// Rule: never create goroutines that mutate Fyne widgets directly.
// Asynchronous work uses Fyne's documented UI-thread mechanism
// (fyne.Do) with a clear ownership rule.
package fyneapp

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/brand"
)

// Run starts the full studio application; see studio.go (Phase 2).
// This file keeps the package contract and Phase 1 history.
var _ = fyne.NewSize
var _ = container.New
var _ = widget.NewLabel
var _ = app.NewWithID
var _ = brand.ProductName
