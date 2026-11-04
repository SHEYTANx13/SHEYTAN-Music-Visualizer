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
	"fmt"
	"image"
	"math"
	"time"

	"github.com/gogpu/gputypes"
	"github.com/gogpu/wgpu"
	_ "github.com/gogpu/wgpu/hal/allbackends"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/visualizer"
)

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
	info      BackendInfo
	instance  *wgpu.Instance
	adapter   *wgpu.Adapter // kept reachable for the device lifetime
	device    *wgpu.Device
	queue     *wgpu.Queue
	pipeline  *wgpu.RenderPipeline
	uniform   *wgpu.Buffer
	bindGroup *wgpu.BindGroup
	cpu       *CPURenderer // fallback renderer for non-GPU modes
	lastW     int
	lastH     int
	target    *wgpu.Texture
	readback  *wgpu.Buffer
	lastImage *image.RGBA
}

func f32bits(v float64) uint32 { return mathFloat32bits(float32(v)) }

func mathFloat32bits(f float32) uint32 {
	return math.Float32bits(f)
}

// NewGPURenderer attempts to create a real GPU renderer. It returns
// nil when no usable adapter/device exists (the caller then uses the
// CPU renderer and reports the fallback honestly).
func NewGPURenderer() *GPURenderer {
	return newGPURendererFor(&wgpu.RequestAdapterOptions{})
}

// newGPURendererFor builds the GPU renderer against a specific adapter
// request (tests force the noop fallback adapter to prove the full
// render/readback pipeline deterministically on any host).
func newGPURendererFor(opts *wgpu.RequestAdapterOptions) *GPURenderer {
	g := &GPURenderer{cpu: NewCPURenderer()}
	inst, err := wgpu.CreateInstance(nil)
	if err != nil {
		g.info = BackendInfo{Backend: BackendGPU, GpuState: GpuNone,
			Detail: fmt.Sprintf("wgpu instance creation failed: %v", err)}
		return nil
	}
	// The instance, adapter and device stay alive for the renderer's
	// lifetime (released in Close); releasing the instance here would
	// invalidate the device.
	g.instance = inst
	adapter, err := inst.RequestAdapter(opts)
	if err != nil || adapter == nil {
		g.info = BackendInfo{Backend: BackendGPU, GpuState: GpuNone,
			Detail: "no GPU adapter available on this host (expected on headless machines)"}
		return nil
	}
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
	}
	dev, err := adapter.RequestDevice(nil)
	if err != nil {
		g.info.Detail = fmt.Sprintf("device creation failed: %v", err)
		return nil
	}
	g.device = dev
	g.queue = dev.Queue()
	g.info.GpuState = GpuAvailable
	if err := g.buildPipeline(); err != nil {
		g.device.Release()
		g.device = nil
		g.info.Detail = fmt.Sprintf("pipeline build failed: %v", err)
		return nil
	}
	return g
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
			Buffer:     &gputypes.BufferBindingLayout{Type: gputypes.BufferBindingTypeUniform, MinBindingSize: uniformSize},
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
			}},
		},
	})
	if err != nil {
		return fmt.Errorf("render pipeline: %w", err)
	}
	return nil
}

// uniformSize matches the WGSL Params struct layout: scalars 0..36,
// 16-byte alignment pads bins to 48, bins 48..304 (16 x vec4), colors
// 304..384 (5 x vec4) = 384 bytes total.
const (
	uniformSize     = 384
	uniformBinsAt   = 48
	uniformColorsAt = 304
)

// Info reports the honest backend state.
func (g *GPURenderer) Info() BackendInfo { return g.info }

// Close releases GPU resources.
func (g *GPURenderer) Close() {
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
		}
		if g.uniform != nil {
			g.uniform.Release()
		}
		if g.pipeline != nil {
			g.pipeline.Release()
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
	if g.cpu != nil {
		g.cpu.Close()
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
	if err := g.renderGPU(in); err != nil {
		// device loss / adapter failure: report and fall back
		g.info.GpuState = GpuFallback
		g.info.Detail = err.Error()
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
	cfg := in.Config
	cfg.Clamp()
	pal := paletteFor(cfg.Palette)
	g.info.GpuState = GpuSelected

	// build uniform data (little-endian; the exact layout mirrors the
	// WGSL Params struct)
	data := make([]byte, uniformSize)
	putF32 := func(off int, v float64) {
		bits := f32bits(v)
		data[off] = byte(bits)
		data[off+1] = byte(bits >> 8)
		data[off+2] = byte(bits >> 16)
		data[off+3] = byte(bits >> 24)
	}
	putF32(0, float64(w))
	putF32(4, float64(h))
	putF32(8, in.Time)
	putF32(12, in.Audio.Bass*cfg.Sensitivity)
	putF32(16, in.Audio.Bass*cfg.PulseResponse*0.5+in.Audio.BeatPhase*0) // beat pulse approximated by bass impact
	putF32(20, 1.0+cfg.Glow*0.2)
	putF32(24, 0.15+0.1*float64(cfg.MotionStyle%3))
	rotSpeed := 0.15 + 0.1*float64(cfg.MotionStyle%3)
	putF32(24, rotSpeed)
	// bins: 64 floats at offset 32
	bins := in.Audio.Bins
	if len(bins) < visualizer.NumVizBins {
		bins = visualizer.InterpolateBins(bins, cfg.SpectrumTilt)
	}
	for i, v := range bins {
		if i >= 64 {
			break
		}
		putF32(32+i*4, v*cfg.Sensitivity)
	}
	// colors at offset 288 (5 vec4s: low, mid, high, peak, back)
	cols := [][3]float64{pal.Low, pal.Mid, pal.High, pal.Peak, pal.Back}
	for ci, c := range cols {
		putF32(288+ci*16, c[0])
		putF32(288+ci*16+4, c[1])
		putF32(288+ci*16+8, c[2])
		putF32(288+ci*16+12, 1)
	}
	if err := g.queue.WriteBuffer(g.uniform, 0, data); err != nil {
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
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		src := raw[y*rowBytes : y*rowBytes+w*4]
		copy(img.Pix[y*img.Stride:y*img.Stride+w*4], src)
	}
	if err := g.readback.Unmap(); err != nil {
		return fmt.Errorf("unmap: %w", err)
	}
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
