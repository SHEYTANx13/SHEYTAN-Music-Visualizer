// theme.go — the SHEYTAN hellish black/red theme built from the
// canonical design tokens (design.MustLoad()) over the go-gui theme
// system (github.com/go-gui-org/go-gui). Black dominates; red is the
// accent for primary actions, focus and meaningful emphasis only.
//
// Run installs gui.ThemeDark as the app default BEFORE the window is
// created (dark polarity, dark titlebars); the token document then
// drives every brand color through this ShellTheme: widget fills,
// borders and text styles carry the exact token values, and the
// go-gui roles (gui.CurrentTheme().TextStyleTitle & co) provide the
// typography.
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

	"github.com/go-gui-org/go-gui/gui"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/design"
)

// ShellTheme owns the token-derived theme for the single window. It is
// safe for concurrent use: the frame thread reads colors every frame,
// while the settings panel switches presets and flags.
type ShellTheme struct {
	mu  sync.Mutex
	doc design.TokenDocument

	intensityName string
	intensity     design.Intensity

	reducedMotion bool
	reducedGlow   bool

	// touchTargetDp is the resolved minimum interactive-target
	// height for the current responsive class (dp); 48 until the
	// shell reports a width. See TouchTargetForWidth.
	touchTargetDp float32

	// spacing is the resolved responsive spacing profile; the phone
	// profile is the safest default until the shell reports a width.
	spacing SpacingProfile
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

// guiColor converts a token-derived color into the go-gui Color type
// (explicitly set so widget defaults never mistake it for "unset").
func guiColor(c color.NRGBA) gui.Color {
	return gui.RGBA(c.R, c.G, c.B, c.A)
}

// NewShellTheme builds the brand theme from the validated token
// document. Identity values mirror internal/brand constants; the JSON
// document is the presentation authority.
func NewShellTheme(doc design.TokenDocument) *ShellTheme {
	t := &ShellTheme{doc: doc, touchTargetDp: 48, spacing: SpacingProfile{BarPadTop: 6, BarPadBottom: 6, BarPadH: 10, PanelPadV: 12, PanelPadH: 12, RowPadV: 5}}
	t.intensityName = doc.DefaultIntensity
	t.intensity = doc.IntensityPresets[doc.DefaultIntensity]
	return t
}

// TokenDoc returns the loaded token document.
func (t *ShellTheme) TokenDoc() design.TokenDocument { return t.doc }

// TouchTargetDp is the resolved minimum interactive-target height for
// the current window class (dp). The token document carries the
// authority per breakpoint (48 dp for phone/tablet, 32 dp for
// desktop/wide); until the shell reports the first window width the
// safest default is the largest documented value.
func (t *ShellTheme) TouchTargetDp() float32 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.touchTargetDp
}

// TouchTargetForWidth resolves the token document's
// touchTargetMinDp for the responsive class of widthDp. Fallbacks
// mirror the documented token values when a class entry omits the
// field, so a token edit can never silently shrink targets to zero.
// Pure; safe for concurrent use.
func (t *ShellTheme) TouchTargetForWidth(widthDp float32) float32 {
	var cls design.Breakpoint
	switch {
	case widthDp < breakpoints.TabletMin:
		cls = t.doc.Breakpoints.Phone
	case widthDp < breakpoints.DesktopMin:
		cls = t.doc.Breakpoints.Tablet
	case widthDp < breakpoints.WideMin:
		cls = t.doc.Breakpoints.Desktop
	default:
		cls = t.doc.Breakpoints.Wide
	}
	if cls.TouchTargetMinDp > 0 {
		return float32(cls.TouchTargetMinDp)
	}
	if widthDp < breakpoints.DesktopMin {
		return 48
	}
	return 32
}

// SetTouchTargetForWidth stores the resolved minimum for the shell's
// current width; every frame's control composition reads it through
// TouchTargetDp so pointer and touch targets always meet the class
// minimum (run 45097038674 touch-safety repair: the token existed but
// was never applied).
func (t *ShellTheme) SetTouchTargetForWidth(widthDp float32) {
	v := t.TouchTargetForWidth(widthDp)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.touchTargetDp = v
}

// SpacingProfile is the token-driven responsive spacing set the shell
// resolves per width class (run 45182658992 space-management pass;
// run 45297707372 adds the scroll-bar sizing). Phones/tablets get
// roomier bars, panels, rows and scroll bars (touch-friendly);
// desktop/wide classes tighten vertically for a denser, professional
// workspace while keeping comfortable horizontal gutters and a clearly
// visible, grabbable scroll bar.
type SpacingProfile struct {
	BarPadTop    float32
	BarPadBottom float32
	BarPadH      float32
	PanelPadV    float32
	PanelPadH    float32
	RowPadV      float32
	// ScrollbarSize is the width of the vertical scroll-bar thumb and
	// gutter (dp). The go-gui theme default (7dp) is too thin to see on
	// the dark brand surfaces and too fine to grab with a finger; every
	// scroll surface resolves this per class (run 45297707372).
	ScrollbarSize float32
}

// SpacingForWidth resolves the profile for the responsive class of
// widthDp. Pure; values derive from the token spacing scale
// (4/8/12/16/24) with documented per-class choices.
func (t *ShellTheme) SpacingForWidth(widthDp float32) SpacingProfile {
	switch {
	case widthDp < breakpoints.TabletMin:
		return SpacingProfile{BarPadTop: 6, BarPadBottom: 6, BarPadH: 10, PanelPadV: 12, PanelPadH: 12, RowPadV: 5, ScrollbarSize: 14}
	case widthDp < breakpoints.DesktopMin:
		return SpacingProfile{BarPadTop: 6, BarPadBottom: 6, BarPadH: 12, PanelPadV: 14, PanelPadH: 14, RowPadV: 5, ScrollbarSize: 14}
	case widthDp < breakpoints.WideMin:
		return SpacingProfile{BarPadTop: 4, BarPadBottom: 4, BarPadH: 10, PanelPadV: 10, PanelPadH: 12, RowPadV: 3, ScrollbarSize: 10}
	default:
		return SpacingProfile{BarPadTop: 4, BarPadBottom: 4, BarPadH: 12, PanelPadV: 12, PanelPadH: 14, RowPadV: 3, ScrollbarSize: 10}
	}
}

// ScrollbarCfgForWidth resolves the vertical scroll-bar configuration
// for the responsive class of widthDp (run 45297707372 scrolling
// repair): a visible, rounded thumb in the accent ink over the surface
// ink, sized for the class (fatter on touch classes), with a 40dp
// minimum thumb so short content stays draggable. Pure.
func (t *ShellTheme) ScrollbarCfgForWidth(widthDp float32) gui.ScrollbarCfg {
	sp := t.SpacingForWidth(widthDp)
	size := sp.ScrollbarSize
	thumb := t.Accent()
	thumb.A = 0xC8 // ~78% alpha: clearly visible, never garish
	bg := t.Surface()
	bg.A = 0x50
	return gui.ScrollbarCfg{
		Size:            size,
		MinThumbSize:    40,
		RadiusThumb:     size / 2,
		Radius:          gui.RadiusPx(size / 3),
		ColorThumb:      guiColor(thumb),
		ColorBackground: guiColor(bg),
	}
}

// SetSpacingForWidth stores the profile resolved for the shell's
// current width; composition helpers read it through Spacing().
func (t *ShellTheme) SetSpacingForWidth(widthDp float32) {
	p := t.SpacingForWidth(widthDp)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.spacing = p
}

// Spacing is the currently resolved spacing profile (phone-roomy
// defaults until the shell reports a width).
func (t *ShellTheme) Spacing() SpacingProfile {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.spacing
}

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
// hard-coded locally. The color.NRGBA getters are the pure token
// surface (unit-tested); the gui.Color variants feed the widgets.

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

// ObsidianColor … DividerColor are the same token colors as go-gui
// Color values for widget fills and borders.

func (t *ShellTheme) ObsidianColor() gui.Color { return guiColor(t.Obsidian()) }
func (t *ShellTheme) SurfaceColor() gui.Color  { return guiColor(t.Surface()) }
func (t *ShellTheme) VolcanicColor() gui.Color { return guiColor(t.Volcanic()) }
func (t *ShellTheme) AccentColor() gui.Color   { return guiColor(t.Accent()) }
func (t *ShellTheme) GoldColor() gui.Color     { return guiColor(t.Gold()) }
func (t *ShellTheme) TextDimColor() gui.Color  { return guiColor(t.TextDim()) }

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

// DecorationBorderColor is the DecorationColor as the go-gui border
// color the panel surfaces draw their hairline accent edge with.
func (t *ShellTheme) DecorationBorderColor() gui.Color {
	return guiColor(t.DecorationColor())
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

// GlowBorderColor is the GlowColor as the go-gui border color of the
// preview chrome frame.
func (t *ShellTheme) GlowBorderColor() gui.Color { return guiColor(t.GlowColor()) }

// IntensityEmbers reports whether the active preset draws ember marks.
func (t *ShellTheme) IntensityEmbers() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.intensity.Embers
}

// Text styles — the go-gui theme roles (CurrentTheme) provide the
// typography; the token colors provide the ink. Every helper returns a
// fresh value, so callers may adjust further.

// textStyle recolors a themed role with an explicit token ink.
func textStyle(role gui.TextStyle, ink color.NRGBA) gui.TextStyle {
	role.Color = guiColor(ink)
	return role
}

// TextStyleTitle is the window/panel title role with the token primary
// text color.
func (t *ShellTheme) TextStyleTitle() gui.TextStyle {
	return textStyle(gui.CurrentTheme().TextStyleTitle, t.Text())
}

// TextStyleSection is the section-heading role with the accent ink
// (the crimson section headings the panels draw).
func (t *ShellTheme) TextStyleSection() gui.TextStyle {
	return textStyle(gui.CurrentTheme().TextStyleTitle, t.Accent())
}

// TextStyleBody is the body role with the token primary text color.
func (t *ShellTheme) TextStyleBody() gui.TextStyle {
	return textStyle(gui.CurrentTheme().TextStyleBody, t.Text())
}

// TextStyleCaption is the caption role with the token secondary
// (dimmed) text color.
func (t *ShellTheme) TextStyleCaption() gui.TextStyle {
	return textStyle(gui.CurrentTheme().TextStyleCaption, t.TextDim())
}

// TextStyleAccentCaption is the caption role with the accent ink.
func (t *ShellTheme) TextStyleAccentCaption() gui.TextStyle {
	return textStyle(gui.CurrentTheme().TextStyleCaption, t.Accent())
}

// TextStyleStatus recolors the caption role with a status color.
func (t *ShellTheme) TextStyleStatus(c color.NRGBA) gui.TextStyle {
	return textStyle(gui.CurrentTheme().TextStyleCaption, c)
}
