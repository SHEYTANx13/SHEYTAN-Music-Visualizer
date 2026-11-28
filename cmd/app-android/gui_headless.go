// gui_headless.go — the non-Android host behavior of the Android
// carrier shell (repair/run-46033044814).
//
// The desktop Go-Gui shell was removed in that repair; the Wails v3
// web shell (cmd/app-wails) is the only desktop UI. Running the mobile
// carrier's run mode on a desktop host is therefore an explicit,
// honest error — never a silent no-op and never a fake window.
//
//go:build !android

package main

import (
	"fmt"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/logging"
)

// runShell reports the honest platform boundary. The --version,
// --build-info-json and --self-check modes still work everywhere (CI
// parity gates), but the lifecycle loop itself is Android-only.
func runShell(boot *logging.Log) error {
	return fmt.Errorf(
		"the Android carrier shell runs only under android (gomobile); " +
			"the desktop product is the Wails shell (cmd/app-wails)")
}
