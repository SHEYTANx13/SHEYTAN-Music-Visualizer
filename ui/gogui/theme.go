// theme.go — the SHEYTAN hellish black/red Gio theme built from the
// canonical design tokens (design.MustLoad()). Black dominates; red is
// the accent for primary actions, focus and meaningful emphasis only.
//
// The intensity presets (subtle / ritual / infernal) from the token
// document drive the decorative chrome parameters: the decoration
// backdrop opacity and the glow strength. The reduced-motion and
// reduced-glow accessibility switches override them honestly.

package gogui

import (
	"fmt"
	"image/color"
	"sync"

	"gioui.org/widget/material"

	"gioui.org/unit"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/design"
)

// ShellTheme owns the token-derived theme for the single window. It is
// safe for concurrent use: the event loop reads colors every frame,
// while the settings panel switches presets and flags.
type ShellTheme struct {
	mu       sync.Mutex
	doc      design.TokenDocument
	material *material.Theme

	intensityName string
	intensity     design.Intensity

	reducedMotion bool
	reducedGlow   bool
}

// parseHex converts a strict #RRGGBB token into an opaque color.NRGBA.
func parseHex(s string) (color.NRGBA, error) {
	if len(s) != 7 || s[0] != '#' {
		return color.NRGBA{}, fmt.Errorf("value %q is not #RRGGBB", s)
	}
	var v uint32
	for i := 1; i < 7; i++ {
		c := s[i]
		var d uint32
		switch {
		case c >= '0' && c <= '9':
			d = uint32(c - '0')
		case c >= 'a' && c <= 'f':
			d = uint32(c-'a') + 10
		case c >= 'A' && c <= 'F':
			d = uint32(c-'A') + 10
		default:
			return color.NRGBA{}, fmt.Errorf("value %q is not #RRGGBB", s)
		}
		v = v<<4 | d
	}
	return color.NRGBA{
		R: uint8(v >> 16),
		G: uint8(v >> 8),
		B: uint8(v),
		A: 0xff,
	}, nil
}

// mustHex is the token-loading path: the token document is validated by
// design.Load, so a failure here is a programming bug and panics.
func mustHex(s string) color.NRGBA {
	c, err := parseHex(s)
	if err != nil {
		panic(err)
	}
	return c
}

// NewShellTheme builds the brand theme from the validated token
// document. Identity values mirror internal/brand constants; the JSON
// document is the presentation authority.
func NewShellTheme(doc design.TokenDocument) *ShellTheme {
	mt := material.NewTheme()
	bg := mustHex(doc.Color.Surface["obsidian"])
	fg := mustHex(doc.Color.Text["primary"])
	accent := mustHex(doc.Color.Accent["infernalRed"])
	mt.Palette = material.Palette{
		Bg:         bg,
		Fg:         fg,
		ContrastBg: accent,
		ContrastFg: fg,
	}
	// 14sp body text matches the token type scale body size.
	mt.TextSize = unit.Sp(14)
	t := &ShellTheme{
		doc:      doc,
		material: mt,
	}
	t.intensityName = doc.DefaultIntensity
	t.intensity = doc.IntensityPresets[doc.DefaultIntensity]
	return t
}

// Theme exposes the underlying material theme (single window goroutine).
func (t *ShellTheme) Theme() *material.Theme { return t.material }

// TokenDoc returns the loaded token document.
func (t *ShellTheme) TokenDoc() design.TokenDocument { return t.doc }

// SetIntensity applies one of the token intensity presets
// (subtle/ritual/infernal). It reports whether the name was known.
func (t *ShellTheme) SetIntensity(name string) bool {
	p, ok := t.doc.IntensityPresets[name]
	if !ok {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.intensityName = name
	t.intensity = p
	return true
}

// IntensityName returns the active intensity preset name.
func (t *ShellTheme) IntensityName() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.intensityName
}

// SetReducedMotion updates the reduced-motion accessibility switch.
func (t *ShellTheme) SetReducedMotion(v bool) {
	t.mu.Lock()
	t.reducedMotion = v
	t.mu.Unlock()
}

// ReducedMotion reports the reduced-motion switch.
func (t *ShellTheme) ReducedMotion() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.reducedMotion
}

// SetReducedGlow updates the reduced-glow accessibility switch.
func (t *ShellTheme) SetReducedGlow(v bool) {
	t.mu.Lock()
	t.reducedGlow = v
	t.mu.Unlock()
}

// ReducedGlow reports the reduced-glow switch.
func (t *ShellTheme) ReducedGlow() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.reducedGlow
}

// Colors — every color is read from the token document, never
// hard-coded locally.

func (t *ShellTheme) Obsidian() color.NRGBA   { return mustHex(t.doc.Color.Surface["obsidian"]) }
func (t *ShellTheme) Surface() color.NRGBA    { return mustHex(t.doc.Color.Surface["deepCharcoal"]) }
func (t *ShellTheme) Volcanic() color.NRGBA   { return mustHex(t.doc.Color.Surface["volcanic"]) }
func (t *ShellTheme) Accent() color.NRGBA     { return mustHex(t.doc.Color.Accent["infernalRed"]) }
func (t *ShellTheme) AccentDark() color.NRGBA { return mustHex(t.doc.Color.Accent["darkBlood"]) }
func (t *ShellTheme) Ember() color.NRGBA      { return mustHex(t.doc.Color.Accent["emberHighlight"]) }
func (t *ShellTheme) HotEmber() color.NRGBA   { return mustHex(t.doc.Color.Accent["hotEmber"]) }
func (t *ShellTheme) Gold() color.NRGBA       { return mustHex(t.doc.Color.Secondary["antiqueGold"]) }
func (t *ShellTheme) Text() color.NRGBA       { return mustHex(t.doc.Color.Text["primary"]) }
func (t *ShellTheme) TextDim() color.NRGBA    { return mustHex(t.doc.Color.Text["secondary"]) }
func (t *ShellTheme) Divider() color.NRGBA    { return mustHex(t.doc.Color.Line["divider"]) }

func (t *ShellTheme) StatusSuccess() color.NRGBA { return mustHex(t.doc.Color.Status["success"]) }
func (t *ShellTheme) StatusWarning() color.NRGBA { return mustHex(t.doc.Color.Status["warning"]) }
func (t *ShellTheme) StatusError() color.NRGBA   { return mustHex(t.doc.Color.Status["error"]) }
func (t *ShellTheme) StatusInfo() color.NRGBA    { return mustHex(t.doc.Color.Status["info"]) }

// withAlpha scales a base color with an explicit alpha byte.
func withAlpha(c color.NRGBA, a uint8) color.NRGBA { c.A = a; return c }

// DecorationColor is the color of the low-opacity decorative backdrop
// chrome (accent line work). The alpha derives from the active
// intensity preset's decorationOpacity, already bounded by the token
// contract (max 0.16).
func (t *ShellTheme) DecorationColor() color.NRGBA {
	t.mu.Lock()
	defer t.mu.Unlock()
	a := uint8(t.intensity.DecorationOpacity*255 + 0.5)
	return withAlpha(mustHex(t.doc.Color.Accent["darkBlood"]), a)
}

// GlowColor is the color of the preview glow frame. The alpha derives
// from the active glowStrength (bounded at 0.6 by the token document);
// the reduced-glow switch zeroes it entirely.
func (t *ShellTheme) GlowColor() color.NRGBA {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.reducedGlow {
		return withAlpha(mustHex(t.doc.Color.Accent["emberHighlight"]), 0)
	}
	a := uint8(t.intensity.GlowStrength*255 + 0.5)
	return withAlpha(mustHex(t.doc.Color.Accent["emberHighlight"]), a)
}

// IntensityEmbers reports whether the active preset draws ember marks.
func (t *ShellTheme) IntensityEmbers() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.intensity.Embers
}
