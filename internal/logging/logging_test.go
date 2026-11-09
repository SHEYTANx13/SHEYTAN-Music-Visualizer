package logging

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/apppaths"
)

func TestOpenCreatesFileAndWritesReadableRecord(t *testing.T) {
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
	// Readable line-oriented text contract: time= level= msg= and the
	// product identity on one line.
	line := string(raw)
	if !strings.Contains(line, "level=INFO") {
		t.Fatalf("severity missing: %q", line)
	}
	if !strings.Contains(line, "msg=hello") || !strings.Contains(line, "k=v") {
		t.Fatalf("message or attrs missing: %q", line)
	}
	if !strings.Contains(line, "product=\"SHEYTAN Music Visualizer\"") {
		t.Fatalf("product identity missing: %q", line)
	}
	if !strings.Contains(line, "time=") {
		t.Fatalf("UTC timestamp missing: %q", line)
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

// TestOpenAppendsAcrossLaunches is the contract that a normal launch
// must never truncate the previous session's log.
func TestOpenAppendsAcrossLaunches(t *testing.T) {
	dir := t.TempDir()
	first, err := Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	first.Logger().Info("first session")
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(dir, Options{})
	if err != nil {
		t.Fatalf("second launch failed: %v", err)
	}
	second.Logger().Info("second session")
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"first session", "second session"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("log lost content across launches (%q missing): %q", want, raw)
		}
	}
}

// TestConcurrentWritesAreAllRecorded verifies the synchronized writer
// keeps every record under concurrent goroutines.
func TestConcurrentWritesAreAllRecorded(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	const goroutines, perG = 16, 25
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for k := 0; k < perG; k++ {
				l.Logger().Info("concurrent", slog.Int("g", g), slog.Int("k", k))
			}
		}(g)
	}
	wg.Wait()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Count(string(raw), "msg=concurrent")
	if lines != goroutines*perG {
		t.Fatalf("recorded %d of %d concurrent records", lines, goroutines*perG)
	}
}

// TestInitFallsBackToStderrAndSurfacesError verifies the fallback
// logger exists, reports fallback=true, and returns the real error.
func TestInitFallsBackToStderrAndSurfacesError(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "file-not-dir")
	if err := os.WriteFile(blocked, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	lay := apppaths.Layout{Logs: filepath.Join(blocked, "logs")}
	l, using, initErr := Init(lay, false)
	if l == nil {
		t.Fatal("Init must always return a usable logger (stderr fallback)")
	}
	if initErr == nil {
		t.Fatal("the initialization error must be surfaced, never suppressed")
	}
	if !l.IsFallback() || using != "stderr" {
		t.Fatalf("fallback not reported: using=%q isFallback=%v", using, l.IsFallback())
	}
	if l.Path() != "" {
		t.Fatalf("fallback log must not claim a file path: %q", l.Path())
	}
	// The fallback must actually be usable.
	l.Logger().Error("fallback write check")
}

// TestInitWritesStartupBanner verifies the startup banner carries
// version/commit/go/os/arch/pid/data mode/log path.
func TestInitWritesStartupBanner(t *testing.T) {
	dir := t.TempDir()
	lay := apppaths.Layout{
		Mode: apppaths.ModePortable, Reason: "test",
		DataRoot: filepath.Join(dir, "data"), Logs: dir,
	}
	l, using, initErr := Init(lay, false)
	if initErr != nil || using != filepath.Join(dir, FileName) {
		t.Fatalf("Init: using=%q err=%v", using, initErr)
	}
	defer l.Close()
	l.Logger().Info("post-banner")
	raw, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, want := range []string{
		"msg=startup", "version=", "commit=", "go=", "os=", "arch=",
		"pid=", "data_mode=portable", "log_path=",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("banner missing %q in %q", want, s)
		}
	}
}

// TestMigrateLegacyCopiesWithoutDestroying verifies the non-destructive
// migration: the legacy file still exists after the copy.
func TestMigrateLegacyCopiesWithoutDestroying(t *testing.T) {
	base := t.TempDir()
	legacyDir := filepath.Join(base, "home", ".config", apppaths.DirName, "logs")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(legacyDir, FileName)
	content := []byte("legacy session evidence")
	if err := os.WriteFile(legacy, content, 0o644); err != nil {
		t.Fatal(err)
	}
	newLogs := filepath.Join(base, "new", "logs")
	lay := apppaths.Layout{Logs: newLogs}
	env := apppaths.Env{
		LookupEnv: func(string) (string, bool) { return "", false },
		UserHome:  func() (string, error) { return filepath.Join(base, "home"), nil },
		GOOS:      "linux",
	}
	got, err := MigrateLegacy(lay, env)
	if err != nil {
		t.Fatalf("MigrateLegacy: %v", err)
	}
	if got == "" {
		t.Fatal("legacy log was not migrated")
	}
	raw, err := os.ReadFile(got)
	if err != nil || string(raw) != string(content) {
		t.Fatalf("migrated content mismatch: %v %q", err, raw)
	}
	// The legacy original must still exist (copy, never move/delete).
	if _, err := os.Stat(legacy); err != nil {
		t.Fatalf("legacy log was destroyed by migration: %v", err)
	}
}

// TestMigrateLegacyNoopWhenActiveLogExists verifies migration never
// overwrites a live active log.
func TestMigrateLegacyNoopWhenActiveLogExists(t *testing.T) {
	base := t.TempDir()
	newLogs := filepath.Join(base, "logs")
	if err := os.MkdirAll(newLogs, 0o755); err != nil {
		t.Fatal(err)
	}
	active := filepath.Join(newLogs, FileName)
	if err := os.WriteFile(active, []byte("live"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := apppaths.Env{
		LookupEnv: func(string) (string, bool) { return "", false },
		UserHome:  func() (string, error) { return base, nil },
		GOOS:      "linux",
	}
	got, err := MigrateLegacy(apppaths.Layout{Logs: newLogs}, env)
	if err != nil || got != "" {
		t.Fatalf("migration must be a no-op when an active log exists: got=%q err=%v", got, err)
	}
}
