package mediatools

// verify.go — verification of an extracted pack BEFORE it can become
// the active app-managed installation (run 43712071111): identity
// (ffmpeg -version prefix), license configuration (ffmpeg -buildconf
// must/must-not contain the manifest's markers), encoder inventory
// (ffmpeg -encoders; every required encoder must be present), the
// ffprobe pair, and a tiny real encode+probe smoke test. Processes run
// with explicit argv, a sanitized environment, bounded output and a
// timeout — never a shell, never unbounded capture.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/procattr"
)

// verifyTimeout bounds each verification subprocess.
const verifyTimeout = 60 * time.Second

// VerifyResult reports the verified capabilities of a pack.
type VerifyResult struct {
	Version  string
	Encoders []string // sorted encoder names found
}

// verifyPack verifies an extracted pack directory (bin/ with the pair).
func verifyPack(pack PlatformPack, binDir string) (VerifyResult, error) {
	ff, fp := BinNames(pack.platformGOOS())
	ffPath := filepath.Join(binDir, ff)
	fpPath := filepath.Join(binDir, fp)
	if !isRegular(ffPath) || !isRegular(fpPath) {
		return VerifyResult{}, fmt.Errorf("mediatools: verification: missing pair in %s", binDir)
	}

	// 1) identity
	version, err := runCapture(ffPath, []string{"-version"})
	if err != nil {
		return VerifyResult{}, fmt.Errorf("mediatools: ffmpeg -version: %w", err)
	}
	wantPrefix := "ffmpeg version " + pack.Version
	if !strings.HasPrefix(version, wantPrefix) {
		return VerifyResult{}, fmt.Errorf("mediatools: ffmpeg -version %q does not match pinned prefix %q", version, wantPrefix)
	}

	// 2) license configuration
	buildconf, err := runCapture(ffPath, []string{"-hide_banner", "-buildconf"})
	if err != nil {
		return VerifyResult{}, fmt.Errorf("mediatools: ffmpeg -buildconf: %w", err)
	}
	for _, must := range pack.LicenseVerification.MustContain {
		if !strings.Contains(buildconf, must) {
			return VerifyResult{}, fmt.Errorf("mediatools: ffmpeg -buildconf missing %q (license verification failed)", must)
		}
	}
	for _, mustNot := range pack.LicenseVerification.MustNotContain {
		if strings.Contains(buildconf, mustNot) {
			return VerifyResult{}, fmt.Errorf("mediatools: ffmpeg -buildconf contains forbidden %q (license verification failed)", mustNot)
		}
	}

	// 3) encoders
	encOut, err := runCapture(ffPath, []string{"-hide_banner", "-encoders"})
	if err != nil {
		return VerifyResult{}, fmt.Errorf("mediatools: ffmpeg -encoders: %w", err)
	}
	encoders := map[string]bool{}
	for _, name := range append(append([]string(nil), pack.RequiredEncoders...), pack.OptionalEncoders...) {
		if encoderListed(encOut, name) {
			encoders[name] = true
		}
	}
	for _, req := range pack.RequiredEncoders {
		if !encoders[req] {
			return VerifyResult{}, fmt.Errorf("mediatools: pinned pack is missing required encoder %q", req)
		}
	}

	// 4) ffprobe pair
	if _, err := runCapture(fpPath, []string{"-version"}); err != nil {
		return VerifyResult{}, fmt.Errorf("mediatools: ffprobe -version: %w", err)
	}

	// 5) required shared libraries (empty for static builds)
	for _, lib := range pack.RequiredSharedLibraries {
		if !isRegular(filepath.Join(binDir, lib)) {
			return VerifyResult{}, fmt.Errorf("mediatools: required shared library %q missing beside the executables", lib)
		}
	}

	// 6) tiny self-contained encode + probe smoke test
	if err := smokeEncodeProbe(ffPath, fpPath); err != nil {
		return VerifyResult{}, fmt.Errorf("mediatools: smoke encode/probe: %w", err)
	}

	return VerifyResult{Version: version, Encoders: sortedEncoders(encoders)}, nil
}

// smokeEncodeProbe encodes a fraction of a second of synthetic video
// + audio with the pack's own libx264/aac and probes the result with
// the pack's own ffprobe — the pack proves itself end to end.
func smokeEncodeProbe(ffPath, fpPath string) error {
	tmp, err := os.MkdirTemp("", "mediatools-smoke-*")
	if err != nil {
		return fmt.Errorf("temp dir: %w", err)
	}
	defer os.RemoveAll(tmp)
	out := filepath.Join(tmp, "smoke.mp4")
	args := []string{
		"-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=red:s=256x256:r=30:d=0.4",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=0.4",
		"-c:v", "libx264", "-preset", "ultrafast", "-t", "0.4",
		"-c:a", "aac", "-y", out,
	}
	if _, err := runCapture(ffPath, args); err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	probe, err := runCapture(fpPath, []string{"-v", "error", "-print_format", "csv", "-show_entries",
		"stream=codec_name,codec_type", out})
	if err != nil {
		return fmt.Errorf("probe: %w", err)
	}
	has := func(name, typ string) bool {
		// CSV rows look like: stream,h264,video / stream,aac,audio
		for _, line := range strings.Split(probe, "\n") {
			parts := strings.Split(strings.TrimSpace(line), ",")
			if len(parts) >= 3 && parts[0] == "stream" && parts[1] == name && parts[2] == typ {
				return true
			}
		}
		return false
	}
	if !has("h264", "video") {
		return fmt.Errorf("probe result has no h264 video stream: %q", probe)
	}
	if !has("aac", "audio") {
		return fmt.Errorf("probe result has no aac audio stream: %q", probe)
	}
	return nil
}

// platformGOOS is the pack's target OS.
func (p PlatformPack) platformGOOS() string {
	if i := strings.IndexByte(p.Platform, '/'); i > 0 {
		return p.Platform[:i]
	}
	return runtime.GOOS
}

// runCapture runs one bounded verification subprocess.
func runCapture(bin string, args []string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), verifyTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	procattr.Configure(cmd) // never a visible child console on Windows
	cmd.Env = sanitizedEnv()
	var out, errb bytes.Buffer
	limited := &limitWriter{w: &out, n: 256 << 10}
	cmd.Stdout = limited
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(errb.String())
		if len(detail) > 256 {
			detail = detail[:256]
		}
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("%s timed out after %s", bin, verifyTimeout)
		}
		if detail != "" {
			return "", fmt.Errorf("%v: %s", err, detail)
		}
		return "", err
	}
	return out.String(), nil
}

// sanitizedEnv is the minimal child environment for verification runs.
func sanitizedEnv() []string {
	if runtime.GOOS == "windows" {
		return []string{"SYSTEMROOT=" + os.Getenv("SYSTEMROOT"), "TEMP=" + os.Getenv("TEMP"), "TMP=" + os.Getenv("TMP")}
	}
	return []string{"LC_ALL=C", "LANG=C"}
}

// limitWriter bounds captured subprocess output.
type limitWriter struct {
	w *bytes.Buffer
	n int
}

func (l *limitWriter) Write(p []byte) (int, error) {
	if l.n <= 0 {
		return len(p), nil // drop the rest silently (bounded capture)
	}
	if len(p) > l.n {
		p = p[:l.n]
	}
	n, err := l.w.Write(p)
	l.n -= n
	return n, err
}

// encoderListed matches an encoder name in `ffmpeg -encoders` output.
func encoderListed(out, name string) bool {
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		// Lines look like: " V....D libx264  libx264 H.264 ..."
		for i, f := range fields {
			if f == name && i > 0 {
				return true
			}
		}
	}
	return false
}
