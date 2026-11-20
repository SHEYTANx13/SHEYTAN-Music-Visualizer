// preview.go — the CPU frame bridge between the studio service (the
// render.Renderer boundary behind studio.PreviewFrame) and the Gio
// window.
//
// Contract:
//   - At most three frame buffers are alive (bounded ring; buffers are
//     reused, never reallocated per frame unless the render size
//     changes).
//   - A stale-frame guard drops frames whose playback session is older
//     than the newest accepted session (never render media-time state
//     from a superseded playback session, never read the wall clock).
//   - Frame pacing: full rate while playing, a slow cadence when idle
//     (driven by studio.IsPlayingLike, additionally slowed by the
//     reduced-motion accessibility switch).

package gogui

import (
	"image"
	"image/color"
	"image/draw"
	"sync"
	"time"

	"gioui.org/op/paint"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/playback"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/studio"
)

const (
	// previewMaxBuffers bounds the ring of reusable blit buffers.
	previewMaxBuffers = 3
	// previewIdleInterval is the idle preview cadence (5 fps).
	previewIdleInterval = 200 * time.Millisecond
	// previewIdleIntervalReduced is the idle cadence with the
	// reduced-motion accessibility switch on.
	previewIdleIntervalReduced = 600 * time.Millisecond
	// previewMinInterval floors the playing-state frame rate so a very
	// fast display cannot spin the CPU renderer at unbounded rates.
	previewMinInterval = 30 * time.Millisecond
)

// previewBridge owns the bounded buffers and the blit state. It is safe
// for concurrent use: the event loop calls every method (Accept, Image,
// ShouldRequestFrame), but the mutex keeps the ring and the newest
// session bookkeeping explicit.
type previewBridge struct {
	mu            sync.Mutex
	bufs          [previewMaxBuffers]*image.RGBA
	cursor        int
	op            paint.ImageOp
	hasFrame      bool
	newestSession uint64
	lastAccepted  time.Time
	lastRequested time.Time
}

func newPreviewBridge() *previewBridge { return &previewBridge{} }

// ShouldRequestFrame reports whether the shell should render a preview
// frame now. playing means audio (or the silent pacing clock) is
// actively consuming media time; reducedMotion is the accessibility
// switch.
func (p *previewBridge) ShouldRequestFrame(now time.Time, playing, reducedMotion bool) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	ok := false
	if playing {
		ok = now.Sub(p.lastRequested) >= previewMinInterval
	} else {
		interval := previewIdleInterval
		if reducedMotion {
			interval = previewIdleIntervalReduced
		}
		ok = now.Sub(p.lastRequested) >= interval
	}
	if ok {
		p.lastRequested = now
	}
	return ok
}

// NextIdleDeadline returns the earliest time the idle pacer allows the
// next request (never before now; used to schedule the exact next
// frame check without busy polling).
func (p *previewBridge) NextIdleDeadline(now time.Time, reducedMotion bool) time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	interval := previewIdleInterval
	if reducedMotion {
		interval = previewIdleIntervalReduced
	}
	d := p.lastRequested.Add(interval)
	if d.Before(now) {
		return now
	}
	return d
}

// Accept copies one delivered frame into the ring buffer and records
// the blit op. It returns false when nothing was accepted: nil image,
// or a stale frame from an older playback session.
func (p *previewBridge) Accept(fr *studio.FrameResult, now time.Time) bool {
	if fr == nil || fr.Image == nil || fr.Image.Bounds().Empty() {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if fr.Clock.Session < p.newestSession {
		// Stale-frame guard: a frame from a superseded playback session
		// must never reach the display.
		return false
	}
	p.newestSession = fr.Clock.Session
	buf := p.bufs[p.cursor]
	if buf == nil || buf.Bounds().Size() != fr.Image.Bounds().Size() {
		buf = image.NewRGBA(image.Rect(0, 0, fr.Image.Bounds().Dx(), fr.Image.Bounds().Dy()))
		p.bufs[p.cursor] = buf
	}
	draw.Draw(buf, buf.Bounds(), fr.Image, fr.Image.Bounds().Min, draw.Src)
	p.op = paint.NewImageOp(buf)
	p.hasFrame = true
	p.cursor = (p.cursor + 1) % previewMaxBuffers
	p.lastAccepted = now
	return true
}

// Image returns the latest blit op (false when no frame has arrived).
func (p *previewBridge) Image() (paint.ImageOp, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.op, p.hasFrame
}

// LastAccepted reports the accept time of the newest frame.
func (p *previewBridge) LastAccepted() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastAccepted
}

// NewestSession reports the newest accepted playback session.
func (p *previewBridge) NewestSession() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.newestSession
}

// BufferCount reports the number of allocated buffers (bounded by
// previewMaxBuffers; test hook for the bounded-memory contract).
func (p *previewBridge) BufferCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, b := range p.bufs {
		if b != nil {
			n++
		}
	}
	return n
}

// placeholderImage renders the deterministic empty-preview backdrop
// (no frames yet / no track): an obsidian field with the paused-clock
// state line drawn by the panel, never a fake render.
func placeholderImage(w, h int, col color.NRGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), &image.Uniform{col}, image.Point{}, draw.Src)
	return img
}

// clockStateLine renders the honest transport readout used by the
// preview placeholder and the status strip.
func clockStateLine(c playback.ClockInfo) string {
	state := c.State.String()
	if state == "" {
		state = "stopped"
	}
	return state
}
