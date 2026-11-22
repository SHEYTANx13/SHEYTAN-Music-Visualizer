package main

// main_test.go — exit-code and contract-gate tests for civalidate
// (previously an untested binary; the collector and validator are the
// fail-closed anchors of the diagnostics contract).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunCleanFilesExitZero(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, dir, "a.log", "plain ascii\nsecond line\n")
	b := writeFile(t, dir, "b.log", "")
	var out, errW strings.Builder
	if rc := run([]string{a, b}, &out, &errW); rc != 0 {
		t.Fatalf("rc=%d out=%q err=%q", rc, out.String(), errW.String())
	}
	if !strings.Contains(out.String(), "checked 2 file(s), 0 violation(s)") {
		t.Fatalf("unexpected output: %q", out.String())
	}
}

func TestRunViolationExitsOneWithMetrics(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "bad.log", "unicode \xC3\xA9 here\n===\n\x1B[31mred\n")
	var out, errW strings.Builder
	if rc := run([]string{p}, &out, &errW); rc != 1 {
		t.Fatalf("rc=%d, want 1 (out=%q err=%q)", rc, out.String(), errW.String())
	}
	if !strings.Contains(out.String(), "VIOLATION:") {
		t.Fatalf("violation not reported: %q", out.String())
	}
	for _, metric := range []string{"non_ascii=2", "ansi_escape=1", "seq_equals=1"} {
		if !strings.Contains(out.String(), metric) {
			t.Fatalf("metric %s missing: %q", metric, out.String())
		}
	}
}

func TestRunStrictFlagsTabsAndCR(t *testing.T) {
	dir := t.TempDir()
	// Tabs are legal in captured logs but violations in the strict
	// final-artifact contract.
	p := writeFile(t, dir, "tabs.log", "a\tb\nc\n")
	var out, errW strings.Builder
	if rc := run([]string{p}, &out, &errW); rc != 0 {
		t.Fatalf("default contract must allow tabs, rc=%d out=%q", rc, out.String())
	}
	out.Reset()
	if rc := run([]string{"--strict", p}, &out, &errW); rc != 1 {
		t.Fatalf("strict contract must flag tabs+CR, rc=%d out=%q", rc, out.String())
	}
	if !strings.Contains(out.String(), "tabs=1") {
		t.Fatalf("strict tab metric missing: %q", out.String())
	}
}

func TestRunMissingPathExitsOne(t *testing.T) {
	var out, errW strings.Builder
	if rc := run([]string{filepath.Join(t.TempDir(), "missing.log")}, &out, &errW); rc != 1 {
		t.Fatalf("missing path must be a violation, rc=%d", rc)
	}
}

func TestRunNoArgsExitTwo(t *testing.T) {
	var out, errW strings.Builder
	if rc := run(nil, &out, &errW); rc != 2 {
		t.Fatalf("no args must be a usage error, rc=%d", rc)
	}
}

func TestRunWalksDirectoriesSorted(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "b.log", "===\n") // violation
	writeFile(t, dir, "a.log", "clean\n")
	writeFile(t, dir, "sub/c.log", "clean\n")
	var out, errW strings.Builder
	if rc := run([]string{dir}, &out, &errW); rc != 1 {
		t.Fatalf("rc=%d", rc)
	}
	if !strings.Contains(out.String(), "checked 3 file(s), 1 violation(s)") {
		t.Fatalf("walk output wrong: %q", out.String())
	}
}
