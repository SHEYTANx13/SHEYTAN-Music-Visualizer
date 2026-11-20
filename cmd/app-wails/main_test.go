//go:build purego && sheytan_nogpu

package main


// main_test.go — flag parsing and pure helper coverage only. No
// window, no webview, no studio runtime: the shell itself cannot run
// on a display-less machine, so these tests pin the CLI contract that
// CI and packaging scripts rely on.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/brand"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/buildinfo"
)

func TestVersionFlagPrintsIdentity(t *testing.T) {
	var out, errW strings.Builder
	rc := run([]string{"--version"}, &out, &errW)
	if rc != 0 {
		t.Fatalf("run(--version) rc = %d, want 0 (stderr: %s)", rc, errW.String())
	}
	got := out.String()
	if !strings.Contains(got, brand.ProductName) {
		t.Errorf("version output missing product name %q: %q", brand.ProductName, got)
	}
	if !strings.Contains(got, buildinfo.Normalized().Version) {
		t.Errorf("version output missing build version %q: %q", buildinfo.Normalized().Version, got)
	}
	// Attribution block: licensor and project home must be present.
	if !strings.Contains(got, brand.Publisher) {
		t.Errorf("version output missing licensor attribution %q: %q", brand.Publisher, got)
	}
	if errW.String() != "" {
		t.Errorf("unexpected stderr output: %q", errW.String())
	}
}

func TestBuildInfoJSONFlagPrintsOneJSONLine(t *testing.T) {
	var out, errW strings.Builder
	rc := run([]string{"--build-info-json"}, &out, &errW)
	if rc != 0 {
		t.Fatalf("run(--build-info-json) rc = %d, want 0 (stderr: %s)", rc, errW.String())
	}
	line := strings.TrimSpace(out.String())
	if strings.ContainsAny(line, "\n") {
		t.Errorf("build-info-json must be ONE line, got: %q", line)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(line), &decoded); err != nil {
		t.Fatalf("build-info-json is not valid JSON: %v (line %q)", err, line)
	}
	// The machine-readable contract consumed by typed CI decoders.
	for _, key := range []string{"version", "commit", "date", "built_by", "go_version", "os", "arch"} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("build-info-json missing stable key %q in: %v", key, decoded)
		}
	}
	if decoded["version"] != buildinfo.Normalized().Version {
		t.Errorf("build-info-json version = %v, want %q", decoded["version"], buildinfo.Normalized().Version)
	}
}

func TestUnexpectedArgumentsRejected(t *testing.T) {
	var out, errW strings.Builder
	rc := run([]string{"extra-arg"}, &out, &errW)
	if rc != 2 {
		t.Errorf("run(extra-arg) rc = %d, want 2", rc)
	}
	if !strings.Contains(errW.String(), "unexpected arguments") {
		t.Errorf("stderr should explain the rejection, got: %q", errW.String())
	}
}

func TestUnknownFlagRejected(t *testing.T) {
	var out, errW strings.Builder
	rc := run([]string{"--definitely-not-a-flag"}, &out, &errW)
	if rc != 2 {
		t.Errorf("run(--definitely-not-a-flag) rc = %d, want 2", rc)
	}
}

func TestVersionAndBuildInfoAreMutuallyExclusivePaths(t *testing.T) {
	// Both flags set: the documented precedence is build-info-json
	// first (mirrors cmd/sheytan-music-visualizer switch order). The
	// test pins the precedence so packaging scripts stay deterministic.
	var out, errW strings.Builder
	rc := run([]string{"--version", "--build-info-json"}, &out, &errW)
	if rc != 0 {
		t.Fatalf("run(--version --build-info-json) rc = %d, want 0", rc)
	}
	if !strings.HasPrefix(strings.TrimSpace(out.String()), "{") {
		t.Errorf("build-info-json should win the precedence, got: %q", out.String())
	}
}
