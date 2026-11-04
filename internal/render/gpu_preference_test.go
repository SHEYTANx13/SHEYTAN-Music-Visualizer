package render

// gpu_preference_test.go — tests for the backend preference model, the
// uniform layout contract between the Go side and the WGSL Params
// struct, and honest fallback reporting. The noop adapter (imported in
// tests only) proves the pipeline; it is never reported as hardware.

import (
	"image"
	"testing"

	"github.com/gogpu/gputypes"
	"github.com/gogpu/wgpu"
	_ "github.com/gogpu/wgpu/hal/noop"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/visualizer"
)

// TestUniformLayoutMatchesWGSL pins the Go/WGSL uniform contract. The
// WGSL Params struct is: scalars 0..27, vec2f pad at 28..35, then the
// vec4f array — vec4 alignment of 16 forces the array to offset 48 —
// and five vec4 colors at 304..383. The previous code wrote bins at 32
// and colors at 288, silently misaligning the spectrum and palette.
func TestUniformLayoutMatchesWGSL(t *testing.T) {
	// scalars: res(2x4) time bass beat glow rotSpeed(2x4) = 28 bytes
	const scalarBytes = 8 + 4 + 4 + 4 + 4 + 4 // res, time, bass, beat, glow, rotSpeed
	if scalarBytes != 28 {
		t.Fatalf("scalar region = %d, want 28", scalarBytes)
	}
	// pad: vec2f at 28 occupies 28..35; the vec4f array must start at
	// the next multiple of vec4 alignment (16) => 48.
	if uniformBinsAt != 48 {
		t.Fatalf("uniformBinsAt = %d, want 48 (vec4f alignment)", uniformBinsAt)
	}
	// 16 vec4 bins => 256 bytes: 48+256 = 304.
	if uniformColorsAt != uniformBinsAt+16*16 {
		t.Fatalf("uniformColorsAt = %d, want %d (after the 16x vec4f bin array)", uniformColorsAt, uniformBinsAt+256)
	}
	// 5 vec4 colors => 384 total.
	if uniformSize != uniformColorsAt+5*16 {
		t.Fatalf("uniformSize = %d, want %d", uniformSize, uniformColorsAt+80)
	}
}

// TestPaletteReachesGPUShader proves the color uniforms are read at the
// correct offsets: two different palettes must produce different frames
// for identical geometry input on the noop adapter.
func TestPaletteReachesGPUShader(t *testing.T) {
	g := newGPURendererFor(&wgpu.RequestAdapterOptions{ForceFallbackAdapter: true})
	if g == nil {
		t.Skip("noop fallback adapter unavailable in this build")
	}
	defer g.Close()

	renderWith := func(palette int) *image.RGBA {
		in := visualizer.FrameInput{
			FrameIndex: 3, Time: 0.1, Width: 64, Height: 64,
			Config: visualizer.Config{
				Mode: visualizer.ModeRadial, Sensitivity: 1, Glow: 0.5,
				Palette: palette, PulseResponse: 1,
			},
			Audio: visualizer.AudioState{Bass: 0.7, Mid: 0.3, Treble: 0.1, Bins: testBins()},
		}
		img, err := g.RenderFrame(in)
		if err != nil {
			t.Fatalf("RenderFrame palette %d: %v", palette, err)
		}
		if g.Info().GpuState != GpuVerified {
			t.Fatalf("palette %d: expected VERIFIED, got %s (%s)", palette, g.Info().GpuState, g.Info().Detail)
		}
		return img
	}
	a := renderWith(0)
	b := renderWith(1)
	if samePixels(a, b) {
		t.Fatal("palettes 0 and 1 produced identical GPU frames; color uniforms are not reaching the shader at the correct offsets")
	}
}

// TestPreferenceParsing covers validation and the env contract.
func TestPreferenceParsing(t *testing.T) {
	for in, want := range map[string]Preference{
		"": PrefAuto, "auto": PrefAuto, " AUTO ": PrefAuto,
		"vulkan": PrefVulkan, "Vulkan": PrefVulkan,
		"dx12": PrefDX12, "DX12": PrefDX12,
		"software": PrefSoftware,
	} {
		got, err := ParsePreference(in)
		if err != nil || got != want {
			t.Fatalf("ParsePreference(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := ParsePreference("metal"); err == nil {
		t.Fatal("unknown preference must be an error, never silently auto")
	}

	t.Setenv("SHEYTAN_GPU_PREFERENCE", "")
	p, set, err := PreferenceFromEnv()
	if err != nil || set || p != PrefAuto {
		t.Fatalf("empty env: p=%v set=%v err=%v", p, set, err)
	}
	t.Setenv("SHEYTAN_GPU_PREFERENCE", "vulkan")
	p, set, err = PreferenceFromEnv()
	if err != nil || !set || p != PrefVulkan {
		t.Fatalf("vulkan env: p=%v set=%v err=%v", p, set, err)
	}
	t.Setenv("SHEYTAN_GPU_PREFERENCE", "nonsense")
	if _, _, err := PreferenceFromEnv(); err == nil {
		t.Fatal("malformed env value must surface an error")
	}
}

// TestSoftwarePreferenceNeverLabelsHardware proves an explicitly
// requested software adapter executes the real pipeline but is never
// classified as hardware — through either the direct info or
// SelectRenderer's refusal.
func TestSoftwarePreferenceNeverLabelsHardware(t *testing.T) {
	g, info := NewGPURendererWithPreference(PrefSoftware)
	if g == nil {
		t.Skip("software/fallback adapter unavailable in this build")
	}
	defer g.Close()
	if info.BackendAPI == "" || isHardwareBackend(info.BackendAPI) {
		t.Fatalf("software preference must not be labeled hardware: %+v", info)
	}
	if !isSoftwareLabel(info.BackendAPI) {
		t.Fatalf("software adapter must carry a software label, got %q", info.BackendAPI)
	}
	if info.DeviceType != "cpu" && info.DeviceType != "other" {
		t.Fatalf("software adapter device type must be cpu/other, got %q", info.DeviceType)
	}
	// The pipeline still executes for real on the software adapter.
	in := visualizer.FrameInput{
		FrameIndex: 1, Time: 1.0 / 30, Width: 48, Height: 48,
		Config: visualizer.Config{Mode: visualizer.ModeRadial, Sensitivity: 1},
		Audio:  visualizer.AudioState{Bass: 0.5, Bins: testBins()},
	}
	img, err := g.RenderFrame(in)
	if err != nil {
		t.Fatalf("software render: %v", err)
	}
	if imageUniform(img) {
		t.Fatal("software adapter produced a uniform frame; pipeline did not execute")
	}
}

// TestSelectRendererReportsHonestReason checks the contract that the
// selected backend always carries either an adapter identity or the
// exact unavailability reason — never a bare, silent CPU fallback.
func TestSelectRendererReportsHonestReason(t *testing.T) {
	r, info := SelectRenderer()
	defer r.Close()
	switch info.Backend {
	case BackendGPU:
		if !isHardwareBackend(info.BackendAPI) {
			t.Fatalf("GPU selection must be a hardware backend, got %q", info.BackendAPI)
		}
	case BackendCPU:
		if info.Detail == "" {
			t.Fatal("CPU fallback must record why the GPU path was unavailable")
		}
	default:
		t.Fatalf("unexpected backend %q", info.Backend)
	}
}

// TestExplicitBackendMaskContract documents the mapping from
// preference to instance backend mask for the pinned wgpu version.
func TestExplicitBackendMaskContract(t *testing.T) {
	if instanceBackendsFor(PrefVulkan) != 1<<uint(gputypes.BackendVulkan) {
		t.Fatal("vulkan preference must restrict the instance to the Vulkan backend")
	}
	if instanceBackendsFor(PrefDX12) != 1<<uint(gputypes.BackendDX12) {
		t.Fatal("dx12 preference must restrict the instance to the DX12 backend")
	}
	if instanceBackendsFor(PrefAuto) == 0 {
		t.Fatal("auto preference must keep primary backends enabled")
	}
}
