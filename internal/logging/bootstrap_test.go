//go:build goffi_static

package logging

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/apppaths"
)

// clearLogDirEnv pins the Bootstrap/Dir environment hermetic (run
// 45532045379 repair): the Windows gate process exports SHEYTAN_LOG_DIR
// for its runtime captures, and the leaked override silently redirected
// every Bootstrap-based test away from its own layout, failing six tests
// on hosted Windows while all local suites stayed green. Every test that
// calls Bootstrap (or asserts on a layout log path) clears the override
// for its own scope; t.Setenv restores the caller's value afterwards.
func clearLogDirEnv(t *testing.T) {
	t.Helper()
	t.Setenv("SHEYTAN_LOG_DIR", "")
}

func testLayout(t *testing.T) apppaths.Layout {
	t.Helper()
	clearLogDirEnv(t)
	root := t.TempDir()
	lay := apppaths.Layout{
		Mode:     "test",
		DataRoot: root,
		Config:   filepath.Join(root, "config"),
		Logs:     filepath.Join(root, "logs"),
		LogsArch: filepath.Join(root, "logs", "archive"),
		Cache:    filepath.Join(root, "cache"),
		Projects: filepath.Join(root, "projects"),
		Exports:  filepath.Join(root, "exports"),
		Temp:     filepath.Join(root, "temp"),
		Crashes:  filepath.Join(root, "crashes"),
	}
	return lay
}

// TestBootstrapOpensRuntimeLog: Bootstrap writes to the real log file
// before any other startup work (the first-byte contract).
func TestBootstrapOpensRuntimeLog(t *testing.T) {
	lay := testLayout(t)
	l := Bootstrap(lay)
	defer l.Close()
	if l.IsFallback() {
		t.Fatalf("bootstrap must open the real log when the layout is writable")
	}
	l.Logger().Error("bootstrap probe", "reason", "test")
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(lay.Logs, FileName))
	if err != nil {
		t.Fatalf("app.log unreadable: %v", err)
	}
	if !bytes.Contains(raw, []byte("bootstrap probe")) || !bytes.Contains(raw, []byte("reason=test")) {
		t.Fatalf("bootstrap record not persisted: %q", raw)
	}
}

// TestBootstrapHonorsLogDirOverride: the documented SHEYTAN_LOG_DIR
// override (logging.Dir's contract; the build scripts route runtime
// logs into the job's ci-logs tree with it) decides the log directory
// ahead of the layout. Run 45347124889: Bootstrap silently ignored the
// override, so CI runtime logs landed under the data layout instead of
// the documented location.
func TestBootstrapHonorsLogDirOverride(t *testing.T) {
	lay := testLayout(t)
	override := filepath.Join(t.TempDir(), "ci-logs", "app-logs")
	// testLayout already cleared the ambient override; this test pins
	// its OWN override explicitly (the documented contract).
	t.Setenv("SHEYTAN_LOG_DIR", override)
	l := Bootstrap(lay)
	defer l.Close()
	if l.IsFallback() {
		t.Fatalf("bootstrap must open the real log when the override directory is writable")
	}
	l.Logger().Error("override probe", "reason", "test")
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(override, FileName))
	if err != nil {
		t.Fatalf("app.log not written to the SHEYTAN_LOG_DIR override %s: %v", override, err)
	}
	if !bytes.Contains(raw, []byte("override probe")) {
		t.Fatalf("override record not persisted: %q", raw)
	}
	if _, err := os.Stat(filepath.Join(lay.Logs, FileName)); err == nil {
		t.Fatalf("the layout log path must stay unused while the override is set")
	}
}

// TestBootstrapDegradesToStderr: an unwritable log location must never
// take the product down - Bootstrap returns the stderr fallback and
// records the reason ON it.
func TestBootstrapDegradesToStderr(t *testing.T) {
	lay := testLayout(t)
	lay.Logs = filepath.Join(lay.DataRoot, "blocked", "logs")
	// A FILE where the log directory must be created makes MkdirAll fail.
	if err := os.MkdirAll(filepath.Dir(lay.Logs), 0o755); err != nil {
		t.Fatalf("prep: %v", err)
	}
	if err := os.WriteFile(lay.Logs, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("prep file: %v", err)
	}
	l := Bootstrap(lay)
	defer l.Close()
	if !l.IsFallback() {
		t.Fatalf("bootstrap must degrade to the stderr fallback when the log dir is unwritable")
	}
}

// TestWriteCrashReportPersistsFile: a recovered panic becomes an ASCII
// crash report under the crashes dir plus an ERROR record.
func TestWriteCrashReportPersistsFile(t *testing.T) {
	lay := testLayout(t)
	l := Bootstrap(lay)
	defer l.Close()
	l.WriteCrashReport(lay.Crashes, "unit test", "boom", []byte("goroutine 1 [running]:\nmain()"))
	entries, err := os.ReadDir(lay.Crashes)
	if err != nil {
		t.Fatalf("crashes dir unreadable: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("want exactly one crash report, got %d", len(entries))
	}
	name := entries[0].Name()
	if !strings.HasPrefix(name, CrashFilePrefix) || !strings.HasSuffix(name, ".txt") {
		t.Fatalf("crash report name %q does not follow the crash-<utc>.txt contract", name)
	}
	raw, err := os.ReadFile(filepath.Join(lay.Crashes, name))
	if err != nil {
		t.Fatalf("crash report unreadable: %v", err)
	}
	for _, want := range []string{"subject: unit test", "recovered: boom", "goroutine 1"} {
		if !bytes.Contains(raw, []byte(want)) {
			t.Fatalf("crash report missing %q: %q", want, raw)
		}
	}
	for i, b := range raw {
		if b > 0x7E || (b < 0x20 && b != '\n' && b != '\t') {
			t.Fatalf("crash report byte %d (0x%02X) breaks the ASCII contract", i, b)
		}
	}
}

// TestWriteCrashReportUnavailableDir: an unwritable crash dir must not
// lose the report - it is inlined into the log record.
func TestWriteCrashReportUnavailableDir(t *testing.T) {
	lay := testLayout(t)
	// A FILE where the crash directory must be created makes MkdirAll fail.
	blocked := filepath.Join(lay.DataRoot, "blocked")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("prep: %v", err)
	}
	l := Bootstrap(lay)
	defer l.Close()
	l.WriteCrashReport(filepath.Join(blocked, "crashes"), "no-dir", "value", []byte("stack"))
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(lay.Logs, FileName))
	if err != nil {
		t.Fatalf("app.log unreadable: %v", err)
	}
	if !bytes.Contains(raw, []byte("report inlined")) || !bytes.Contains(raw, []byte("recovered")) {
		t.Fatalf("inlined crash report missing: %q", raw)
	}
}

// TestTeeStderrFansOut: every stderr write reaches BOTH the console and
// the log file (the GUI-subsystem persistence contract).
func TestTeeStderrFansOut(t *testing.T) {
	lay := testLayout(t)
	l := Bootstrap(lay)
	defer l.Close()

	console := &bytes.Buffer{}
	tee := &stderrTee{log: l.file, orig: console}
	payload := []byte("startup failure line\n")
	if n, err := tee.Write(payload); err != nil || n != len(payload) {
		t.Fatalf("tee write: n=%d err=%v", n, err)
	}
	if console.String() != string(payload) {
		t.Fatalf("console side lost the payload: %q", console.String())
	}
	l.Close() // flush+close, then read the file
	raw, err := os.ReadFile(filepath.Join(lay.Logs, FileName))
	if err != nil {
		t.Fatalf("app.log unreadable: %v", err)
	}
	if !bytes.Contains(raw, payload) {
		t.Fatalf("log side lost the payload: %q", raw)
	}
}

// TestTeeStderrSurvivesBrokenConsole: a failing console side must not
// lose the log side (a GUI-subsystem process may have no console).
func TestTeeStderrSurvivesBrokenConsole(t *testing.T) {
	lay := testLayout(t)
	l := Bootstrap(lay)
	defer l.Close()
	tee := &stderrTee{log: l.file, orig: failingWriter{}}
	if n, _ := tee.Write([]byte("line\n")); n != 5 {
		t.Fatalf("tee must still count the log-side write when the console fails: n=%d", n)
	}
	l.Close()
	raw, _ := os.ReadFile(filepath.Join(lay.Logs, FileName))
	if !bytes.Contains(raw, []byte("line\n")) {
		t.Fatalf("log side lost the payload on console failure")
	}
}

type failingWriter struct{}

func (failingWriter) Write(p []byte) (int, error) { return 0, os.ErrClosed }

// TestLogExitRecordsCode: the final exit-code record is persisted so an
// immediate silent exit is visible in the log.
func TestLogExitRecordsCode(t *testing.T) {
	lay := testLayout(t)
	l := Bootstrap(lay)
	l.LogExit(3)
	l.Close()
	raw, _ := os.ReadFile(filepath.Join(lay.Logs, FileName))
	if !bytes.Contains(raw, []byte("msg=exit")) || !bytes.Contains(raw, []byte("code=3")) {
		t.Fatalf("exit record missing: %q", raw)
	}
}
