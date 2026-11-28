package wails

// service_new_test.go — run 46168717789 app-feature regression tests:
// the Worlds facade states and the diagnostics bundle writer.

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/apppaths"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/buildinfo"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/logging"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/presentation"
)

// TestWorldsNoTrackState: without a track the Worlds surface reports
// the honest no-track state and never invents a plan.
func TestWorldsNoTrackState(t *testing.T) {
	svc, _, _ := newTestService(t)
	doc := svc.GetWorlds()
	if doc.State != "no-track" {
		t.Fatalf("worlds state = %q, want no-track", doc.State)
	}
	if doc.Plan != nil {
		t.Fatal("worlds must not serve a plan without a track")
	}
}

// TestDiagnosticsExportCommandRegistered: the log-catcher command is
// canonical, dispatched and NOT in the unsupported map.
func TestDiagnosticsExportCommandRegistered(t *testing.T) {
	if err := presentation.ValidateCommand(presentation.CmdDiagnosticsExport); err != nil {
		t.Fatalf("diagnostics.export must be canonical: %v", err)
	}
	if reason, refused := unsupportedReasons[presentation.CmdDiagnosticsExport]; refused {
		t.Fatalf("diagnostics.export must be dispatched, not refused: %s", reason)
	}
	found := false
	for _, id := range dispatchedCommands {
		if id == presentation.CmdDiagnosticsExport {
			found = true
		}
	}
	if !found {
		t.Fatal("diagnostics.export missing from dispatchedCommands")
	}
	for _, id := range []string{"export.preview", "export.subtitle", "export.subtitle.clear"} {
		if err := presentation.ValidateCommand(presentation.CommandID(id)); err != nil {
			t.Fatalf("%s must be canonical: %v", id, err)
		}
	}
}

// TestDiagnosticsBundleWriter: the bundle contains the synthesized
// evidence entries, stages atomically (no .part residue) and opens as
// a valid zip.
func TestDiagnosticsBundleWriter(t *testing.T) {
	dir := t.TempDir()
	lay := apppaths.Layout{
		Mode: apppaths.ModeInstalled, Reason: "test layout",
		DataRoot: dir, Logs: filepath.Join(dir, "logs"),
	}
	dst := filepath.Join(dir, "bundle.zip")
	if err := writeDiagnosticsBundle(dst, lay, nil); err != nil {
		t.Fatalf("bundle write failed: %v", err)
	}
	f, err := os.Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(f, st.Size())
	if err != nil {
		t.Fatalf("bundle is not a valid zip: %v", err)
	}
	names := map[string]bool{}
	for _, e := range zr.File {
		names[e.Name] = true
	}
	if !names["build-info.json"] || !names["data-layout.txt"] {
		t.Fatalf("bundle missing evidence entries: %v", names)
	}
	if _, err := os.Stat(dst + ".part"); !os.IsNotExist(err) {
		t.Fatal("staged .part file leaked after successful rename")
	}
	// build-info content mirrors the machine-readable contract values.
	raw, _ := zr.File[0].Open()
	buf := make([]byte, 4096)
	n, _ := raw.Read(buf)
	if !strings.Contains(string(buf[:n]), buildinfo.Normalized().Version) {
		t.Fatal("bundle build-info.json does not carry the normalized version")
	}
}

// TestRuntimeLogAttachmentEvidence: with an attached live log the
// diagnostics check proves the file; LogPath comes from the writer.
func TestRuntimeLogAttachmentEvidence(t *testing.T) {
	svc, _, _ := newTestService(t)
	dir := t.TempDir()
	lg, err := logging.Open(dir, logging.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	lg.Logger().Info("test line")
	svc.setRuntimeLog(lg)
	if got := svc.runtimeLog(); got != lg {
		t.Fatal("runtime log attachment failed")
	}
	if lg.Path() == "" {
		t.Fatal("attached log must expose its path")
	}
}
