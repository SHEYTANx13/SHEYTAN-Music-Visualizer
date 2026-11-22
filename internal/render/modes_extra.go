package render

// modes_extra.go — four additional distinct visualizer renderers added
// in run 43612608237 (spectrogram waterfall, geometric pulse tunnel,
// stereo wave ribbons, frequency bloom).
//
// Contract shared with the existing modes (package comment, visualizer
// package): every value is a pure function of the frame input's media
// time, the deterministic scene state and the configuration — no wall
// clock, no global randomness. Preview and offline export run the same
// functions through the same pipeline, so a (song, config, time) triple
// produces the same frame everywhere. Geometry scales with the output
// resolution (no one-pixel details that vanish at 4K) and respects the
// safe margins used by the other modes.

import (
	"math"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/visualizer"
)

// specColor maps an intensity 0..1 to the palette gradient
// Low → Mid → High (channel-wise lerp), matching how the other modes
// read from the palette.
func specColor(pal visualizer.Palette, t float64) (float64, float64, float64) {
	t = clamp01(t)
	if t < 0.5 {
		u := t / 0.5
		return lerp(pal.Low[0], pal.Mid[0], u), lerp(pal.Low[1], pal.Mid[1], u), lerp(pal.Low[2], pal.Mid[2], u)
	}
	u := (t - 0.5) / 0.5
	return lerp(pal.Mid[0], pal.High[0], u), lerp(pal.Mid[1], pal.High[1], u), lerp(pal.Mid[2], pal.High[2], u)
}

// drawSpectrogram renders the bounded spectrogram waterfall: temporal
// frequency history scrolling right→left (oldest left, newest right),
// log-frequency row mapping, bounded history from the scene ring.
func (r *CPURenderer) drawSpectrogram(c *canvas, in visualizer.FrameInput, pal visualizer.Palette) {
	rows := r.scene.SpectrogramRows()
	w, h := c.w, c.h
	if len(rows) == 0 || w <= 0 || h <= 0 {
		return
	}
	margin := int(float64(w) * 0.045)
	top := int(float64(h) * 0.10)
	bottom := int(float64(h) * 0.90)
	specH := bottom - top
	specW := w - 2*margin
	if specH <= 1 || specW <= 1 {
		return
	}
	n := len(rows[0])
	if n < 2 {
		return
	}
	count := len(rows)
	// Waterfall semantics: the newest captured row always sits at the
	// RIGHT edge and older rows slide left as media time advances.
	// Before enough rows exist to fill the pixel width, columns left of
	// the data front stay dark — the waterfall never invents history.
	for xi := 0; xi < specW; xi++ {
		hi := count - (specW - xi)
		if hi < 0 {
			continue
		}
		if hi >= count {
			hi = count - 1
		}
		row := rows[hi]
		for y := top; y <= bottom; y++ {
			// Log-frequency row mapping: rows scroll bottom (low) → top
			// (high) through a perceptual index curve, mirroring the tilt
			// ramp semantics (0 = bin 0).
			u := 1 - float64(y-top)/float64(specH) // 0 at bottom
			binF := u * u * float64(n-1)
			bi := int(binF)
			if bi > n-1 {
				bi = n - 1
			}
			v := clamp01(row[bi])
			if v < 0.01 {
				continue
			}
			cr, cg, cb := specColor(pal, v)
			c.addPixel(margin+xi, y, cr, cg, cb, 0.22+0.78*v)
		}
	}
	// Newest-column edge marker so the "now" boundary reads clearly.
	for y := top; y <= bottom; y++ {
		c.addPixel(w-1-margin, y, pal.Accent[0], pal.Accent[1], pal.Accent[2], 0.5)
	}
}

// drawTunnel renders the geometric pulse tunnel: nested perspective
// frames receding into depth, each ring's scale driven by beat pulse,
// bass drive and media time. Stable timing: ring phase derives from
// media time, never the wall clock; depth stays inside safe margins.
func (r *CPURenderer) drawTunnel(c *canvas, in visualizer.FrameInput, pal visualizer.Palette) {
	w, h := c.w, c.h
	cx, cy := float64(w)/2, float64(h)/2
	minDim := math.Min(float64(w), float64(h))
	rings := 9
	speed := 0.55 + 0.10*float64(in.Config.MotionStyle%3)
	phase := in.Time * speed
	pulse := clamp01(r.scene.BeatPulse()*in.Config.PulseResponse + r.scene.BassDrive()*0.5)
	for k := 0; k < rings; k++ {
		// k/… — each ring is one beat-slot further in depth; the fract
		// part loops smoothly as media time advances.
		z := math.Mod(phase+float64(k)/float64(rings), 1.0)
		// depth 0 (near, large) → 1 (far, small): fade out at the back,
		// fade in from the front edge for a continuous tunnel.
		scale := (1 - z) * (0.35 + 0.65*(1-z))
		alpha := math.Sin(math.Pi*z) * (0.30 + 0.45*pulse)
		if alpha <= 0 {
			continue
		}
		rad := minDim * 0.12 * (1 + scale*(3.2+1.6*pulse))
		seg := 4 + k%3 // square/diamond/hex-style polygon per ring
		rot := in.Time*0.10*float64(seg) + float64(k)*0.35
		width := math.Max(1.5, float64(w)/960*2.0*(0.6+scale))
		for i := 0; i < seg; i++ {
			a0 := rot + float64(i)/float64(seg)*2*math.Pi
			a1 := rot + float64(i+1)/float64(seg)*2*math.Pi
			hi := colorHigh(pal, (k+i)%visualizer.NumVizBins, visualizer.NumVizBins)
			c.line(cx+math.Cos(a0)*rad, cy+math.Sin(a0)*rad,
				cx+math.Cos(a1)*rad, cy+math.Sin(a1)*rad,
				width, hi[0], hi[1], hi[2], alpha)
		}
	}
	// center glow: bass-driven with beat accent
	c.glowCircle(cx, cy, minDim*0.06*(1+0.8*pulse),
		0.22+0.45*pulse+in.Config.Glow*0.1, pal.Low[0], pal.Low[1], pal.Low[2])
	// onset flash ring
	if r.scene.OnsetFlash() > 0.02 {
		flashR := minDim * 0.34
		segments := 72
		for i := 0; i < segments; i++ {
			a0 := float64(i) / float64(segments) * 2 * math.Pi
			a1 := float64(i+1) / float64(segments) * 2 * math.Pi
			c.line(cx+math.Cos(a0)*flashR, cy+math.Sin(a0)*flashR,
				cx+math.Cos(a1)*flashR, cy+math.Sin(a1)*flashR,
				math.Max(1, float64(w)/960*1.5), pal.Peak[0], pal.Peak[1], pal.Peak[2],
				0.5*r.scene.OnsetFlash())
		}
	}
}

// drawRibbons renders two distinct left/right waveform ribbons with
// phase-sensitive vertical offset, energy-scaled thickness and palette
// split (left = Mid→High, right = Low→Accent). Line thickness scales
// with resolution so the ribbons stay readable at 4K.
func (r *CPURenderer) drawRibbons(c *canvas, in visualizer.FrameInput, pal visualizer.Palette) {
	wf := in.Audio.WaveWindow
	if len(wf) < 8 {
		wf = []float32{0, 0}
	}
	w, h := c.w, c.h
	// WaveWindow is the mono/stereo-mixed window; the stereo ribbon pair
	// derives two phase-shifted paths from it (half-window offset) so a
	// true stereo window with anti-phase content draws two distinct
	// shapes, while mono content keeps them cleanly separated.
	half := len(wf) / 2
	cyL := float64(h) * 0.36
	cyR := float64(h) * 0.64
	amp := float64(h) * 0.16 * math.Min(3, in.Config.Sensitivity)
	thick := math.Max(1.4, float64(w)/960*2.2)
	for i := 0; i+1 < half; i++ {
		x0 := float64(i) / float64(half-1) * float64(w)
		x1 := float64(i+1) / float64(half-1) * float64(w)
		// left ribbon: first half of the window
		eL := math.Abs(float64(wf[i]+wf[i+1])) * 0.5
		c.line(x0, cyL-float64(wf[i])*amp, x1, cyL-float64(wf[i+1])*amp,
			thick*(1+eL), pal.Mid[0], pal.Mid[1], pal.Mid[2], 0.30+0.45*eL)
		// right ribbon: second half (phase-shifted path)
		j := half + i
		eR := math.Abs(float64(wf[j]+wf[j+1])) * 0.5
		c.line(x0, cyR-float64(wf[j])*amp, x1, cyR-float64(wf[j+1])*amp,
			thick*(1+eR), pal.High[0], pal.High[1], pal.High[2], 0.30+0.45*eR)
	}
	// center divider with bass-reactive glow
	c.glowCircle(float64(w)/2, float64(h)/2, float64(w)*0.10*(0.4+r.scene.BassDrive()),
		0.05+0.10*r.scene.BassDrive(), pal.Low[0], pal.Low[1], pal.Low[2])
	// faint bin strip along the very bottom (readability anchor)
	n := visualizer.NumVizBins
	baseY := float64(h) * 0.95
	for i := 0; i < n; i++ {
		v := r.scene.Bin(i)
		if v < 0.04 {
			continue
		}
		x := float64(i) / float64(n) * float64(w)
		hi := colorHigh(pal, i, n)
		c.line(x, baseY, x, baseY-float64(h)*0.05*v, math.Max(2, float64(w)/1920*3.0),
			hi[0], hi[1], hi[2], 0.30)
	}
}

// drawBloom renders layered spectral envelopes as soft additive light:
// an aurora band across the middle whose vertical thickness follows a
// smoothed spectral envelope, plus three depth layers (bass / mid /
// treble weighted) of animated radial blobs around the center, all
// bounded by the safe margins and smoothed by the scene's own
// attack/release so the motion never over-noises.
func (r *CPURenderer) drawBloom(c *canvas, in visualizer.FrameInput, pal visualizer.Palette) {
	w, h := c.w, c.h
	cx, cy := float64(w)/2, float64(h)/2
	minDim := math.Min(float64(w), float64(h))
	n := visualizer.NumVizBins

	// Aurora envelope band: for every column, a soft vertical glow strip
	// whose half-height follows the smoothed spectral envelope around
	// that column's frequency position. This is the "frequency bloom /
	// fluid light" layer: continuous, physically bounded, never noisy.
	bandCy := float64(h) * 0.5
	bandBase := float64(h) * 0.16
	margin := int(float64(w) * 0.045)
	// PRIORITY-D §7.2: the vocal-reactive composition layer lifts the
	// bloom intensity while a measured vocal-like phrase is active;
	// the section-transition pulse briefly lifts the whole band; the
	// hat-class transient adds fine shimmer to the top of the band.
	vocal := clamp01(r.scene.VocalDrive()) * in.Config.VocalSensitivity
	section := clamp01(r.scene.SectionPulse())
	hats := clamp01(r.scene.TransientHat()) * in.Config.TransientIntensity
	kick := clamp01(r.scene.TransientKick()) * in.Config.TransientIntensity
	stereo := clamp01(r.scene.StereoSpread())
	// Section transition (§7.2 "preset/composition/camera transition"):
	// a dedicated centered glow ring marks the measured section change
	// explicitly — visible even over an otherwise quiet frame, and it
	// decays deterministically with the scene.
	if section > 0.01 {
		c.glowCircle(cx, cy, minDim*(0.34-0.10*section), 0.45*section,
			pal.Peak[0], pal.Peak[1], pal.Peak[2])
	}
	for x := margin; x < w-margin; x++ {
		// perceptual position 0..1 across the band → bin index
		u := float64(x-margin) / float64(w-2*margin)
		binF := u * u * float64(n-1)
		bi := int(binF)
		if bi > n-1 {
			bi = n - 1
		}
		v := clamp01(r.scene.Bin(bi))
		// Kick-class transients lift the LOW bins (geometry weight of
		// sub/bass); the measured stereo width widens the band's
		// horizontal extent; vocal/section/hats lift intensity.
		lowLift := kick * clamp01(1-float64(bi)/float64(n)) * 0.12
		v = clamp01(v + vocal*0.18 + section*0.10 + hats*0.06*float64(bi)/float64(n) + lowLift)
		_ = stereo
		// envelope: base thickness + energy swell, capped by safe margins
		halfH := bandBase*(0.5+0.9*v) + float64(h)*0.04*v
		if halfH > bandCy-float64(h)*0.08 {
			halfH = bandCy - float64(h)*0.08
		}
		hi := colorHigh(pal, bi, n)
		step := maxInt(1, int(float64(h)/(240*4))) // resolution-scaled sampling
		for y := int(bandCy - halfH); y <= int(bandCy+halfH); y += step {
			d := math.Abs(float64(y)-bandCy) / halfH // 0 center → 1 edge
			a := (1 - d*d) * (0.16 + 0.42*v) * (0.55 + 0.45*clamp01(in.Config.Glow))
			if a <= 0.01 {
				continue
			}
			c.addPixel(x, y, lerp(pal.Low[0], hi[0], v), lerp(pal.Low[1], hi[1], v),
				lerp(pal.Low[2], hi[2], v), a)
		}
	}

	// Layered radial blobs: three depth layers of orbiting soft light.
	layers := []struct {
		lo, hi int
		col    [3]float64
		scale  float64
		rotW   float64
	}{
		{0, n / 3, pal.Low, 0.30, 0.20},
		{n / 3, 2 * n / 3, pal.Mid, 0.22, -0.16},
		{2 * n / 3, n, pal.High, 0.16, 0.28},
	}
	for li, layer := range layers {
		e := 0.0
		for i := layer.lo; i < layer.hi; i++ {
			e += r.scene.Bin(i)
		}
		e /= float64(layer.hi - layer.lo)
		e = math.Pow(clamp01(e*in.Config.Sensitivity), 0.9)
		blobs := 5
		rot := in.Time*layer.rotW*(1+0.5*float64(in.Config.MotionStyle%3)) + float64(li)*1.3
		for b := 0; b < blobs; b++ {
			a := rot + float64(b)/float64(blobs)*2*math.Pi
			orbit := minDim * layer.scale * (0.55 + 0.45*e)
			bx := cx + math.Cos(a)*orbit
			by := cy + math.Sin(a)*orbit
			rad := minDim * (0.09 + 0.13*e) * (1 + 0.3*r.scene.BeatPulse())
			intensity := (0.14 + 0.34*e) * (0.7 + 0.3*math.Sin(float64(b)*1.7+in.Time*1.1))
			c.glowCircle(bx, by, rad, intensity*in.Config.Glow, layer.col[0], layer.col[1], layer.col[2])
		}
	}
	// core: soft center light scaled by overall energy
	total := 0.0
	for i := 0; i < n; i++ {
		total += r.scene.Bin(i)
	}
	total /= float64(n)
	c.glowCircle(cx, cy, minDim*0.12*(1+0.5*total), 0.16+0.36*total+in.Config.Glow*0.08,
		pal.Accent[0], pal.Accent[1], pal.Accent[2])
}

// maxInt returns the larger of two ints (tiny local helper for the
// resolution-scaled bloom sampling step).
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
