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
	Detail     string   `json:"detail,omitempty"` // honest fallback/limit reason
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
// returned info always reflects the actual decision.
func SelectRenderer() (Renderer, BackendInfo) {
	g := NewGPURenderer()
	if g != nil {
		info := g.Info()
		if info.GpuState == GpuAvailable && isHardwareBackend(info.BackendAPI) {
			return g, info
		}
		g.Close()
		c := NewCPURenderer()
		ci := c.Info()
		if info.Adapter != "" {
			ci.Detail = "GPU fallback: " + info.Adapter + " (" + info.BackendAPI +
				") is not a hardware adapter; using the CPU renderer"
		}
		return c, ci
	}
	c := NewCPURenderer()
	return c, c.Info()
}
