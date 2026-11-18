package mediatools

// archive_test.go — the malicious-archive attack matrix for the central
// extraction boundary (work order V5 §2.4). Every fixture is a hostile
// archive; extraction must fail (or harmlessly skip) with the sentinel
// file OUTSIDE the destination unchanged and nothing materialized
// outside the destination root. Positive fixtures prove that well-formed
// packs (nested directories, spaces, non-ASCII names, executable bits)
// still extract.

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ulikunitz/xz"
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

// zipEntry is one ordered zip fixture entry.
type zipEntry struct {
	Name    string
	Content string
	Mode    os.FileMode // 0 -> 0644
}

// buildZipOrdered builds a zip with entries in the exact given order
// (collision fixtures depend on order; a map would randomize it).
func buildZipOrdered(t *testing.T, entries []zipEntry) string {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, e := range entries {
		hdr := &zip.FileHeader{Name: e.Name, Method: zip.Deflate}
		mode := os.FileMode(0o644)
		if e.Mode != 0 {
			mode = e.Mode
		}
		hdr.SetMode(mode)
		f, err := w.CreateHeader(hdr)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(e.Content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return writeTemp(t, buf.Bytes(), "pack.zip")
}

// buildZip keeps the map-based convenience of the original fixtures.
func buildZip(t *testing.T, entries map[string]string, modes map[string]os.FileMode) string {
	t.Helper()
	ordered := make([]zipEntry, 0, len(entries))
	for name, content := range entries {
		e := zipEntry{Name: name, Content: content}
		if m, ok := modes[name]; ok {
			e.Mode = m
		}
		ordered = append(ordered, e)
	}
	return buildZipOrdered(t, ordered)
}

func writeTemp(t *testing.T, b []byte, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// tarEntry is one ordered tar fixture entry (shared with xz_test.go).
type tarEntry struct {
	name     string
	content  string
	isDir    bool
	typeflag byte   // overrides isDir when set
	size     int64  // overrides the content-derived size when > 0
	linkname string // symlink/hardlink target
}

// buildTarXZ builds a .tar.xz fixture with entries in the given order.
func buildTarXZ(t *testing.T, entries []tarEntry) string {
	t.Helper()
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	for _, e := range entries {
		typ := e.typeflag
		if typ == 0 {
			if e.isDir {
				typ = tar.TypeDir
			} else {
				typ = tar.TypeReg
			}
		}
		size := e.size
		if size == 0 {
			size = int64(len(e.content))
		}
		hdr := &tar.Header{Name: e.name, Typeflag: typ, Mode: 0o644, Size: size, Linkname: e.linkname}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if typ == tar.TypeReg && len(e.content) > 0 {
			if _, err := tw.Write([]byte(e.content)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	var xzBuf bytes.Buffer
	xw, err := xz.NewWriter(&xzBuf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := xw.Write(tarBuf.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := xw.Close(); err != nil {
		t.Fatal(err)
	}
	return writeTemp(t, xzBuf.Bytes(), "pack.tar.xz")
}

// sentinel creates a file outside the extraction destination that must
// survive every rejected extraction unchanged, plus its directory which
// must not gain any new entries.
func sentinel(t *testing.T) (dir, content string) {
	t.Helper()
	dir = t.TempDir()
	content = "sentinel-must-survive"
	if err := os.WriteFile(filepath.Join(dir, "outside.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, content
}

func assertSentinelIntact(t *testing.T, dir, want string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dir, "outside.txt"))
	if err != nil {
		t.Fatalf("sentinel file removed: %v", err)
	}
	if string(got) != want {
		t.Fatalf("sentinel file modified: %q != %q", got, want)
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 1 {
		names := make([]string, 0, len(ents))
		for _, e := range ents {
			names = append(names, e.Name())
		}
		t.Fatalf("attacker wrote outside the destination: %v", names)
	}
}

// extractZipAt extracts a zip into a fresh dest and returns (dest, result, err).
func extractZipAt(t *testing.T, archivePath string) (string, ExtractResult, error) {
	t.Helper()
	dest := t.TempDir()
	res, err := extractZip(archivePath, ExtractOptions{Pack: testPack(), Dest: dest, Goos: "windows"})
	return dest, res, err
}

func extractTarXZAt(t *testing.T, archivePath string) (string, ExtractResult, error) {
	t.Helper()
	dest := t.TempDir()
	res, err := extractTarXZ(archivePath, ExtractOptions{Pack: testPack(), Dest: dest, Goos: "linux"})
	return dest, res, err
}

func baseEntries() []zipEntry {
	return []zipEntry{
		{Name: "pkg-root/bin/ffmpeg", Content: "FF"},
		{Name: "pkg-root/bin/ffprobe", Content: "FP"},
	}
}

// ------------------------------------------------------------------
// Positive fixtures
// ------------------------------------------------------------------

func TestExtractZipHappyPathSelectsOnlyPackFiles(t *testing.T) {
	path := buildZip(t, map[string]string{
		"pkg-root/bin/ffmpeg":     "FF",
		"pkg-root/bin/ffprobe":    "FP",
		"pkg-root/bin/README.txt": "skip me (not a license/exe)",
		"pkg-root/COPYING.GPLv2":  "license text",
		"unrelated/docs/big.html": strings.Repeat("x", 4096),
	}, nil)
	dest, res, err := extractZipAt(t, path)
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
	// Only the selected files may exist under dest. README.txt counts as
	// a license document under the existing wantsFile contract.
	var found []string
	_ = filepath.Walk(dest, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			found = append(found, p)
		}
		return nil
	})
	if len(found) != 4 { // ffmpeg, ffprobe, COPYING.GPLv2, README.txt
		t.Fatalf("unexpected materialized set: %v", found)
	}
	for _, p := range found {
		if strings.Contains(p, "big.html") {
			t.Fatalf("unrelated entry materialized: %s", p)
		}
	}
}

func TestExtractZipNestedDirsWithSpacesAndNonASCII(t *testing.T) {
	path := buildZipOrdered(t, []zipEntry{
		{Name: "pkg-root/", Content: ""},
		{Name: "pkg-root/bin/", Content: ""},
		{Name: "pkg-root/bin/ffmpeg", Content: "FF"},
		{Name: "pkg-root/bin/ffprobe", Content: "FP"},
		{Name: "pkg-root/bin/my tool.exe", Content: "helper"},
		{Name: "pkg-root/COPYING.ünicode", Content: "license"},
	})
	dest, res, err := extractZipAt(t, path)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"pkg-root/bin/my tool.exe", "pkg-root/COPYING.ünicode"} {
		if _, err := os.Stat(filepath.Join(dest, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("expected %s: %v", rel, err)
		}
	}
	fi, err := os.Stat(res.FFmpeg)
	if err != nil {
		t.Fatal(err)
	}
	// Executable trust (run 44298066730 failure B): on Unix the pinned
	// executable carries 0755 from the archive-safe allowlist and the
	// license carries 0644 — POSIX permission bits are preserved and
	// verified. Windows cannot represent POSIX execute bits (Go
	// reports -rw-rw-rw- for every regular file), so the Windows
	// assertion verifies the PORTABLE trust contract instead: regular
	// file type, exact pinned naming/destination (Unicode and spaces
	// preserved above), exact extracted size, and a regular ffprobe.
	// The test is neither skipped nor weakened: each platform asserts
	// the strongest contract its filesystem can represent.
	if runtime.GOOS == "windows" {
		if !fi.Mode().IsRegular() {
			t.Fatalf("pinned executable is not a regular file on windows: %v", fi.Mode())
		}
		if fi.Size() != 2 { // "FF"
			t.Fatalf("pinned executable size = %d, want 2", fi.Size())
		}
		if fp, ferr := os.Stat(res.FFprobe); ferr != nil {
			t.Fatal(ferr)
		} else if !fp.Mode().IsRegular() {
			t.Fatalf("pinned ffprobe is not a regular file on windows: %v", fp.Mode())
		}
		return
	}
	if fi.Mode().Perm() != 0o755 {
		t.Fatalf("pinned executable mode = %v, want 0755", fi.Mode().Perm())
	}
	fi, err = os.Stat(filepath.Join(dest, "pkg-root", "COPYING.ünicode"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o644 {
		t.Fatalf("license mode = %v, want 0644", fi.Mode().Perm())
	}
}

func TestExtractTarXZSelectsOnlyPackFiles(t *testing.T) {
	path := buildTarXZ(t, []tarEntry{
		{name: "pkg-root/", isDir: true},
		{name: "pkg-root/bin/", isDir: true},
		{name: "pkg-root/bin/ffmpeg", content: "FF"},
		{name: "pkg-root/bin/ffprobe", content: "FP"},
		{name: "pkg-root/COPYING.GPLv2", content: "license"},
		{name: "unrelated/docs/deep/thing.html", content: strings.Repeat("y", 512)},
	})
	dest, res, err := extractTarXZAt(t, path)
	if err != nil {
		t.Fatal(err)
	}
	if res.Files != 3 {
		t.Fatalf("materialized %d files, want 3 (ffmpeg, ffprobe, license)", res.Files)
	}
	if _, err := os.Stat(filepath.Join(dest, "pkg-root", "bin", "ffmpeg")); err != nil {
		t.Fatal(err)
	}
}

func TestExtractZipRejectsMissingPair(t *testing.T) {
	path := buildZip(t, map[string]string{
		"pkg-root/bin/ffmpeg": "FF",
		"unrelated/COPYING":   "L",
	}, nil)
	if _, _, err := extractZipAt(t, path); err == nil {
		t.Fatal("missing ffprobe must be rejected")
	}
}

func TestExtractZipRejectsDuplicateExtractionIntoSameDest(t *testing.T) {
	path := buildZip(t, map[string]string{
		"pkg-root/bin/ffmpeg":  "FF",
		"pkg-root/bin/ffprobe": "FP",
	}, nil)
	dest := t.TempDir()
	opt := ExtractOptions{Pack: testPack(), Dest: dest, Goos: "windows"}
	if _, err := extractZip(path, opt); err != nil {
		t.Fatal(err)
	}
	if _, err := extractZip(path, opt); err == nil {
		t.Fatal("duplicate archive paths must be rejected, not overwritten")
	}
}

// ------------------------------------------------------------------
// Name-validation attack matrix (both formats)
// ------------------------------------------------------------------

func TestValidateEntryName(t *testing.T) {
	bad := []string{
		"", ".", "..", "./", "/abs/evil.exe", "//server/share/x",
		`C:\evil\evil.exe`, `\\server\share\evil.exe`, "C:/evil/evil.exe",
		`back\slash.exe`, "a/b\\c", "../up", "a/../../b", "sub/../../outside.txt",
		"x\x00y", "a\x01b", "a//b", "a/./b", "./a", "a/", "a/./",
		"evil.exe:stream", "CON", "NUL.txt", "com1.bin", "LPT1", "aux",
		"pkg-root/evil.", "pkg-root/evil ", "pkg-root/tra il./x",
		strings.Repeat("a/", 1200) + "end",
	}
	for _, name := range bad {
		if _, _, err := validateEntryName(name, false); err == nil {
			t.Errorf("file name %q must be rejected", name)
		}
	}
	// directory entries: one trailing slash is canonical and stripped
	for _, name := range bad {
		if name == "a/" {
			continue // canonical dir form, tested below
		}
		if _, _, err := validateEntryName(name, true); err == nil {
			t.Errorf("dir name %q must be rejected", name)
		}
	}
	if s, l, err := validateEntryName("pkg-root/bin/ffmpeg", false); err != nil || s != "pkg-root/bin/ffmpeg" || l != filepath.FromSlash("pkg-root/bin/ffmpeg") {
		t.Fatalf("clean name rejected: %v (%q/%q)", err, s, l)
	}
	if s, _, err := validateEntryName("pkg-root/bin/", true); err != nil || s != "pkg-root/bin" {
		t.Fatalf("canonical dir name rejected: %v (%q)", err, s)
	}
}

func TestExtractionRejectsMaliciousNamesBothFormats(t *testing.T) {
	attacks := []string{
		"../outside.txt", "..", "a/..", "./a", "sub/../../outside.txt",
		`..\outside.txt`, `back\slash.exe`, "a/b\\c",
		"/etc/passwd", `\Windows\system32\evil.exe`,
		`C:\evil.exe`, "C:/evil.exe", `\\server\share\evil.exe`,
		"evil.exe:stream", "CON", "NUL", "com1", "LPT1.exe", "aux.bin",
		"x\x00y", "a//b", "evil.", "evil ",
		strings.Repeat("long/", 600) + "name",
	}
	for _, attack := range attacks {
		t.Run("zip/"+strings.ReplaceAll(attack, "/", "_"), func(t *testing.T) {
			sent, want := sentinel(t)
			path := buildZipOrdered(t, append(baseEntries(), zipEntry{Name: attack, Content: "X"}))
			_, _, err := extractZipAt(t, path)
			if err == nil {
				t.Fatalf("zip entry %q must be rejected", attack)
			}
			assertSentinelIntact(t, sent, want)
		})
		if strings.ContainsRune(attack, 0) {
			// Go's tar writer cannot encode NUL into a PAX path record;
			// the name-level rejection is covered by the zip case and by
			// TestValidateEntryName.
			continue
		}
		t.Run("tar.xz/"+strings.ReplaceAll(attack, "/", "_"), func(t *testing.T) {
			sent, want := sentinel(t)
			path := buildTarXZ(t, append(baseTarEntries(), tarEntry{name: attack, content: "X"}))
			_, _, err := extractTarXZAt(t, path)
			if err == nil {
				t.Fatalf("tar entry %q must be rejected", attack)
			}
			assertSentinelIntact(t, sent, want)
		})
	}
}

func baseTarEntries() []tarEntry {
	return []tarEntry{
		{name: "pkg-root/bin/ffmpeg", content: "FF"},
		{name: "pkg-root/bin/ffprobe", content: "FP"},
	}
}

// ------------------------------------------------------------------
// Entry-type attacks
// ------------------------------------------------------------------

func TestExtractZipRejectsSymlinkEntries(t *testing.T) {
	path := buildZip(t, map[string]string{
		"pkg-root/bin/ffmpeg":   "FF",
		"pkg-root/bin/ffprobe":  "FP",
		"pkg-root/bin/link.exe": "target",
	}, map[string]os.FileMode{
		"pkg-root/bin/link.exe": os.ModeSymlink,
	})
	if _, _, err := extractZipAt(t, path); err == nil {
		t.Fatal("symlink entry must be rejected")
	}
}

func TestExtractTarRejectsSpecialEntryTypes(t *testing.T) {
	special := []byte{tar.TypeSymlink, tar.TypeLink, tar.TypeChar, tar.TypeBlock, tar.TypeFifo}
	for _, typ := range special {
		path := buildTarXZ(t, append(baseTarEntries(), tarEntry{
			name: "pkg-root/bin/evil", typeflag: typ, linkname: "../../outside.txt",
		}))
		if _, _, err := extractTarXZAt(t, path); err == nil {
			t.Fatalf("tar entry type %c must be rejected", typ)
		}
	}
}

func TestExtractTarRejectsSymlinkParentFollowedByChild(t *testing.T) {
	// A symlink directory followed by a child entry is the classic
	// escape; the typeflag check refuses the parent outright.
	path := buildTarXZ(t, []tarEntry{
		{name: "pkg-root/", isDir: true},
		{name: "pkg-root/bin/", isDir: true},
		{name: "pkg-root/bin/ffmpeg", content: "FF"},
		{name: "pkg-root/bin/ffprobe", content: "FP"},
		{name: "escape", typeflag: tar.TypeSymlink, linkname: "/tmp"},
		{name: "escape/evil.exe", content: "X"},
	})
	sent, want := sentinel(t)
	if _, _, err := extractTarXZAt(t, path); err == nil {
		t.Fatal("symlink parent must be rejected")
	}
	assertSentinelIntact(t, sent, want)
}

// ------------------------------------------------------------------
// Collision attacks
// ------------------------------------------------------------------

func TestExtractRejectsDuplicateAndCaseCollisions(t *testing.T) {
	cases := []struct {
		name string
		zips []zipEntry
		tars []tarEntry
	}{
		{"exact duplicate", append(baseEntries(), zipEntry{Name: "pkg-root/bin/ffmpeg", Content: "XX"}),
			append(baseTarEntries(), tarEntry{name: "pkg-root/bin/ffmpeg", content: "XX"})},
		{"case-only duplicate", append(baseEntries(), zipEntry{Name: "pkg-root/bin/FFMPEG", Content: "XX"}),
			append(baseTarEntries(), tarEntry{name: "pkg-root/bin/FFprobe", content: "XX"})},
		{"license case collision", append(baseEntries(), zipEntry{Name: "pkg-root/COPYING", Content: "L1"}, zipEntry{Name: "pkg-root/copying", Content: "L2"}),
			append(baseTarEntries(), tarEntry{name: "pkg-root/COPYING", content: "L1"}, tarEntry{name: "pkg-root/copying", content: "L2"})},
	}
	for _, tc := range cases {
		t.Run(tc.name+"/zip", func(t *testing.T) {
			if _, _, err := extractZipAt(t, buildZipOrdered(t, tc.zips)); err == nil {
				t.Fatal("duplicate/case collision must be rejected")
			}
		})
		t.Run(tc.name+"/tar.xz", func(t *testing.T) {
			if _, _, err := extractTarXZAt(t, buildTarXZ(t, tc.tars)); err == nil {
				t.Fatal("duplicate/case collision must be rejected")
			}
		})
	}
}

func TestExtractRejectsFileDirAndParentChildConflicts(t *testing.T) {
	// dir entry then file with the same (wanted) name
	t.Run("dir then file", func(t *testing.T) {
		path := buildZipOrdered(t, []zipEntry{
			{Name: "pkg-root/bin/ffmpeg", Content: "FF"},
			{Name: "pkg-root/bin/ffprobe", Content: "FP"},
			{Name: "pkg-root/COPYING/", Content: ""},
			{Name: "pkg-root/COPYING", Content: "L"},
		})
		if _, _, err := extractZipAt(t, path); err == nil {
			t.Fatal("directory/file collision must be rejected")
		}
	})
	// file then dir entry with the same (wanted) name
	t.Run("file then dir", func(t *testing.T) {
		path := buildZipOrdered(t, []zipEntry{
			{Name: "pkg-root/bin/ffmpeg", Content: "FF"},
			{Name: "pkg-root/bin/ffprobe", Content: "FP"},
			{Name: "pkg-root/COPYING", Content: "L"},
			{Name: "pkg-root/COPYING/", Content: ""},
		})
		if _, _, err := extractZipAt(t, path); err == nil {
			t.Fatal("file/directory collision must be rejected")
		}
	})
	// file "a" then file "a/b" (both wanted, under bin/)
	t.Run("file then child", func(t *testing.T) {
		path := buildZipOrdered(t, []zipEntry{
			{Name: "pkg-root/bin/ffmpeg/x", Content: "X"},
			{Name: "pkg-root/bin/ffmpeg", Content: "FF"},
			{Name: "pkg-root/bin/ffprobe", Content: "FP"},
		})
		if _, _, err := extractZipAt(t, path); err == nil {
			t.Fatal("parent/child conflict must be rejected")
		}
	})
	// file "a/b" then file "a"
	t.Run("child then file", func(t *testing.T) {
		path := buildZipOrdered(t, []zipEntry{
			{Name: "pkg-root/bin/ffmpeg", Content: "FF"},
			{Name: "pkg-root/bin/ffprobe", Content: "FP"},
			{Name: "pkg-root/bin/ffmpeg/x", Content: "X"},
		})
		if _, _, err := extractZipAt(t, path); err == nil {
			t.Fatal("parent/child conflict must be rejected")
		}
	})
}

// ------------------------------------------------------------------
// Resource-exhaustion attacks
// ------------------------------------------------------------------

func TestExtractionBoundsContract(t *testing.T) {
	if maxEntryCount != 50000 || maxEntryBytes != 512<<20 || maxTotalBytes != 3<<30 {
		t.Fatalf("extraction bounds changed: count=%d entry=%d total=%d", maxEntryCount, maxEntryBytes, maxTotalBytes)
	}
}

func TestExtractZipRejectsExcessiveEntryCount(t *testing.T) {
	entries := make([]zipEntry, 0, maxEntryCount+2)
	entries = append(entries, zipEntry{Name: "pkg-root/bin/ffmpeg", Content: "FF"})
	entries = append(entries, zipEntry{Name: "pkg-root/bin/ffprobe", Content: "FP"})
	for i := 0; i < maxEntryCount; i++ {
		entries = append(entries, zipEntry{Name: fmt.Sprintf("unrelated/filler-%d.txt", i), Content: "x"})
	}
	path := buildZipOrdered(t, entries)
	if _, _, err := extractZipAt(t, path); err == nil {
		t.Fatal("excessive entry count must be rejected")
	}
}

func TestExtractTarRejectsHugeDeclaredSize(t *testing.T) {
	// Deliberately malformed archive: a regular-file header declaring a
	// size beyond the per-entry bound with a truncated body. The bound
	// check must fire on the header, before any body is read.
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	if err := tw.WriteHeader(&tar.Header{
		Name: "pkg-root/bin/huge", Typeflag: tar.TypeReg, Mode: 0o644, Size: maxEntryBytes + 1,
	}); err != nil {
		t.Fatal(err)
	}
	// no body, no tar Close: the truncation itself is part of the attack
	var xzBuf bytes.Buffer
	xw, err := xz.NewWriter(&xzBuf)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := xw.Write(tarBuf.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := xw.Close(); err != nil {
		t.Fatal(err)
	}
	path := writeTemp(t, xzBuf.Bytes(), "huge.tar.xz")
	if _, _, err := extractTarXZAt(t, path); err == nil {
		t.Fatal("declared size beyond the per-entry bound must be rejected")
	}
}

func TestWriteFileEnforcesByteBound(t *testing.T) {
	dest := t.TempDir()
	sess, err := newExtractSession(dest)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.close()
	if _, err := sess.writeFile("pkg-root/bin/ffmpeg", strings.NewReader("0123456789"), 8, true); err == nil {
		t.Fatal("per-file byte bound must be enforced during the copy")
	}
	if _, err := os.Stat(filepath.Join(dest, "pkg-root", "bin", "ffmpeg")); err != nil {
		t.Fatalf("partial file should exist in the discarded staging tree: %v", err)
	}
}

// ------------------------------------------------------------------
// Integrity attacks
// ------------------------------------------------------------------

func TestExtractZipRejectsBadCRC(t *testing.T) {
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, e := range baseEntries() {
		f, _ := w.CreateHeader(&zip.FileHeader{Name: e.Name, Method: zip.Deflate})
		f.Write([]byte(e.Content))
	}
	license, _ := w.CreateHeader(&zip.FileHeader{Name: "pkg-root/COPYING", Method: zip.Deflate})
	license.Write([]byte("license text"))
	w.Close()
	raw := buf.Bytes()
	// Corrupt the central-directory CRC of the license entry: the
	// reader verifies the decompressed stream against it on read.
	sig := []byte("PK\x01\x02")
	at := bytes.LastIndex(raw, sig) // last central record = the license
	if at < 0 {
		t.Fatal("central directory signature not found")
	}
	raw[at+16] ^= 0xff
	raw[at+17] ^= 0xff
	path := writeTemp(t, raw, "badcrc.zip")
	if _, _, err := extractZipAt(t, path); err == nil {
		t.Fatal("CRC mismatch must be rejected")
	}
}

func TestExtractZipRejectsTruncatedArchive(t *testing.T) {
	path := buildZip(t, map[string]string{
		"pkg-root/bin/ffmpeg":  "FF",
		"pkg-root/bin/ffprobe": "FP",
		"pkg-root/COPYING":     strings.Repeat("L", 4096),
	}, nil)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := extractZipAt(t, writeTemp(t, raw[:len(raw)*2/3], "trunc.zip")); err == nil {
		t.Fatal("truncated zip must be rejected")
	}
}

func TestExtractTarRejectsTruncatedArchive(t *testing.T) {
	path := buildTarXZ(t, []tarEntry{
		{name: "pkg-root/bin/ffmpeg", content: strings.Repeat("F", 4096)},
		{name: "pkg-root/bin/ffprobe", content: strings.Repeat("P", 4096)},
		{name: "pkg-root/COPYING", content: strings.Repeat("L", 8192)},
	})
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := extractTarXZAt(t, writeTemp(t, raw[:len(raw)/2], "trunc.tar.xz")); err == nil {
		t.Fatal("truncated tar.xz must be rejected")
	}
}

// ------------------------------------------------------------------
// Destination-boundary attacks
// ------------------------------------------------------------------

func TestExtractCannotEscapeThroughPreexistingSymlink(t *testing.T) {
	outside, want := sentinel(t)
	dest := t.TempDir()
	// A hostile pre-existing symlink INSIDE the destination pointing
	// outside it. Production destinations are fresh staging dirs, but
	// the boundary must hold even when this invariant is broken.
	if err := os.Symlink(outside, filepath.Join(dest, "pkg-root")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	path := buildZip(t, map[string]string{
		"pkg-root/bin/ffmpeg":  "FF",
		"pkg-root/bin/ffprobe": "FP",
	}, nil)
	_, err := extractZip(path, ExtractOptions{Pack: testPack(), Dest: dest, Goos: "windows"})
	if err == nil {
		t.Fatal("extraction through an outside-pointing symlink must fail")
	}
	assertSentinelIntact(t, outside, want)
}

func TestExtractRejectsSymlinkedDestination(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	path := buildZip(t, map[string]string{
		"pkg-root/bin/ffmpeg":  "FF",
		"pkg-root/bin/ffprobe": "FP",
	}, nil)
	if _, err := extractZip(path, ExtractOptions{Pack: testPack(), Dest: link, Goos: "windows"}); err == nil {
		t.Fatal("a symlinked destination must be refused")
	}
}
