// Command app-android is the Android carrier entry point of SHEYTAN
// Music Visualizer BETA (gomobile packaging; the APK's native entry).
//
// History (repair/run-46033044814): this entry used to be the desktop
// Go-Gui shell (cmd/app-gogui). The desktop Go-Gui shell and the whole
// ui/gogui panel tree were REMOVED in that repair — the Wails v3 web
// shell (cmd/app-wails) is now the only desktop UI. What remains here
// is the gomobile/Android delivery only: gomobile build refuses to
// package a main package that does not import golang.org/x/mobile/app
// (see gui_mobile.go), so this package is the mobile carrier.
//
// Usage (desktop hosts, for CI parity gates):
//
//	app-android --version         print product/build metadata, then exit
//	app-android --build-info-json print the machine-readable build snapshot
//	                              as one JSON line, then exit
//	app-android --self-check      verify foundation invariants without any
//	                              window or mobile lifecycle, then exit
//
// Starting the shell run mode on a non-Android host is an explicit,
// honest error (the mobile lifecycle glue is Android-only; the desktop
// product is app-wails).
package main

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/design"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/apppaths"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/brand"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/buildinfo"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/logging"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/presentation"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/render"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/visualizer"
)

func main() {
	// BOOTSTRAP FIRST: the runtime log opens before flag parsing (the
	// same bootstrap-first diagnostics contract the product shells
	// follow; a failure to write the log degrades to stderr and never
	// takes the process down).
	lay := apppaths.Resolve(apppaths.OSEnv())
	if err := apppaths.Bootstrap(lay); err != nil {
		_ = err // crash-report directory may be missing; never fatal here
	}
	boot := logging.Bootstrap(lay)
	boot.LogStartupBanner(lay)
	errW := boot.TeeStderr()
	code := 1
	defer func() {
		if r := recover(); r != nil {
			boot.WriteCrashReport(lay.Crashes, "app-android main", r, debug.Stack())
			code = 1
		}
		boot.LogExit(code)
		_ = boot.Close()
		os.Exit(code)
	}()
	code = run(os.Args[1:], os.Stdout, errW, boot)
}

// mode is the parsed command-line intent.
type mode struct {
	Version       bool
	BuildInfoJSON bool
	SelfCheck     bool
	Run           bool
}

// parseArgs maps flags onto the intent. Unknown flags or stray
// arguments are a usage error (exit 2 upstream).
func parseArgs(args []string, fs *flag.FlagSet) (mode, error) {
	var m mode
	fs.BoolVar(&m.Version, "version", false, "print product and build metadata, then exit")
	fs.BoolVar(&m.BuildInfoJSON, "build-info-json", false, "print the build metadata as one stable JSON line, then exit")
	fs.BoolVar(&m.SelfCheck, "self-check", false, "run foundation self-checks, then exit without a window")
	if err := fs.Parse(args); err != nil {
		return mode{}, err
	}
	if fs.NArg() > 0 {
		return mode{}, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	// Exactly one mode wins; the default is the shell run.
	if !m.Version && !m.BuildInfoJSON && !m.SelfCheck {
		m.Run = true
	}
	return m, nil
}

func run(args []string, out, errW io.Writer, boot *logging.Log) int {
	fs := flag.NewFlagSet(brand.ProductName+" (Android carrier shell)", flag.ContinueOnError)
	fs.SetOutput(errW)
	m, err := parseArgs(args, fs)
	if err != nil {
		fmt.Fprintln(errW, err)
		logBoot(boot, "startup", "argument parse failed", err)
		return 2
	}
	switch {
	case m.BuildInfoJSON:
		fmt.Fprintln(out, string(buildinfo.JSON()))
		return 0
	case m.Version:
		fmt.Fprintf(out, "%s (Android carrier shell) %s\n", brand.ProductName, buildinfo.Line())
		for _, line := range brand.AttributionBlock() {
			fmt.Fprintln(out, line)
		}
		return 0
	case m.SelfCheck:
		return selfCheck(out, errW)
	default:
		err := runShell(boot)
		if err == nil {
			return 0
		}
		fmt.Fprintf(errW, "%s: startup failed: %v\n", brand.ProductName, err)
		logBoot(boot, "startup", "shell startup failed", err)
		return 1
	}
}

// logBoot records one startup-failure diagnosis in the runtime log
// (nil-safe: the stderr fallback Log is never nil, but be defensive).
func logBoot(boot *logging.Log, phase, msg string, err error) {
	if boot == nil {
		return
	}
	boot.Logger().Error(msg,
		slogString("phase", phase), slogString("err", err.Error()))
}

// slogString keeps the call sites terse (attribute values pass the
// ASCII contract through the handler's ReplaceAttr).
func slogString(key, value string) slog.Attr { return slog.String(key, value) }

type check struct {
	name string
	fn   func(io.Writer) error
}

// selfCheck verifies the foundation invariants WITHOUT opening any
// window or mobile lifecycle: build identity, token document, the real
// renderer selection rendering one deterministic CPU frame, the
// presentation destination catalog integrity and a writable workspace
// layout store.
func selfCheck(out, errW io.Writer) int {
	checks := []check{
		{"buildinfo", checkBuildInfo},
		{"design-tokens", checkDesignTokens},
		{"renderer-frame", checkRendererFrame},
		{"destination-catalog", checkDestinationCatalog},
		{"layout-store", checkLayoutStore},
	}
	failures := 0
	for _, c := range checks {
		if err := c.fn(out); err != nil {
			failures++
			fmt.Fprintf(errW, "self-check FAIL %s: %v\n", c.name, err)
			continue
		}
		fmt.Fprintf(out, "self-check PASS %s\n", c.name)
	}
	if failures > 0 {
		fmt.Fprintf(errW, "self-check: %d check(s) failed\n", failures)
		return 1
	}
	fmt.Fprintln(out, "self-check: all foundation invariants hold")
	return 0
}

// checkBuildInfo verifies the normalized build identity is complete.
func checkBuildInfo(out io.Writer) error {
	i := buildinfo.Normalized()
	for name, v := range map[string]string{
		"version": i.Version, "commit": i.Commit, "date": i.Date, "builtBy": i.BuiltBy, "go": i.GoVersion,
	} {
		if strings.TrimSpace(v) == "" {
			return fmt.Errorf("buildinfo.%s is empty", name)
		}
	}
	fmt.Fprintf(out, "  version=%s go=%s\n", i.Version, i.GoVersion)
	return nil
}

// checkDesignTokens loads the canonical token document and verifies
// the identity contract the shells rely on.
func checkDesignTokens(out io.Writer) error {
	doc, err := design.Load()
	if err != nil {
		return fmt.Errorf("design tokens: %w", err)
	}
	if doc.Identity != "BETA" {
		return fmt.Errorf("token identity is %q, want BETA", doc.Identity)
	}
	for _, name := range []string{"subtle", "ritual", "infernal"} {
		if _, ok := doc.IntensityPresets[name]; !ok {
			return fmt.Errorf("intensity preset %q missing", name)
		}
	}
	for _, group := range []string{"obsidian", "deepCharcoal", "volcanic"} {
		if doc.Color.Surface[group] == "" {
			return fmt.Errorf("surface token %q missing", group)
		}
	}
	fmt.Fprintf(out, "  intensity presets: subtle/ritual/infernal · default=%s · breakpoints ok\n", doc.DefaultIntensity)
	return nil
}

// checkRendererFrame selects the REAL renderer (CPU always available,
// GPU only with a proven hardware adapter) and renders one
// deterministic frame, reporting the honest backend evidence.
func checkRendererFrame(out io.Writer) error {
	r, info := render.SelectRenderer()
	defer r.Close()
	if info.Backend != render.BackendCPU && info.Backend != render.BackendGPU {
		return fmt.Errorf("unexpected backend %q", info.Backend)
	}
	img, err := r.RenderFrame(sampleFrame(96, 64))
	if err != nil {
		return fmt.Errorf("render frame: %w", err)
	}
	if img == nil || img.Bounds().Empty() {
		return fmt.Errorf("empty frame")
	}
	fmt.Fprintf(out, "  backend=%s gpu=%s api=%s adapter=%s class=%s\n",
		info.Backend, info.GpuState, info.BackendAPI, info.Adapter, info.DeviceClass)
	if info.Detail != "" {
		fmt.Fprintf(out, "  renderer detail: %s\n", info.Detail)
	}
	return nil
}

// checkDestinationCatalog verifies the canonical destination set the
// tab strips render (twelve destinations, valid order).
func checkDestinationCatalog(out io.Writer) error {
	catalog := presentation.DestinationCatalog()
	if len(catalog) != len(presentation.Destinations) {
		return fmt.Errorf("catalog has %d entries, want %d", len(catalog), len(presentation.Destinations))
	}
	if err := presentation.ValidateDestinationOrder(presentation.DefaultLayout().TabOrder); err != nil {
		return fmt.Errorf("default layout order: %w", err)
	}
	return nil
}

// checkLayoutStore verifies the workspace layout store round-trips
// through the resolved config directory (default layout load + save).
func checkLayoutStore(out io.Writer) error {
	lay := apppaths.Resolve(apppaths.OSEnv())
	if err := apppaths.Bootstrap(lay); err != nil {
		return fmt.Errorf("data layout bootstrap: %w", err)
	}
	store := presentation.NewLayoutStore(filepath.Join(lay.Config, presentation.DefaultFilename))
	got, err := store.Load()
	if err != nil {
		return fmt.Errorf("load layout: %w", err)
	}
	if err := store.Save(got); err != nil {
		return fmt.Errorf("save layout: %w", err)
	}
	fmt.Fprintf(out, "  data-mode=%s layout=%s\n", lay.Mode, filepath.Join(lay.Config, presentation.DefaultFilename))
	return nil
}

// sampleFrame builds the deterministic visualizer frame input used by
// the renderer check.
func sampleFrame(w, h int) visualizer.FrameInput {
	bins := make([]float64, visualizer.NumVizBins)
	for i := range bins {
		bins[i] = 0.4 + 0.3*float64(i%8)/8
	}
	return visualizer.FrameInput{
		FrameIndex: 1, Time: 1.0 / 30, Width: w, Height: h,
		Config: visualizer.Config{Mode: visualizer.ModeBars},
		Audio:  visualizer.AudioState{Bass: 0.5, Mid: 0.3, Treble: 0.2, Bins: bins},
	}
}
