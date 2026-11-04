package brand

import (
	"regexp"
	"strings"
	"testing"
)

var hexColor = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

func TestIdentityConstants(t *testing.T) {
	if ProductName != "SHEYTAN Music Visualizer" {
		t.Fatalf("ProductName = %q", ProductName)
	}
	if BrandTag != "SHEYTAN™" {
		t.Fatalf("BrandTag = %q", BrandTag)
	}
	if Publisher != "Parsaetak" {
		t.Fatalf("Publisher = %q", Publisher)
	}
	if Author != "Parsa Tak" {
		t.Fatalf("Author = %q", Author)
	}
	if WindowTitle() != ProductName {
		t.Fatalf("window title must be the exact product name")
	}
	if Copyright != "© 2024–2026 Parsaetak. All rights reserved." {
		t.Fatalf("Copyright = %q", Copyright)
	}
}

func TestTrademarkStatementHasNoOpenSourceImplication(t *testing.T) {
	stmt := strings.ToLower(TrademarkStatement)
	for _, banned := range []string{"open source", "licensed to reuse"} {
		if strings.Contains(stmt, banned) && !strings.Contains(stmt, "nothing here implies") {
			t.Fatalf("trademark statement must not imply %q", banned)
		}
	}
	if !strings.Contains(stmt, "trademarks of parsaetak") {
		t.Fatalf("trademark statement must attribute marks to Parsaetak")
	}
}

func TestPaletteTokensAreHexColors(t *testing.T) {
	palette := Palette()
	if len(palette) != 10 {
		t.Fatalf("palette must carry exactly the 10 identity tokens, got %d", len(palette))
	}
	for name, value := range palette {
		if !hexColor.MatchString(value) {
			t.Fatalf("token %s = %q is not a #RRGGBB color", name, value)
		}
	}
}

// TestTwoPartyAttributionURLs pins the exact legal/brand links: the
// Parsaetak licensor/rights URL and the SHEYTAN project/brand URL must
// both be present, correctly spelled, and the stale Parsaetak GitHub
// destination must not be the official licensor link.
func TestTwoPartyAttributionURLs(t *testing.T) {
	if LicensorURL != "https://linktr.ee/Parsaetak" {
		t.Fatalf("LicensorURL = %q, want https://linktr.ee/Parsaetak", LicensorURL)
	}
	if ProjectURL != "https://github.com/SHEYTANx13" {
		t.Fatalf("ProjectURL = %q, want https://github.com/SHEYTANx13", ProjectURL)
	}
	if SourceURL != ProjectURL {
		t.Fatalf("SourceURL = %q must be the SHEYTAN project home", SourceURL)
	}
	block := strings.Join(AttributionBlock(), "\n")
	for _, want := range []string{"https://linktr.ee/Parsaetak", "https://github.com/SHEYTANx13", "Parsaetak", "SHEYTAN"} {
		if !strings.Contains(block, want) {
			t.Fatalf("attribution block missing %q:\n%s", want, block)
		}
	}
	// Role honesty: licensor role for Parsaetak, product/brand role for
	// SHEYTAN; brand credit must not be misreported as legal ownership.
	if !strings.Contains(block, "Licensor / rights holder: Parsaetak") {
		t.Fatal("Parsaetak must be named as licensor/rights holder")
	}
	if !strings.Contains(block, "Product / brand / project home: SHEYTAN") {
		t.Fatal("SHEYTAN must be named as the product/brand/project identity")
	}
}
