package mediatools

// archive.go — the ONE central, format-aware, validated extraction
// boundary for every untrusted archive the application opens (ZIP and
// TAR.XZ today). This is the root-cause remediation for the two open
// CodeQL `go/zipslip` alerts (CWE-22, High) previously reported at the
// old string-sanitized extraction loops: archive entry names are no
// longer turned into filesystem paths through `filepath.Join` +
// `os.OpenFile` after a prefix check. Instead every name is validated
// in the pure slash domain (`path.Clean` canonical form +
// `io/fs.ValidPath` + per-component Windows-safety rules), localized
// with `filepath.Localize` and gated by `filepath.IsLocal` (the
// sanitizer barrier that severs the name→path taint flow), and EVERY
// file or directory creation happens through an `os.Root` opened on a
// fresh private destination, so containment is enforced by the
// operating-system-level API and not by string comparison.
//
// Defense summary (work order V5 §2.2–2.3):
//   - names: reject empty, NUL/control chars, backslashes (Windows
//     separator ambiguity), colons (drive prefixes + alternate data
//     streams), non-canonical forms (`.`/`..`/`//`/trailing separator
//     on files/`./` prefixes), Windows reserved device names
//     (CON/NUL/AUX/PRN/COM1-9/LPT1-9/CONIN$/CONOUT$, also with
//     extensions), components ending in dot or space (Win32 aliasing),
//     and absurd name lengths.
//   - entry types: only regular files and directories are honored;
//     symlinks, hardlinks, devices, FIFOs, sockets and unknown types
//     are rejected outright (go/unsafe-unzip-symlink defense).
//   - collisions: exact and case-insensitive duplicate files,
//     file/directory collisions and parent/child conflicts are
//     rejected BEFORE any byte is written.
//   - modes: archive-supplied modes are ignored; files are created
//     0644, and 0755 only for the pinned executable naming rule.
//   - bombs: entry-count, per-entry and total expanded-size bounds,
//     declared-size prechecks, streamed bounded copies (never an
//     allocation sized from an untrusted declared length), CRC
//     verification through the archive readers, and truncation errors
//     propagate as hard failures.
//   - containment: exclusive creation (O_EXCL) through os.Root;
//     outside-pointing symlinks pre-existing in the destination cannot
//     be followed out of the root; the destination must be a real
//     directory (not a symlink) owned by the caller, and production
//     callers extract into a fresh unique staging directory that is
//     atomically promoted only after full verification (see manager.go).

import (
	"archive/tar"
	"archive/zip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

// Extraction bounds (documented, deliberately generous for FFmpeg packs).
const (
	maxEntryCount  = 50000
	maxEntryBytes  = 512 << 20 // 512 MiB per file
	maxTotalBytes  = 3 << 30   // 3 GiB expanded total (archive-bomb bound)
	copyBufferSize = 1 << 20
	licenseNameCap = 64
	// maxEntryNameLen bounds untrusted archive entry names long before
	// any OS path limit becomes relevant.
	maxEntryNameLen = 2048
)

// windowsReservedNames are the device names Win32 reserves regardless
// of extension or case; a component matching one of them can alias a
// device instead of a file.
var windowsReservedNames = func() map[string]bool {
	m := map[string]bool{
		"CON": true, "PRN": true, "AUX": true, "NUL": true,
		"CONIN$": true, "CONOUT$": true,
	}
	for i := 1; i <= 9; i++ {
		m[fmt.Sprintf("COM%d", i)] = true
		m[fmt.Sprintf("LPT%d", i)] = true
	}
	return m
}()

// isReservedComponent reports whether one path component would alias a
// reserved Windows device name ("NUL", "com1.bin", "AUX.txt", ...).
func isReservedComponent(c string) bool {
	base, _, _ := strings.Cut(c, ".")
	return windowsReservedNames[strings.ToUpper(base)]
}

// validateEntryName validates one untrusted archive entry name and
// returns its canonical slash-separated form together with the
// localized (OS-separator) relative path used for the confined write.
// isDir selects the directory convention: archive directory entries
// canonically end with exactly one trailing slash, which is tolerated
// and stripped; a trailing separator on a file entry is an anomaly and
// rejected.
//
// The returned local name has passed `filepath.IsLocal` — the explicit
// lexical sanitizer for path-injection taint flows — and every consumer
// writes it only through os.Root methods.
func validateEntryName(raw string, isDir bool) (slash, local string, err error) {
	if raw == "" {
		return "", "", fmt.Errorf("empty entry name")
	}
	if len(raw) > maxEntryNameLen {
		return "", "", fmt.Errorf("entry name exceeds %d characters", maxEntryNameLen)
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			return "", "", fmt.Errorf("control character in entry name")
		}
	}
	if strings.Contains(raw, "\\") {
		return "", "", fmt.Errorf("backslash in entry name %q", raw)
	}
	if strings.Contains(raw, ":") {
		return "", "", fmt.Errorf("colon in entry name %q (drive prefix or alternate data stream)", raw)
	}
	name := raw
	if isDir {
		name = strings.TrimSuffix(name, "/")
		if name == "" {
			return "", "", fmt.Errorf("rooted/absolute directory entry %q", raw)
		}
	}
	if name != path.Clean(name) {
		// Rejects ".", "..", "a//b", "a/./b", "./a", "a/" (on files),
		// "./", trailing separators and every other non-canonical form.
		return "", "", fmt.Errorf("non-canonical entry name %q", raw)
	}
	if name == "." {
		return "", "", fmt.Errorf("dot entry name %q", raw)
	}
	if !fs.ValidPath(name) {
		// Defense in depth: ValidPath rejects absolute paths, parent
		// references and empty components after canonicalization.
		return "", "", fmt.Errorf("invalid entry name %q", raw)
	}
	for _, comp := range strings.Split(name, "/") {
		if comp == "" {
			return "", "", fmt.Errorf("empty component in entry name %q", raw)
		}
		if strings.HasSuffix(comp, ".") || strings.HasSuffix(comp, " ") {
			return "", "", fmt.Errorf("component ending in dot/space in entry name %q (filesystem aliasing)", raw)
		}
		if isReservedComponent(comp) {
			return "", "", fmt.Errorf("reserved device name in entry %q", raw)
		}
	}
	local, lerr := filepath.Localize(name)
	if lerr != nil {
		return "", "", fmt.Errorf("entry name %q cannot be localized: %w", raw, lerr)
	}
	if !filepath.IsLocal(local) {
		// The sanitizer barrier: anything that could lexically escape
		// the destination root is refused here, independent of the
		// os.Root containment that follows.
		return "", "", fmt.Errorf("entry name %q is not a local relative path", raw)
	}
	return name, local, nil
}

// foldPath is the case-insensitive comparison key used for collision
// detection so that case-only collisions are caught on every platform
// (they are silent escapes of intent on case-insensitive filesystems).
func foldPath(p string) string { return strings.ToLower(p) }

// extractionTracker remembers every materialized path (and every
// directory entry) so duplicates, case-collisions, file/directory
// collisions and parent/child conflicts are rejected before writing.
type extractionTracker struct {
	files map[string]string // fold(slash) -> slash (materialized files)
	dirs  map[string]bool   // fold(slash) (materialized/explicit directories)
}

func newExtractionTracker() *extractionTracker {
	return &extractionTracker{files: map[string]string{}, dirs: map[string]bool{}}
}

// ancestors iterates the parent chain of slashName ("a/b/c" -> "a/b", "a").
func ancestors(slashName string, fn func(anc string) bool) {
	rest := slashName
	for {
		i := strings.LastIndex(rest, "/")
		if i <= 0 {
			return
		}
		rest = rest[:i]
		if fn(rest) {
			return
		}
	}
}

// claimDir records a directory entry. Re-claiming the same directory is
// tolerated (streaming tars legitimately repeat parents), but a
// directory may never collide with a materialized file, and no ancestor
// may be a materialized file.
func (x *extractionTracker) claimDir(slashName string) error {
	fold := foldPath(slashName)
	if prev, ok := x.files[fold]; ok {
		return fmt.Errorf("directory entry %q collides with materialized file %q", slashName, prev)
	}
	var bad string
	ancestors(slashName, func(anc string) bool {
		if p, ok := x.files[foldPath(anc)]; ok {
			bad = p
			return true
		}
		return false
	})
	if bad != "" {
		return fmt.Errorf("directory entry %q is nested under materialized file %q", slashName, bad)
	}
	x.dirs[fold] = true
	ancestors(slashName, func(anc string) bool {
		x.dirs[foldPath(anc)] = true
		return false
	})
	return nil
}

// claimFile records intent to materialize a file. It rejects exact and
// case-insensitive duplicates, file/directory collisions and
// parent/child conflicts before any byte is written.
func (x *extractionTracker) claimFile(slashName string) error {
	fold := foldPath(slashName)
	if prev, ok := x.files[fold]; ok {
		return fmt.Errorf("duplicate materialized path %q (case-insensitive match with %q)", slashName, prev)
	}
	if x.dirs[fold] {
		return fmt.Errorf("file entry %q collides with a directory", slashName)
	}
	var bad string
	ancestors(slashName, func(anc string) bool {
		if p, ok := x.files[foldPath(anc)]; ok {
			bad = p
			return true
		}
		return false
	})
	if bad != "" {
		return fmt.Errorf("file entry %q is nested under materialized file %q", slashName, bad)
	}
	x.files[fold] = slashName
	ancestors(slashName, func(anc string) bool {
		x.dirs[foldPath(anc)] = true
		return false
	})
	return nil
}

// extractSession confines every write to the destination directory
// through an os.Root handle. The root enforces containment at the
// syscall level (openat2/RESOLVE_BENEATH on Linux, path normalization
// and reparse-point refusal on Windows): even a pre-existing symlink
// inside the destination cannot make an operation land outside it.
type extractSession struct {
	root    *os.Root
	dest    string
	tracker *extractionTracker
}

// newExtractSession opens the confined extraction root. The destination
// must be a real directory (a symlinked destination is refused); the
// production caller always passes a fresh unique staging directory.
func newExtractSession(dest string) (*extractSession, error) {
	fi, err := os.Lstat(dest)
	if err != nil {
		return nil, fmt.Errorf("mediatools: extraction destination: %w", err)
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("mediatools: extraction destination %q is not a directory", dest)
	}
	root, err := os.OpenRoot(dest)
	if err != nil {
		return nil, fmt.Errorf("mediatools: open extraction root: %w", err)
	}
	return &extractSession{root: root, dest: dest, tracker: newExtractionTracker()}, nil
}

func (s *extractSession) close() { _ = s.root.Close() }

// writeFile streams src into the confined path local (already
// validated and IsLocal-gated) with exclusive creation semantics and a
// hard per-file byte bound. Modes come from the small allowlist only:
// 0644 for data files, 0755 for the pinned executable naming rule.
func (s *extractSession) writeFile(local string, src io.Reader, limit int64, executable bool) (int64, error) {
	if dir := filepath.Dir(local); dir != "." {
		if err := s.root.MkdirAll(dir, 0o755); err != nil {
			return 0, fmt.Errorf("mediatools: mkdir for %q: %w", local, err)
		}
	}
	perm := os.FileMode(0o644)
	if executable {
		perm = 0o755
	}
	out, err := s.root.OpenFile(local, os.O_CREATE|os.O_WRONLY|os.O_EXCL, perm)
	if err != nil {
		return 0, fmt.Errorf("mediatools: create %q: %w", local, err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = out.Close()
		}
	}()
	buf := make([]byte, copyBufferSize)
	var total int64
	for {
		n, rerr := src.Read(buf)
		if n > 0 {
			total += int64(n)
			if total > limit {
				return total, fmt.Errorf("mediatools: %q exceeds %d bytes", local, limit)
			}
			if _, werr := out.Write(buf[:n]); werr != nil {
				return total, fmt.Errorf("mediatools: write %q: %w", local, werr)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return total, fmt.Errorf("mediatools: read %q: %w", local, rerr)
		}
	}
	closed = true
	if err := out.Close(); err != nil {
		return total, fmt.Errorf("mediatools: close %q: %w", local, err)
	}
	return total, nil
}

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

// extractZip safely extracts the selected files from a .zip archive
// through the confined extraction session.
func extractZip(archivePath string, opt ExtractOptions) (ExtractResult, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return ExtractResult{}, fmt.Errorf("mediatools: open zip: %w", err)
	}
	defer zr.Close()
	sess, err := newExtractSession(opt.Dest)
	if err != nil {
		return ExtractResult{}, err
	}
	defer sess.close()
	res := ExtractResult{}
	entries := 0
	for _, f := range zr.File {
		entries++
		if entries > maxEntryCount {
			return res, fmt.Errorf("mediatools: archive exceeds %d entries", maxEntryCount)
		}
		// Go's zip reader marks every trailing-slash name as a
		// directory (zip spec), so the dir/file discrimination is
		// unambiguous here.
		isDir := f.FileInfo().IsDir()
		name, local, nerr := validateEntryName(f.Name, isDir)
		if nerr != nil {
			return res, fmt.Errorf("mediatools: reject zip entry: %w", nerr)
		}
		if isDir {
			if derr := sess.tracker.claimDir(name); derr != nil {
				return res, fmt.Errorf("mediatools: reject zip entry: %w", derr)
			}
			continue
		}
		mode := f.FileInfo().Mode()
		if !mode.IsRegular() {
			// Symlinks in zip are flagged by mode bits; this check
			// refuses them along with devices and any special type.
			return res, fmt.Errorf("mediatools: reject non-regular zip entry %q (mode %v)", name, mode)
		}
		if int64(f.UncompressedSize64) > maxEntryBytes {
			return res, fmt.Errorf("mediatools: zip entry %q exceeds %d bytes", name, maxEntryBytes)
		}
		if !wantsFile(opt.Pack, name) {
			continue
		}
		if cerr := sess.tracker.claimFile(name); cerr != nil {
			return res, fmt.Errorf("mediatools: reject zip entry: %w", cerr)
		}
		rc, oerr := f.Open()
		if oerr != nil {
			return res, fmt.Errorf("mediatools: open zip entry %q: %w", name, oerr)
		}
		written, werr := sess.writeFile(local, rc, maxEntryBytes, isExecutableName(local))
		_ = rc.Close()
		if werr != nil {
			return res, werr
		}
		res.Files++
		res.Bytes += written
		if res.Bytes > maxTotalBytes {
			return res, fmt.Errorf("mediatools: archive exceeds %d expanded bytes", maxTotalBytes)
		}
	}
	return res, finalizeExtract(opt, &res)
}

// extractTarXZ safely extracts the selected files from a .tar.xz
// archive through the confined extraction session.
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
	sess, err := newExtractSession(opt.Dest)
	if err != nil {
		return ExtractResult{}, err
	}
	defer sess.close()
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
		isDir := hdr.Typeflag == tar.TypeDir
		name, local, nerr := validateEntryName(hdr.Name, isDir)
		if nerr != nil {
			return res, fmt.Errorf("mediatools: reject tar entry: %w", nerr)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if derr := sess.tracker.claimDir(name); derr != nil {
				return res, fmt.Errorf("mediatools: reject tar entry: %w", derr)
			}
			continue
		case tar.TypeReg:
		case tar.TypeXGlobalHeader, tar.TypeXHeader:
			continue // pax metadata, not a file
		case tar.TypeSymlink, tar.TypeLink, tar.TypeChar, tar.TypeBlock, tar.TypeFifo:
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
		if cerr := sess.tracker.claimFile(name); cerr != nil {
			return res, fmt.Errorf("mediatools: reject tar entry: %w", cerr)
		}
		written, cerr := sess.writeFile(local, tr, hdr.Size, isExecutableName(local))
		if cerr != nil {
			return res, cerr
		}
		res.Bytes += written
		res.Files++
		if res.Bytes > maxTotalBytes {
			return res, fmt.Errorf("mediatools: archive exceeds %d expanded bytes", maxTotalBytes)
		}
	}
	return res, finalizeExtract(opt, &res)
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
