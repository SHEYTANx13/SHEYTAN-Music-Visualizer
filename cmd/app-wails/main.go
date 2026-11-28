// Command app-wails is the Wails v3 web-technology shell entry point
//go:build purego && sheytan_nogpu

// for SHEYTAN Music Visualizer BETA.
//
// It is an ALTERNATIVE presentation shell with its own window and
// webview lifecycle: one top-level webview window showing the control/
// status surface (build identity, transport, presets, settings,
// diagnostics and renderer EVIDENCE — the web shell does not render
// the visualizer; the real-time preview lives in the native shell).
//
// Usage:
//
//	app-wails                     start the web shell window
//	app-wails --version           print product/build metadata, then exit
//	app-wails --build-info-json   print machine-readable build metadata
//	                              as one JSON line, then exit
//
// Build note: the Wails v3 application package needs either cgo or the
// purego build tag on linux/darwin (upstream buildguard). The repo
// canonical link configuration additionally applies -tags goffi_static
// because the core links the wgpu/goffi FFI stack together with
// CGO-based dependencies (see docs/ai-notes.md "Critical linker discovery").
//
//	linux:   CGO_ENABLED=1 go build -tags "goffi_static purego" ./cmd/app-wails
//	windows: CGO_ENABLED=0 go build -tags purego               ./cmd/app-wails
//
// Real startup errors return a non-zero process exit code; errors are
// never swallowed.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/apppaths"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/brand"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/buildinfo"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/logging"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/studio"
	wailsshell "github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/transport/wails"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the testable process body. Flag semantics mirror
// cmd/sheytan-music-visualizer exactly: --version prints the product
// line plus the attribution block; --build-info-json prints the stable
// machine-readable JSON line consumed by typed CI decoders.
func run(args []string, out, errW io.Writer) int {
	fs := flag.NewFlagSet(brand.ProductName, flag.ContinueOnError)
	fs.SetOutput(errW)
	showVersion := fs.Bool("version", false, "print product and build metadata, then exit")
	buildInfoJSONFlag := fs.Bool("build-info-json", false, "print the build metadata as one stable JSON line (machine-readable CI evidence), then exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(errW, "%s: unexpected arguments: %s\n", brand.ProductName, strings.Join(fs.Args(), " "))
		return 2
	}
	switch {
	case *buildInfoJSONFlag:
		fmt.Fprintln(out, string(buildinfo.JSON()))
		return 0
	case *showVersion:
		fmt.Fprintf(out, "%s %s\n", brand.ProductName, buildinfo.Line())
		for _, line := range brand.AttributionBlock() {
			fmt.Fprintln(out, line)
		}
		return 0
	default:
		if err := runShell(); err != nil {
			fmt.Fprintf(errW, "%s: startup failed: %v\n", brand.ProductName, err)
			return 1
		}
		return 0
	}
}

// runShell creates the studio core and hands it to the single-window
// web shell. studio.Close is the graceful teardown on EVERY exit path
// after successful construction (the studio Close is idempotent and
// dependency-safe by contract), so neither app.Run errors nor panics
// leak owned workers, transports or scheduler goroutines.
func runShell() error {
	// Run 46168717789 log catcher: THE PRODUCT SHELL NOW WRITES THE APP
	// LOG. Previously only the CLI opened it, so app-wails.exe (the
	// shell users actually run) produced no app.log at all and the
	// Diagnostics tab showed a path no file backed. The log is opened
	// BEFORE the studio so the startup banner lands in the file, and
	// closed LAST so the shutdown is captured. A failed open falls
	// back to the stderr logger inside logging.Init (honest, recorded)
	// — the shell still starts.
	lay := apppaths.Resolve(apppaths.OSEnv())
	lg, _, logErr := logging.Init(lay, os.Getenv("SHEYTAN_DEBUG") != "")
	if logErr != nil {
		// The fallback logger is already active and the error is
		// recorded in it; startup proceeds with the visible fallback.
		fmt.Fprintf(os.Stderr, "%s: runtime log fallback: %v\n", brand.ProductName, logErr)
	}
	st, err := studio.New()
	if err != nil {
		return fmt.Errorf("create studio: %w", err)
	}
	defer st.Close()
	defer lg.Close()

	app := wailsshell.BuildApp(st, lg)
	if err := app.Run(); err != nil {
		return fmt.Errorf("web shell runtime: %w", err)
	}
	return nil
}
