package mediatools

// paths.go — app-managed media-tool directory layout (run 43712071111):
//
//	<DataRoot>/media-tools/current/bin/ffmpeg[.exe]   active pair
//	<DataRoot>/media-tools/current/bin/ffprobe[.exe]
//	<DataRoot>/media-tools/current/manifest.json      installed pack identity
//	<DataRoot>/media-tools/current/licenses/…         provenance/license copies
//	<DataRoot>/media-tools/previous/…                 last known-good (rollback)
//	<DataRoot>/media-tools/.tmp-<rand>/…              in-flight install staging
//
// The resolver order (internal/fflocate) treats current/bin as the
// app-managed location: user-selected > app-managed > app-local
// portable > system PATH.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
)

// Dir layout helpers.
func Root(dataRoot string) string        { return filepath.Join(dataRoot, "media-tools") }
func CurrentDir(dataRoot string) string  { return filepath.Join(Root(dataRoot), "current") }
func PreviousDir(dataRoot string) string { return filepath.Join(Root(dataRoot), "previous") }
func CurrentBin(dataRoot string) string  { return filepath.Join(CurrentDir(dataRoot), "bin") }

// InstalledManifestPath is the pack identity record inside current/.
func InstalledManifestPath(dataRoot string) string {
	return filepath.Join(CurrentDir(dataRoot), "manifest.json")
}

// LicensesDir holds the provenance/license copies inside current/.
func LicensesDir(dataRoot string) string {
	return filepath.Join(CurrentDir(dataRoot), "licenses")
}

// BinNames returns the platform executable names for the pair.
func BinNames(goos string) (ffmpeg, ffprobe string) {
	if goos == "windows" {
		return "ffmpeg.exe", "ffprobe.exe"
	}
	return "ffmpeg", "ffprobe"
}

// InstalledManifest is the record persisted inside current/.
type InstalledManifest struct {
	Platform    string       `json:"platform"`
	Version     string       `json:"version"`
	URL         string       `json:"url"`
	SHA256      string       `json:"sha256"`
	License     string       `json:"license"`
	SourceURL   string       `json:"sourceUrl"`
	InstalledAt string       `json:"installedAt"`
	Encoders    []string     `json:"encoders"`
	Pack        PlatformPack `json:"pack"`
}

// InstalledInfo is the inspected state of an installed pack.
type InstalledInfo struct {
	Manifest InstalledManifest
	FFmpeg   string
	FFprobe  string
}

// InspectInstalled reads current/ and reports what is actually there
// (filesystem evidence only; it never runs the executables).
func InspectInstalled(dataRoot string) (*InstalledInfo, error) {
	bin := CurrentBin(dataRoot)
	ff, fp := BinNames(runtime.GOOS)
	ffPath := filepath.Join(bin, ff)
	fpPath := filepath.Join(bin, fp)
	if !isRegular(ffPath) || !isRegular(fpPath) {
		return nil, fmt.Errorf("mediatools: no app-managed tool pair in %s", bin)
	}
	raw, err := os.ReadFile(InstalledManifestPath(dataRoot))
	if err != nil {
		return nil, fmt.Errorf("mediatools: installed manifest unreadable: %w", err)
	}
	var im InstalledManifest
	if err := json.Unmarshal(raw, &im); err != nil {
		return nil, fmt.Errorf("mediatools: installed manifest invalid: %w", err)
	}
	return &InstalledInfo{Manifest: im, FFmpeg: ffPath, FFprobe: fpPath}, nil
}

// HumanBytes renders a byte count for UI display.
func HumanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

func isRegular(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

// sortedEncoders returns a deterministic encoder list.
func sortedEncoders(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		if v {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
