package buildinfo

import (
	"encoding/json"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

var control = regexp.MustCompile(`[\x00-\x1f\x7f]`)

func TestDefaultsAreSafe(t *testing.T) {
	i := Normalized()
	if i.Version == "" || i.Commit == "" || i.Date == "" || i.BuiltBy == "" || i.GoVersion == "" {
		t.Fatalf("normalized info contains empty fields: %+v", i)
	}
	if strings.ContainsAny(i.Version, "\n\r") {
		t.Fatalf("version must be single-line: %q", i.Version)
	}
}

func TestLineIsSingleLine(t *testing.T) {
	line := Line()
	if strings.Contains(line, "\n") {
		t.Fatalf("Line() must be single-line: %q", line)
	}
	if !strings.Contains(line, "commit") || !strings.Contains(line, "go") {
		t.Fatalf("Line() missing metadata: %q", line)
	}
}

func TestSanitizeCollapsesControlCharacters(t *testing.T) {
	if got := sanitize("1.2.3\nEVIL"); control.MatchString(got) {
		t.Fatalf("sanitize left control characters: %q", got)
	}
	if got := sanitize("  trimmed  "); got != "trimmed" {
		t.Fatalf("sanitize must trim: %q", got)
	}
}

func TestJSONSnapshotContract(t *testing.T) {
	var snap struct {
		Version   string `json:"version"`
		Commit    string `json:"commit"`
		Date      string `json:"date"`
		BuiltBy   string `json:"built_by"`
		GoVersion string `json:"go_version"`
		OS        string `json:"os"`
		Arch      string `json:"arch"`
	}
	if err := json.Unmarshal(JSON(), &snap); err != nil {
		t.Fatalf("JSON() is not valid JSON: %v", err)
	}
	n := Normalized()
	if snap.Version != n.Version || snap.Commit != n.Commit || snap.Date != n.Date ||
		snap.BuiltBy != n.BuiltBy || snap.GoVersion != n.GoVersion {
		t.Fatalf("JSON fields diverge from Normalized: %+v vs %+v", snap, n)
	}
	for name, v := range map[string]string{
		"version": snap.Version, "commit": snap.Commit, "date": snap.Date,
		"built_by": snap.BuiltBy, "go_version": snap.GoVersion,
		"os": snap.OS, "arch": snap.Arch,
	} {
		if strings.TrimSpace(v) == "" {
			t.Fatalf("JSON field %s is empty", name)
		}
	}
	if strings.ContainsAny(snap.Commit, "\r\n") {
		t.Fatalf("commit must stay single-line in JSON: %q", snap.Commit)
	}
	if snap.OS != runtime.GOOS || snap.Arch != runtime.GOARCH {
		t.Fatalf("os/arch mismatch: %s/%s vs %s/%s", snap.OS, snap.Arch, runtime.GOOS, runtime.GOARCH)
	}
}
