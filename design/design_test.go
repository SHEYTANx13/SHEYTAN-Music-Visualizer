package design

import (
	"strings"
	"testing"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/brand"
)

func TestLoadCanonicalTokens(t *testing.T) {
	doc, err := Load()
	if err != nil {
		t.Fatalf("canonical tokens must load: %v", err)
	}
	if doc.Identity != "BETA" {
		t.Fatalf("identity %q, want BETA", doc.Identity)
	}
	if doc.DefaultIntensity != "ritual" {
		t.Fatalf("default intensity %q", doc.DefaultIntensity)
	}
	if _, ok := doc.IntensityPresets["subtle"]; !ok {
		t.Fatal("missing subtle preset")
	}
	if _, ok := doc.IntensityPresets["infernal"]; !ok {
		t.Fatal("missing infernal preset")
	}
	if doc.Decoration.MaxDecorationOpacity <= 0 || doc.Decoration.MaxDecorationOpacity >= 0.2 {
		t.Fatalf("decoration opacity contract broken: %v", doc.Decoration.MaxDecorationOpacity)
	}
	if len(doc.Accessibility.ContrastPairs) < 4 {
		t.Fatal("contrast pairs under-documented")
	}
}

func TestIdentityColorsMatchBrand(t *testing.T) {
	doc, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	surface := doc.Color.Surface
	if surface["obsidian"] != brand.ColorObsidian {
		t.Errorf("obsidian drift: tokens=%s brand=%s", surface["obsidian"], brand.ColorObsidian)
	}
	if doc.Color.Accent["darkBlood"] != brand.ColorDarkBlood {
		t.Errorf("darkBlood drift: tokens=%s brand=%s", doc.Color.Accent["darkBlood"], brand.ColorDarkBlood)
	}
	if doc.Color.Accent["infernalRed"] != brand.ColorInfernalRed {
		t.Errorf("infernalRed drift: tokens=%s brand=%s", doc.Color.Accent["infernalRed"], brand.ColorInfernalRed)
	}
	if doc.Color.Text["primary"] != brand.ColorPrimaryText {
		t.Errorf("primary text drift: tokens=%s brand=%s", doc.Color.Text["primary"], brand.ColorPrimaryText)
	}
}

func TestRawServesSameDocument(t *testing.T) {
	raw := Raw()
	if len(raw) == 0 {
		t.Fatal("raw tokens empty")
	}
	if !strings.Contains(string(raw), "\"schemaVersion\": 1") {
		t.Fatal("raw JSON missing schemaVersion")
	}
}
