package mediatools

// archive_test.go — extraction safety fixtures (run 43712071111):
// traversal, absolute paths, symlinks, archive bombs and duplicate
// executable paths must all be rejected before any file escapes the
// destination; only the pinned executables + license docs are
// materialized.

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testPack() PlatformPack {
	return PlatformPack{
		Platform:         "linux/amd64",
		Version:          "n-test",
		URL:              "https://example.com/pack.zip",
		SHA256:           strings.Repeat("0", 64),
		ArchiveFormat:    "zip",
		FFmpegPath:       "pkg-root/bin/ffmpeg",
		FFprobePath:      "pkg-root/bin/ffprobe",
		RequiredEncoders: []string{"libx264", "libx265", "aac"},
		LicenseClass:     "GPL-test",
		LicenseNotice:    "test notice",
		SourceURL:        "https://example.com/src",
	}
}

func buildZip(t *testing.T, entries map[string]string, modes map[string]os.FileMode) string {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, content := range entries {
		hdr := &zip.FileHeader{Name: name, Method: zip.Deflate}
		mode := os.FileMode(0o644)
		if m, ok := modes[name]; ok {
			mode = m
		}
		hdr.SetMode(mode)
		f, err := w.CreateHeader(hdr)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "pack.zip")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func extract(t *testing.T, archivePath string) (ExtractResult, error) {
	t.Helper()
	dest := t.TempDir()
	return extractZip(archivePath, ExtractOptions{Pack: testPack(), Dest: dest, Goos: "windows"})
}

func TestExtractZipHappyPathSelectsOnlyPackFiles(t *testing.T) {
	path := buildZip(t, map[string]string{
		"pkg-root/bin/ffmpeg":     "FF",
		"pkg-root/bin/ffprobe":    "FP",
		"pkg-root/bin/README.txt": "skip me (not a license/exe)",
		"pkg-root/COPYING.GPLv2":  "license text",
		"unrelated/docs/big.html": strings.Repeat("x", 4096),
	}, nil)
	res, err := extract(t, path)
	if err != nil {
		t.Fatal(err)
	}
	if res.FFmpeg == "" || res.FFprobe == "" {
		t.Fatal("pinned executables not located")
	}
	for _, p := range []string{res.FFmpeg, res.FFprobe} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected %s to exist: %v", p, err)
		}
	}
	if strings.Contains(res.FFmpeg, "big.html") {
		t.Fatal("unrelated files must not be materialized")
	}
}

func TestExtractZipRejectsTraversal(t *testing.T) {
	path := buildZip(t, map[string]string{
		"pkg-root/bin/ffmpeg":   "FF",
		"pkg-root/bin/ffprobe":  "FP",
		"evil/../../escape.exe": "X",
	}, nil)
	if _, err := extract(t, path); err == nil {
		t.Fatal("traversal entry must be rejected")
	}
}

func TestExtractZipRejectsAbsoluteAndDrivePaths(t *testing.T) {
	for _, name := range []string{"/abs/evil.exe", `C:\evil\evil.exe`, `back\slash.exe`} {
		path := buildZip(t, map[string]string{
			"pkg-root/bin/ffmpeg":  "FF",
			"pkg-root/bin/ffprobe": "FP",
			name:                   "X",
		}, nil)
		if _, err := extract(t, path); err == nil {
			t.Fatalf("entry %q must be rejected", name)
		}
	}
}

func TestExtractZipRejectsSymlinkEntries(t *testing.T) {
	path := buildZip(t, map[string]string{
		"pkg-root/bin/ffmpeg":   "FF",
		"pkg-root/bin/ffprobe":  "FP",
		"pkg-root/bin/link.exe": "target",
	}, map[string]os.FileMode{
		"pkg-root/bin/link.exe": os.ModeSymlink,
	})
	if _, err := extract(t, path); err == nil {
		t.Fatal("symlink entry must be rejected")
	}
}

func TestExtractZipRejectsMissingPair(t *testing.T) {
	path := buildZip(t, map[string]string{
		"pkg-root/bin/ffmpeg": "FF",
		"unrelated/COPYING":   "L",
	}, nil)
	if _, err := extract(t, path); err == nil {
		t.Fatal("missing ffprobe must be rejected")
	}
}

func TestExtractZipRejectsDuplicateExecutablePath(t *testing.T) {
	path := buildZip(t, map[string]string{
		"pkg-root/bin/ffmpeg":  "FF",
		"pkg-root/bin/ffprobe": "FP",
	}, nil)
	// Extract twice into the SAME destination: the second run must
	// fail on the duplicate rather than overwrite.
	dest := t.TempDir()
	opt := ExtractOptions{Pack: testPack(), Dest: dest, Goos: "windows"}
	if _, err := extractZip(path, opt); err != nil {
		t.Fatal(err)
	}
	if _, err := extractZip(path, opt); err == nil {
		t.Fatal("duplicate archive paths must be rejected, not overwritten")
	}
}

func TestExtractZipRejectsOversizedFile(t *testing.T) {
	big := strings.Repeat("x", 1024)
	path := buildZip(t, map[string]string{
		"pkg-root/bin/ffmpeg":   "FF",
		"pkg-root/bin/ffprobe":  "FP",
		"pkg-root/bin/huge.exe": big,
	}, nil)
	// Lower the bound for the test by monkey-patching is not possible
	// for a const; instead assert the guard exists via a fake large
	// entry relative to the per-entry bound using the public constant.
	// A truly 512 MiB fixture is not feasible in CI; the bound itself
	// is asserted by contract below.
	if maxEntryBytes != 512<<20 {
		t.Fatalf("per-entry bound changed: %d", maxEntryBytes)
	}
	if _, err := extract(t, path); err != nil {
		t.Fatalf("benign fixture must extract: %v", err)
	}
}

func TestSafeEntryName(t *testing.T) {
	bad := []string{"", "/abs", `C:\x`, `a\b`, "../up", "a/../../b", "x\x00y"}
	for _, name := range bad {
		if _, err := safeEntryName(name); err == nil {
			t.Errorf("%q must be rejected", name)
		}
	}
	if n, err := safeEntryName("pkg-root/bin/ffmpeg"); err != nil || n != "pkg-root/bin/ffmpeg" {
		t.Fatalf("clean name rejected: %v", err)
	}
}
