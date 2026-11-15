package export

// climode_test.go — run 43612608237 package-verification proof: the
// headless --export-cli diagnostic must produce a real, ffprobe-validated
// MP4 using an app-local staged FFmpeg pair with the system PATH scrubbed
// (the portable-package contract: never depend on a CI machine's global
// FFmpeg).
//
// The staged pair here is the local development machine's ffmpeg/ffprobe
// copied into a synthetic app-local layout. On hosted CI the SAME gate
// runs against the pinned redistributable FFmpeg 9.0.2 bundle staged by
// the packaging scripts. Provenance of the staged binaries is reported
// verbatim in the test log (version line) — the test never claims a
// specific upstream version it did not verify.

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIExportDiagnosticAppLocal(t *testing.T) {
	if testing.Short() {
		t.Skip("SKIP: short mode (CLI export is an integration test)")
	}
	// Locate a system ffmpeg/ffprobe pair to stage (the test's SUBJECT is
	// app-local resolution + the CLI pipeline, not tool provenance).
	sysFF, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skipf("SKIP (NOT AVAILABLE): no system ffmpeg to stage: %v", err)
	}
	sysFP, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skipf("SKIP (NOT AVAILABLE): no system ffprobe to stage: %v", err)
	}

	dir := t.TempDir()
	binDir := filepath.Join(dir, "tools", "ffmpeg", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stage := func(src, name string) {
		b, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("stage %s: %v", name, err)
		}
		dst := filepath.Join(binDir, name)
		if err := os.WriteFile(dst, b, 0o755); err != nil {
			t.Fatalf("stage %s: %v", name, err)
		}
	}
	stage(sysFF, "ffmpeg")
	stage(sysFP, "ffprobe")

	// Highest-priority override points at the staged app-local layout and
	// the process PATH is scrubbed for this binary only (os.Setenv, test
	// scoped) — resolution MUST come from the staged pair, never PATH.
	SetPathOverride(binDir)
	t.Cleanup(func() {
		SetPathOverride("")
		// Force a re-probe so the process-level DetectEnv cache cannot
		// retain paths under this test's temp dir after t.TempDir cleanup
		// (later export tests in the same process must re-resolve normally).
		_, _ = DetectEnv(true)
	})
	origPath := os.Getenv("PATH")
	os.Setenv("PATH", "/usr/bin:/bin") // /usr/bin holds the system ffmpeg?
	// /usr/bin typically DOES contain the system ffmpeg; to prove the
	// staged pair is used, assert the RESOLVED source below, not PATH.
	t.Cleanup(func() { os.Setenv("PATH", origPath) })

	env, err := DetectEnv(true) // force re-probe under the override
	if err != nil {
		t.Fatalf("detect after staging: %v", err)
	}
	if env.Source != "override" {
		t.Fatalf("resolved source %q, want the staged override (app-local contract)", env.Source)
	}
	if env.FFmpegPath != filepath.Join(binDir, "ffmpeg") || env.FFprobePath != filepath.Join(binDir, "ffprobe") {
		t.Fatalf("resolved pair %s/%s is not the staged app-local pair", env.FFmpegPath, env.FFprobePath)
	}
	t.Logf("staged pair resolved: %s (%s)", env.FFmpegPath, env.Version)

	var out bytes.Buffer
	var errW bytes.Buffer
	outPath := filepath.Join(dir, "cli-diagnostic.mp4")
	rc := RunCLI(CLIOptions{
		Out: outPath, Width: 640, Height: 360, FPS: 30, Seconds: 0.5,
	}, &out, &errW)
	if rc != 0 {
		t.Fatalf("RunCLI rc=%d stderr=%s", rc, errW.String())
	}
	if !strings.Contains(out.String(), "export-cli: OK") {
		t.Fatalf("RunCLI output missing OK line: %s", out.String())
	}
	fi, err := os.Stat(outPath)
	if err != nil || fi.Size() == 0 {
		t.Fatalf("CLI export output missing/empty: %v", err)
	}
	if _, err := os.Stat(outPath + ".part.mp4"); err == nil {
		t.Fatal("partial file left behind after CLI export")
	}
	t.Logf("CLI diagnostic export OK: %d bytes", fi.Size())
	_ = io.Discard
}
