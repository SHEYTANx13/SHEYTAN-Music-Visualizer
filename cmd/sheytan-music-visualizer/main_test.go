package main

// main_test.go — contract tests for the non-GUI commands. The package
// status observers consume --build-info-json as the machine-readable
// commit authority; these tests pin its exact shape and honesty.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/buildinfo"
)

func TestBuildInfoJSONCommand(t *testing.T) {
	var out strings.Builder
	var errW strings.Builder
	rc := run([]string{"--build-info-json"}, &out, &errW)
	if rc != 0 {
		t.Fatalf("--build-info-json exit = %d, want 0 (stderr: %s)", rc, errW.String())
	}
	line := strings.TrimRight(out.String(), "\r\n")
	if strings.Contains(line, "\n") {
		t.Fatalf("build-info-json must be one line, got %q", out.String())
	}
	var snap map[string]string
	if err := json.Unmarshal([]byte(line), &snap); err != nil {
		t.Fatalf("output is not JSON: %v (%q)", err, line)
	}
	n := buildinfo.Normalized()
	for _, f := range []string{"version", "commit", "date", "built_by", "go_version", "os", "arch"} {
		v, ok := snap[f]
		if !ok {
			t.Fatalf("JSON missing field %q: %s", f, line)
		}
		if strings.TrimSpace(v) == "" {
			t.Fatalf("JSON field %q is empty", f)
		}
	}
	if snap["commit"] != n.Commit {
		t.Fatalf("commit mismatch: JSON %q vs buildinfo %q", snap["commit"], n.Commit)
	}
}

func TestVersionCommandStillHumanReadable(t *testing.T) {
	var out strings.Builder
	var errW strings.Builder
	rc := run([]string{"--version"}, &out, &errW)
	if rc != 0 {
		t.Fatalf("--version exit = %d, want 0", rc)
	}
	first := strings.SplitN(out.String(), "\n", 2)[0]
	if !strings.Contains(first, buildinfo.Normalized().Commit) {
		t.Fatalf("human --version lost the commit: %q", first)
	}
}

func TestUnexpectedArgStillRejected(t *testing.T) {
	var out, errW strings.Builder
	if rc := run([]string{"--bogus-flag"}, &out, &errW); rc == 0 {
		t.Fatal("unknown flag must exit nonzero")
	}
}
