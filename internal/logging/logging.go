// Package logging provides minimal structured local logging for
// SHEYTAN Music Visualizer using the standard library log/slog.
//
// Contract:
//   - Logs are written to an app-owned directory under the user config/data
//     directory — never the source tree and never the current working
//     directory.
//   - Directory and file creation errors are returned to the caller; logs
//     are never silently dropped.
//   - A simple bounded size/rotation policy keeps the log footprint small:
//     when the active log exceeds Options.MaxBytes it is rotated to
//     "<name>.1" (replacing any previous rotation). This is deliberately
//     not a general logging framework.
//   - Log content security: never log audio/media bytes, credentials,
//     secrets, or unnecessary full user paths.
package logging

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/brand"
)

// DefaultBaseDir returns the app-owned base directory under the user
// config directory (os.UserConfigDir), e.g.:
//   - Windows: %AppData%\SHEYTAN-Music-Visualizer
//   - Linux:   ~/.config/SHEYTAN-Music-Visualizer
//   - macOS:   ~/Library/Application Support/SHEYTAN-Music-Visualizer
func DefaultBaseDir() (string, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("logging: resolve user config dir: %w", err)
	}
	return filepath.Join(cfg, brand.AppDirName), nil
}

// Options configures a file logger.
type Options struct {
	// MaxBytes is the rotation threshold for the active log file.
	// Values <= 0 select the default (5 MiB).
	MaxBytes int64
	// Level is the minimum level persisted to file. Default: Info.
	Level slog.Level
}

const defaultMaxBytes = int64(5 << 20) // 5 MiB

// Logger is the application logger handle.
type Logger struct {
	file *os.File
	log  *slog.Logger
	path string
}

// Open creates (if needed) baseDir/logs and opens the log file baseDir/logs/
// app.log, rotating an oversized existing log first. The returned Logger must
// be closed with Close.
func Open(baseDir string, opts Options) (*Logger, error) {
	if strings.TrimSpace(baseDir) == "" {
		return nil, errors.New("logging: base directory is empty")
	}
	logDir := filepath.Join(baseDir, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, fmt.Errorf("logging: create log directory: %w", err)
	}
	path := filepath.Join(logDir, "app.log")

	max := opts.MaxBytes
	if max <= 0 {
		max = defaultMaxBytes
	}
	if err := rotateIfNeeded(path, max); err != nil {
		return nil, err
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("logging: open log file: %w", err)
	}
	level := opts.Level
	if level == 0 {
		level = slog.LevelInfo
	}
	h := slog.NewJSONHandler(f, &slog.HandlerOptions{Level: level})
	return &Logger{file: f, log: slog.New(h), path: path}, nil
}

// rotateIfNeeded renames path to path+".1" when the file exceeds max bytes.
func rotateIfNeeded(path string, max int64) error {
	st, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("logging: stat log file: %w", err)
	}
	if st.Size() <= max {
		return nil
	}
	if err := os.Remove(path + ".1"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("logging: remove previous rotation: %w", err)
	}
	if err := os.Rename(path, path+".1"); err != nil {
		return fmt.Errorf("logging: rotate log file: %w", err)
	}
	return nil
}

// Path returns the active log file path.
func (l *Logger) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

// Info logs at info level. Key-value attrs only; never log media bytes,
// credentials or secrets.
func (l *Logger) Info(msg string, args ...any) { l.slog().Info(msg, args...) }

// Warn logs at warn level.
func (l *Logger) Warn(msg string, args ...any) { l.slog().Warn(msg, args...) }

// Error logs at error level.
func (l *Logger) Error(msg string, args ...any) { l.slog().Error(msg, args...) }

func (l *Logger) slog() *slog.Logger {
	if l == nil || l.log == nil {
		return slog.Default()
	}
	return l.log
}

// Close flushes and closes the underlying file.
func (l *Logger) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := l.file.Sync()
	if cerr := l.file.Close(); err == nil {
		err = cerr
	}
	l.file = nil
	return err
}

// EnsureWritable verifies that dir can be created and written to, using a
// probe file that is removed afterwards. It is the storage part of the
// --self-check contract.
func EnsureWritable(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("logging: create directory: %w", err)
	}
	probe := filepath.Join(dir, ".write-probe-"+time.Now().UTC().Format("20060102T150405"))
	f, err := os.Create(probe)
	if err != nil {
		return fmt.Errorf("logging: write probe failed: %w", err)
	}
	if _, err := f.WriteString("probe\n"); err != nil {
		f.Close()
		os.Remove(probe)
		return fmt.Errorf("logging: probe write failed: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(probe)
		return fmt.Errorf("logging: probe close failed: %w", err)
	}
	if err := os.Remove(probe); err != nil {
		return fmt.Errorf("logging: probe cleanup failed: %w", err)
	}
	return nil
}

// NewStderr returns a logger writing structured entries to w. It is used
// only when the file logger cannot be created, so the failure stays visible
// on the console while the application can still run.
func NewStderr(w io.Writer) *Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo})
	return &Logger{log: slog.New(h)}
}

// Entry is a decoded structured log entry, used by tests to assert content.
type Entry struct {
	Time  time.Time         `json:"time"`
	Level string            `json:"level"`
	Msg   string            `json:"msg"`
	Attrs map[string]string `json:"-"`
}

// DecodeEntry parses one JSON log line. Attrs collects unknown string fields.
func DecodeEntry(line []byte) (Entry, error) {
	var raw map[string]any
	if err := json.Unmarshal(line, &raw); err != nil {
		return Entry{}, err
	}
	e := Entry{Attrs: map[string]string{}}
	if t, ok := raw["time"].(string); ok {
		if pt, perr := time.Parse(time.RFC3339Nano, t); perr == nil {
			e.Time = pt
		}
	}
	if lv, ok := raw["level"].(string); ok {
		e.Level = lv
	}
	if m, ok := raw["msg"].(string); ok {
		e.Msg = m
	}
	for k, v := range raw {
		switch k {
		case "time", "level", "msg":
			continue
		}
		if s, ok := v.(string); ok {
			e.Attrs[k] = s
		}
	}
	return e, nil
}
