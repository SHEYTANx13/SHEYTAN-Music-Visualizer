package presentation

// recent_paths_test.go — run-45042834042 UX-readiness contract tests for
// the persisted MRU import-path list (additive v1 layout field).
import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSanitizeRecentPathsBoundsAndDedupes(t *testing.T) {
	in := []string{
		"/a/first.wav",
		"/a/first.wav",            // duplicate: dropped
		"",                        // empty: dropped
		"   ",                     // blank: dropped
		strings.Repeat("x", 4096), // over byte bound: dropped
		"/b/second.wav",
		"/c/third.wav",
	}
	got := SanitizeRecentPaths(in)
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3 (%v)", len(got), got)
	}
	if got[0] != "/a/first.wav" || got[1] != "/b/second.wav" || got[2] != "/c/third.wav" {
		t.Fatalf("order/content wrong: %v", got)
	}

	// The cap: 8 entries in, 5 out, most recent first.
	many := make([]string, 0, 8)
	for i := 0; i < 8; i++ {
		many = append(many, "/p/entry"+string(rune('a'+i))+".wav")
	}
	if got := SanitizeRecentPaths(many); len(got) != RecentPathsMax {
		t.Fatalf("cap: len = %d, want %d", len(got), RecentPathsMax)
	}
	if got := SanitizeRecentPaths(nil); got != nil {
		t.Fatalf("nil in -> %v, want nil", got)
	}
}

func TestLayoutStoreRecentPathsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace-layout.json")
	store := NewLayoutStore(path)

	lay, err := store.Load()
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	lay.RecentPaths = []string{"/music/one.wav", "/music/two.wav"}
	if err := store.Save(lay); err != nil {
		t.Fatalf("save: %v", err)
	}

	again, err := store.Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(again.RecentPaths) != 2 || again.RecentPaths[0] != "/music/one.wav" {
		t.Fatalf("recent paths lost on reload: %v", again.RecentPaths)
	}
}

func TestLayoutStoreHostileRecentPathsRecovered(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workspace-layout.json")
	store := NewLayoutStore(path)

	// Save a valid layout, then splice hostile (but JSON-valid) entries
	// into the recentPaths array through the raw file: Load must
	// sanitize them (drop empty/oversized), never fail.
	lay := DefaultLayout()
	lay.RecentPaths = []string{"/ok/first.wav"}
	if err := store.Save(lay); err != nil {
		t.Fatalf("save: %v", err)
	}
	rawBytes, err := osReadFileForTest(path)
	raw := string(rawBytes)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	longPath := "/x/very-long-" + strings.Repeat("p", 2048) + "-path"
	hostile := strings.Replace(raw,
		`"/ok/first.wav"`,
		`"", "   ", "`+longPath+`", "/x/kept.wav"`,
		1)
	if hostile == raw {
		t.Fatal("corruption splice did not change the file")
	}
	if err := osWriteFileForTest(path, []byte(hostile), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("load with hostile list must sanitize, not recover: %v", err)
	}
	if len(loaded.RecentPaths) != 1 || loaded.RecentPaths[0] != "/x/kept.wav" {
		t.Fatalf("hostile list not sanitized: %v", loaded.RecentPaths)
	}
}
