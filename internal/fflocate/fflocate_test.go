package fflocate

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func writeBin(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#bin"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func testOpts(appLocal string, lookup func(string) (string, error)) Options {
	return Options{
		Override: DirOverride(),
		AppLocal: appLocal,
		Lookup:   lookup,
		GOOS:     runtime.GOOS,
	}
}

// TestOrderAppLocalBeatsPath verifies resolution order rule 2.
func TestOrderAppLocalBeatsPath(t *testing.T) {
	base := t.TempDir()
	appLocal := filepath.Join(base, "tools", "ffmpeg", "bin")
	if err := os.MkdirAll(appLocal, 0o755); err != nil {
		t.Fatal(err)
	}
	writeBin(t, appLocal, "ffmpeg")
	writeBin(t, appLocal, "ffprobe")
	looked := 0
	r, err := Locate(testOpts(appLocal, func(string) (string, error) {
		looked++
		return "", errors.New("not on PATH")
	}))
	if err != nil {
		t.Fatalf("Locate: %v", err)
	}
	if r.Source != "app-local" {
		t.Fatalf("source = %q, want app-local", r.Source)
	}
	if r.FFmpeg != filepath.Join(appLocal, "ffmpeg") || r.FFprobe != filepath.Join(appLocal, "ffprobe") {
		t.Fatalf("resolved pair = %q/%q", r.FFmpeg, r.FFprobe)
	}
	if looked != 0 {
		t.Fatal("PATH must not be consulted when app-local resolves")
	}
}

// TestOverrideHighestPriorityAndStrict verifies rule 1 and that an
// incomplete override pair is a reported error, never a silent
// fallback.
func TestOverrideHighestPriorityAndStrict(t *testing.T) {
	base := t.TempDir()
	over := filepath.Join(base, "myffmpeg")
	if err := os.MkdirAll(over, 0o755); err != nil {
		t.Fatal(err)
	}
	writeBin(t, over, "ffmpeg")
	writeBin(t, over, "ffprobe")
	o := testOpts("", nil)
	o.Override = over
	r, err := Locate(o)
	if err != nil || r.Source != "override" {
		t.Fatalf("override not honored: %+v err=%v", r, err)
	}
	// Incomplete override: ffprobe missing -> explicit error.
	if err := os.Remove(filepath.Join(over, "ffprobe")); err != nil {
		t.Fatal(err)
	}
	if _, err := Locate(o); err == nil {
		t.Fatal("incomplete override pair must be an explicit error")
	}
}

// TestPathFallback verifies rule 3.
func TestPathFallback(t *testing.T) {
	o := testOpts("", func(name string) (string, error) {
		return "/usr/bin/" + name, nil
	})
	r, err := Locate(o)
	if err != nil || r.Source != "path" {
		t.Fatalf("path fallback failed: %+v err=%v", r, err)
	}
}

// TestNothingFoundIsActionable verifies the not-found error explains
// every searched location (user-facing actionability).
func TestNothingFoundIsActionable(t *testing.T) {
	o := testOpts("", func(string) (string, error) {
		return "", errors.New("not found")
	})
	_, err := Locate(o)
	if err == nil {
		t.Fatal("expected error")
	}
	for _, want := range []string{"tools/ffmpeg/bin", "PATH"} {
		if !contains(err.Error(), want) {
			t.Fatalf("error %q does not mention %q", err.Error(), want)
		}
	}
}

// TestWindowsBinarySuffix documents the .exe contract.
func TestWindowsBinarySuffix(t *testing.T) {
	if got := binName("ffmpeg", "windows"); got != "ffmpeg.exe" {
		t.Fatalf("windows name = %q", got)
	}
	if got := binName("ffmpeg", "linux"); got != "ffmpeg" {
		t.Fatalf("linux name = %q", got)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
