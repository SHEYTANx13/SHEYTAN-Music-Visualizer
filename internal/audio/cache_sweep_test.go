package audio

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestCacheSweepBudget proves the disk-budget sweep removes orphaned
// families oldest-first, never touches the kept family, and stops at
// the budget.
func TestCacheSweepBudget(t *testing.T) {
	dir := t.TempDir()
	c, err := NewCache(dir)
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	// Three orphaned families with staggered mtimes + one kept family.
	fams := []string{"old", "mid", "new", "keep"}
	now := time.Now()
	for i, fp := range fams {
		for _, suffix := range []string{".f32", ".json"} {
			p := filepath.Join(dir, fp+suffix)
			if err := os.WriteFile(p, make([]byte, 1000), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			st := now.Add(-time.Duration(len(fams)-i) * time.Hour)
			if err := os.Chtimes(p, st, st); err != nil {
				t.Fatalf("chtimes: %v", err)
			}
		}
	}
	// Budget admits exactly one family (1000+1000 bytes each); the
	// kept family is 1 family's worth below... total = 8 files * 1000 =
	// 8000 bytes; budget 4000 -> must free 4000 (two oldest families).
	removed, freed, err := c.Sweep(map[string]struct{}{"keep": {}}, 4000)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if removed != 2 || freed != 4000 {
		t.Fatalf("removed=%d freed=%d, want 2 / 4000", removed, freed)
	}
	for _, suffix := range []string{".f32", ".json"} {
		if _, err := os.Stat(filepath.Join(dir, "keep"+suffix)); err != nil {
			t.Fatalf("kept family must survive: %v", err)
		}
	}
	for _, fp := range []string{"old", "mid"} {
		if _, err := os.Stat(filepath.Join(dir, fp+".f32")); !os.IsNotExist(err) {
			t.Fatalf("orphan %s should be gone (err=%v)", fp, err)
		}
	}
	// "new" survives (budget already met after two removals).
	if _, err := os.Stat(filepath.Join(dir, "new.f32")); err != nil {
		t.Fatalf("newest orphan must survive at budget: %v", err)
	}
}

// TestCacheSweepWithinBudget is a no-op when the cache is small.
func TestCacheSweepWithinBudget(t *testing.T) {
	dir := t.TempDir()
	c, err := NewCache(dir)
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.f32"), make([]byte, 10), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	removed, freed, err := c.Sweep(map[string]struct{}{}, 1<<30)
	if err != nil || removed != 0 || freed != 0 {
		t.Fatalf("within-budget sweep must be a no-op: removed=%d freed=%d err=%v", removed, freed, err)
	}
}

// TestCacheSweepKeepsActiveTrack never deletes the loaded track even
// under an impossible budget.
func TestCacheSweepKeepsActiveTrack(t *testing.T) {
	dir := t.TempDir()
	c, err := NewCache(dir)
	if err != nil {
		t.Fatalf("NewCache: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "active.f32"), make([]byte, 100), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stale.f32"), make([]byte, 100), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	removed, _, err := c.Sweep(map[string]struct{}{"active": {}}, 0)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed=%d, want 1 (stale only)", removed)
	}
	if _, err := os.Stat(filepath.Join(dir, "active.f32")); err != nil {
		t.Fatalf("active track must never be swept: %v", err)
	}
}
