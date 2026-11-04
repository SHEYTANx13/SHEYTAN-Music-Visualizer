package logging

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenCreatesFileAndWritesStructuredRecord(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir, Options{})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	l.Logger().Info("hello", "k", "v")
	if err := l.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatalf("log file not readable: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("log file is empty")
	}
	var rec map[string]any
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("log record is not valid JSON: %v (%q)", err, raw)
	}
	if rec["product"] != "SHEYTAN Music Visualizer" {
		t.Fatalf("product field missing/incorrect: %v", rec["product"])
	}
}

func TestOpenRejectsEmptyDir(t *testing.T) {
	if _, err := Open("   ", Options{}); err == nil {
		t.Fatal("expected error for empty log directory")
	}
}

func TestOpenSurfacesCreationError(t *testing.T) {
	// A path component that exists as a regular file must produce a
	// visible error, never a silently dropped log.
	blocked := filepath.Join(t.TempDir(), "file-not-dir")
	if err := os.WriteFile(blocked, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(filepath.Join(blocked, "logs"), Options{}); err == nil {
		t.Fatal("expected error when log directory cannot be created")
	}
}

func TestRotationReplacesRotatedFile(t *testing.T) {
	dir := t.TempDir()
	active := filepath.Join(dir, FileName)
	if err := os.WriteFile(active, []byte(strings.Repeat("a", 4096)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(active+RotatedSuffix, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := Open(dir, Options{MaxBytes: 1024})
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	rot, err := os.ReadFile(active + RotatedSuffix)
	if err != nil {
		t.Fatalf("rotated file missing: %v", err)
	}
	if !strings.HasPrefix(string(rot), "aaaa") {
		t.Fatalf("rotated file does not contain previous active log content")
	}
	newActive, err := os.Stat(active)
	if err != nil {
		t.Fatalf("active log not recreated after rotation: %v", err)
	}
	if newActive.Size() != 0 {
		t.Fatalf("active log should be empty after rotation, got %d bytes", newActive.Size())
	}
}

func TestVerifyWritableFailsOnBlockedPath(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "file-not-dir")
	if err := os.WriteFile(blocked, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyWritable(filepath.Join(blocked, "logs")); err == nil {
		t.Fatal("expected error for unwritable directory")
	}
	if err := VerifyWritable(t.TempDir()); err != nil {
		t.Fatalf("VerifyWritable failed on temp dir: %v", err)
	}
}
