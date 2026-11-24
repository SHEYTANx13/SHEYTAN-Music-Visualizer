// bootstrap.go — the FIRST-BYTE startup diagnostics contract for the
// desktop shells (run 45297707372 repair).
//
// The Windows product executable is a GUI-subsystem binary (-H=windowsgui):
// it has NO console, so anything written to stderr before this repair
// was invisible and unpersisted — a user whose app "did not open" had
// no evidence at all (the reported failure of the run-45297707372
// package). This file closes that gap:
//
//   - Bootstrap opens the runtime log BEFORE any other startup work and
//     never fails the process: on any error it degrades to a stderr
//     logger and records the reason (logging is a startup feature, not
//     a gate).
//   - TeeStderr fans every later stderr write (usage errors, startup
//     failures, flag-parse diagnostics) into the same log file, so a
//     GUI-subsystem build persists what a console build would print.
//   - WriteCrashReport persists a recovered panic as
//     crashes/crash-<utc>.txt plus an ERROR record, so a crash during
//     startup leaves a durable, human-readable report.
//   - LogExit records the process exit code, so even an immediate
//     silent exit is visible in the log.
//
// Honest boundary: a panic on a NON-main goroutine terminates the
// process through the Go runtime, whose traceback goes to the OS-level
// stderr handle — on a GUI-subsystem Windows build that handle has no
// console and the runtime's cached handle cannot be redirected from
// inside the process. Everything reachable from the main goroutine
// (the whole go-gui window lifecycle on desktop shells) is covered by
// the recover boundary + crash report; goroutine crashes are covered
// wherever the runtime prints to the replaced os.Stderr writer. This
// boundary is documented in docs/TROUBLESHOOTING.md.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/apppaths"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/buildinfo"
)

// CrashFilePrefix names the crash report files written by
// WriteCrashReport into the layout's crashes directory.
const CrashFilePrefix = "crash-"

// Bootstrap opens the runtime log for the earliest possible startup
// moment and returns a never-nil Log. The log directory is chosen by
// the same contract as Dir: the explicit SHEYTAN_LOG_DIR override wins
// (run 45347124889 repair: CI sets SHEYTAN_LOG_DIR to route runtime
// logs into the job's ci-logs tree and the previous layout-only path
// silently ignored that documented override), and the layout's log
// directory is the default. The directory is created best-effort first
// (the layout bootstrap owns the contract directories; a failure here
// must not block startup). On failure the returned Log is the stderr
// fallback and the error is recorded ON that fallback, never
// swallowed.
func Bootstrap(lay apppaths.Layout) *Log {
	dir := lay.Logs
	if override := strings.TrimSpace(os.Getenv("SHEYTAN_LOG_DIR")); override != "" {
		if abs, err := filepath.Abs(override); err == nil {
			dir = abs
		}
	}
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err == nil {
			if l, err := Open(dir, Options{}); err == nil {
				return l
			}
		}
	}
	l := stderrLog(slog.LevelInfo)
	l.Logger().Error("bootstrap: runtime log unavailable; stderr fallback active",
		slog.String("wanted_path", filepath.Join(lay.Logs, FileName)))
	return l
}

// LogExit records the final process exit code (Info severity: an exit
// code is a fact, not an error; non-zero codes carry the reason via
// the error records already written by the caller).
func (l *Log) LogExit(code int) {
	if l == nil || l.logger == nil {
		return
	}
	l.logger.Info("exit", slog.Int("code", code))
}

// WriteCrashReport persists a recovered panic: one ASCII crash report
// file under dir (created best-effort) plus an ERROR record in the
// runtime log. The report carries the recovered value, the goroutine
// stack, the build identity and the UTC timestamp. It never panics and
// never blocks shutdown on I/O errors; every failure is recorded in
// the log instead.
func (l *Log) WriteCrashReport(dir, subject string, recovered any, stack []byte) {
	if l == nil || l.logger == nil {
		return
	}
	bi := buildinfo.Normalized()
	report := fmt.Sprintf(
		"SHEYTAN Music Visualizer crash report\n"+
			"subject: %s\n"+
			"time_utc: %s\n"+
			"version: %s commit: %s built: %s by %s\n"+
			"go: %s os: %s arch: %s pid: %d\n"+
			"recovered: %v\n\nstack:\n%s",
		subject,
		time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		bi.Version, bi.Commit, bi.Date, bi.BuiltBy,
		bi.GoVersion, runtime.GOOS, runtime.GOARCH, os.Getpid(),
		recovered, string(stack))
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err == nil {
			name := fmt.Sprintf("%s%s.txt", CrashFilePrefix,
				time.Now().UTC().Format("20060102-150405"))
			path := filepath.Join(dir, name)
			if err := os.WriteFile(path, []byte(sanitizeString(report)), 0o644); err == nil {
				l.logger.Error("crash report written",
					slog.String("subject", subject), slog.String("path", path))
				return
			} else {
				l.logger.Error("crash report write failed",
					slog.String("dir", dir), slog.String("err", err.Error()))
				return
			}
		}
	}
	l.logger.Error("crash report directory unavailable; report inlined",
		slog.String("subject", subject),
		slog.String("report", report))
}

// TeeStderr returns a writer that fans every write out to the ORIGINAL
// os.Stderr (captured at call time) and the log file. Callers pass it
// wherever stderr-bound diagnostics go (flag usage errors, startup
// failures), so a GUI-subsystem process without a console still
// persists what a console process would print. Writes are serialized
// so concurrent goroutines cannot interleave partial lines into the
// log file.
func (l *Log) TeeStderr() io.Writer {
	if l == nil || l.logger == nil || l.file == nil {
		return os.Stderr
	}
	return &stderrTee{log: l.file, orig: os.Stderr}
}

// stderrTee fans one write out to the original stderr and the log
// file. The log file is already written concurrently by the slog
// handler; the mutex only prevents THIS writer from interleaving its
// own partial lines.
type stderrTee struct {
	mu   sync.Mutex
	log  *os.File
	orig io.Writer
}

func (t *stderrTee) Write(p []byte) (int, error) {
	t.mu.Lock()
	n, err := t.log.Write(p)
	t.mu.Unlock()
	// The console side is best-effort (a GUI-subsystem process may have
	// no console at all); the log side is the durable copy.
	nOrig, errOrig := t.orig.Write(p)
	if err == nil {
		err = errOrig
	}
	if err == nil && n < len(p) {
		err = io.ErrShortWrite
	}
	if n < nOrig {
		n = nOrig
	}
	return n, err
}
