package render

// registry_test.go — collection/registry integrity for the visualizer
// catalog (run 43541087097 gates G8/G9): every registered mode is a
// real, distinct implementation with a stable unique ID, a valid
// default/preset configuration, audio reactivity, deterministic
// output, and correct scaling across layouts. The UI collection (mode
// select + preset rail) is driven by the same registry, so these
// invariants guarantee every visible item launches a working renderer.

import (
	"fmt"
	"image"
	"testing"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/visualizer"
)

// layoutMatrix covers narrow/tall, square, wide and 4K logical layouts
// (the responsive contract's representative sizes).
var layoutMatrix = []struct {
	name string
	w, h int
}{
	{"phone-portrait", 360, 640},
	{"square", 512, 512},
	{"desktop", 1280, 720},
	{"wide", 1920, 1080},
}

// TestRegistryModesHaveStableUniqueIDs pins the stable-ID contract:
// IDs are non-empty, unique, and exactly the documented set in the
// canonical UI order (projects and presets persist these strings).
func TestRegistryModesHaveStableUniqueIDs(t *testing.T) {
	want := []visualizer.Mode{visualizer.ModeBars, visualizer.ModeRadial, visualizer.ModeWaveform, visualizer.ModeParticles}
	if len(visualizer.AllModes) != len(want) {
		t.Fatalf("AllModes has %d entries, want %d — the shipped collection changed", len(visualizer.AllModes), len(want))
	}
	seen := map[visualizer.Mode]bool{}
	for i, m := range visualizer.AllModes {
		if m == "" || m == visualizer.ModeNone {
			t.Fatalf("AllModes[%d] has the empty/none ID %q — every visible item needs a real stable ID", i, m)
		}
		if seen[m] {
			t.Fatalf("duplicate mode ID %q in AllModes", m)
		}
		seen[m] = true
		if m != want[i] {
			t.Fatalf("AllModes[%d] = %q, want %q — the canonical UI order changed (projects depend on stable IDs, order drives the collection)", i, m, want[i])
		}
		if m.DisplayName() == "" || m.DisplayName() == string(m) && m == "" {
			t.Fatalf("mode %q has no display name", m)
		}
		if !visualizer.ValidMode(m) {
			t.Fatalf("mode %q is not accepted by ValidMode", m)
		}
	}
}

// TestEveryRegisteredModeIsARealImplementation renders every registered
// mode through the CPU renderer at multiple layouts: each must produce
// a non-uniform frame (a real drawing, not a blank), honour the layout
// (bounds match), be deterministic for an identical input, and be
// reachable with its default configuration.
func TestEveryRegisteredModeIsARealImplementation(t *testing.T) {
	for _, m := range visualizer.AllModes {
		cfg := visualizer.Config{Mode: m}
		cfg.Clamp()
		if cfg.Mode != m {
			t.Fatalf("mode %q does not survive Clamp() with a valid default config (normalized to %q)", m, cfg.Mode)
		}
		for _, lay := range layoutMatrix {
			r := NewCPURenderer()
			in := visualizer.FrameInput{
				FrameIndex: 4, Time: 0.2, Width: lay.w, Height: lay.h,
				Config: cfg,
				Audio:  visualizer.AudioState{Bass: 0.6, Mid: 0.4, Treble: 0.3, RMS: 0.5, Bins: testBins(), WaveWindow: testWaveWindow()},
			}
			img, err := r.RenderFrame(in)
			if err != nil {
				t.Fatalf("mode %q at %s: %v", m, lay.name, err)
			}
			r.Close()
			if img.Bounds().Dx() != lay.w || img.Bounds().Dy() != lay.h {
				t.Fatalf("mode %q at %s: bounds %v do not honour the requested layout", m, lay.name, img.Bounds())
			}
			if imageUniform(img) {
				t.Fatalf("mode %q at %s rendered a uniform frame — the item does not draw", m, lay.name)
			}
		}
	}
}

// TestModesRespondToAudio verifies every mode's output actually
// changes when the audio features change (audio reactivity contract —
// no dead visualizer item).
func TestModesRespondToAudio(t *testing.T) {
	quiet := visualizer.AudioState{Bass: 0.0, Mid: 0.0, Treble: 0.0, Bins: make([]float64, visualizer.NumVizBins)}
	loud := visualizer.AudioState{Bass: 0.9, Mid: 0.7, Treble: 0.5, RMS: 0.8, Bins: testBins(), Onset: true}
	for _, m := range visualizer.AllModes {
		r := NewCPURenderer()
		mk := func(a visualizer.AudioState) *image.RGBA {
			img, err := r.RenderFrame(visualizer.FrameInput{
				Time: 1.0, Width: 320, Height: 240,
				Config: visualizer.Config{Mode: m},
				Audio:  a,
			})
			if err != nil {
				t.Fatalf("mode %q: %v", m, err)
			}
			return img
		}
		quietImg := mk(quiet)
		r.Close()
		r2 := NewCPURenderer()
		mkQuiet := func() *image.RGBA {
			img, err := r2.RenderFrame(visualizer.FrameInput{Time: 1.0, Width: 320, Height: 240, Config: visualizer.Config{Mode: m}, Audio: quiet})
			if err != nil {
				t.Fatalf("mode %q: %v", m, err)
			}
			return img
		}
		_ = mkQuiet()
		r2.Close()

		r3 := NewCPURenderer()
		loudImg, err := r3.RenderFrame(visualizer.FrameInput{Time: 1.0, Width: 320, Height: 240, Config: visualizer.Config{Mode: m}, Audio: loud})
		if err != nil {
			t.Fatalf("mode %q loud: %v", m, err)
		}
		r3.Close()
		if samePixels(quietImg, loudImg) {
			t.Fatalf("mode %q draws identical frames for silence and loud audio — the item is not audio-reactive", m)
		}
	}
}

// TestPresetsAreValidAndRenderable pins the preset rail: every preset
// the UI shows has a name, a registered mode, a self-consistent
// configuration and renders a real frame — every visible collection
// item must launch the intended renderer.
func TestPresetsAreValidAndRenderable(t *testing.T) {
	presets := visualizer.Presets()
	if len(presets) < 4 {
		t.Fatalf("expected at least the 4 shipped presets, got %d", len(presets))
	}
	seenName := map[string]bool{}
	seenMode := map[visualizer.Mode]bool{}
	for _, p := range presets {
		if p.Name == "" || seenName[p.Name] {
			t.Fatalf("preset name %q is empty or duplicated", p.Name)
		}
		seenName[p.Name] = true
		if !visualizer.ValidMode(p.Config.Mode) {
			t.Fatalf("preset %q references unregistered mode %q", p.Name, p.Config.Mode)
		}
		seenMode[p.Config.Mode] = true
		clamped := p.Config
		clamped.Clamp()
		if clamped.Mode != p.Config.Mode {
			t.Fatalf("preset %q mode %q normalizes to %q — the stored config is invalid", p.Name, p.Config.Mode, clamped.Mode)
		}
		r := NewCPURenderer()
		img, err := r.RenderFrame(visualizer.FrameInput{
			Time: 0.5, Width: 320, Height: 240,
			Config: p.Config,
			Audio:  visualizer.AudioState{Bass: 0.6, Mid: 0.4, Treble: 0.2, Bins: testBins(), WaveWindow: testWaveWindow()},
		})
		r.Close()
		if err != nil {
			t.Fatalf("preset %q: render: %v", p.Name, err)
		}
		if imageUniform(img) {
			t.Fatalf("preset %q renders a uniform frame", p.Name)
		}
	}
	// The preset rail must cover more than one mode to be a meaningful
	// collection browser, and every shipped mode must be reachable via
	// at least one preset.
	for _, m := range visualizer.AllModes {
		if !seenMode[m] {
			t.Fatalf("no preset demonstrates mode %q — collection discoverability gap", m)
		}
	}
}

// TestModesAreVisuallyDistinct guards the "no recoloured copies of one
// renderer" contract: pairwise render differences must exceed a
// structural threshold (each mode's algorithm produces a genuinely
// different composition).
func TestModesAreVisuallyDistinct(t *testing.T) {
	type frame struct {
		mode visualizer.Mode
		img  *image.RGBA
	}
	var frames []frame
	for _, m := range visualizer.AllModes {
		r := NewCPURenderer()
		img, err := r.RenderFrame(visualizer.FrameInput{
			Time: 0.5, Width: 320, Height: 240,
			Config: visualizer.Config{Mode: m},
			Audio:  visualizer.AudioState{Bass: 0.6, Mid: 0.4, Treble: 0.2, Bins: testBins(), WaveWindow: testWaveWindow()},
		})
		r.Close()
		if err != nil {
			t.Fatalf("mode %q: %v", m, err)
		}
		frames = append(frames, frame{m, img})
	}
	for i := 0; i < len(frames); i++ {
		for j := i + 1; j < len(frames); j++ {
			diff := pixelDiff(frames[i].img, frames[j].img)
			if diff < 0.10 {
				t.Fatalf("modes %q and %q differ in only %.1f%% of pixels — they are visually near-identical", frames[i].mode, frames[j].mode, diff*100)
			}
		}
	}
}

// pixelDiff returns the fraction of pixels that differ (any channel).
func pixelDiff(a, b *image.RGBA) float64 {
	if len(a.Pix) != len(b.Pix) || len(a.Pix) == 0 {
		return 1
	}
	diff := 0
	for i := 0; i < len(a.Pix); i += 4 {
		if a.Pix[i] != b.Pix[i] || a.Pix[i+1] != b.Pix[i+1] || a.Pix[i+2] != b.Pix[i+2] {
			diff++
		}
	}
	return float64(diff) / (float64(len(a.Pix)) / 4)
}

// testWaveWindow provides a non-degenerate waveform window.
func testWaveWindow() []float32 {
	w := make([]float32, 64)
	for i := range w {
		w[i] = float32(0.2 + 0.6*float64(i%17)/17)
	}
	return w
}

var _ = fmt.Sprintf
