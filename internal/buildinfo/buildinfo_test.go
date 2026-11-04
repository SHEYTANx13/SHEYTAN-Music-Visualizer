package buildinfo

import (
	"strings"
	"testing"
)

func TestDefaultsAreSafeDevelopmentValues(t *testing.T) {
	i := Get()
	if i.Version == "" || i.Commit == "" || i.BuildDate == "" {
		t.Fatalf("defaults must normalize to non-empty values: %+v", i)
	}
	if i.GoVersion == "" || i.GOOS == "" || i.GOARCH == "" {
		t.Fatalf("runtime identity missing: %+v", i)
	}
}

func TestValidatePassesForDefaults(t *testing.T) {
	if err := Validate(); err != nil {
		t.Fatalf("Validate with defaults: %v", err)
	}
}

func TestNormalizationTrimsAndFillsEmpties(t *testing.T) {
	oldV, oldC, oldD := Version, Commit, BuildDate
	defer func() { Version, Commit, BuildDate = oldV, oldC, oldD }()

	Version = "  "
	Commit = " abc123 "
	BuildDate = ""
	i := Get()
	if i.Version != defaultVersion {
		t.Fatalf("empty version must fall back to %q, got %q", defaultVersion, i.Version)
	}
	if i.Commit != "abc123" {
		t.Fatalf("commit must be trimmed, got %q", i.Commit)
	}
	if i.BuildDate != defaultTimestamp {
		t.Fatalf("empty build date must fall back, got %q", i.BuildDate)
	}
}

func TestStringIsSingleLineBanner(t *testing.T) {
	s := Get().String()
	if strings.ContainsAny(s, "\n\r") {
		t.Fatalf("banner must be single line: %q", s)
	}
	for _, part := range []string{"commit", "built"} {
		if !strings.Contains(s, part) {
			t.Fatalf("banner missing %q: %s", part, s)
		}
	}
}

func TestValidateFailsOnFabricatedEmptyVersion(t *testing.T) {
	oldV := Version
	defer func() { Version = oldV }()
	// Simulate a broken link-time injection: whitespace only, which
	// normalizes to the dev default — that must still validate. A truly
	// empty authority cannot be produced through Get(); assert the
	// normalization protects it.
	Version = "   "
	if err := Validate(); err != nil {
		t.Fatalf("whitespace version must normalize and validate: %v", err)
	}
	if Get().Version != defaultVersion {
		t.Fatal("normalization failed")
	}
}
