package ghreq

import (
	"errors"
	"strings"
	"testing"
)

func validRequirement() Requirement {
	return Requirement{
		ID:            "tool-example",
		DisplayName:   "Example tool",
		Category:      CategoryMediaTool,
		Criticality:   CriticalOptional,
		Owner:         "acme",
		Repo:          "widget",
		ReleasePolicy: ReleaseStableLatest,
		AssetRules: []AssetRule{{
			Platform:     "windows/amd64",
			IncludeRegex: "^widget-win64.zip$",
		}},
		Executable:       true,
		PinnedSHA256:     strings.Repeat("ab", 32),
		LicenseID:        "MIT",
		SourceURL:        "https://github.com/acme/widget/releases",
		InstallDirRel:    "tools/widget",
		MaxExpandedBytes: 1 << 30,
	}
}

func TestCatalogValidateAcceptsWellFormedEntry(t *testing.T) {
	c := Catalog{Requirements: []Requirement{validRequirement()}}
	if err := c.Validate(); err != nil {
		t.Fatalf("valid entry rejected: %v", err)
	}
}

func TestCatalogRejections(t *testing.T) {
	tests := []struct {
		name string
		mut  func(*Requirement)
		want error
	}{
		{"duplicate id", func(r *Requirement) {}, ErrDuplicateID},
		{"invalid id", func(r *Requirement) { r.ID = "Bad ID" }, ErrInvalidID},
		{"wildcard owner", func(r *Requirement) { r.Owner = "acme..widget" }, nil},
		{"missing license", func(r *Requirement) { r.LicenseID = " " }, ErrMissingLicense},
		{"traversal install path", func(r *Requirement) { r.InstallDirRel = "../escape" }, ErrUnsafeInstallPath},
		{"absolute install path", func(r *Requirement) { r.InstallDirRel = "/etc/tool" }, ErrUnsafeInstallPath},
		{"drive prefix install path", func(r *Requirement) { r.InstallDirRel = "C:/tool" }, ErrUnsafeInstallPath},
		{"executable without digest", func(r *Requirement) { r.PinnedSHA256 = "" }, ErrExecutableWithoutDigest},
		{"no asset rules", func(r *Requirement) { r.AssetRules = nil }, ErrNoAssetRules},
		{"unanchored selector", func(r *Requirement) { r.AssetRules[0].IncludeRegex = "widget" }, ErrAmbiguousAssetSelector},
		{"bad source url", func(r *Requirement) { r.SourceURL = "http://example.com/x" }, ErrInvalidSourceURL},
		{"non-github source url", func(r *Requirement) { r.SourceURL = "https://example.com/x" }, ErrInvalidSourceURL},
		{"bad digest format", func(r *Requirement) { r.PinnedSHA256 = "deadbeef" }, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := validRequirement()
			tc.mut(&r)
			if tc.name == "bad digest format" {
				// digest malformed but executable: must fail the digest
				// contract, surfaced through Validate.
				r.Executable = true
			}
			var c *Catalog
			if tc.name == "duplicate id" {
				c = &Catalog{Requirements: []Requirement{r, r}}
			} else {
				c = &Catalog{Requirements: []Requirement{r}}
			}
			err := c.Validate()
			if tc.want == nil {
				if err == nil {
					t.Fatalf("expected rejection, got none")
				}
				return
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
}

func TestCatalogReservedDeviceNameRejected(t *testing.T) {
	r := validRequirement()
	r.InstallDirRel = "tools/con"
	c := Catalog{Requirements: []Requirement{r}}
	if err := c.Validate(); !errors.Is(err, ErrUnsafeInstallPath) {
		t.Fatalf("reserved device name accepted: %v", err)
	}
}

func TestEmbeddedCatalogIsValidAndReal(t *testing.T) {
	c := Embedded()
	if err := c.Validate(); err != nil {
		t.Fatalf("embedded catalog invalid: %v", err)
	}
	if len(c.Requirements) == 0 {
		t.Fatal("embedded catalog is empty; the media tools requirement must exist")
	}
	r := c.Requirements[0]
	if r.ID != "media-tools-ffmpeg-gpl" {
		t.Fatalf("unexpected id %q", r.ID)
	}
	if !r.Executable {
		t.Fatal("media tools install executables; the executable flag is required")
	}
	if r.Category != CategoryMediaTool || r.Criticality != CriticalOptional {
		t.Fatalf("wrong classification: %s / %s", r.Category, r.Criticality)
	}
	if len(r.AssetRules) != 2 {
		t.Fatalf("expected windows+linux rules, got %d", len(r.AssetRules))
	}
	// Executable trust: every platform rule must have a pinned digest.
	for _, ar := range r.AssetRules {
		if r.PinnedDigestFor(ar.Platform) == "" {
			t.Fatalf("platform %s has no pinned digest", ar.Platform)
		}
	}
	if !strings.Contains(r.SourceURL, "https://github.com/BtbN/FFmpeg-Builds/releases/tag/") {
		t.Fatalf("source url not canonical: %q", r.SourceURL)
	}
}

func TestPinnedDigestPlatformFallback(t *testing.T) {
	r := validRequirement()
	if got := r.PinnedDigestFor("linux/arm64"); got != strings.Repeat("ab", 32) {
		t.Fatalf("global pin fallback broken: %q", got)
	}
	r.pinnedDigests = map[string]string{"windows/amd64": strings.Repeat("cd", 32)}
	if got := r.PinnedDigestFor("windows/amd64"); got != strings.Repeat("cd", 32) {
		t.Fatalf("per-platform pin ignored: %q", got)
	}
}
