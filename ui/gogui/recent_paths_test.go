package gogui

// recent_paths_test.go — run-45042834042 UX-readiness contract: the
// MRU import-path list (the documented manual path fallback) persists
// through the shared workspace-layout store and is re-seeded on the
// next session.
import (
	"path/filepath"
	"testing"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/presentation"
)

func TestRememberImportPathPersistsAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace-layout.json")

	// Session 1: remember two paths on a shell bound to a real store.
	sh1 := &shell{
		state: newUIState(),
		store: presentation.NewLayoutStore(path),
		tabs:  TabState{Layout: presentation.DefaultLayout()},
	}
	sh1.rememberImportPath("/music/one.wav")
	sh1.rememberImportPath("/music/two.wav")

	// The persisted layout now carries the MRU list (most recent first).
	tabs2, err := LoadTabs(presentation.NewLayoutStore(path))
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(tabs2.Layout.RecentPaths) != 2 {
		t.Fatalf("persisted list = %v, want 2 entries", tabs2.Layout.RecentPaths)
	}
	if tabs2.Layout.RecentPaths[0] != "/music/two.wav" || tabs2.Layout.RecentPaths[1] != "/music/one.wav" {
		t.Fatalf("persisted order wrong: %v", tabs2.Layout.RecentPaths)
	}

	// Session 2: the seed seam restores the list from the loaded layout.
	sh2 := &shell{state: newUIState(), tabs: tabs2}
	sh2.seedRecentPaths(tabs2)
	if len(sh2.recentPaths) != 2 || sh2.recentPaths[0] != "/music/two.wav" {
		t.Fatalf("seeded list = %v, want most-recent-first", sh2.recentPaths)
	}

	// A duplicate remember call is a no-op (no reordering, no rewrite).
	before := len(sh2.recentPaths)
	sh2.rememberImportPath("/music/two.wav")
	if len(sh2.recentPaths) != before || sh2.recentPaths[0] != "/music/two.wav" {
		t.Fatalf("duplicate remember mutated the list: %v", sh2.recentPaths)
	}
}

func TestRememberImportPathBounded(t *testing.T) {
	sh := &shell{
		state: newUIState(),
		store: presentation.NewLayoutStore(filepath.Join(t.TempDir(), "workspace-layout.json")),
		tabs:  TabState{Layout: presentation.DefaultLayout()},
	}
	for i := 0; i < presentation.RecentPathsMax+3; i++ {
		sh.rememberImportPath("/p/entry" + string(rune('a'+i)) + ".wav")
	}
	if len(sh.recentPaths) != presentation.RecentPathsMax {
		t.Fatalf("bound: len = %d, want %d", len(sh.recentPaths), presentation.RecentPathsMax)
	}
	if sh.recentPaths[0] != "/p/entry"+string(rune('a'+presentation.RecentPathsMax+2))+".wav" {
		t.Fatalf("most recent entry not first: %v", sh.recentPaths)
	}
}
