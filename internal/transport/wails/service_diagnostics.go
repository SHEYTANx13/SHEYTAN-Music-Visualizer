package wails

// service_diagnostics.go — the runtime log catcher (run 46168717789
// task: "I opened the windows version but there is no log to attach it
// seems log catcher is having an issue").
//
// TWO defects are repaired here:
//
//  1. THE PRODUCT SHELL NEVER OPENED THE APP LOG. Only the CLI
//     authority (cmd/sheytan-music-visualizer) called logging.Open, so
//     app-wails.exe — the shell users actually run — wrote NO app.log
//     at all. The Diagnostics tab then displayed a log path that no
//     file ever backed. runShell (cmd/app-wails) now opens the log
//     before the studio and hands it to BuildApp; this file carries
//     the facade side (setRuntimeLog + live GetDiagnostics evidence).
//
//  2. THERE WAS NO WAY TO ATTACH A LOG. The Diagnostics pane printed
//     a path string and nothing else. The new diagnostics.export
//     command writes a single ZIP bundle through the native save
//     dialog: the active app log (plus the rotated generation and the
//     bounded crash reports when present), the machine-readable build
//     metadata and the data-layout facts. Everything in the bundle is
//     already ASCII-sanitized app evidence (internal/logging contract)
//     — no media content, no credentials, no web-layer path access.

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/apppaths"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/buildinfo"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/logging"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/presentation"
)

// setRuntimeLog attaches the shell's live application log so the
// Diagnostics surface reports the REAL writer state instead of a
// theoretical path. Called once from BuildApp; nil-safe (headless
// contexts keep the resolution-based honest states).
func (s *SheytanService) setRuntimeLog(lg *logging.Log) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rtLog = lg
}

// runtimeLog returns the attached live log (nil when headless).
func (s *SheytanService) runtimeLog() *logging.Log {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rtLog
}

// bundleSource is one file that goes into the diagnostics bundle.
type bundleSource struct {
	// Name inside the ZIP archive (forward slashes, no traversal).
	Name string
	// Path on disk; empty for synthesized entries (Text).
	Path string
	// Text is the entry body for synthesized entries.
	Text string
}

// collectDiagnosticsSources gathers the app-evidence files for the
// bundle. Absent optional files are skipped silently (they are
// optional BY CONTRACT — absence is not an error, the bundle is still
// written); required errors propagate.
func collectDiagnosticsSources(lay apppaths.Layout, lg *logging.Log) ([]bundleSource, error) {
	sources := make([]bundleSource, 0, 8)

	// Build metadata (machine-readable CI contract JSON) + data layout.
	info := buildinfo.Normalized()
	sources = append(sources, bundleSource{
		Name: "build-info.json",
		Text: fmt.Sprintf("{\n  \"version\": %q,\n  \"commit\": %q,\n  \"date\": %q,\n  \"builtBy\": %q,\n  \"go\": %q\n}\n",
			info.Version, info.Commit, info.Date, info.BuiltBy, info.GoVersion),
	}, bundleSource{
		Name: "data-layout.txt",
		Text: fmt.Sprintf("mode=%s\nreason=%s\ndataRoot=%s\nos=%s arch=%s\ngeneratedUtc=%s\n",
			lay.Mode, lay.Reason, lay.DataRoot, GOOSName(), GOArchName(),
			time.Now().UTC().Format("2006-01-02T15:04:05Z")),
	})

	// The live log (and its rotated generation). The writer state is
	// reported honestly: a stderr fallback writes no file, and the
	// bundle says exactly that.
	if lg != nil {
		sources = append(sources, bundleSource{
			Name: "log-writer.txt",
			Text: fmt.Sprintf("fallback=%v\npath=%s\n",
				lg.IsFallback(), lg.Path()),
		})
		if !lg.IsFallback() && lg.Path() != "" {
			sources = append(sources, bundleSource{Name: "app.log", Path: lg.Path()})
			if rotated := lg.Path() + logging.RotatedSuffix; fileExistsNonEmpty(rotated) {
				sources = append(sources, bundleSource{Name: "app.log.1", Path: rotated})
			}
		}
	} else if dir, err := logging.Dir(); err == nil {
		// Headless/test context without an attached writer: attach the
		// resolved-location log if one exists, never invent one.
		p := filepath.Join(dir, logging.FileName)
		if fileExistsNonEmpty(p) {
			sources = append(sources, bundleSource{Name: "app.log", Path: p})
		}
	}

	// Bounded crash reports (internal/logging session contract: at most
	// maxCrashReports files, each bounded in size, names sanitized).
	if lay.Crashes != "" {
		entries, err := os.ReadDir(lay.Crashes)
		if err == nil {
			count := 0
			for _, e := range entries {
				if count >= 10 {
					break
				}
				if e.IsDir() || !strings.HasPrefix(e.Name(), "crash-") || !strings.HasSuffix(e.Name(), ".txt") {
					continue
				}
				sources = append(sources, bundleSource{
					Name: "crashes/" + e.Name(),
					Path: filepath.Join(lay.Crashes, e.Name()),
				})
				count++
			}
		}
	}
	return sources, nil
}

// writeDiagnosticsBundle writes the ZIP atomically: the bundle is
// staged beside the destination and renamed only after the archive
// closed cleanly, so a failed export can never leave a half-written
// bundle that looks complete.
func writeDiagnosticsBundle(dst string, lay apppaths.Layout, lg *logging.Log) error {
	if strings.TrimSpace(dst) == "" {
		return fmt.Errorf("diagnostics: destination path is empty")
	}
	if !strings.HasSuffix(strings.ToLower(dst), ".zip") {
		return fmt.Errorf("diagnostics: destination must be a .zip path, got %q", filepath.Base(dst))
	}
	if dir := filepath.Dir(dst); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("diagnostics: cannot create destination directory: %w", err)
		}
	}
	sources, err := collectDiagnosticsSources(lay, lg)
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		return fmt.Errorf("diagnostics: no evidence to bundle (no log writer attached and no evidence files found)")
	}

	staged := dst + ".part"
	f, err := os.OpenFile(staged, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("diagnostics: cannot stage bundle: %w", err)
	}
	zw := zip.NewWriter(f)
	ok := false
	defer func() {
		if !ok {
			_ = zw.Close()
			_ = f.Close()
			_ = os.Remove(staged)
		}
	}()
	for _, src := range sources {
		name := strings.ReplaceAll(src.Name, "\\", "/")
		if strings.Contains(name, "..") || strings.HasPrefix(name, "/") {
			return fmt.Errorf("diagnostics: unsafe bundle entry name %q", src.Name)
		}
		w, err := zw.Create(name)
		if err != nil {
			return fmt.Errorf("diagnostics: cannot create zip entry %q: %w", name, err)
		}
		if src.Text != "" {
			if _, err := io.WriteString(w, src.Text); err != nil {
				return fmt.Errorf("diagnostics: cannot write entry %q: %w", name, err)
			}
			continue
		}
		fh, err := os.Open(src.Path)
		if err != nil {
			return fmt.Errorf("diagnostics: cannot read evidence %q: %w", src.Path, err)
		}
		_, copyErr := io.Copy(w, fh)
		closeErr := fh.Close()
		if copyErr != nil {
			return fmt.Errorf("diagnostics: cannot copy evidence %q: %w", src.Path, copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("diagnostics: cannot close evidence %q: %w", src.Path, closeErr)
		}
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("diagnostics: cannot finalize zip: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("diagnostics: cannot close staged bundle: %w", err)
	}
	if err := os.Rename(staged, dst); err != nil {
		_ = os.Remove(staged)
		return fmt.Errorf("diagnostics: cannot finalize bundle: %w", err)
	}
	ok = true
	return nil
}

// runDiagnosticsExport dispatches diagnostics.export through the native
// save dialog. The chosen path is consumed inside Go; it never crosses
// into the web layer (the same boundary rule as export.start).
func (s *SheytanService) runDiagnosticsExport(cmd presentation.CommandID) error {
	host := s.dialogsHost()
	if host == nil {
		return &FacadeError{Command: cmd, Kind: KindRejected,
			Reason: "no native dialog host registered (headless or test context cannot open OS dialogs)"}
	}
	defaultName := fmt.Sprintf("sheytan-diagnostics-%s.zip",
		time.Now().UTC().Format("20060102-150405"))
	path, err := host.SaveBundlePath(defaultName)
	if err != nil {
		return &FacadeError{Command: cmd, Kind: KindRejected,
			Reason: fmt.Sprintf("diagnostics dialog failed: %v", err)}
	}
	if strings.TrimSpace(path) == "" {
		return nil // user cancelled the dialog; no state change
	}
	lay := apppaths.Resolve(apppaths.OSEnv())
	if err := writeDiagnosticsBundle(path, lay, s.runtimeLog()); err != nil {
		return reject(cmd, err)
	}
	s.runtimeLogLog(fmt.Sprintf("diagnostics bundle written: %s", path))
	return nil
}

// runtimeLogLog records one diagnostics-lifecycle line into the live
// app log when one is attached (nil-safe no-op).
func (s *SheytanService) runtimeLogLog(msg string) {
	if lg := s.runtimeLog(); lg != nil {
		lg.Logger().Info(msg)
	}
}

// GOOSName/GOArchName are runtime aliases so the data-layout entry can
// carry the platform without importing runtime in two files.
func GOOSName() string   { return runtime.GOOS }
func GOArchName() string { return runtime.GOARCH }

func fileExistsNonEmpty(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir() && info.Size() > 0
}
