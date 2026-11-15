package logging

// session_test.go — session boundary + crash-report fixtures (run
// 43712071111 P0-A).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSessionSentinelCleanAndUnclean(t *testing.T) {
	dir := t.TempDir()
	if _, err := MarkSessionActive(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, SessionSentinelName)); err != nil {
		t.Fatalf("sentinel missing: %v", err)
	}
	// Second active marking while the sentinel exists: unclean previous.
	unclean, err := MarkSessionActive(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !unclean {
		t.Fatal("leftover sentinel must report an unclean previous session")
	}
	// Clean shutdown clears it; the next start is clean again.
	if err := ClearSessionActive(dir); err != nil {
		t.Fatal(err)
	}
	unclean, err = MarkSessionActive(dir)
	if err != nil || unclean {
		t.Fatalf("after clean shutdown: unclean=%v err=%v", unclean, err)
	}
}

func TestSessionSentinelEmptyDirIsError(t *testing.T) {
	if _, err := MarkSessionActive(""); err == nil {
		t.Fatal("empty dir must be an error")
	}
	if err := ClearSessionActive(""); err != nil {
		t.Fatalf("clearing empty dir must be a no-op: %v", err)
	}
}

func TestWriteCrashReport(t *testing.T) {
	dir := t.TempDir()
	path, err := WriteCrashReport(dir, "render-loop", "preview loop", "boom\nsecond line", []byte("goroutine 1 [running]:\nmain.main()"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, want := range []string{"recovered panic report", "subsystem=render-loop", "operation=preview loop", "goroutine 1"} {
		if !strings.Contains(s, want) {
			t.Errorf("crash report missing %q", want)
		}
	}
	if strings.Count(s, "\n") > 200 {
		t.Error("crash report must stay bounded")
	}
	// A reporting failure (empty dir) must not panic.
	if _, err := WriteCrashReport("", "x", "y", nil, nil); err == nil {
		t.Fatal("empty dir must error")
	}
}

func TestCrashReportRetentionBound(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < maxCrashReports+3; i++ {
		if _, err := WriteCrashReport(dir, "loop", "op", i, []byte("x")); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond) // distinct mtimes
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "crash-") {
			count++
		}
	}
	if count > maxCrashReports {
		t.Fatalf("retention bound exceeded: %d > %d", count, maxCrashReports)
	}
}
