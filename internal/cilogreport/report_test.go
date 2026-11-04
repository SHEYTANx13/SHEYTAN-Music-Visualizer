package cilogreport

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var fixedNow = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

// writeTree materializes a parts-root layout. Keys are slash paths relative
// to the parts root, values are file contents.
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func manifest(jobs []JobSpec) *Manifest {
	return &Manifest{
		Product:      "SHEYTAN Music Visualizer",
		WorkflowFile: ".github/workflows/build-desktop.yml",
		RunID:        "111222333",
		Attempt:      "1",
		Commit:       "abc1234567890abcdef1234567890abcdef1234",
		Ref:          "refs/heads/main",
		Event:        "push",
		StartedAt:    "2026-11-04T11:00:00Z",
		RunnerOS:     "test",
		Jobs:         jobs,
	}
}

func render(t *testing.T, r *Report) string {
	t.Helper()
	var buf bytes.Buffer
	if err := r.Render(&buf); err != nil {
		t.Fatalf("Render: %v", err)
	}
	return buf.String()
}

// Case 1 — all expected jobs have logs; report COMPLETE; exit 0.
func TestAllJobsHaveLogsCompleteReport(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"preflight-logs-part/preflight-build.log": "ok\n",
		"windows-logs-part/windows-build.log":     "built\n",
		"linux-logs-part/linux-build.log":         "built\n",
	})
	m := manifest([]JobSpec{
		{Name: "preflight", Result: ResultSuccess, ExpectedArtifacts: []string{"preflight-logs-part"}},
		{Name: "build-windows", Result: ResultSuccess, ExpectedArtifacts: []string{"windows-logs-part"}},
		{Name: "build-linux", Result: ResultSuccess, ExpectedArtifacts: []string{"linux-logs-part"}},
	})
	r, err := Build(m, root, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if r.Coverage != CoverageComplete {
		t.Fatalf("coverage = %s, want COMPLETE (missing=%+v)", r.Coverage, r.Missing)
	}
	out := render(t, r)
	if !strings.Contains(out, "COVERAGE: COMPLETE") {
		t.Fatal("report lacks COMPLETE statement")
	}
}

// Case 2 — a failed command appears in the error index AND the full log is
// preserved; coverage can still be COMPLETE (evidence coverage ≠ build success).
func TestFailedCommandIndexedAndLogPreserved(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"preflight-logs-part/preflight-tests.log": "go test output\nEXIT_CODE: 1 FAIL\n--- FAIL: TestSomething\nFAIL\nmore context\n",
	})
	m := manifest([]JobSpec{
		{Name: "preflight", Result: ResultFailure, ExpectedArtifacts: []string{"preflight-logs-part"}},
	})
	r, err := Build(m, root, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if r.Coverage != CoverageComplete {
		t.Fatalf("evidence coverage must be COMPLETE: %+v", r.Missing)
	}
	if len(r.Errors) < 3 {
		t.Fatalf("expected >=3 indexed error records, got %d: %+v", len(r.Errors), r.Errors)
	}
	out := render(t, r)
	for _, want := range []string{"ERROR INDEX", "EXIT_CODE: 1 FAIL", "--- FAIL: TestSomething", "more context"} {
		if !strings.Contains(out, want) {
			t.Fatalf("report missing %q", want)
		}
	}
}

// Case 3 — warning-only logs appear in the warning index.
func TestWarningsIndexed(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"linux-logs-part/linux-build.log": "WARNING: deprecated flag in use\n::warning::deprecation notice\nplain line\n",
	})
	m := manifest([]JobSpec{{Name: "build-linux", Result: ResultSuccess, ExpectedArtifacts: []string{"linux-logs-part"}}})
	r, _ := Build(m, root, fixedNow)
	if len(r.Warnings) != 2 {
		t.Fatalf("want 2 warnings, got %d", len(r.Warnings))
	}
	out := render(t, r)
	if !strings.Contains(out, "::warning::deprecation notice") {
		t.Fatal("warning index missing annotation")
	}
	if len(r.Errors) != 0 {
		t.Fatalf("warnings must not be indexed as errors: %+v", r.Errors)
	}
}

// Case 4 — an empty but present log file counts as captured and is preserved.
func TestEmptyLogFileCapturedAndPreserved(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"preflight-logs-part/empty-step.log": "",
		"preflight-logs-part/other.log":      "content\n",
	})
	m := manifest([]JobSpec{{Name: "preflight", Result: ResultSuccess, ExpectedArtifacts: []string{"preflight-logs-part"}}})
	r, _ := Build(m, root, fixedNow)
	if r.Coverage != CoverageComplete {
		t.Fatalf("empty file must still satisfy coverage, missing=%+v", r.Missing)
	}
	var empty *LogFile
	for i := range r.Files {
		if r.Files[i].RelPath == "empty-step.log" {
			empty = &r.Files[i]
		}
	}
	if empty == nil || empty.Size != 0 {
		t.Fatalf("empty file not preserved: %+v", r.Files)
	}
	out := render(t, r)
	if !strings.Contains(out, "[JOB: preflight | ARTIFACT: preflight-logs-part | FILE: empty-step.log | 0 bytes]") {
		t.Fatal("empty file not rendered with explicit header")
	}
	if !strings.Contains(out, "empty file — captured as evidence") {
		t.Fatal("empty file marker missing")
	}
}

// Case 5 — a missing artifact: report still written, job named, coverage
// INCOMPLETE, exit code 1.
func TestMissingSourceFailsClosedAfterReport(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"windows-logs-part/windows-build.log": "built\n",
	})
	m := manifest([]JobSpec{
		{Name: "build-windows", Result: ResultSuccess, ExpectedArtifacts: []string{"windows-logs-part"}},
		{Name: "build-linux", Result: ResultSuccess, ExpectedArtifacts: []string{"linux-logs-part"}},
	})
	reportPath := filepath.Join(t.TempDir(), "out", "report.txt")
	_, code, err := Execute(m, root, reportPath, fixedNow)
	if err != nil {
		t.Fatalf("Execute returned internal error: %v", err)
	}
	if code != ExitIncomplete {
		t.Fatalf("exit = %d, want %d", code, ExitIncomplete)
	}
	raw, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("report not written: %v", err)
	}
	out := string(raw)
	if !strings.Contains(out, "MISSING LOG SOURCE") || !strings.Contains(out, `job="build-linux"`) {
		t.Fatal("report must name the missing job")
	}
	if !strings.Contains(out, "COVERAGE: INCOMPLETE") {
		t.Fatal("coverage must be INCOMPLETE")
	}
}

// Case 6 — a skipped job appears under NEVER_STARTED, not as missing/failed.
func TestSkippedJobIsNeverStarted(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"preflight-logs-part/preflight-build.log": "ok\n",
	})
	m := manifest([]JobSpec{
		{Name: "preflight", Result: ResultSuccess, ExpectedArtifacts: []string{"preflight-logs-part"}},
		{Name: "build-windows", Result: ResultSkipped, ExpectedArtifacts: []string{"windows-logs-part"}},
	})
	r, _ := Build(m, root, fixedNow)
	if r.Coverage != CoverageComplete {
		t.Fatalf("skipped job must not create missing sources: %+v", r.Missing)
	}
	if len(r.NeverStarted) != 1 || r.NeverStarted[0] != "build-windows" {
		t.Fatalf("NEVER_STARTED wrong: %+v", r.NeverStarted)
	}
	out := render(t, r)
	if !strings.Contains(out, "NEVER_STARTED") || !strings.Contains(out, "build-windows: SKIPPED / NEVER_STARTED") {
		t.Fatal("report lacks NEVER_STARTED section entry")
	}
	if strings.Contains(out, `job="build-windows" artifact=`) {
		t.Fatal("skipped job wrongly reported as missing source")
	}
}

// Case 7 — same basenames in two namespaces are both retained, no overwrite.
func TestSameBasenameInTwoNamespacesRetained(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"windows-logs-part/compiler.log": "windows compiler log\n",
		"linux-logs-part/compiler.log":   "linux compiler log\n",
	})
	m := manifest([]JobSpec{
		{Name: "build-windows", Result: ResultSuccess, ExpectedArtifacts: []string{"windows-logs-part"}},
		{Name: "build-linux", Result: ResultSuccess, ExpectedArtifacts: []string{"linux-logs-part"}},
	})
	r, _ := Build(m, root, fixedNow)
	if len(r.Files) != 2 {
		t.Fatalf("want 2 files, got %+v", r.Files)
	}
	out := render(t, r)
	if !strings.Contains(out, "windows compiler log") || !strings.Contains(out, "linux compiler log") {
		t.Fatal("one namespace overwrote the other")
	}
	if !strings.Contains(out, "ARTIFACT: windows-logs-part | FILE: compiler.log") ||
		!strings.Contains(out, "ARTIFACT: linux-logs-part | FILE: compiler.log") {
		t.Fatal("namespaced headers missing")
	}
}

// Case 8 — multiple log files sort deterministically (lexicographic).
func TestDeterministicFileOrder(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"preflight-logs-part/c-third.log":  "c\n",
		"preflight-logs-part/a-first.log":  "a\n",
		"preflight-logs-part/b-second.log": "b\n",
	})
	m := manifest([]JobSpec{{Name: "preflight", Result: ResultSuccess, ExpectedArtifacts: []string{"preflight-logs-part"}}})
	var got []string
	for i := 0; i < 3; i++ {
		r, err := Build(m, root, fixedNow)
		if err != nil {
			t.Fatal(err)
		}
		got = nil
		for _, f := range r.Files {
			got = append(got, f.RelPath)
		}
	}
	want := []string{"a-first.log", "b-second.log", "c-third.log"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order %v, want %v", got, want)
		}
	}
}

// Case 9 — a red run: one failed job, one missing source, one skipped job;
// all three distinct facts appear accurately.
func TestRedRunDistinguishesFailedMissingSkipped(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"preflight-logs-part/preflight-tests.log": "EXIT_CODE: 2 FAIL\nERROR: go vet flagged something\n",
	})
	m := manifest([]JobSpec{
		{Name: "preflight", Result: ResultFailure, ExpectedArtifacts: []string{"preflight-logs-part"}},
		{Name: "build-windows", Result: ResultSkipped, ExpectedArtifacts: []string{"windows-logs-part"}},
		{Name: "build-linux", Result: ResultSuccess, ExpectedArtifacts: []string{"linux-logs-part"}},
	})
	r, _ := Build(m, root, fixedNow)
	if r.Coverage != CoverageIncomplete {
		t.Fatalf("coverage=%s, want INCOMPLETE", r.Coverage)
	}
	if r.CountFailure != 1 || r.CountSkipped != 1 || r.CountSuccess != 1 {
		t.Fatalf("outcome counts wrong: %+v", r)
	}
	if len(r.Missing) != 1 || r.Missing[0].Job != "build-linux" {
		t.Fatalf("missing source wrong: %+v", r.Missing)
	}
	if len(r.NeverStarted) != 1 || r.NeverStarted[0] != "build-windows" {
		t.Fatalf("never started wrong: %+v", r.NeverStarted)
	}
	if r.FailedCommandRecords < 1 {
		t.Fatal("failed command record not counted")
	}
	out := render(t, r)
	for _, want := range []string{"COVERAGE: INCOMPLETE", "MISSING LOG SOURCE", "NEVER_STARTED", "ERROR INDEX"} {
		if !strings.Contains(out, want) {
			t.Fatalf("report missing %q", want)
		}
	}
}

// Case 10 — unicode and filenames with spaces are preserved.
func TestUnicodeAndSpacesPreserved(t *testing.T) {
	root := t.TempDir()
	name := "шейтан build log — مرحلة 1.log"
	writeTree(t, root, map[string]string{
		"preflight-logs-part/" + name: "content with unicode ✓\n",
	})
	m := manifest([]JobSpec{{Name: "preflight", Result: ResultSuccess, ExpectedArtifacts: []string{"preflight-logs-part"}}})
	r, _ := Build(m, root, fixedNow)
	found := false
	for _, f := range r.Files {
		if f.RelPath == name {
			found = true
		}
	}
	if !found {
		t.Fatalf("spaced/unicode filename lost: %+v", r.Files)
	}
	out := render(t, r)
	if !strings.Contains(out, name) || !strings.Contains(out, "content with unicode ✓") {
		t.Fatal("unicode content or filename not preserved in report")
	}
}

// Manifest hygiene — duplicate/empty job names and bad results fail loudly.
func TestManifestValidation(t *testing.T) {
	dir := t.TempDir()
	bad := []Manifest{
		{WorkflowFile: "w.yml", Jobs: []JobSpec{{Name: "", Result: "success"}}},
		{WorkflowFile: "w.yml", Jobs: []JobSpec{
			{Name: "a", Result: "success"}, {Name: "a", Result: "success"},
		}},
		{WorkflowFile: "w.yml", Jobs: []JobSpec{{Name: "a", Result: "exploded"}}},
		{WorkflowFile: "", Jobs: []JobSpec{{Name: "a", Result: "success"}}},
	}
	for i, m := range bad {
		p := filepath.Join(dir, "m.json")
		raw, _ := json.Marshal(m)
		if err := os.WriteFile(p, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadManifest(p); err == nil {
			t.Fatalf("case %d: expected validation error", i)
		}
	}
}

// Strict manifest decoding — unknown fields are rejected.
func TestManifestStrictFields(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "m.json")
	if err := os.WriteFile(p, []byte(`{"workflow_file":"w.yml","typo_field":1,"jobs":[{"name":"a","result":"success"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadManifest(p); err == nil {
		t.Fatal("unknown manifest field must be rejected")
	}
}
