// policy_modes.go — the user-facing resource policy modes for the
// System Integration surface. Default is Auto (recommended); the other
// modes (High performance / Balanced / Battery efficient / Manual) are
// GUIDANCE a bounded application reads at defined decision points —
// never a silent GOMAXPROCS or thermal override. The Go scheduler
// default already folds CPU affinity and cgroup quota into its
// baseline (see sysprofile.go CPUInfo.EffectiveParallelism), so this
// policy never calls runtime.GOMAXPROCS(n): it caps workers and queues
// WITHIN the live baseline and explains every deviation in Source.
//
// Manual: the existing settings architecture exposes the manual lever
// as the SHEYTAN_MAX_WORKERS environment override honored by
// WorkerPolicyFor (clamped to the scheduler baseline). Selecting
// "manual" in the UI therefore means Auto + that override; the mode is
// reported honestly rather than pretending an extra knob exists.
package sysprofile

import (
	"fmt"
	"strings"
)

// PerformanceMode is the user-selectable resource mode.
type PerformanceMode string

const (
	// ModeAuto is the default: the scheduler baseline decides, memory
	// guidance stays conservative, GPU follows the render selector.
	ModeAuto PerformanceMode = "auto"
	// ModeHighPerformance allows the bounded upper guidance (more
	// workers inside the baseline, larger frame/export queues).
	ModeHighPerformance PerformanceMode = "high-performance"
	// ModeBalanced is the conservative middle (identical to Auto's
	// worker guidance, documented separately for the UI).
	ModeBalanced PerformanceMode = "balanced"
	// ModeBatteryEfficient lowers background guidance; playback/UI
	// latency still leads (audio correctness is never traded).
	ModeBatteryEfficient PerformanceMode = "battery-efficient"
	// ModeManual is Auto plus the SHEYTAN_MAX_WORKERS user override.
	ModeManual PerformanceMode = "manual"
)

// ModeChoices lists the display order for the UI select.
var ModeChoices = []string{
	string(ModeAuto),
	string(ModeHighPerformance),
	string(ModeBalanced),
	string(ModeBatteryEfficient),
	string(ModeManual),
}

// ParsePerformanceMode validates a mode string (empty -> Auto).
func ParsePerformanceMode(s string) (PerformanceMode, error) {
	v := strings.ToLower(strings.TrimSpace(s))
	if v == "" {
		return ModeAuto, nil
	}
	switch PerformanceMode(v) {
	case ModeAuto, ModeHighPerformance, ModeBalanced, ModeBatteryEfficient, ModeManual:
		return PerformanceMode(v), nil
	default:
		return "", fmt.Errorf("unknown performance mode %q (want one of: %s)", s, strings.Join(ModeChoices, ", "))
	}
}

// ModePolicy is the bounded guidance derived from a mode. Every field
// is a CAP or a RECOMMENDATION — the package never mutates the Go
// scheduler, opens queues itself, or exceeds the live baseline.
type ModePolicy struct {
	Mode PerformanceMode `json:"mode"`
	// Workers: recommended worker count for parallel background stages
	// (analysis fan-out, import decode). Always >= 1 and <= the live
	// scheduler baseline.
	Workers int `json:"workers"`
	// MaxExportHeight: memory-quality guidance (mirrors MemoryBudget,
	// narrowed by mode for Battery efficient).
	MaxExportHeight int `json:"maxExportHeight"`
	// FrameQueueDepth: bounded visualization frame queue depth.
	FrameQueueDepth int `json:"frameQueueDepth"`
	// GpuAllowed: Battery efficient still allows the verified GPU path
	// (offloading the CPU render loop SAVES energy); the flag exists so
	// a future policy has an honest, tested place to express it.
	GpuAllowed bool `json:"gpuAllowed"`
	// Source explains exactly how every number above was derived.
	Source string `json:"source"`
}

// ModePolicyFor derives the policy from the mode and a collected
// profile. Unknown profile data keeps the shipped defaults (never a
// guessed budget); user overrides clamp inside the scheduler baseline.
func ModePolicyFor(mode PerformanceMode, p Profile) ModePolicy {
	base := gomaxprocsCurrent()
	if base < 1 {
		base = 1
	}
	pol := ModePolicy{
		Mode:            ModeAuto,
		Workers:         base,
		MaxExportHeight: 1080,
		FrameQueueDepth: 8,
		GpuAllowed:      true,
		Source:          fmt.Sprintf("auto: scheduler baseline %d workers, shipped 1080p default, bounded frame queue 8", base),
	}
	switch mode {
	case ModeHighPerformance:
		pol.Mode = mode
		workers := base // still capped: no oversubscription of the baseline
		pol.Workers = workers
		pol.FrameQueueDepth = 12
		pol.Source = fmt.Sprintf("high-performance: %d workers (scheduler baseline cap), frame queue 12; memory budget unchanged for stability", workers)
	case ModeBalanced:
		pol.Mode = mode
		pol.Source = fmt.Sprintf("balanced: %d workers (scheduler baseline), frame queue 8", base)
	case ModeBatteryEfficient:
		pol.Mode = mode
		workers := 2
		if workers > base {
			workers = base
		}
		pol.Workers = workers
		pol.FrameQueueDepth = 4
		pol.MaxExportHeight = 1080
		pol.Source = fmt.Sprintf("battery-efficient: %d background workers, frame queue 4, export height capped at 1080p (audio/UI latency still lead)", workers)
	case ModeManual:
		pol.Mode = mode
		wp := WorkerPolicyFor()
		pol.Workers = wp.Workers
		pol.Source = "manual: " + wp.Source
	default:
		// ModeAuto and anything unparseable keep the Auto guidance with
		// the memory-quality refinement below.
	}
	// Memory-quality refinement (Auto/Balanced/High performance only):
	// apply the documented MemoryBudget mapping when the profile
	// actually detected available memory.
	if mode != ModeBatteryEfficient && p.Memory.AvailableBytes.State == StateDetected {
		b := MemoryBudgetFor(p)
		if b.RecommendedExportHeight > pol.MaxExportHeight {
			pol.MaxExportHeight = b.RecommendedExportHeight
			pol.Source += "; " + b.Reason
		}
	}
	return pol
}
