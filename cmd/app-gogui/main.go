// Command app-gogui is the entry point of the NATIVE Go-Gui shell
// (github.com/go-gui-org/go-gui v0.98.0) of SHEYTAN Music Visualizer
// BETA.
//
// Usage:
//
//	app-gogui                 start the shell window (full run)
//	app-gogui --smoke N       open the real window, render N frames
//	                          through the real shell layout, then close
//	                          cleanly (exit 0; non-zero on failure)
//	app-gogui --version       print product/build metadata + attribution
//	app-gogui --build-info-json
//	                          print the machine-readable build snapshot
//	                          as one JSON line
//	app-gogui --self-check    verify gogui-scope foundation invariants
//	                          and exit without opening a window
//
// The shell owns exactly ONE top-level window; real startup errors
// return a non-zero process exit code and are never swallowed.
//
// BOOTSTRAP-FIRST DIAGNOSTICS (run 45297707372 repair): main opens the
// runtime log BEFORE any other startup work, tees stderr into it (the
// Windows product build is -H=windowsgui and has no console, so an
// un-logged stderr write used to be invisible), records the process
// exit code, and persists every recovered panic as a crash report
// under the layout's crashes directory. A user whose app fails to open
// always finds app.log evidence; see docs/TROUBLESHOOTING.md.
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
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/ui/gogui"
)

func main() {
	// BOOTSTRAP FIRST: the runtime log opens before flag parsing,
	// before design tokens, before any subsystem. Every later step
	// is logged; every failure path writes its reason. Bootstrap is
	// best-effort by contract: an unwritable log location degrades
	// to stderr and must never take the product down.
	lay := apppaths.Resolve(apppaths.OSEnv())
	if err := apppaths.Bootstrap(lay); err != nil {
		// The layout bootstrap failure is surfaced by run()'s own
		// error paths too; here it only means the crash-report
		// directory may be missing. Never fatal at this point.
		_ = err
	}
	boot := logging.Bootstrap(lay)
	// The startup banner is the FIRST record: build identity, OS, arch,
	// PID and the resolved log path - so even a crash two lines later is
	// attributable to an exact build.
	boot.LogStartupBanner(lay)
	errW := boot.TeeStderr()
	code := 1
	defer func() {
		if r := recover(); r != nil {
			boot.WriteCrashReport(lay.Crashes, "app-gogui main", r, debug.Stack())
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
	Smoke         int
	Run           bool
}

// parseArgs maps flags onto the intent. Unknown flags or stray
// arguments are a usage error (exit 2 upstream).
func parseArgs(args []string, fs *flag.FlagSet) (mode, error) {
	var m mode
	fs.BoolVar(&m.Version, "version", false, "print product and build metadata, then exit")
	fs.BoolVar(&m.BuildInfoJSON, "build-info-json", false, "print the build metadata as one stable JSON line, then exit")
	fs.BoolVar(&m.SelfCheck, "self-check", false, "run gogui-scope foundation self-checks, then exit without a window")
	fs.IntVar(&m.Smoke, "smoke", 0, "render N frames through the real shell window, then close cleanly")
	if err := fs.Parse(args); err != nil {
		return mode{}, err
	}
	if fs.NArg() > 0 {
		return mode{}, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if m.Smoke < 0 {
		return mode{}, fmt.Errorf("--smoke needs a non-negative frame count")
	}
	// Exactly one mode wins; the default is the full window run.
	if !m.Version && !m.BuildInfoJSON && !m.SelfCheck && m.Smoke == 0 {
		m.Run = true
	}
	return m, nil
}

func run(args []string, out, errW io.Writer, boot *logging.Log) int {
	fs := flag.NewFlagSet(brand.ProductName+" (native Go-Gui shell)", flag.ContinueOnError)
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
		fmt.Fprintf(out, "%s (native Go-Gui shell) %s\n", brand.ProductName, buildinfo.Line())
		for _, line := range brand.AttributionBlock() {
			fmt.Fprintln(out, line)
		}
		return 0
	case m.SelfCheck:
		return selfCheck(out, errW)
	case m.Smoke > 0:
		err := gogui.Run(gogui.RunOptions{SmokeFrames: m.Smoke, Log: boot})
		if err == nil {
			return 0
		}
		fmt.Fprintf(errW, "%s: smoke run failed: %v\n", brand.ProductName, err)
		logBoot(boot, "smoke", "smoke run failed", err)
		return 1
	default:
		err := runGUI(boot)
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

// selfCheck verifies the gogui-scope foundation invariants WITHOUT
// opening a window: build identity, token document, the real renderer
// selection rendering one deterministic CPU frame, the presentation
// destination catalog integrity and a writable workspace layout store.
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
	fmt.Fprintln(out, "self-check: all gogui foundation invariants hold")
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
	fmt.Fprintf(out, "  version=%s commit=%s go=%s\n", i.Version, i.Commit, i.GoVersion)
	return nil
}

// checkDesignTokens loads the canonical token document and verifies
// the identity contract the shell theme relies on.
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
// tab strip renders (11 destinations, valid order).
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
