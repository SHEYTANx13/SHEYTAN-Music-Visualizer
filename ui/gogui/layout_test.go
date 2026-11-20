package gogui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/presentation"
)

// TestBreakpointFor verifies the responsive classification against the
// token boundaries (phone <600, tablet 600-959, desktop 960-1679,
// wide >=1680).
func TestBreakpointFor(t *testing.T) {
	doc := mustTestDoc(t)
	if doc.Breakpoints.Tablet.MinDp != 600 || doc.Breakpoints.Desktop.MinDp != 960 || doc.Breakpoints.Wide.MinDp != 1680 {
		t.Fatalf("token breakpoints drifted: %+v", doc.Breakpoints)
	}
	cases := []struct {
		width float32
		want  Breakpoint
	}{
		{0, BreakpointPhone},
		{320, BreakpointPhone},
		{599, BreakpointPhone},
		{600, BreakpointTablet},
		{768, BreakpointTablet},
		{959, BreakpointTablet},
		{960, BreakpointDesktop},
		{1280, BreakpointDesktop},
		{1679, BreakpointDesktop},
		{1680, BreakpointWide},
		{1920, BreakpointWide},
	}
	for _, c := range cases {
		if got := breakpointFor(c.width); got != c.want {
			t.Errorf("breakpointFor(%v) = %q, want %q", c.width, got, c.want)
		}
	}
}

// TestLayoutModeFor verifies the stacked/split switch (phone/tablet
// stacked, desktop/wide split) exactly as the brief mandates.
func TestLayoutModeFor(t *testing.T) {
	cases := []struct {
		width float32
		mode  LayoutMode
		split bool
	}{
		{400, ModeStacked, false},
		{700, ModeStackedWide, false},
		{1280, ModeSplit, true},
		{1920, ModeSplitCapped, true},
	}
	for _, c := range cases {
		if got := layoutModeFor(c.width); got != c.mode {
			t.Errorf("layoutModeFor(%v) = %q, want %q", c.width, got, c.mode)
		}
		if got := isSplitFor(c.width); got != c.split {
			t.Errorf("isSplitFor(%v) = %v, want %v", c.width, got, c.split)
		}
	}
}

// TestTabStateRoundTrip verifies the persisted workspace layout
// round-trips through the shared LayoutStore (default load, user order
// save + reload, active tab persistence) via t.TempDir().
func TestTabStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace-layout.json")
	store := presentation.NewLayoutStore(path)

	// First run: default layout, no error.
	tabs, err := LoadTabs(store)
	if err != nil {
		t.Fatalf("LoadTabs: %v", err)
	}
	if tabs.Recovered {
		t.Errorf("first run must not report recovery")
	}
	if tabs.Layout.ActiveTab != presentation.DestHome {
		t.Errorf("default active tab = %q, want home", tabs.Layout.ActiveTab)
	}

	// User reorders tabs and switches the active destination.
	reordered := []presentation.Destination{
		presentation.DestStudio, presentation.DestHome, presentation.DestExport,
		presentation.DestTemplates, presentation.DestLibrary,
		presentation.DestWorlds, presentation.DestRequirements,
		presentation.DestDownloads, presentation.DestSystemFiles,
		presentation.DestSettings, presentation.DestDiagnostics,
	}
	tabs.Layout.TabOrder = reordered
	tabs.SetActive(presentation.DestExport)
	if err := store.Save(tabs.Layout); err != nil {
		t.Fatalf("Save: %v", err)
	}

	tabs2, err := LoadTabs(store)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if tabs2.Recovered {
		t.Errorf("valid document must not report recovery")
	}
	if len(tabs2.Layout.TabOrder) != len(reordered) {
		t.Fatalf("round trip lost entries: %d != %d", len(tabs2.Layout.TabOrder), len(reordered))
	}
	for i, d := range reordered {
		if tabs2.Layout.TabOrder[i] != d {
			t.Errorf("round trip order[%d] = %q, want %q", i, tabs2.Layout.TabOrder[i], d)
		}
	}
	if tabs2.Layout.ActiveTab != presentation.DestExport {
		t.Errorf("round trip active tab = %q, want export", tabs2.Layout.ActiveTab)
	}
	if idx := tabs2.ActiveIndex(); idx != 2 {
		t.Errorf("ActiveIndex for export in reordered list = %d, want 2", idx)
	}
}

// TestTabStateCorruptRecovery verifies the corrupt-document recovery
// path reports honestly and restores the canonical default.
func TestTabStateCorruptRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace-layout.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	tabs, _ := LoadTabs(presentation.NewLayoutStore(path))
	if !tabs.Recovered {
		t.Errorf("corrupt document must report recovery")
	}
	if len(tabs.Layout.TabOrder) != len(presentation.Destinations) {
		t.Errorf("recovered layout must be the canonical default")
	}
	if tabs.Layout.ActiveTab != presentation.DestHome {
		t.Errorf("recovered active tab = %q, want home", tabs.Layout.ActiveTab)
	}
}

// TestTabForShortcut verifies the navigate mapping (mod+1..9 → tabs
// 1..9, mod+0 → tab 10; tab 11 has no shortcut).
func TestTabForShortcut(t *testing.T) {
	order := presentation.Destinations
	if d, ok := TabForShortcut(order, 1); !ok || d != presentation.DestHome {
		t.Errorf("tab 1 = %q (%v), want home", d, ok)
	}
	if d, ok := TabForShortcut(order, 10); !ok || d != presentation.DestSettings {
		t.Errorf("tab 10 = %q (%v), want settings", d, ok)
	}
	if _, ok := TabForShortcut(order, 11); ok {
		t.Errorf("tab 11 must have no shortcut (canonical table: mod+1..9,0)")
	}
	if _, ok := TabForShortcut(order, 0); ok {
		t.Errorf("index 0 must be out of range")
	}
}

// TestOrderedCatalog verifies the strip follows the persisted order and
// the catalog metadata stays intact.
func TestOrderedCatalog(t *testing.T) {
	tabs := TabState{Layout: presentation.DefaultLayout()}
	catalog := tabs.OrderedCatalog()
	if len(catalog) != len(presentation.Destinations) {
		t.Fatalf("ordered catalog size = %d, want %d", len(catalog), len(presentation.Destinations))
	}
	for i, info := range catalog {
		if info.ID != presentation.Destinations[i] {
			t.Errorf("catalog[%d] = %q, want %q", i, info.ID, presentation.Destinations[i])
		}
	}
	// Unavailable destinations carry honest empty states.
	for _, info := range catalog {
		if !info.Available && info.EmptyState == "" {
			t.Errorf("destination %q is unavailable but has no empty state", info.ID)
		}
	}
}
