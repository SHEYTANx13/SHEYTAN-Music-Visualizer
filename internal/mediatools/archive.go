package mediatools

// archive.go — bounded, traversal-safe extraction of the pinned media
// tool archives (run 43712071111). Every entry is validated BEFORE any
// file is created: no absolute paths, no "..", no drive prefixes, no
// backslash separators, no symlinks/hardlinks, no device/fifo entries,
// no overwriting duplicates. Extraction is bounded by file count,
// per-file size and total expanded size (archive-bomb defense). Only
// the pinned executables, their bin/ siblings and license documents
// are materialized; unrelated archive content is skipped (bounded and
// counted, never written).

import (
	"archive/tar"
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Extraction bounds (documented, deliberately generous for FFmpeg packs).
const (
	maxEntryCount  = 50000
	maxEntryBytes  = 512 << 20 // 512 MiB per file
	maxTotalBytes  = 3 << 30   // 3 GiB expanded total (zip-bomb bound)
	copyBufferSize = 1 << 20
	licenseNameCap = 64
)

// ExtractOptions selects what gets materialized.
type ExtractOptions struct {
	// Pack identifies the expected executables (archive-relative).
	Pack PlatformPack
	// Dest is the (empty, fresh) destination directory.
	Dest string
	// Goos selects the executable names.
	Goos string
}

// ExtractResult reports what was written.
type ExtractResult struct {
	FFmpeg  string
	FFprobe string
	Files   int
	Bytes   int64
}

// safeEntryName validates one archive entry name and returns its
// cleaned slash-separated form. The returned name is guaranteed to be
// relative and free of traversal, drive prefixes and separators that
// could escape Dest.
func safeEntryName(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("empty entry name")
	}
	if strings.ContainsAny(name, "\x00") {
		return "", fmt.Errorf("NUL byte in entry name")
	}
	if strings.Contains(name, "\\") {
		return "", fmt.Errorf("backslash in entry name %q", name)
	}
	if strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("absolute entry name %q", name)
	}
	if len(name) >= 2 && name[1] == ':' {
		return "", fmt.Errorf("drive-prefixed entry name %q", name)
	}
	clean := filepath.ToSlash(filepath.Clean(name))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("traversal in entry name %q", name)
	}
	return clean, nil
}

// wantsFile reports whether an archive entry should be materialized:
// the pinned executables' bin/ directory siblings and license docs.
func wantsFile(pack PlatformPack, slashName string) bool {
	binDir := pack.binDir()
	if binDir != "" && strings.HasPrefix(slashName, binDir+"/") {
		return true
	}
	base := strings.ToLower(filepath.Base(slashName))
	if len(base) > licenseNameCap {
		return false
	}
	switch {
	case strings.HasPrefix(base, "copying"), strings.HasPrefix(base, "license"),
		strings.HasPrefix(base, "notice"), strings.HasPrefix(base, "readme"):
		return true
	}
	return false
}

// binDir is the archive-relative directory holding the pinned pair.
func (p PlatformPack) binDir() string {
	dir := filepath.ToSlash(filepath.Dir(p.FFmpegPath))
	if dir == "." || dir == "/" {
		return ""
	}
	return dir
}

// extractDispatch runs the format-appropriate safe extractor. It is
// the shared pipeline used by the manager install flow and the
// pinned-archive integration test.
func extractDispatch(pack PlatformPack, archivePath string, opt ExtractOptions) (ExtractResult, error) {
	switch pack.ArchiveFormat {
	case "zip":
		return extractZip(archivePath, opt)
	case "tar.xz":
		return extractTarXZ(archivePath, opt)
	default:
		return ExtractResult{}, fmt.Errorf("mediatools: unsupported archive format %q", pack.ArchiveFormat)
	}
}

// extractZip safely extracts the selected files from a .zip archive.
func extractZip(archivePath string, opt ExtractOptions) (ExtractResult, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return ExtractResult{}, fmt.Errorf("mediatools: open zip: %w", err)
	}
	defer zr.Close()
	res := ExtractResult{}
	entries := 0
	for _, f := range zr.File {
		entries++
		if entries > maxEntryCount {
			return res, fmt.Errorf("mediatools: archive exceeds %d entries", maxEntryCount)
		}
		name, nerr := safeEntryName(f.Name)
		if nerr != nil {
			return res, fmt.Errorf("mediatools: reject zip entry: %w", nerr)
		}
		if f.FileInfo().IsDir() {
			continue
		}
		mode := f.FileInfo().Mode()
		if !mode.IsRegular() {
			return res, fmt.Errorf("mediatools: reject non-regular zip entry %q (mode %v)", name, mode)
		}
		// Symlinks in zip are flagged by mode bits; the IsRegular check
		// above already refuses them.
		if int64(f.UncompressedSize64) > maxEntryBytes {
			return res, fmt.Errorf("mediatools: zip entry %q exceeds %d bytes", name, maxEntryBytes)
		}
		if !wantsFile(opt.Pack, name) {
			continue
		}
		written, werr := writeZipEntry(f, name, opt.Dest, &res)
		if werr != nil {
			return res, werr
		}
		res.Bytes += written
		if res.Bytes > maxTotalBytes {
			return res, fmt.Errorf("mediatools: archive exceeds %d expanded bytes", maxTotalBytes)
		}
	}
	return res, finalizeExtract(opt, &res)
}

func writeZipEntry(f *zip.File, name, dest string, res *ExtractResult) (int64, error) {
	rc, err := f.Open()
	if err != nil {
		return 0, fmt.Errorf("mediatools: open zip entry %q: %w", name, err)
	}
	defer rc.Close()
	target, err := materializePath(dest, name)
	if err != nil {
		return 0, err
	}
	if target == "" {
		return 0, nil // skipped (not selected)
	}
	n, err := copyBounded(target, rc, maxEntryBytes)
	if err != nil {
		return 0, err
	}
	res.Files++
	if isExecutableName(target) {
		_ = os.Chmod(target, 0o755)
	}
	return n, nil
}

// extractTarXZ safely extracts the selected files from a .tar.xz archive.
func extractTarXZ(archivePath string, opt ExtractOptions) (ExtractResult, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return ExtractResult{}, fmt.Errorf("mediatools: open archive: %w", err)
	}
	defer f.Close()
	xr, err := newXZReader(f)
	if err != nil {
		return ExtractResult{}, fmt.Errorf("mediatools: xz decode: %w", err)
	}
	defer xr.Close()
	tr := tar.NewReader(xr)
	res := ExtractResult{}
	entries := 0
	for {
		hdr, herr := tr.Next()
		if herr == io.EOF {
			break
		}
		if herr != nil {
			return res, fmt.Errorf("mediatools: tar read: %w", herr)
		}
		entries++
		if entries > maxEntryCount {
			return res, fmt.Errorf("mediatools: archive exceeds %d entries", maxEntryCount)
		}
		name, nerr := safeEntryName(hdr.Name)
		if nerr != nil {
			return res, fmt.Errorf("mediatools: reject tar entry: %w", nerr)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg:
		case tar.TypeSymlink, tar.TypeLink, tar.TypeChar, tar.TypeBlock, tar.TypeFifo, tar.TypeXGlobalHeader, tar.TypeXHeader:
			if hdr.Typeflag == tar.TypeXGlobalHeader || hdr.Typeflag == tar.TypeXHeader {
				continue // pax metadata, not a file
			}
			return res, fmt.Errorf("mediatools: reject tar entry %q (type %c — symlinks/hardlinks/devices are forbidden)", name, hdr.Typeflag)
		default:
			return res, fmt.Errorf("mediatools: reject tar entry %q (unknown type %c)", name, hdr.Typeflag)
		}
		if hdr.Size < 0 || hdr.Size > maxEntryBytes {
			return res, fmt.Errorf("mediatools: tar entry %q exceeds size bound (%d)", name, hdr.Size)
		}
		if !wantsFile(opt.Pack, name) {
			continue
		}
		target, merr := materializePath(opt.Dest, name)
		if merr != nil {
			return res, merr
		}
		if target == "" {
			continue
		}
		n, cerr := copyBounded(target, tr, hdr.Size)
		if cerr != nil {
			return res, cerr
		}
		res.Bytes += n
		res.Files++
		if res.Bytes > maxTotalBytes {
			return res, fmt.Errorf("mediatools: archive exceeds %d expanded bytes", maxTotalBytes)
		}
		_ = os.Chmod(target, 0o755)
	}
	return res, finalizeExtract(opt, &res)
}

// materializePath maps a validated archive name onto a unique path
// under dest, refusing to overwrite an existing file (duplicate
// executable paths are rejected). Returns "" when the file is not
// selected for materialization.
func materializePath(dest, slashName string) (string, error) {
	target := filepath.Join(dest, filepath.FromSlash(slashName))
	if fi, err := os.Lstat(target); err == nil && fi.Mode().IsDir() {
		return "", nil // a directory that shadows this name: skip
	} else if err == nil {
		return "", fmt.Errorf("mediatools: duplicate archive path %q", slashName)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", fmt.Errorf("mediatools: mkdir for %q: %w", slashName, err)
	}
	return target, nil
}

// copyBounded streams src into target with a hard byte bound.
func copyBounded(target string, src io.Reader, limit int64) (int64, error) {
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
	if err != nil {
		return 0, fmt.Errorf("mediatools: create %q: %w", target, err)
	}
	defer out.Close()
	buf := make([]byte, copyBufferSize)
	var total int64
	for {
		n, rerr := src.Read(buf)
		if n > 0 {
			total += int64(n)
			if total > limit {
				return total, fmt.Errorf("mediatools: %q exceeds %d bytes", target, limit)
			}
			if _, werr := out.Write(buf[:n]); werr != nil {
				return total, fmt.Errorf("mediatools: write %q: %w", target, werr)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return total, fmt.Errorf("mediatools: read %q: %w", target, rerr)
		}
	}
	return total, nil
}

// finalizeExtract verifies the pinned executables landed in the
// extraction result.
func finalizeExtract(opt ExtractOptions, res *ExtractResult) error {
	bin := opt.Pack.binDir()
	if bin == "" {
		return fmt.Errorf("mediatools: manifest pack has no bin dir")
	}
	ff := filepath.Join(opt.Dest, filepath.FromSlash(opt.Pack.FFmpegPath))
	fp := filepath.Join(opt.Dest, filepath.FromSlash(opt.Pack.FFprobePath))
	if !isRegular(ff) {
		return fmt.Errorf("mediatools: archive did not contain %q", opt.Pack.FFmpegPath)
	}
	if !isRegular(fp) {
		return fmt.Errorf("mediatools: archive did not contain %q", opt.Pack.FFprobePath)
	}
	res.FFmpeg = ff
	res.FFprobe = fp
	return nil
}

// runtimeGOOS is a package-level indirection so tests can pin the
// executable-suffix decision on any host.
var runtimeGOOS = runtime.GOOS

// isExecutableName reports whether a materialized file should carry
// the executable bit (the pinned pair, or a bin/ sibling).
func isExecutableName(path string) bool {
	if strings.HasSuffix(path, ".exe") {
		return true
	}
	return filepath.Base(filepath.Dir(path)) == "bin" && !strings.Contains(filepath.Base(path), ".")
}
