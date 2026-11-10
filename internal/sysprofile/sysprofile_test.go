// sysprofile_test.go — deterministic tests for the system profile:
// fake platform sources prove the state machine (unknown/detected/
// failed are reported honestly), the worker policy respects the
// scheduler baseline, the memory budget derives honestly, and the
// render decision never conflates selected/executing/verified. The
// real-collector test runs only where the native interfaces exist.
package sysprofile

import (
	"strings"
	"testing"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/render"
)

// swapHooks installs deterministic fake platform sources and returns a
// restore function. The swap itself is the only critical section — the
// hooks stay swapped for the duration of the test without holding the
// mutex (holding it would deadlock Collect).
func swapHooks(t *testing.T, cpuModel, memory, osVersion, physicalCores, power any) func() {
	t.Helper()
	hookMu.Lock()
	origCPU, origMem, origOS, origCores, origPower := hookCPUModel, hookMemory, hookOSVersion, hookPhysicalCores, hookPower
	if f, ok := cpuModel.(func() (string, string, error)); ok {
		hookCPUModel = f
	}
	if f, ok := memory.(func() (int64, int64, string, error)); ok {
		hookMemory = f
	}
	if f, ok := osVersion.(func() (string, string, error)); ok {
		hookOSVersion = f
	}
	if f, ok := physicalCores.(func() (string, string, error)); ok {
		hookPhysicalCores = f
	}
	if f, ok := power.(func() (string, string, error)); ok {
		hookPower = f
	}
	hookMu.Unlock()
	return func() {
		hookMu.Lock()
		hookCPUModel, hookMemory, hookOSVersion, hookPhysicalCores, hookPower = origCPU, origMem, origOS, origCores, origPower
		hookMu.Unlock()
	}
}

func cpuRenderInfo() render.BackendInfo {
	c := render.NewCPURenderer()
	defer c.Close()
	return c.Info()
}

// TestCollectUnknownFieldsAreHonest pins the contract that missing
// platform sources produce UNKNOWN facts WITH a reason — never empty
// states, never guessed values.
func TestCollectUnknownFieldsAreHonest(t *testing.T) {
	// Explicit EMPTY fakes (not nil): nil leaves the real platform
	// hooks in place, which would make this test machine-dependent.
	defer swapHooks(t,
		func() (string, string, error) { return "", "", nil },
		func() (int64, int64, string, error) { return 0, 0, "", nil },
		func() (string, string, error) { return "", "", nil },
		func() (string, string, error) { return "", "", nil },
		func() (string, string, error) { return "", "", nil },
	)()
	p := Collect(cpuRenderInfo())

	for name, f := range map[string]Fact{
		"os.version":        p.OS.Version,
		"cpu.model":         p.CPU.Model,
		"cpu.physicalCores": p.CPU.PhysicalCores,
		"audio.output":      p.Audio.DefaultOutput,
		"power.state":       p.Power.State,
	} {
		if f.State != StateUnknown {
			t.Errorf("%s state = %q, want unknown", name, f.State)
		}
		if strings.TrimSpace(f.Reason) == "" {
			t.Errorf("%s is unknown without a reason (dishonest)", name)
		}
		if f.Value != "" {
			t.Errorf("%s carries a value %q while unknown (fabricated)", name, f.Value)
		}
	}
	// Always-known facts stay detected.
	if p.OS.Family.State != StateDetected || p.OS.Family.Value == "" {
		t.Errorf("os.family = %+v, want detected with a value", p.OS.Family)
	}
	if p.CPU.LogicalCPUs.State != StateDetected || p.CPU.LogicalCPUs.Value <= 0 {
		t.Errorf("cpu.logicalCPUs = %+v, want detected > 0", p.CPU.LogicalCPUs)
	}
	if p.CPU.EffectiveParallelism.State != StateDetected || p.CPU.EffectiveParallelism.Value <= 0 {
		t.Errorf("cpu.effectiveParallelism = %+v, want detected > 0", p.CPU.EffectiveParallelism)
	}
	if p.CPU.EffectiveParallelism.Value != int64(runtime_GOMAXPROCS()) {
		t.Errorf("effectiveParallelism must mirror runtime.GOMAXPROCS(0)")
	}
}

// TestCollectDetectedFactsWithFakeSources proves the detected path
// carries value+source together (provenance is part of the contract).
func TestCollectDetectedFactsWithFakeSources(t *testing.T) {
	defer swapHooks(t,
		func() (string, string, error) { return "Fake CPU Model 9000", "fake-cpuinfo", nil },
		func() (int64, int64, string, error) { return 32 << 30, 20 << 30, "fake-meminfo", nil },
		func() (string, string, error) { return "TestOS 1.0", "fake-os", nil },
		func() (string, string, error) { return "8", "fake-topology", nil },
		func() (string, string, error) { return "ac", "fake-power", nil },
	)()
	p := Collect(cpuRenderInfo())

	if p.CPU.Model.Value != "Fake CPU Model 9000" || p.CPU.Model.Source != "fake-cpuinfo" || p.CPU.Model.State != StateDetected {
		t.Errorf("cpu.model = %+v", p.CPU.Model)
	}
	if p.Memory.TotalBytes.Value != 32<<30 || p.Memory.AvailableBytes.Value != 20<<30 ||
		p.Memory.TotalBytes.Source != "fake-meminfo" {
		t.Errorf("memory = %+v", p.Memory)
	}
	if p.OS.Version.Value != "TestOS 1.0" || p.OS.Version.State != StateDetected {
		t.Errorf("os.version = %+v", p.OS.Version)
	}
	if p.CPU.PhysicalCores.Value != "8" {
		t.Errorf("cpu.physicalCores = %+v", p.CPU.PhysicalCores)
	}
	if p.Power.State.Value != "ac" || p.Power.State.State != StateDetected {
		t.Errorf("power.state = %+v", p.Power.State)
	}
}

// TestCollectMemoryFailureIsFailedWithReason proves a failed read is
// reported FAILED (not unknown, not zero) with the exact error.
func TestCollectMemoryFailureIsFailedWithReason(t *testing.T) {
	defer swapHooks(t, nil,
		func() (int64, int64, string, error) { return 0, 0, "", errFakeMemory },
		nil, nil, nil)()
	p := Collect(cpuRenderInfo())
	if p.Memory.TotalBytes.State != StateFailed || !strings.Contains(p.Memory.TotalBytes.Reason, "offline") {
		t.Errorf("total = %+v, want failed with the exact reason", p.Memory.TotalBytes)
	}
	if p.Memory.AvailableBytes.State != StateFailed {
		t.Errorf("available = %+v, want failed", p.Memory.AvailableBytes)
	}
}

var errFakeMemory = &errString{"fake memory controller offline"}

type errString struct{ s string }

func (e *errString) Error() string { return e.s }

// TestDecideRenderNeverConflatesStates pins the honesty rules: a
// verified hardware GPU is "GPU verified"; an unverified GPU is
// "pending"; a CPU fallback carries the exact reason; a verified
// software adapter is never labeled physical acceleration.
func TestDecideRenderNeverConflatesStates(t *testing.T) {
	cases := []struct {
		name    string
		info    render.BackendInfo
		wantSub []string
		noSub   []string
	}{
		{
			name: "verified hardware gpu",
			info: render.BackendInfo{Backend: render.BackendGPU, GpuState: render.GpuVerified,
				Adapter: "TestGPU", BackendAPI: "vulkan", DeviceClass: render.DeviceClassHardware},
			wantSub: []string{"GPU verified", "TestGPU"},
			noSub:   []string{"NOT proven"},
		},
		{
			name: "gpu selected not yet verified",
			info: render.BackendInfo{Backend: render.BackendGPU, GpuState: render.GpuSelected,
				Adapter: "TestGPU", BackendAPI: "vulkan", DeviceClass: render.DeviceClassHardware},
			wantSub: []string{"verification pending", "TestGPU"},
			noSub:   []string{"verified"},
		},
		{
			name: "cpu fallback with reason",
			info: render.BackendInfo{Backend: render.BackendCPU, GpuState: render.GpuFallback,
				Detail: "GPU fallback: adapter refused"},
			wantSub: []string{"CPU fallback", "adapter refused"},
		},
		{
			name: "verified software adapter is not hardware",
			info: render.BackendInfo{Backend: render.BackendGPU, GpuState: render.GpuVerified,
				Adapter: "llvmpipe", BackendAPI: "software", DeviceClass: render.DeviceClassSoftware},
			wantSub: []string{"NOT proven physical"},
		},
		{
			name:    "bare cpu renderer",
			info:    render.BackendInfo{Backend: render.BackendCPU, GpuState: render.GpuNone},
			wantSub: []string{"CPU renderer active"},
		},
	}
	for _, tc := range cases {
		sec := decideRender(tc.info)
		for _, w := range tc.wantSub {
			if !strings.Contains(sec.Decision, w) {
				t.Errorf("%s: decision %q must contain %q", tc.name, sec.Decision, w)
			}
		}
		for _, n := range tc.noSub {
			if strings.Contains(sec.Decision, n) {
				t.Errorf("%s: decision %q must NOT contain %q", tc.name, sec.Decision, n)
			}
		}
	}
	sec := decideRender(render.BackendInfo{Backend: render.BackendGPU, GpuState: render.GpuVerified,
		Adapter: "TestGPU", BackendAPI: "vulkan", DeviceClass: render.DeviceClassHardware})
	if !sec.Verified {
		t.Error("verified hardware GPU must set RenderSection.Verified")
	}
	sec = decideRender(render.BackendInfo{Backend: render.BackendGPU, GpuState: render.GpuSelected,
		Adapter: "TestGPU", DeviceClass: render.DeviceClassHardware})
	if sec.Verified {
		t.Error("a merely-selected GPU must never set Verified")
	}
}

// TestWorkerPolicyRespectsSchedulerBaseline pins the auto-tuning
// contract: the default mirrors runtime.GOMAXPROCS(0) (never NumCPU
// blindly), a user override is honored within [1, baseline], invalid
// values are ignored with the reason, and nothing here mutates the
// scheduler.
func TestWorkerPolicyRespectsSchedulerBaseline(t *testing.T) {
	base := runtime_GOMAXPROCS()

	pol := WorkerPolicyFor()
	if pol.Effective != base || pol.Workers != base {
		t.Fatalf("default policy = %+v, want effective=workers=%d", pol, base)
	}
	if !strings.Contains(pol.Source, "GOMAXPROCS") {
		t.Errorf("default source must name the scheduler baseline: %q", pol.Source)
	}

	t.Setenv("SHEYTAN_MAX_WORKERS", "1")
	pol = WorkerPolicyFor()
	if pol.Workers != 1 || !strings.Contains(pol.Source, "user-configured") {
		t.Fatalf("override 1 → %+v", pol)
	}

	t.Setenv("SHEYTAN_MAX_WORKERS", "0")
	pol = WorkerPolicyFor()
	if pol.Workers != base || !strings.Contains(pol.Source, "must be >= 1") {
		t.Fatalf("invalid 0 → %+v", pol)
	}

	t.Setenv("SHEYTAN_MAX_WORKERS", "abc")
	pol = WorkerPolicyFor()
	if pol.Workers != base || !strings.Contains(pol.Source, "not a number") {
		t.Fatalf("invalid abc → %+v", pol)
	}

	t.Setenv("SHEYTAN_MAX_WORKERS", "99999")
	pol = WorkerPolicyFor()
	if pol.Workers != base || !strings.Contains(pol.Source, "clamped") {
		t.Fatalf("oversized → %+v (must clamp to the scheduler baseline %d)", pol, base)
	}

	if got := runtime_GOMAXPROCS(); got != base {
		t.Fatalf("WorkerPolicy mutated the scheduler: %d → %d", base, got)
	}
}

// TestMemoryBudgetDerivesHonestDefaults pins the memory-aware preset
// mapping and the unknown-memory fallback to the shipped default.
func TestMemoryBudgetDerivesHonestDefaults(t *testing.T) {
	mk := func(avail int64, state State) Profile {
		p := Collect(cpuRenderInfo())
		p.Memory.AvailableBytes = Quantity{Value: avail, State: state, Source: "test"}
		return p
	}
	if b := MemoryBudgetFor(mk(16<<30, StateDetected)); b.RecommendedExportHeight != 2160 {
		t.Errorf("16 GiB → height %d, want 2160", b.RecommendedExportHeight)
	}
	if b := MemoryBudgetFor(mk(9<<30, StateDetected)); b.RecommendedExportHeight != 1440 {
		t.Errorf("9 GiB → height %d, want 1440", b.RecommendedExportHeight)
	}
	if b := MemoryBudgetFor(mk(2<<30, StateDetected)); b.RecommendedExportHeight != 1080 || !strings.Contains(b.Reason, "1080p") {
		t.Errorf("2 GiB → %+v", b)
	}
	if b := MemoryBudgetFor(mk(0, StateUnknown)); b.RecommendedExportHeight != 1080 || !strings.Contains(b.Reason, "unknown") {
		t.Errorf("unknown memory must keep the 1080p default with an explanation: %+v", b)
	}
}

// TestRealCollectOnThisMachine runs the REAL collectors where they
// exist and asserts every fact is detected (value+source) or unknown
// with a reason — never empty, never fabricated.
func TestRealCollectOnThisMachine(t *testing.T) {
	p := Collect(cpuRenderInfo())
	checks := map[string]Fact{
		"os.family":  p.OS.Family,
		"os.arch":    p.OS.Arch,
		"cpu.model":  p.CPU.Model,
		"cpu.feats":  p.CPU.Features,
		"mem note":   {State: p.Memory.TotalBytes.State, Reason: p.Memory.TotalBytes.Reason, Source: p.Memory.TotalBytes.Source},
		"audio.out":  p.Audio.DefaultOutput,
		"power":      p.Power.State,
		"os.version": p.OS.Version,
	}
	for name, f := range checks {
		switch f.State {
		case StateDetected:
			if strings.TrimSpace(f.Value) == "" && name != "mem note" {
				t.Errorf("%s detected with empty value", name)
			}
			if strings.TrimSpace(f.Source) == "" {
				t.Errorf("%s detected without provenance source", name)
			}
		case StateUnknown, StateFailed:
			if strings.TrimSpace(f.Reason) == "" {
				t.Errorf("%s is %s without a reason", name, f.State)
			}
		default:
			t.Errorf("%s carries invalid state %q", name, f.State)
		}
	}
	if p.Memory.TotalBytes.State != StateDetected || p.Memory.TotalBytes.Value <= 0 {
		t.Errorf("this machine must report real physical memory: %+v", p.Memory.TotalBytes)
	}
}

// TestProbeRendererIsHonest verifies the probe contract: it either
// returns a verified state from a real render, or an exact reason. It
// never panics and never fabricates a verified flag.
func TestProbeRendererIsHonest(t *testing.T) {
	info, verified := ProbeRenderer(render.PrefAuto)
	if verified && info.GpuState != render.GpuVerified {
		t.Fatalf("verified=true must mean GpuState=verified, got %+v", info)
	}
	if !verified && info.Detail == "" && info.Adapter == "" {
		t.Fatalf("unverified probe must carry the exact reason, got %+v", info)
	}
	if info.DeviceClass == render.DeviceClassHardware && !isHardwareAPIForTest(info.BackendAPI) {
		t.Fatalf("hardware class requires a real driver backend API: %+v", info)
	}
	// An explicit software probe may never claim hardware.
	info, _ = ProbeRenderer(render.PrefSoftware)
	if info.DeviceClass == render.DeviceClassHardware {
		t.Fatalf("software probe must never classify hardware: %+v", info)
	}
}

func isHardwareAPIForTest(api string) bool {
	switch api {
	case "vulkan", "dx12", "metal":
		return true
	}
	return false
}

// runtime_GOMAXPROCS isolates the runtime call for readability.
func runtime_GOMAXPROCS() int { return gomaxprocsCurrent() }
