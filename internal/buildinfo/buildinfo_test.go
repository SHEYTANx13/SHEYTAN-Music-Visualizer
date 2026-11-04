package buildinfo

import (
	"regexp"
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
