// Package logging provides minimal structured local logging for
// SHEYTAN Music Visualizer using the standard log/slog package.
//
// Policy:
//   - Logs are written to an app-owned directory inside the user config
//     directory — never to the source tree or the current working
//     directory. SHEYTAN_LOG_DIR overrides the directory explicitly
//     (used by tests and CI); the override is explicit configuration,
//     not a silent fallback.
//   - Directory and file creation errors are returned to the caller;
//     logs are never silently dropped.
//   - Log size is bounded with a simple single-file rotation policy.
//   - Never log audio contents, media bytes, secrets, credentials or
//     unnecessary full user paths.
package logging

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/brand"
)

const (
	// FileName is the active application log file.
	FileName = "app.log"

	// RotatedSuffix is appended to the previous log on rotation.
	RotatedSuffix = ".1"

	// DefaultMaxBytes bounds the active log file before rotation.
	DefaultMaxBytes = int64(5 << 20) // 5 MiB
)

// Dir returns the app-owned log directory. An explicit SHEYTAN_LOG_DIR
// environment value overrides the platform default.
func Dir() (string, error) {
	if override := strings.TrimSpace(os.Getenv("SHEYTAN_LOG_DIR")); override != "" {
		return filepath.Abs(override)
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("logging: cannot resolve user config directory: %w", err)
	}
	return filepath.Join(base, brand.LogDirName, "logs"), nil
}

// Options controls Open.
type Options struct {
	// MaxBytes bounds the active log file before rotation. Values <= 0
	// select DefaultMaxBytes.
	MaxBytes int64
}

// Log is an open application log. Call Close when done.
type Log struct {
	logger *slog.Logger
	file   *os.File
	path   string
}

// Open prepares dir, rotates an oversized active log, and returns a JSON
// slog logger writing to <dir>/app.log. Errors are returned, never
// swallowed.
func Open(dir string, opts Options) (*Log, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("logging: log directory must not be empty")
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
	handler := slog.NewJSONHandler(f, &slog.HandlerOptions{Level: slog.LevelInfo})
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

// Logger returns the underlying slog logger.
func (l *Log) Logger() *slog.Logger {
	if l == nil || l.logger == nil {
		return nil
	}
	return l.logger
}

// Path returns the active log file path.
func (l *Log) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

// Close flushes and closes the log file.
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
// writes, without keeping a file open. Used by --self-check.
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
