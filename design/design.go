// Package design is the canonical design-token source for SHEYTAN
// Music Visualizer.
//
// design/tokens.json is the single framework-independent authority for
// the SHEYTAN visual identity (obsidian surfaces, deep crimson/blood-red
// primary accents, restrained ember and antique-gold secondary accents,
// accessible status colors, intensity presets, breakpoints and motion).
// Both presentation shells — the native Go-Gui (Gio) shell and the Wails
// v3 web shell — consume these values from this package (or, in the web
// shell, from the same JSON served as an asset); they must never
// hard-code presentation values locally.
//
// Color identity values mirror internal/brand constants; brand remains
// the identity authority and this JSON the presentation encoding of the
// same direction. A package test cross-checks the overlapping values so
// the two files can never silently drift apart.
package design

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

//go:embed tokens.json
var raw []byte

// TokenDocument is the decoded shape of design/tokens.json.
type TokenDocument struct {
	SchemaVersion    int                       `json:"schemaVersion"`
	Product          string                    `json:"product"`
	Identity         string                    `json:"identity"`
	Description      string                    `json:"description"`
	Color            ColorTokens               `json:"color"`
	Typography       TypographyTokens          `json:"typography"`
	Spacing          SpacingTokens             `json:"spacing"`
	Radius           map[string]int            `json:"radius"`
	Elevation        map[string]ElevationLevel `json:"elevation"`
	Motion           MotionTokens              `json:"motion"`
	Decoration       DecorationTokens          `json:"decoration"`
	IntensityPresets map[string]Intensity      `json:"intensityPresets"`
	DefaultIntensity string                    `json:"defaultIntensity"`
	Breakpoints      BreakpointTokens          `json:"breakpoints"`
	Accessibility    AccessibilityTokens       `json:"accessibility"`
}

// ColorTokens groups surface, accent, secondary, text, line and status
// colors. Every value is an #RRGGBB string.
type ColorTokens struct {
	Surface   map[string]string `json:"surface"`
	Accent    map[string]string `json:"accent"`
	Secondary map[string]string `json:"secondary"`
	Text      map[string]string `json:"text"`
	Line      map[string]string `json:"line"`
	Status    map[string]string `json:"status"`
}

// TypographyTokens defines the family stack and the type scale.
type TypographyTokens struct {
	FamilyStack []string          `json:"familyStack"`
	Scale       map[string]TypeRp `json:"scale"`
}

// TypeRp is one type-scale entry (size in dp, weight 1-1000).
type TypeRp struct {
	Size   int `json:"size"`
	Weight int `json:"weight"`
}

// SpacingTokens defines the spacing unit and the step ramp.
type SpacingTokens struct {
	Unit  int   `json:"unit"`
	Steps []int `json:"steps"`
}

// MotionTokens defines durations, easings and the two accessibility
// reduction switches.
type MotionTokens struct {
	DurationMs    map[string]int    `json:"durationMs"`
	Easing        map[string]string `json:"easing"`
	ReducedMotion bool              `json:"reducedMotion"`
	ReducedGlow   bool              `json:"reducedGlow"`
}

// DecorationTokens bound all decorative art direction.
type DecorationTokens struct {
	MagicCircleRings     int     `json:"magicCircleRings"`
	OrbitNodes           int     `json:"orbitNodes"`
	RuneMarks            bool    `json:"runeMarks"`
	RadialLight          bool    `json:"radialLight"`
	MaxDecorationOpacity float64 `json:"maxDecorationOpacity"`
	MaxGlowStrength      float64 `json:"maxGlowStrength"`
	Embers               bool    `json:"embers"`
	Note                 string  `json:"note"`
}

// Intensity is one theme-intensity preset (Subtle, Ritual, Infernal).
type Intensity struct {
	DecorationOpacity float64 `json:"decorationOpacity"`
	GlowStrength      float64 `json:"glowStrength"`
	Embers            bool    `json:"embers"`
}

// BreakpointTokens defines the phone/tablet/desktop/wide classes.
type BreakpointTokens struct {
	Phone   Breakpoint `json:"phone"`
	Tablet  Breakpoint `json:"tablet"`
	Desktop Breakpoint `json:"desktop"`
	Wide    Breakpoint `json:"wide"`
}

// Breakpoint is one responsive class with its dp window and layout mode.
type Breakpoint struct {
	MaxDp            int    `json:"maxDp,omitempty"`
	MinDp            int    `json:"minDp,omitempty"`
	Layout           string `json:"layout"`
	TouchTargetMinDp int    `json:"touchTargetMinDp,omitempty"`
	ContentCapDp     int    `json:"contentCapDp,omitempty"`
}

// AccessibilityTokens carries focus, contrast and screen-reader rules.
type AccessibilityTokens struct {
	FocusRing        FocusRing       `json:"focusRing"`
	MinContrastBody  float64         `json:"minContrastBody"`
	MinContrastLarge float64         `json:"minContrastLarge"`
	ContrastPairs    []ContrastPair  `json:"contrastPairs"`
	ScreenReader     map[string]bool `json:"screenReader"`
}

// FocusRing is the visible keyboard-focus treatment.
type FocusRing struct {
	Color    string `json:"color"`
	WidthDp  int    `json:"widthDp"`
	OffsetDp int    `json:"offsetDp"`
}

// ContrastPair is one documented foreground/background contrast ratio.
type ContrastPair struct {
	Foreground string  `json:"foreground"`
	Background string  `json:"background"`
	Ratio      float64 `json:"ratio"`
}

// Load decodes and validates the canonical token document. It fails
// closed: any structural violation (missing version, missing preset,
// bad hex) is an error, never a silent default.
func Load() (TokenDocument, error) {
	var doc TokenDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return TokenDocument{}, fmt.Errorf("design: tokens.json decode: %w", err)
	}
	if doc.SchemaVersion != 1 {
		return TokenDocument{}, fmt.Errorf("design: unsupported schemaVersion %d", doc.SchemaVersion)
	}
	if doc.Product == "" || doc.Identity != "BETA" {
		return TokenDocument{}, fmt.Errorf("design: product/identity missing or not BETA")
	}
	if _, ok := doc.IntensityPresets[doc.DefaultIntensity]; !ok {
		return TokenDocument{}, fmt.Errorf("design: defaultIntensity %q has no preset", doc.DefaultIntensity)
	}
	for name := range doc.IntensityPresets {
		p := doc.IntensityPresets[name]
		if p.DecorationOpacity < 0 || p.DecorationOpacity > doc.Decoration.MaxDecorationOpacity {
			return TokenDocument{}, fmt.Errorf("design: preset %q decorationOpacity out of contract range", name)
		}
		if p.GlowStrength < 0 || p.GlowStrength > doc.Decoration.MaxGlowStrength {
			return TokenDocument{}, fmt.Errorf("design: preset %q glowStrength out of contract range", name)
		}
	}
	for group, colors := range map[string]map[string]string{
		"surface": doc.Color.Surface, "accent": doc.Color.Accent,
		"secondary": doc.Color.Secondary, "text": doc.Color.Text,
		"line": doc.Color.Line, "status": doc.Color.Status,
	} {
		for name, hex := range colors {
			if err := validHex(hex); err != nil {
				return TokenDocument{}, fmt.Errorf("design: color %s.%s: %w", group, name, err)
			}
		}
	}
	if doc.Accessibility.MinContrastBody < 4.5 {
		return TokenDocument{}, fmt.Errorf("design: minContrastBody below WCAG AA body threshold")
	}
	return doc, nil
}

// MustLoad returns the canonical tokens and panics on violation. Shells
// call it once at startup: a broken token document is a build-time bug,
// not a runtime condition to paper over.
func MustLoad() TokenDocument {
	doc, err := Load()
	if err != nil {
		panic(err)
	}
	return doc
}

// Raw returns the raw canonical JSON bytes (used by the Wails shell to
// serve the identical document to the web layer).
func Raw() []byte {
	out := make([]byte, len(raw))
	copy(out, raw)
	return out
}

func validHex(s string) error {
	if len(s) != 7 || s[0] != '#' {
		return fmt.Errorf("value %q is not #RRGGBB", s)
	}
	for i := 1; i < 7; i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'F' || c >= 'a' && c <= 'f') {
			return fmt.Errorf("value %q is not #RRGGBB", s)
		}
	}
	return nil
}

// ElevationLevel is one elevation step with optional tint/glow.
type ElevationLevel struct {
	ShadowOpacity float64 `json:"shadowOpacity"`
	Tint          string  `json:"tint,omitempty"`
	GlowTint      string  `json:"glowTint,omitempty"`
	GlowOpacity   float64 `json:"glowOpacity,omitempty"`
}
