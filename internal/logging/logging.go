// Package logging provides the user-facing runtime log for SHEYTAN
// Music Visualizer using the standard log/slog package.
//
// Policy (run 42481815519 runtime-logging contract):
//   - Logging is a NORMAL STARTUP feature, not a --self-check feature:
//     Init is called before subsystems and window construction in
//     every interactive launch.
//   - The primary user-facing file is a readable line-oriented text
//     log (app.log) with UTC timestamps and severity.
//   - The active log lives in the apppaths layout (portable data/,
//     installed per-user location, or the visible fallback location).
//     SHEYTAN_LOG_DIR overrides the log directory explicitly (CI and
//     tests); SHEYTAN_DATA_DIR overrides the whole data root.
//   - Append, never truncate: rotation renames an oversized active log
//     to app.log.1 (one retained generation, 5 MiB default) so the
//     previous session's log survives normal launches.
//   - A stderr fallback logger is returned when the file cannot be
//     opened; the caller surfaces the resolved fallback and the exact
//     error. Logger initialization failures are never silently
//     suppressed.
//   - Never log audio contents, media bytes, secrets, credentials or
//     unnecessary full private paths.
package logging

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/apppaths"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/brand"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/buildinfo"
)

const (
	// FileName is the active application log file.
	FileName = "app.log"

	// RotatedSuffix is appended to the previous log on rotation.
	RotatedSuffix = ".1"

	// DefaultMaxBytes bounds the active log file before rotation.
	DefaultMaxBytes = int64(5 << 20) // 5 MiB

	// RetainedGenerations documents the rotation retention: the
	// active log plus exactly one rotated generation are kept.
	RetainedGenerations = 1
)

// Dir returns the app-owned log directory. SHEYTAN_LOG_DIR overrides
// explicitly; otherwise the apppaths layout decides (portable data/,
// installed per-user base, visible fallback).
func Dir() (string, error) {
	if override := strings.TrimSpace(os.Getenv("SHEYTAN_LOG_DIR")); override != "" {
		return filepath.Abs(override)
	}
	l := apppaths.Resolve(apppaths.OSEnv())
	if l.Logs == "" {
		return "", errors.New("logging: no log directory could be resolved")
	}
	return l.Logs, nil
}

// Options controls Open.
type Options struct {
	// MaxBytes bounds the active log file before rotation. Values <= 0
	// select DefaultMaxBytes.
	MaxBytes int64
	// Level sets the minimum severity (default Info; use
	// slog.LevelDebug for user-requested verbose mode).
	Level slog.Level
}

// Log is an open application log. Call Close when done. All methods
// are safe for concurrent goroutines (slog handlers synchronize).
type Log struct {
	logger   *slog.Logger
	file     *os.File
	path     string
	fallback bool // true when writing to stderr instead of a file
}

// Open prepares dir, rotates an oversized active log, and returns a
// text slog logger appending to <dir>/app.log. Errors are returned,
// never swallowed.
func Open(dir string, opts Options) (*Log, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("logging: log directory must not be empty")
	}
	maxBytes := opts.MaxBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("logging: cannot create log directory %q: %w", dir, err)
	}
	path := filepath.Join(dir, FileName)
	if info, err := os.Stat(path); err == nil && info.Size() >= maxBytes {
		if err := os.Rename(path, path+RotatedSuffix); err != nil {
			return nil, fmt.Errorf("logging: cannot rotate %q: %w", path, err)
		}
	} else if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("logging: cannot stat %q: %w", path, err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("logging: cannot open %q: %w", path, err)
	}
	level := opts.Level
	if level == 0 {
		level = slog.LevelInfo
	}
	handler := slog.NewTextHandler(f, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			// UTC timestamps for every record (contract: UTC + severity).
			if a.Key == slog.TimeKey {
				if t, ok := a.Value.Any().(time.Time); ok {
					return slog.Attr{Key: slog.TimeKey, Value: slog.TimeValue(t.UTC())}
				}
			}
			return a
		},
	})
	logger := slog.New(handler).With(slog.String("product", brand.ProductName))
	return &Log{logger: logger, file: f, path: path}, nil
}

// OpenDefault resolves the default app log directory and opens the log.
func OpenDefault(opts Options) (*Log, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	return Open(dir, opts)
}

// Init opens the runtime log for normal interactive startup and writes
// the startup banner (version, commit, build date/builder, Go version,
// OS/arch, PID, data mode, log path). On failure it returns a
// stderr-backed logger, the resolved fallback name and the error — the
// caller surfaces both and the application keeps running: a broken log
// file must never take the product down.
func Init(l apppaths.Layout, debug bool) (log *Log, using string, initErr error) {
	opts := Options{}
	if debug {
		opts.Level = slog.LevelDebug
	}
	log, initErr = Open(l.Logs, opts)
	if initErr != nil {
		log = stderrLog(opts.Level)
		using = "stderr"
		log.Logger().Error("runtime log unavailable; falling back to stderr",
			slog.String("wanted_path", filepath.Join(l.Logs, FileName)),
			slog.String("err", initErr.Error()))
		return log, using, initErr
	}
	log.LogStartupBanner(l)
	return log, log.Path(), nil
}

// stderrLog builds the fallback logger (never nil).
func stderrLog(level slog.Level) *Log {
	if level == 0 {
		level = slog.LevelInfo
	}
	h := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	return &Log{logger: slog.New(h).With(slog.String("product", brand.ProductName)), fallback: true}
}

// LogStartupBanner records the process and build identity, the data
// mode with its reason, and the active log path.
func (l *Log) LogStartupBanner(lay apppaths.Layout) {
	if l == nil || l.logger == nil {
		return
	}
	i := buildinfo.Normalized()
	l.logger.Info("startup",
		slog.String("version", i.Version),
		slog.String("commit", i.Commit),
		slog.String("built", i.Date),
		slog.String("built_by", i.BuiltBy),
		slog.String("go", i.GoVersion),
		slog.String("os", runtime.GOOS),
		slog.String("arch", runtime.GOARCH),
		slog.Int("pid", os.Getpid()),
		slog.String("data_mode", string(lay.Mode)),
		slog.String("data_reason", lay.Reason),
		slog.String("data_root", lay.DataRoot),
		slog.String("log_path", l.Path()),
	)
}

// Logger returns the underlying slog logger (nil-safe).
func (l *Log) Logger() *slog.Logger {
	if l == nil || l.logger == nil {
		return nil
	}
	return l.logger
}

// Path returns the active log file path (empty for the stderr
// fallback).
func (l *Log) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

// IsFallback reports whether this log writes to stderr because the
// real file could not be opened.
func (l *Log) IsFallback() bool { return l != nil && l.fallback }

// Close flushes and closes the log file (no-op for the fallback).
func (l *Log) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := l.file.Sync()
	if cerr := l.file.Close(); err == nil {
		err = cerr
	}
	l.file = nil
	l.logger = nil
	return err
}

// VerifyWritable checks that dir exists (or can be created) and accepts
// writes, without keeping a file open.
func VerifyWritable(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("logging: cannot create %q: %w", dir, err)
	}
	probe, err := os.CreateTemp(dir, "selfcheck-*.tmp")
	if err != nil {
		return fmt.Errorf("logging: directory %q is not writable: %w", dir, err)
	}
	name := probe.Name()
	if err := probe.Close(); err != nil {
		return fmt.Errorf("logging: cannot close probe file in %q: %w", dir, err)
	}
	if err := os.Remove(name); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("logging: cannot remove probe file %q: %w", name, err)
	}
	return nil
}

// Stat reports the size of the active log file, if any.
func Stat(dir string) (fs.FileInfo, error) {
	return os.Stat(filepath.Join(dir, FileName))
}

// MigrateLegacy preserves continuity without destructive moves: when
// the new log does not exist yet and a pre-resolver app.log is found
// in a legacy location, it is COPIED (never moved) into the new logs
// directory under its legacy name. Errors are reported, never fatal.
// The env parameter makes the legacy locations testable; callers pass
// apppaths.OSEnv().
func MigrateLegacy(lay apppaths.Layout, e apppaths.Env) (migrated string, err error) {
	if lay.Logs == "" {
		return "", nil
	}
	newPath := filepath.Join(lay.Logs, FileName)
	if _, statErr := os.Stat(newPath); statErr == nil {
		return "", nil // active log already exists; nothing to migrate
	}
	for _, legacyDir := range apppaths.LegacyLogDirs(e) {
		legacy := filepath.Join(legacyDir, FileName)
		raw, rerr := os.ReadFile(legacy)
		if rerr != nil || len(raw) == 0 {
			continue
		}
		if err := os.MkdirAll(lay.Logs, 0o755); err != nil {
			return "", fmt.Errorf("logging: migration target unavailable: %w", err)
		}
		dst := filepath.Join(lay.Logs, "app.legacy.log")
		if werr := os.WriteFile(dst, raw, 0o644); werr != nil {
			return "", fmt.Errorf("logging: migration copy failed: %w", werr)
		}
		return dst, nil
	}
	return "", nil
}

// runtime indirections removed: the banner reports runtime.GOOS and
// runtime.GOARCH directly.
