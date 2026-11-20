package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"strings"
	"testing"
)

// TestParseArgs verifies the flag mapping and the usage errors.
func TestParseArgs(t *testing.T) {
	cases := []struct {
		args    []string
		want    mode
		wantErr bool
	}{
		{[]string{}, mode{Run: true}, false},
		{[]string{"--version"}, mode{Version: true}, false},
		{[]string{"--build-info-json"}, mode{BuildInfoJSON: true}, false},
		{[]string{"--self-check"}, mode{SelfCheck: true}, false},
		{[]string{"--smoke", "40"}, mode{Smoke: 40}, false},
		{[]string{"--smoke", "1"}, mode{Smoke: 1}, false},
		{[]string{"--smoke", "-1"}, mode{}, true},
		{[]string{"--bogus"}, mode{}, true},
		{[]string{"stray"}, mode{}, true},
	}
	for _, c := range cases {
		fs := flag.NewFlagSet("test", flag.ContinueOnError)
		fs.SetOutput(new(bytes.Buffer))
		got, err := parseArgs(c.args, fs)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseArgs(%v) expected error, got %+v", c.args, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseArgs(%v) unexpected error: %v", c.args, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseArgs(%v) = %+v, want %+v", c.args, got, c.want)
		}
	}
}

// TestRunVersion verifies the --version output carries the product
// identity and the two-party attribution.
func TestRunVersion(t *testing.T) {
	var out, errW bytes.Buffer
	if rc := run([]string{"--version"}, &out, &errW); rc != 0 {
		t.Fatalf("--version exit = %d, stderr: %s", rc, errW.String())
	}
	text := out.String()
	for _, want := range []string{"SHEYTAN Music Visualizer", "Parsaetak", "BETA"} {
		if !strings.Contains(text, want) {
			t.Errorf("--version output missing %q", want)
		}
	}
}

// TestRunBuildInfoJSON verifies the machine-readable build snapshot is
// exactly one JSON line.
func TestRunBuildInfoJSON(t *testing.T) {
	var out, errW bytes.Buffer
	if rc := run([]string{"--build-info-json"}, &out, &errW); rc != 0 {
		t.Fatalf("--build-info-json exit = %d, stderr: %s", rc, errW.String())
	}
	var doc map[string]string
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &doc); err != nil {
		t.Fatalf("build info json: %v (%s)", err, out.String())
	}
}

// TestSelfCheckPureParts runs the display-less self-check functions:
// build identity, token document, destination catalog and the REAL
// renderer selection with one deterministic CPU frame. No window is
// created; the layout-store check (real user paths) is deliberately
// out of the test scope — the shell package covers it via t.TempDir().
func TestSelfCheckPureParts(t *testing.T) {
	var out bytes.Buffer
	if err := checkBuildInfo(&out); err != nil {
		t.Errorf("checkBuildInfo: %v", err)
	}
	if err := checkDesignTokens(&out); err != nil {
		t.Errorf("checkDesignTokens: %v", err)
	}
	if err := checkDestinationCatalog(&out); err != nil {
		t.Errorf("checkDestinationCatalog: %v", err)
	}
	if err := checkRendererFrame(&out); err != nil {
		t.Errorf("checkRendererFrame: %v", err)
	}
	if !strings.Contains(out.String(), "backend=") {
		t.Errorf("renderer evidence line missing: %s", out.String())
	}
}

// TestSampleFrame verifies the deterministic frame input used by the
// renderer check.
func TestSampleFrame(t *testing.T) {
	in := sampleFrame(96, 64)
	if in.Width != 96 || in.Height != 64 {
		t.Errorf("sample frame size = %dx%d", in.Width, in.Height)
	}
	if len(in.Audio.Bins) == 0 {
		t.Error("sample frame needs spectrum bins")
	}
}
