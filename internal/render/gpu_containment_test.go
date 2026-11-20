//go:build !sheytan_nogpu

package render

// gpu_containment_test.go — GPU failure containment fixtures (run
// 43712071111 P0-A): repeated injected GPU failures must produce a
// COOLDOWN (CPU frames without new GPU work) and, after the bounded
// limit, a permanent downgrade for the renderer's lifetime (GPU
// resources released, no unbounded retry loop) — never an application
// exit, never an oscillating per-frame retry.

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/visualizer"
	"github.com/gogpu/wgpu"
)

// containmentRenderer builds a GPU renderer with an injectable GPU
// submission step (skips when no adapter/device can be created).
func containmentRenderer(t *testing.T) (*GPURenderer, bool) {
	t.Helper()
	g, _ := newGPURendererFull(PrefAuto)
	if g == nil || g.device == nil {
		t.Skip("no GPU adapter/device available in this environment (containment drill needs a creatable device)")
	}
	return g, true
}

func containmentInput() visualizer.FrameInput {
	bins := make([]float64, visualizer.NumVizBins)
	for i := range bins {
		bins[i] = 0.5
	}
	return visualizer.FrameInput{
		FrameIndex: 1, Time: 0.1, Width: 64, Height: 48,
		Config: visualizer.Config{Mode: visualizer.ModeRadial},
		Audio:  visualizer.AudioState{Bass: 0.4, Bins: bins},
	}
}

func TestGPUFailureCooldownKeepsCPUFrames(t *testing.T) {
	g, ok := containmentRenderer(t)
	if !ok {
		return
	}
	defer g.Close()
	calls := 0
	g.renderGPUFn = func(in visualizer.FrameInput) error {
		calls++
		return errors.New("synthetic device failure")
	}
	in := containmentInput()
	// First failure: fallback + cooldown window opens.
	img, err := g.RenderFrame(in)
	if err != nil || img == nil {
		t.Fatalf("first failure must fall back to a CPU frame: %v", err)
	}
	if g.info.GpuState != GpuFallback {
		t.Fatalf("gpu state = %s, want fallback", g.info.GpuState)
	}
	if g.fallbackCount != 1 {
		t.Fatalf("fallbackCount = %d, want 1", g.fallbackCount)
	}
	// Within the cooldown window the CPU path renders WITHOUT new GPU
	// submissions (no tight retry loop).
	before := calls
	for i := 0; i < 3; i++ {
		if _, err := g.RenderFrame(in); err != nil {
			t.Fatalf("cooldown frame: %v", err)
		}
	}
	if calls != before {
		t.Fatalf("cooldown must not submit new GPU work (calls %d -> %d)", before, calls)
	}
}

func TestGPUPermanentDowngradeAfterBoundedFailures(t *testing.T) {
	g, ok := containmentRenderer(t)
	if !ok {
		return
	}
	defer g.Close()
	submits := 0
	g.renderGPUFn = func(in visualizer.FrameInput) error {
		submits++
		return wgpu.ErrDeviceLost
	}
	_ = submits
	in := containmentInput()
	// Two failures recorded (cooldown window open, expired for the
	// drill), then the third releases the GPU permanently.
	g.fallbackCount = maxConsecutiveGPUFailures - 1
	g.cooldownUntil = time.Time{}
	if _, err := g.RenderFrame(in); err != nil {
		t.Fatalf("final failure must fall back to CPU: %v", err)
	}
	if g.device != nil {
		t.Fatal("GPU must be released after the bounded failure count")
	}
	if !strings.Contains(g.info.Detail, "GPU disabled after") {
		t.Fatalf("detail must name the permanent downgrade: %q", g.info.Detail)
	}
	// Every later frame routes to the CPU renderer with ZERO further
	// GPU submissions — no unbounded retry, no re-create loop.
	img, err := g.RenderFrame(in)
	if err != nil || img == nil {
		t.Fatalf("post-downgrade frame: %v", err)
	}
	if submits != 1 {
		t.Fatalf("GPU submissions = %d after post-downgrade frame, want exactly 1", submits)
	}
}

func TestGPUSuccessResetsFailureCounter(t *testing.T) {
	g, ok := containmentRenderer(t)
	if !ok {
		return
	}
	defer g.Close()
	fail := false
	in := containmentInput()
	// Alternate: inject one failure, then verify the counter resets on
	// a successful frame so transient errors never accumulate into a
	// false permanent downgrade.
	g.renderGPUFn = func(i visualizer.FrameInput) error {
		if fail {
			return errors.New("transient")
		}
		return g.renderGPU(i)
	}
	fail = true
	if _, err := g.RenderFrame(in); err != nil {
		t.Fatal(err)
	}
	if g.fallbackCount != 1 {
		t.Fatalf("fallbackCount = %d, want 1", g.fallbackCount)
	}
	fail = false
	g.cooldownUntil = time.Time{} // drill: cooldown expired
	g.fallbackCount = 1
	if _, err := g.RenderFrame(in); err != nil {
		t.Fatal(err)
	}
	if g.fallbackCount != 0 {
		t.Fatalf("fallbackCount = %d after success, want 0", g.fallbackCount)
	}
}
