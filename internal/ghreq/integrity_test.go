package ghreq

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFileBytes(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func sha256hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func TestVerifyFileSHA256(t *testing.T) {
	dir := t.TempDir()
	data := []byte("archive-bytes-0001")
	p := writeFileBytes(t, dir, "a.bin", data)

	ok, err := VerifyFileSHA256(p, sha256hex(data))
	if err != nil || !ok {
		t.Fatalf("correct digest failed: ok=%v err=%v", ok, err)
	}
	ok, err = VerifyFileSHA256(p, strings.Repeat("00", 32))
	if err != nil || ok {
		t.Fatalf("wrong digest accepted: ok=%v err=%v", ok, err)
	}
	_, err = VerifyFileSHA256(p, "tooshort")
	if !errors.Is(err, ErrDigestFormat) {
		t.Fatalf("bad digest format not typed: %v", err)
	}
}

func TestClassifyIntegrityPinnedWins(t *testing.T) {
	dir := t.TempDir()
	data := []byte("trusted-bytes")
	p := writeFileBytes(t, dir, "tool.zip", data)

	req := &Requirement{ID: "t", Executable: true, PinnedSHA256: sha256hex(data)}
	asset := &Asset{Digest: "sha256:" + strings.Repeat("ff", 32)}
	level, err := ClassifyIntegrity(req, asset, "windows/amd64", p)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if level != IntegrityPinnedManifest {
		t.Fatalf("pinned digest must win: %s", level)
	}
	if err := EnsureExecutableTrust(req, level); err != nil {
		t.Fatalf("executable trust failed at pinned level: %v", err)
	}
}

func TestClassifyIntegrityGitHubDigest(t *testing.T) {
	dir := t.TempDir()
	data := []byte("github-known-bytes")
	p := writeFileBytes(t, dir, "tool.zip", data)

	req := &Requirement{ID: "t", Executable: true}
	asset := &Asset{Digest: "sha256:" + sha256hex(data)}
	level, err := ClassifyIntegrity(req, asset, "linux/amd64", p)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if level != IntegrityGitHubDigest {
		t.Fatalf("want github digest level, got %s", level)
	}
	if err := EnsureExecutableTrust(req, level); err != nil {
		t.Fatalf("github digest must satisfy executable trust: %v", err)
	}
}

func TestClassifyIntegrityMismatchBlocks(t *testing.T) {
	dir := t.TempDir()
	p := writeFileBytes(t, dir, "tool.zip", []byte("actual"))
	req := &Requirement{ID: "t", Executable: true, PinnedSHA256: strings.Repeat("11", 32)}
	level, err := ClassifyIntegrity(req, nil, "linux/amd64", p)
	if err == nil || level != IntegrityFailed {
		t.Fatalf("mismatch must fail hard: level=%s err=%v", level, err)
	}
	if !errors.Is(err, ErrDigestMismatch) {
		t.Fatalf("want digest mismatch, got %v", err)
	}
	if err := EnsureExecutableTrust(req, level); !errors.Is(err, ErrNoTrustMaterial) {
		t.Fatalf("failed level must block executables, got %v", err)
	}
}

func TestTransportOnlyBlocksExecutable(t *testing.T) {
	dir := t.TempDir()
	data := []byte("unverifiable-bytes")
	p := writeFileBytes(t, dir, "tool.zip", data)
	req := &Requirement{ID: "t", Executable: true}
	asset := &Asset{} // no digest supplied by GitHub, nothing pinned
	level, err := ClassifyIntegrity(req, asset, "linux/amd64", p)
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
	if level != IntegrityTransportOnly {
		t.Fatalf("want TRANSPORT_ONLY, got %s", level)
	}
	if err := EnsureExecutableTrust(req, level); !errors.Is(err, ErrNoTrustMaterial) {
		t.Fatalf("executable must be blocked at transport-only level, got %v", err)
	}
	// Non-executable assets may proceed with an explicit user choice
	// (the UI layer owns that confirmation; the engine reports level).
	req.Executable = false
	if err := EnsureExecutableTrust(req, level); err != nil {
		t.Fatalf("non-executable blocked wrongly: %v", err)
	}
}

func TestBadGitHubDigestFormatRejected(t *testing.T) {
	dir := t.TempDir()
	p := writeFileBytes(t, dir, "tool.zip", []byte("x"))
	req := &Requirement{ID: "t"}
	asset := &Asset{Digest: "md5:abcdef"} // wrong algorithm
	_, err := ClassifyIntegrity(req, asset, "linux/amd64", p)
	if !errors.Is(err, ErrDigestFormat) {
		t.Fatalf("md5 digest not rejected: %v", err)
	}
	asset.Digest = "sha256:zz-not-hex"
	if _, err := ClassifyIntegrity(req, asset, "linux/amd64", p); !errors.Is(err, ErrDigestFormat) {
		t.Fatalf("non-hex digest not rejected: %v", err)
	}
}

func TestHTMLPayloadRejected(t *testing.T) {
	err := CheckHTMLPayload([]byte("<html><body>Service unavailable</body></html>"))
	if !errors.Is(err, ErrHTMLPayload) {
		t.Fatalf("html payload accepted: %v", err)
	}
	if err := CheckHTMLPayload([]byte("PK\x03\x04")); err != nil {
		t.Fatalf("binary rejected: %v", err)
	}
}

func TestSizeBounds(t *testing.T) {
	req := &Requirement{ID: "t", MaxExpandedBytes: 100 << 20}
	if err := SizeBounds(50<<20, req); err != nil {
		t.Fatalf("in-bounds size rejected: %v", err)
	}
	if err := SizeBounds(200<<20, req); !errors.Is(err, ErrSizeOutsideBounds) {
		t.Fatalf("oversize accepted: %v", err)
	}
	if err := SizeBounds(0, req); !errors.Is(err, ErrSizeOutsideBounds) {
		t.Fatalf("zero-size accepted: %v", err)
	}
}
