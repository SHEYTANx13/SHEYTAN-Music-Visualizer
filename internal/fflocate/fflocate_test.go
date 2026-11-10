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
	// OS-aware fixture names (run 42598929646 Windows repair): the
	// locator resolves the platform binary name via binName — on
	// Windows that is ffmpeg.exe/ffprobe.exe — so fixtures MUST create
	// the suffixed names on Windows. Extensionless fixtures made both
	// Windows tests fail there even though the locator is correct; on
	// Linux the extensionless names remain correct.
	if err := os.WriteFile(filepath.Join(dir, binName(name, runtime.GOOS)), []byte("#bin"), 0o755); err != nil {
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
	if r.FFmpeg != filepath.Join(appLocal, binName("ffmpeg", runtime.GOOS)) || r.FFprobe != filepath.Join(appLocal, binName("ffprobe", runtime.GOOS)) {
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
	if err := os.Remove(filepath.Join(over, binName("ffprobe", runtime.GOOS))); err != nil {
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

// TestWindowsPairRequiresExeSuffix proves the Windows locator contract
// on ANY host: GOOS is an injected Options field, not a syscall, so the
// exact run-42598929646 Windows behavior is reproducible here. With
// GOOS=windows a directory holding only extensionless binaries must
// NOT satisfy the pair, a directory holding ffmpeg.exe+ffprobe.exe
// must, and an app-local .exe pair must outrank PATH (the PATH lookup
// must never be consulted when app-local resolves).
func TestWindowsPairRequiresExeSuffix(t *testing.T) {
	base := t.TempDir()
	noExe := filepath.Join(base, "extensionless")
	if err := os.MkdirAll(noExe, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"ffmpeg", "ffprobe"} {
		if err := os.WriteFile(filepath.Join(noExe, n), []byte("#bin"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	r, err := Locate(Options{AppLocal: noExe, GOOS: "windows", Lookup: func(string) (string, error) {
		return "", errors.New("not on PATH")
	}})
	if err == nil {
		t.Fatalf("extensionless fixtures must NOT satisfy the windows pair; got %+v", r)
	}

	withExe := filepath.Join(base, "with-exe")
	if err := os.MkdirAll(withExe, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"ffmpeg.exe", "ffprobe.exe"} {
		if err := os.WriteFile(filepath.Join(withExe, n), []byte("#bin"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	looked := 0
	r, err = Locate(Options{AppLocal: withExe, GOOS: "windows", Lookup: func(string) (string, error) {
		looked++
		return "", errors.New("not on PATH")
	}})
	if err != nil {
		t.Fatalf(".exe pair must satisfy the windows pair: %v", err)
	}
	if r.Source != "app-local" || r.FFmpeg != filepath.Join(withExe, "ffmpeg.exe") || r.FFprobe != filepath.Join(withExe, "ffprobe.exe") {
		t.Fatalf("windows app-local pair wrong: %+v", r)
	}
	if looked != 0 {
		t.Fatal("PATH must not be consulted when the windows app-local pair resolves")
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
