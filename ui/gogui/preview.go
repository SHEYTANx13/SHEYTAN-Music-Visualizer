// preview.go — the CPU frame bridge between the studio service (the
// render.Renderer boundary behind studio.PreviewFrame) and the go-gui
// window (a gui.PixelSurface shown through gui.Image(Surface)).
//
// Contract:
//   - At most three frame buffers are alive (bounded ring; buffers are
//     reused, never reallocated per frame unless the render size
//     changes). PixelSurface.Update copies the caller's pixels
//     synchronously under its own lock BEFORE returning, so a ring
//     buffer is only ever reused after its copy completed — the
//     "never recycle while a copy is in flight" rule holds exactly as
//     in the ring blit it replaces.
//   - A stale-frame guard drops frames whose playback session is older
//     than the newest accepted session (never render media-time state
//     from a superseded playback session, never read the wall clock).
//   - Frame pacing: full rate while playing (30 ms floor), a slow
//     cadence when idle (driven by studio.IsPlayingLike, additionally
//     slowed by the reduced-motion accessibility switch). The pacer
//     goroutine in shell.go owns the cadence; every published frame
//     wakes the window through the PixelSurface itself.
//   - Pixel layout: the renderers return premultiplied *image.RGBA;
//     PixelSurface pixels are straight-alpha RGBA8 (image.NRGBA
//     semantics), so Publish converts per pixel before Update.

package gogui

import (
	"image"
	"image/color"
	"image/draw"
	"sync"
	"time"

	"github.com/go-gui-org/go-gui/gui"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/playback"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/studio"
)

const (
	// previewMaxBuffers bounds the ring of reusable conversion buffers.
	previewMaxBuffers = 3
	// previewIdleInterval is the idle preview cadence (5 fps).
	previewIdleInterval = 200 * time.Millisecond
	// previewIdleIntervalReduced is the idle cadence with the
	// reduced-motion accessibility switch on.
	previewIdleIntervalReduced = 600 * time.Millisecond
	// previewMinInterval floors the playing-state frame rate so a very
	// fast display cannot spin the CPU renderer at unbounded rates.
	previewMinInterval = 30 * time.Millisecond
	// previewDefaultWidth/Height are the surface creation bounds; the
	// surface follows the laid-out pane through Resize.
	previewDefaultWidth  = 480
	previewDefaultHeight = 270
)

// previewBridge owns the bounded conversion buffers and the publish
// state. It is safe for concurrent use: the pacer goroutine calls
// Publish/ShouldRequestFrame while the frame thread reads the
// accessors; the mutex keeps the ring and the newest-session
// bookkeeping explicit.
type previewBridge struct {
	mu            sync.Mutex
	bufs          [previewMaxBuffers][]byte // straight-alpha RGBA8, tightly packed
	cursor        int
	surface       *gui.PixelSurface
	lastW, lastH  int
	hasFrame      bool
	newestSession uint64
	lastAccepted  time.Time
	lastRequested time.Time
}

func newPreviewBridge() *previewBridge { return &previewBridge{} }

// Attach binds the surface the bridge publishes into (called once,
// before the window opens; nil is allowed and leaves the bridge inert).
func (p *previewBridge) Attach(s *gui.PixelSurface) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.surface = s
}

// Surface returns the attached surface (nil when none).
func (p *previewBridge) Surface() *gui.PixelSurface {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.surface
}

// HasFrame reports whether at least one frame was published.
func (p *previewBridge) HasFrame() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.hasFrame
}

// ShouldRequestFrame reports whether the pacer should render a preview
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

// Publish converts one delivered frame into the straight-alpha layout
// and copies it into the PixelSurface. It returns false when nothing
// was published: nil image, a stale frame from an older playback
// session, or a surface error (resizes propagate as honest failures —
// the next frame retries at the laid-out size).
func (p *previewBridge) Publish(fr *studio.FrameResult, now time.Time) bool {
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
	if p.surface == nil {
		return false
	}
	w, h := fr.Image.Bounds().Dx(), fr.Image.Bounds().Dy()
	if p.lastW != w || p.lastH != h {
		if err := p.surface.Resize(w, h); err != nil {
			return false
		}
		p.lastW, p.lastH = w, h
	}
	buf := p.bufs[p.cursor]
	need := w * h * 4
	if len(buf) != need {
		buf = make([]byte, need)
		p.bufs[p.cursor] = buf
	}
	straightFromPremultiplied(buf, fr.Image)
	if err := p.surface.Update(buf, w*4); err != nil {
		return false
	}
	// Update copied the pixels under its own lock before returning: the
	// ring slot is free again and the copy can never race a reuse.
	p.hasFrame = true
	p.cursor = (p.cursor + 1) % previewMaxBuffers
	p.lastAccepted = now
	return true
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

// BufferCount reports the number of allocated conversion buffers
// (bounded by previewMaxBuffers; test hook for the bounded-memory
// contract).
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

// straightFromPremultiplied converts a premultiplied image.RGBA frame
// into the straight-alpha RGBA8 byte layout PixelSurface.Update
// expects (PixelSurface pixels carry image.NRGBA semantics). Fully
// opaque pixels — the common case, the visualizers draw on an opaque
// ground — copy unchanged; dst must hold w*h*4 bytes. The source
// stride is honored, so row padding is skipped.
func straightFromPremultiplied(dst []byte, src *image.RGBA) {
	w := src.Bounds().Dx()
	h := src.Bounds().Dy()
	di := 0
	for y := 0; y < h; y++ {
		row := src.Pix[y*src.Stride:]
		for x := 0; x < w; x++ {
			j := x * 4
			a := row[j+3]
			switch a {
			case 255:
				dst[di], dst[di+1], dst[di+2], dst[di+3] = row[j], row[j+1], row[j+2], 255
			case 0:
				dst[di], dst[di+1], dst[di+2], dst[di+3] = 0, 0, 0, 0
			default:
				dst[di] = uint8(uint16(row[j]) * 255 / uint16(a))
				dst[di+1] = uint8(uint16(row[j+1]) * 255 / uint16(a))
				dst[di+2] = uint8(uint16(row[j+2]) * 255 / uint16(a))
				dst[di+3] = a
			}
			di += 4
		}
	}
}

// placeholderImage renders the deterministic empty-preview backdrop
// (no frames yet / no track): an obsidian field the surface is seeded
// with, never a fake render.
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
