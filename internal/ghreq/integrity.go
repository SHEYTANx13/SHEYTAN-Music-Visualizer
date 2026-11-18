package ghreq

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Integrity levels report exactly which trust evidence succeeded.
// They are user-visible strings per the work order contract; do not
// rename them casually.
type IntegrityLevel string

const (
	// IntegrityPinnedManifest means the bytes match the digest pinned
	// in the repository's provenance records.
	IntegrityPinnedManifest IntegrityLevel = "PINNED_MANIFEST_DIGEST_VERIFIED"
	// IntegrityGitHubDigest means the bytes match the sha256 digest
	// GitHub's release API reported for the asset. This proves byte
	// consistency with GitHub metadata, NOT publisher identity.
	IntegrityGitHubDigest IntegrityLevel = "GITHUB_RELEASE_DIGEST_VERIFIED"
	// IntegrityTransportOnly means HTTPS succeeded but no independent
	// digest or signature was available. Executable requirements can
	// never be installed at this level.
	IntegrityTransportOnly IntegrityLevel = "TRANSPORT_ONLY"
	// IntegrityFailed means a digest mismatched, the payload was not a
	// package, or verification could not run.
	IntegrityFailed IntegrityLevel = "FAILED"
)

// Integrity failures.
var (
	ErrDigestMismatch    = errors.New("ghreq: digest mismatch")
	ErrNoTrustMaterial   = errors.New("ghreq: no trusted integrity material for executable package")
	ErrHTMLPayload       = errors.New("ghreq: payload is an html error page, not a package")
	ErrDigestFormat      = errors.New("ghreq: digest field malformed")
	ErrSizeOutsideBounds = errors.New("ghreq: size outside bounds")
)

// VerifyFileSHA256 hashes a local file and compares the lowercase hex
// digest against expectedHex.
func VerifyFileSHA256(path, expectedHex string) (bool, error) {
	expectedHex = strings.ToLower(strings.TrimSpace(expectedHex))
	if len(expectedHex) != 64 || !isHex(expectedHex) {
		return false, fmt.Errorf("%w: %q", ErrDigestFormat, expectedHex)
	}
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false, err
	}
	got := hex.EncodeToString(h.Sum(nil))
	return got == expectedHex, nil
}

// SHA256File returns the lowercase hex digest of a local file.
func SHA256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func isHex(s string) bool {
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// ClassifyIntegrity verifies a downloaded archive against every piece
// of trust material available and reports exactly which level held:
// pinned catalog digest first, then GitHub's release-asset digest,
// then transport-only. Any mismatch is a hard failure.
func ClassifyIntegrity(req *Requirement, asset *Asset, platform, filePath string) (IntegrityLevel, error) {
	// Pinned digest from the repository provenance records wins.
	if pin := req.PinnedDigestFor(platform); pin != "" {
		ok, err := VerifyFileSHA256(filePath, pin)
		if err != nil {
			return IntegrityFailed, err
		}
		if !ok {
			return IntegrityFailed, fmt.Errorf("%w: pinned catalog digest", ErrDigestMismatch)
		}
		return IntegrityPinnedManifest, nil
	}
	// GitHub's release-asset digest field, obtained over TLS from the
	// official API. Format: "sha256:<hex>".
	if asset != nil && asset.Digest != "" {
		algo, hexPart, found := strings.Cut(asset.Digest, ":")
		if !found || algo != "sha256" {
			return IntegrityFailed, fmt.Errorf("%w: %q", ErrDigestFormat, asset.Digest)
		}
		ok, err := VerifyFileSHA256(filePath, hexPart)
		if err != nil {
			return IntegrityFailed, err
		}
		if !ok {
			return IntegrityFailed, fmt.Errorf("%w: github release digest", ErrDigestMismatch)
		}
		return IntegrityGitHubDigest, nil
	}
	// Nothing pinned, nothing reported: transport evidence only.
	return IntegrityTransportOnly, nil
}

// EnsureExecutableTrust enforces the hard gate: an executable
// requirement may only be installed with pinned-manifest or GitHub
// release digest evidence. TRANSPORT_ONLY is never sufficient and a
// UI override cannot bypass this.
func EnsureExecutableTrust(req *Requirement, level IntegrityLevel) error {
	if !req.Executable {
		return nil
	}
	switch level {
	case IntegrityPinnedManifest, IntegrityGitHubDigest:
		return nil
	default:
		return fmt.Errorf("%w: %s at %s", ErrNoTrustMaterial, req.ID, level)
	}
}

// CheckHTMLPayload rejects payloads whose leading bytes are an HTML
// error page. Call it before digest verification to produce a precise
// actionable failure instead of a bare mismatch.
func CheckHTMLPayload(head []byte) error {
	if LooksLikeHTMLErrorPage(head) {
		return ErrHTMLPayload
	}
	return nil
}

// SizeBounds checks the reported payload size against the catalog
// bounds before any bytes are trusted.
func SizeBounds(size int64, req *Requirement) error {
	if req == nil || req.MaxExpandedBytes <= 0 {
		return nil
	}
	// The archive must be strictly smaller than the expanded bound and
	// must not be empty.
	if size <= 0 || size > req.MaxExpandedBytes {
		return fmt.Errorf("%w: size=%d bound=%d", ErrSizeOutsideBounds, size, req.MaxExpandedBytes)
	}
	return nil
}
