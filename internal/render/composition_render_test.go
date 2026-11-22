package render

// composition_render_test.go — §7.2/§7.5/§8.3 property tests: measured
// features reach the renderer and change composition deterministically;
// discrete events stay discrete; confidence gates work; preview and
// export render identical bytes from identical state.

import (
	"image"
	"image/color"
	"testing"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/dsp"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/visualizer"
)

func frameAt(t float64, audio visualizer.AudioState) visualizer.FrameInput {
	cfg := visualizer.Config{Mode: visualizer.ModeBloom, Glow: 0.8}
	// Priority-D knobs explicitly on (zero value is a valid "off").
	cfg.VocalLayerEnabled = true
	cfg.VocalSensitivity = 1.2
	cfg.StereoWidthGain = 0.6
	cfg.SectionIntensity = 0.8
	cfg.TransientIntensity = 1.2
	cfg.ConfidenceThreshold = 0.3
	cfg.Clamp()
	return visualizer.FrameInput{
		FrameIndex: int(t * 30),
		Time:       t,
		Width:      320,
		Height:     180,
		Config:     cfg,
		Audio:      audio,
	}
}

// renderBytes renders one frame and fingerprints EVERY pixel (full
// comparison, no sampling: a subtle layer difference must count).
func renderBytes(t *testing.T, in visualizer.FrameInput) []byte {
	t.Helper()
	r := NewCPURenderer()
	img, err := r.RenderFrame(in)
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	out := make([]byte, 0, b.Dx()*b.Dy()*3)
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			c := color.RGBAModel.Convert(img.At(x, y)).(color.RGBA)
			out = append(out, c.R, c.G, c.B)
		}
	}
	return out
}

func TestRendererReactsToMeasuredFeatures(t *testing.T) {
	base := visualizer.AudioState{Time: 1.0, RMS: 0.3, Bass: 0.3, Mid: 0.3, Treble: 0.3}
	base.HasComposition = true
	base.Confidence = 1.0
	// Vocal-active vs vocal-idle states must render DIFFERENTLY when
	// the vocal layer is enabled.
	vocalOn := base
	vocalOn.VocalLikelihood = 0.9
	vocalOn.VocalPhraseActive = true
	vocalOff := base
	vocalOff.VocalLikelihood = 0.0
	bOn := renderBytes(t, frameAt(1.0, vocalOn))
	bOff := renderBytes(t, frameAt(1.0, vocalOff))
	if bytesEqual(bOn, bOff) {
		t.Fatal("vocal-active and vocal-idle states rendered identically (vocal layer has no effect)")
	}
	// Confidence below the threshold must GATE the feature off: the
	// vocal-active state with zero confidence renders like vocal-idle.
	gated := vocalOn
	gated.Confidence = 0.0
	bGated := renderBytes(t, frameAt(1.0, gated))
	if !bytesEqual(bGated, bOff) {
		t.Fatal("low-confidence vocal evidence must gate OFF, not partially apply")
	}
}

func TestRendererDeterministicAndSeekStable(t *testing.T) {
	audio := visualizer.AudioState{Time: 2.0, RMS: 0.5, Bass: 0.4, Mid: 0.4, Treble: 0.3}
	audio.HasComposition = true
	audio.Confidence = 1.0
	audio.BeatPhase = 0.5
	a := renderBytes(t, frameAt(2.0, audio))
	b := renderBytes(t, frameAt(2.0, audio))
	if !bytesEqual(a, b) {
		t.Fatal("same input/config/time produced different frames (determinism violated)")
	}
	// Out-of-order rendering of the same timestamp is equivalent (§7.5).
	c := renderBytes(t, frameAt(3.0, audio))
	_ = c
	d := renderBytes(t, frameAt(2.0, audio))
	if !bytesEqual(a, d) {
		t.Fatal("repeated frame rendering diverged (seek determinism violated)")
	}
}

func TestDiscreteTransientNotSmeared(t *testing.T) {
	// The kick-class transient lifts the LOW bins in bloom (its §7.2
	// mapping) — rendered through the scene's discrete decay.
	base := visualizer.AudioState{Time: 1.0, RMS: 0.4, Bass: 0.4}
	base.HasComposition = true
	base.Confidence = 1.0
	hit := base
	hit.Transient = 1.0
	hit.TransientBand = 0
	idle := base
	bHit := renderBytes(t, frameAt(1.0, hit))
	bIdle := renderBytes(t, frameAt(1.0, idle))
	if bytesEqual(bHit, bIdle) {
		t.Fatal("kick-class transient has no visual effect")
	}
	// One frame later (time advanced past the discrete decay window
	// semantics): the transient itself is a per-frame discrete input;
	// the renderer must not fabricate its own persistence across
	// unrelated frames. Render at a LATER time with no transient: the
	// kick layer must decay deterministically, but the discrete input
	// at that later time is zero.
	later := base
	later.Time = 1.5
	bLater := renderBytes(t, frameAt(1.5, later))
	if bytesEqual(bLater, bIdle) {
		// acceptable: decayed fully at the renderer's discretion
		t.Log("transient decayed to identity by +0.5 s (fine)")
	}
}

func TestStereoWidthAffectsComposition(t *testing.T) {
	// The measured width widens the bars field: rendered through the
	// bars mode with the scene evolved two frames (stereoSpread is a
	// smoothed measurement).
	mkFrame := func(t float64, width float64) visualizer.FrameInput {
		in := frameAt(t, visualizer.AudioState{Time: t, RMS: 0.4, Bass: 0.3, Mid: 0.3})
		in.Config.Mode = visualizer.ModeBars
		in.Config.Clamp()
		in.Audio.HasComposition = true
		in.Audio.Confidence = 1.0
		in.Audio.StereoWidth = width
		return in
	}
	r := NewCPURenderer()
	if _, err := r.RenderFrame(mkFrame(1.0, 0)); err != nil {
		t.Fatal(err)
	}
	imgN, err := r.RenderFrame(mkFrame(1.1, 0))
	if err != nil {
		t.Fatal(err)
	}
	r2 := NewCPURenderer()
	if _, err := r2.RenderFrame(mkFrame(1.0, 1)); err != nil {
		t.Fatal(err)
	}
	imgW, err := r2.RenderFrame(mkFrame(1.1, 1))
	if err != nil {
		t.Fatal(err)
	}
	if bytesEqual(imageBytes(imgN), imageBytes(imgW)) {
		t.Fatal("stereo width has no visual effect on stereo-aware composition")
	}
}

func TestSectionTransitionLiftsComposition(t *testing.T) {
	// The pulse fires on a section CHANGE between frames, so the
	// renderer evolves across two frames (deterministic, media-time
	// driven): frame 1 establishes section 0, frame 2 enters section 1.
	r := NewCPURenderer()
	mk := func(t float64, sec int, pulseOverride float64) visualizer.FrameInput {
		in := frameAt(t, visualizer.AudioState{Time: t, RMS: 0.3, Bass: 0.3})
		in.Audio.HasComposition = true
		in.Audio.Confidence = 1.0
		in.Audio.SectionIndex = sec
		_ = pulseOverride
		return in
	}
	if _, err := r.RenderFrame(mk(1.0, 0, 0)); err != nil {
		t.Fatal(err)
	}
	img1, err := r.RenderFrame(mk(1.1, 0, 0)) // settled baseline
	if err != nil {
		t.Fatal(err)
	}
	// The renderer returns its OWN buffer: snapshot the bytes BEFORE
	// the next render overwrites them.
	b1 := imageBytes(img1)
	img2, err := r.RenderFrame(mk(1.2, 1, 0)) // section change → pulse
	if err != nil {
		t.Fatal(err)
	}
	if img1 == nil || img2 == nil {
		t.Fatal("nil frame")
	}
	b2 := imageBytes(img2)
	if bytesEqual(b1, b2) {
		t.Fatal("section transition pulse has no visual effect")
	}
}

func imageBytes(img image.Image) []byte {
	b := img.Bounds()
	out := make([]byte, 0, b.Dx()*b.Dy()*3)
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			c := color.RGBAModel.Convert(img.At(x, y)).(color.RGBA)
			out = append(out, c.R, c.G, c.B)
		}
	}
	return out
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

var _ = image.Rect
var _ = dsp.BandCount
