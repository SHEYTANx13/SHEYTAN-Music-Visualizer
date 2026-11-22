package main

// main_test.go — end-to-end CLI contract tests for ci-log-collector
// (previously an untested binary). They pin the exit-code contract
// (0/1/2/3), the warning/error-only artifact, the empty-file-is-clean
// rule, secret redaction, and the record-limit fail-closed behavior.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture writes a manifest plus parts tree and returns the CLI args.
func fixture(t *testing.T, manifest string, artifacts map[string]map[string]string) []string {
	t.Helper()
	root := t.TempDir()
	mp := filepath.Join(root, "manifest.json")
	if err := os.WriteFile(mp, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, files := range artifacts {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for rel, content := range files {
			p := filepath.Join(dir, rel)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	report := filepath.Join(t.TempDir(), "log.txt")
	summary := filepath.Join(t.TempDir(), "summary.txt")
	return []string{"--report", report, "--summary", summary, "--parts-root", root, "--manifest", mp}
}

const greenManifest = `{"product":"SHEYTAN Music Visualizer","workflow":"build.yml","run_id":"1","attempt":"1",
 "jobs":[{"name":"preflight","result":"success","artifact":"preflight-logs-part","required":true}]}`

func TestRunGreenRunEmptyArtifactExitZero(t *testing.T) {
	args := fixture(t, greenManifest, map[string]map[string]string{
		"preflight-logs-part": {"p.log": "all gates pass\nEXIT_CODE: 0 PASS\n"},
	})
	var out, errW strings.Builder
	if rc := run(args, &out, &errW); rc != 0 {
		t.Fatalf("rc=%d err=%q", rc, errW.String())
	}
	reportPath := args[1]
	b, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 0 {
		t.Fatalf("clean run must produce an EMPTY artifact, got %q", b)
	}
	if !strings.Contains(out.String(), "error-records=0; warning-records=0") {
		t.Fatalf("summary line must report zero records: %q", out.String())
	}
	// The summary file carries the metadata instead.
	s, err := os.ReadFile(args[3])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(s), "Coverage: COMPLETE") || !strings.Contains(string(s), "RUN METADATA") {
		t.Fatalf("summary missing metadata: %q", s)
	}
	if strings.Contains(string(s), "#### LOG CONTENT") {
		t.Fatal("summary must never embed raw logs")
	}
}

func TestRunFailedRequiredJobExitThree(t *testing.T) {
	args := fixture(t, `{"product":"x","workflow":"build.yml","run_id":"1","attempt":"1",
 "jobs":[{"name":"build-windows","result":"failure","artifact":"windows-logs-part","required":true}]}`,
		map[string]map[string]string{
			"windows-logs-part": {"w.log": "no textual error at all\n"},
		})
	var out, errW strings.Builder
	if rc := run(args, &out, &errW); rc != 3 {
		t.Fatalf("rc=%d, want 3 (failed required job)", rc)
	}
	b, _ := os.ReadFile(args[1])
	if !strings.Contains(string(b), "message=required job did not succeed: workflow conclusion failure") {
		t.Fatalf("authoritative failure record missing:\n%s", b)
	}
}

func TestRunMissingSourceExitOne(t *testing.T) {
	args := fixture(t, greenManifest, nil) // artifact never uploaded
	var out, errW strings.Builder
	if rc := run(args, &out, &errW); rc != 1 {
		t.Fatalf("rc=%d, want 1 (incomplete coverage)", rc)
	}
	b, _ := os.ReadFile(args[1])
	if !strings.Contains(string(b), "missing required log source: preflight") {
		t.Fatalf("missing-source record missing:\n%s", b)
	}
}

func TestRunHardErrorsExitTwo(t *testing.T) {
	// Missing manifest.
	root := t.TempDir()
	var out, errW strings.Builder
	args := []string{"--report", filepath.Join(t.TempDir(), "log.txt"), "--parts-root", root,
		"--manifest", filepath.Join(root, "absent.json")}
	if rc := run(args, &out, &errW); rc != 2 || !strings.Contains(errW.String(), "HARD_ERROR manifest-missing") {
		t.Fatalf("rc=%d err=%q", rc, errW.String())
	}

	// Zero-byte manifest.
	mp := filepath.Join(root, "empty.json")
	if err := os.WriteFile(mp, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	errW.Reset()
	args = []string{"--report", filepath.Join(t.TempDir(), "log.txt"), "--parts-root", root, "--manifest", mp}
	if rc := run(args, &out, &errW); rc != 2 || !strings.Contains(errW.String(), "HARD_ERROR manifest-empty") {
		t.Fatalf("rc=%d err=%q", rc, errW.String())
	}

	// Missing required flags.
	errW.Reset()
	if rc := run([]string{"--report", "x"}, &out, &errW); rc != 2 {
		t.Fatalf("flag error must be rc=2, got %d", rc)
	}
}

func TestRunSecretsNeverLeak(t *testing.T) {
	secret := "ghs_supersecrettokenvalue12345678"
	args := fixture(t, greenManifest, map[string]map[string]string{
		"preflight-logs-part": {"p.log": "::error::auth failed token=" + secret + "\n"},
	})
	var out, errW strings.Builder
	if rc := run(args, &out, &errW); rc != 0 {
		t.Fatalf("rc=%d (a warning record does not fail the run)", rc)
	}
	b, _ := os.ReadFile(args[1])
	if strings.Contains(string(b), secret) {
		t.Fatal("secret leaked into the artifact")
	}
	if !strings.Contains(string(b), "***REDACTED***") {
		t.Fatalf("redaction marker missing:\n%s", b)
	}
}

func TestRunRecordLimitExceededIsHardError(t *testing.T) {
	// A record set over the 256 KiB ceiling must fail the collector
	// CLOSED (exit 2, no partial artifact written).
	var files strings.Builder
	for i := 0; i < 3000; i++ {
		files.WriteString("::error::distinct failure number " + strings.Repeat("x", 60) + "-" + string(rune('a'+i%26)) + "-" + itoa(i) + "\n")
	}
	args := fixture(t, greenManifest, map[string]map[string]string{
		"preflight-logs-part": {"p.log": files.String()},
	})
	var out, errW strings.Builder
	rc := run(args, &out, &errW)
	if rc != 2 {
		t.Fatalf("over-limit record set must fail closed with rc=2, got %d", rc)
	}
	if !strings.Contains(errW.String(), "diagnostic record limit exceeded") {
		t.Fatalf("overflow explanation missing: %q", errW.String())
	}
	if _, err := os.Stat(args[1]); !os.IsNotExist(err) {
		t.Fatal("no partial artifact may exist after a fail-closed overflow")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func TestRunArtifactNeverContainsRawLogs(t *testing.T) {
	raw := ":: COMMAND BEGIN ::\nSTEP: go-test\nsome ordinary command chatter line\n:: COMMAND END ::\nEXIT_CODE: 0 PASS\n"
	args := fixture(t, greenManifest, map[string]map[string]string{
		"preflight-logs-part": {"p.log": raw},
	})
	var out, errW strings.Builder
	if rc := run(args, &out, &errW); rc != 0 {
		t.Fatalf("rc=%d err=%q", rc, errW.String())
	}
	b, _ := os.ReadFile(args[1])
	for _, banned := range []string{"COMMAND BEGIN", "command chatter", "EXIT_CODE"} {
		if strings.Contains(string(b), banned) {
			t.Fatalf("raw log text %q leaked into the artifact", banned)
		}
	}
}
