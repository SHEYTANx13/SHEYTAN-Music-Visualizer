//go:build purego && sheytan_nogpu

package wails

// app.go — the Wails v3 application wiring for the web shell.
//
// This file requires the purego build tag because the Wails v3
// application package compiles on linux/darwin only with either cgo or
// -tags purego (upstream internal/buildguard). The facade and the
// /api mux (service.go) stay toolkit-free and headless-testable; only
// the window lifecycle lives here. See doc.go for the boundary rules.

import (
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/brand"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/studio"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// BuildApp creates the single-window Wails application for the web
// shell: one SheytanService instance (registered as a Wails Service so
// the runtime binding path stays available to future slices) and one
// asset handler serving the embedded frontend plus the /api/* facade
// endpoints. It does NOT call app.Run; the caller owns the blocking
// run and the studio teardown.
func BuildApp(st *studio.Studio) *application.App {
	svc := NewService(st)

	app := application.New(application.Options{
		Name:        brand.ProductName,
		Description: "SHEYTAN Music Visualizer BETA — web-technology shell (control/status surface; the web shell does not render the visualizer)",
		Services: []application.Service{
			application.NewService(svc),
		},
		Assets: application.AssetOptions{
			Handler: NewAssetHandler(svc),
		},
	})

	// app.quit leaves the web layer with an explicit, window-owned
	// action instead of process-level privileges.
	svc.setQuitHandler(func() {
		if a := application.Get(); a != nil {
			a.Quit()
		}
	})

	// Exactly ONE top-level window. The title comes from the brand
	// authority; the obsidian background avoids a white flash before
	// the first webview paint.
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            brand.WindowTitle(),
		Width:            1280,
		Height:           800,
		MinWidth:         960,
		MinHeight:        600,
		BackgroundColour: application.NewRGBA(0x08, 0x08, 0x08, 0xFF),
	})

	return app
}
