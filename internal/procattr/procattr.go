// Package procattr is the single child-process creation-policy authority
// for SHEYTAN Music Visualizer (run 42598929646 Windows no-console
// contract).
//
// The application is a GUI-subsystem Windows binary. Every console child
// it launches (the FFmpeg/ffprobe helpers) would normally inherit — or
// allocate — a visible console window. HideWindow plus CREATE_NO_WINDOW
// prevent BOTH: no window is created for the child and no console is
// attached. This is window hygiene only: stdout/stderr pipes keep
// working, so helper diagnostics stay captured in the runtime/export
// logs (output is redirected, never suppressed).
//
// The helper is a no-op on non-Windows platforms: POSIX children have no
// console-window concept, and Linux desktop launches never allocate a
// terminal for a child process.
package procattr

import (
	"os/exec"
)

// Configure applies the platform child-process policy to cmd. It must
// be called before Start/Run/Output. On Windows it sets HideWindow and
// CREATE_NO_WINDOW (see procattr_windows.go); on every other platform
// it deliberately changes nothing.
func Configure(cmd *exec.Cmd) {
	configure(cmd)
}
