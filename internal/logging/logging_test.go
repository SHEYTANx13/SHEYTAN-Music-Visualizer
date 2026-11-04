package logging

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenCreatesAppOwnedDirAndWritesStructuredEntries(t *testing.T) {
	base := t.TempDir()
	l, err := Open(base, Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer l.Close()

	wantDir := filepath.Join(base, "logs")
	if st, err := os.Stat(wantDir); err != nil || !st.IsDir() {
		t.Fatalf("log dir %s not created: %v", wantDir, err)
	}
	l.Info("foundation started", "component", "test")
	if err := l.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	raw, err := os.ReadFile(l.Path())
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	line := bytes.TrimSpace(raw)
	if len(line) == 0 {
		t.Fatal("log file empty")
	}
	e, err := DecodeEntry(line)
	if err != nil {
		t.Fatalf("decode entry: %v", err)
	}
	if e.Msg != "foundation started" || e.Level != "INFO" {
		t.Fatalf("unexpected entry: %+v", e)
	}
	if e.Attrs["component"] != "test" {
		t.Fatalf("attr component missing: %+v", e.Attrs)
	}
}

func TestOpenRejectsEmptyBaseDir(t *testing.T) {
	if _, err := Open("  ", Options{}); err == nil {
		t.Fatal("expected error for empty base dir")
	}
}

func TestOpenFailsVisiblyWhenBaseIsAFile(t *testing.T) {
	base := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(base, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(base, Options{}); err == nil {
		t.Fatal("expected visible error when base path is a regular file")
	}
}

func TestRotationReplacesOversizedActiveLog(t *testing.T) {
	base := t.TempDir()
	// First session writes a small file; we then inflate it beyond the
	// threshold and expect the next Open to rotate it to app.log.1.
	l, err := Open(base, Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	l.Info("first session")
	l.Close()

	big := strings.Repeat("x", 4096)
	if err := os.WriteFile(filepath.Join(base, "logs", "app.log"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	l2, err := Open(base, Options{MaxBytes: 1024})
	if err != nil {
		t.Fatalf("re-Open: %v", err)
	}
	defer l2.Close()

	if _, err := os.Stat(filepath.Join(base, "logs", "app.log.1")); err != nil {
		t.Fatalf("rotation file missing: %v", err)
	}
	cur, err := os.ReadFile(l2.Path())
	if err != nil {
		t.Fatal(err)
	}
	if len(cur) != 0 {
		t.Fatalf("active log should start empty after rotation, got %d bytes", len(cur))
	}
}

func TestRotationNotTriggeredUnderThreshold(t *testing.T) {
	base := t.TempDir()
	l, err := Open(base, Options{MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	l.Info("small session")
	l.Close()
	if _, err := os.Stat(filepath.Join(base, "logs", "app.log.1")); !os.IsNotExist(err) {
		t.Fatalf("unexpected rotation file: %v", err)
	}
}

func TestEnsureWritableCreatesAndCleansProbe(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "appdir")
	if err := EnsureWritable(dir); err != nil {
		t.Fatalf("EnsureWritable: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".write-probe-") {
			t.Fatalf("probe file left behind: %s", e.Name())
		}
	}
}

func TestEnsureWritableFailsOnReadOnlyPath(t *testing.T) {
	base := t.TempDir()
	readOnly := filepath.Join(base, "ro")
	if err := os.Mkdir(readOnly, 0o500); err != nil {
		t.Skipf("cannot create read-only dir on this host: %v", err)
	}
	if err := EnsureWritable(filepath.Join(readOnly, "sub")); err == nil {
		// Running as root (or privileged) would legitimately pass.
		if os.Geteuid() == 0 {
			t.Skip("running as root; read-only dir is writable")
		}
		t.Fatal("expected failure for read-only path")
	}
}

func TestStderrFallbackLogs(t *testing.T) {
	var buf bytes.Buffer
	l := NewStderr(&buf)
	l.Warn("file logger unavailable", "reason", "test")
	e, err := DecodeEntry(buf.Bytes())
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if e.Msg != "file logger unavailable" || e.Level != "WARN" {
		t.Fatalf("unexpected entry: %+v", e)
	}
}

func TestDefaultBaseDirIsNotSourceTreeOrCWD(t *testing.T) {
	dir, err := DefaultBaseDir()
	if err != nil {
		t.Skipf("user config dir unavailable on host: %v", err)
	}
	cwd, _ := os.Getwd()
	if filepath.IsAbs(dir) == false {
		t.Fatalf("base dir must be absolute, got %q", dir)
	}
	if dir == cwd || dir == filepath.Dir(cwd) {
		t.Fatalf("base dir must not be the source tree or CWD: %q", dir)
	}
	if !strings.Contains(filepath.Base(dir), "SHEYTAN-Music-Visualizer") {
		t.Fatalf("base dir not app-owned: %q", dir)
	}
}
