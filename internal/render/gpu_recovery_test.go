package render

// gpu_recovery_test.go — device-loss RECOVERY drills (roadmap Phase 2
// #10: "GPU-device-loss recovery drills: NOT DONE"). A real device
// loss needs real hardware; what CAN be drilled anywhere is the
// recovery path: when the GPU submission fails with
// wgpu.ErrDeviceLost, the renderer must (a) report GpuState=Fallback
// with an explicit "device lost:" detail, and (b) still return a
// valid CPU-rendered frame — never an error, never a fake GPU state.
// The same drill covers a generic GPU failure (transient error class)
// to prove the two reasons stay distinguishable.

import (
	"errors"
	"fmt"
	"testing"

	"github.com/gogpu/wgpu"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/visualizer"
)

func recoveryTestRenderer(t *testing.T) *GPURenderer {
	t.Helper()
	g := newGPURendererFor(&wgpu.RequestAdapterOptions{ForceFallbackAdapter: true})
	if g == nil {
		t.Skip("SKIP (NOT AVAILABLE): noop fallback adapter unavailable in this build")
	}
	if g.device == nil {
		t.Skip("SKIP (NOT AVAILABLE): noop adapter produced no device; recovery path needs a live GPU renderer object")
	}
	return g
}

func recoveryTestInput() visualizer.FrameInput {
	return visualizer.FrameInput{
		FrameIndex: 1,
		Time:       1.0 / 30.0,
		Width:      64,
		Height:     64,
		Config: visualizer.Config{
			Mode: visualizer.ModeRadial, Sensitivity: 1, Smoothing: 0.3,
			Glow: 0.5, Palette: 0, PulseResponse: 1,
		},
		Audio: visualizer.AudioState{Bass: 0.5, Mid: 0.4, Treble: 0.2, Bins: testBins()},
	}
}

func TestDeviceLossRecoveryDrill(t *testing.T) {
	g := recoveryTestRenderer(t)
	defer g.Close()

	// Inject a device loss into the submission step.
	g.renderGPUFn = func(in visualizer.FrameInput) error {
		return fmt.Errorf("wgpu: submit failed: %w", wgpu.ErrDeviceLost)
	}

	img, err := g.RenderFrame(recoveryTestInput())
	if err != nil {
		t.Fatalf("recovered render must not error, got: %v", err)
	}
	if img == nil {
		t.Fatal("recovered render must return the CPU frame")
	}
	if img == nil || img.Bounds().Dx() != 64 {
		t.Fatal("recovered render must return a valid 64px frame")
	}
	info := g.Info()
	if info.GpuState != GpuFallback {
		t.Fatalf("GpuState = %s, want FALLBACK after device loss", info.GpuState)
	}
	if len(info.Detail) < len("device lost: ") || info.Detail[:len("device lost: ")] != "device lost: " {
		t.Fatalf("Detail = %q, want the explicit device-lost classification", info.Detail)
	}
}

func TestGenericGPUFailureStaysDistinguishable(t *testing.T) {
	g := recoveryTestRenderer(t)
	defer g.Close()

	g.renderGPUFn = func(in visualizer.FrameInput) error {
		return fmt.Errorf("wgpu: map readback failed (transient)")
	}

	img, err := g.RenderFrame(recoveryTestInput())
	if err != nil {
		t.Fatalf("recovered render must not error, got: %v", err)
	}
	if img == nil {
		t.Fatal("recovered render must return the CPU frame")
	}
	info := g.Info()
	if info.GpuState != GpuFallback {
		t.Fatalf("GpuState = %s, want FALLBACK after GPU failure", info.GpuState)
	}
	if info.Detail == "" || info.Detail[:len("device lost: ")] == "device lost: " {
		t.Fatalf("a transient failure must NOT be classified as device loss (Detail=%q)", info.Detail)
	}
}

// Sanity: the injected sentinel really IS wgpu.ErrDeviceLost via
// errors.Is, so the production classification and the drill share one
// definition.
func TestDeviceLossSentinelIsWgpuErrDeviceLost(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", wgpu.ErrDeviceLost)
	if !errors.Is(err, wgpu.ErrDeviceLost) {
		t.Fatal("sentinel must satisfy errors.Is with itself")
	}
}
