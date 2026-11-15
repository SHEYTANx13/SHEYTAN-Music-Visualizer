package fflocate

// appmanaged_test.go — the app-managed resolution rule (run
// 43712071111): user override > app-managed download dir >
// app-local portable dir > system PATH.

import (
	"os"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/apppaths"
	"path/filepath"
	"runtime"
	"testing"
)

// writePair creates an ffmpeg/ffprobe pair inside dir.
func writePair(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{binName("ffmpeg", runtime.GOOS), binName("ffprobe", runtime.GOOS)} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAppManagedResolutionOrder(t *testing.T) {
	base := t.TempDir()
	appLocal := filepath.Join(base, "app", "tools", "ffmpeg", "bin")
	appManaged := filepath.Join(base, "data", "media-tools", "current", "bin")
	writePair(t, appLocal)
	writePair(t, appManaged)

	// App-managed wins over app-local.
	r, err := Locate(Options{AppManaged: appManaged, AppLocal: appLocal, GOOS: runtime.GOOS})
	if err != nil {
		t.Fatal(err)
	}
	if r.Source != "app-managed" {
		t.Fatalf("source = %s, want app-managed", r.Source)
	}

	// App-local used when app-managed is absent.
	r, err = Locate(Options{AppLocal: appLocal, GOOS: runtime.GOOS})
	if err != nil || r.Source != "app-local" {
		t.Fatalf("app-local fallback: %+v, %v", r, err)
	}

	// Missing app-managed dir does not block later rules.
	r, err = Locate(Options{AppManaged: filepath.Join(base, "missing"), AppLocal: appLocal, GOOS: runtime.GOOS})
	if err != nil || r.Source != "app-local" {
		t.Fatalf("missing app-managed must fall through: %+v, %v", r, err)
	}

	// Override still wins over everything.
	override := filepath.Join(base, "override")
	writePair(t, override)
	r, err = Locate(Options{Override: override, AppManaged: appManaged, AppLocal: appLocal, GOOS: runtime.GOOS})
	if err != nil || r.Source != "override" {
		t.Fatalf("override priority: %+v, %v", r, err)
	}
}

func TestLayoutMediaToolsBin(t *testing.T) {
	// apppaths.MediaToolsBin contract: <DataRoot>/media-tools/current/bin.
	lay := apppaths.Resolve(apppaths.Env{
		LookupEnv: func(string) (string, bool) { return "", false },
		ExecPath:  func() (string, error) { return filepath.Join(t.TempDir(), "app", "bin", "app.exe"), nil },
		UserHome:  func() (string, error) { return filepath.Join(t.TempDir(), "home"), nil },
		TempDir:   func() string { return t.TempDir() },
		GOOS:      runtime.GOOS,
	})
	if lay.DataRoot == "" {
		t.Skip("no portable layout in this environment")
	}
	got := lay.MediaToolsBin()
	want := filepath.Join(lay.DataRoot, "media-tools", "current", "bin")
	if got != want {
		t.Fatalf("MediaToolsBin = %s, want %s", got, want)
	}
}
