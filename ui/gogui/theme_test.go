package gogui

import (
	"image/color"
	"testing"

	"github.com/go-gui-org/go-gui/gui"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/design"
)

// TestParseHex verifies the strict #RRGGBB token parsing.
func TestParseHex(t *testing.T) {
	cases := []struct {
		in   string
		want color.NRGBA
		err  bool
	}{
		{"#080808", color.NRGBA{R: 0x08, G: 0x08, B: 0x08, A: 0xff}, false},
		{"#D51D2A", color.NRGBA{R: 0xD5, G: 0x1D, B: 0x2A, A: 0xff}, false},
		{"#ff453a", color.NRGBA{R: 0xff, G: 0x45, B: 0x3a, A: 0xff}, false},
		{"", color.NRGBA{}, true},
		{"#0808", color.NRGBA{}, true},
		{"080808", color.NRGBA{}, true},
		{"#08080G", color.NRGBA{}, true},
		{"#0808088", color.NRGBA{}, true},
	}
	for _, c := range cases {
		got, err := parseHex(c.in)
		if c.err {
			if err == nil {
				t.Errorf("parseHex(%q) expected error, got %v", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseHex(%q) unexpected error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseHex(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// mustTestDoc loads the canonical tokens (a broken document is a build
// bug — the same fail-closed behavior the shell startup uses).
func mustTestDoc(t *testing.T) design.TokenDocument {
	t.Helper()
	return design.MustLoad()
}

// TestThemeTokensFromDocument verifies the ShellTheme color surface is
// built from the token document, not hard-coded locally, and that the
// go-gui Color variants carry exactly the token values.
func TestThemeTokensFromDocument(t *testing.T) {
	doc := mustTestDoc(t)
	th := NewShellTheme(doc)
	if got, want := th.Text(), mustHex(doc.Color.Text["primary"]); got != want {
		t.Errorf("Text = %v, want %v (token text.primary)", got, want)
	}
	if got, want := th.Accent(), mustHex(doc.Color.Accent["infernalRed"]); got != want {
		t.Errorf("Accent = %v, want %v (token accent.infernalRed)", got, want)
	}
	if got, want := th.Obsidian(), mustHex(doc.Color.Surface["obsidian"]); got != want {
		t.Errorf("Obsidian = %v, want %v", got, want)
	}
	if got, want := th.Gold(), mustHex(doc.Color.Secondary["antiqueGold"]); got != want {
		t.Errorf("Gold = %v, want %v", got, want)
	}
	// The gui.Color variants are the same tokens, explicitly set.
	if got, want := th.AccentColor(), guiColor(mustHex(doc.Color.Accent["infernalRed"])); got != want {
		t.Errorf("AccentColor = %v, want %v", got, want)
	}
	if !th.AccentColor().IsSet() {
		t.Error("AccentColor must be an explicitly set gui.Color")
	}
	if got, want := th.ObsidianColor(), guiColor(mustHex(doc.Color.Surface["obsidian"])); got != want {
		t.Errorf("ObsidianColor = %v, want %v", got, want)
	}
}

// TestThemeTextStyles verifies the text style helpers carry the theme
// roles (typography) with the token ink (colors) — the go-gui role is
// the base, the token color the override.
func TestThemeTextStyles(t *testing.T) {
	doc := mustTestDoc(t)
	th := NewShellTheme(doc)
	title := th.TextStyleTitle()
	if got, want := title.Color, guiColor(th.Text()); got != want {
		t.Errorf("title ink = %v, want token primary %v", got, want)
	}
	base := gui.CurrentTheme().TextStyleTitle
	if title.Size != base.Size {
		t.Errorf("title size = %v, want the themed role size %v", title.Size, base.Size)
	}
	caption := th.TextStyleCaption()
	if got, want := caption.Color, guiColor(th.TextDim()); got != want {
		t.Errorf("caption ink = %v, want token secondary %v", got, want)
	}
	section := th.TextStyleSection()
	if got, want := section.Color, guiColor(th.Accent()); got != want {
		t.Errorf("section ink = %v, want accent %v", got, want)
	}
}

// TestIntensityPresets verifies the token presets switch the decoration
// and glow parameters exactly as the JSON document specifies.
func TestIntensityPresets(t *testing.T) {
	doc := mustTestDoc(t)
	th := NewShellTheme(doc)
	if th.IntensityName() != doc.DefaultIntensity {
		t.Fatalf("default intensity = %q, want %q", th.IntensityName(), doc.DefaultIntensity)
	}
	for name, preset := range doc.IntensityPresets {
		if !th.SetIntensity(name) {
			t.Fatalf("SetIntensity(%q) failed", name)
		}
		if th.IntensityName() != name {
			t.Errorf("intensity name = %q, want %q", th.IntensityName(), name)
		}
		wantDeco := uint8(preset.DecorationOpacity*255 + 0.5)
		if got := th.DecorationColor().A; got != wantDeco {
			t.Errorf("%s decoration alpha = %d, want %d", name, got, wantDeco)
		}
		wantGlow := uint8(preset.GlowStrength*255 + 0.5)
		if got := th.GlowColor().A; got != wantGlow {
			t.Errorf("%s glow alpha = %d, want %d", name, got, wantGlow)
		}
		if th.IntensityEmbers() != preset.Embers {
			t.Errorf("%s embers = %v, want %v", name, th.IntensityEmbers(), preset.Embers)
		}
	}
	if th.SetIntensity("bogus") {
		t.Errorf("SetIntensity(bogus) reported success")
	}
}

// TestReducedSwitches verifies the accessibility overrides: reduced
// glow zeroes the glow chrome entirely.
func TestReducedSwitches(t *testing.T) {
	doc := mustTestDoc(t)
	th := NewShellTheme(doc)
	if th.ReducedMotion() || th.ReducedGlow() {
		t.Fatalf("reduced switches must default to false")
	}
	if !th.SetIntensity("infernal") {
		t.Fatal("infernal preset missing")
	}
	th.SetReducedGlow(true)
	if got := th.GlowColor().A; got != 0 {
		t.Errorf("reduced glow alpha = %d, want 0", got)
	}
	th.SetReducedGlow(false)
	if got := th.GlowColor().A; got == 0 {
		t.Errorf("glow alpha = 0 after unsetting reduced glow")
	}
	th.SetReducedMotion(true)
	if !th.ReducedMotion() {
		t.Errorf("reduced motion flag not persisted")
	}
}

// TestBrandCrossCheck verifies the overlapping token values still match
// the internal/brand identity constants (the same drift-guard the
// design package test enforces).
func TestBrandCrossCheck(t *testing.T) {
	doc := mustTestDoc(t)
	surfaces := map[string]string{
		"obsidian":     "#080808",
		"deepCharcoal": "#111111",
		"volcanic":     "#190909",
	}
	accents := map[string]string{
		"darkBlood":      "#9F0B18",
		"infernalRed":    "#D51D2A",
		"emberHighlight": "#FF453A",
		"hotEmber":       "#FF7043",
	}
	for name, want := range surfaces {
		if got := doc.Color.Surface[name]; got != want {
			t.Errorf("token surface %s = %q, want %q", name, got, want)
		}
	}
	for name, want := range accents {
		if got := doc.Color.Accent[name]; got != want {
			t.Errorf("token accent %s = %q, want %q", name, got, want)
		}
	}
}
