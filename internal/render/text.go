package render

// text.go — overlay text rendering for the visualizer: song title,
// artist and the SHEYTAN™ watermark. Uses the embedded Go Regular
// TrueType (golang.org/x/image/font/gofont, same licence family as
// the standard library) rasterized through x/image — no system font
// dependency, no new third-party module.

import (
	"image"
	"image/color"
	"image/draw"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

type textFace struct {
	face font.Face
}

var embeddedFont *opentype.Font

func loadEmbeddedFont() (*opentype.Font, error) {
	if embeddedFont != nil {
		return embeddedFont, nil
	}
	f, err := opentype.Parse(goregular.TTF)
	if err != nil {
		return nil, err
	}
	embeddedFont = f
	return f, nil
}

// newTextFace builds a face at the given pixel size.
func newTextFace(sizePx float64) (*textFace, error) {
	f, err := loadEmbeddedFont()
	if err != nil {
		return nil, err
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{
		Size:    sizePx,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return nil, err
	}
	return &textFace{face: face}, nil
}

// drawString renders s at (x, y) baseline with a soft shadow for
// contrast on bright visual content.
func (t *textFace) drawString(dst *image.RGBA, s string, x, y int, c color.Color, shadow bool) {
	if s == "" {
		return
	}
	d := &font.Drawer{Dst: dst, Src: image.NewUniform(c), Face: t.face}
	if shadow {
		ds := &font.Drawer{Dst: dst, Src: image.NewUniform(color.NRGBA{0, 0, 0, 160}), Face: t.face}
		dot := fixed.P(x+1, y+2)
		ds.Dot = dot
		ds.DrawString(s)
	}
	d.Dot = fixed.P(x, y)
	d.DrawString(s)
}

// Measure returns the pixel width of s.
func (t *textFace) Measure(s string) int {
	if s == "" {
		return 0
	}
	w := font.MeasureString(t.face, s)
	return int(w >> 6)
}

// drawWatermark draws the SHEYTAN™ mark bottom-right, subtle.
func drawWatermark(dst *image.RGBA, w, h int) {
	face, err := newTextFace(float64(h) / 38.0)
	if err != nil {
		return
	}
	defer face.face.Close()
	s := "SHEYTAN™"
	width := face.Measure(s)
	x := w - width - int(float64(w)*0.03)
	y := h - int(float64(h)*0.045)
	face.drawString(dst, s, x, y, color.NRGBA{R: 242, G: 238, B: 238, A: 110}, true)
}

var _ = draw.Src
