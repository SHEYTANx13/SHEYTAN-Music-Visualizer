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
	"fmt"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/appicon"
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
		// Brand icon on the runtime surface (run 46121470729
		// icon task): Wails hands options.Icon to the platform
		// shell (Linux application_linux[_purego].go setIcon;
		// the about box and dialogs elsewhere). The
		// EXECUTION-FILE icon contract itself is carried by the
		// build lanes: Windows resource.syso (windres,
		// build-shell-resource gate), macOS AppIcon.icns
		// (iconutil, package-macos gate), Linux freedesktop
		// hicolor PNG in the portable package.
		Icon: appicon.PNG(),
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

	// Native dialog host (host.go): import.audio and export.start ask
	// the OS for a user-selected path. The path is consumed inside Go
	// (studio.ImportFile / export.Request); it NEVER crosses into the
	// web layer. A user cancel arrives as ("", nil) and is reported as
	// no-change, never as a fake error. Audio filters mirror the real
	// import surface (WAV/MP3/FLAC/Ogg containers, see internal/audio).
	svc.setDialogs(HostDialogsFuncs{
		OpenAudio: func() (string, error) {
			a := application.Get()
			if a == nil {
				return "", fmt.Errorf("application is not running")
			}
			return a.Dialog.OpenFile().
				SetTitle("Import music (WAV / MP3 / FLAC / Ogg)").
				CanChooseFiles(true).
				AddFilter("Audio files", "*.wav;*.mp3;*.flac;*.ogg;*.oga").
				AddFilter("WAV", "*.wav").
				AddFilter("MP3", "*.mp3").
				AddFilter("FLAC", "*.flac").
				AddFilter("Ogg Vorbis", "*.ogg;*.oga").
				PromptForSingleSelection()
		},
		SaveExport: func(defaultName string) (string, error) {
			a := application.Get()
			if a == nil {
				return "", fmt.Errorf("application is not running")
			}
			return a.Dialog.SaveFile().
				SetMessage("Save music video (MP4)").
				SetFilename(defaultName).
				CanCreateDirectories(true).
				AddFilter("MP4 video", "*.mp4").
				PromptForSingleSelection()
		},
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
