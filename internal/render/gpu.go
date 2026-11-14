package render

// gpu.go — the Go-controlled WGPU renderer. The Radial Spectrum mode
// is rendered by a WGSL fragment shader (real GPU draw work on an
// offscreen target) with a verified readback path. Other modes keep
// the CPU implementation; the same visualizer contract and scene
// parameters apply to both.
//
// Evidence honesty: the renderer reports DETECTED (adapter found) →
// AVAILABLE (device created) → SELECTED (picked as backend) →
// EXECUTED (command buffer submitted) → VERIFIED (readback compared).
// Any failure downgrades to the CPU renderer with the reason recorded.

import (
	"context"
	"errors"
	"fmt"
	"image"
	"math"
	"os"
	"strings"
	"time"

	"github.com/gogpu/gputypes"
	"github.com/gogpu/wgpu"
	_ "github.com/gogpu/wgpu/hal/allbackends"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/visualizer"
)

// Preference is the explicit backend preference model. Auto prefers a
// real hardware adapter on the platform (Vulkan first, then DX12 on
// Windows, then GL); explicit selections either select that backend or
// report the exact reason they cannot — they never silently select
// software while claiming the requested backend.
type Preference string

const (
	PrefAuto     Preference = "auto"
	PrefCPU      Preference = "cpu"
	PrefVulkan   Preference = "vulkan"
	PrefDX12     Preference = "dx12"
	PrefSoftware Preference = "software"
)

// ParsePreference validates a user-supplied preference string
// (case-insensitive). Unknown values are an error, never silently auto.
func ParsePreference(s string) (Preference, error) {
	switch Preference(strings.ToLower(strings.TrimSpace(s))) {
	case "", PrefAuto:
		return PrefAuto, nil
	case PrefCPU:
		return PrefCPU, nil
	case PrefVulkan:
		return PrefVulkan, nil
	case PrefDX12:
		return PrefDX12, nil
	case PrefSoftware:
		return PrefSoftware, nil
	default:
		return "", fmt.Errorf("render: unknown GPU preference %q (want auto|cpu|vulkan|dx12|software)", s)
	}
}

// PreferenceFromEnv reads SHEYTAN_GPU_PREFERENCE (optional). Missing
// env var → (PrefAuto, false); malformed value → error so callers can
// surface it instead of silently falling back.
func PreferenceFromEnv() (Preference, bool, error) {
	v := strings.TrimSpace(os.Getenv("SHEYTAN_GPU_PREFERENCE"))
	if v == "" {
		return PrefAuto, false, nil
	}
	p, err := ParsePreference(v)
	if err != nil {
		return "", true, err
	}
	return p, true, nil
}

// instanceBackendsFor maps the preference onto the instance backend
// mask (the pinned github.com/gogpu/wgpu v0.34.7 API: the instance only
// enumerates adapters from backends enabled in this mask; provider
// priority orders Vulkan → DX12 → GL → software).
func instanceBackendsFor(p Preference) gputypes.Backends {
	switch p {
	case PrefVulkan:
		return gputypes.BackendsVulkan
	case PrefDX12:
		return gputypes.BackendsDX12
	default:
		// Auto and Software use the default primary mask; Software is
		// enforced via ForceFallbackAdapter on the adapter request.
		return gputypes.BackendsPrimary
	}
}

// deviceTypeName maps the adapter device type honestly.
func deviceTypeName(t gputypes.DeviceType) string {
	switch t {
	case gputypes.DeviceTypeDiscreteGPU:
		return "discrete-gpu"
	case gputypes.DeviceTypeIntegratedGPU:
		return "integrated-gpu"
	case gputypes.DeviceTypeVirtualGPU:
		return "virtual-gpu"
	case gputypes.DeviceTypeCPU:
		return "cpu"
	default:
		return "other"
	}
}

// Adapter device classes (run 42231355353 GPU evidence repair). The
// class is reported next to the raw device type so a software adapter
// can never be represented as physical GPU acceleration, no matter how
// the underlying API labels its device type.
const (
	DeviceClassHardware = "hardware" // proven physical adapter on a real driver backend
	DeviceClassSoftware = "software" // CPU/WARP/llvmpipe-class renderer — never hardware
	DeviceClassVirtual  = "virtual"  // virtual GPU (VM/remote) — never physical hardware
	DeviceClassUnknown  = "unknown"  // properties do not prove hardware — never labeled hardware
)

// Microsoft's software-adapter PCI identity. DXGI marks this adapter
// with DXGI_ADAPTER_FLAG_SOFTWARE; the wgpu D3D layer can still report
// it as an integrated GPU. Microsoft ships no physical GPUs, so the
// Microsoft vendor id alone identifies the software adapter; the
// 0x1414/0x008c pair is the canonical Basic Render Driver / WARP
// identity. A physical primary adapter can carry a similar NAME when
// the display driver is broken, so names never decide the class —
// properties do (DXGI_ADAPTER_FLAG_SOFTWARE semantics).
const (
	// msSoftwareVendorID is Microsoft's PCI vendor id (0x1414). Microsoft
	// ships no physical GPUs — the vendor id alone identifies the software
	// adapter (the Basic Render Driver / WARP device id is 0x008c) even
	// when the D3D layer mislabels its device type.
	msSoftwareVendorID uint32 = 0x1414
)

// softwareAdapterNames are adapter names that ARE software rasterizers
// by construction (Mesa/SwiftShader software implementations identify
// themselves; this is not display-name guessing but the renderer's own
// identity). "Microsoft Basic Render Driver" is deliberately NOT in
// this list: its name alone cannot distinguish the software adapter
// from a primary adapter with a broken display driver.
var softwareAdapterNames = []string{
	"llvmpipe", "lavapipe", "softpipe", "swiftshader", "swrast",
}

// classifyAdapterInfo assigns the honest device class from adapter
// PROPERTIES (PCI identity, device type, backend), never from the
// display name alone. First match wins:
//
//   - DeviceType CPU, the Microsoft software PCI identity (0x1414, and
//     specifically 0x1414/0x008c), or a self-identified software
//     rasterizer name → software;
//   - a wgpu noop/empty backend → software (the GoGPU software/noop
//     adapter reports BackendEmpty);
//   - a discrete GPU on a real driver backend → hardware;
//   - an integrated GPU on a real driver backend with a non-Microsoft
//     PCI vendor → hardware; with Microsoft's vendor id it is the
//     software adapter mislabeled by the D3D layer → software;
//   - a virtual GPU → virtual;
//   - everything else (missing IDs, unknown type) → unknown.
func classifyAdapterInfo(info gputypes.AdapterInfo) string {
	if info.DeviceType == gputypes.DeviceTypeCPU {
		return DeviceClassSoftware
	}
	if info.VendorID == msSoftwareVendorID {
		// Microsoft ships no physical GPUs; 0x1414(/0x008c) is the WARP
		// Basic Render Driver identity even when the type is mislabeled.
		return DeviceClassSoftware
	}
	name := info.Name
	for _, sw := range softwareAdapterNames {
		if containsFold(name, sw) {
			return DeviceClassSoftware
		}
	}
	api := backendName(info.Backend)
	switch info.DeviceType {
	case gputypes.DeviceTypeDiscreteGPU:
		if isHardwareBackend(api) {
			return DeviceClassHardware
		}
		return DeviceClassUnknown
	case gputypes.DeviceTypeIntegratedGPU:
		if isHardwareBackend(api) {
			return DeviceClassHardware
		}
		return DeviceClassUnknown
	case gputypes.DeviceTypeVirtualGPU:
		return DeviceClassVirtual
	default:
		if info.Backend == gputypes.BackendEmpty {
			// The GoGPU software/noop adapter reports no backend.
			return DeviceClassSoftware
		}
		return DeviceClassUnknown
	}
}

// wgslShader is the Radial Spectrum fragment shader. It mirrors the
// CPU radial renderer's math (base radius driven by bass + beat, 64
// mirrored bins, palette ramp) inside the brand palette.
const wgslShader = `
struct Params {
        res      : vec2f,
        time     : f32,
        bass     : f32,
        beat     : f32,
        glow     : f32,
        rotSpeed : f32,
        pad      : vec2f,
        bins     : array<vec4f, 16>,
        colLow   : vec4f,
        colMid   : vec4f,
        colHigh  : vec4f,
        colPeak  : vec4f,
        colBack  : vec4f,
}
@group(0) @binding(0) var<uniform> P : Params;

@vertex
fn vs(@builtin(vertex_index) vi : u32) -> @builtin(position) vec4f {
        var pos = array<vec2f, 3>(vec2f(-1.0, -1.0), vec2f(3.0, -1.0), vec2f(-1.0, 3.0));
        return vec4f(pos[vi], 0.0, 1.0);
}

fn binAt(a : f32) -> f32 {
        // mirror 64 bins across the circle; deterministic
        var t = a / (2.0 * 3.14159265358979);
        if (t < 0.0) { t = t + 1.0; }
        t = t * 64.0;
        var idx = i32(floor(t)) % 64;
        if (idx >= 32) { idx = 63 - idx; }
        let v4 = P.bins[idx / 4];
        return select(v4.x, v4.y, (idx % 4) == 1)
                 + select(0.0, v4.z - v4.x, (idx % 4) == 2)
                 + select(0.0, v4.w - v4.x, (idx % 4) == 3);
}

@fragment
fn fs(@builtin(position) frag : vec4f) -> @location(0) vec4f {
        let uv = (frag.xy - P.res * 0.5) / min(P.res.x, P.res.y);
        let r = length(uv);
        let ang = atan2(uv.y, uv.x);

        // background: obsidian gradient
        let bgT = clamp(frag.y / P.res.y, 0.0, 1.0);
        var col = mix(vec3f(0.031, 0.031, 0.031), vec3f(P.colBack.x, P.colBack.y, P.colBack.z) * 1.6, bgT * bgT);

        // radial spectrum
        let baseR = 0.20 * (1.0 + 0.10 * P.bass * P.glow + 0.08 * P.beat);
        let rot = P.time * P.rotSpeed;
        let v = pow(clamp(binAt(ang - rot), 0.0, 1.0), 0.85);
        let len = baseR + v * 0.30;

        // bar body
        let bar = smoothstep(len + 0.004, len - 0.004, r) * smoothstep(baseR - 0.004, baseR + 0.004, r);
        let tc = clamp(v, 0.0, 1.0);
        var barCol = mix(vec3f(P.colLow.x, P.colLow.y, P.colLow.z), vec3f(P.colMid.x, P.colMid.y, P.colMid.z), tc);
        barCol = mix(barCol, vec3f(P.colHigh.x, P.colHigh.y, P.colHigh.z), pow(tc, 2.0));
        // peak highlight: the strongest bins tip toward the peak color,
        // mirroring the CPU renderer's peak-hold markers on strong bars.
        // Every packed lane (colLow..colBack) is read here so the shader
        // and the Go packer stay in contract (see TestWGSLParamsContractMatchesGoLayout).
        barCol = mix(barCol, vec3f(P.colPeak.x, P.colPeak.y, P.colPeak.z), smoothstep(0.85, 1.0, tc));
        col = mix(col, barCol, bar * (0.55 + 0.45 * v));

        // ring
        let ring = exp(-abs(r - baseR * 0.92) * 90.0);
        col = mix(col, vec3f(P.colMid.x, P.colMid.y, P.colMid.z), ring * 0.5);

        // core glow driven by bass + beat
        let core = exp(-r * r * 26.0) * (0.18 + 0.35 * P.bass + 0.25 * P.beat);
        col += vec3f(P.colLow.x, P.colLow.y, P.colLow.z) * core;

        return vec4f(col, 1.0);
}
`

// GPURenderer renders the radial mode on the GPU.
type GPURenderer struct {
	info       BackendInfo
	preference Preference
	instance   *wgpu.Instance
	adapter    *wgpu.Adapter // kept reachable for the device lifetime
	device     *wgpu.Device
	queue      *wgpu.Queue
	pipeline   *wgpu.RenderPipeline
	uniform    *wgpu.Buffer
	bindGroup  *wgpu.BindGroup
	cpu        *CPURenderer // fallback renderer for non-GPU modes
	lastW      int
	lastH      int
	target     *wgpu.Texture
	readback   *wgpu.Buffer
	// renderGPUFn is the GPU submission step, injectable so the
	// device-loss RECOVERY path can be drilled in tests without
	// hardware (production code always uses the real renderGPU).
	renderGPUFn func(in visualizer.FrameInput) error
	lastImage   *image.RGBA
	uniformData []byte // reused uniform staging buffer
	// sceneBins is the scratch the scene-smoothed bin values are packed
	// from (run 43541087097 GPU/CPU parity repair — see renderGPU).
	sceneBins [visualizer.NumVizBins]float64
}

func f32bits(v float64) uint32 { return mathFloat32bits(float32(v)) }

// f32le decodes a little-endian float32 from b (diagnostics and tests).
func f32le(b []byte) float64 {
	bits := uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
	return float64(math.Float32frombits(bits))
}

func mathFloat32bits(f float32) uint32 {
	return math.Float32bits(f)
}

// NewGPURenderer attempts to create a real GPU renderer with the Auto
// preference. It returns nil when no usable adapter/device exists (the
// caller then uses the CPU renderer and reports the fallback honestly;
// SelectRenderer carries the reason).
func NewGPURenderer() *GPURenderer {
	g, _ := newGPURendererFull(PrefAuto)
	return g
}

// NewGPURendererWithPreference builds the GPU renderer for an explicit
// preference. It returns nil plus the honest failure/fallback info.
func NewGPURendererWithPreference(p Preference) (*GPURenderer, BackendInfo) {
	return newGPURendererFull(p)
}

// newGPURendererFor builds the GPU renderer against a specific adapter
// request (tests force the noop fallback adapter to prove the full
// render/readback pipeline deterministically on any host).
func newGPURendererFor(opts *wgpu.RequestAdapterOptions) *GPURenderer {
	g, _ := newGPURendererFull(PrefAuto, opts)
	return g
}

// newGPURendererFull is the single construction path. opts==nil uses a
// default adapter request (no forced fallback).
func newGPURendererFull(p Preference, opts ...*wgpu.RequestAdapterOptions) (*GPURenderer, BackendInfo) {
	g := &GPURenderer{cpu: NewCPURenderer(), preference: p}
	inst, err := wgpu.CreateInstance(&wgpu.InstanceDescriptor{Backends: instanceBackendsFor(p)})
	if err != nil {
		info := BackendInfo{Backend: BackendGPU, GpuState: GpuNone,
			Detail: fmt.Sprintf("wgpu instance creation failed: %v", err)}
		return nil, info
	}
	// The instance, adapter and device stay alive for the renderer's
	// lifetime (released in Close); releasing the instance here would
	// invalidate the device.
	g.instance = inst
	req := &wgpu.RequestAdapterOptions{ForceFallbackAdapter: p == PrefSoftware}
	if len(opts) > 0 && opts[0] != nil {
		req = opts[0]
	}
	adapter, err := inst.RequestAdapter(req)
	if err != nil || adapter == nil {
		reason := "no GPU adapter available on this host (expected on headless machines)"
		if err != nil {
			reason = fmt.Sprintf("adapter request failed: %v", err)
		} else if p != PrefAuto {
			reason = fmt.Sprintf("no adapter for the explicitly requested %q backend on this host", p)
		}
		info := BackendInfo{Backend: BackendGPU, GpuState: GpuNone, Detail: reason}
		return nil, info
	}
	// OWNERSHIP: the adapter must be kept reachable for the device
	// lifetime and released in Close. (It previously leaked out of
	// scope here while the comment claimed otherwise.)
	g.adapter = adapter
	info := adapter.Info()
	api := backendName(info.Backend)
	if api == "unknown" || api == "software" {
		// GoGPU software/noop adapters report BackendEmpty; label
		// by the adapter's self-reported name for full honesty.
		switch {
		case containsFold(info.Name, "noop"):
			api = "noop"
		case containsFold(info.Name, "software"):
			api = "software"
		}
	}
	g.info = BackendInfo{
		Backend: BackendGPU, GpuState: GpuDetected,
		Adapter: info.Name, Vendor: info.Vendor, Driver: info.Driver,
		BackendAPI: api,
		DeviceType: deviceTypeName(info.DeviceType),
		// DeviceClass is the honest hardware/software classification
		// derived from adapter PROPERTIES (PCI identity, device type,
		// backend) — never from the adapter display name alone.
		DeviceClass: classifyAdapterInfo(info),
		VendorID:    info.VendorID, DeviceID: info.DeviceID,
	}
	dev, err := adapter.RequestDevice(nil)
	if err != nil {
		g.info.Detail = fmt.Sprintf("device creation failed: %v", err)
		g.releaseGPU()
		return nil, g.info
	}
	g.device = dev
	g.queue = dev.Queue()
	g.info.GpuState = GpuAvailable
	if err := g.buildPipeline(); err != nil {
		g.info.Detail = fmt.Sprintf("pipeline build failed: %v", err)
		g.releaseGPU()
		return nil, g.info
	}
	if p == PrefVulkan && api != "vulkan" || p == PrefDX12 && api != "dx12" {
		// Defense in depth: the backend mask should make this
		// impossible; if a backend mismatch ever occurs it is recorded,
		// never silently relabeled.
		g.info.Detail = fmt.Sprintf("preference %q requested but adapter reports %q", p, api)
	}
	g.renderGPUFn = g.renderGPU
	return g, g.info
}

func backendName(b gputypes.Backend) string {
	switch b {
	case gputypes.BackendVulkan:
		return "vulkan"
	case gputypes.BackendDX12:
		return "dx12"
	case gputypes.BackendMetal:
		return "metal"
	case gputypes.BackendGL:
		return "gl"
	default:
		return "unknown"
	}
}

// buildPipeline compiles the WGSL and the render pipeline.
func (g *GPURenderer) buildPipeline() error {
	shader, err := g.device.CreateShaderModule(&wgpu.ShaderModuleDescriptor{
		Label: "sheytan-radial", WGSL: wgslShader,
	})
	if err != nil {
		return fmt.Errorf("shader compile: %w", err)
	}
	bgl, err := g.device.CreateBindGroupLayout(&wgpu.BindGroupLayoutDescriptor{
		Label: "sheytan-params",
		Entries: []gputypes.BindGroupLayoutEntry{{
			Binding:    0,
			Visibility: gputypes.ShaderStageFragment,
			Buffer:     &gputypes.BufferBindingLayout{Type: gputypes.BufferBindingTypeUniform, MinBindingSize: uniformBindMinSize},
		}},
	})
	if err != nil {
		return fmt.Errorf("bind group layout: %w", err)
	}
	pl, err := g.device.CreatePipelineLayout(&wgpu.PipelineLayoutDescriptor{
		Label: "sheytan-layout", BindGroupLayouts: []*wgpu.BindGroupLayout{bgl},
	})
	if err != nil {
		return fmt.Errorf("pipeline layout: %w", err)
	}
	g.uniform, err = g.device.CreateBuffer(&wgpu.BufferDescriptor{
		Label: "sheytan-uniforms", Size: uniformSize,
		Usage: gputypes.BufferUsageUniform | gputypes.BufferUsageCopyDst,
	})
	if err != nil {
		return fmt.Errorf("uniform buffer: %w", err)
	}
	g.bindGroup, err = g.device.CreateBindGroup(&wgpu.BindGroupDescriptor{
		Label: "sheytan-bind", Layout: bgl,
		Entries: []wgpu.BindGroupEntry{{Binding: 0, Buffer: g.uniform}},
	})
	if err != nil {
		return fmt.Errorf("bind group: %w", err)
	}
	g.pipeline, err = g.device.CreateRenderPipeline(&wgpu.RenderPipelineDescriptor{
		Label:  "sheytan-radial-pipeline",
		Layout: pl,
		Vertex: wgpu.VertexState{Module: shader, EntryPoint: "vs"},
		Primitive: gputypes.PrimitiveState{
			Topology: gputypes.PrimitiveTopologyTriangleList,
		},
		Fragment: &wgpu.FragmentState{
			Module:     shader,
			EntryPoint: "fs",
			Targets: []gputypes.ColorTargetState{{
				Format: gputypes.TextureFormatRGBA8Unorm,
				// WriteMask MUST be stated explicitly: in the
				// pinned gputypes the zero value of
				// ColorWriteMask is ColorWriteMaskNone, and the
				// project's supported software adapter applies
				// the mask verbatim (color_target.go takes
				// state.WriteMask as-is; the nil-state default
				// never applies because we pass a state). A
				// zero mask silently dropped EVERY fragment
				// write, so every frame kept the load-op clear
				// color and palettes rendered identically
				// (run 42939743766, TestPaletteReachesGPUShader).
				WriteMask: gputypes.ColorWriteMaskAll,
			}},
		},
	})
	if err != nil {
		return fmt.Errorf("render pipeline: %w", err)
	}
	return nil
}

// Uniform layout contract — the single authoritative Go↔WGSL table.
//
// The WGSL Params struct (wgslShader above) is laid out under the current
// WGSL uniform address-space constraints (W3C WGSL §14.4.5,
// https://www.w3.org/TR/2026/CRD-WGSL-20261021/#address-space-layout-constraints):
//
//	Offset  Size  Member     WGSL type          Alignment
//	     0     8  res        vec2f               8
//	     8     4  time       f32                 4
//	    12     4  bass       f32                 4
//	    16     4  beat       f32                 4
//	    20     4  glow       f32                 4
//	    24     4  rotSpeed   f32                 4
//	    32     8  (pad)      vec2f (unused)      8   (28 rounds up to 32)
//	    48   256  bins       array<vec4f, 16>   16
//	   304    16  colLow     vec4f              16
//	   320    16  colMid     vec4f              16
//	   336    16  colHigh    vec4f              16
//	   352    16  colPeak    vec4f              16
//	   368    16  colBack    vec4f              16
//	   384        (end)      struct size          16
//
// Every color is one 16-byte vec4f lane written R,G,B,A (alpha = 1.0).
// The packer below is the ONLY writer of this block; offsets live here and
// in the WGSL contract test, nowhere else.
const (
	uniformSize = 384

	uniformResAt      = 0  // vec2f: width, height
	uniformTimeAt     = 8  // f32
	uniformBassAt     = 12 // f32
	uniformBeatAt     = 16 // f32
	uniformGlowAt     = 20 // f32
	uniformRotSpeedAt = 24 // f32
	uniformPadAt      = 32 // vec2f padding, written as zeros

	uniformBinsAt = 48 // array<vec4f, 16> — 64 interleaved f32s

	uniformColorsAt = 304 // first vec4f color lane (colLow)

	// colorLaneStride is the byte distance between consecutive color
	// lanes: each color is one 16-byte vec4f (R,G,B,A).
	colorLaneStride = 16

	// uniformColorCount is the number of color lanes the shader consumes:
	// colLow, colMid, colHigh, colPeak, colBack in this order.
	uniformColorCount = 5

	// uniformBindMinSize is the bind-group MinBindingSize; it must equal
	// the struct size so a truncation cannot pass validation silently.
	uniformBindMinSize = uniformSize
)

// colorLaneAt returns the byte offset of color lane i (0=colLow …
// 4=colBack). Lanes start at uniformColorsAt, each on its own 16-byte
// boundary.
func colorLaneAt(i int) int { return uniformColorsAt + i*colorLaneStride }

// paletteColors returns the five shader colors of a palette in the exact
// lane order the WGSL struct declares: colLow, colMid, colHigh, colPeak,
// colBack.
func paletteColors(p visualizer.Palette) [uniformColorCount][3]float64 {
	return [uniformColorCount][3]float64{p.Low, p.Mid, p.High, p.Peak, p.Back}
}

// packUniforms fills dst (uniformSize bytes) with the little-endian byte
// sequence the WGSL Params struct consumes for one frame. This is the
// single packer for the whole renderer: no other call site may write
// uniform bytes or restate offsets. dst is fully cleared first, so a
// reused staging buffer can never leak a previous palette's bytes.
// bins are the GAIN-ADJUSTED display values to pack (scene-smoothed in
// production — matching the CPU renderer — or raw inputs in tests);
// the packer does not multiply them again.
func packUniforms(dst []byte, in visualizer.FrameInput, cfg visualizer.Config, pal visualizer.Palette, bins []float64) {
	if len(dst) != uniformSize {
		panic(fmt.Sprintf("render: uniform staging buffer is %d bytes, want %d", len(dst), uniformSize))
	}
	for i := range dst {
		dst[i] = 0
	}
	putF32 := func(off int, v float64) {
		bits := f32bits(v)
		dst[off] = byte(bits)
		dst[off+1] = byte(bits >> 8)
		dst[off+2] = byte(bits >> 16)
		dst[off+3] = byte(bits >> 24)
	}
	putF32(uniformResAt, float64(in.Width))
	putF32(uniformResAt+4, float64(in.Height))
	putF32(uniformTimeAt, in.Time)
	putF32(uniformBassAt, in.Audio.Bass*cfg.Sensitivity)
	// beat pulse approximated by bass impact (WGSL offset 16)
	putF32(uniformBeatAt, in.Audio.Bass*cfg.PulseResponse*0.5)
	putF32(uniformGlowAt, 1.0+cfg.Glow*0.2)
	putF32(uniformRotSpeedAt, 0.15+0.1*float64(cfg.MotionStyle%3))
	// bins: 64 floats; the array starts at 48 because vec4f alignment
	// (16) rounds the vec2f pad (28..35) up to the next multiple of 16.
	if len(bins) < visualizer.NumVizBins {
		bins = visualizer.InterpolateBins(bins, cfg.SpectrumTilt)
	}
	for i, v := range bins {
		if i >= 64 {
			break
		}
		putF32(uniformBinsAt+i*4, v)
	}
	// five 16-byte color lanes, R,G,B,A with alpha 1.0
	cols := paletteColors(pal)
	for ci, c := range cols {
		base := colorLaneAt(ci)
		putF32(base, c[0])
		putF32(base+4, c[1])
		putF32(base+8, c[2])
		putF32(base+12, 1)
	}
}

// Info reports the honest backend state.
func (g *GPURenderer) Info() BackendInfo { return g.info }

// uniformDiagnostics renders a bounded, test-only summary of the uniform
// path: adapter identity/class, the documented layout offsets, the
// staging buffer length and the bytes currently staged for the color
// lanes. It never dumps the whole uniform block or frame memory.
func (g *GPURenderer) uniformDiagnostics() string {
	info := g.Info()
	return fmt.Sprintf(
		"diagnostics: adapter=%q backend=%s class=%s deviceType=%s state=%s detail=%q; uniform len=%d (want %d), bins@%d, colors@%d, laneStride=%d, lanes=%d; staged colLow RGBA=[%v %v %v %v], colBack=[%v %v %v %v]",
		info.Adapter, info.BackendAPI, info.DeviceClass, info.DeviceType, info.GpuState, info.Detail,
		len(g.uniformData), uniformSize,
		uniformBinsAt, uniformColorsAt, colorLaneStride, uniformColorCount,
		f32le(g.uniformData[colorLaneAt(0):]), f32le(g.uniformData[colorLaneAt(0)+4:]),
		f32le(g.uniformData[colorLaneAt(0)+8:]), f32le(g.uniformData[colorLaneAt(0)+12:]),
		f32le(g.uniformData[colorLaneAt(4):]), f32le(g.uniformData[colorLaneAt(4)+4:]),
		f32le(g.uniformData[colorLaneAt(4)+8:]), f32le(g.uniformData[colorLaneAt(4)+12:]),
	)
}

// Close releases GPU resources.
func (g *GPURenderer) Close() {
	g.releaseGPU()
	if g.cpu != nil {
		g.cpu.Close()
	}
}

// releaseGPU releases adapter/device resources deterministically. It is
// used both by Close and by construction failure paths so a partially
// created renderer never leaks a device or adapter.
func (g *GPURenderer) releaseGPU() {
	if g.device != nil {
		if g.readback != nil {
			g.readback.Release()
			g.readback = nil
		}
		if g.target != nil {
			g.target.Release()
			g.target = nil
		}
		if g.bindGroup != nil {
			g.bindGroup.Release()
			g.bindGroup = nil
		}
		if g.uniform != nil {
			g.uniform.Release()
			g.uniform = nil
		}
		if g.pipeline != nil {
			g.pipeline.Release()
			pipeline := g.pipeline
			_ = pipeline
			g.pipeline = nil
		}
		g.device.Release()
		g.device = nil
	}
	if g.adapter != nil {
		g.adapter.Release()
		g.adapter = nil
	}
	if g.instance != nil {
		g.instance.Release()
		g.instance = nil
	}
}

// RenderFrame renders input. The radial mode runs on the GPU; other
// modes run on the equivalent CPU implementation (documented). The
// GPU image is returned after a verified readback.
func (g *GPURenderer) RenderFrame(in visualizer.FrameInput) (*image.RGBA, error) {
	if g.device == nil {
		return g.cpu.RenderFrame(in)
	}
	if in.Config.Mode != visualizer.ModeRadial {
		return g.cpu.RenderFrame(in)
	}
	if in.Width <= 0 || in.Height <= 0 || in.Width > 7680 || in.Height > 4320 {
		return nil, fmt.Errorf("render: invalid frame size %dx%d", in.Width, in.Height)
	}
	if err := g.renderGPUFn(in); err != nil {
		// device loss / adapter failure: report and fall back. A
		// lost device is named explicitly (wgpu.ErrDeviceLost) so
		// the reason is not confused with a transient error.
		if errors.Is(err, wgpu.ErrDeviceLost) {
			g.info.Detail = "device lost: " + err.Error()
		} else {
			g.info.Detail = err.Error()
		}
		g.info.GpuState = GpuFallback
		return g.cpu.RenderFrame(in)
	}
	return g.lastImage, nil
}

// lastImage holds the most recent readback (owned by the renderer).
// (Reused buffer; callers must copy if they retain it.)

// renderGPU uploads uniforms, renders to the offscreen target and
// reads the pixels back.
func (g *GPURenderer) renderGPU(in visualizer.FrameInput) error {
	w, h := in.Width, in.Height
	// Normalize in place (see CPURenderer.RenderFrame): the scene update
	// and the packer must see the same clamped values.
	in.Config.Clamp()
	cfg := in.Config
	pal := paletteFor(cfg.Palette)
	g.info.GpuState = GpuSelected

	// Scene-smoothing parity (run 43541087097): the CPU radial
	// renderer consumes attack/release-smoothed bins from the shared
	// scene state; the shader previously received RAW bins, so the GPU
	// output jittered where the CPU output flowed (and switching
	// backend visibly changed the visuals). The GPU renderer advances
	// its own SceneState — the same deterministic media-time evolution —
	// and packs the smoothed, gain-adjusted values. The advance is
	// GATED on media time: a repeated render of the SAME timestamp
	// (paused preview, repeated export frame, tests) is bit-stable, a
	// backward seek resets the deterministic evolution, and only a
	// forward timeline advances the smoothers. The scene lives on the
	// embedded CPU renderer, which is otherwise unused in this mode;
	// single-goroutine ownership is unchanged.
	st := &g.cpu.scene
	switch {
	case !st.hasLast || in.Time > st.lastTime:
		st.Update(&in)
	case in.Time < st.lastTime:
		st.Reset()
		st.Update(&in)
	}
	for i := range g.sceneBins {
		g.sceneBins[i] = st.Bin(i)
	}

	// build uniform data via the single packer (see the layout table
	// above packUniforms). The staging buffer is reused across frames;
	// packUniforms clears it fully, so no stale palette bytes survive.
	if len(g.uniformData) != uniformSize {
		g.uniformData = make([]byte, uniformSize)
	}
	packUniforms(g.uniformData, in, cfg, pal, g.sceneBins[:])
	if err := g.queue.WriteBuffer(g.uniform, 0, g.uniformData); err != nil {
		return fmt.Errorf("uniform upload: %w", err)
	}

	// target texture (recreate on size change; resources are released)
	if g.target == nil || g.lastW != w || g.lastH != h {
		if g.target != nil {
			g.target.Release()
		}
		if g.readback != nil {
			g.readback.Release()
		}
		tex, err := g.device.CreateTexture(&wgpu.TextureDescriptor{
			Label:         "sheytan-target",
			Size:          wgpu.Extent3D{Width: uint32(w), Height: uint32(h), DepthOrArrayLayers: 1},
			Dimension:     gputypes.TextureDimension2D,
			MipLevelCount: 1,
			SampleCount:   1,
			Format:        gputypes.TextureFormatRGBA8Unorm,
			Usage:         gputypes.TextureUsageRenderAttachment | gputypes.TextureUsageCopySrc,
		})
		if err != nil {
			return fmt.Errorf("target texture: %w", err)
		}
		rowBytes := ((w*4 + 255) / 256) * 256
		rb, err := g.device.CreateBuffer(&wgpu.BufferDescriptor{
			Label: "sheytan-readback", Size: uint64(rowBytes * h),
			Usage: gputypes.BufferUsageCopyDst | gputypes.BufferUsageMapRead,
		})
		if err != nil {
			tex.Release()
			return fmt.Errorf("readback buffer: %w", err)
		}
		g.target, g.readback, g.lastW, g.lastH = tex, rb, w, h
	}
	view, err := g.device.CreateTextureView(g.target, nil)
	if err != nil {
		return fmt.Errorf("texture view: %w", err)
	}
	defer view.Release()

	enc, err := g.device.CreateCommandEncoder(nil)
	if err != nil {
		return fmt.Errorf("command encoder: %w", err)
	}
	pass, err := enc.BeginRenderPass(&wgpu.RenderPassDescriptor{
		Label: "sheytan-frame",
		ColorAttachments: []wgpu.RenderPassColorAttachment{{
			View:       view,
			LoadOp:     gputypes.LoadOpClear,
			StoreOp:    gputypes.StoreOpStore,
			ClearValue: gputypes.Color{R: 0.03, G: 0.03, B: 0.03, A: 1},
		}},
	})
	if err != nil {
		return fmt.Errorf("render pass: %w", err)
	}
	pass.SetPipeline(g.pipeline)
	pass.SetBindGroup(0, g.bindGroup, nil)
	pass.Draw(gputypes.DrawArgs{VertexCount: 3, InstanceCount: 1})
	if err := pass.End(); err != nil {
		return fmt.Errorf("pass end: %w", err)
	}
	rowBytes := ((w*4 + 255) / 256) * 256
	enc.CopyTextureToBuffer(
		g.target,
		g.readback,
		[]wgpu.BufferTextureCopy{{
			BufferLayout: wgpu.ImageDataLayout{
				BytesPerRow:  uint32(rowBytes),
				RowsPerImage: uint32(h),
			},
			TextureBase: wgpu.ImageCopyTexture{Texture: g.target, Aspect: gputypes.TextureAspectAll},
			Size:        wgpu.Extent3D{Width: uint32(w), Height: uint32(h), DepthOrArrayLayers: 1},
		}},
	)
	cb, err := enc.Finish()
	if err != nil {
		return fmt.Errorf("encoder finish: %w", err)
	}
	if _, err := g.queue.Submit(cb); err != nil {
		return fmt.Errorf("submit: %w", err)
	}
	g.info.GpuState = GpuExecuted

	// map and read back
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := g.readback.Map(ctx, wgpu.MapModeRead, 0, g.readback.Size()); err != nil {
		return fmt.Errorf("map: %w", err)
	}
	mr, err := g.readback.MappedRange(0, g.readback.Size())
	if err != nil {
		_ = g.readback.Unmap()
		return fmt.Errorf("mapped range: %w", err)
	}
	raw := mr.Bytes()
	if len(raw) < rowBytes*h {
		_ = g.readback.Unmap()
		return fmt.Errorf("readback short: %d < %d", len(raw), rowBytes*h)
	}
	// A fresh image per frame: callers (export encoder, Fyne canvas)
	// may retain the returned image past the next call, so the readback
	// buffer itself must not be handed out twice. The per-frame
	// allocation is bounded by the capped frame size.
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		src := raw[y*rowBytes : y*rowBytes+w*4]
		copy(img.Pix[y*img.Stride:y*img.Stride+w*4], src)
	}
	if err := g.readback.Unmap(); err != nil {
		return fmt.Errorf("unmap: %w", err)
	}
	// Overlay parity (run 43541087097): the CPU renderer composites the
	// title/subtitle/watermark onto every mode; the GPU path previously
	// returned the bare shader image, so a GPU machine exported videos
	// WITHOUT the configured overlays. The same overlay pass now runs
	// on the readback image — identical text, identical placement.
	ov := newCanvasWith(img, &g.cpu.powScratch)
	g.cpu.drawOverlays(ov, in)
	g.lastImage = img
	g.info.GpuState = GpuVerified
	return nil
}

// readbackCompare is used by the verification test to compare a
// rendered frame against the CPU reference within tolerance.
func readbackCompare(a, b []byte, tol int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		d := int(a[i]) - int(b[i])
		if d < -tol || d > tol {
			return false
		}
	}
	return true
}

func containsFold(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if eqFold(s[i:i+len(sub)], sub) {
			return true
		}
	}
	return false
}

func eqFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

// isHardwareBackend reports whether a backend API is a real graphics
// driver (noop/software adapters never count as GPU execution).
func isHardwareBackend(api string) bool {
	switch api {
	case "vulkan", "dx12", "metal", "gl":
		return true
	default:
		return false
	}
}
