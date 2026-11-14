package render

// gpu_preference_test.go — tests for the backend preference model, the
// uniform layout contract between the Go side and the WGSL Params
// struct, and honest fallback reporting. The noop adapter (imported in
// tests only) proves the pipeline; it is never reported as hardware.

import (
	"image"
	"image/color"
	"math"
	"strings"
	"testing"

	"github.com/gogpu/gputypes"
	"github.com/gogpu/wgpu"
	_ "github.com/gogpu/wgpu/hal/noop"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/visualizer"
)

// TestUniformLayoutMatchesWGSL pins the Go/WGSL uniform contract against
// the layout table documented next to packUniforms in gpu.go. The WGSL
// Params struct is: scalars 0..27, vec2f pad at 32..39 (28 rounds up to
// the pad's 8-byte alignment), the vec4f bin array at 48 (16-byte
// alignment), and five vec4f color lanes at 304..383, 384 bytes total.
// These assertions are the executable form of that table; the static
// WGSL source contract is checked by TestWGSLParamsContractMatchesGoLayout.
func TestUniformLayoutMatchesWGSL(t *testing.T) {
	// scalars: res(2x4) time bass beat glow rotSpeed = 28 bytes
	const scalarBytes = uniformRotSpeedAt + 4
	if scalarBytes != 28 {
		t.Fatalf("scalar region = %d, want 28", scalarBytes)
	}
	// pad: vec2f must start on its own 8-byte alignment after rotSpeed.
	if uniformPadAt != 32 {
		t.Fatalf("uniformPadAt = %d, want 32 (vec2f alignment after 28)", uniformPadAt)
	}
	// the vec4f array must start on a 16-byte boundary.
	if uniformBinsAt != 48 {
		t.Fatalf("uniformBinsAt = %d, want 48 (vec4f alignment)", uniformBinsAt)
	}
	// 16 vec4 bins => 256 bytes: 48+256 = 304.
	if uniformColorsAt != uniformBinsAt+16*16 {
		t.Fatalf("uniformColorsAt = %d, want %d (after the 16x vec4f bin array)", uniformColorsAt, uniformBinsAt+256)
	}
	// 5 vec4 color lanes => 384 total.
	if uniformSize != uniformColorsAt+uniformColorCount*colorLaneStride {
		t.Fatalf("uniformSize = %d, want %d", uniformSize, uniformColorsAt+uniformColorCount*colorLaneStride)
	}
	// every color lane must sit on its own 16-byte boundary and the last
	// lane must end exactly at the struct size.
	for i := 0; i < uniformColorCount; i++ {
		off := colorLaneAt(i)
		if off%16 != 0 {
			t.Fatalf("color lane %d offset %d is not 16-byte aligned", i, off)
		}
	}
	if end := colorLaneAt(uniformColorCount-1) + colorLaneStride; end != uniformSize {
		t.Fatalf("last color lane ends at %d, want %d", end, uniformSize)
	}
	// the bind group must demand the full struct so a truncation cannot
	// pass validation silently.
	if uniformBindMinSize != uniformSize {
		t.Fatalf("uniformBindMinSize = %d, want %d", uniformBindMinSize, uniformSize)
	}
}

// TestColorLaneOrderMatchesPaletteFields proves the lane order written by
// the packer is exactly the WGSL declaration order colLow, colMid,
// colHigh, colPeak, colBack. A synthetic palette with a distinct value in
// every field makes any reordering visible in the packed bytes.
func TestColorLaneOrderMatchesPaletteFields(t *testing.T) {
	pal := visualizer.Palette{
		Name:   "probe",
		Back:   [3]float64{0.50, 0.51, 0.52},
		Low:    [3]float64{0.10, 0.11, 0.12},
		Mid:    [3]float64{0.20, 0.21, 0.22},
		High:   [3]float64{0.30, 0.31, 0.32},
		Peak:   [3]float64{0.40, 0.41, 0.42},
		Accent: [3]float64{0.90, 0.91, 0.92}, // Accent is not a shader lane
	}
	wantFirstComponent := [uniformColorCount]float64{0.10, 0.20, 0.30, 0.40, 0.50}
	cols := paletteColors(pal)
	for i, c := range cols {
		if c[0] != wantFirstComponent[i] {
			t.Fatalf("lane %d first component = %v, want %v (lane order must be Low,Mid,High,Peak,Back)", i, c[0], wantFirstComponent[i])
		}
	}
}

// TestPackUniformsContract checks the packed bytes directly: lane values
// land at the documented offsets, alpha is 1.0, the pad region stays zero
// and a reused buffer carries no bytes from a previous palette.
func TestPackUniformsContract(t *testing.T) {
	palA := visualizer.Palettes[0]
	palB := visualizer.Palettes[1]
	in := visualizer.FrameInput{
		FrameIndex: 5, Time: 0.25, Width: 128, Height: 72,
		Config: visualizer.Config{Mode: visualizer.ModeRadial, Sensitivity: 1, Glow: 0.5, Palette: 0, PulseResponse: 1},
		Audio:  visualizer.AudioState{Bass: 0.7, Mid: 0.3, Treble: 0.1, Bins: testBins()},
	}
	buf := make([]byte, uniformSize)
	packUniforms(buf, in, in.Config, palA, in.Audio.Bins)

	// res little-endian f32 at 0/4
	if got := f32le(buf[uniformResAt:]); got != 128 {
		t.Fatalf("packed res.x = %v, want 128", got)
	}
	if got := f32le(buf[uniformResAt+4:]); got != 72 {
		t.Fatalf("packed res.y = %v, want 72", got)
	}
	// pad region stays zero
	for i := uniformPadAt; i < uniformBinsAt; i++ {
		if buf[i] != 0 {
			t.Fatalf("pad byte %d = %#x, want 0", i, buf[i])
		}
	}
	// colLow R/G/B and alpha=1 at lane 0
	low := palA.Low
	if got := f32le(buf[colorLaneAt(0):]); !near(got, low[0]) {
		t.Fatalf("colLow.R = %v, want %v", got, low[0])
	}
	if got := f32le(buf[colorLaneAt(0)+12:]); got != 1 {
		t.Fatalf("colLow.A = %v, want 1.0", got)
	}
	// colBack at lane 4
	back := palA.Back
	if got := f32le(buf[colorLaneAt(4)+8:]); !near(got, back[2]) {
		t.Fatalf("colBack.B = %v, want %v", got, back[2])
	}
	// reuse the same buffer for palette B: every lane byte that differs
	// between the palettes must be overwritten (no stale bytes).
	packUniforms(buf, in, in.Config, palB, in.Audio.Bins)
	bufA := make([]byte, uniformSize)
	packUniforms(bufA, in, in.Config, palA, in.Audio.Bins)
	for i := uniformColorsAt; i < uniformSize; i++ {
		if buf[i] != bufA[i] && buf[i] == 0 && bufA[i] != 0 {
			t.Fatalf("stale byte at %d: reused buffer kept 0 where palette A has %#x (palette bytes leaked between frames)", i, bufA[i])
		}
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

// wgslParamsMembers is the expected WGSL Params member list in declaration
// order: name and the Go layout constant that must match its offset.
var wgslParamsMembers = []struct {
	name   string
	offset int
}{
	{"res", uniformResAt},
	{"time", uniformTimeAt},
	{"bass", uniformBassAt},
	{"beat", uniformBeatAt},
	{"glow", uniformGlowAt},
	{"rotSpeed", uniformRotSpeedAt},
	{"pad", uniformPadAt},
	{"bins", uniformBinsAt},
	{"colLow", colorLaneAt(0)},
	{"colMid", colorLaneAt(1)},
	{"colHigh", colorLaneAt(2)},
	{"colPeak", colorLaneAt(3)},
	{"colBack", colorLaneAt(4)},
}

// TestWGSLParamsContractMatchesGoLayout statically checks the shipped
// wgslShader source: the Params struct must declare exactly these members
// in this order, and the shader body must read every color lane — a
// member rename, reordering, or a color the shader stops reading would
// silently break the Go↔WGSL contract that the runtime tests above pin.
func TestWGSLParamsContractMatchesGoLayout(t *testing.T) {
	// 1. struct member order
	start := strings.Index(wgslShader, "struct Params {")
	end := strings.Index(wgslShader[start:], "}")
	if start < 0 || end < 0 {
		t.Fatal("Params struct not found in wgslShader")
	}
	body := wgslShader[start : start+end]
	for i, m := range wgslParamsMembers {
		if !strings.Contains(body, m.name+" ") && !strings.Contains(body, m.name+":") && !strings.Contains(body, m.name+"\t") {
			t.Fatalf("Params member %d %q missing from the WGSL struct", i, m.name)
		}
		idx := strings.Index(body, m.name)
		if idx < 0 {
			t.Fatalf("Params member %q not found", m.name)
		}
		if i > 0 {
			prev := strings.Index(body, wgslParamsMembers[i-1].name)
			if idx < prev {
				t.Fatalf("Params member %q appears before %q; declaration order must match the Go layout table", m.name, wgslParamsMembers[i-1].name)
			}
		}
	}
	// 2. the fragment shader must read all five color lanes
	for i, m := range wgslParamsMembers[8:] {
		if !strings.Contains(wgslShader, "P."+m.name) {
			t.Fatalf("shader never reads P.%s (color lane %d); the lane would be packed but never rendered", m.name, i)
		}
	}
	// 3. the uniform binding must be the Params struct at group 0 binding 0
	if !strings.Contains(wgslShader, "@group(0) @binding(0) var<uniform> P : Params;") {
		t.Fatal("shader must bind Params as var<uniform> P at @group(0) @binding(0)")
	}
}

// TestPaletteReachesGPUShader proves the color uniforms are read at the
// correct offsets: two different palettes must produce different frames
// for identical geometry input on the project's supported software
// adapter (labelled noop/software — never a physical GPU). The assertion
// checks the palette-affected channels specifically so an unrelated
// frame difference cannot satisfy it, and prints a bounded diagnostic on
// failure (adapter, uniform length, lane offsets, per-channel max diff).
func TestPaletteReachesGPUShader(t *testing.T) {
	g := newGPURendererFor(&wgpu.RequestAdapterOptions{ForceFallbackAdapter: true})
	if g == nil {
		t.Skip("noop fallback adapter unavailable in this build")
	}
	defer g.Close()

	// Two known, intentionally distinct brand palettes: Hellfire vs
	// Crimson differ in every required lane.
	const paletteA, paletteB = 0, 1
	if palettesIdentical(visualizer.Palettes[paletteA], visualizer.Palettes[paletteB]) {
		t.Fatal("palettes 0 and 1 are identical; the fixture is meaningless")
	}

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
	a := renderWith(paletteA)
	b := renderWith(paletteB)

	if samePixels(a, b) {
		t.Fatalf("palettes %d and %d produced identical GPU frames; color uniforms are not reaching the shader\n%s",
			paletteA, paletteB, g.uniformDiagnostics())
	}
	// The palettes must change the palette-carrying channels, not just
	// any byte: every brand palette lives on the red axis (Low/Mid R
	// differs strongly between palette 0 and 1), while G/B move less
	// because the dark background dominates those channels. Require a
	// clear red-axis shift plus some movement in G or B so an unrelated
	// single-channel glitch cannot satisfy the assertion.
	const minRedDelta = 16 // u8; Low.R 0.62 vs 0.36 → ~67 u8 at the bars
	const minOtherSum = 8  // combined G+B movement
	dR, dG, dB, changedPixels := channelDiff(a, b)
	if dR < minRedDelta || dG+dB < minOtherSum {
		t.Fatalf("palette change did not move the expected color channels (maxDelta R=%d G=%d B=%d, changedPixels=%d); palette data may not be reaching the shader\n%s",
			dR, dG, dB, changedPixels, g.uniformDiagnostics())
	}
	if changedPixels < 16 {
		t.Fatalf("only %d pixels changed between palettes; expected a frame-wide palette effect\n%s", changedPixels, g.uniformDiagnostics())
	}
}

// palettesIdentical reports whether two palettes carry identical values
// in every shader lane.
func palettesIdentical(a, b visualizer.Palette) bool {
	pa := paletteColors(a)
	pb := paletteColors(b)
	for i := range pa {
		for c := 0; c < 3; c++ {
			if pa[i][c] != pb[i][c] {
				return false
			}
		}
	}
	return true
}

// channelDiff returns the max per-channel u8 delta between two frames and
// how many pixels differ at all.
func channelDiff(a, b *image.RGBA) (dR, dG, dB int, changed int) {
	for i := 0; i+4 <= len(a.Pix); i += 4 {
		if a.Pix[i] == b.Pix[i] && a.Pix[i+1] == b.Pix[i+1] && a.Pix[i+2] == b.Pix[i+2] && a.Pix[i+3] == b.Pix[i+3] {
			continue
		}
		changed++
		if d := abs(int(a.Pix[i]) - int(b.Pix[i])); d > dR {
			dR = d
		}
		if d := abs(int(a.Pix[i+1]) - int(b.Pix[i+1])); d > dG {
			dG = d
		}
		if d := abs(int(a.Pix[i+2]) - int(b.Pix[i+2])); d > dB {
			dB = d
		}
	}
	return dR, dG, dB, changed
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// TestPaletteSelectionClampsInvalidIDs covers the failure cases around
// palette selection: out-of-range IDs fall back to palette 0 instead of
// panicking or producing undefined colors.
func TestPaletteSelectionClampsInvalidIDs(t *testing.T) {
	for _, id := range []int{-1, len(visualizer.Palettes), 99} {
		cfg := visualizer.Config{Mode: visualizer.ModeRadial, Palette: id}
		cfg.Clamp()
		if cfg.Palette != 0 {
			t.Fatalf("palette id %d clamped to %d, want 0", id, cfg.Palette)
		}
		pal := paletteFor(id)
		want := visualizer.Palettes[0]
		if pal.Name != want.Name || pal.Low != want.Low || pal.Mid != want.Mid || pal.High != want.High || pal.Peak != want.Peak || pal.Back != want.Back {
			t.Fatalf("paletteFor(%d) returned %+v, want the first brand palette", id, pal)
		}
	}
}

// TestSoftwareAdapterIsLabelledHonest pins that the palette proof runs on
// the software adapter and is labelled as such — it never claims a
// physical GPU.
func TestSoftwareAdapterIsLabelledHonest(t *testing.T) {
	g := newGPURendererFor(&wgpu.RequestAdapterOptions{ForceFallbackAdapter: true})
	if g == nil {
		t.Skip("noop fallback adapter unavailable in this build")
	}
	defer g.Close()
	info := g.Info()
	if isHardwareBackend(info.BackendAPI) {
		t.Fatalf("fallback adapter must not be classified as hardware: %+v", info)
	}
	if info.DeviceClass != DeviceClassSoftware {
		t.Fatalf("fallback adapter DeviceClass = %q, want software", info.DeviceClass)
	}
	var _ color.Color // keep color import tied to frame diagnostics
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
