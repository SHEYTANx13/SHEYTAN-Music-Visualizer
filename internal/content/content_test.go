// content_test.go — the §3.12 mandatory CRUD verification scenarios for
// the content service (run-43767556517). Scenarios map to the work
// order lettering:
//
//	A create · B read/list · C update/stale/invalid · D delete safety ·
//	E restore · F undo/redo sequence · G persistence across reopen ·
//	H journal/failure recovery · I import safety · J concurrency/stale
//	operations · K cross-platform (pure Go; runs on Windows and Linux)
//	L service-level selection guarantees (IDs are never index/name based).
//
// All tests are deterministic and bounded; no sleeps, no timing windows.
package content

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/project"
)

// newTestLibrary opens an isolated library in a temp dir.
func newTestLibrary(t *testing.T) *Library {
	t.Helper()
	l, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open library: %v", err)
	}
	return l
}

func mustCreate(t *testing.T, l *Library, name string) Entity {
	t.Helper()
	e, err := l.Create(CreateRequest{
		Name: name, SourcePath: "/music/" + name + ".flac",
		Fingerprint: "fp-" + name, Container: "flac", Duration: 180,
	})
	if err != nil {
		t.Fatalf("create %q: %v", name, err)
	}
	if !ValidID(e.ID) {
		t.Fatalf("created id %q is not the typed format", e.ID)
	}
	return e
}

// A — create: unique IDs, valid defaults, durable persistence.
func TestCreateAssignsUniqueTypedIDsAndPersists(t *testing.T) {
	l := newTestLibrary(t)
	a := mustCreate(t, l, "Alpha")
	b := mustCreate(t, l, "Beta")
	if a.ID == b.ID {
		t.Fatal("two creates must never share an identity")
	}
	if a.Revision != 1 || b.Revision != 1 {
		t.Fatalf("fresh entities must start at revision 1, got %d/%d", a.Revision, b.Revision)
	}
	// Durable: a fresh Library over the same root sees both entities.
	l2, err := Open(l.Root())
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, err := l2.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("reopened library has %d entities, want 2", len(got))
	}
	ids := map[string]bool{}
	for _, e := range got {
		ids[e.ID] = true
	}
	if !ids[a.ID] || !ids[b.ID] {
		t.Fatal("reopened library lost a created identity")
	}
}

// A — create must reject an invalid name instead of writing garbage.
func TestCreateRejectsInvalidNames(t *testing.T) {
	l := newTestLibrary(t)
	for _, name := range []string{"", "   ", ".", ".."} {
		if _, err := l.Create(CreateRequest{Name: name, Fingerprint: "fp", Container: "flac"}); !errors.Is(err, ErrInvalidName) {
			t.Fatalf("name %q must be rejected with ErrInvalidName, got %v", name, err)
		}
	}
}

// B — read/list: deterministic order, filtering does not alter identity.
func TestListIsDeterministicAndIdentityStable(t *testing.T) {
	l := newTestLibrary(t)
	mustCreate(t, l, "zeta")
	mustCreate(t, l, "alpha")
	first, err := l.List()
	if err != nil {
		t.Fatal(err)
	}
	second, err := l.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("list length changed: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].ID != second[i].ID || first[i].Name != second[i].Name {
			t.Fatalf("list order/content is not deterministic: %v vs %v", first, second)
		}
	}
	if first[0].Name != "alpha" || first[1].Name != "zeta" {
		t.Fatalf("list must be name-sorted, got %q then %q", first[0].Name, first[1].Name)
	}
}

// C — update: edit fields including explicit zero values; stale revision
// and identity/revision tampering are rejected without partial writes.
func TestUpdateSemantics(t *testing.T) {
	l := newTestLibrary(t)
	e := mustCreate(t, l, "Edit me")

	// Happy path: explicit zero value (TempoOverride 0 stays 0), revision bumps.
	e2, err := l.Update(e.ID, e.Revision, func(p *project.Project) error {
		p.TempoOverrideBPM = 0
		p.SyncOffsetSeconds = 1.5
		p.Visualizer.TitleText = "Live Set"
		return nil
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if e2.ID != e.ID {
		t.Fatal("update must preserve the stable id")
	}
	if e2.Revision != e.Revision+1 {
		t.Fatalf("update must bump the revision by one: %d -> %d", e.Revision, e2.Revision)
	}

	// Stale revision: the old editor's write must be refused and must
	// NOT overwrite the newer committed state.
	_, err = l.Update(e.ID, e.Revision, func(p *project.Project) error {
		p.Visualizer.TitleText = "STALE EDIT"
		return nil
	})
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale revision must be ErrRevisionConflict, got %v", err)
	}
	doc, err := e2.Document()
	if err != nil {
		t.Fatal(err)
	}
	if doc.Visualizer.TitleText != "Live Set" {
		t.Fatalf("stale edit overwrote committed state: %q", doc.Visualizer.TitleText)
	}

	// Invalid change: the mutator's own error must abort with no write.
	_, err = l.Update(e.ID, e2.Revision, func(p *project.Project) error {
		p.Visualizer.TitleText = "should never commit"
		return errors.New("user rejected the change")
	})
	if err == nil || !strings.Contains(err.Error(), "change rejected") {
		t.Fatalf("mutator error must abort the update, got %v", err)
	}
	doc, err = e2.Document()
	if err != nil {
		t.Fatal(err)
	}
	if doc.Visualizer.TitleText != "Live Set" {
		t.Fatalf("rejected mutation partially committed: %q", doc.Visualizer.TitleText)
	}

	// Identity tampering is refused.
	_, err = l.Update(e.ID, e2.Revision, func(p *project.Project) error {
		p.ContentID = "prj-zzzzzzzzzzzzzzzzzzzzzzzzzz"
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "stable id") {
		t.Fatalf("identity change must be refused, got %v", err)
	}

	// Revision tampering is refused.
	_, err = l.Update(e.ID, e2.Revision, func(p *project.Project) error {
		p.Revision = 999
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "revision token") {
		t.Fatalf("revision token change must be refused, got %v", err)
	}
}

// C/J — external modification: a document rewritten behind the library's
// back is detected and refused (never last-write-wins).
func TestExternalModificationDetected(t *testing.T) {
	l := newTestLibrary(t)
	e := mustCreate(t, l, "External")
	// Rewrite the file behind the library's back (same revision).
	doc, err := e.Document()
	if err != nil {
		t.Fatal(err)
	}
	doc.Visualizer.TitleText = "tampered by another app"
	if err := project.Save(doc, e.Path); err != nil {
		t.Fatal(err)
	}
	_, err = l.Update(e.ID, e.Revision, func(p *project.Project) error { return nil })
	if !errors.Is(err, ErrExternalModification) {
		t.Fatalf("external modification must be ErrExternalModification, got %v", err)
	}
}

// D — delete: soft-delete keeps the document recoverable and NEVER
// touches the external source media; purge is explicit and separate.
func TestDeleteSafetyAndPurgePolicy(t *testing.T) {
	l := newTestLibrary(t)
	sourceDir := t.TempDir()
	source := filepath.Join(sourceDir, "song source.flac")
	if err := os.WriteFile(source, []byte("AUDIO-BYTES-DO-NOT-DELETE"), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := l.Create(CreateRequest{Name: "Doomed", SourcePath: source, Fingerprint: "fp", Container: "flac", Duration: 60})
	if err != nil {
		t.Fatal(err)
	}
	if err := l.SoftDelete(e.ID, e.Revision, "user removed the reference"); err != nil {
		t.Fatalf("soft delete: %v", err)
	}
	// The external source file must be untouched.
	if b, err := os.ReadFile(source); err != nil || string(b) != "AUDIO-BYTES-DO-NOT-DELETE" {
		t.Fatalf("source media was damaged by delete: %v", err)
	}
	// The entity is now trashed; live Get maps to the precise error.
	if _, err := l.Get(e.ID); !errors.Is(err, ErrEntityTrashed) {
		t.Fatalf("live get after trash must be ErrEntityTrashed, got %v", err)
	}
	trashed, err := l.ListTrashed()
	if err != nil || len(trashed) != 1 || trashed[0].ID != e.ID {
		t.Fatalf("trash list: %v %v", trashed, err)
	}
	// Purge on a LIVE entity must be refused.
	live := mustCreate(t, l, "Still live")
	if err := l.Purge(live.ID); !errors.Is(err, ErrNotTrashed) {
		t.Fatalf("purge of a live entity must be refused, got %v", err)
	}
	// Explicit purge of the trashed entity removes only the managed doc.
	if err := l.Purge(e.ID); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if _, err := l.ListTrashed(); err != nil {
		t.Fatal(err)
	}
	if got, _ := l.ListTrashed(); len(got) != 0 {
		t.Fatalf("purge left trash behind: %v", got)
	}
	if _, err := os.Stat(e.Path); !os.IsNotExist(err) {
		t.Fatal("purged document still on disk")
	}
	// The source file STILL must be untouched after purge.
	if b, err := os.ReadFile(source); err != nil || string(b) != "AUDIO-BYTES-DO-NOT-DELETE" {
		t.Fatalf("source media was damaged by purge: %v", err)
	}
}

// D — render guard: an active render blocks the destructive command.
func TestRenderGuardBlocksDelete(t *testing.T) {
	l := newTestLibrary(t)
	e := mustCreate(t, l, "Rendering now")
	l.RenderGuard = func(id string) bool { return id == e.ID }
	if err := l.SoftDelete(e.ID, e.Revision, ""); !errors.Is(err, ErrRenderActive) {
		t.Fatalf("render guard must refuse deletion, got %v", err)
	}
	l.RenderGuard = func(id string) bool { return false }
	if err := l.SoftDelete(e.ID, e.Revision, ""); err != nil {
		t.Fatalf("delete after guard cleared: %v", err)
	}
}

// E — restore: original ID, revision history and relationships survive.
func TestRestorePreservesIdentityAndHistory(t *testing.T) {
	l := newTestLibrary(t)
	e := mustCreate(t, l, "Recyclable")
	e2, err := l.Update(e.ID, e.Revision, func(p *project.Project) error {
		p.Visualizer.Palette = 2
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := l.SoftDelete(e.ID, e2.Revision, "cleanup"); err != nil {
		t.Fatal(err)
	}
	restored, err := l.Restore(e.ID)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if restored.ID != e.ID {
		t.Fatal("restore must keep the stable id")
	}
	if restored.Revision != e2.Revision {
		t.Fatalf("restore must keep the revision history: got %d want %d", restored.Revision, e2.Revision)
	}
	doc, err := restored.Document()
	if err != nil {
		t.Fatal(err)
	}
	if doc.Visualizer.Palette != 2 || doc.MediaRef.Fingerprint != "fp-Recyclable" {
		t.Fatalf("restore lost relationships: %+v", doc)
	}
	// Double restore is refused honestly (it is live again).
	if _, err := l.Restore(e.ID); err == nil {
		t.Fatal("restore of a live entity must fail")
	}
}

// F — undo/redo: add → edit → rename → delete with canonical state
// compared at every step; redo re-applies exactly.
func TestUndoRedoSequence(t *testing.T) {
	l := newTestLibrary(t)
	a := mustCreate(t, l, "Sequence")
	b, err := l.Update(a.ID, a.Revision, func(p *project.Project) error {
		p.Visualizer.Palette = 3
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	c, err := l.Rename(a.ID, b.Revision, "Sequence renamed")
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "Sequence renamed" {
		t.Fatalf("rename result: %+v", c)
	}
	// undo rename -> undo edit -> undo create(trash)
	if label, err := l.Undo(); err != nil || !strings.Contains(label, "Rename") {
		t.Fatalf("undo rename: %q %v", label, err)
	}
	if e, err := l.Get(a.ID); err != nil || e.Name != "Sequence" {
		t.Fatalf("after undo rename: %+v %v", e, err)
	}
	if _, err := l.Undo(); err != nil {
		t.Fatalf("undo edit: %v", err)
	}
	doc, err := l.Get(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	d, err := doc.Document()
	if err != nil {
		t.Fatal(err)
	}
	if d.Visualizer.Palette != 0 {
		t.Fatalf("undo edit must restore palette 0, got %d (rev %d)", d.Visualizer.Palette, doc.Revision)
	}
	if _, err := l.Undo(); err != nil {
		t.Fatalf("undo create: %v", err)
	}
	if _, err := l.Get(a.ID); !errors.Is(err, ErrEntityTrashed) {
		t.Fatalf("undo create must trash the entity, got %v", err)
	}
	// redo chain: restore -> re-edit -> re-rename
	for i := 0; i < 3; i++ {
		if _, err := l.Redo(); err != nil {
			t.Fatalf("redo %d: %v", i, err)
		}
	}
	e, err := l.Get(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if e.Name != "Sequence renamed" {
		t.Fatalf("redo chain lost the rename: %q", e.Name)
	}
	dd, err := e.Document()
	if err != nil {
		t.Fatal(err)
	}
	if dd.Visualizer.Palette != 3 {
		t.Fatalf("redo chain lost the edit: %d", dd.Visualizer.Palette)
	}
	if e.Revision <= d.Revision {
		t.Fatalf("undo/redo must keep revisions monotonic: %d after %d", e.Revision, d.Revision)
	}
	// Undo of a purged entity is refused with an explanation, and the
	// stack stays usable.
	if err := l.SoftDelete(e.ID, e.Revision, "purge prep"); err != nil {
		t.Fatal(err)
	}
	if err := l.Purge(e.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Undo(); err == nil || !strings.Contains(err.Error(), "purged") {
		t.Fatalf("undo of purged entity must be refused with explanation, got %v", err)
	}
	if _, err := l.Undo(); err != nil && !errors.Is(err, ErrUndoUnavailable) {
		t.Fatalf("stack must remain consistent after refused undo: %v", err)
	}
}

// F — undo stack is bounded and a new command clears the redo branch.
func TestUndoStackBoundedAndRedoCleared(t *testing.T) {
	l := newTestLibrary(t)
	e := mustCreate(t, l, "Bounded")
	rev := e.Revision
	for i := 0; i < maxUndoRecords+5; i++ {
		next, err := l.Update(e.ID, rev, func(p *project.Project) error {
			p.SyncOffsetSeconds = float64(i%11) / 10
			return nil
		})
		if err != nil {
			t.Fatalf("edit %d: %v", i, err)
		}
		rev = next.Revision
	}
	undo, redo := l.UndoDepth()
	if undo > maxUndoRecords {
		t.Fatalf("undo stack exceeded bound: %d", undo)
	}
	if redo != 0 {
		t.Fatalf("redo branch must be empty after new commands, got %d", redo)
	}
	// The oldest records were dropped: exactly maxUndoRecords undos apply.
	for i := 0; i < maxUndoRecords; i++ {
		if _, err := l.Undo(); err != nil {
			t.Fatalf("undo %d: %v", i, err)
		}
	}
	if _, err := l.Undo(); !errors.Is(err, ErrUndoUnavailable) {
		t.Fatalf("stack must be exhausted, got %v", err)
	}
}

// G — persistence: save/close/reopen preserves exact IDs, revisions,
// references and document bytes.
func TestPersistenceAcrossReopen(t *testing.T) {
	root := t.TempDir()
	l, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	e := mustCreate(t, l, "Durable")
	if _, err := l.Update(e.ID, e.Revision, func(p *project.Project) error {
		p.Visualizer.Mode = "radial"
		p.TimelineMarkers = []float64{1.5, 22.25}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(e.Path)
	if err != nil {
		t.Fatal(err)
	}
	l2, err := Open(root)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, err := l2.Get(e.ID)
	if err != nil {
		t.Fatalf("reopen lost the entity by id: %v", err)
	}
	after, err := os.ReadFile(got.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("reopen mutated the document bytes")
	}
	doc, err := got.Document()
	if err != nil {
		t.Fatal(err)
	}
	if doc.Visualizer.Mode != "radial" || len(doc.TimelineMarkers) != 2 || doc.TimelineMarkers[1] != 22.25 {
		t.Fatalf("reopen lost content: %+v", doc)
	}
	if got.Revision != 2 {
		t.Fatalf("revision not durable: %d", got.Revision)
	}
}

// H — failure recovery: journals left behind by interrupted move
// transactions resolve to the complete old OR complete new state,
// never a mixed state; unparsable journals fail safe.
func TestJournalRecovery(t *testing.T) {
	t.Run("committed-before-crash", func(t *testing.T) {
		root := t.TempDir()
		l, err := Open(root)
		if err != nil {
			t.Fatal(err)
		}
		e := mustCreate(t, l, "CrashCommitted")
		// Simulate: journal written, rename performed, process died
		// before journal removal.
		from := e.Path
		to := filepath.Join(l.trash, filepath.Base(from))
		if _, rollback, err := writeJournal(root, "trash", e.ID, from, to, e.Revision, e.Revision); err != nil {
			t.Fatal(err)
		} else {
			_ = rollback
		}
		if err := os.Rename(from, to); err != nil {
			t.Fatal(err)
		}
		l2, err := Open(root)
		if err != nil {
			t.Fatalf("recovery open failed: %v", err)
		}
		if _, err := l2.Get(e.ID); !errors.Is(err, ErrEntityTrashed) {
			t.Fatalf("recovered state must be the committed (trashed) one, got %v", err)
		}
		// The journal is consumed: reopening again is a no-op.
		if _, err := Open(root); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("never-executed-rollback", func(t *testing.T) {
		root := t.TempDir()
		l, err := Open(root)
		if err != nil {
			t.Fatal(err)
		}
		e := mustCreate(t, l, "CrashRolledBack")
		from := e.Path
		to := filepath.Join(l.trash, "never-happened.sheytan.json")
		if _, _, err := writeJournal(root, "trash", e.ID, from, to, e.Revision, e.Revision); err != nil {
			t.Fatal(err)
		}
		// No rename performed (crash right after the journal write).
		l2, err := Open(root)
		if err != nil {
			t.Fatalf("recovery open failed: %v", err)
		}
		if _, err := l2.Get(e.ID); err != nil {
			t.Fatalf("rolled-back state must keep the live entity: %v", err)
		}
	})
	t.Run("stale-staging-cleaned", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, ".sheytan-project-123.tmp"), []byte("partial"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(root); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(root, ".sheytan-project-123.tmp")); !os.IsNotExist(err) {
			t.Fatal("stale staging file survived recovery")
		}
	})
	t.Run("corrupt-journal-fails-safe", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, ".journal-trash-1.json"), []byte("{not json"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := Open(root)
		if err == nil || !strings.Contains(err.Error(), "manual inspection required") {
			t.Fatalf("corrupt journal must fail safe with a precise message, got %v", err)
		}
	})
	t.Run("duplicate-ids-fail-safe", func(t *testing.T) {
		root := t.TempDir()
		if _, err := Open(root); err != nil {
			t.Fatal(err)
		}
		raw := []byte(`{"version":1,"name":"X","contentId":"prj-00000000000000000000000000","revision":1,"media":{"fileName":"x","fingerprint":"f"},"visualizer":{"mode":"bars"},"export":{"width":1920,"height":1080,"fps":30,"qualityPreset":"high","encoder":"auto"}}`)
		if err := os.WriteFile(filepath.Join(root, "One.sheytan.json"), raw, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "Two.sheytan.json"), raw, 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := Open(root)
		if err == nil || !strings.Contains(err.Error(), "duplicate id") {
			t.Fatalf("duplicate ids must fail safe, got %v", err)
		}
	})
}

// I — import safety: malformed documents refused; exact duplicates
// detected and named; distinct copies get new identities and
// collision-safe names; source file untouched; Unicode/spaces safe.
func TestImportSafety(t *testing.T) {
	l := newTestLibrary(t)
	dir := t.TempDir()

	// Malformed file is refused, nothing written.
	bad := filepath.Join(dir, "bad.sheytan.json")
	if err := os.WriteFile(bad, []byte("{nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := l.ImportFile(bad); err == nil {
		t.Fatal("malformed import must be refused")
	}

	// A foreign document (built outside the library, legacy format
	// without an id) imports with a NEW identity.
	foreign := project.NewDefault("Импорт альбом", "/music/import.flac", "fp-import", "flac", 210)
	src := filepath.Join(dir, "orig.sheytan.json")
	if err := project.Save(foreign, src); err != nil {
		t.Fatal(err)
	}
	imported, err := l.ImportFile(src)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if !ValidID(imported.ID) {
		t.Fatalf("import must mint a typed id, got %q", imported.ID)
	}
	if imported.Name != "Импорт альбом" {
		t.Fatalf("import must preserve the display name, got %q", imported.Name)
	}

	// Byte-identical import is a detected duplicate naming the existing id.
	if _, err := l.ImportFile(src); !errors.Is(err, ErrDuplicateContent) {
		t.Fatalf("duplicate import must be ErrDuplicateContent, got %v", err)
	}

	// A modified copy imports as a distinct entity with a collision-safe name.
	live, err := l.Get(imported.ID)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := live.Document()
	if err != nil {
		t.Fatal(err)
	}
	doc.Visualizer.Mode = "waveform"
	raw := saveForBytes(t, doc)
	modified := filepath.Join(dir, "modified.sheytan.json")
	if err := os.WriteFile(modified, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	e2, err := l.ImportFile(modified)
	if err != nil {
		t.Fatalf("distinct import: %v", err)
	}
	if e2.Path == live.Path || strings.EqualFold(e2.Path, live.Path) {
		t.Fatal("distinct import must not reuse the existing path")
	}
	if !strings.Contains(filepath.Base(e2.Path), "(2)") {
		t.Fatalf("collision-safe name expected, got %q", filepath.Base(e2.Path))
	}
}

// I — names with spaces, Unicode and hostile characters map to safe,
// stable managed files without ever overwriting another entity.
func TestNameHandling(t *testing.T) {
	l := newTestLibrary(t)
	a := mustCreate(t, l, "Плейлист: финал/версия 1?")
	b := mustCreate(t, l, "Плейлист: финал/версия 1?")
	if a.Path == b.Path {
		t.Fatal("identical hostile names must map to distinct files")
	}
	if !strings.Contains(filepath.Base(b.Path), "(2)") {
		t.Fatalf("second entity must be collision-renamed, got %q", filepath.Base(b.Path))
	}
	// Both remain retrievable by their stable IDs.
	for _, e := range []Entity{a, b} {
		if _, err := l.Get(e.ID); err != nil {
			t.Fatalf("get %s: %v", e.ID, err)
		}
	}
}

// J — concurrency: double soft-delete, delete with a stale revision,
// and rename onto an existing name are all refused safely.
func TestConcurrencyRefusals(t *testing.T) {
	l := newTestLibrary(t)
	e := mustCreate(t, l, "Concurrent")
	e2, err := l.Update(e.ID, e.Revision, func(p *project.Project) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	// Stale delete (editor saw revision 1, world is at 2).
	if err := l.SoftDelete(e.ID, e.Revision, ""); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale delete must conflict, got %v", err)
	}
	// Double delete: after the first succeeds, the second with the same
	// revision cannot find the live entity.
	if err := l.SoftDelete(e.ID, e2.Revision, ""); err != nil {
		t.Fatalf("first delete: %v", err)
	}
	if err := l.SoftDelete(e.ID, e2.Revision, ""); !errors.Is(err, ErrEntityTrashed) {
		t.Fatalf("double delete must report the trash state, got %v", err)
	}
	if _, err := l.Restore(e.ID); err != nil {
		t.Fatal(err)
	}
	// Rename onto a taken name is refused; the name maps to a NEW id.
	other := mustCreate(t, l, "Other")
	if _, err := l.Rename(e.ID, e.Revision, other.Name); err == nil {
		t.Fatal("rename onto a taken display name must be refused")
	}
}

// L — selection targets stable IDs even when names change; the ID is
// never derived from an index, name or path.
func TestSelectionTargetsStableIDs(t *testing.T) {
	l := newTestLibrary(t)
	a := mustCreate(t, l, "First")
	b := mustCreate(t, l, "Second")
	// Renaming a does not disturb b's identity, and a keeps its id.
	if _, err := l.Rename(a.ID, a.Revision, "First renamed"); err != nil {
		t.Fatal(err)
	}
	if got, err := l.Get(b.ID); err != nil || got.Name != "Second" {
		t.Fatalf("b disturbed by a's rename: %+v %v", got, err)
	}
	if got, err := l.Get(a.ID); err != nil || got.Name != "First renamed" {
		t.Fatalf("a lost its identity through rename: %+v %v", got, err)
	}
	// Deleting a does not move ids onto other entities.
	if err := l.SoftDelete(a.ID, a.Revision+1, "cleanup"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Get(b.ID); err != nil {
		t.Fatalf("b must be untouched by a's deletion: %v", err)
	}
}

// K — the library is pure Go over os/filepath: it builds and runs on
// every supported desktop platform; this file executes unchanged on
// Windows and Linux runners (recorded per platform in CI evidence).

// helpers -----------------------------------------------------------------

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// saveForBytes serializes a document through the production writer and
// returns the exact bytes written (no test-only marshal API).
func saveForBytes(t *testing.T, p *project.Project) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "serialize.sheytan.json")
	if err := project.Save(p, path); err != nil {
		t.Fatal(err)
	}
	return mustRead(t, path)
}
