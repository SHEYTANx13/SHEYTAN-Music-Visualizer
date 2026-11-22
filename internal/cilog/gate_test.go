package cilog

// gate_test.go — fixtures for the authoritative gate-status table and
// the run-41915116613 regression classes. These fixtures assert BOTH
// the report contents and the result/exit-status semantics:
//
//   - all jobs pass;
//   - one Windows command fails;
//   - four Windows commands fail while five earlier gates pass
//     (the exact mismatch class of run 41915116613: a text-grep
//     summary reported NINE failed gates while only four command
//     records had nonzero exit codes);
//   - dependency-skipped gates are never failed gates;
//   - a job fails but uploads a log / fails without a log;
//   - an empty log artifact;
//   - duplicate filenames in different namespaces;
//   - CodeQL Go + Actions matrix logs;
//   - a warning without an error;
//   - `ERROR` as ordinary text inside a PASSING command's output;
//   - deduplication of repeated log copies without provenance loss.

import (
	"fmt"
	"strings"
	"testing"
)

// record renders one structured command record as the capture helpers
// (scripts/ci/capture.sh / Capture-Step.ps1) write them.
func record(step string, exitCode int, verdict string, output string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "\n:: COMMAND BEGIN ::\n")
	fmt.Fprintf(&b, "JOB: phase2-windows-build\n")
	fmt.Fprintf(&b, "STEP: %s\n", step)
	fmt.Fprintf(&b, "STARTED: 2026-11-04T21:20:00Z\n")
	fmt.Fprintf(&b, "WORKDIR: D:\\a\\SHEYTAN-Music-Visualizer\\SHEYTAN-Music-Visualizer\n")
	fmt.Fprintf(&b, "COMMAND: go test -tags goffi_static ./...\n")
	fmt.Fprintf(&b, "TOOL_CONTEXT: gate:%s\n", step)
	fmt.Fprintf(&b, ":: OUTPUT (stdout+stderr from first byte) ::\n")
	b.WriteString(output)
	fmt.Fprintf(&b, ":: END OUTPUT ::\n")
	fmt.Fprintf(&b, "EXIT_CODE: %d %s\n", exitCode, verdict)
	fmt.Fprintf(&b, "ENDED: 2026-11-04T21:20:05Z\n")
	fmt.Fprintf(&b, ":: COMMAND END ::\n")
	return b.String()
}

// run41915116613Log reproduces the ESSENTIAL shape of the failed
// Windows log part: five earlier gates PASS (with real command
// output), four gates FAIL (the $goTags scope failure class), the
// downstream gates are SKIPPED, and the build script's own summary
// sections repeat the EXIT_CODE lines as plain-text copies.
func run41915116613Log() string {
	var b strings.Builder
	b.WriteString("==============================================================\n")
	b.WriteString("SHEYTAN MUSIC VISUALIZER - PHASE 2 LOCAL VERIFICATION (WINDOWS)\n")
	b.WriteString(record("toolchain-report", 0, "PASS", "go version go1.27.3 windows/amd64\ngcc: x86_64-w64-mingw32-gcc\n"))
	b.WriteString(record("desktop-env-check", 0, "PASS", "GOOS=windows GOARCH=amd64 CGO_ENABLED=1\ngcc resolved to: D:\\a\\...\\ucrt64\\bin\\gcc.exe\n"))
	b.WriteString(record("gofmt-verify", 0, "PASS", "gofmt clean\n"))
	b.WriteString(record("go-mod-tidy-verify", 0, "PASS", "All modules are tidy.\n"))
	b.WriteString(record("go-mod-verify", 0, "PASS", "all modules verified\n"))
	b.WriteString(record("go-test", 1, "FAIL", "The variable '$goTags' cannot be retrieved because it has not been set.\n"))
	b.WriteString(record("go-vet", 1, "FAIL", "The variable '$goTags' cannot be retrieved because it has not been set.\n"))
	b.WriteString(record("go-build-packages", 1, "FAIL", "The variable '$goTags' cannot be retrieved because it has not been set.\n"))
	b.WriteString(record("build-executable", 1, "FAIL", "The variable '$goTags' cannot be retrieved because it has not been set.\n"))
	b.WriteString("\n==============================================================\n")
	b.WriteString("RUN SUMMARY - phase2-windows-build\n")
	b.WriteString("Failed gates:     9\n")
	b.WriteString("Failed gate list: toolchain-report desktop-env-check gofmt-verify go-mod-tidy-verify go-mod-verify go-test go-vet go-build-packages build-executable\n")
	b.WriteString("\nFAILURE INDEX (EXIT_CODE records with non-zero codes):\n")
	// The build script's failure-index section copies the EXIT_CODE
	// lines with line-number prefixes — plain-text copies that must
	// neither create gates nor be counted as command records.
	b.WriteString("35: EXIT_CODE: 1 FAIL\n42: EXIT_CODE: 1 FAIL\n49: EXIT_CODE: 1 FAIL\n56: EXIT_CODE: 1 FAIL\n")
	b.WriteString("\nSKIPPED GATES:\n")
	b.WriteString("SKIPPED: gate run-version (executable build failed)\n")
	b.WriteString("SKIPPED: gate run-self-check (executable build failed)\n")
	b.WriteString("SKIPPED: gate stage-portable (executable build failed)\n")
	b.WriteString("SKIPPED: gate validate-pe (executable build failed)\n")
	b.WriteString("SKIPPED: gate create-portable-zip (executable build failed)\n")
	b.WriteString("RUN RESULT: FAIL\n")
	b.WriteString("==============================================================\n")
	return b.String()
}

// THE REGRESSION: four failed commands, five earlier passing gates,
// five skipped gates, and summary/index copies. The gate table must
// list exactly the four failed gates — never the five passing ones,
// never the skipped ones, never the copied index lines.
func TestFourFailuresWhileFiveGatesPassRegression(t *testing.T) {
	opts, _ := fixture(t, []tJob{
		{"build-windows", ResultFailure, "windows-logs-part", true, map[string]string{
			"phase2-build.log": run41915116613Log(),
		}},
	})
	res, report := mustCollect(t, opts)

	if res.FailedGateCount != 4 {
		t.Fatalf("FailedGateCount = %d, want exactly 4 (the nine-gate summary must never return)", res.FailedGateCount)
	}
	wantFailed := map[string]bool{"go-test": true, "go-vet": true, "go-build-packages": true, "build-executable": true}
	gotFailed := map[string]bool{}
	for _, g := range res.Gates {
		if g.Status == GateFail {
			gotFailed[g.Step] = true
		}
	}
	for step := range wantFailed {
		if !gotFailed[step] {
			t.Fatalf("failed gate %q missing from %v", step, gotFailed)
		}
	}
	for step := range gotFailed {
		if !wantFailed[step] {
			t.Fatalf("gate %q must NOT be listed failed (it has EXIT_CODE: 0 PASS or was skipped)", step)
		}
	}
	if res.SkippedGateCount != 5 {
		t.Fatalf("SkippedGateCount = %d, want 5 (downstream gates blocked by the failed build)", res.SkippedGateCount)
	}
	for _, g := range res.Gates {
		if g.Step == "toolchain-report" && g.Status != GatePass {
			t.Fatalf("passing gate toolchain-report must be PASS, got %s", g.Status)
		}
	}
	// The artifact carries one ERROR record per failed gate and one per
	// skipped gate of the required job - exactly four failed and five
	// skipped, never the nine-gate summary, never the passing gates.
	failedRecs := 0
	skippedRecs := 0
	for _, line := range strings.Split(report, "\n") {
		if strings.Contains(line, "gate go-test failed with exit code 1") ||
			strings.Contains(line, "gate go-vet failed with exit code 1") ||
			strings.Contains(line, "gate go-build-packages failed with exit code 1") ||
			strings.Contains(line, "gate build-executable failed with exit code 1") {
			failedRecs++
		}
		if strings.Contains(line, "gate SKIPPED: ") && strings.Contains(line, "job=build-windows") {
			skippedRecs++
		}
	}
	if failedRecs != 4 {
		t.Fatalf("failed-gate ERROR records = %d, want exactly 4\n---\n%s", failedRecs, report)
	}
	if skippedRecs != 5 {
		t.Fatalf("skipped-gate ERROR records = %d, want exactly 5 (required job)", skippedRecs)
	}
	summary := mustSummary(t, opts)
	if !strings.Contains(summary, "Gates passed=5 failed=4 skipped=5 blocked=0") {
		t.Fatalf("summary gate counters must agree with the authoritative table:\n%s", summary)
	}
	if strings.Contains(report, "RUN SUMMARY") || strings.Contains(report, "FAILURE INDEX") {
		t.Fatal("artifact must not contain summary sections")
	}
	// Overall: the required Windows job failed -> FAIL.
	if res.Overall != OverallFail {
		t.Fatalf("overall = %s, want FAIL (required job failed)", res.Overall)
	}
}

// One failing Windows command: minimal red path with complete evidence.
func TestOneWindowsCommandFails(t *testing.T) {
	var b strings.Builder
	b.WriteString(record("go-test", 2, "FAIL", "-- FAIL: TestX (0.00s)\nFAIL\texample.com/pkg\t0.5s\n"))
	b.WriteString("RUN RESULT: FAIL\n")
	opts, _ := fixture(t, []tJob{
		{"build-windows", ResultFailure, "windows-logs-part", true, map[string]string{"phase2-build.log": b.String()}},
	})
	res, report := mustCollect(t, opts)
	if res.FailedGateCount != 1 || res.Gates[0].Step != "go-test" || res.Gates[0].ExitCode != 2 {
		t.Fatalf("gate table wrong: failed=%d gates=%+v", res.FailedGateCount, res.Gates)
	}
	if !strings.Contains(report, "job=build-windows") || !strings.Contains(report, "gate go-test failed with exit code 2") {
		t.Fatalf("gate ERROR record must carry job and step provenance:\n%s", report)
	}
	if res.Overall != OverallFail {
		t.Fatalf("overall = %s, want FAIL", res.Overall)
	}
}

// A passing gate whose OUTPUT contains `ERROR:` as ordinary text keeps
// status PASS; the text index may list the line as supplemental
// evidence, but no gate may be marked failed by it.
func TestErrorTextInsidePassingCommandIsNotAGateFailure(t *testing.T) {
	var b strings.Builder
	b.WriteString(record("toolchain-report", 0, "PASS", "checking...\nERROR: no such option --verbose (harmless probe text)\ndone\n"))
	b.WriteString("RUN RESULT: PASS\n")
	opts, _ := fixture(t, []tJob{
		{"build-windows", ResultSuccess, "windows-logs-part", true, map[string]string{"phase2-build.log": b.String()}},
	})
	res, report := mustCollect(t, opts)
	if res.FailedGateCount != 0 {
		t.Fatalf("FailedGateCount = %d, want 0 (ERROR text inside output never changes gate status)", res.FailedGateCount)
	}
	if res.Gates[0].Status != GatePass {
		t.Fatalf("gate status = %s, want PASS", res.Gates[0].Status)
	}
	if strings.Contains(report, "no such option --verbose") {
		t.Fatal("ordinary ERROR text inside a PASSING gate's output must not become a record (the exit code is the authority)")
	}
	if report != "" {
		t.Fatalf("all gates passed; artifact must be empty, got:\n%s", report)
	}
	if res.Overall != OverallPass {
		t.Fatalf("overall = %s, want PASS", res.Overall)
	}
}

// Repeated log copies (same line in the raw section and a summary
// section) collapse into one text finding that keeps every
// provenance.
func TestRepeatedLogCopiesDeduplicatedWithProvenance(t *testing.T) {
	line := "EXIT_CODE: 1 FAIL\n"
	content := record("go-test", 1, "FAIL", "boom\n") +
		"\nFAILURE INDEX:\n12: EXIT_CODE: 1 FAIL\n"
	// The indexed copy above carries a line-number prefix so it is a
	// different TEXT line; duplicate the exact same line twice inside
	// one file plus once in a second file of the SAME job namespace.
	content2 := "EXIT_CODE: 1 FAIL\nEXIT_CODE: 1 FAIL\n"
	opts, _ := fixture(t, []tJob{
		{"build-windows", ResultFailure, "windows-logs-part", true, map[string]string{
			"phase2-build.log": content + line,
			"copy.log":         content2,
		}},
	})
	res, report := mustCollect(t, opts)
	// Distinct TEXT findings: every bare `EXIT_CODE: 1 FAIL` copy (the
	// record line, the trailing copy, and two copies in copy.log) is
	// IDENTICAL text -> ONE deduplicated finding with all four
	// provenances. The line-number-prefixed summary copy does not match
	// the anchored record pattern (and must not create a gate).
	if res.FailedCommandRecords != 1 {
		t.Fatalf("FailedCommandRecords = %d, want 1 (identical text deduplicated across files)", res.FailedCommandRecords)
	}
	// The raw EXIT_CODE text itself must never enter the artifact
	// (the authoritative gate record carries the failure once).
	if strings.Contains(report, "EXIT_CODE: 1 FAIL") {
		t.Fatal("raw EXIT_CODE copies must not leak into the artifact")
	}
	if n := strings.Count(report, "gate go-test failed with exit code 1"); n != 1 {
		t.Fatalf("authoritative gate record must exist exactly once, got %d:\n%s", n, report)
	}
	// Gate status is unaffected by the copies: exactly one failed gate.
	if res.FailedGateCount != 1 || res.Gates[0].Step != "go-test" {
		t.Fatalf("gate table must ignore copies: failed=%d gates=%+v", res.FailedGateCount, res.Gates)
	}
	for _, g := range res.Gates {
		if g.Step == "go-test" && len(g.Wheres) != 1 {
			t.Fatalf("gate provenance = %v, want the single record location", g.Wheres)
		}
	}
}

// All jobs pass: gate table present, zero failures, PASS overall —
// and a gates=0 line in the CLI-visible counters (asserted here as
// Result fields).
func TestGateTableOnAllGreenRun(t *testing.T) {
	var b strings.Builder
	b.WriteString(record("toolchain-report", 0, "PASS", "ok\n"))
	b.WriteString(record("go-test", 0, "PASS", "ok   \texample.com/pkg\t0.5s\n"))
	b.WriteString("RUN RESULT: PASS\n")
	opts, _ := fixture(t, []tJob{
		{"build-windows", ResultSuccess, "windows-logs-part", true, map[string]string{"phase2-build.log": b.String()}},
	})
	res, report := mustCollect(t, opts)
	if res.FailedGateCount != 0 || res.SkippedGateCount != 0 || res.PassedGateCount != 2 {
		t.Fatalf("counters = failed %d skipped %d passed %d, want 0/0/2",
			res.FailedGateCount, res.SkippedGateCount, res.PassedGateCount)
	}
	if res.Overall != OverallPass {
		t.Fatalf("overall = %s, want PASS", res.Overall)
	}
	if report != "" {
		t.Fatalf("green run artifact must be empty, got:\n%s", report)
	}
	if !strings.Contains(mustSummary(t, opts), "Gates passed=2 failed=0 skipped=0 blocked=0") {
		t.Fatal("summary must state zero failed gates on a green run")
	}
}

// Skipped gates are not failed gates even when the reason text looks
// alarming.
func TestSkippedGatesAreNotFailedGates(t *testing.T) {
	var b strings.Builder
	b.WriteString(record("build-executable", 1, "FAIL", "link error\n"))
	b.WriteString("SKIPPED: gate run-version (executable build failed)\n")
	b.WriteString("SKIPPED: gate create-portable-zip (executable build failed)\n")
	b.WriteString("RUN RESULT: FAIL\n")
	opts, _ := fixture(t, []tJob{
		{"build-windows", ResultFailure, "windows-logs-part", true, map[string]string{"phase2-build.log": b.String()}},
	})
	res, report := mustCollect(t, opts)
	if res.FailedGateCount != 1 {
		t.Fatalf("FailedGateCount = %d, want 1 (skips are not failures)", res.FailedGateCount)
	}
	if res.SkippedGateCount != 2 {
		t.Fatalf("SkippedGateCount = %d, want 2", res.SkippedGateCount)
	}
	for _, g := range res.Gates {
		if g.Step == "run-version" && g.Status != GateSkipped {
			t.Fatalf("run-version must be SKIPPED, got %s", g.Status)
		}
	}
	if !strings.Contains(report, "gate SKIPPED: run-version (executable build failed)") {
		t.Fatalf("artifact must show the skipped gates distinctly:\n%s", report)
	}
}

// A record whose verdict text says PASS but whose CODE is nonzero is
// treated by the code (the machine-readable authority), not the word.
func TestExitCodeIsAuthoritativeOverVerdictWord(t *testing.T) {
	content := record("go-test", 3, "PASS", "suspicious\n")
	opts, _ := fixture(t, []tJob{
		{"build-windows", ResultSuccess, "windows-logs-part", true, map[string]string{"phase2-build.log": content}},
	})
	res, _ := mustCollect(t, opts)
	if res.Gates[0].Status != GateFail || res.Gates[0].ExitCode != 3 {
		t.Fatalf("gate = %+v, want FAIL with exit 3 (code is the authority)", res.Gates[0])
	}
}

// CodeQL Go + Actions matrix evidence: two matrix legs upload
// distinctly named log parts with subdirectory structure; both are
// consolidated without collision.
func TestCodeQLMatrixLogsConsolidated(t *testing.T) {
	opts, _ := fixture(t, []tJob{
		{"codeql-go", ResultSuccess, "codeql-logs-part-go", true, map[string]string{
			"go/codeql-go-build.log": "build for extraction\nEXIT_CODE: 0 PASS\n",
		}},
		{"codeql-actions", ResultSuccess, "codeql-logs-part-actions", true, map[string]string{
			"actions/codeql-actions.log": "actions analysis (build-mode none)\n",
		}},
	})
	res, report := mustCollect(t, opts)
	if res.LogFiles != 2 || res.Coverage != CoverageComplete {
		t.Fatalf("log files=%d coverage=%s, want 2/COMPLETE (both matrix namespaces consolidated)", res.LogFiles, res.Coverage)
	}
	if strings.Contains(report, "ARTIFACT:") || strings.Contains(report, "#### LOG CONTENT") {
		t.Fatal("record-only artifact must not embed raw log listings")
	}
}

// A job that fails but uploads its log keeps coverage COMPLETE (its
// gate table shows the failed command; coverage is about evidence).
func TestFailedJobWithUploadedLogHasCompleteCoverage(t *testing.T) {
	opts, _ := fixture(t, []tJob{
		{"build-linux", ResultFailure, "linux-logs-part", true, map[string]string{
			"linux-build.log": record("go-test", 1, "FAIL", "FAIL\texample.com/pkg\t0.5s\n"),
		}},
	})
	res, _ := mustCollect(t, opts)
	if res.Coverage != CoverageComplete {
		t.Fatalf("coverage = %s, want COMPLETE", res.Coverage)
	}
	if res.FailedGateCount != 1 || res.Overall != OverallFail {
		t.Fatalf("gates=%d overall=%s, want 1/FAIL", res.FailedGateCount, res.Overall)
	}
}

// GitHub workflow-command annotations in raw API logs are captured:
// ##[error] / ##[warning] forms count like their ::error:: / ::warning::
// equivalents.
func TestWorkflowCommandAnnotationFormsAreIndexed(t *testing.T) {
	content := "2026-11-04T21:20:33.1234567Z ##[error]Process completed with exit code 1.\n" +
		"2026-11-04T21:20:34.0000000Z ##[warning]cache reservation conflict\n"
	opts, _ := fixture(t, []tJob{
		{"build-windows", ResultFailure, "windows-logs-part", true, map[string]string{"job-raw.log": content}},
	})
	res, report := mustCollect(t, opts)
	if res.FailedCommandRecords != 1 {
		t.Fatalf("error records = %d, want 1 (##[error] form must be indexed)", res.FailedCommandRecords)
	}
	if res.WarningRecords != 1 {
		t.Fatalf("warning records = %d, want 1 (##[warning] form must be indexed)", res.WarningRecords)
	}
	if !strings.Contains(report, "Process completed with exit code 1.") {
		t.Fatalf("report must contain the ##[error] diagnostic as a record:\n%s", report)
	}
	if strings.Contains(report, "##[") {
		t.Fatal("raw workflow-command syntax must not leak into the record message")
	}
}

// A warning without any error: warning index filled, error index
// empty, run can still be green.
func TestWarningWithoutErrorStaysGreen(t *testing.T) {
	opts, _ := fixture(t, []tJob{
		{"preflight", ResultSuccess, "preflight-logs-part", false, map[string]string{
			"p.log": "::warning::deprecated input only\n",
		}},
	})
	res, _ := mustCollect(t, opts)
	if res.WarningRecords != 1 || res.FailedCommandRecords != 0 {
		t.Fatalf("warnings=%d errors=%d, want 1/0", res.WarningRecords, res.FailedCommandRecords)
	}
	if res.Overall != OverallPass {
		t.Fatalf("overall = %s, want PASS", res.Overall)
	}
}
