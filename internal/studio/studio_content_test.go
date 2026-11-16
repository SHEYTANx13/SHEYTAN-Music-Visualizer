// studio_content_test.go — the studio ↔ content-service integration
// contract (run-43767556517 §3.12 wiring). A library-rooted SaveProject
// goes through the validated content boundary (typed ID, revision
// conflict detection); a save outside the library root keeps the plain
// atomic-save behavior with no managed identity.
package studio

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/content"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/project"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/visualizer"
)

func newManagedStudio(t *testing.T) (*Studio, *content.Library) {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "track.wav")
	writeTestWav(t, src, 3.0)

	s, err := New()
	if err != nil {
		t.Fatalf("studio: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.ImportFile(src); err != nil {
		t.Fatalf("import: %v", err)
	}
	waitFor(t, s, 60*time.Second, func() bool { return s.Track() != nil }, "import")
	waitFor(t, s, 60*time.Second, func() bool { return s.Analysis() != nil }, "analysis")

	lib, err := content.Open(filepath.Join(dir, "projects"))
	if err != nil {
		t.Fatalf("library: %v", err)
	}
	s.AttachContentLibrary(lib)
	return s, lib
}

func TestStudioSaveRoutesThroughContentService(t *testing.T) {
	s, lib := newManagedStudio(t)

	// First save into the library root creates a managed entity with a
	// stable typed ID.
	managed := filepath.Join(lib.Root(), "Live Set.sheytan.json")
	if err := s.SaveProject(managed); err != nil {
		t.Fatalf("managed save: %v", err)
	}
	live, err := lib.List()
	if err != nil || len(live) != 1 {
		t.Fatalf("library listing after save: %v (%d entities)", err, len(live))
	}
	e := live[0]
	if !content.ValidID(e.ID) {
		t.Fatalf("managed entity id %q is not the typed format", e.ID)
	}
	if e.Revision != 1 {
		t.Fatalf("first managed save must be revision 1, got %d", e.Revision)
	}
	doc, err := e.Document()
	if err != nil {
		t.Fatal(err)
	}
	if doc.MediaRef.Fingerprint == "" || doc.Visualizer.Mode == "" {
		t.Fatalf("managed document lost studio state: %+v", doc)
	}

	// A second save updates the SAME entity under the revision
	// precondition (no duplicate, revision bump).
	s.SetConfig(visualizer.Config{Mode: visualizer.ModeRadial})
	if err := s.SaveProject(managed); err != nil {
		t.Fatalf("second managed save: %v", err)
	}
	live, err = lib.List()
	if err != nil || len(live) != 1 {
		t.Fatalf("second save duplicated the entity: %d entities", len(live))
	}
	if live[0].Revision != 2 {
		t.Fatalf("second save must bump the revision, got %d", live[0].Revision)
	}
	doc, err = live[0].Document()
	if err != nil {
		t.Fatal(err)
	}
	if doc.Visualizer.Mode != "radial" {
		t.Fatalf("second save lost the new state: %q", doc.Visualizer.Mode)
	}

	// An external overwrite between saves is refused with the typed
	// conflict (never last-write-wins), and the studio keeps its draft.
	doc.Visualizer.TitleText = "tampered externally"
	if err := project.Save(doc, live[0].Path); err != nil {
		t.Fatal(err)
	}
	s.SetConfig(visualizer.Config{Mode: visualizer.ModeBars})
	err = s.SaveProject(managed)
	if err == nil || !errors.Is(err, content.ErrExternalModification) {
		t.Fatalf("external overwrite must surface ErrExternalModification, got %v", err)
	}
}

func TestStudioSaveOutsideLibraryStaysLegacy(t *testing.T) {
	s, lib := newManagedStudio(t)
	outside := filepath.Join(t.TempDir(), "unmanaged.sheytan.json")
	if err := s.SaveProject(outside); err != nil {
		t.Fatalf("legacy save: %v", err)
	}
	live, err := lib.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 0 {
		t.Fatalf("an unmanaged save must not enter the library (%d entities)", len(live))
	}
	// The document exists at the exact requested path (legacy behavior).
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("legacy document missing: %v", err)
	}
}
