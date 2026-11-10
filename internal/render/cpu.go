package render

// cpu.go — the deterministic pure-Go software renderer. It renders
// every mode and always works; it is also the fallback when no usable
// GPU adapter is available. All drawing is bounded per-frame work on
// reusable buffers.

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"runtime"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/visualizer"
)

// CPURenderer renders frames on the CPU.
type CPURenderer struct {
	info  BackendInfo
	scene SceneState
	buf   *image.RGBA
	// powScratch backs the per-column falloff table in vbar across
	// frames (profiled: one allocation per bar per frame otherwise).
	// Owned by the render-loop goroutine that owns RenderFrame; never
	// shared across goroutines.
	powScratch []float64
}

// NewCPURenderer creates the CPU renderer (always available).
func NewCPURenderer() *CPURenderer {
	return &CPURenderer{
		info: BackendInfo{
			Backend:    BackendCPU,
			GpuState:   GpuNone,
			BackendAPI: "software",
			Detail:     "deterministic Go software renderer (always available)",
		},
	}
}

// Info reports the backend.
func (r *CPURenderer) Info() BackendInfo { return r.info }

// Close releases buffers.
func (r *CPURenderer) Close() { r.buf = nil; r.powScratch = nil }

// RenderFrame renders one frame. The output buffer is reused between
// calls — callers must copy if they retain the image.
func (r *CPURenderer) RenderFrame(in visualizer.FrameInput) (*image.RGBA, error) {
	if in.Width <= 0 || in.Height <= 0 || in.Width > 7680 || in.Height > 4320 {
		return nil, fmt.Errorf("render: invalid frame size %dx%d", in.Width, in.Height)
	}
	cfg := in.Config
	cfg.Clamp()
	pal := paletteFor(cfg.Palette)
	if r.buf == nil || r.buf.Bounds().Dx() != in.Width || r.buf.Bounds().Dy() != in.Height {
		r.buf = image.NewRGBA(image.Rect(0, 0, in.Width, in.Height))
		r.scene.Reset()
	}
	r.scene.Update(&in)
	if !r.scene.particleInit {
		seed := cfg.VariationSeed
		if seed == 0 && in.DNA != nil {
			seed = in.DNA.Seed
		}
		r.scene.Seed(seed)
	}
	if cfg.Mode == visualizer.ModeParticles {
		dt := 1.0 / 60.0
		if r.scene.hasLast && in.Time > r.scene.lastTime {
			dt = in.Time - r.scene.lastTime
			if dt > 0.25 {
				dt = 0.25
			}
		}
		r.scene.UpdateParticles(dt, in.Audio.Bass, in.Audio.Mid, in.Audio.Flux, in.Audio.Onset,
			cfg.ParticleDensity, in.Width, in.Height)
	}

	c := newCanvasWith(r.buf, &r.powScratch)
	c.background(in, pal)
	// Layered composition: the optional dimmed background layer is
	// drawn first, then the primary mode composites over it
	// (additive canvas blending). Same draw functions, dimmed
	// palette — one deterministic pipeline for preview and export.
	// The GPU shader path covers the primary radial mode only; the
	// layer is a CPU-renderer feature (reported honestly in docs).
	if cfg.BackgroundLayer != visualizer.ModeNone && cfg.BackgroundIntensity > 0 {
		layerIn := in
		layerCfg := cfg
		layerCfg.Mode = cfg.BackgroundLayer
		layerIn.Config = layerCfg
		switch cfg.BackgroundLayer {
		case visualizer.ModeBars:
			r.drawBars(c, layerIn, dimPalette(pal, cfg.BackgroundIntensity))
		case visualizer.ModeRadial:
			r.drawRadial(c, layerIn, dimPalette(pal, cfg.BackgroundIntensity))
		case visualizer.ModeWaveform:
			r.drawWaveform(c, layerIn, dimPalette(pal, cfg.BackgroundIntensity))
		case visualizer.ModeParticles:
			r.drawParticles(c, layerIn, dimPalette(pal, cfg.BackgroundIntensity))
		}
	}
	switch cfg.Mode {
	case visualizer.ModeRadial:
		r.drawRadial(c, in, pal)
	case visualizer.ModeWaveform:
		r.drawWaveform(c, in, pal)
	case visualizer.ModeParticles:
		r.drawParticles(c, in, pal)
	default:
		r.drawBars(c, in, pal)
	}
	r.drawOverlays(c, in)
	runtime.KeepAlive(c)
	return r.buf, nil
}

// paletteFor returns the palette (clamped index).
func paletteFor(i int) visualizer.Palette {
	if i < 0 || i >= len(visualizer.Palettes) {
		i = 0
	}
	return visualizer.Palettes[i]
}

// dimPalette scales every foreground color of a palette by k (0..1)
// for the background composition layer. The obsidian background is
// left untouched so the layer cannot brighten the backdrop.
func dimPalette(p visualizer.Palette, k float64) visualizer.Palette {
	k = clamp01(k)
	scale := func(c [3]float64) [3]float64 {
		return [3]float64{c[0] * k, c[1] * k, c[2] * k}
	}
	return visualizer.Palette{
		Name:   p.Name,
		Back:   p.Back,
		Low:    scale(p.Low),
		Mid:    scale(p.Mid),
		High:   scale(p.High),
		Peak:   scale(p.Peak),
		Accent: scale(p.Accent),
	}
}

// canvas is a small software drawing surface over *image.RGBA.
//
// Per-frame hot-path caching (run 42837709122 §5.3, profiled): the
// width/height are captured ONCE at construction instead of re-reading
// image.Bounds() inside addPixel — the CPU profile of the 1080p bar
// render showed Bounds()/clamp01/u8 conversions among the top costs.
// All draw math is unchanged; outputs stay bit-identical (verified by
// the deterministic frame tests and the readback benchmarks).
type canvas struct {
	img        *image.RGBA
	w          int
	h          int
	powScratch *[]float64 // shared per-renderer falloff table (may be nil)
}

// newCanvas wraps img and caches its geometry for the hot path.
// scratch (optional) is reused for per-call falloff tables; nil
// allocates per call (deterministic either way).
func newCanvasWith(img *image.RGBA, scratch *[]float64) *canvas {
	b := img.Bounds()
	return &canvas{img: img, w: b.Dx(), h: b.Dy(), powScratch: scratch}
}

// background paints the vertical obsidian gradient with a subtle
// bass-driven floor glow.
func (c *canvas) background(in visualizer.FrameInput, pal visualizer.Palette) {
	b := c.img.Bounds()
	w, h := b.Dx(), b.Dy()
	drive := 0.0
	if in.DNA != nil {
		drive = in.DNA.Loudness
	}
	floorGlow := 0.05 + 0.20*math.Min(1, in.Audio.Bass+drive*0.3)
	for y := 0; y < h; y++ {
		t := float64(y) / float64(h)
		row := c.img.Pix[y*c.img.Stride:]
		br := lerp(pal.Back[0], pal.Low[0]*0.45, t*t*floorGlow*3)
		bg := lerp(pal.Back[1], pal.Low[1]*0.45, t*t*floorGlow*3)
		bb := lerp(pal.Back[2], pal.Low[2]*0.45, t*t*floorGlow*3)
		// Hot path (profiled): the row color is constant in x — convert
		// to output bytes ONCE per row (identical values, fewer u8
		// calls) instead of once per pixel.
		r8, g8, b8 := u8(br), u8(bg), u8(bb)
		for x := 0; x < w; x++ {
			o := x * 4
			row[o] = r8
			row[o+1] = g8
			row[o+2] = b8
			row[o+3] = 255
		}
	}
}

// addPixel blends one pixel additively (clamped).
func (c *canvas) addPixel(x, y int, r, g, b, a float64) {
	if x < 0 || y < 0 || x >= c.w || y >= c.h {
		return
	}
	o := y*c.img.Stride + x*4
	p := c.img.Pix
	p[o] = u8(clamp01(float64(p[o])/255+r*a) * 255)
	p[o+1] = u8(clamp01(float64(p[o+1])/255+g*a) * 255)
	p[o+2] = u8(clamp01(float64(p[o+2])/255+b*a) * 255)
	p[o+3] = 255
}

// vline draws a vertical gradient bar with horizontal soft falloff
// (cheap glow) from yBottom (screen) up to yTop.
func (c *canvas) vbar(x, yTop, yBottom, w int, lo, hi [3]float64, glow float64) {
	if yTop > yBottom {
		yTop, yBottom = yBottom, yTop
	}
	hh := yBottom - yTop
	// Hot-path arithmetic (profiled): math.Pow(t, 0.85) depends only on
	// y, but the old loop recomputed it for every (dx, y) pair. One
	// reused table per call produces the IDENTICAL values (same
	// inputs, same function) while cutting pow calls by the dx-column
	// count and keeping the steady state allocation-free.
	var powT []float64
	if c.powScratch != nil {
		if cap(*c.powScratch) < hh+1 {
			*c.powScratch = make([]float64, hh+1)
		}
		powT = (*c.powScratch)[:hh+1]
	} else {
		powT = make([]float64, hh+1)
	}
	for y := yTop; y <= yBottom; y++ {
		t := 0.0
		if hh > 0 {
			t = float64(yBottom-y) / float64(hh)
		}
		powT[y-yTop] = math.Pow(t, 0.85)
	}
	for dx := -w; dx <= w; dx++ {
		fall := 1 - math.Abs(float64(dx))/(float64(w)+1)
		if fall <= 0 {
			continue
		}
		alpha := math.Pow(fall, 1.5+glow)
		if dx != 0 && alpha < 0.03 {
			continue
		}
		xx := x + dx
		for y := yTop; y <= yBottom; y++ {
			t := powT[y-yTop]
			r := lerp(lo[0], hi[0], t)
			g := lerp(lo[1], hi[1], t)
			b := lerp(lo[2], hi[2], t)
			c.addPixel(xx, y, r, g, b, alpha*0.9)
		}
	}
}

// line draws a soft additive line segment.
func (c *canvas) line(x0, y0, x1, y1, thick float64, r, g, b, a float64) {
	minX := int(math.Floor(math.Min(x0, x1) - thick - 1))
	maxX := int(math.Ceil(math.Max(x0, x1) + thick + 1))
	minY := int(math.Floor(math.Min(y0, y1) - thick - 1))
	maxY := int(math.Ceil(math.Max(y0, y1) + thick + 1))
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			d := distToSeg(float64(x)+0.5, float64(y)+0.5, x0, y0, x1, y1)
			if d > thick {
				continue
			}
			t := 1 - d/thick
			c.addPixel(x, y, r, g, b, a*t*t)
		}
	}
}

// glowCircle draws an additive radial glow.
func (c *canvas) glowCircle(cx, cy, radius, intensity float64, r, g, b float64) {
	if radius <= 0 || intensity <= 0 {
		return
	}
	minX := int(math.Floor(cx - radius*2))
	maxX := int(math.Ceil(cx + radius*2))
	minY := int(math.Floor(cy - radius*2))
	maxY := int(math.Ceil(cy + radius*2))
	// Hot-path arithmetic (profiled): the squared-distance test rejects
	// every out-of-disc box pixel WITHOUT the sqrt; the in-disc branch
	// keeps the identical d = sqrt(d2) value the alpha uses. At the
	// disc boundary any float rounding flip changes t by <1 ulp where
	// alpha = intensity*t*t rounds to the same u8 output.
	limit2 := (radius * 2) * (radius * 2)
	for y := minY; y <= maxY; y++ {
		dy := float64(y) + 0.5 - cy
		dy2 := dy * dy
		for x := minX; x <= maxX; x++ {
			dx := float64(x) + 0.5 - cx
			d2 := dx*dx + dy2
			if d2 > limit2 {
				continue
			}
			d := math.Sqrt(d2)
			t := 1 - d/(radius*2)
			if t < 0 {
				t = 0
			}
			c.addPixel(x, y, r, g, b, intensity*t*t)
		}
	}
}

// ---------------------------------------------------------------------------
// Modes
// ---------------------------------------------------------------------------

func (r *CPURenderer) drawBars(c *canvas, in visualizer.FrameInput, pal visualizer.Palette) {
	w, h := in.Width, in.Height
	n := visualizer.NumVizBins
	margin := int(float64(w) * 0.045)
	baseline := int(float64(h) * 0.88)
	maxH := int(float64(h) * 0.72)
	slot := float64(w-2*margin) / float64(n)
	barW := int(math.Max(2, slot*0.62))
	gapX := margin + int(slot*0.19)
	for i := 0; i < n; i++ {
		v := math.Pow(r.scene.Bin(i), 0.8)
		peaks := r.scene.Peak(i)
		x := gapX + int(float64(i)*slot)
		bh := int(v * float64(maxH) * (0.25 + 0.75*clamp01(in.Config.PulseResponse*(0.6+0.4*r.scene.BassDrive()))))
		if bh < 1 && v > 0.02 {
			bh = 1
		}
		hi := colorHigh(pal, i, n)
		c.vbar(x+barW/2, baseline-bh, baseline, barW, pal.Low, hi, 1.2+in.Config.Glow)
		// peak cap
		py := baseline - int(peaks*float64(maxH))
		if py > baseline-2 {
			py = baseline - 2
		}
		for dx := 0; dx < barW; dx++ {
			c.addPixel(x+dx, py, pal.Peak[0], pal.Peak[1], pal.Peak[2], 0.9)
			c.addPixel(x+dx, py+1, pal.Peak[0], pal.Peak[1], pal.Peak[2], 0.35)
		}
	}
	// floor line + bass underglow
	c.glowCircle(float64(w)/2, float64(baseline)+float64(h)*0.03, float64(w)*0.22*(0.4+r.scene.BassDrive()), 0.10+0.20*r.scene.BassDrive(), pal.Mid[0], pal.Mid[1], pal.Mid[2])
	for x := margin; x < w-margin; x++ {
		c.addPixel(x, baseline+1, pal.Mid[0], pal.Mid[1], pal.Mid[2], 0.5)
	}
}

func (r *CPURenderer) drawRadial(c *canvas, in visualizer.FrameInput, pal visualizer.Palette) {
	w, h := in.Width, in.Height
	n := visualizer.NumVizBins
	cx, cy := float64(w)/2, float64(h)/2
	baseR := math.Min(float64(w), float64(h)) * 0.20
	baseR *= 1 + 0.10*r.scene.BassDrive()*in.Config.PulseResponse + 0.08*r.scene.BeatPulse()
	maxLen := math.Min(float64(w), float64(h)) * 0.30
	rot := in.Time * (0.15 + 0.1*float64(in.Config.MotionStyle%3))
	for i := 0; i < n; i++ {
		v := math.Pow(r.scene.Bin(i), 0.85)
		ang := rot + float64(i)/float64(n)*2*math.Pi
		// mirror for a symmetric second half
		for _, a := range []float64{ang, -ang} {
			ca, sa := math.Cos(a), math.Sin(a)
			x0, y0 := cx+ca*baseR, cy+sa*baseR
			x1, y1 := cx+ca*(baseR+v*maxLen), cy+sa*(baseR+v*maxLen)
			hi := colorHigh(pal, i, n)
			c.line(x0, y0, x1, y1, math.Max(1.5, float64(w)/640*2.2), hi[0], hi[1], hi[2], 0.55+0.35*v)
		}
	}
	// inner ring
	ringR := baseR * 0.92
	segments := 96
	for i := 0; i < segments; i++ {
		a0 := float64(i) / float64(segments) * 2 * math.Pi
		a1 := float64(i+1) / float64(segments) * 2 * math.Pi
		c.line(cx+math.Cos(a0)*ringR, cy+math.Sin(a0)*ringR,
			cx+math.Cos(a1)*ringR, cy+math.Sin(a1)*ringR,
			math.Max(1, float64(w)/960*1.4), pal.Mid[0], pal.Mid[1], pal.Mid[2], 0.5)
	}
	c.glowCircle(cx, cy, baseR*0.85*(1+0.25*r.scene.BeatPulse()),
		0.18+0.35*r.scene.BassDrive()+in.Config.Glow*0.1, pal.Low[0], pal.Low[1], pal.Low[2])
}

func (r *CPURenderer) drawWaveform(c *canvas, in visualizer.FrameInput, pal visualizer.Palette) {
	wf := in.Audio.WaveWindow
	if len(wf) < 8 {
		wf = []float32{0, 0}
	}
	cy := float64(in.Height) / 2
	amp := float64(in.Height) * 0.30 * math.Min(3, in.Config.Sensitivity)
	accent := pal.High
	// mirror ghost line
	for pass, alpha := range []float64{0.16, 0.75} {
		_ = pass
		for i := 0; i+1 < len(wf); i++ {
			x0 := float64(i) / float64(len(wf)-1) * float64(in.Width)
			x1 := float64(i+1) / float64(len(wf)-1) * float64(in.Width)
			y0 := cy - float64(wf[i])*amp
			y1 := cy - float64(wf[i+1])*amp
			if pass == 1 {
				y0, y1 = cy+float64(wf[i])*amp, cy+float64(wf[i+1])*amp
			}
			energy := float64(wf[i]+wf[i+1]) * 0.5
			c.line(x0, y0, x1, y1, math.Max(1.2, float64(in.Width)/960*2.0)*(1+energy),
				lerp(pal.Mid[0], accent[0], energy), lerp(pal.Mid[1], accent[1], energy),
				lerp(pal.Mid[2], accent[2], energy), alpha)
		}
	}
	// frequency-reactive accents: thin bars along the bottom
	n := visualizer.NumVizBins
	baseY := float64(in.Height) * 0.94
	for i := 0; i < n; i++ {
		v := r.scene.Bin(i)
		if v < 0.03 {
			continue
		}
		x := int(float64(i) / float64(n) * float64(in.Width))
		c.line(float64(x), baseY, float64(x), baseY-float64(in.Height)*0.06*v,
			2, pal.Low[0], pal.Low[1], pal.Low[2], 0.35+v*0.3)
	}
	c.glowCircle(float64(in.Width)/2, cy, float64(in.Width)*0.16*(0.5+r.scene.BassDrive()),
		0.05+0.12*r.scene.BassDrive(), pal.Low[0], pal.Low[1], pal.Low[2])
}

func (r *CPURenderer) drawParticles(c *canvas, in visualizer.FrameInput, pal visualizer.Palette) {
	w, h := float64(in.Width), float64(in.Height)
	// ember particles
	for i := 0; i < r.scene.ParticleCount(); i++ {
		x, y, life, size, heat := r.scene.Particle(i)
		px, py := x*w, y*h
		rad := (2 + 5*size) * (w / 1920)
		if rad < 1.2 {
			rad = 1.2
		}
		a := life * life * (0.35 + 0.45*heat)
		col := pal.Low
		if heat > 0.6 {
			col = pal.High
		} else if heat > 0.3 {
			col = pal.Mid
		}
		c.glowCircle(px, py, rad, a+in.Config.Glow*0.1, col[0], col[1], col[2])
	}
	// central ember bowl
	cx, cy := w/2, h*0.78
	c.glowCircle(cx, cy, w*0.12*(1+0.3*r.scene.BassDrive()), 0.10+0.25*r.scene.BassDrive(),
		pal.Low[0], pal.Low[1], pal.Low[2])
}

// drawOverlays renders title/subtitle and the watermark.
func (r *CPURenderer) drawOverlays(c *canvas, in visualizer.FrameInput) {
	cfg := in.Config
	if cfg.ShowTitle {
		size := math.Max(16, float64(in.Height)*0.045)
		if f, err := newTextFace(size); err == nil {
			defer f.face.Close()
			x := int(float64(in.Width) * 0.045)
			y := int(float64(in.Height) * 0.90)
			f.drawString(c.img, cfg.TitleText, x, y, color.NRGBA{R: 242, G: 238, B: 238, A: 235}, true)
			if cfg.SubtitleText != "" {
				if f2, e2 := newTextFace(size * 0.6); e2 == nil {
					f2.drawString(c.img, cfg.SubtitleText, x, y+int(size*0.9),
						color.NRGBA{R: 201, G: 184, B: 184, A: 200}, true)
					f2.face.Close()
				}
			}
		}
	}
	if cfg.ShowWatermark {
		drawWatermark(c.img, in.Width, in.Height)
	}
}

// helpers

func lerp(a, b, t float64) float64 { return a + (b-a)*t }

func u8(v float64) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

func colorHigh(pal visualizer.Palette, i, n int) [3]float64 {
	t := float64(i) / float64(n-1)
	return [3]float64{
		lerp(pal.Mid[0], pal.High[0], t),
		lerp(pal.Mid[1], pal.High[1], t),
		lerp(pal.Mid[2], pal.High[2], t),
	}
}

func distToSeg(px, py, x0, y0, x1, y1 float64) float64 {
	dx, dy := x1-x0, y1-y0
	l2 := dx*dx + dy*dy
	if l2 == 0 {
		return math.Hypot(px-x0, py-y0)
	}
	t := ((px-x0)*dx + (py-y0)*dy) / l2
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	return math.Hypot(px-(x0+t*dx), py-(y0+t*dy))
}
