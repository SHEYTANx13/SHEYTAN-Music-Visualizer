// Package fflocate is the SINGLE FFmpeg/ffprobe discovery authority
// for SHEYTAN Music Visualizer. Both the export pipeline and the
// optional audio AAC/M4A decoder adapter resolve their binaries here;
// no subsystem implements its own search.
//
// Resolution order (first fully present pair wins):
//
//  1. Explicit user configuration (SetDirOverride / SHEYTAN_FFMPEG
//     env — back-compat with the existing documented override).
//  2. App-local packaged tools: <app root>/tools/ffmpeg/bin
//     (apppaths Layout.ToolsFFmpegBin). This is where a legally
//     verified redistributable build or a user-approved setup puts
//     the binaries.
//  3. System PATH (exec.LookPath) — the documented external fallback.
//
// The locator only establishes CANDIDATE paths (existence + regular
// file). Version/capability probing stays with the callers
// (export.DetectEnv, audio probeFFmpegVersion), so a stale or
// non-executable binary is still reported honestly instead of being
// silently accepted.
//
// Bundling policy: the project does NOT ship an FFmpeg binary.
// license.md is proprietary and redistributing an FFmpeg build
// (LGPL/GPL configuration, notices, corresponding source) cannot be
// legally verified inside this task; see packaging/FFMPEG-TOOLS-LICENSING.md
// for the documented user-approved local setup path.
package fflocate

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

var (
	mu      sync.Mutex
	dirOver string
)

// SetDirOverride configures an explicit directory containing
// ffmpeg/ffprobe (user setting). Empty clears the override.
func SetDirOverride(dir string) {
	mu.Lock()
	defer mu.Unlock()
	dirOver = strings.TrimSpace(dir)
}

// DirOverride returns the current override ("" when unset).
func DirOverride() string {
	mu.Lock()
	defer mu.Unlock()
	return dirOver
}

// Options carries the process inputs; tests inject them.
type Options struct {
	Override string // explicit directory (highest priority)
	AppLocal string // <app root>/tools/ffmpeg/bin ("" = skip)
	Lookup   func(string) (string, error)
	GOOS     string
}

// OPOpts builds Options from process state and the app layout. The
// explicit SHEYTAN_FFMPEG environment directory (documented override)
// participates as the highest-priority location.
func OPOpts(appLocalBin string) Options {
	o := Options{
		Override: DirOverride(),
		AppLocal: appLocalBin,
		Lookup:   execLookPath,
		GOOS:     runtime.GOOS,
	}
	if o.Override == "" {
		o.Override = strings.TrimSpace(os.Getenv("SHEYTAN_FFMPEG"))
	}
	return o
}

func execLookPath(name string) (string, error) {
	return exec.LookPath(name)
}

// Result is the located binary pair.
type Result struct {
	FFmpeg  string
	FFprobe string
	// Source names which rule resolved: "override", "app-local", "path".
	Source string
}

// Locate resolves the ffmpeg/ffprobe pair. Both binaries must resolve
// from the SAME location class before it is accepted; an ffmpeg
// without ffprobe is not usable for export validation.
func Locate(o Options) (Result, error) {
	// 1) Explicit override.
	if d := strings.TrimSpace(o.Override); d != "" {
		if r, ok := pairInDir(d, "override", o.GOOS); ok {
			return r, nil
		}
		// The override was explicit: an incomplete pair there is an
		// error worth reporting verbatim (never silently fall back).
		return Result{}, fmt.Errorf(
			"fflocate: configured FFmpeg directory %q does not contain a usable ffmpeg+ffprobe pair", d)
	}
	// 2) App-local tools.
	if d := strings.TrimSpace(o.AppLocal); d != "" {
		if r, ok := pairInDir(d, "app-local", o.GOOS); ok {
			return r, nil
		}
	}
	// 3) PATH fallback.
	if o.Lookup == nil {
		return Result{}, fmt.Errorf("fflocate: no lookup function configured")
	}
	ff, err1 := o.Lookup(binName("ffmpeg", o.GOOS))
	fp, err2 := o.Lookup(binName("ffprobe", o.GOOS))
	if err1 == nil && err2 == nil {
		return Result{FFmpeg: ff, FFprobe: fp, Source: "path"}, nil
	}
	return Result{}, fmt.Errorf(
		"fflocate: ffmpeg/ffprobe not found (searched: configured directory, app-local tools dir, PATH); " +
			"install FFmpeg locally or place both binaries in <app>/tools/ffmpeg/bin")
}

// pairInDir checks the exact binary names inside dir.
func pairInDir(dir, source, goos string) (Result, bool) {
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return Result{}, false
	}
	ff := filepath.Join(dir, binName("ffmpeg", goos))
	fp := filepath.Join(dir, binName("ffprobe", goos))
	if isRegular(ff) && isRegular(fp) {
		return Result{FFmpeg: ff, FFprobe: fp, Source: source}, true
	}
	return Result{}, false
}

// binName appends the platform executable suffix.
func binName(base, goos string) string {
	if goos == "windows" {
		return base + ".exe"
	}
	return base
}

func isRegular(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Mode().IsRegular()
}
