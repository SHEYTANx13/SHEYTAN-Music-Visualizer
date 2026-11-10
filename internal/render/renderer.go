// Package render owns the deterministic frame renderers: a pure-Go CPU
// renderer (always available) and a Go-controlled WGPU renderer used
// when a real GPU adapter is available. Both implement the same
// Renderer contract and consume the same visualizer frame inputs.
//
// Evidence honesty: the package distinguishes detected / available /
// selected / executed / verified GPU states. Adapter enumeration alone
// is never reported as GPU rendering; a backend is EXECUTED only after
// a submitted command buffer and VERIFIED only after a successful
// readback compare.
package render

import (
	"image"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/visualizer"
)

// Backend identifies a rendering backend.
type Backend string

const (
	BackendCPU     Backend = "cpu"
	BackendGPU     Backend = "gpu"
	BackendGPUNoop Backend = "gpu-noop" // wgpu noop backend (test/CI only)
)

// GpuState is the honest GPU evidence state machine.
type GpuState string

const (
	GpuNone      GpuState = "none"      // no GPU path attempted
	GpuDetected  GpuState = "detected"  // adapter enumerated
	GpuAvailable GpuState = "available" // adapter + device created
	GpuSelected  GpuState = "selected"  // chosen as active backend
	GpuExecuted  GpuState = "executed"  // commands submitted without error
	GpuVerified  GpuState = "verified"  // readback compared successfully
	GpuFallback  GpuState = "fallback"  // GPU attempted but fell back to CPU
)

// BackendInfo describes the active renderer for status/diagnostics.
type BackendInfo struct {
	Backend    Backend  `json:"backend"`
	GpuState   GpuState `json:"gpuState"`
	Adapter    string   `json:"adapter,omitempty"`
	Vendor     string   `json:"vendor,omitempty"`
	BackendAPI string   `json:"backendApi,omitempty"` // vulkan / dx12 / metal / noop / software
	Driver     string   `json:"driver,omitempty"`
	// DeviceType is the honest adapter class (discrete-gpu,
	// integrated-gpu, virtual-gpu, cpu, other) when the API exposes it.
	DeviceType string `json:"deviceType,omitempty"`
	// DeviceClass is the honest hardware/software classification derived
	// from adapter PROPERTIES (PCI identity, device type, backend):
	// hardware / software / virtual / unknown (run 42231355353 repair:
	// a software adapter — e.g. Microsoft Basic Render Driver / WARP —
	// is never represented as physical GPU acceleration, whatever the
	// underlying API reports as its device type).
	DeviceClass string `json:"deviceClass,omitempty"`
	// VendorID/DeviceID are the PCI identifiers when available.
	VendorID uint32 `json:"vendorId,omitempty"`
	DeviceID uint32 `json:"deviceId,omitempty"`
	// Detail carries the honest fallback/limit reason.
	Detail string `json:"detail,omitempty"`
}

// Renderer renders one deterministic frame per input.
type Renderer interface {
	// Info describes the active backend honestly.
	Info() BackendInfo
	// RenderFrame renders input into a new RGBA image (or a reusable
	// buffer when the implementation supports it). Implementations
	// must be safe for sequential use from one goroutine.
	RenderFrame(in visualizer.FrameInput) (*image.RGBA, error)
	// Close releases GPU resources.
	Close()
}

// SelectRenderer picks the best available backend: GPU when a real
// hardware adapter can be proven, otherwise CPU. Software/noop wgpu
// adapters are deliberately refused as "GPU" — they execute pipeline
// semantics on the CPU and labeling them GPU would be dishonest. The
// refusal is PROPERTY-based (run 42231355353 repair): the adapter must
// classify as hardware (DeviceClass) AND report a real driver backend
// API AND have created a device. A dx12-reported Microsoft Basic Render
// Driver / WARP adapter fails the DeviceClass check and falls back to
// the CPU renderer with the exact reason recorded. The returned info
// always reflects the actual decision, including the exact reason when
// the GPU path was unavailable or refused.
func SelectRenderer() (Renderer, BackendInfo) {
	pref, fromEnv, envErr := PreferenceFromEnv()
	if envErr != nil {
		c := NewCPURenderer()
		ci := c.Info()
		ci.Detail = "GPU preference env invalid (" + envErr.Error() + "); using the CPU renderer"
		return c, ci
	}
	return selectRendererWith(pref, fromEnv)
}

// selectRendererWith is the preference-driven selection core shared by
// the env path (SelectRenderer) and the persisted-preference path
// (SelectRendererExplicit, preference_store.go).
func selectRendererWith(pref Preference, fromEnv bool) (Renderer, BackendInfo) {
	g, attemptInfo := newGPURendererFull(pref)
	if g != nil {
		info := g.Info()
		if info.GpuState == GpuAvailable && isHardwareBackend(info.BackendAPI) && info.DeviceClass == DeviceClassHardware {
			if fromEnv {
				info.Detail = "GPU preference from SHEYTAN_GPU_PREFERENCE=" + string(pref)
			}
			return g, info
		}
		g.Close()
		c := NewCPURenderer()
		ci := c.Info()
		switch {
		case attemptInfo.Adapter != "":
			class := attemptInfo.DeviceClass
			if class == "" {
				class = DeviceClassUnknown
			}
			ci.Detail = "GPU fallback: " + attemptInfo.Adapter + " (" + attemptInfo.BackendAPI +
				", device class " + class + ") is not a proven hardware adapter; using the CPU renderer"
		case attemptInfo.Detail != "":
			ci.Detail = "GPU fallback: " + attemptInfo.Detail
		}
		return c, ci
	}
	c := NewCPURenderer()
	ci := c.Info()
	if attemptInfo.Detail != "" {
		ci.Detail = "GPU fallback: " + attemptInfo.Detail
	}
	return c, ci
}
