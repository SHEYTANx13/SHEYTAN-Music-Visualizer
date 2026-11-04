package cilog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var fixedNow = func() time.Time { return time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC) }

type tJob struct {
	name     string
	result   string
	artifact string
	required bool              // true = gate job whose failure must fail the run
	files    map[string]string // rel path -> content ("" = empty file)
}

func fixture(t *testing.T, jobs []tJob) (Options, string) {
	t.Helper()
	root := t.TempDir()
	opts := Options{PartsRoot: root, ReportPath: filepath.Join(t.TempDir(), "report.txt"), Now: fixedNow}
	for _, j := range jobs {
		if j.artifact == "" {
			continue
		}
		dir := filepath.Join(root, j.artifact)
		// An artifact declared with no files means the artifact was
		// never uploaded: the directory must stay absent. The empty
		// artifact-directory case is tested explicitly elsewhere.
		if len(j.files) == 0 {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for rel, content := range j.files {
			p := filepath.Join(dir, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, j := range jobs {
		opts.Meta.Jobs = append(opts.Meta.Jobs, JobSpec{Name: j.name, Result: j.result, Artifact: j.artifact, Required: j.required})
	}
	return opts, opts.ReportPath
}

func mustCollect(t *testing.T, opts Options) (Result, string) {
	t.Helper()
	res, err := Collect(opts)
	if err != nil {
		t.Fatalf("Collect returned hard error: %v", err)
	}
	raw, err := os.ReadFile(opts.ReportPath)
	if err != nil {
		t.Fatalf("report not written: %v", err)
	}
	return res, string(raw)
}

// Fixture 1: all expected jobs have logs -> COMPLETE.
func TestAllJobsHaveLogsReportComplete(t *testing.T) {
	opts, _ := fixture(t, []tJob{
		{"preflight", ResultSuccess, "preflight-logs-part", false, map[string]string{"preflight-build.log": "ok\n"}},
		{"build-windows", ResultSuccess, "windows-logs-part", false, map[string]string{"windows-build.log": "ok\n"}},
	})
	res, report := mustCollect(t, opts)
	if res.Coverage != CoverageComplete {
		t.Fatalf("coverage = %s, want COMPLETE", res.Coverage)
	}
	if !strings.Contains(report, "COMPLETE — every job that ran contributed its expected log part.") {
		t.Fatal("report lacks COMPLETE coverage statement")
	}
	if strings.Contains(report, "MISSING LOG SOURCE: ") {
		t.Fatal("complete report must not list missing sources")
	}
}

// Fixture 2: failed command appears in the error index and its full log
// is preserved; evidence coverage stays COMPLETE.
func TestFailedCommandIndexedButCoverageComplete(t *testing.T) {
	logContent := "building...\nEXIT_CODE: 2 FAIL\n--- FAIL: TestSomething\nFAIL\texample.com/pkg\t0.5s\n"
	opts, _ := fixture(t, []tJob{
		{"preflight", ResultFailure, "preflight-logs-part", false, map[string]string{"preflight-tests.log": logContent}},
	})
	res, report := mustCollect(t, opts)
	if res.Coverage != CoverageComplete {
		t.Fatalf("coverage = %s: failed commands are a build failure, not a coverage failure", res.Coverage)
	}
	if res.FailedCommandRecords != 3 {
		t.Fatalf("failed records = %d, want 3 (EXIT_CODE record + test summary + FAIL line)", res.FailedCommandRecords)
	}
	if !strings.Contains(report, "[preflight | preflight-logs-part/preflight-tests.log:2] EXIT_CODE: 2 FAIL") {
		t.Fatal("error index lacks provenance for the failed command record")
	}
	// Full original log preserved verbatim in the consolidated section.
	if !strings.Contains(report, "FAIL\texample.com/pkg\t0.5s\n#### LOG CONTENT END") {
		t.Fatal("full failing log not preserved verbatim")
	}
}

// Fixture 3: warning-only lines appear in the warning index.
func TestWarningsIndexed(t *testing.T) {
	opts, _ := fixture(t, []tJob{
		{"preflight", ResultSuccess, "preflight-logs-part", false, map[string]string{"p.log": "::warning::deprecated input\nWARNING: minor drift\n"}},
	})
	res, report := mustCollect(t, opts)
	if res.WarningRecords != 2 {
		t.Fatalf("warning records = %d, want 2", res.WarningRecords)
	}
	if !strings.Contains(report, "[preflight | preflight-logs-part/p.log:1] ::warning::deprecated input") {
		t.Fatal("warning index lacks provenance")
	}
	if res.FailedCommandRecords != 0 {
		t.Fatalf("warnings must not be counted as errors, got %d", res.FailedCommandRecords)
	}
}

// Fixture 4: an empty but present log file counts as captured and is
// preserved.
func TestEmptyLogFileCaptured(t *testing.T) {
	opts, _ := fixture(t, []tJob{
		{"preflight", ResultSuccess, "preflight-logs-part", false, map[string]string{"empty.log": ""}},
	})
	res, report := mustCollect(t, opts)
	if res.Coverage != CoverageComplete {
		t.Fatalf("coverage = %s, want COMPLETE", res.Coverage)
	}
	if res.LogFiles != 1 {
		t.Fatalf("log files = %d, want 1", res.LogFiles)
	}
	if !strings.Contains(report, "FILE: empty.log\n#### LOG CONTENT BEGIN\n#### LOG CONTENT END") {
		t.Fatal("empty log file not preserved with explicit begin/end markers")
	}
}

// Fixture 5: one job's artifact missing -> report written first, job
// named, coverage INCOMPLETE (collector exits non-zero at CLI level).
func TestMissingArtifactNamedAndIncomplete(t *testing.T) {
	opts, _ := fixture(t, []tJob{
		{"preflight", ResultSuccess, "preflight-logs-part", false, map[string]string{"p.log": "ok\n"}},
		{"build-windows", ResultFailure, "windows-logs-part", false, nil}, // artifact never uploaded
	})
	res, report := mustCollect(t, opts)
	if res.Coverage != CoverageIncomplete {
		t.Fatalf("coverage = %s, want INCOMPLETE", res.Coverage)
	}
	if !strings.Contains(report, "MISSING LOG SOURCE: build-windows (expected artifact directory \"windows-logs-part\" under parts root)") {
		t.Fatal("report must name the job with the missing artifact")
	}
	if !strings.Contains(report, "Evidence coverage: INCOMPLETE — log sources are missing") {
		t.Fatal("report must state INCOMPLETE coverage")
	}
	if !strings.Contains(report, "ARTIFACT: preflight-logs-part | FILE: p.log") {
		t.Fatal("report must still contain the sources that were collected")
	}
}

// Fixture 6: a skipped job appears under NEVER_STARTED, not as a missing
// artifact or failed command.
func TestSkippedJobIsNeverStarted(t *testing.T) {
	opts, _ := fixture(t, []tJob{
		{"preflight", ResultFailure, "preflight-logs-part", false, map[string]string{"p.log": "EXIT_CODE: 1 FAIL\n"}},
		{"build-linux", ResultSkipped, "linux-logs-part", false, nil},
	})
	res, report := mustCollect(t, opts)
	if res.Coverage != CoverageComplete {
		t.Fatalf("coverage = %s, want COMPLETE (skipped jobs never owed logs)", res.Coverage)
	}
	if len(res.NeverStarted) != 1 || res.NeverStarted[0] != "build-linux" {
		t.Fatalf("never started = %v, want [build-linux]", res.NeverStarted)
	}
	if !strings.Contains(report, "NEVER_STARTED: build-linux") {
		t.Fatal("skipped job not listed under NEVER_STARTED")
	}
	if strings.Contains(report, "MISSING LOG SOURCE: build-linux") {
		t.Fatal("skipped job must not be reported as a missing log source")
	}
}

// Fixture 7: the same basename in two job namespaces is retained twice
// without overwriting.
func TestSameBasenameInTwoNamespacesPreserved(t *testing.T) {
	opts, _ := fixture(t, []tJob{
		{"preflight", ResultSuccess, "preflight-logs-part", false, map[string]string{"build.log": "from preflight\n"}},
		{"build-linux", ResultSuccess, "linux-logs-part", false, map[string]string{"build.log": "from linux\n"}},
	})
	res, report := mustCollect(t, opts)
	if res.LogFiles != 2 {
		t.Fatalf("log files = %d, want 2", res.LogFiles)
	}
	if !strings.Contains(report, "ARTIFACT: preflight-logs-part | FILE: build.log\n#### LOG CONTENT BEGIN\nfrom preflight\n") {
		t.Fatal("preflight build.log content lost")
	}
	if !strings.Contains(report, "ARTIFACT: linux-logs-part | FILE: build.log\n#### LOG CONTENT BEGIN\nfrom linux\n") {
		t.Fatal("linux build.log content lost")
	}
}

// Fixture 8: multiple log files sort deterministically within a job.
func TestDeterministicFileOrder(t *testing.T) {
	opts, _ := fixture(t, []tJob{
		{"preflight", ResultSuccess, "preflight-logs-part", false, map[string]string{
			"c.log": "c\n", "a.log": "a\n", "b.log": "b\n",
		}},
	})
	_, report := mustCollect(t, opts)
	iA := strings.Index(report, "FILE: a.log")
	iB := strings.Index(report, "FILE: b.log")
	iC := strings.Index(report, "FILE: c.log")
	if iA < 0 || iB < 0 || iC < 0 || !(iA < iB && iB < iC) {
		t.Fatalf("files not in deterministic lexicographic order (a=%d b=%d c=%d)", iA, iB, iC)
	}
	// Run twice; output must be byte-identical.
	opts2, _ := fixture(t, []tJob{
		{"preflight", ResultSuccess, "preflight-logs-part", false, map[string]string{
			"c.log": "c\n", "a.log": "a\n", "b.log": "b\n",
		}},
	})
	_, report2 := mustCollect(t, opts2)
	if report != report2 {
		t.Fatal("report not deterministic across runs")
	}
}

// Fixture 9: a red run with one failed job, one missing source and one
// skipped job records all three facts distinctly and accurately.
func TestRedRunRecordsAllThreeFacts(t *testing.T) {
	opts, _ := fixture(t, []tJob{
		{"preflight", ResultSuccess, "preflight-logs-part", false, map[string]string{"p.log": "fine\n"}},
		{"build-windows", ResultFailure, "windows-logs-part", false, nil},
		{"build-linux", ResultSkipped, "linux-logs-part", false, nil},
	})
	res, report := mustCollect(t, opts)
	if res.Coverage != CoverageIncomplete {
		t.Fatalf("coverage = %s, want INCOMPLETE", res.Coverage)
	}
	if !strings.Contains(report, "(succeeded 1, failed 1, cancelled 0, skipped 1)") {
		t.Fatal("summary must count exactly one failed job")
	}
	if !strings.Contains(report, "skipped 1") {
		t.Fatal("summary must count exactly one skipped job")
	}
	if !strings.Contains(report, "MISSING LOG SOURCE jobs:       1\n") {
		t.Fatal("summary must count exactly one missing log source")
	}
	if !strings.Contains(report, "NEVER_STARTED: build-linux") ||
		!strings.Contains(report, "MISSING LOG SOURCE: build-windows") {
		t.Fatal("report must keep skipped and missing facts distinct")
	}
	if strings.Contains(report, "COMPLETE — every job") {
		t.Fatal("red run with missing source must not claim COMPLETE")
	}
}

// Fixture 10: unicode characters and filenames with spaces are
// preserved.
func TestUnicodeAndSpacedFilenamesPreserved(t *testing.T) {
	content := "ünïcodé π-content ✓\n"
	opts, _ := fixture(t, []tJob{
		{"preflight", ResultSuccess, "preflight-logs-part", false, map[string]string{
			"build log with spaces.log": content,
			"ünïcodé-π.log":             content,
		}},
	})
	res, report := mustCollect(t, opts)
	if res.LogFiles != 2 {
		t.Fatalf("log files = %d, want 2", res.LogFiles)
	}
	if !strings.Contains(report, "FILE: build log with spaces.log") {
		t.Fatal("spaced filename not preserved")
	}
	if !strings.Contains(report, "FILE: ünïcodé-π.log") {
		t.Fatal("unicode filename not preserved")
	}
	if !strings.Contains(report, "ünïcodé π-content ✓") {
		t.Fatal("unicode content not preserved")
	}
}

// Contract guard: invalid result values are a hard manifest error.
func TestInvalidResultIsHardError(t *testing.T) {
	opts, _ := fixture(t, []tJob{{name: "j", result: "weird", artifact: ""}})
	if _, err := Collect(opts); err == nil {
		t.Fatal("invalid result value must be a hard error")
	}
}

// Contract guard: duplicate artifact names across jobs are a hard error.
func TestDuplicateArtifactNamesRejected(t *testing.T) {
	opts, _ := fixture(t, []tJob{
		{"a", ResultSuccess, "same-part", false, map[string]string{"x.log": "x\n"}},
		{"b", ResultSuccess, "same-part", false, map[string]string{"y.log": "y\n"}},
	})
	if _, err := Collect(opts); err == nil {
		t.Fatal("duplicate artifact names must be rejected")
	}
}

// Contract guard: an artifact directory that exists but contains no log
// files is a missing source, not silent coverage.
func TestEmptyArtifactDirectoryIsMissingSource(t *testing.T) {
	opts, _ := fixture(t, []tJob{{"j", ResultSuccess, "empty-part", true, nil}})
	if err := os.MkdirAll(filepath.Join(opts.PartsRoot, "empty-part"), 0o755); err != nil {
		t.Fatal(err)
	}
	res, _ := mustCollect(t, opts)
	if res.Coverage != CoverageIncomplete {
		t.Fatalf("coverage = %s, want INCOMPLETE", res.Coverage)
	}
	if !strings.Contains(strings.Join(res.MissingSources, " "), "contains no log files") {
		t.Fatalf("missing sources = %v", res.MissingSources)
	}
}

// Fixture 11: a required job fails WITHOUT any textual error marker in
// its captured logs. The failure index must derive the failure from the
// job conclusion, and the overall result must be FAIL — the collector
// must never convert a failed build job into a green report.
func TestFailedJobWithoutErrorMarkerIsReportedAsFailure(t *testing.T) {
	opts, _ := fixture(t, []tJob{
		{"preflight", ResultSuccess, "preflight-logs-part", true, map[string]string{"p.log": "all gates pass\n"}},
		{"build-windows", ResultFailure, "windows-logs-part", true, map[string]string{"w.log": "everything looks fine, no markers here\n"}},
	})
	res, report := mustCollect(t, opts)
	if res.Overall != OverallFail {
		t.Fatalf("overall = %s, want FAIL", res.Overall)
	}
	if len(res.FailedJobs) != 1 || !strings.Contains(res.FailedJobs[0], "build-windows") {
		t.Fatalf("failed jobs = %v, want exactly build-windows", res.FailedJobs)
	}
	if !strings.Contains(report, "JOB FAILURE: build-windows (workflow conclusion: failure)") {
		t.Fatal("report must list the authoritative job conclusion failure")
	}
	if !strings.Contains(report, "failed WITHOUT any textual error marker") {
		t.Fatal("report must explain that text scanning is not the status authority")
	}
	if !strings.Contains(report, "OVERALL RESULT: FAIL") {
		t.Fatal("report must end with the truthful overall FAIL")
	}
	if strings.Contains(report, "OVERALL RESULT: PASS") {
		t.Fatal("a failed required job must never produce an overall PASS")
	}
}

// Fixture 12: all required jobs succeeded and coverage is complete.
func TestOverallPassRequiresRequiredSuccess(t *testing.T) {
	opts, _ := fixture(t, []tJob{
		{"preflight", ResultSuccess, "preflight-logs-part", true, map[string]string{"p.log": "ok\n"}},
		{"build-linux", ResultSuccess, "linux-logs-part", true, map[string]string{"l.log": "ok\n"}},
	})
	res, report := mustCollect(t, opts)
	if res.Overall != OverallPass || res.Coverage != CoverageComplete {
		t.Fatalf("overall=%s coverage=%s, want PASS/COMPLETE", res.Overall, res.Coverage)
	}
	if !strings.Contains(report, "OVERALL RESULT: PASS") {
		t.Fatal("report must state the truthful overall PASS")
	}
}

// Fixture 13: exact secret values and generic token patterns are
// redacted from every raw section and index line.
func TestSecretRedaction(t *testing.T) {
	secret := "ghp_AbC123AbC123AbC123AbC123AbC12345"
	exact := "super-exact-workflow-token-9876543210"
	opts, _ := fixture(t, []tJob{
		{"preflight", ResultSuccess, "preflight-logs-part", false, map[string]string{
			"p.log": "token=" + exact + " pat=" + secret + " AKIAIOSFODNN7EXAMPLE bearer Bearer abcdef0123456789abcdef\n",
		}},
	})
	opts.ExtraSecrets = []string{exact}
	_, report := mustCollect(t, opts)
	for _, banned := range []string{exact, secret, "AKIAIOSFODNN7EXAMPLE", "abcdef0123456789abcdef"} {
		if strings.Contains(report, banned) {
			t.Fatalf("report leaked secret %q", banned)
		}
	}
	if !strings.Contains(report, "***REDACTED***") {
		t.Fatal("report must visibly mark redactions")
	}
}

// Fixture 14: duplicate artifact names are a hard error (2 exit code at
// CLI level), not a silent overwrite.
func TestDuplicateArtifactNamesAreHardError(t *testing.T) {
	opts, _ := fixture(t, []tJob{
		{"a", ResultSuccess, "same-part", false, map[string]string{"x.log": "x\n"}},
		{"b", ResultSuccess, "same-part", false, map[string]string{"y.log": "y\n"}},
	})
	if _, err := Collect(opts); err == nil {
		t.Fatal("duplicate artifact names must be a hard error")
	}
}

// Fixture 15: multiline output (stderr interleaved) and unicode are
// preserved verbatim in the raw section.
func TestMultilineAndUnicodeRawPreserved(t *testing.T) {
	content := "line1 stderr\nлиния-2 ✓\r\nline3\ttab\n"
	opts, _ := fixture(t, []tJob{
		{"build", ResultSuccess, "linux-logs-part", false, map[string]string{"m.log": content}},
	})
	_, report := mustCollect(t, opts)
	if !strings.Contains(report, content) {
		t.Fatal("multiline/unicode raw content not preserved verbatim")
	}
}
