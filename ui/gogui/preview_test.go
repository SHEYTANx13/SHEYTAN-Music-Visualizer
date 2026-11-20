package gogui

import (
	"image"
	"image/color"
	"testing"
	"time"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/playback"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/render"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/studio"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/visualizer"
)

// sampleFrameInput builds a deterministic small frame input.
func sampleFrameInput(w, h int) visualizer.FrameInput {
	bins := make([]float64, visualizer.NumVizBins)
	for i := range bins {
		bins[i] = 0.4 + 0.3*float64(i%8)/8
	}
	return visualizer.FrameInput{
		FrameIndex: 1, Time: 1.0 / 30, Width: w, Height: h,
		Config: visualizer.Config{Mode: visualizer.ModeBars},
		Audio:  visualizer.AudioState{Bass: 0.5, Mid: 0.3, Treble: 0.2, Bins: bins},
	}
}

// TestPreviewBridgeRealRenderer exercises the bounded buffer contract
// through the REAL render.SelectRenderer path (CPU always works
// headless): render, accept, blit op availability, bounded buffers.
func TestPreviewBridgeRealRenderer(t *testing.T) {
	r, info := render.SelectRenderer()
	defer r.Close()
	if info.Backend != render.BackendCPU && info.Backend != render.BackendGPU {
		t.Fatalf("unexpected backend %q", info.Backend)
	}

	img, err := r.RenderFrame(sampleFrameInput(96, 64))
	if err != nil {
		t.Fatalf("RenderFrame: %v", err)
	}
	if img == nil || img.Bounds() != image.Rect(0, 0, 96, 64) {
		t.Fatalf("frame bounds = %v, want 96x64", img.Bounds())
	}

	p := newPreviewBridge()
	now := time.Now()
	fr := &studio.FrameResult{Image: img, Clock: playback.ClockInfo{Session: 1, State: playback.StatePlaying}}
	if !p.Accept(fr, now) {
		t.Fatal("first frame must be accepted")
	}
	if got := p.BufferCount(); got < 1 || got > previewMaxBuffers {
		t.Fatalf("buffer count = %d, want 1..%d", got, previewMaxBuffers)
	}
	if p.NewestSession() != 1 {
		t.Errorf("newest session = %d, want 1", p.NewestSession())
	}
	if op, ok := p.Image(); !ok || op.Size() != image.Pt(96, 64) {
		t.Errorf("Image() = %v, %v; want op with size 96x64", op, ok)
	}

	// More frames than buffers: the ring stays bounded.
	for i := 0; i < 8; i++ {
		img2, err := r.RenderFrame(sampleFrameInput(96, 64))
		if err != nil {
			t.Fatalf("RenderFrame: %v", err)
		}
		fr = &studio.FrameResult{Image: img2, Clock: playback.ClockInfo{Session: uint64(i + 2)}}
		p.Accept(fr, now.Add(time.Duration(i)*time.Millisecond))
	}
	if got := p.BufferCount(); got > previewMaxBuffers {
		t.Fatalf("buffer count grew to %d, want <= %d", got, previewMaxBuffers)
	}
	if p.NewestSession() != 9 {
		t.Errorf("newest session = %d, want 9", p.NewestSession())
	}
}

// TestPreviewStaleFrameGuard verifies frames from an older playback
// session are dropped (the generation check), while newer sessions and
// the same session pass.
func TestPreviewStaleFrameGuard(t *testing.T) {
	p := newPreviewBridge()
	now := time.Now()
	mk := func(session uint64, c color.NRGBA) *studio.FrameResult {
		img := image.NewRGBA(image.Rect(0, 0, 8, 8))
		for i := range img.Pix {
			img.Pix[i] = c.R
		}
		return &studio.FrameResult{Image: img, Clock: playback.ClockInfo{Session: session}}
	}
	if !p.Accept(mk(3, color.NRGBA{R: 1}), now) {
		t.Fatal("session 3 frame must be accepted")
	}
	if p.Accept(mk(2, color.NRGBA{R: 2}), now) {
		t.Error("stale session 2 frame must be dropped")
	}
	if p.Accept(mk(3, color.NRGBA{R: 3}), now) {
		t.Log("same-session frame accepted (allowed)")
	}
	if !p.Accept(mk(4, color.NRGBA{R: 4}), now) {
		t.Error("newer session 4 frame must be accepted")
	}
	if got := p.NewestSession(); got != 4 {
		t.Errorf("newest session = %d, want 4", got)
	}
	if p.Accept(nil, now) {
		t.Error("nil frame must be dropped")
	}
	if p.Accept(&studio.FrameResult{}, now) {
		t.Error("nil image frame must be dropped")
	}
}

// TestPreviewPacing verifies the frame pacer: full rate while playing
// (with a small floor), throttled when idle, extra-slow with the
// reduced-motion switch.
func TestPreviewPacing(t *testing.T) {
	p := newPreviewBridge()
	t0 := time.Unix(0, 0).UTC()
	if !p.ShouldRequestFrame(t0, true, false) {
		t.Error("playing state must allow the first frame")
	}
	if p.ShouldRequestFrame(t0.Add(10*time.Millisecond), true, false) {
		t.Error("playing state must respect the floor interval (30 ms)")
	}
	if !p.ShouldRequestFrame(t0.Add(31*time.Millisecond), true, false) {
		t.Error("playing state must allow the next frame after the floor")
	}
	if p.ShouldRequestFrame(t0.Add(32*time.Millisecond), false, false) {
		t.Error("idle state must not allow a frame inside the 200 ms interval")
	}
	if !p.ShouldRequestFrame(t0.Add(231*time.Millisecond), false, false) {
		t.Error("idle state must allow a frame 200 ms after the last request")
	}
	if p.ShouldRequestFrame(t0.Add(205*time.Millisecond), false, true) {
		t.Error("reduced-motion idle state must not allow a frame inside the 600 ms interval")
	}
	if !p.ShouldRequestFrame(t0.Add(831*time.Millisecond), false, true) {
		t.Error("reduced-motion idle state must allow a frame 600 ms after the last request")
	}
	// NextIdleDeadline never lies about the past.
	if d := p.NextIdleDeadline(t0.Add(100*time.Millisecond), true); d.Before(t0.Add(100 * time.Millisecond)) {
		t.Errorf("NextIdleDeadline = %v, must never be before now", d)
	}
}

// TestPlaceholderImage verifies the deterministic empty-state backdrop.
func TestPlaceholderImage(t *testing.T) {
	img := placeholderImage(16, 12, color.NRGBA{R: 8, G: 8, B: 8, A: 255})
	if img.Bounds() != image.Rect(0, 0, 16, 12) {
		t.Fatalf("bounds = %v", img.Bounds())
	}
	if got := img.At(0, 0); got != (color.RGBA{R: 8, G: 8, B: 8, A: 255}) {
		t.Errorf("placeholder pixel = %v", got)
	}
}

// TestClockStateLine verifies the honest state line fallback.
func TestClockStateLine(t *testing.T) {
	if got := clockStateLine(playback.ClockInfo{State: playback.StatePlaying}); got != "playing" {
		t.Errorf("clockStateLine = %q, want playing", got)
	}
	if got := clockStateLine(playback.ClockInfo{}); got == "" {
		t.Error("empty state must still render a line")
	}
}
