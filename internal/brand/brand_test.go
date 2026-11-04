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
