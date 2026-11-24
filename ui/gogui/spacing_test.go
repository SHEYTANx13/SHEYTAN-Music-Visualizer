package gogui

import (
	"testing"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/design"
)

// TestSpacingProfileForWidth proves the responsive space-management
// contract: touch classes get roomier vertical rhythm, desktop/wide
// classes tighten vertically for professional density, and the stored
// profile follows the shell width.
func TestSpacingProfileForWidth(t *testing.T) {
	th := NewShellTheme(design.MustLoad())
	phone := th.SpacingForWidth(360)
	tablet := th.SpacingForWidth(600)
	desktop := th.SpacingForWidth(1280)
	wide := th.SpacingForWidth(1920)

	// Touch classes keep roomier bars and rows than desktop.
	if phone.BarPadTop < desktop.BarPadTop || phone.BarPadBottom < desktop.BarPadBottom {
		t.Fatalf("phone bar padding must be >= desktop (touch-friendly): %+v vs %+v", phone, desktop)
	}
	if phone.RowPadV < desktop.RowPadV {
		t.Fatalf("phone row padding must be >= desktop: %+v vs %+v", phone, desktop)
	}
	if tablet.RowPadV < desktop.RowPadV {
		t.Fatalf("tablet row padding must be >= desktop: %+v vs %+v", tablet, desktop)
	}
	// Desktop/wide keep the compact professional density.
	if desktop.BarPadTop > 4 || wide.BarPadTop > 4 {
		t.Fatalf("desktop/wide bar padding must stay compact: %+v %+v", desktop, wide)
	}
	// Horizontal gutters never collapse.
	for name, sp := range map[string]SpacingProfile{
		"phone": phone, "tablet": tablet, "desktop": desktop, "wide": wide,
	} {
		if sp.BarPadH < 8 || sp.PanelPadH < 10 {
			t.Fatalf("%s gutters too tight: %+v", name, sp)
		}
	}
	// Stored profile follows the shell width every frame.
	th.SetSpacingForWidth(360)
	if got := th.Spacing(); got != phone {
		t.Fatalf("stored profile after phone width = %+v, want %+v", got, phone)
	}
	th.SetSpacingForWidth(1280)
	if got := th.Spacing(); got != desktop {
		t.Fatalf("stored profile after desktop width = %+v, want %+v", got, desktop)
	}
}

// TestSpacingDefaultsArePhoneSafe: before the first frame reports a
// width, the safest (roomiest) profile is active.
func TestSpacingDefaultsArePhoneSafe(t *testing.T) {
	th := NewShellTheme(design.MustLoad())
	sp := th.Spacing()
	if sp.BarPadTop < 6 || sp.RowPadV < 5 {
		t.Fatalf("default spacing must be the phone-safe profile: %+v", sp)
	}
}

// TestScrollbarSizeForWidth (run 45297707372 scrolling repair): the
// scroll bar is fatter on touch classes than on desktop/wide, always
// visible (>= 10dp), and the resolved config carries the accent thumb
// with a 40dp minimum thumb.
func TestScrollbarSizeForWidth(t *testing.T) {
	th := NewShellTheme(design.MustLoad())
	phone := th.SpacingForWidth(360)
	desktop := th.SpacingForWidth(1280)
	if phone.ScrollbarSize < desktop.ScrollbarSize {
		t.Fatalf("phone scroll bar must be >= desktop (touch-friendly): %+v vs %+v", phone, desktop)
	}
	for name, sp := range map[string]SpacingProfile{
		"phone": phone, "tablet": th.SpacingForWidth(600),
		"desktop": desktop, "wide": th.SpacingForWidth(1920),
	} {
		if sp.ScrollbarSize < 10 {
			t.Fatalf("%s scroll bar too thin to be visible/grabbable: %+v", name, sp)
		}
	}
	cfg := th.ScrollbarCfgForWidth(360)
	if cfg.Size != phone.ScrollbarSize {
		t.Fatalf("scrollbar cfg size %v != profile %v", cfg.Size, phone.ScrollbarSize)
	}
	if cfg.MinThumbSize < 40 {
		t.Fatalf("minimum thumb %v must stay draggable (>= 40)", cfg.MinThumbSize)
	}
	// gui.Color is a struct value; the thumb must be the visible accent
	// ink (explicitly set, opaque, non-black).
	c := cfg.ColorThumb
	if c.A == 0 {
		t.Fatalf("thumb color must be opaque enough to be visible (alpha %d)", c.A)
	}
	if c.R == 0 && c.G == 0 && c.B == 0 {
		t.Fatalf("thumb color must be the visible accent ink, not black")
	}
}
