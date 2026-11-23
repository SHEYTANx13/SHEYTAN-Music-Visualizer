package mediatools

// manifest.go — the pinned, versioned media-tool manifest (run
// 43712071111). The committed JSON document is the ONLY authority for
// what the in-app Media Tools & Codecs manager may download: exact
// upstream release asset URLs, exact versions, pinned SHA-256 digests,
// archive formats, the executable paths inside each archive, the
// license class and the provenance links. A floating "latest" URL or
// an asset name discovered from an unverified server response is
// never trusted.

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

//go:embed manifest.json
var embeddedManifest []byte

// SchemaVersion is the manifest document schema this build understands.
const SchemaVersion = 1

// Manifest is the versioned media-tool manifest document.
type Manifest struct {
	Schema    int                     `json:"schema"`
	Updated   string                  `json:"updated"`
	Note      string                  `json:"note,omitempty"`
	Platforms map[string]PlatformPack `json:"platforms"`
}

// PlatformPack describes the pinned tool pack for one OS/arch.
type PlatformPack struct {
	// Platform identity, e.g. "windows/amd64".
	Platform string `json:"platform"`
	// Version is the exact upstream build identity, e.g.
	// "n9.0.2-61-gd59078794d". BtbN rotates dated autobuild releases away
	// after a rolling window; when a pin 404s, rotate every pin site
	// together (manifest.json, MediaTools-Pinned.ps1,
	// ensure-pinned-media-tools.sh, packaging/FFMPEG-TOOLS-PROVENANCE.md).
	Version string `json:"version"`
	// URL is the exact upstream release asset URL (HTTPS only).
	URL string `json:"url"`
	// SHA256 is the pinned digest of the archive bytes.
	SHA256 string `json:"sha256"`
	// ArchiveFormat: "zip" or "tar.xz".
	ArchiveFormat string `json:"archiveFormat"`
	// FFmpegPath/FFprobePath are the archive-relative executable paths.
	FFmpegPath  string `json:"ffmpegPath"`
	FFprobePath string `json:"ffprobePath"`
	// RequiredEncoders must ALL be present after installation; a pack
	// missing one is rejected (never silently accepted).
	RequiredEncoders []string `json:"requiredEncoders"`
	// OptionalEncoders are checked and reported but not required.
	OptionalEncoders []string `json:"optionalEncoders,omitempty"`
	// LicenseClass is the pack's exact license label.
	LicenseClass string `json:"licenseClass"`
	// LicenseVerification are substrings ffmpeg -buildconf must
	// (MustContain) and must not (MustNotContain) match.
	LicenseVerification struct {
		MustContain    []string `json:"mustContain"`
		MustNotContain []string `json:"mustNotContain"`
	} `json:"licenseVerification"`
	// LicenseNotice is the short user-facing license statement.
	LicenseNotice string `json:"licenseNotice"`
	// SourceURL/BuildURL document where the pack comes from and how it
	// was built.
	SourceURL string `json:"sourceUrl"`
	BuildURL  string `json:"buildUrl"`
	// ApproxDownloadMB/AprroxInstalledMB are documented approximate
	// sizes (honest estimates, not exact claims).
	ApproxDownloadMB  int `json:"approxDownloadMB"`
	ApproxInstalledMB int `json:"approxInstalledMB"`
	// RequiredSharedLibraries lists DLLs/shared libraries that must ship
	// beside the executables (empty for static builds).
	RequiredSharedLibraries []string `json:"requiredSharedLibraries,omitempty"`
}

var sha256Re = regexp.MustCompile(`^[0-9a-f]{64}$`)

// EmbeddedManifest returns the committed manifest document.
func EmbeddedManifest() Manifest {
	m, err := ParseManifest(embeddedManifest)
	if err != nil {
		// The committed document is schema-validated by tests; a parse
		// failure here is a build-integrity bug, surfaced loudly.
		panic("mediatools: embedded manifest is invalid: " + err.Error())
	}
	return m
}

// ParseManifest validates and decodes a manifest document.
func ParseManifest(data []byte) (Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return m, fmt.Errorf("mediatools: manifest json: %w", err)
	}
	if m.Schema != SchemaVersion {
		return m, fmt.Errorf("mediatools: manifest schema %d != supported %d", m.Schema, SchemaVersion)
	}
	if len(m.Platforms) == 0 {
		return m, fmt.Errorf("mediatools: manifest has no platforms")
	}
	for key, p := range m.Platforms {
		if err := validatePack(key, p); err != nil {
			return m, err
		}
	}
	return m, nil
}

func validatePack(key string, p PlatformPack) error {
	if p.Platform == "" || p.Platform != key {
		return fmt.Errorf("mediatools: manifest platform key %q mismatched with entry %q", key, p.Platform)
	}
	if !strings.HasPrefix(p.URL, "https://") {
		return fmt.Errorf("mediatools: manifest %q: URL must be HTTPS", key)
	}
	if strings.Contains(p.URL, "latest") {
		return fmt.Errorf("mediatools: manifest %q: floating \"latest\" URL is forbidden", key)
	}
	if !sha256Re.MatchString(p.SHA256) {
		return fmt.Errorf("mediatools: manifest %q: sha256 must be 64 lowercase hex chars", key)
	}
	if p.Version == "" {
		return fmt.Errorf("mediatools: manifest %q: version is empty", key)
	}
	switch p.ArchiveFormat {
	case "zip", "tar.xz":
	default:
		return fmt.Errorf("mediatools: manifest %q: unsupported archive format %q", key, p.ArchiveFormat)
	}
	if p.FFmpegPath == "" || p.FFprobePath == "" {
		return fmt.Errorf("mediatools: manifest %q: executable paths are required", key)
	}
	if err := checkSafeRelPath(p.FFmpegPath); err != nil {
		return fmt.Errorf("mediatools: manifest %q ffmpegPath: %w", key, err)
	}
	if err := checkSafeRelPath(p.FFprobePath); err != nil {
		return fmt.Errorf("mediatools: manifest %q ffprobePath: %w", key, err)
	}
	if len(p.RequiredEncoders) == 0 {
		return fmt.Errorf("mediatools: manifest %q: required encoders are empty", key)
	}
	if p.LicenseClass == "" || p.LicenseNotice == "" || p.SourceURL == "" {
		return fmt.Errorf("mediatools: manifest %q: license/provenance fields are required", key)
	}
	return nil
}

// checkSafeRelPath rejects absolute paths, traversal and drive prefixes.
func checkSafeRelPath(p string) error {
	if p == "" {
		return fmt.Errorf("empty path")
	}
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, "\\") {
		return fmt.Errorf("absolute path %q", p)
	}
	if len(p) >= 2 && p[1] == ':' {
		return fmt.Errorf("drive-prefixed path %q", p)
	}
	if strings.Contains(p, "\\") {
		return fmt.Errorf("backslash path %q", p)
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return fmt.Errorf("traversal in path %q", p)
		}
	}
	return nil
}

// PackFor returns the pinned pack for the running platform (or an
// unsupported-platform error naming the platform).
func PackFor(m Manifest, goos, arch string) (PlatformPack, error) {
	key := goos + "/" + arch
	p, ok := m.Platforms[key]
	if !ok {
		return PlatformPack{}, fmt.Errorf("mediatools: no pinned media-tool pack for %s (supported: %s)", key, supportedPlatforms(m))
	}
	return p, nil
}

func supportedPlatforms(m Manifest) string {
	keys := make([]string, 0, len(m.Platforms))
	for k := range m.Platforms {
		keys = append(keys, k)
	}
	return strings.Join(keys, ", ")
}
