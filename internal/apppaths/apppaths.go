// Package apppaths is the SINGLE data-path authority for SHEYTAN Music
// Visualizer. Logging, configuration, cache, projects, exports, temp
// files, crash diagnostics and external tool resolution all use this
// package; no subsystem invents paths independently.
//
// Directory contract (portable packages carry this tree beside the
// executable; installed builds resolve the same tree under a writable
// per-user base):
//
//	<app root>/                 executable, license.md, readme.md
//	  portable.mode             marker shipped ONLY in portable packages
//	  tools/ffmpeg/bin/         optional app-local FFmpeg (see fflocate)
//	  data/                     mutable data root (portable mode)
//	    config/  logs/  logs/archive/  cache/
//	    projects/  exports/  temp/  crashes/
//
// Modes:
//
//   - ModeEnvOverride: SHEYTAN_DATA_DIR names the data root explicitly
//     (CI, tests, advanced users). Explicit configuration, never a
//     silent fallback.
//   - ModePortable: a portable.mode marker exists beside the
//     executable AND the data root is usable (probed for real write
//     access by Bootstrap). All mutable data stays beside the
//     executable.
//   - ModeInstalled: no portable marker (or the portable data root is
//     not usable). Windows: %LOCALAPPDATA% (per-device, never the
//     roaming profile — logs and caches must not bloat roaming
//     storage). Linux: XDG data home (~/.local/share per default).
//   - ModeFallback: nothing else resolved; a directory under the
//     system temp dir is used and the reason is recorded for the UI
//     and the log. Fallback is visible, never silent.
//
// Migration policy: existing data written by earlier versions
// (Windows %APPDATA%, Linux ~/.config — the pre-resolver log
// location) is never moved or deleted. logging.MigrateLegacy performs
// a read-only COPY of the previous app.log into the new location on
// first run so continuity is preserved without destructive moves.
//
// Upgrades/uninstall: only the executable-side files change during an
// upgrade; user data (projects/exports/config/logs) survives because
// installed-mode data lives outside the install directory, and
// portable-mode data lives in data/ which distributors never replace.
package apppaths

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Mode is the resolved data-path mode.
type Mode string

const (
	// ModeEnvOverride: SHEYTAN_DATA_DIR was set explicitly.
	ModeEnvOverride Mode = "env-override"
	// ModePortable: portable.mode marker beside the executable and a
	// usable data root.
	ModePortable Mode = "portable"
	// ModeInstalled: per-user installed data location.
	ModeInstalled Mode = "installed"
	// ModeFallback: no writable per-user location resolved; temp dir.
	ModeFallback Mode = "fallback"
)

// MarkerFile is the portable-mode marker shipped in portable packages.
const MarkerFile = "portable.mode"

// DataDir is the mutable data directory name inside the app root in
// portable mode.
const DataDir = "data"

// DirName is the product directory name used inside per-user bases.
const DirName = "SHEYTAN Music Visualizer"

// SubDirs are the subdirectories created under the data root by
// Bootstrap (exported for documentation and tests).
var SubDirs = []string{
	"config", "logs", filepath.Join("logs", "archive"), "cache",
	"projects", "exports", "temp", "crashes",
}

// Layout is the resolved, single-source path set.
type Layout struct {
	Mode   Mode   // resolved mode
	Reason string // human-readable resolution note (shown in UI/log)

	AppRoot string // directory of the running executable ("" when unknown)
	Tools   string // app-local tools dir: <AppRoot>/tools ("" when unknown)

	DataRoot string // root for ALL mutable data
	Config   string
	Logs     string
	LogsArch string // rotated log archive
	Cache    string
	Projects string
	Exports  string
	Temp     string
	Crashes  string
}

// Env describes the process inputs Resolve needs; tests inject values
// instead of mutating process state.
type Env struct {
	LookupEnv func(string) (string, bool)
	ExecPath  func() (string, error) // os.Executable equivalent
	UserHome  func() (string, error) // os.UserHomeDir equivalent
	TempDir   func() string          // os.TempDir equivalent
	GOOS      string                 // runtime.GOOS equivalent
}

// OSEnv builds the real process environment.
func OSEnv() Env {
	return Env{
		LookupEnv: os.LookupEnv,
		ExecPath:  os.Executable,
		UserHome:  os.UserHomeDir,
		TempDir:   os.TempDir,
		GOOS:      runtime.GOOS,
	}
}

func (e Env) get(key string) string {
	if e.LookupEnv == nil {
		return ""
	}
	v, _ := e.LookupEnv(key)
	return strings.TrimSpace(v)
}

func (e Env) home() string {
	if e.UserHome == nil {
		return ""
	}
	h, err := e.UserHome()
	if err != nil {
		return ""
	}
	return h
}

// Resolve computes the layout. It performs NO filesystem mutation;
// Bootstrap creates the directories. Any path component that exists as
// a regular file makes the candidate unusable (fail-over with reason).
func Resolve(e Env) Layout {
	// 1) Explicit override wins.
	if root := e.get("SHEYTAN_DATA_DIR"); root != "" {
		l := layoutForRoot(root)
		l.Mode = ModeEnvOverride
		l.Reason = "SHEYTAN_DATA_DIR override"
		l.AppRoot, l.Tools = appRoot(e)
		return l
	}

	appRoot, tools := appRoot(e)

	// 2) Portable mode: marker beside the executable.
	if appRoot != "" {
		marker := filepath.Join(appRoot, MarkerFile)
		if fileExists(marker) {
			root := filepath.Join(appRoot, DataDir)
			if dirUsable(root) {
				l := layoutForRoot(root)
				l.Mode = ModePortable
				l.Reason = "portable package (portable.mode beside executable)"
				l.AppRoot, l.Tools = appRoot, tools
				return l
			}
			// Portable root exists but is not usable — fall back to the
			// installed mode; the reason records why so the UI and the
			// log show exactly what happened.
			l := installedLayout(e)
			l.AppRoot, l.Tools = appRoot, tools
			l.Reason = "portable.mode present but " + root +
				" is not usable; using the per-user location (read-only media?)"
			return l
		}
	}

	// 3) Installed per-user layout.
	l := installedLayout(e)
	l.AppRoot, l.Tools = appRoot, tools
	return l
}

func layoutForRoot(root string) Layout {
	return Layout{
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
}

// installedLayout resolves the writable per-user base.
// Windows: %LOCALAPPDATA% (per-device; the roaming profile is avoided
// for logs/caches by policy). Linux: XDG_DATA_HOME or ~/.local/share.
func installedLayout(e Env) Layout {
	var base string
	switch e.GOOS {
	case "windows":
		local := e.get("LOCALAPPDATA")
		if local == "" || !dirUsable(local) {
			local = ""
		}
		if local == "" {
			home := e.home()
			if home != "" {
				local = filepath.Join(home, "AppData", "Local")
			}
		}
		if local != "" {
			base = filepath.Join(local, DirName)
		}
	default: // linux and other XDG platforms
		dataHome := e.get("XDG_DATA_HOME")
		if dataHome == "" {
			if home := e.home(); home != "" {
				dataHome = filepath.Join(home, ".local", "share")
			}
		}
		if dataHome != "" {
			base = filepath.Join(dataHome, DirName)
		}
	}
	if base != "" && dirUsable(base) {
		l := layoutForRoot(filepath.Join(base, "data"))
		l.Mode = ModeInstalled
		l.Reason = "installed per-user data location"
		return l
	}

	// 4) Last resort: temp dir. Recorded and visible, never silent.
	root := filepath.Join(e.TempDir(), DirName, "data")
	l := layoutForRoot(root)
	l.Mode = ModeFallback
	l.Reason = "no writable per-user location resolved; using a temporary directory (data will not survive reboots)"
	return l
}

// appRoot returns the executable's directory and its tools dir.
func appRoot(e Env) (root, tools string) {
	if e.ExecPath == nil {
		return "", ""
	}
	exe, err := e.ExecPath()
	if err != nil {
		return "", ""
	}
	// Resolve symlinks so a linked binary still finds its real folder.
	if resolved, lerr := filepath.EvalSymlinks(exe); lerr == nil {
		exe = resolved
	}
	dir := filepath.Dir(filepath.Clean(exe))
	if dir == "" || dir == "." {
		return "", ""
	}
	return dir, filepath.Join(dir, "tools")
}

// ToolsFFmpegBin is the app-local FFmpeg binary directory used by
// fflocate: <AppRoot>/tools/ffmpeg/bin.
func (l Layout) ToolsFFmpegBin() string {
	if l.Tools == "" {
		return ""
	}
	return filepath.Join(l.Tools, "ffmpeg", "bin")
}

// Bootstrap creates every directory of the layout and verifies real
// write access with a temporary-file probe in the logs and exports
// directories. It returns all failures together so one broken subtree
// does not hide another.
func Bootstrap(l Layout) error {
	if l.DataRoot == "" {
		return errors.New("apppaths: layout has no data root (programming error)")
	}
	dirs := []string{
		l.DataRoot, l.Config, l.Logs, l.LogsArch, l.Cache,
		l.Projects, l.Exports, l.Temp, l.Crashes,
	}
	var errs []error
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			errs = append(errs, fmt.Errorf("apppaths: cannot create %q: %w", d, err))
		}
	}
	for _, d := range []string{l.Logs, l.Exports} {
		if err := probeWritable(d); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// probeWritable verifies dir accepts a real file write, then removes
// the probe file.
func probeWritable(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("apppaths: cannot create %q: %w", dir, err)
	}
	probe, err := os.CreateTemp(dir, ".write-probe-*.tmp")
	if err != nil {
		return fmt.Errorf("apppaths: directory %q is not writable: %w", dir, err)
	}
	name := probe.Name()
	if err := probe.Close(); err != nil {
		return fmt.Errorf("apppaths: cannot close probe file in %q: %w", dir, err)
	}
	if err := os.Remove(name); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("apppaths: cannot remove probe file %q: %w", name, err)
	}
	return nil
}

// dirUsable reports whether dir either does not exist yet (creatable
// later) or is a real directory. A regular file at the path makes it
// unusable.
func dirUsable(dir string) bool {
	fi, err := os.Stat(dir)
	if err == nil {
		return fi.IsDir()
	}
	if os.IsNotExist(err) {
		// The nearest existing ancestor must be a directory too.
		parent := filepath.Dir(dir)
		if parent == dir {
			return true
		}
		return dirUsable(parent)
	}
	return false
}

func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}

// LegacyLogDirs lists the pre-resolver log directories that earlier
// versions used (checked read-only for migration, never deleted).
// Windows: %APPDATA%\SHEYTAN Music Visualizer\logs (roaming — the
// resolver no longer writes there). Other platforms: XDG config home
// (~/.config), the historical logging.Dir() location.
func LegacyLogDirs(e Env) []string {
	var out []string
	switch e.GOOS {
	case "windows":
		appdata := e.get("APPDATA")
		if appdata == "" {
			if home := e.home(); home != "" {
				appdata = filepath.Join(home, "AppData", "Roaming")
			}
		}
		if appdata != "" {
			out = append(out, filepath.Join(appdata, DirName, "logs"))
		}
	default:
		cfg := e.get("XDG_CONFIG_HOME")
		if cfg == "" {
			if home := e.home(); home != "" {
				cfg = filepath.Join(home, ".config")
			}
		}
		if cfg != "" {
			out = append(out, filepath.Join(cfg, DirName, "logs"))
		}
	}
	return out
}
