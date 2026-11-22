package cilog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/asciilog"
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
	opts := Options{
		PartsRoot:   root,
		ReportPath:  filepath.Join(t.TempDir(), "report.txt"),
		SummaryPath: filepath.Join(t.TempDir(), "summary.txt"),
		Now:         fixedNow,
	}
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

// mustSummary reads the written job summary.
func mustSummary(t *testing.T, opts Options) string {
	t.Helper()
	b, err := os.ReadFile(opts.SummaryPath)
	if err != nil {
		t.Fatalf("summary not written: %v", err)
	}
	return string(b)
}

// assertRecordContract verifies the strict warning/error-only contract
// of a final artifact body: every line is a record, bytes are strict.
func assertRecordContract(t *testing.T, report string) {
	t.Helper()
	if strings.Contains(report, "\r") {
		t.Fatal("final artifact must not contain CR bytes")
	}
	rep := asciilog.ValidateStrict([]byte(report))
	if rep.Violations() > 0 {
		t.Fatalf("final artifact violates the strict byte contract: %+v", rep)
	}
	for i, line := range strings.Split(strings.TrimSuffix(report, "\n"), "\n") {
		if report == "" {
			break
		}
		if !strings.HasPrefix(line, "WARNING|") && !strings.HasPrefix(line, "ERROR|") {
			t.Fatalf("line %d is not a warning/error record: %q", i+1, line)
		}
	}
	if strings.Contains(report, "#### LOG CONTENT") ||
		strings.Contains(report, "COMMAND BEGIN") ||
		strings.Contains(report, "RUN METADATA") ||
		strings.Contains(report, "JOB STATUS") ||
		strings.Contains(report, "OVERALL RESULT") {
		t.Fatal("final artifact must not contain raw-log or metadata sections")
	}
}

// countRecords counts ERROR| and WARNING| lines.
func countRecords(report string) (errors, warnings int) {
	for _, line := range strings.Split(report, "\n") {
		if strings.HasPrefix(line, "ERROR|") {
			errors++
		} else if strings.HasPrefix(line, "WARNING|") {
			warnings++
		}
	}
	return
}

// Fixture 1: all expected jobs have logs -> COMPLETE, and a clean run
// produces an EMPTY diagnostics file (no fabricated warnings).
func TestAllJobsHaveLogsReportComplete(t *testing.T) {
	opts, _ := fixture(t, []tJob{
		{"preflight", ResultSuccess, "preflight-logs-part", false, map[string]string{"preflight-build.log": "ok\n"}},
		{"build-windows", ResultSuccess, "windows-logs-part", false, map[string]string{"windows-build.log": "ok\n"}},
	})
	res, report := mustCollect(t, opts)
	if res.Coverage != CoverageComplete {
		t.Fatalf("coverage = %s, want COMPLETE", res.Coverage)
	}
	assertRecordContract(t, report)
	if report != "" {
		t.Fatalf("clean run must produce an empty diagnostics file, got %d bytes", len(report))
	}
	if res.ErrorRecordCount != 0 || res.WarningRecordCount != 0 {
		t.Fatalf("clean run record counts = %d/%d, want 0/0", res.ErrorRecordCount, res.WarningRecordCount)
	}
	// Clean-run metadata lives in the job summary, not the artifact.
	summary := mustSummary(t, opts)
	if !strings.Contains(summary, "Coverage: COMPLETE") {
		t.Fatal("job summary must state COMPLETE coverage")
	}
}

// Fixture 2: failed commands become ERROR records (with gate provenance
// for the structured record); evidence coverage stays COMPLETE; raw
// logs never enter the artifact.
func TestFailedCommandIndexedButCoverageComplete(t *testing.T) {
	logContent := ":: COMMAND BEGIN ::\nJOB: phase2-tests\nSTEP: go-test\n" +
		":: OUTPUT (stdout+stderr from first byte) ::\nbuilding...\n--- FAIL: TestSomething\n:: END OUTPUT ::\n" +
		"EXIT_CODE: 2 FAIL\n:: COMMAND END ::\nFAIL\texample.com/pkg\t0.5s\n"
	opts, _ := fixture(t, []tJob{
		{"preflight", ResultFailure, "preflight-logs-part", false, map[string]string{"preflight-tests.log": logContent}},
	})
	res, report := mustCollect(t, opts)
	if res.Coverage != CoverageComplete {
		t.Fatalf("coverage = %s: failed commands are a build failure, not a coverage failure", res.Coverage)
	}
	assertRecordContract(t, report)
	errs, _ := countRecords(report)
	if errs < 3 {
		t.Fatalf("error records = %d, want >=3 (gate record + test failure + package failure)", errs)
	}
	if !strings.Contains(report, "job=preflight") {
		t.Fatal("records must carry the originating job")
	}
	if !strings.Contains(report, "message=test failure: TestSomething") {
		t.Fatal("go-test failure record missing")
	}
	if !strings.Contains(report, "gate go-test failed with exit code 2") {
		t.Fatal("authoritative gate failure record missing")
	}
	// The structured EXIT_CODE line itself must not be duplicated verbatim.
	if strings.Contains(report, "EXIT_CODE: 2 FAIL") {
		t.Fatal("raw EXIT_CODE record must not be duplicated in the artifact")
	}
	if strings.Contains(report, "FAIL\texample.com/pkg") {
		t.Fatal("raw log lines must not enter the artifact; only records")
	}
}

// Fixture 3: warning-only lines become WARNING records with severity
// preserved.
func TestWarningsIndexed(t *testing.T) {
	opts, _ := fixture(t, []tJob{
		{"preflight", ResultSuccess, "preflight-logs-part", false, map[string]string{"p.log": "::warning::deprecated input\nWARNING: minor drift\n"}},
	})
	res, report := mustCollect(t, opts)
	assertRecordContract(t, report)
	_, warns := countRecords(report)
	if warns != 2 {
		t.Fatalf("warning records = %d, want 2\nreport:\n%s", warns, report)
	}
	if res.ErrorRecordCount != 0 {
		t.Fatalf("warnings must not be counted as errors, got %d", res.ErrorRecordCount)
	}
	if !strings.Contains(report, "WARNING|") {
		t.Fatal("warning severity lost")
	}
	if !strings.Contains(report, "deprecated input") || !strings.Contains(report, "minor drift") {
		t.Fatal("warning messages lost")
	}
}

// Fixture 4: an empty but present log file counts as captured; with no
// diagnostics the artifact is empty (valid per contract).
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
	if report != "" {
		t.Fatalf("empty log must not fabricate records, got %q", report)
	}
}

// Fixture 5: one job's artifact missing -> report written first, ERROR
// record names the job, coverage INCOMPLETE.
func TestMissingArtifactNamedAndIncomplete(t *testing.T) {
	opts, _ := fixture(t, []tJob{
		{"preflight", ResultSuccess, "preflight-logs-part", false, map[string]string{"p.log": "ok\n"}},
		{"build-windows", ResultFailure, "windows-logs-part", false, nil}, // artifact never uploaded
	})
	res, report := mustCollect(t, opts)
	if res.Coverage != CoverageIncomplete {
		t.Fatalf("coverage = %s, want INCOMPLETE", res.Coverage)
	}
	assertRecordContract(t, report)
	if !strings.Contains(report, "message=missing required log source: build-windows") {
		t.Fatal("ERROR record must name the job with the missing artifact")
	}
	if !strings.Contains(report, "platform=windows-x64|job=build-windows") {
		t.Fatal("missing-source record must carry platform and job provenance")
	}
	if !strings.Contains(mustSummary(t, opts), "Missing sources: 1") {
		t.Fatal("job summary must count the missing source")
	}
}

// Fixture 6: a skipped job appears as a WARNING record (never started),
// not as a missing artifact or failed command.
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
	assertRecordContract(t, report)
	if !strings.Contains(report, "job=build-linux") ||
		!strings.Contains(report, "message=job never started") {
		t.Fatal("skipped job not represented as a never-started record")
	}
	if strings.Contains(report, "missing required log source: build-linux") {
		t.Fatal("skipped job must not be reported as a missing log source")
	}
}

// Fixture 7: the same basename in two job namespaces is scanned twice
// without overwriting (both contribute their records).
func TestSameBasenameInTwoNamespacesPreserved(t *testing.T) {
	opts, _ := fixture(t, []tJob{
		{"preflight", ResultSuccess, "preflight-logs-part", false, map[string]string{"build.log": "::error::preflight boom\n"}},
		{"build-linux", ResultSuccess, "linux-logs-part", false, map[string]string{"build.log": "::error::linux boom\n"}},
	})
	res, report := mustCollect(t, opts)
	if res.LogFiles != 2 {
		t.Fatalf("log files = %d, want 2", res.LogFiles)
	}
	if !strings.Contains(report, "preflight boom") || !strings.Contains(report, "linux boom") {
		t.Fatal("records from both namespaces must survive")
	}
	errs, _ := countRecords(report)
	if errs != 2 {
		t.Fatalf("error records = %d, want 2", errs)
	}
}

// Fixture 8: record output is deterministic across runs (severity and
// job ordering).
func TestDeterministicFileOrder(t *testing.T) {
	files := map[string]string{
		"c.log": "::error::alpha failure\n",
		"a.log": "::error::zulu failure\n",
		"b.log": "::error::mike failure\n",
	}
	opts, _ := fixture(t, []tJob{
		{"preflight", ResultSuccess, "preflight-logs-part", false, files},
	})
	_, report := mustCollect(t, opts)
	opts2, _ := fixture(t, []tJob{
		{"preflight", ResultSuccess, "preflight-logs-part", false, files},
	})
	_, report2 := mustCollect(t, opts2)
	if report != report2 {
		t.Fatal("report not deterministic across runs")
	}
	iA := strings.Index(report, "alpha")
	iM := strings.Index(report, "mike")
	iZ := strings.Index(report, "zulu")
	if iA < 0 || iM < 0 || iZ < 0 || !(iA < iM && iM < iZ) {
		t.Fatalf("records not in deterministic message order (a=%d m=%d z=%d)", iA, iM, iZ)
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
	assertRecordContract(t, report)
	if !strings.Contains(report, "message=missing required log source: build-windows") {
		t.Fatal("missing source fact lost")
	}
	if !strings.Contains(report, "message=job never started") {
		t.Fatal("skipped job fact lost")
	}
	if !strings.Contains(report, "message=informational job did not succeed: workflow conclusion failure") {
		t.Fatal("failed job fact lost")
	}
	errs, warns := countRecords(report)
	if errs < 1 || warns < 2 {
		t.Fatalf("record counts errors=%d warnings=%d, want >=1 / >=2", errs, warns)
	}
}

// Fixture 10: unicode content and spaced filenames never leak non-ASCII
// into the strict artifact; the strict validator stays clean.
func TestUnicodeAndSpacedFilenamesPreserved(t *testing.T) {
	content := "ünïcodé π-content ✓\n::error::failure in ünïcodé module\n"
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
	assertRecordContract(t, report)
	for i := 0; i < len(report); i++ {
		if report[i] >= 0x80 {
			t.Fatalf("non-ASCII byte 0x%02x at %d survived into the strict artifact", report[i], i)
		}
	}
	if !strings.Contains(report, "failure in ") {
		t.Fatal("diagnostic message lost")
	}
	if strings.Contains(report, "ünïcodé") {
		t.Fatal("non-ASCII must degrade, not survive")
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
// its captured logs. The authoritative conclusion still produces an
// ERROR record and Overall=FAIL - a failed build job never turns green.
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
	assertRecordContract(t, report)
	if !strings.Contains(report, "platform=windows-x64|job=build-windows") {
		t.Fatal("ERROR record must carry the failed job's provenance")
	}
	if !strings.Contains(report, "message=required job did not succeed: workflow conclusion failure") {
		t.Fatal("authoritative job conclusion must be an ERROR record")
	}
	if !strings.Contains(report, "command=github-job") {
		t.Fatal("the record must identify the conclusion's origin (github-job)")
	}
}

// Fixture 12: all required jobs succeeded and coverage is complete;
// the artifact is empty and the summary states the truthful PASS.
func TestOverallPassRequiresRequiredSuccess(t *testing.T) {
	opts, _ := fixture(t, []tJob{
		{"preflight", ResultSuccess, "preflight-logs-part", true, map[string]string{"p.log": "ok\n"}},
		{"build-linux", ResultSuccess, "linux-logs-part", true, map[string]string{"l.log": "ok\n"}},
	})
	res, report := mustCollect(t, opts)
	if res.Overall != OverallPass || res.Coverage != CoverageComplete {
		t.Fatalf("overall=%s coverage=%s, want PASS/COMPLETE", res.Overall, res.Coverage)
	}
	if report != "" {
		t.Fatalf("green run artifact must be empty, got %q", report)
	}
	if !strings.Contains(mustSummary(t, opts), "Overall build verdict") ||
		!strings.Contains(mustSummary(t, opts), "PASS") {
		t.Fatal("job summary must state the truthful overall PASS")
	}
}

// Fixture 13: exact secret values and generic token patterns are
// redacted BEFORE records are written; the artifact visibly marks
// redactions and never prints the secret (not even in test output).
func TestSecretRedaction(t *testing.T) {
	exact := "super-exact-workflow-token-9876543210"
	opts, _ := fixture(t, []tJob{
		{"preflight", ResultSuccess, "preflight-logs-part", false, map[string]string{
			"p.log": "::error::auth failed for token=" + exact + " with Bearer abcdef0123456789abcdef and key AKIAIOSFODNN7EXAMPLE\n",
		}},
	})
	opts.ExtraSecrets = []string{exact}
	_, report := mustCollect(t, opts)
	for _, banned := range []string{exact, "abcdef0123456789abcdef", "AKIAIOSFODNN7EXAMPLE"} {
		if strings.Contains(report, banned) {
			t.Fatalf("report leaked secret %q", banned)
		}
	}
	if !strings.Contains(report, "***REDACTED***") {
		t.Fatal("report must visibly mark redactions")
	}
}

// Fixture 14: duplicate artifact names are a hard error (exit code 2 at
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

// Fixture 15: multiline output is normalized to single-line messages;
// CRLF is normalized; tabs become spaces in the STRICT artifact.
func TestMultilineAndUnicodeRawPreserved(t *testing.T) {
	// STRICT ARTIFACT CONTRACT: the final artifact carries one-line
	// records; CR/TAB never survive; non-ASCII degrades to '?'.
	content := "line1 stderr\r\n::error::multi line\tmessage here\n" +
		"линия-2 ✓\n" +
		"::error::second\ttabbed failure\n"
	opts, _ := fixture(t, []tJob{
		{"build", ResultSuccess, "linux-logs-part", false, map[string]string{"m.log": content}},
	})
	_, report := mustCollect(t, opts)
	assertRecordContract(t, report)
	if !strings.Contains(report, "multi line message here") {
		t.Fatal("message content lost or tabs not normalized")
	}
	if strings.Contains(report, "\t") {
		t.Fatal("tabs must become spaces in the strict artifact")
	}
	if strings.Contains(report, "линия") {
		t.Fatal("non-ASCII raw content must not survive")
	}
	errs, _ := countRecords(report)
	if errs != 2 {
		t.Fatalf("error records = %d, want 2", errs)
	}
}
