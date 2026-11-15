package logging

// session.go — crash diagnostics and session-boundary records (run
// 43712071111 P0-A): every app-owned goroutine boundary that recovers
// a panic writes a BOUNDED crash report into the apppaths crashes/
// directory (never the log directory), and the runtime log carries a
// session-start marker plus a clean-shutdown marker. On the next
// launch, a leftover session sentinel proves the previous session
// ended WITHOUT a clean shutdown, so the previous log is flagged (and
// preserved by the existing append/rotation policy) instead of being
// silently treated as a normal end.

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// SessionSentinelName is the marker file whose presence means a
// session is (or was) active. It is removed only by a clean shutdown.
const SessionSentinelName = "session-active"

// maxCrashReports bounds how many crash-*.txt files are retained in
// the crashes directory (oldest removed first). Crash reports are
// bounded in size by maxCrashReportBytes.
const (
	maxCrashReports     = 10
	maxCrashReportBytes = 256 << 10 // 256 KiB
)

// WriteCrashReport persists one bounded crash report into dir (the
// apppaths crashes directory) and returns the file path. The report
// identifies the subsystem, the operation and the recovered value,
// plus the goroutine stack — never media content, credentials or
// private user data. Errors are returned to the caller (which already
// runs inside a recovered panic boundary); a reporting failure must
// never panic again.
func WriteCrashReport(dir, subsystem, operation string, recovered any, stack []byte) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", fmt.Errorf("logging: crash report directory is empty")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("logging: cannot create crash dir %q: %w", dir, err)
	}
	pruneCrashReports(dir, maxCrashReports-1)
	name := fmt.Sprintf("crash-%s-%d.txt", sanitize(subsystem), time.Now().UnixMilli())
	path := filepath.Join(dir, name)
	var b strings.Builder
	b.WriteString("SHEYTAN Music Visualizer — recovered panic report\n")
	b.WriteString("time_utc=" + time.Now().UTC().Format(time.RFC3339) + "\n")
	b.WriteString("subsystem=" + sanitize(subsystem) + "\n")
	b.WriteString("operation=" + sanitize(operation) + "\n")
	b.WriteString(fmt.Sprintf("go=%s os=%s arch=%s pid=%d\n", runtime.Version(), runtime.GOOS, runtime.GOARCH, os.Getpid()))
	b.WriteString(fmt.Sprintf("recovered=%v\n", recovered))
	b.WriteString("--- stack (bounded) ---\n")
	if len(stack) > maxCrashReportBytes {
		stack = stack[:maxCrashReportBytes]
	}
	b.Write(stack)
	b.WriteString("\n--- end stack ---\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return "", fmt.Errorf("logging: cannot write crash report %q: %w", path, err)
	}
	return path, nil
}

// sanitize keeps crash-report identifiers single-line and bounded.
func sanitize(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 64 {
		s = s[:64]
	}
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return s
}

// pruneCrashReports keeps at most n crash-*.txt files (best effort;
// errors ignored — crash retention must never block recovery).
func pruneCrashReports(dir string, n int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type fileInfo struct {
		name string
		mod  time.Time
	}
	var files []fileInfo
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "crash-") || !strings.HasSuffix(e.Name(), ".txt") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, fileInfo{e.Name(), info.ModTime()})
	}
	for i := 0; i < len(files); i++ {
		for j := i + 1; j < len(files); j++ {
			if files[j].mod.Before(files[i].mod) {
				files[i], files[j] = files[j], files[i]
			}
		}
	}
	excess := len(files) - n
	for i := 0; i < excess; i++ {
		_ = os.Remove(filepath.Join(dir, files[i].name))
	}
}

// MarkSessionActive writes the session sentinel into dir and reports
// whether the PREVIOUS session ended without a clean shutdown (the
// sentinel already existed). The sentinel is removed again only by
// ClearSessionActive on a clean shutdown path.
func MarkSessionActive(dir string) (previousUnclean bool, err error) {
	if strings.TrimSpace(dir) == "" {
		return false, fmt.Errorf("logging: session directory is empty")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, fmt.Errorf("logging: cannot create session dir %q: %w", dir, err)
	}
	path := filepath.Join(dir, SessionSentinelName)
	if _, statErr := os.Stat(path); statErr == nil {
		previousUnclean = true
	}
	content := fmt.Sprintf("pid=%d started_utc=%s\n", os.Getpid(), time.Now().UTC().Format(time.RFC3339))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return previousUnclean, fmt.Errorf("logging: cannot write session sentinel %q: %w", path, err)
	}
	return previousUnclean, nil
}

// ClearSessionActive removes the session sentinel (clean shutdown).
func ClearSessionActive(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	path := filepath.Join(dir, SessionSentinelName)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("logging: cannot remove session sentinel %q: %w", path, err)
	}
	return nil
}

// sessionOnce guards the per-process clean-shutdown marker so repeated
// Close calls (appclose idempotency, defensive repeats) log it once.
var sessionOnce sync.Once

// LogCleanShutdown records the clean-session-end marker and clears the
// session sentinel. Safe to call multiple times (idempotent).
func (l *Log) LogCleanShutdown(logsDir string) {
	if l == nil || l.logger == nil {
		return
	}
	sessionOnce.Do(func() {
		l.logger.Info("shutdown complete (clean session end)")
		if err := ClearSessionActive(logsDir); err != nil {
			l.logger.Warn("session sentinel could not be cleared", "err", err.Error())
		}
	})
}

// LogUncleanPreviousSession records that the previous session did not
// end cleanly (leftover sentinel) so users attach the right log.
func (l *Log) LogUncleanPreviousSession() {
	if l == nil || l.logger == nil {
		return
	}
	l.logger.Warn("previous session ended without a clean-shutdown marker (possible crash); previous log preserved")
}
