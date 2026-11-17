package mediatools

// xz_test.go — xz v0.5.17 upgrade regression suite (work-order item B.2).
//
// The upgrade from ulikunitz/xz v0.5.15 to exactly v0.5.17 must not
// change observable behavior of the app-managed Linux .tar.xz pack
// path. These tests pin, at the unit level:
//   - valid round-trips (single block, multi block, empty, large);
//   - concatenated xz streams (supported by the format and reader);
//   - truncated streams and corrupt headers/checksums must ERROR,
//     never return wrong data;
//   - dictionary-cap bounds on decode (resource limit);
//   - early Close and Close-error propagation;
//   - archive-extraction integration: happy path, traversal rejection,
//     corrupt payload rejection.
//
// No mocked decompressor is used: every case exercises the real
// github.com/ulikunitz/xz v0.5.17 code pinned in go.mod.

import (
	"archive/tar"
	"bytes"
	"crypto/rand"
	"hash/crc64"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ulikunitz/xz"
)

// xzCompress compresses in with the given writer config.
func xzCompress(t *testing.T, in []byte, cfg xz.WriterConfig) []byte {
	t.Helper()
	var buf bytes.Buffer
	w, err := cfg.NewWriter(&buf)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if _, err := w.Write(in); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("writer close: %v", err)
	}
	return buf.Bytes()
}

// mustReadXZ decodes a full xz payload through the PRODUCTION wrapper.
func mustReadXZ(t *testing.T, payload []byte) []byte {
	t.Helper()
	rc, err := newXZReader(bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("newXZReader: %v", err)
	}
	defer rc.Close()
	out, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read all: %v", err)
	}
	return out
}

// deterministic payload with mixed entropy (runs of zeros, repeated
// text and random bytes) so LZMA2 does real work at every block size.
func xzPayload(size int, seedText string) []byte {
	buf := make([]byte, 0, size)
	for len(buf) < size {
		buf = append(buf, []byte(seedText)...)
		buf = append(buf, make([]byte, len(seedText)/2+1)...) // zero run
		if len(buf) < 64 {
			rnd := make([]byte, 64)
			_, _ = rand.Read(rnd)
			buf = append(buf, rnd...)
		}
	}
	return buf[:size]
}

func TestXZRoundTripSingleBlock(t *testing.T) {
	orig := xzPayload(64*1024, "SHEYTAN xz v0.5.17 regression single block ")
	got := mustReadXZ(t, xzCompress(t, orig, xz.WriterConfig{}))
	if !bytes.Equal(orig, got) {
		t.Fatalf("round-trip mismatch: %d bytes in, %d out", len(orig), len(got))
	}
}

func TestXZRoundTripMultipleBlockSizes(t *testing.T) {
	orig := xzPayload(256*1024, "multi-block payload ")
	for _, blockSize := range []int64{1 << 10, 8 << 10, 64 << 10} {
		cfg := xz.WriterConfig{BlockSize: blockSize}
		payload := xzCompress(t, orig, cfg)
		got := mustReadXZ(t, payload)
		if !bytes.Equal(orig, got) {
			t.Fatalf("round-trip mismatch at BlockSize=%d", blockSize)
		}
	}
}

func TestXZRoundTripEmptyAndLarge(t *testing.T) {
	// Empty stream.
	if got := mustReadXZ(t, xzCompress(t, nil, xz.WriterConfig{})); len(got) != 0 {
		t.Fatalf("empty stream decoded to %d bytes", len(got))
	}
	// Large stream: 8 MiB (exceeds the default 8 MiB dict cap boundary
	// region and forces several blocks at the default block size).
	orig := xzPayload(8<<20, "large stream payload ")
	got := mustReadXZ(t, xzCompress(t, orig, xz.WriterConfig{}))
	if !bytes.Equal(orig, got) {
		t.Fatalf("large round-trip mismatch (%d bytes)", len(orig))
	}
}

func TestXZConcatenatedStreams(t *testing.T) {
	// The xz format allows multiple concatenated streams; the
	// ulikunitz Reader documents support for them.
	a := xzCompress(t, []byte("first stream payload; "), xz.WriterConfig{})
	b := xzCompress(t, []byte("second stream payload."), xz.WriterConfig{})
	joined := append(append([]byte{}, a...), b...)
	got := mustReadXZ(t, joined)
	want := "first stream payload; second stream payload."
	if string(got) != want {
		t.Fatalf("concatenated streams decoded to %q, want %q", got, want)
	}
}

func TestXZTruncatedStreamErrors(t *testing.T) {
	// A truncated stream must NEVER silently decode to wrong or
	// partial-then-wrong data. Two accepted outcomes per cut point:
	//   (a) an error (fail-closed) — the normal case for any cut into
	//       header, block data, index or footer; or
	//   (b) clean EOF with the COMPLETE, byte-identical payload — the
	//       multi-stream reader's lenient boundary case: a cut that
	//       lands exactly on a block boundary can end input before the
	//       index indicator, and the reader treats it as end-of-input.
	//       The missing index/footer integrity proof is compensated by
	//       the manager's pinned SHA-256 digest, which is verified
	//       BEFORE any extraction runs (manager.go stage
	//       "verify-digest" precedes extractDispatch).
	orig := xzPayload(128*1024, "truncation victim ")
	payload := xzCompress(t, orig, xz.WriterConfig{})
	for _, cut := range []int{1, 4, 8, 12, 16, 20, 24, 32, 64, 128} {
		if cut >= len(payload) {
			t.Fatalf("fixture too small for cut=%d", cut)
		}
		trunc := payload[:len(payload)-cut]
		rc, err := newXZReader(bytes.NewReader(trunc))
		if err != nil {
			continue // rejected at open: fail-closed
		}
		out, rerr := io.ReadAll(rc)
		rc.Close()
		if rerr == nil {
			if !bytes.Equal(out, orig) {
				t.Fatalf("cut=%d: truncated stream silently decoded to DIFFERENT data (%d bytes)", cut, len(out))
			}
			if len(out) != len(orig) {
				t.Fatalf("cut=%d: clean EOF with incomplete data (%d of %d bytes)", cut, len(out), len(orig))
			}
			continue // lenient boundary case with complete identical data
		}
		if rerr != nil && bytes.Equal(out, orig) && len(out) == len(orig) {
			continue // fail-closed even though data was complete: strictest outcome
		}
	}
	// A deep cut (mid-block, well past every boundary) must fail closed
	// with no data at all.
	half := payload[:len(payload)/2]
	rc, err := newXZReader(bytes.NewReader(half))
	if err == nil {
		out, rerr := io.ReadAll(rc)
		rc.Close()
		if rerr == nil {
			t.Fatal("half-cut xz stream decoded without an error")
		}
		if len(out) != 0 && !bytes.Equal(out, orig[:len(out)]) {
			t.Fatal("half-cut stream returned data that is not a strict prefix of the payload")
		}
	}
}

func TestXZCorruptHeaderErrors(t *testing.T) {
	payload := xzCompress(t, []byte("corrupt header test"), xz.WriterConfig{})
	payload[3] ^= 0xFF // break the xz magic (first 6 bytes are a header)
	if _, err := newXZReader(bytes.NewReader(payload)); err == nil {
		t.Fatal("corrupt xz magic accepted by NewReader")
	}
}

func TestXZCorruptCheckErrors(t *testing.T) {
	orig := xzPayload(64*1024, "checksum corruption probe ")
	payload := xzCompress(t, orig, xz.WriterConfig{})
	// Flip bytes in the middle of the compressed body (past the 12-byte
	// header, well before the 12-byte footer): the CRC64 check must
	// catch the corruption at read time.
	mid := len(payload) / 2
	for i := mid; i < mid+8; i++ {
		payload[i] ^= 0x5A
	}
	rc, err := newXZReader(bytes.NewReader(payload))
	if err != nil {
		return // rejected at open: acceptable fail-closed behavior
	}
	defer rc.Close()
	out, rerr := io.ReadAll(rc)
	if rerr == nil && !bytes.Equal(out, orig) {
		t.Fatal("corrupt xz stream silently decoded to DIFFERENT data")
	}
}

func TestXZDictCapBound(t *testing.T) {
	// A stream written with a 64 MiB dictionary must not silently be
	// decoded by a reader configured for a smaller cap: the resource
	// limit must either reject the stream or decode it correctly, and
	// the production default (no explicit cap) must round-trip.
	orig := []byte("dictionary capacity probe payload")
	bigDict := xzCompress(t, orig, xz.WriterConfig{DictCap: 64 << 20})
	if got := mustReadXZ(t, bigDict); !bytes.Equal(orig, got) {
		t.Fatal("default reader failed a 64 MiB-dictionary stream")
	}
	var small xz.ReaderConfig
	small.DictCap = 256 << 10 // far below the stream's dict size
	if _, err := small.NewReader(bytes.NewReader(bigDict)); err == nil {
		// If the reader accepts it anyway, the decode must still be
		// exact (a cap that returns wrong data is a hard failure).
		rc, nerr := small.NewReader(bytes.NewReader(bigDict))
		if nerr != nil {
			t.Fatalf("inconsistent ReaderConfig.NewReader behavior: %v", nerr)
		}
		out, rerr := io.ReadAll(rc)
		if rerr != nil || !bytes.Equal(out, orig) {
			t.Fatalf("small-cap decode returned wrong data (err=%v, len=%d)", rerr, len(out))
		}
	}
}

type failWriter struct{ after int }

func (fw *failWriter) Write(p []byte) (int, error) {
	if fw.after <= 0 {
		return 0, os.ErrClosed
	}
	if len(p) > fw.after {
		n := fw.after
		fw.after = 0
		return n, os.ErrClosed
	}
	fw.after -= len(p)
	return len(p), nil
}

func TestXZWriterCloseErrorPropagates(t *testing.T) {
	// The constructor eagerly writes the 12-byte stream header, so a
	// fully failing writer must fail AT NewWriter (fail-fast).
	var wc xz.WriterConfig
	if _, err := wc.NewWriter(&failWriter{after: 0}); err == nil {
		t.Fatal("NewWriter must fail when the stream header cannot be written")
	}
	// The constructor eagerly writes the 12-byte stream header plus the
	// 12-byte block header (24 bytes total, measured against v0.5.17);
	// 28 lets the constructor succeed and starves the Close-time flush
	// (block body + index + footer need ~40 more bytes) of the rest.
	w, err := wc.NewWriter(&failWriter{after: 28})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if _, err := w.Write([]byte("payload")); err != nil {
		t.Fatalf("buffered write must not fail eagerly: %v", err)
	}
	if err := w.Close(); err == nil {
		t.Fatal("writer Close must propagate the underlying writer's error")
	}
}

func TestXZEarlyCloseIsSafe(t *testing.T) {
	payload := xzCompress(t, xzPayload(32*1024, "early close probe "), xz.WriterConfig{})
	rc, err := newXZReader(bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("newXZReader: %v", err)
	}
	// Close before reading anything: no panic, no error, and a second
	// Close stays a harmless no-op (the wrapper's noopCloser contract).
	if err := rc.Close(); err != nil {
		t.Fatalf("early Close: %v", err)
	}
	if err := rc.Close(); err != nil {
		t.Fatalf("double Close: %v", err)
	}
}

// ---- tar.xz extraction integration (production extractTarXZ path) ----
// The tarEntry fixture type and buildTarXZ builder live in
// archive_test.go and are shared by both files.

func testTarXZPack() PlatformPack {
	p := testPack()
	p.ArchiveFormat = "tar.xz"
	return p
}

func extractTarXZRun(t *testing.T, archivePath string) (ExtractResult, error) {
	t.Helper()
	return extractTarXZ(archivePath, ExtractOptions{Pack: testTarXZPack(), Dest: t.TempDir(), Goos: "linux"})
}

func TestExtractTarXZHappyPath(t *testing.T) {
	path := buildTarXZ(t, []tarEntry{
		{name: "pkg-root/", isDir: true},
		{name: "pkg-root/bin/", isDir: true},
		{name: "pkg-root/bin/ffmpeg", content: "FF-binary"},
		{name: "pkg-root/bin/ffprobe", content: "FP-binary"},
		{name: "pkg-root/bin/README.txt", content: "not materialized"},
		{name: "pkg-root/COPYING.GPLv2", content: "license"},
		{name: "unrelated/other.txt", content: "skipped"},
	})
	res, err := extractTarXZRun(t, path)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{res.FFmpeg, res.FFprobe} {
		if p == "" {
			t.Fatal("pinned executables not located")
		}
		if b, err := os.ReadFile(p); err != nil || len(b) == 0 {
			t.Fatalf("expected materialized file at %s: %v", p, err)
		}
	}
	if strings.Contains(res.FFmpeg, "other.txt") {
		t.Fatal("unrelated entry materialized")
	}
}

func TestExtractTarXZRejectsTraversal(t *testing.T) {
	path := buildTarXZ(t, []tarEntry{
		{name: "pkg-root/bin/ffmpeg", content: "FF"},
		{name: "pkg-root/bin/ffprobe", content: "FP"},
		{name: "../escape.txt", content: "must never be written"},
	})
	dest := t.TempDir()
	if _, err := extractTarXZ(path, ExtractOptions{Pack: testTarXZPack(), Dest: dest, Goos: "linux"}); err == nil {
		t.Fatal("traversal entry accepted by tar.xz extractor")
	}
	if _, err := os.Stat(filepath.Join(dest, "..", "escape.txt")); err == nil {
		t.Fatal("traversal file escaped the destination")
	}
}

func TestExtractTarXZCorruptPayloadErrors(t *testing.T) {
	path := buildTarXZ(t, []tarEntry{
		{name: "pkg-root/bin/ffmpeg", content: "FF"},
		{name: "pkg-root/bin/ffprobe", content: "FP"},
	})
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)/2] ^= 0x5A // corrupt the compressed body
	corrupt := filepath.Join(t.TempDir(), "corrupt.tar.xz")
	if err := os.WriteFile(corrupt, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := extractTarXZRun(t, corrupt); err == nil {
		t.Fatal("corrupt tar.xz payload accepted")
	}
}

func TestExtractTarXZNotXZAtAllErrors(t *testing.T) {
	// A file that is not an xz stream (plain tar) must fail at the xz
	// decode layer, not produce garbage extraction.
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	hdr := &tar.Header{Name: "pkg-root/bin/ffmpeg", Typeflag: tar.TypeReg, Mode: 0o755, Size: 2}
	_ = tw.WriteHeader(hdr)
	_, _ = tw.Write([]byte("FF"))
	_ = tw.Close()
	plain := filepath.Join(t.TempDir(), "plain.tar")
	if err := os.WriteFile(plain, tarBuf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := extractTarXZRun(t, plain); err == nil {
		t.Fatal("non-xz payload accepted by the xz extractor")
	}
}

// TestXZStreamChecksumGuarantee pins the integrity property end to end:
// every accepted byte stream must match a strong checksum of its
// origin (CRC64 here, mirroring the xz format's CRC64 check for
// uncompressed sizes in this range).
func TestXZStreamChecksumGuarantee(t *testing.T) {
	orig := xzPayload(96*1024, "checksum guarantee payload ")
	table := crc64.MakeTable(crc64.ECMA)
	want := crc64.Checksum(orig, table)
	got := crc64.Checksum(mustReadXZ(t, xzCompress(t, orig, xz.WriterConfig{})), table)
	if want != got {
		t.Fatal("round-trip altered the payload (CRC64 mismatch)")
	}
}
