package gogui

// view_test.go — the headless test-window contract: the tab strip (11
// destinations) renders through gui.NewTestWindow with clean
// diagnostics, every control is discoverable by ID, clickable through
// the Test* API, and joins the focus traversal. No display required.

import (
	"testing"

	"github.com/go-gui-org/go-gui/gui"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/design"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/presentation"
)

// newTabStripTestWindow renders the real tab strip in a headless test
// window and returns the window plus the shared tab state.
func newTabStripTestWindow(t *testing.T) (*gui.Window, *TabState, *[]presentation.Destination) {
	t.Helper()
	th := NewShellTheme(design.MustLoad())
	tabs := &TabState{Layout: presentation.DefaultLayout()}
	var clicked []presentation.Destination
	w := gui.NewTestWindow(t, gui.WindowCfg{
		Title: "tab strip contract test",
		State: tabs,
		OnInit: func(w *gui.Window) {
			w.SetView(func(w *gui.Window) gui.View {
				return tabStripView(tabs, th, func(d presentation.Destination) {
					tabs.SetActive(d)
					clicked = append(clicked, d)
				}, func(int) {})
			})
		},
	})
	w.TestRender(nil) // settle the first frame before any Test* action
	return w, tabs, &clicked
}

// TestTabStripControlDiscovery verifies all 11 destinations render
// exactly one discoverable control each, in the persisted order.
func TestTabStripControlDiscovery(t *testing.T) {
	w, tabs, _ := newTabStripTestWindow(t)

	catalog := tabs.OrderedCatalog()
	if len(catalog) != len(presentation.Destinations) {
		t.Fatalf("catalog size = %d, want %d", len(catalog), len(presentation.Destinations))
	}
	for _, info := range catalog {
		ids := w.ResolveID("tab-" + string(info.ID))
		if len(ids) != 1 {
			t.Errorf("destination %q: found %d controls with leaf %q, want exactly 1",
				info.ID, len(ids), "tab-"+string(info.ID))
		}
	}
}

// TestTabStripCleanDiagnostics verifies the real view renders without
// duplicate IDs or other debug findings.
func TestTabStripCleanDiagnostics(t *testing.T) {
	w, _, _ := newTabStripTestWindow(t)
	if dup := w.TestDuplicateIDs(); len(dup) > 0 {
		t.Errorf("duplicate IDs in the tab strip: %v", dup)
	}
	if found := w.TestFindings(gui.DebugAll); len(found) > 0 {
		t.Errorf("debug findings in the tab strip: %v", found)
	}
}

// TestTabStripClickActivatesDestination verifies a click through the
// Test* dispatch activates the destination (the same path a real
// pointer press takes).
func TestTabStripClickActivatesDestination(t *testing.T) {
	w, tabs, clicked := newTabStripTestWindow(t)

	id := w.ResolveID("tab-" + string(presentation.DestStudio))[0]
	if err := w.TestClick(id); err != nil {
		t.Fatalf("TestClick(%q): %v", id, err)
	}
	if tabs.Layout.ActiveTab != presentation.DestStudio {
		t.Errorf("active tab after click = %q, want studio", tabs.Layout.ActiveTab)
	}
	if len(*clicked) != 1 || (*clicked)[0] != presentation.DestStudio {
		t.Errorf("clicked destinations = %v, want exactly [studio]", *clicked)
	}
}

// TestTabStripMoveControlsReorder verifies the ‹/› reorder controls
// perform the persisted-order mutation through the same Test* dispatch
// a real pointer press takes (run 44648357437 item 4.3: the reorder is
// wired to a real UI gesture, not merely a command ID).
func TestTabStripMoveControlsReorder(t *testing.T) {
	th := NewShellTheme(design.MustLoad())
	tabs := &TabState{Layout: presentation.DefaultLayout()}
	tabs.SetActive(presentation.DestHome)

	w := gui.NewTestWindow(t, gui.WindowCfg{Title: "tab strip reorder", State: tabs})
	w.TestRender(func(w *gui.Window) gui.View {
		return tabStripView(tabs, th, func(presentation.Destination) {}, func(delta int) {
			tabs.MoveActive(delta)
		})
	})

	first, second := tabs.Layout.TabOrder[0], tabs.Layout.TabOrder[1]
	id := w.ResolveID("tab-move-right")[0]
	if err := w.TestClick(id); err != nil {
		t.Fatalf("TestClick(tab-move-right): %v", err)
	}
	if tabs.Layout.TabOrder[0] != second || tabs.Layout.TabOrder[1] != first {
		t.Errorf("right move did not swap positions 0/1: order now %v, %v", tabs.Layout.TabOrder[0], tabs.Layout.TabOrder[1])
	}
	id = w.ResolveID("tab-move-left")[0]
	if err := w.TestClick(id); err != nil {
		t.Fatalf("TestClick(tab-move-left): %v", err)
	}
	if tabs.Layout.TabOrder[0] != first || tabs.Layout.TabOrder[1] != second {
		t.Errorf("left move did not restore the canonical order")
	}
}

// TestTabStripFocusTraversal verifies the tab buttons are focusable
// and Tab walks between them (the accessibility focus contract).
func TestTabStripFocusTraversal(t *testing.T) {
	w, _, _ := newTabStripTestWindow(t)

	first := w.ResolveID("tab-" + string(presentation.DestHome))[0]
	if err := w.TestFocus(first); err != nil {
		t.Fatalf("TestFocus(%q): %v", first, err)
	}
	next, err := w.TestTab(gui.TabForward)
	if err != nil {
		t.Fatalf("TestTab(forward): %v", err)
	}
	if next == "" || next == first {
		t.Errorf("tab traversal moved focus to %q, want a different control", next)
	}
	_, err = w.TestTab(gui.TabBackward)
	if err != nil {
		t.Fatalf("TestTab(backward): %v", err)
	}
}

// TestTabStripActiveTabStyling verifies the active destination's
// control carries the accent fill while the rest carry the volcanic
// fill (the honest selected-state contract), walking the real layout
// tree the test window produced.
func TestTabStripActiveTabStyling(t *testing.T) {
	th := NewShellTheme(design.MustLoad())
	tabs := &TabState{Layout: presentation.DefaultLayout()}
	tabs.SetActive(presentation.DestTemplates)

	w := gui.NewTestWindow(t, gui.WindowCfg{Title: "tab strip styling", State: tabs})
	root := w.TestRender(func(w *gui.Window) gui.View {
		return tabStripView(tabs, th, func(presentation.Destination) {}, func(int) {})
	})

	for _, dest := range presentation.Destinations {
		ly := findTestLayoutByID(root, "tab-"+string(dest))
		if ly == nil || ly.Shape == nil {
			t.Fatalf("tab control %q not found in the rendered tree", dest)
		}
		want := th.VolcanicColor()
		if dest == presentation.DestTemplates {
			want = th.AccentColor()
		}
		if got := ly.Shape.Color; got != want {
			t.Errorf("tab %q fill = %v, want %v", dest, got, want)
		}
	}
}

// findTestLayoutByID walks a rendered layout tree for a shape with the
// given (leaf) ID.
func findTestLayoutByID(l *gui.Layout, id string) *gui.Layout {
	if l == nil {
		return nil
	}
	if l.Shape != nil && l.Shape.ID == id {
		return l
	}
	for i := range l.Children {
		if hit := findTestLayoutByID(&l.Children[i], id); hit != nil {
			return hit
		}
	}
	return nil
}
