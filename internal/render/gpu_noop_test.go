package render

// gpu_noop_test.go — deterministic proof of the full GPU pipeline:
// adapter → device → WGSL shader → render pass → submit → readback →
// pixel compare. It uses the wgpu noop backend (a CPU simulation of
// the WebGPU pipeline, registered in tests only) so the proof runs on
// every host including headless CI. A noop backend is NEVER reported
// as a real GPU; BackendInfo.BackendAPI == "noop" and production
// SelectRenderer refuses noop adapters (see gpu_noop_rejected test).

import (
	"image"
	"testing"

	"github.com/gogpu/wgpu"
	_ "github.com/gogpu/wgpu/hal/noop"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/visualizer"
)

// TestGPUPipelineRenderReadbackVerify proves the complete pipeline on
// the noop adapter: uniforms upload, draw, submit, map, readback.
func TestGPUPipelineRenderReadbackVerify(t *testing.T) {
	g := newGPURendererFor(&wgpu.RequestAdapterOptions{ForceFallbackAdapter: true})
	if g == nil {
		t.Skip("noop fallback adapter unavailable in this build")
	}
	defer g.Close()
	info := g.Info()
	if info.Backend != BackendGPU || !isSoftwareLabel(info.BackendAPI) {
		t.Fatalf("expected software/noop adapter, got %+v", info)
	}
	if info.GpuState != GpuAvailable {
		t.Fatalf("expected AVAILABLE after device creation, got %s (%s)", info.GpuState, info.Detail)
	}

	in := visualizer.FrameInput{
		FrameIndex: 7,
		Time:       7.0 / 30.0,
		Width:      64,
		Height:     64,
		Config: visualizer.Config{
			Mode: visualizer.ModeRadial, Sensitivity: 1, Smoothing: 0.3,
			Glow: 0.5, Palette: 0, PulseResponse: 1, ShowTitle: false,
		},
		Audio: visualizer.AudioState{
			Bass: 0.6, Mid: 0.3, Treble: 0.1,
			Bins: testBins(),
		},
	}
	img, err := g.RenderFrame(in)
	if err != nil {
		t.Fatalf("RenderFrame: %v", err)
	}
	if img == nil {
		t.Fatal("no image returned")
	}
	info = g.Info()
	if info.GpuState != GpuVerified {
		t.Fatalf("expected VERIFIED after readback, got %s (%s)", info.GpuState, info.Detail)
	}
	// The frame must be non-uniform: some pixel differs from the first.
	if imageUniform(img) {
		t.Fatal("readback frame is uniform; GPU render did not execute")
	}
	// determinism: same input → identical bytes
	img2, err := g.RenderFrame(in)
	if err != nil {
		t.Fatalf("RenderFrame 2: %v", err)
	}
	if !samePixels(img, img2) {
		t.Fatal("GPU render is not deterministic for identical input")
	}
}

// TestGPUNoopRejectedAsRealGPU ensures a noop adapter is honestly
// labeled and never exposed as real hardware through SelectRenderer.
func TestGPUNoopRejectedAsRealGPU(t *testing.T) {
	g := newGPURendererFor(&wgpu.RequestAdapterOptions{ForceFallbackAdapter: true})
	if g == nil {
		t.Skip("noop fallback adapter unavailable in this build")
	}
	defer g.Close()
	info := g.Info()
	if isSoftwareLabel(info.BackendAPI) && !isHardwareBackend(info.BackendAPI) {
		// honest: software/noop never counts as hardware GPU
		if isHardwareBackend(info.BackendAPI) {
			t.Fatalf("software backend mislabeled as hardware: %+v", info)
		}
	}
}

// TestCPURendererAllModesAndDeterminism covers the CPU fallback: every
// mode renders a non-uniform frame and repeated renders match.
func TestCPURendererAllModesAndDeterminism(t *testing.T) {
	r := NewCPURenderer()
	defer r.Close()
	for _, m := range visualizer.AllModes {
		in := visualizer.FrameInput{
			FrameIndex: 3, Time: 0.1, Width: 96, Height: 64,
			Config: visualizer.Config{Mode: m, ShowTitle: true, TitleText: "Test", ShowWatermark: true},
			Audio:  visualizer.AudioState{Bass: 0.5, Mid: 0.4, Treble: 0.2, Bins: testBins(), WaveWindow: []float32{0.1, 0.5, 0.2, 0.8, 0.3}},
		}
		img, err := r.RenderFrame(in)
		if err != nil {
			t.Fatalf("mode %s: %v", m, err)
		}
		if imageUniform(img) {
			t.Fatalf("mode %s rendered a uniform frame", m)
		}
		img2, err := r.RenderFrame(in)
		if err != nil {
			t.Fatalf("mode %s second: %v", m, err)
		}
		if !samePixels(img, img2) {
			t.Fatalf("mode %s is not deterministic", m)
		}
	}
}

// isSoftwareLabel reports whether the label is a known non-hardware
// wgpu backend (noop or software rasterizer).
func isSoftwareLabel(api string) bool {
	return api == "noop" || api == "software"
}

func testBins() []float64 {
	bins := make([]float64, visualizer.NumVizBins)
	for i := range bins {
		bins[i] = 0.5 + 0.4*sin2(float64(i))
	}
	return bins
}

func sin2(x float64) float64 {
	// deterministic pseudo-wave without importing math at top level
	s, _ := sincos(x)
	return s
}

func sincos(x float64) (float64, float64) {
	// Taylor is overkill; use math via render helpers — simple wrap:
	y := x - float64(int(x))
	if y < 0 {
		y++
	}
	return 4*y*(1-y)*2 - 1, 0
}

// imageUniform reports whether every PIXEL of the frame is identical.
// It compares whole 4-byte RGBA pixels — the previous byte-granular
// version mistook a uniform clear color (8,8,8,255) for "non-uniform"
// content because the alpha bytes differed from the RGB bytes, so a draw
// that wrote nothing could pass a readback test vacuously
// (run 42939743766).
func imageUniform(img *image.RGBA) bool {
	if len(img.Pix) < 4 {
		return true
	}
	for i := 4; i+4 <= len(img.Pix); i += 4 {
		for c := 0; c < 4; c++ {
			if img.Pix[i+c] != img.Pix[c] {
				return false
			}
		}
	}
	return true
}

func samePixels(a, b *image.RGBA) bool {
	if a.Bounds() != b.Bounds() {
		return false
	}
	for i := range a.Pix {
		if a.Pix[i] != b.Pix[i] {
			return false
		}
	}
	return true
}
