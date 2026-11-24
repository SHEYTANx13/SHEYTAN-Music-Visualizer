package worldassets

import "testing"

// TestCatalogInvariants proves every catalog entry is valid, unique,
// correctly namespaced and non-empty geometry.
func TestCatalogInvariants(t *testing.T) {
	cat := Catalog()
	if len(cat) < 13 {
		t.Fatalf("catalog unexpectedly small: %d assets", len(cat))
	}
	seen := map[string]bool{}
	for _, a := range cat {
		if seen[a.ID] {
			t.Fatalf("duplicate asset id %q", a.ID)
		}
		seen[a.ID] = true
		if err := ValidateAsset(a); err != nil {
			t.Fatalf("asset %s invalid: %v", a.ID, err)
		}
		switch a.Family {
		case FamilySigil:
			if len(a.Segments) == 0 {
				t.Fatalf("sigil %s has no segments", a.ID)
			}
		case FamilyTree:
			if len(a.Segments) == 0 || a.Height <= 0 {
				t.Fatalf("tree %s lacks skeleton/height", a.ID)
			}
		case FamilyFloor:
			if len(a.Outline) < 3 || a.Pattern == "" {
				t.Fatalf("floor %s lacks outline/pattern", a.ID)
			}
		}
	}
}

// TestFamiliesCovered checks the three requested families exist with
// the documented minimum set (5 sigils, 3 trees, 5 floors).
func TestFamiliesCovered(t *testing.T) {
	if got := len(IDsForFamily(FamilySigil)); got != 5 {
		t.Fatalf("sigil family: got %d, want 5", got)
	}
	if got := len(IDsForFamily(FamilyTree)); got != 3 {
		t.Fatalf("tree family: got %d, want 3", got)
	}
	if got := len(IDsForFamily(FamilyFloor)); got != 5 {
		t.Fatalf("floor family: got %d, want 5", got)
	}
	if _, ok := Get("sigil/inverted-pentagram"); !ok {
		t.Fatalf("anchor sigil missing")
	}
	if _, ok := Get("floor/mirror-water"); !ok {
		t.Fatalf("anchor floor missing")
	}
}

// TestDeterministicGeometry: the same asset is identical across two
// calls (no hidden rng or time source).
func TestDeterministicGeometry(t *testing.T) {
	a1, _ := Get("sigil/inverted-pentagram")
	a2, _ := Get("sigil/inverted-pentagram")
	if len(a1.Segments) != len(a2.Segments) {
		t.Fatalf("segment count differs between calls")
	}
	for i := range a1.Segments {
		if a1.Segments[i] != a2.Segments[i] {
			t.Fatalf("segment %d differs between calls", i)
		}
	}
}

// TestValidateAssetRejects proves the validator catches broken assets.
func TestValidateAssetRejects(t *testing.T) {
	cases := []Asset{
		{ID: "", Family: FamilySigil},
		{ID: "bogus", Family: FamilySigil, Segments: [][2][3]float64{{{0, 0, 0}, {0, 0, 0}}}},
		{ID: "sigil/x", Family: "weird"},
		{ID: "sigil/x", Family: FamilySigil, Segments: [][2][3]float64{{{2, 0, 0}, {0, 0, 0}}}},
		{ID: "floor/x", Family: FamilyFloor, Outline: [][2]float64{{-0.5, -0.5}, {0.5, -0.5}, {0.5, 0.5}}, Pattern: "lava"},
	}
	for i, c := range cases {
		if err := ValidateAsset(c); err == nil {
			t.Fatalf("case %d: expected validation error for %+v", i, c)
		}
	}
}
