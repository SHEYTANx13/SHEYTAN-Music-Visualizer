package sysprofile

import (
	"strings"
	"sync"
	"testing"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/render"
)

func activeCPUBackend() render.BackendInfo {
	return render.BackendInfo{Backend: render.BackendCPU, Detail: "test backend"}
}

// CollectWith normalizes zero Export/Display sections into explicit
// Unknown-with-reason facts — never blank output.
func TestCollectWithZeroSectionsNormalizesUnknowns(t *testing.T) {
	p := CollectWith(activeCPUBackend(), ExportInfo{}, DisplayInfo{})
	if p.Export.FfmpegPath.State != StateUnknown || p.Export.FfmpegPath.Reason == "" {
		t.Fatalf("ffmpeg fact not normalized: %+v", p.Export.FfmpegPath)
	}
	if p.Export.BuiltInDecoders.State != StateUnknown {
		t.Fatalf("decoder fact not normalized: %+v", p.Export.BuiltInDecoders)
	}
	if p.Display.Resolution.State != StateUnknown || p.Display.Resolution.Reason == "" {
		t.Fatalf("resolution fact not normalized: %+v", p.Display.Resolution)
	}
	if p.Display.Refresh.State != StateUnknown {
		t.Fatalf("refresh fact not normalized: %+v", p.Display.Refresh)
	}
	// Collect() keeps its old shape and behavior.
	p2 := Collect(activeCPUBackend())
	if p2.Export.FfmpegPath.State != StateUnknown {
		t.Fatalf("Collect export default changed: %+v", p2.Export.FfmpegPath)
	}
}

// CollectDisplay reports only what was supplied; refresh stays Unknown
// everywhere (the pinned framework does not expose it).
func TestCollectDisplay(t *testing.T) {
	d := CollectDisplay(1440, 860, 1.5)
	if d.Resolution.State != StateDetected || d.Resolution.Value == "" {
		t.Fatalf("resolution not detected: %+v", d.Resolution)
	}
	if d.Scale.State != StateDetected {
		t.Fatalf("scale not detected: %+v", d.Scale)
	}
	if d.Refresh.State != StateUnknown {
		t.Fatalf("refresh must stay unknown: %+v", d.Refresh)
	}
	d2 := CollectDisplay(0, 0, 0)
	if d2.Resolution.State != StateUnknown || d2.Scale.State != StateUnknown {
		t.Fatalf("zero inputs must stay unknown: %+v %+v", d2.Resolution, d2.Scale)
	}
}

// ModePolicyFor: every mode is bounded by the scheduler baseline, every
// mode reports its provenance, and battery-efficient keeps 1080p.
func TestModePolicyBounded(t *testing.T) {
	p := Profile{} // no detected memory: budgets must keep shipped defaults
	for _, m := range ModeChoices {
		pm, err := ParsePerformanceMode(m)
		if err != nil || pm != PerformanceMode(m) {
			t.Fatalf("mode %q did not round-trip: %v %v", m, pm, err)
		}
		pol := ModePolicyFor(pm, p)
		base := gomaxprocsCurrent()
		if pol.Workers < 1 || pol.Workers > base {
			t.Fatalf("mode %q workers %d outside baseline %d", m, pol.Workers, base)
		}
		if pol.FrameQueueDepth < 1 {
			t.Fatalf("mode %q frame queue unbounded/empty", m)
		}
		if pol.Source == "" {
			t.Fatalf("mode %q has no provenance", m)
		}
		if !pol.GpuAllowed {
			t.Fatalf("mode %q disallowed GPU without a reason contract", m)
		}
	}
	bat := ModePolicyFor(ModeBatteryEfficient, p)
	if bat.MaxExportHeight != 1080 {
		t.Fatalf("battery mode must cap at 1080p, got %d", bat.MaxExportHeight)
	}
	if bat.Workers > 2 {
		t.Fatalf("battery mode must lower workers, got %d", bat.Workers)
	}
}

// Memory-aware refinement only raises the cap when memory was truly
// detected — unknown memory never invents a budget.
func TestModePolicyMemoryRefinement(t *testing.T) {
	p := Profile{}
	p.Memory.AvailableBytes = Quantity{Value: 16 * gib, State: StateDetected, Source: "test"}
	pol := ModePolicyFor(ModeAuto, p)
	if pol.MaxExportHeight != 2160 {
		t.Fatalf("16 GiB should allow 2160p guidance, got %d", pol.MaxExportHeight)
	}
	if !strings.Contains(pol.Source, "GiB") {
		t.Fatalf("memory provenance missing in source: %q", pol.Source)
	}
	p2 := Profile{}
	p2.Memory.AvailableBytes = Quantity{State: StateUnknown, Reason: "n/a"}
	pol2 := ModePolicyFor(ModeAuto, p2)
	if pol2.MaxExportHeight != 1080 {
		t.Fatalf("unknown memory must keep 1080p, got %d", pol2.MaxExportHeight)
	}
}

// ParsePerformanceMode rejects garbage and normalizes whitespace/case.
func TestParsePerformanceMode(t *testing.T) {
	if m, _ := ParsePerformanceMode(""); m != ModeAuto {
		t.Fatalf("empty must be auto, got %q", m)
	}
	if m, _ := ParsePerformanceMode("  HIGH-PERFORMANCE "); m != ModeHighPerformance {
		t.Fatalf("case/space normalization failed, got %q", m)
	}
	if _, err := ParsePerformanceMode("turbo-boost"); err == nil {
		t.Fatal("garbage mode accepted")
	}
}

// Concurrent ModePolicyFor must be race-free (run under -race in CI).
func TestModePolicyConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = ModePolicyFor(ModeAuto, Profile{})
		}()
	}
	wg.Wait()
}
