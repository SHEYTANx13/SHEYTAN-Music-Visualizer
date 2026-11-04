package brand

import (
	"strings"
	"testing"
)

func TestProductIdentityIsExact(t *testing.T) {
	if ProductName != "SHEYTAN Music Visualizer" {
		t.Fatalf("product name drifted: %q", ProductName)
	}
	if BrandTag != "SHEYTAN™" {
		t.Fatalf("brand tag drifted: %q", BrandTag)
	}
	if Publisher != "Parsaetak" || Author != "Parsa Tak" {
		t.Fatalf("attribution drifted: publisher=%q author=%q", Publisher, Author)
	}
}

func TestLegalFooterCarriesCopyrightAndTrademark(t *testing.T) {
	if !strings.Contains(LegalFooter, "© 2024–2026 Parsaetak. All rights reserved.") {
		t.Fatalf("legal footer missing canonical copyright: %q", LegalFooter)
	}
	if !strings.Contains(LegalFooter, "trademarks of Parsaetak") {
		t.Fatalf("legal footer missing trademark statement: %q", LegalFooter)
	}
	// The trademark position must not imply open-source reuse rights.
	if strings.Contains(strings.ToLower(TrademarkStatement), "open source") {
		t.Fatalf("trademark statement must not imply open source: %q", TrademarkStatement)
	}
}

func TestPaletteTokensAreWellFormedHex(t *testing.T) {
	for name, hex := range map[string]string{
		"Obsidian":        Theme.Obsidian,
		"DeepCharcoal":    Theme.DeepCharcoal,
		"VolcanicSurface": Theme.VolcanicSurface,
		"DarkBlood":       Theme.DarkBlood,
		"InfernalRed":     Theme.InfernalRed,
		"EmberHighlight":  Theme.EmberHighlight,
		"HotEmberAccent":  Theme.HotEmberAccent,
		"PrimaryText":     Theme.PrimaryText,
		"SecondaryText":   Theme.SecondaryText,
		"Divider":         Theme.Divider,
	} {
		if len(hex) != 7 || hex[0] != '#' {
			t.Fatalf("palette token %s malformed: %q", name, hex)
		}
		for _, c := range hex[1:] {
			if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
				t.Fatalf("palette token %s has bad hex digit: %q", name, hex)
			}
		}
	}
}

func TestPlaceholderMentionsPhase2(t *testing.T) {
	if !strings.Contains(PhasePlaceholder, "Phase 2") {
		t.Fatalf("placeholder must point to Phase 2: %q", PhasePlaceholder)
	}
}
