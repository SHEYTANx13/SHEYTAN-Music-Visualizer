package render

// cpu_layer_test.go — layered composition fixtures (roadmap Phase 2
// #6: "Layered compositions: NOT DONE"). The background layer must
// (a) visibly change the frame, (b) stay deterministic, and (c) leave
// the layer-free render path byte-identical to the historical output.

import (
	"image"
	"testing"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/visualizer"
)

func layerTestInput(mode visualizer.Mode, layer visualizer.Mode, intensity float64) visualizer.FrameInput {
	return visualizer.FrameInput{
		FrameIndex: 3,
		Time:       3.0 / 30.0,
		Width:      128,
		Height:     96,
		Config: visualizer.Config{
			Mode: mode, Sensitivity: 1.2, Smoothing: 0.2,
			Glow: 0.6, Palette: 0, PulseResponse: 1.2,
			BackgroundLayer:     layer,
			BackgroundIntensity: intensity,
			ShowTitle:           false, ShowWatermark: false,
		},
		Audio: visualizer.AudioState{
			Bass: 0.7, Mid: 0.35, Treble: 0.15,
			Bins: testBins(),
		},
	}
}

// meanLuma is a crude brightness measure to assert that a layer adds
// energy to the frame.
func meanLuma(img *image.RGBA) float64 {
	var sum float64
	n := 0
	for y := 0; y < img.Bounds().Dy(); y++ {
		row := img.Pix[y*img.Stride:]
		for x := 0; x < img.Bounds().Dx(); x++ {
			o := x * 4
			sum += 0.299*float64(row[o]) + 0.587*float64(row[o+1]) + 0.114*float64(row[o+2])
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return sum / float64(n)
}

func TestBackgroundLayerChangesFrame(t *testing.T) {
	r := NewCPURenderer()
	defer r.Close()

	base, err := r.RenderFrame(layerTestInput(visualizer.ModeBars, visualizer.ModeNone, 0))
	if err != nil {
		t.Fatalf("base render: %v", err)
	}
	baseLuma := meanLuma(base)

	layered, err := r.RenderFrame(layerTestInput(visualizer.ModeBars, visualizer.ModeWaveform, 0.45))
	if err != nil {
		t.Fatalf("layered render: %v", err)
	}
	layeredLuma := meanLuma(layered)
	if layeredLuma <= baseLuma {
		t.Fatalf("layered frame not brighter than base (%.2f <= %.2f) — the background layer did not draw",
			layeredLuma, baseLuma)
	}
}

func TestBackgroundLayerDeterministic(t *testing.T) {
	mk := func() *image.RGBA {
		r := NewCPURenderer()
		defer r.Close()
		img, err := r.RenderFrame(layerTestInput(visualizer.ModeRadial, visualizer.ModeBars, 0.35))
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		cp := image.NewRGBA(img.Bounds())
		copy(cp.Pix, img.Pix)
		return cp
	}
	a, b := mk(), mk()
	if !pixEqual(a, b) {
		t.Fatal("layered render is not deterministic across fresh renderers")
	}
}

func pixEqual(a, b *image.RGBA) bool {
	if a.Bounds() != b.Bounds() {
		return false
	}
	for i := range a.Pix {
		if a.Pix[i] != b.Pix[i] {
			return false
		}
	}
	return true
}

// The layer-free path must be byte-identical whether the layer is
// unset ("") or explicitly "none" — the historical output is the
// contract.
func TestLayerFreePathUnchanged(t *testing.T) {
	mk := func(layer visualizer.Mode) *image.RGBA {
		r := NewCPURenderer()
		defer r.Close()
		in := layerTestInput(visualizer.ModeWaveform, layer, 0.5)
		img, err := r.RenderFrame(in)
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		cp := image.NewRGBA(img.Bounds())
		copy(cp.Pix, img.Pix)
		return cp
	}
	if !pixEqual(mk(""), mk(visualizer.ModeNone)) {
		t.Fatal("empty and explicit-none layer configs must render identically")
	}
}

// Every mode works as a background layer without panics or errors.
func TestAllModesUsableAsBackgroundLayer(t *testing.T) {
	for _, m := range visualizer.AllModes {
		r := NewCPURenderer()
		img, err := r.RenderFrame(layerTestInput(visualizer.ModeBars, m, 0.4))
		r.Close()
		if err != nil {
			t.Fatalf("background layer %s: %v", m, err)
		}
		if img == nil {
			t.Fatalf("background layer %s: nil image", m)
		}
	}
}
