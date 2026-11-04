// Command sheytan-music-visualizer is the desktop application entry
// point for SHEYTAN Music Visualizer.
//
// Usage:
//
//	sheytan-music-visualizer              start the studio window
//	sheytan-music-visualizer --version    print product/build metadata
//	sheytan-music-visualizer --self-check verify foundation invariants
//	                                      and exit without a window
//
// Real startup errors return a non-zero process exit code; errors are
// never swallowed.
package main

import (
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"strings"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/audio"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/brand"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/buildinfo"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/dsp"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/export"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/logging"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/render"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/visualizer"
	fyneapp "github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/ui/fyne"
)

var hexColor = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, out, errW io.Writer) int {
	fs := flag.NewFlagSet(brand.ProductName, flag.ContinueOnError)
	fs.SetOutput(errW)
	showVersion := fs.Bool("version", false, "print product and build metadata, then exit")
	selfCheckFlag := fs.Bool("self-check", false, "run foundation self-checks, then exit without opening a window")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(errW, "%s: unexpected arguments: %s\n", brand.ProductName, strings.Join(fs.Args(), " "))
		return 2
	}
	switch {
	case *showVersion:
		fmt.Fprintf(out, "%s %s\n", brand.ProductName, buildinfo.Line())
		fmt.Fprintf(out, "%s\n%s\n", brand.Copyright, brand.SourceURL)
		return 0
	case *selfCheckFlag:
		return selfCheck(out, errW)
	default:
		if err := fyneapp.Run(); err != nil {
			fmt.Fprintf(errW, "%s: startup failed: %v\n", brand.ProductName, err)
			return 1
		}
		return 0
	}
}

type check struct {
	name string
	fn   func(io.Writer) error
}

func selfCheck(out, errW io.Writer) int {
	checks := []check{
		{"buildinfo", checkBuildInfo},
		{"brand-identity", checkBrandIdentity},
		{"log-directory", checkLogDirectory},
		{"decoder-registry", checkDecoderRegistry},
		{"dsp-fixture", checkDSPFixture},
		{"visualizer-config", checkVisualizerConfig},
		{"renderer-select", checkRendererSelect},
		{"export-env", checkExportEnv},
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

func checkBrandIdentity(out io.Writer) error {
	if brand.ProductName == "" || brand.AppID == "" || brand.ModulePath == "" {
		return fmt.Errorf("core identity constants must be non-empty")
	}
	if strings.ContainsAny(brand.ProductName, "\n\r\t") {
		return fmt.Errorf("product name must be single-line")
	}
	for token, value := range brand.Palette() {
		if !hexColor.MatchString(value) {
			return fmt.Errorf("palette token %s = %q is not #RRGGBB", token, value)
		}
	}
	return nil
}

func checkLogDirectory(out io.Writer) error {
	dir, err := logging.Dir()
	if err != nil {
		return fmt.Errorf("resolve log directory: %w", err)
	}
	if err := logging.VerifyWritable(dir); err != nil {
		return fmt.Errorf("log directory not writable: %w", err)
	}
	l, err := logging.Open(dir, logging.Options{})
	if err != nil {
		return fmt.Errorf("open application log: %w", err)
	}
	defer l.Close()
	l.Logger().Info("self-check executed", "component", "cmd/sheytan-music-visualizer")
	fmt.Fprintf(out, "  log=%s\n", l.Path())
	return nil
}

// checkDecoderRegistry verifies the four native decoders register and
// the registry reports them from implementations (not a hard-coded list).
func checkDecoderRegistry(out io.Writer) error {
	audio.RegisterAll()
	fs := audio.SupportedFormats()
	got := map[string]bool{}
	for _, f := range fs {
		got[f.Container] = true
	}
	for _, want := range []string{"wav", "mp3", "flac", "ogg"} {
		if !got[want] {
			return fmt.Errorf("decoder %s not registered", want)
		}
	}
	fmt.Fprintf(out, "  decoders=%d (wav, mp3, flac, ogg)\n", len(fs))
	return nil
}

// checkDSPFixture runs a tiny synthesized sine through the analyzer
// and verifies the measured band energy lands in the bass band.
func checkDSPFixture(out io.Writer) error {
	a := dsp.NewAnalyzer()
	// 0.5 s of 80 Hz sine at 48 kHz stereo
	n := dsp.SampleRate / 2
	samples := make([]float32, n*2)
	for i := 0; i < n; i++ {
		v := float32(0.6 * math.Sin(2*math.Pi*80*float64(i)/dsp.SampleRate))
		samples[2*i], samples[2*i+1] = v, v
	}
	a.Process(samples)
	res := a.Finish()
	if len(res.Frames) == 0 {
		return fmt.Errorf("analyzer produced no frames")
	}
	if res.BassEnergy < 0.5 {
		return fmt.Errorf("80 Hz sine should put most energy in bass, got %.2f", res.BassEnergy)
	}
	fmt.Fprintf(out, "  fixture: bass=%.2f mid=%.2f treble=%.2f rms=%.3f\n",
		res.BassEnergy, res.MidEnergy, res.TrebleEnergy, res.MeanRMS)
	return nil
}

// checkVisualizerConfig verifies clamping and the preset set.
func checkVisualizerConfig(out io.Writer) error {
	c := visualizer.Config{Mode: "bogus", Sensitivity: 99, Palette: 42}
	c.Clamp()
	if c.Mode != visualizer.ModeBars || c.Sensitivity != 3 || c.Palette != 0 {
		return fmt.Errorf("config clamping is broken: %+v", c)
	}
	if len(visualizer.Presets()) < 3 {
		return fmt.Errorf("expected at least 3 presets")
	}
	fmt.Fprintf(out, "  modes=%d presets=%d palettes=%d\n",
		len(visualizer.AllModes), len(visualizer.Presets()), len(visualizer.Palettes))
	return nil
}

// checkRendererSelect verifies an honest renderer can be selected and
// renders one deterministic frame (CPU renderer always works).
func checkRendererSelect(out io.Writer) error {
	r, info := render.SelectRenderer()
	defer r.Close()
	if info.Backend != render.BackendCPU && info.Backend != render.BackendGPU {
		return fmt.Errorf("unexpected backend %q", info.Backend)
	}
	in := sampleFrame(64, 48)
	img, err := r.RenderFrame(in)
	if err != nil {
		return fmt.Errorf("render frame: %w", err)
	}
	if img == nil || img.Bounds().Empty() {
		return fmt.Errorf("empty frame")
	}
	fmt.Fprintf(out, "  backend=%s gpu=%s api=%s adapter=%s\n",
		info.Backend, info.GpuState, info.BackendAPI, info.Adapter)
	return nil
}

// checkExportEnv reports the local FFmpeg environment honestly
// (informational only — a missing FFmpeg is not a failure of the
// application foundation).
func checkExportEnv(out io.Writer) error {
	env, err := export.DetectEnv(false)
	if err != nil {
		fmt.Fprintf(out, "  ffmpeg: NOT AVAILABLE (%v)\n", err)
		return nil
	}
	fmt.Fprintf(out, "  ffmpeg=%s encoders=%v\n", env.Version, env.EncoderList(false))
	return nil
}

func sampleFrame(w, h int) visualizer.FrameInput {
	bins := make([]float64, visualizer.NumVizBins)
	for i := range bins {
		bins[i] = 0.4 + 0.3*math.Sin(float64(i)/7)
	}
	return visualizer.FrameInput{
		FrameIndex: 1, Time: 1.0 / 30, Width: w, Height: h,
		Config: visualizer.Config{Mode: visualizer.ModeBars},
		Audio:  visualizer.AudioState{Bass: 0.5, Mid: 0.3, Treble: 0.2, Bins: bins},
	}
}
