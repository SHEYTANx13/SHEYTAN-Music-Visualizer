package gogui

import (
	"image"
	"image/color"
	"testing"
	"time"

	"github.com/go-gui-org/go-gui/gui"

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
// headless): render, publish into a real PixelSurface, bounded
// buffers, resize on demand.
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

	surface, err := gui.NewPixelSurface(gui.PixelSurfaceCfg{Width: 96, Height: 64})
	if err != nil {
		t.Fatalf("NewPixelSurface: %v", err)
	}
	defer surface.Close()

	p := newPreviewBridge()
	p.Attach(surface)
	now := time.Now()
	fr := &studio.FrameResult{Image: img, Clock: playback.ClockInfo{Session: 1, State: playback.StatePlaying}}
	if !p.Publish(fr, now) {
		t.Fatal("first frame must be published")
	}
	if got := p.BufferCount(); got < 1 || got > previewMaxBuffers {
		t.Fatalf("buffer count = %d, want 1..%d", got, previewMaxBuffers)
	}
	if p.NewestSession() != 1 {
		t.Errorf("newest session = %d, want 1", p.NewestSession())
	}
	if !p.HasFrame() {
		t.Error("HasFrame must be true after the first publish")
	}
	// The surface must have received the pixels (version bump + opaque
	// ground: the visualizer renders onto an opaque field).
	var gotW, gotH int
	surface.ReadPixels(0, func(w, h int, pix []byte, version uint64) {
		gotW, gotH = w, h
		if version == 0 {
			t.Error("surface version must advance past 0 after a publish")
		}
	})
	if gotW != 96 || gotH != 64 {
		t.Errorf("surface dims = %dx%d, want 96x64", gotW, gotH)
	}

	// More frames than buffers: the ring stays bounded.
	for i := 0; i < 8; i++ {
		img2, err := r.RenderFrame(sampleFrameInput(96, 64))
		if err != nil {
			t.Fatalf("RenderFrame: %v", err)
		}
		fr = &studio.FrameResult{Image: img2, Clock: playback.ClockInfo{Session: uint64(i + 2)}}
		p.Publish(fr, now.Add(time.Duration(i)*time.Millisecond))
	}
	if got := p.BufferCount(); got > previewMaxBuffers {
		t.Fatalf("buffer count grew to %d, want <= %d", got, previewMaxBuffers)
	}
	if p.NewestSession() != 9 {
		t.Errorf("newest session = %d, want 9", p.NewestSession())
	}

	// A size change resizes the surface instead of failing.
	img3, err := r.RenderFrame(sampleFrameInput(64, 48))
	if err != nil {
		t.Fatalf("RenderFrame: %v", err)
	}
	if !p.Publish(&studio.FrameResult{Image: img3, Clock: playback.ClockInfo{Session: 10}}, now) {
		t.Fatal("resized frame must be published")
	}
	surface.ReadPixels(0, func(w, h int, _ []byte, _ uint64) {
		if w != 64 || h != 48 {
			t.Errorf("surface dims after resize = %dx%d, want 64x48", w, h)
		}
	})
}

// TestPreviewStaleFrameGuard verifies frames from an older playback
// session are dropped (the generation check), while newer sessions and
// the same session pass.
func TestPreviewStaleFrameGuard(t *testing.T) {
	surface, err := gui.NewPixelSurface(gui.PixelSurfaceCfg{Width: 8, Height: 8})
	if err != nil {
		t.Fatalf("NewPixelSurface: %v", err)
	}
	defer surface.Close()
	p := newPreviewBridge()
	p.Attach(surface)
	now := time.Now()
	mk := func(session uint64, c color.NRGBA) *studio.FrameResult {
		img := image.NewRGBA(image.Rect(0, 0, 8, 8))
		for i := range img.Pix {
			img.Pix[i] = c.R
		}
		return &studio.FrameResult{Image: img, Clock: playback.ClockInfo{Session: session}}
	}
	if !p.Publish(mk(3, color.NRGBA{R: 1}), now) {
		t.Fatal("session 3 frame must be accepted")
	}
	if p.Publish(mk(2, color.NRGBA{R: 2}), now) {
		t.Error("stale session 2 frame must be dropped")
	}
	if p.Publish(mk(3, color.NRGBA{R: 3}), now) {
		t.Log("same-session frame accepted (allowed)")
	}
	if !p.Publish(mk(4, color.NRGBA{R: 4}), now) {
		t.Error("newer session 4 frame must be accepted")
	}
	if got := p.NewestSession(); got != 4 {
		t.Errorf("newest session = %d, want 4", got)
	}
	if p.Publish(nil, now) {
		t.Error("nil frame must be dropped")
	}
	if p.Publish(&studio.FrameResult{}, now) {
		t.Error("nil image frame must be dropped")
	}
}

// TestPreviewWithoutSurface verifies the bridge stays inert (honest
// no-publish) when the PixelSurface could not be created.
func TestPreviewWithoutSurface(t *testing.T) {
	p := newPreviewBridge()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	if p.Publish(&studio.FrameResult{Image: img, Clock: playback.ClockInfo{Session: 1}}, time.Now()) {
		t.Error("publish without a surface must fail honestly")
	}
	if p.HasFrame() {
		t.Error("HasFrame must stay false without a surface")
	}
	if p.Surface() != nil {
		t.Error("Surface must be nil without an attach")
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

// TestStraightFromPremultiplied verifies the PixelSurface pixel
// conversion: opaque copies unchanged, transparent zeroes, partial
// alpha un-premultiplies, and the source stride is honored. The
// partial-alpha pixel is legal premultiplied input (every channel
// <= alpha, as the renderers produce).
func TestStraightFromPremultiplied(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 3, 2))
	// Row 0: opaque, transparent, partial alpha (channels <= alpha).
	src.SetRGBA(0, 0, color.RGBA{R: 0xD5, G: 0x1D, B: 0x2A, A: 0xFF})
	src.SetRGBA(1, 0, color.RGBA{R: 0x80, G: 0x40, B: 0x20, A: 0x00})
	src.SetRGBA(2, 0, color.RGBA{R: 0x40, G: 0x20, B: 0x60, A: 0x80})
	// Row 1: opaque pixels.
	src.SetRGBA(0, 1, color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF})
	src.SetRGBA(1, 1, color.RGBA{R: 0x10, G: 0x20, B: 0x30, A: 0xFF})
	src.SetRGBA(2, 1, color.RGBA{R: 0x00, G: 0x00, B: 0x00, A: 0xFF})

	dst := make([]byte, 3*2*4)
	straightFromPremultiplied(dst, src)

	want := []byte{
		0xD5, 0x1D, 0x2A, 0xFF, // opaque: unchanged
		0x00, 0x00, 0x00, 0x00, // transparent: zeroed
		// 0x40*255/0x80=127 · 0x20*255/0x80=63 · 0x60*255/0x80=191 (truncating)
		0x7F, 0x3F, 0xBF, 0x80,
		0xFF, 0xFF, 0xFF, 0xFF, // opaque white
		0x10, 0x20, 0x30, 0xFF, // opaque: unchanged
		0x00, 0x00, 0x00, 0xFF, // opaque black
	}
	for i := range want {
		if dst[i] != want[i] {
			t.Errorf("dst[%d] = %#x, want %#x", i, dst[i], want[i])
		}
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

// TestClampPreviewSize verifies the bounded preview budget clamp.
func TestClampPreviewSize(t *testing.T) {
	if w, h := clampPreviewSize(0, 0); w != 480 || h != 270 {
		t.Errorf("clampPreviewSize(0,0) = %dx%d, want 480x270", w, h)
	}
	if w, h := clampPreviewSize(1920, 1080); w != previewMaxWidthPx || h != previewMaxHeightPx {
		t.Errorf("clampPreviewSize(1920,1080) = %dx%d, want the %dx%d budget", w, h, previewMaxWidthPx, previewMaxHeightPx)
	}
	if w, h := clampPreviewSize(4, 4); w != 16 || h != 16 {
		t.Errorf("clampPreviewSize(4,4) = %dx%d, want the 16px floor", w, h)
	}
	if w, _ := clampPreviewSize(1280, 720); w != previewMaxWidthPx {
		t.Errorf("clampPreviewSize(1280,720) width = %d, want %d", w, previewMaxWidthPx)
	}
}
