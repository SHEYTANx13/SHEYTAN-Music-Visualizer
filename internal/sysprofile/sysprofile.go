// Package sysprofile is the SINGLE System Integration authority for
// SHEYTAN Music Visualizer: it collects an honest machine profile,
// derives a safe auto-selection policy, and reports — with explicit
// provenance — what was DETECTED, what is SUPPORTED, what was SELECTED,
// what is EXECUTING and what was VERIFIED (run 42598929646 contract:
// these five states are never conflated).
//
// Evidence honesty rules enforced here:
//
//   - "Detected" is not "usable"; "usable" is not "selected";
//     "selected" is not "executing"; "executing" is not "verified".
//   - A value that cannot be read on an OS or runner is reported as
//     Unknown WITH the exact reason — never guessed, never fabricated.
//   - GPU facts come exclusively from the existing internal/render
//     discovery (the pinned wgpu v0.34.7 API exposes adapter
//     REQUEST, not multi-adapter enumeration; this package therefore
//     extends that discovery instead of building a second enumerator).
//   - Hardware inspection is LOCAL-ONLY: file reads, native OS APIs
//     and registry reads. No WMI/PowerShell/wmic subprocesses, no
//     network, no telemetry, no serial numbers or user identifiers.
//
// Thread safety: Collect is a pure read-only snapshot builder; it is
// safe to call from a background goroutine (the UI calls it off the
// UI thread and marshals widget updates back through fyne.Do).
package sysprofile

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/buildinfo"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/render"
)

// State is the explicit provenance/evidence state of a reported value.
// The progression Unknown→Detected→Supported→Selected→Executing→Verified
// mirrors the render.GpuState machine and the run-42598929646 reporting
// contract; Failed marks an attempted read/selection that did not succeed.
type State string

const (
	// StateUnknown: the value is not reliably readable on this
	// platform/build (Reason carries the exact why).
	StateUnknown State = "unknown"
	// StateDetected: read from a real source on this machine.
	StateDetected State = "detected"
	// StateSupported: detected AND its capability check passed.
	StateSupported State = "supported"
	// StateSelected: chosen for use by the policy (or the user).
	StateSelected State = "selected"
	// StateExecuting: actually in use by the running application.
	StateExecuting State = "executing"
	// StateVerified: executed AND its result was validated (e.g. a GPU
	// frame rendered and read back; never inferred from names or IDs).
	StateVerified State = "verified"
	// StateFailed: attempted and failed (Reason carries the error).
	StateFailed State = "failed"
)

// Fact is a reported string value with explicit provenance.
type Fact struct {
	Value  string `json:"value,omitempty"`
	State  State  `json:"state"`
	Source string `json:"source,omitempty"` // where the value came from
	Reason string `json:"reason,omitempty"` // why unknown/failed, verbatim
}

// Quantity is a reported numeric value with explicit provenance.
type Quantity struct {
	Value  int64  `json:"value,omitempty"`
	State  State  `json:"state"`
	Source string `json:"source,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// unknownFact is the canonical unavailable-fact constructor.
func unknownFact(reason string) Fact { return Fact{State: StateUnknown, Reason: reason} }

// failedFact is the canonical failed-fact constructor.
func failedFact(reason string) Fact { return Fact{State: StateFailed, Reason: reason} }

// failedQuantity is the canonical failed-quantity constructor.
func failedQuantity(reason string) Quantity { return Quantity{State: StateFailed, Reason: reason} }

// OSInfo describes the operating system and the running app build.
type OSInfo struct {
	Family  Fact `json:"family"`  // windows / linux / … (runtime.GOOS — always known)
	Version Fact `json:"version"` // reliable native version or Unknown+reason
	Arch    Fact `json:"arch"`    // runtime.GOARCH
	// ProcessBitness: pointer-width bitness of THIS process (never the
	// OS's maximum capability).
	ProcessBitness Fact `json:"processBitness"`
	AppVersion     Fact `json:"appVersion"` // buildinfo (always known)
	AppCommit      Fact `json:"appCommit"`
}

// CPUInfo describes the processors this process can actually use.
type CPUInfo struct {
	// Model: vendor/model string ONLY from a safe native source
	// (Linux /proc/cpuinfo, Windows registry). No WMI/PowerShell/wmic
	// subprocess is ever spawned for it (hard rule).
	Model Fact `json:"model"`
	// LogicalCPUs: OS-visible logical CPUs available to this process
	// (runtime.NumCPU — Go's runtime already folds CPU affinity and
	// Linux cgroup quota into this value).
	LogicalCPUs Quantity `json:"logicalCPUs"`
	// EffectiveParallelism: runtime.GOMAXPROCS(0) — the CURRENT Go
	// scheduler parallelism baseline. Never overwritten by this
	// package (Go's default is affinity/container-aware; freezing an
	// obsolete total count would oversubscribe containers).
	EffectiveParallelism Quantity `json:"effectiveParallelism"`
	// PhysicalCores: reported only when the OS exposes it reliably;
	// Unknown otherwise (never derived by dividing numbers).
	PhysicalCores Fact `json:"physicalCores"`
	// Features: CPU SIMD feature flags the CPU AND OS have enabled
	// (golang.org/x/sys/cpu — OS-supported AVX state included).
	Features Fact `json:"features"`
}

// MemoryInfo describes physical memory this process can budget against.
// Process virtual address space is deliberately NOT reported as memory
// capacity (they are different resources).
type MemoryInfo struct {
	TotalBytes     Quantity `json:"totalBytes"`
	AvailableBytes Quantity `json:"availableBytes"`
}

// PowerInfo is the AC/battery state ONLY when the OS exposes it
// reliably. It may inform an energy preference; it never alters an OS
// power plan.
type PowerInfo struct {
	State Fact `json:"state"` // "ac" / "battery" / Unknown+reason
}

// AudioInfo reports what the existing audio subsystem can honestly
// state without opening or restarting any stream.
type AudioInfo struct {
	// DefaultOutput: the OS default output device NAME is not exposed
	// by the pinned oto backend, so it is Unknown with that exact
	// reason instead of a fabricated device label. Playback uses the
	// OS default output device.
	DefaultOutput Fact `json:"defaultOutput"`
	// Backend: the audio engine identity that IS known (oto via the
	// existing playback subsystem).
	Backend Fact `json:"backend"`
}

// RenderSection reports the GPU/render path using the existing
// internal/render evidence model verbatim (no second GPU taxonomy).
type RenderSection struct {
	// Active: the renderer backend actually EXECUTING app frames
	// (backend, gpu state machine, adapter identity, honest device
	// class and the exact fallback reason when CPU).
	Active render.BackendInfo `json:"active"`
	// Decision is the user-facing one-line verdict, e.g.
	// "Auto — GPU verified" / "Auto — CPU fallback (reason)" /
	// "Manual: software renderer (never hardware acceleration)".
	Decision string `json:"decision"`
	// Verified is true ONLY when the active backend executed frames and
	// validated them (CPU: deterministic frame render; GPU: readback
	// compare in the real shader path). Never inferred from a name.
	Verified bool `json:"verified"`
}

// Profile is the complete collected snapshot. CollectedAt and Collector
// make every report auditable (which source produced it, when).
type Profile struct {
	CollectedAt time.Time     `json:"collectedAt"`
	Collector   string        `json:"collector"` // linux-native / windows-native / generic
	OS          OSInfo        `json:"os"`
	CPU         CPUInfo       `json:"cpu"`
	Memory      MemoryInfo    `json:"memory"`
	Power       PowerInfo     `json:"power"`
	Audio       AudioInfo     `json:"audio"`
	Render      RenderSection `json:"render"`
	// Export: FFmpeg/ffprobe capability through the single discovery
	// authority (Unknown with the reason when the caller supplied none).
	Export ExportInfo `json:"export"`
	// Display: window-reported display facts (Unknown with the reason
	// when the caller supplied none).
	Display DisplayInfo `json:"display"`
}

// platform hooks — assigned by the build-tagged collector files and
// swappable in tests for deterministic profiles.
var (
	// hookCPUModel reads the CPU vendor/model via native OS APIs only.
	hookCPUModel func() (string, string, error) // (value, source, err)
	// hookMemory reads total/available physical memory natively.
	hookMemory func() (total, avail int64, source string, err error)
	// hookOSVersion reads a reliable OS version natively.
	hookOSVersion func() (string, string, error)
	// hookPhysicalCores reads a reliable physical core count if the OS
	// exposes one; err == nil with value "" means genuinely unavailable.
	hookPhysicalCores func() (string, string, error)
	// hookPower reads AC/battery state when reliably available.
	hookPower func() (string, string, error)
)

// hookMu serializes hook swaps (tests) against collection. Collection
// itself reads the hooks WITHOUT holding this mutex — the mutex only
// guards the swap, so a swap never deadlocks a concurrent snapshot.
var hookMu sync.Mutex

// LockHooks/UnlockHooks were removed: holding the lock across a test
// body deadlocked Collect (the swap is the only critical section).

// Collect builds the profile snapshot. activeRenderer is the BackendInfo
// of the renderer the application is actually executing (from
// render.Renderer.Info()); it is passed in rather than re-selected so
// the profile can never disagree with the live render path.
//
// Export/Display sections default to Unknown-with-reason; callers that
// observed the real values use CollectWith.
func Collect(activeRenderer render.BackendInfo) Profile {
	return CollectWith(activeRenderer, ExportInfo{}, DisplayInfo{})
}

// CollectWith is Collect plus caller-observed Export and Display
// sections (the UI passes what the live window and the single
// FFmpeg discovery authority actually reported). Zero values are
// normalized to explicit Unknown facts — never blank output.
func CollectWith(activeRenderer render.BackendInfo, exportSec ExportInfo, displaySec DisplayInfo) Profile {
	// The hook mutex makes a test-side hook swap atomic against this
	// snapshot. Collection holds it only for the read — swaps never
	// hold it across a test body, so no lock ordering can deadlock.
	hookMu.Lock()
	defer hookMu.Unlock()

	p := Profile{
		CollectedAt: time.Now().UTC(),
		Collector:   collectorName(),
		OS:          collectOS(),
		CPU:         collectCPU(),
		Memory:      collectMemory(),
		Power:       collectPower(),
		Audio:       collectAudio(),
	}
	p.Render = decideRender(activeRenderer)
	p.Export = normalizeExport(exportSec)
	p.Display = normalizeDisplay(displaySec)
	return p
}

// normalizeExport turns a zero/partial ExportInfo into explicit
// Unknown-with-reason facts (never blank output).
func normalizeExport(e ExportInfo) ExportInfo {
	if e.FfmpegPath.State == "" {
		e.FfmpegPath = unknownFact("FFmpeg discovery not performed for this snapshot")
	}
	if e.FfprobePath.State == "" {
		e.FfprobePath = unknownFact("FFmpeg discovery not performed for this snapshot")
	}
	if e.SourceDir.State == "" {
		e.SourceDir = unknownFact("no FFmpeg source location supplied")
	}
	if e.Version.State == "" {
		e.Version = unknownFact("version not probed")
	}
	if e.Encoders.State == "" {
		e.Encoders = unknownFact("encoder set not probed")
	}
	if e.BuiltInDecoders.State == "" {
		e.BuiltInDecoders = unknownFact("decoder registry coverage not supplied by the caller")
	}
	if e.Summary == "" {
		e.Summary = "export capability not observed in this snapshot"
	}
	return e
}

// normalizeDisplay turns a zero/partial DisplayInfo into explicit
// Unknown-with-reason facts (never blank output).
func normalizeDisplay(d DisplayInfo) DisplayInfo {
	if d.Resolution.State == "" {
		d.Resolution = unknownFact("window size not observed")
	}
	if d.Scale.State == "" {
		d.Scale = unknownFact("scale factor not observed")
	}
	if d.Refresh.State == "" {
		d.Refresh = unknownFact(
			"the pinned Fyne v2.8.2 framework does not expose the display refresh rate")
	}
	return d
}

// collectOS gathers the always-known facts plus the platform version.
func collectOS() OSInfo {
	o := OSInfo{
		Family: Fact{Value: runtime.GOOS, State: StateDetected, Source: "runtime.GOOS"},
		Arch:   Fact{Value: runtime.GOARCH, State: StateDetected, Source: "runtime.GOARCH"},
		ProcessBitness: Fact{
			Value:  strconv.Itoa(strconv.IntSize) + "-bit",
			State:  StateDetected,
			Source: "strconv.IntSize (process pointer width)",
		},
		AppVersion: Fact{Value: buildinfo.Normalized().Version, State: StateDetected, Source: "buildinfo"},
		AppCommit:  Fact{Value: buildinfo.Normalized().Commit, State: StateDetected, Source: "buildinfo"},
	}
	if hookOSVersion != nil {
		if v, src, err := hookOSVersion(); err == nil && v != "" {
			o.Version = Fact{Value: v, State: StateDetected, Source: src}
		} else if err != nil {
			o.Version = unknownFact("OS version not available: " + err.Error())
		} else {
			o.Version = unknownFact("OS version not exposed by this platform build")
		}
	} else {
		o.Version = unknownFact("no native OS version collector for this platform")
	}
	return o
}

// collectCPU gathers the scheduler facts, the safe native model string
// and the CPU/OS-enabled SIMD features.
func collectCPU() CPUInfo {
	c := CPUInfo{
		LogicalCPUs: Quantity{
			Value:  int64(runtime.NumCPU()),
			State:  StateDetected,
			Source: "runtime.NumCPU (OS-visible; affinity/cgroup-aware)",
		},
		EffectiveParallelism: Quantity{
			Value:  int64(runtime.GOMAXPROCS(0)),
			State:  StateDetected,
			Source: "runtime.GOMAXPROCS(0) (current scheduler baseline)",
		},
	}
	if hookCPUModel != nil {
		if m, src, err := hookCPUModel(); err == nil && m != "" {
			c.Model = Fact{Value: m, State: StateDetected, Source: src}
		} else if err != nil {
			c.Model = unknownFact("CPU model not available: " + err.Error())
		} else {
			c.Model = unknownFact("CPU model not exposed by this platform build")
		}
	} else {
		c.Model = unknownFact("no native CPU model collector for this platform")
	}
	if hookPhysicalCores != nil {
		if v, src, err := hookPhysicalCores(); err == nil && v != "" {
			c.PhysicalCores = Fact{Value: v, State: StateDetected, Source: src}
		} else if err != nil {
			c.PhysicalCores = unknownFact("physical core count not available: " + err.Error())
		} else {
			c.PhysicalCores = unknownFact("physical core count not exposed by this platform")
		}
	} else {
		c.PhysicalCores = unknownFact("no reliable physical core count source on this platform")
	}
	c.Features = cpuFeaturesFact()
	return c
}

// collectMemory reads physical memory through the platform hook. A
// failure is reported Failed with the reason — never a guessed size.
func collectMemory() MemoryInfo {
	m := MemoryInfo{
		TotalBytes: Quantity{State: StateUnknown,
			Reason: "no native memory collector for this platform"},
		AvailableBytes: Quantity{State: StateUnknown,
			Reason: "no native memory collector for this platform"},
	}
	if hookMemory == nil {
		return m
	}
	total, avail, src, err := hookMemory()
	if err != nil {
		return MemoryInfo{
			TotalBytes:     failedQuantity("memory read failed: " + err.Error()),
			AvailableBytes: failedQuantity("memory read failed: " + err.Error()),
		}
	}
	m.TotalBytes = Quantity{Value: total, State: StateDetected, Source: src}
	m.AvailableBytes = Quantity{Value: avail, State: StateDetected, Source: src}
	return m
}

// collectPower reads AC/battery only when the platform exposes it.
func collectPower() PowerInfo {
	if hookPower == nil {
		return PowerInfo{State: unknownFact("no native power-state collector for this platform")}
	}
	v, src, err := hookPower()
	if err != nil || v == "" {
		return PowerInfo{State: unknownFact("power state not available on this machine" + reasonSuffix(err))}
	}
	return PowerInfo{State: Fact{Value: v, State: StateDetected, Source: src}}
}

// collectAudio reports only what the existing audio subsystem can state
// without opening a stream. The pinned oto backend does not enumerate
// device names, so the output device is honestly Unknown; the engine
// identity IS known.
func collectAudio() AudioInfo {
	return AudioInfo{
		DefaultOutput: unknownFact(
			"the pinned audio backend (ebitengine/oto v3) does not expose device enumeration; " +
				"playback uses the OS default output device"),
		Backend: Fact{
			Value:  "ebitengine/oto v3 (existing playback subsystem)",
			State:  StateDetected,
			Source: "go.mod pinned audio engine",
		},
	}
}

// decideRender converts the ACTIVE renderer evidence into the honest
// decision line. The states are never conflated: a GPU adapter name in
// Active.Adapter without a verified render is still just selected.
func decideRender(active render.BackendInfo) RenderSection {
	sec := RenderSection{Active: active}
	switch {
	case active.Backend == render.BackendGPU && active.GpuState == render.GpuVerified &&
		active.DeviceClass == render.DeviceClassHardware:
		sec.Decision = fmt.Sprintf("GPU verified — %s (%s) executed and read back on the real render path",
			active.Adapter, active.BackendAPI)
		sec.Verified = true
	case active.Backend == render.BackendGPU && active.GpuState == render.GpuVerified:
		sec.Decision = fmt.Sprintf("GPU executing and verified on adapter %s, but the adapter class is %s — NOT proven physical hardware acceleration",
			active.Adapter, displayOr(active.DeviceClass))
		sec.Verified = true
	case active.Backend == render.BackendGPU:
		// Selected/available but not yet verified by a rendered frame.
		sec.Decision = fmt.Sprintf("GPU selected (%s) — verification pending until the first frame executes and reads back",
			displayOr(active.Adapter))
	case active.Backend == render.BackendCPU && active.Detail != "":
		sec.Decision = "CPU fallback — " + active.Detail
	case active.Backend == render.BackendCPU:
		sec.Decision = "CPU renderer active (deterministic Go path)"
	default:
		sec.Decision = "renderer state " + string(active.Backend) + "/" + string(active.GpuState)
	}
	return sec
}

// cpuFeaturesFact renders the CPU SIMD feature set that is enabled by
// BOTH the CPU and the OS (x/sys/cpu already checks OS support for
// AVX/AVX2). Only vector paths dispatch on these; see cpu_features_*.go.
func cpuFeaturesFact() Fact {
	f := cpuFeatures()
	if len(f) == 0 {
		return unknownFact("no CPU feature flags available on this architecture build")
	}
	return Fact{
		Value:  strings.Join(f, ", "),
		State:  StateDetected,
		Source: "golang.org/x/sys/cpu (CPU and OS supported)",
	}
}

func reasonSuffix(err error) string {
	if err == nil {
		return ""
	}
	return ": " + err.Error()
}

func displayOr(s string) string {
	if strings.TrimSpace(s) == "" {
		return "unknown"
	}
	return s
}

// ---- auto-tuning policy ------------------------------------------------

// WorkerPolicy is the safe CPU worker guidance derived from the CURRENT
// scheduler baseline. It is guidance for parallel sections (export
// encoding stages, analysis fan-out) — the package never calls
// runtime.GOMAXPROCS(n) or SetDefaultGOMAXPROCS to freeze the count.
type WorkerPolicy struct {
	// Effective is runtime.GOMAXPROCS(0) — the scheduler baseline the
	// app must stay within unless the user configured workers.
	Effective int `json:"effective"`
	// Workers is the recommended worker count for parallel stages.
	Workers int `json:"workers"`
	// Source records where Workers came from.
	Source string `json:"source"`
}

// envWorkerOverride is the optional user-configured worker count.
const envWorkerOverride = "SHEYTAN_MAX_WORKERS"

// WorkerPolicyFor computes the policy from the live scheduler. A malformed
// user override is ignored (with the reason in Source) rather than
// guessing; an override is honored but never below 1 and never above
// the current scheduler baseline (the scheduler boundary the prompt
// requires — a user wanting more parallelism changes GOMAXPROCS
// deliberately, not silently through the visualizer).
func WorkerPolicyFor() WorkerPolicy {
	base := runtime.GOMAXPROCS(0)
	pol := WorkerPolicy{Effective: base, Workers: base,
		Source: "runtime.GOMAXPROCS(0) (container/affinity-aware default)"}
	if v := strings.TrimSpace(os.Getenv(envWorkerOverride)); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			pol.Source = "SHEYTAN_MAX_WORKERS=" + v + " ignored (not a number); " + pol.Source
			return pol
		}
		if n < 1 {
			pol.Source = "SHEYTAN_MAX_WORKERS=" + v + " ignored (must be >= 1); " + pol.Source
			return pol
		}
		if n > base {
			pol.Workers = base
			pol.Source = fmt.Sprintf("SHEYTAN_MAX_WORKERS=%d clamped to the scheduler baseline %d", n, base)
			return pol
		}
		pol.Workers = n
		pol.Source = fmt.Sprintf("SHEYTAN_MAX_WORKERS=%d (user-configured)", n)
	}
	return pol
}

// ---- memory-aware quality budget ---------------------------------------

// MemoryBudget is the memory-aware default-quality guidance. The
// mapping is deliberately conservative and documented: the DEFAULT
// export preset only rises above 1080p when available memory clearly
// covers 4K frame pipelines; the user keeps full manual control.
type MemoryBudget struct {
	AvailableBytes Quantity `json:"availableBytes"`
	// RecommendedExportHeight: 2160 when >= 12 GiB available, 1440 when
	// >= 8 GiB, else 1080 (the shipped default). Unknown state keeps
	// the shipped default.
	RecommendedExportHeight int    `json:"recommendedExportHeight"`
	Reason                  string `json:"reason"`
}

const (
	gib            = int64(1) << 30
	fourKAvailable = 12 * gib
	qhdAvailable   = 8 * gib
)

// MemoryBudgetFor derives the guidance from a collected profile.
func MemoryBudgetFor(p Profile) MemoryBudget {
	b := MemoryBudget{RecommendedExportHeight: 1080,
		Reason: "default 1080p preset"}
	b.AvailableBytes = p.Memory.AvailableBytes
	if p.Memory.AvailableBytes.State != StateDetected {
		b.Reason = "default 1080p preset (available memory unknown — no honest budget basis)"
		return b
	}
	avail := p.Memory.AvailableBytes.Value
	switch {
	case avail >= fourKAvailable:
		b.RecommendedExportHeight = 2160
		b.Reason = fmt.Sprintf("available memory %.1f GiB comfortably covers 4K export pipelines", float64(avail)/float64(gib))
	case avail >= qhdAvailable:
		b.RecommendedExportHeight = 1440
		b.Reason = fmt.Sprintf("available memory %.1f GiB covers 1440p export pipelines", float64(avail)/float64(gib))
	default:
		b.Reason = fmt.Sprintf("available memory %.1f GiB — keeping the 1080p default", float64(avail)/float64(gib))
	}
	return b
}

// collectorName identifies the platform collector set that ran.
func collectorName() string {
	return platformCollectorName
}
