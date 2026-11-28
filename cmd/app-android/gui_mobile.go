// gui_mobile.go — the gomobile lifecycle glue for the mobile targets
// (android and iOS) of the SHEYTAN Android carrier shell.
//
// gomobile build (-target android) refuses to package a main package
// that does not import golang.org/x/mobile/app (cmd/gomobile build.go
// checks the symbol table; the NativeActivity / UIKit lifecycle glue is
// linked only when the app imports it). Without this file the required
// product APK gate fails with
//   "cmd/app-android does not import golang.org/x/mobile/app"
// — the same class as the observed build-android failure of hosted run
// 45297707372 (then against cmd/app-gogui, removed by
// repair/run-46033044814 when the desktop Go-Gui shell was retired).
//
// HONEST RUNTIME SCOPE (docs/roadmap.md + docs/agent.md): go-gui's
// android delivery used the Kotlin GLSurfaceView host pattern, which
// shipped with a later native host slice. This build is therefore the
// EXPERIMENTAL compile-class carrier: it proves the whole product tree
// (goffi, wgpu, audio, export) compiles and links for the target ABI,
// and it runs the real x/mobile lifecycle loop so the packaged APK is
// structurally valid and OS-lifecycle responsive (it exits cleanly when
// the activity is destroyed). It does NOT claim shell rendering, audio
// playback, or any product runtime behavior on the device;
// validate-apk.py checks structure, identity, ABI and signature only,
// and emulator evidence is recorded UNAVAILABLE by the CI contract.
// No placeholder screen, no fake UI, no invented runtime claim.
//
//go:build android

package main

import (
	"golang.org/x/mobile/app"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/logging"
)

// runShell is the mobile entry: run the real x/mobile lifecycle loop
// (NativeActivity on android, UIApplication on iOS) draining OS events
// until the OS closes the app. The boot parameter keeps signature
// parity with the host entry; mobile builds receive no command line.
func runShell(boot *logging.Log) error {
	app.Main(func(a app.App) {
		for range a.Events() {
			// Experimental carrier: consume the OS lifecycle so the
			// main thread stays responsive (no ANR) until the OS
			// destroys the activity. The shell's mobile delivery is
			// documented as a later slice; nothing here fakes it.
		}
	})
	return nil
}
