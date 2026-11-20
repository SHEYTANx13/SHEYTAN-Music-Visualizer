//go:build sheytan_nogpu

// gpu_unlinked.go — honest native-GPU-absent build profile.
//
// The sheytan_nogpu profile links the application WITHOUT the native
// GoGPU (github.com/gogpu/wgpu) renderer. It exists because the Wails
// v3 purego shell and the goffi/fakecgo machinery of the gogpu stack
// cannot be linked into one binary (both define their own fakecgo
// symbols), while Wails requires CGO_ENABLED=0 purego builds.
//
// The profile is an honest configuration, not a degraded lie: every
// selector reports the exact reason and falls back to the always-valid
// CPU renderer. GPU evidence states remain exact: nothing is reported
// as detected/available/executed/verified, because nothing GPU is
// linked.
package render

import (
	"errors"
	"image"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/visualizer"
)

// DeviceClass values are shared by both profiles.
const (
	DeviceClassHardware = "hardware" // proven physical adapter on a real driver backend
	DeviceClassSoftware = "software" // CPU/WARP/llvmpipe-class renderer — never hardware
	DeviceClassVirtual  = "virtual"  // virtual GPU (VM/remote) — never physical hardware
	DeviceClassUnknown  = "unknown"  // properties do not prove hardware — never labeled hardware
)

// isHardwareBackend mirrors the linked profile's contract: with no GPU
// renderer linked there is no backend API to classify, so nothing is
// hardware.
func isHardwareBackend(api string) bool {
	return false
}

// nogpuDetail is the exact, user-visible reason recorded in BackendInfo.
const nogpuDetail = "native GPU renderer not linked in this build profile (sheytan_nogpu); the CPU renderer is the active renderer"

// errGPURendererUnlinked is returned by the placeholder GPURenderer.
var errGPURendererUnlinked = errors.New("render: native GPU renderer not linked (sheytan_nogpu build profile)")

// nogpuInfo builds the honest BackendInfo for the unlinked profile.
func nogpuInfo() BackendInfo {
	return BackendInfo{
		Backend:    BackendCPU,
		GpuState:   GpuNone,
		BackendAPI: "software",
		Detail:     nogpuDetail,
	}
}

// NewGPURenderer returns nil: no GPU renderer exists in this profile.
// Every caller already treats nil as "fall back to CPU".
func NewGPURenderer() *GPURenderer { return nil }

// NewGPURendererWithPreference returns nil plus the honest reason.
func NewGPURendererWithPreference(p Preference) (*GPURenderer, BackendInfo) {
	return nil, nogpuInfo()
}

// newGPURendererFull returns nil so selectRendererWith falls back to
// the CPU renderer carrying the exact profile reason.
func newGPURendererFull(p Preference, _ ...struct{}) (*GPURenderer, BackendInfo) {
	return nil, nogpuInfo()
}

// GPURenderer is a placeholder type so code that names the type keeps
// compiling. It is never instantiated in this profile.
type GPURenderer struct{}

// Info always reports the unlinked profile honestly.
func (g *GPURenderer) Info() BackendInfo { return nogpuInfo() }

// RenderFrame refuses: this profile has no GPU frame path.
func (g *GPURenderer) RenderFrame(in visualizer.FrameInput) (*image.RGBA, error) {
	return nil, errGPURendererUnlinked
}

// Close is a no-op: nothing was created.
func (g *GPURenderer) Close() {}
