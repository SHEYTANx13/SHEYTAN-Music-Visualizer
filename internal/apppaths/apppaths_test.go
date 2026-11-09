package apppaths

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func testEnv(home, tmp string, env map[string]string, exe string) Env {
	return Env{
		LookupEnv: func(k string) (string, bool) { v, ok := env[k]; return v, ok },
		ExecPath:  func() (string, error) { return exe, nil },
		UserHome:  func() (string, error) { return home, nil },
		TempDir:   func() string { return tmp },
		GOOS:      runtime.GOOS,
	}
}

// TestPortableMarkerSelectsDataBesideExecutable verifies the portable
// contract: with portable.mode beside the executable all mutable data
// resolves under <exe dir>/data, including when the working directory
// is unrelated (paths with spaces included).
func TestPortableMarkerSelectsDataBesideExecutable(t *testing.T) {
	base := t.TempDir()
	// Deliberate space in the app dir name.
	appDir := filepath.Join(base, "App Folder With Spaces")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(appDir, "sheytan-music-visualizer")
	if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, MarkerFile), []byte("portable\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// An unrelated working directory must not influence resolution.
	env := testEnv(base, base, map[string]string{}, exe)
	l := Resolve(env)
	if l.Mode != ModePortable {
		t.Fatalf("mode = %q, want portable (reason: %s)", l.Mode, l.Reason)
	}
	want := filepath.Join(appDir, DataDir)
	if l.DataRoot != want {
		t.Fatalf("DataRoot = %q, want %q", l.DataRoot, want)
	}
	if l.Logs != filepath.Join(want, "logs") || l.Projects != filepath.Join(want, "projects") {
		t.Fatalf("subdirs not under data root: %+v", l)
	}
	if l.Tools != filepath.Join(appDir, "tools") {
		t.Fatalf("Tools = %q, want %q", l.Tools, filepath.Join(appDir, "tools"))
	}
	if l.ToolsFFmpegBin() != filepath.Join(appDir, "tools", "ffmpeg", "bin") {
		t.Fatalf("ToolsFFmpegBin = %q", l.ToolsFFmpegBin())
	}
}

// TestReadOnlyPortableFallsBackToInstalled verifies the read-only
// portable fallback: when data/ beside the executable cannot be used
// (a regular file blocks creation) the layout falls back to the
// installed mode and the reason says exactly why.
func TestReadOnlyPortableFallsBackToInstalled(t *testing.T) {
	base := t.TempDir()
	appDir := filepath.Join(base, "readonly")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(appDir, "app")
	if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, MarkerFile), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	// data exists as a REGULAR FILE -> directory unusable.
	if err := os.WriteFile(filepath.Join(appDir, DataDir), []byte("blocker"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := testEnv(base, base, map[string]string{}, exe)
	l := Resolve(env)
	if l.Mode != ModeInstalled {
		t.Fatalf("mode = %q, want installed (reason: %s)", l.Mode, l.Reason)
	}
	if l.DataRoot == filepath.Join(appDir, DataDir) {
		t.Fatal("fell back to the unusable portable root")
	}
	if l.Reason == "" {
		t.Fatal("fallback reason must be recorded, never silent")
	}
}

// TestInstalledLinuxUsesXDGDataHome verifies the XDG contract on Linux.
func TestInstalledLinuxUsesXDGDataHome(t *testing.T) {
	base := t.TempDir()
	xdg := filepath.Join(base, "xdg-data")
	if err := os.MkdirAll(xdg, 0o755); err != nil {
		t.Fatal(err)
	}
	env := testEnv(base, base, map[string]string{"XDG_DATA_HOME": xdg}, filepath.Join(base, "app"))
	env.GOOS = "linux"
	l := Resolve(env)
	if l.Mode != ModeInstalled {
		t.Fatalf("mode = %q (%s)", l.Mode, l.Reason)
	}
	want := filepath.Join(xdg, DirName, "data")
	if l.DataRoot != want {
		t.Fatalf("DataRoot = %q, want %q", l.DataRoot, want)
	}
}

// TestInstalledWindowsPrefersLocalAppData verifies Windows resolves the
// per-device location (LOCALAPPDATA) and NEVER the roaming profile.
func TestInstalledWindowsPrefersLocalAppData(t *testing.T) {
	base := t.TempDir()
	local := filepath.Join(base, "AppData", "Local")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	env := testEnv(base, base, map[string]string{"LOCALAPPDATA": local}, filepath.Join(base, "app.exe"))
	env.GOOS = "windows"
	l := Resolve(env)
	if l.Mode != ModeInstalled {
		t.Fatalf("mode = %q (%s)", l.Mode, l.Reason)
	}
	want := filepath.Join(local, DirName, "data")
	if l.DataRoot != want {
		t.Fatalf("DataRoot = %q, want %q (must not roam)", l.DataRoot, want)
	}
	if filepath.Base(filepath.Dir(filepath.Dir(l.DataRoot))) == "Roaming" {
		t.Fatal("resolved into the roaming profile")
	}
}

// TestDataDirOverrideWins verifies the explicit SHEYTAN_DATA_DIR
// override beats portable markers and platform defaults.
func TestDataDirOverrideWins(t *testing.T) {
	base := t.TempDir()
	appDir := filepath.Join(base, "app")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(appDir, "app")
	if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, MarkerFile), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	over := filepath.Join(base, "ci data dir")
	env := testEnv(base, base, map[string]string{"SHEYTAN_DATA_DIR": over}, exe)
	l := Resolve(env)
	if l.Mode != ModeEnvOverride {
		t.Fatalf("mode = %q, want env-override", l.Mode)
	}
	if l.DataRoot != over {
		t.Fatalf("DataRoot = %q, want %q", l.DataRoot, over)
	}
}

// TestFallbackWhenNothingWritable verifies the temp-dir fallback is
// recorded with its reason instead of failing silently.
func TestFallbackWhenNothingWritable(t *testing.T) {
	base := t.TempDir()
	// No home dir resolution, no XDG, no LOCALAPPDATA, exec fails.
	env := Env{
		LookupEnv: func(string) (string, bool) { return "", false },
		ExecPath:  func() (string, error) { return "", errors.New("no exe") },
		UserHome:  func() (string, error) { return "", errors.New("no home") },
		TempDir:   func() string { return base },
		GOOS:      "linux",
	}
	l := Resolve(env)
	if l.Mode != ModeFallback {
		t.Fatalf("mode = %q, want fallback", l.Mode)
	}
	if l.Reason == "" {
		t.Fatal("fallback must record its reason")
	}
	if l.DataRoot != filepath.Join(base, DirName, "data") {
		t.Fatalf("DataRoot = %q", l.DataRoot)
	}
}

// TestBootstrapCreatesTreeAndVerifiesWrite runs the real bootstrap and
// checks every contract directory exists and accepts writes.
func TestBootstrapCreatesTreeAndVerifiesWrite(t *testing.T) {
	base := t.TempDir()
	appDir := filepath.Join(base, "pkg with space")
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(appDir, "app")
	if err := os.WriteFile(exe, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, MarkerFile), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	l := Resolve(testEnv(base, base, map[string]string{}, exe))
	if err := Bootstrap(l); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	for _, d := range []string{l.Config, l.Logs, l.LogsArch, l.Cache, l.Projects, l.Exports, l.Temp, l.Crashes} {
		fi, err := os.Stat(d)
		if err != nil || !fi.IsDir() {
			t.Fatalf("bootstrap did not create %q (err=%v)", d, err)
		}
	}
	// A probe file must really have been writable: write + read back.
	probe := filepath.Join(l.Logs, "bootstrap-verify.tmp")
	if err := os.WriteFile(probe, []byte("ok"), 0o644); err != nil {
		t.Fatalf("logs dir not writable after bootstrap: %v", err)
	}
	os.Remove(probe)
}

// TestBootstrapReportsUnwritableSubtree verifies Bootstrap surfaces a
// real failure when the logs directory cannot be created, instead of
// silently degrading.
func TestBootstrapReportsUnwritableSubtree(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	// "logs" exists as a regular file -> MkdirAll(logs) must fail.
	if err := os.WriteFile(filepath.Join(root, "logs"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	l := layoutForRoot(root)
	if err := Bootstrap(l); err == nil {
		t.Fatal("Bootstrap must fail when the logs dir cannot be created")
	}
}

// TestLegacyLogDirsDocumentsPreResolverLocations verifies the legacy
// locations used for the non-destructive migration copy.
func TestLegacyLogDirsDocumentsPreResolverLocations(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	env := testEnv(home, base, map[string]string{}, filepath.Join(base, "app"))
	env.GOOS = "linux"
	got := LegacyLogDirs(env)
	if len(got) != 1 {
		t.Fatalf("legacy dirs = %v", got)
	}
	want := filepath.Join(home, ".config", DirName, "logs")
	if got[0] != want {
		t.Fatalf("legacy dir = %q, want %q", got[0], want)
	}
	env.GOOS = "windows"
	env.LookupEnv = func(k string) (string, bool) {
		if k == "APPDATA" {
			return filepath.Join(home, "AppData", "Roaming"), true
		}
		return "", false
	}
	got = LegacyLogDirs(env)
	if len(got) != 1 || got[0] != filepath.Join(home, "AppData", "Roaming", DirName, "logs") {
		t.Fatalf("windows legacy dirs = %v", got)
	}
}
