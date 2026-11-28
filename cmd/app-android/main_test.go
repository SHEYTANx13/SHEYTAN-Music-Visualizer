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

// TestRunJSONMode verifies the machine-readable build snapshot.
func TestRunJSONMode(t *testing.T) {
	var out bytes.Buffer
	rc := run([]string{"--build-info-json"}, &out, new(bytes.Buffer), nil)
	if rc != 0 {
		t.Fatalf("run(--build-info-json) = %d, want 0", rc)
	}
	line := strings.TrimSpace(out.String())
	var doc map[string]any
	if err := json.Unmarshal([]byte(line), &doc); err != nil {
		t.Fatalf("build-info-json is not one JSON line: %v (%s)", err, line)
	}
	for _, key := range []string{"version", "commit", "date", "built_by", "go_version", "os", "arch"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("build-info-json lacks %q", key)
		}
	}
}

// TestRunVersionMode verifies the identity line + attribution block.
func TestRunVersionMode(t *testing.T) {
	var out bytes.Buffer
	rc := run([]string{"--version"}, &out, new(bytes.Buffer), nil)
	if rc != 0 {
		t.Fatalf("run(--version) = %d, want 0", rc)
	}
	if !strings.Contains(out.String(), "SHEYTAN") {
		t.Errorf("version output lacks the product identity: %s", out.String())
	}
}
