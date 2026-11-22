package cilog

// classification_test.go — run-42026322748 fixtures for the log-source
// classification and status-deduplication requirements (brief §1.I/§1.J,
// §10.E fixtures 33/34/36/37/38/39/46 and §10.F fixtures 47/50):
//
//   - a skipped job is NEVER_STARTED and its raw job log is never fetched
//     (a skipped job has no log; the 404 class must not be reported);
//   - an in-progress job is IN_PROGRESS_AT_SNAPSHOT (its raw log is not
//     final and its current 404 is not a completed-job failure);
//   - a completed job whose artifact is missing but whose raw log is
//     recoverable states BOTH facts (ARTIFACT_MISSING + RECOVERED_FROM_API);
//   - a completed job with neither artifact nor API log is
//     API_LOG_UNAVAILABLE — and the run still fails closed;
//   - manifest and API conclusions of the same job are deduplicated to one
//     entry showing both sources, and an aggregate manifest row proven
//     successful by the API is reconciled instead of double-counted;
//   - a required job that ended with no gate record is reported as
//     GATES NOT REACHED, never as zero failed gates;
//   - a BLOCKED gate (missing prerequisite) is its own state, distinct
//     from SKIPPED and FAIL;
//   - a silent successful CodeQL build (empty stdout) still yields a
//     PASS gate record from its EXIT_CODE line;
//   - warning-only runs stay diagnostics, never analysis failures.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func collectWithMeta(t *testing.T, opts *Options, meta RunMeta) (Result, string) {
	t.Helper()
	opts.Meta = meta
	if opts.ReportPath == "" {
		opts.ReportPath = t.TempDir() + "/log.txt"
	}
	if opts.SummaryPath == "" {
		opts.SummaryPath = t.TempDir() + "/summary.txt"
	}
	if opts.Now == nil {
		opts.Now = fixedNow
	}
	res, err := Collect(*opts)
	if err != nil {
		t.Fatalf("Collect returned hard error: %v", err)
	}
	return res, readFile(t, opts.ReportPath)
}

// TestSkippedJobNeverFetchesRawLog (fixture 33): a skipped job has no raw
// job log; the collector must not even request it, and must not report a
// failed fetch.
func TestSkippedJobNeverFetchesRawLog(t *testing.T) {
	logRequests := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/n/actions/runs/1/attempts/1/jobs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"total_count":1,"jobs":[{"id":51,"name":"build-linux (ubuntu-26.04, x64)",
                        "status":"completed","conclusion":"skipped","started_at":null,"completed_at":null,"steps":[]}]}`))
	})
	mux.HandleFunc("/repos/o/n/actions/jobs/51/logs", func(w http.ResponseWriter, r *http.Request) {
		logRequests++
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	root := t.TempDir()
	opts := Options{PartsRoot: root, FetchJobLogs: true, API: NewGitHubClient(srv.URL, "o/n", "")}
	res, raw := collectWithMeta(t, &opts, RunMeta{
		Workflow: "build.yml", RunID: "1", Attempt: "1",
		Jobs: []JobSpec{{Name: "build-linux", Result: ResultSkipped, Artifact: "linux-logs-part", Required: true}},
	})

	if logRequests != 0 {
		t.Fatalf("raw log fetched %d times for a skipped job; want 0 (no log exists)", logRequests)
	}
	if len(res.NeverStarted) != 1 || res.NeverStarted[0] != "build-linux" {
		t.Fatalf("never started = %v, want [build-linux]", res.NeverStarted)
	}
	if !strings.Contains(raw, "message=job never started (declared skipped in the manifest)") {
		t.Fatalf("artifact must classify the skipped job as a never-started WARNING record:\n%s", raw)
	}
	if strings.Contains(raw, "missing required log source: build-linux") {
		t.Fatal("a skipped job must not be reported as a missing log source")
	}
	if strings.Contains(raw, "API_LOG_UNAVAILABLE") {
		t.Fatal("a skipped job's missing log must not be classified as API-unavailable")
	}
	if res.Overall != OverallFail {
		t.Fatalf("a skipped REQUIRED job must fail the run; overall=%s", res.Overall)
	}
}

// TestInProgressJobClassifiedNotFailed (fixture 37): a job still running at
// snapshot time has no final raw log; its 404 class is
// IN_PROGRESS_AT_SNAPSHOT, never a completed-job failure.
func TestInProgressJobClassifiedNotFailed(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/n/actions/runs/1/attempts/1/jobs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"total_count":1,"jobs":[{"id":52,"name":"build-linux (ubuntu-26.04, x64)",
                        "status":"in_progress","conclusion":null,"started_at":"2026-11-05T12:21:00Z","completed_at":null,"steps":[]}]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	root := t.TempDir()
	opts := Options{PartsRoot: root, FetchJobLogs: true, API: NewGitHubClient(srv.URL, "o/n", "")}
	res, raw := collectWithMeta(t, &opts, RunMeta{
		Workflow: "build.yml", RunID: "1", Attempt: "1",
		Jobs: []JobSpec{{Name: "build-linux", Result: ResultSuccess, Artifact: "linux-logs-part", Required: true}},
	})

	found := false
	for _, u := range res.APIUnavailableLogs {
		if strings.Contains(u, "IN_PROGRESS_AT_SNAPSHOT") {
			found = true
		}
	}
	if !found {
		t.Fatalf("in-progress job not classified IN_PROGRESS_AT_SNAPSHOT: %v", res.APIUnavailableLogs)
	}
	if !strings.Contains(raw, "IN_PROGRESS_AT_SNAPSHOT") {
		t.Fatal("report must state IN_PROGRESS_AT_SNAPSHOT for a still-running job")
	}
}

// TestRecoveredFromAPI (fixture 34): a failed job with a missing artifact
// whose raw log IS recoverable states both facts separately and keeps the
// run red.
func TestRecoveredFromAPI(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/n/actions/runs/1/attempts/1/jobs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"total_count":1,"jobs":[{"id":53,"name":"build-windows (windows-latest, x64, UCRT64)",
                        "status":"completed","conclusion":"failure","started_at":"2026-11-05T12:21:00Z","completed_at":"2026-11-05T12:25:00Z",
                        "steps":[{"number":1,"name":"Build and verify Windows x64 desktop app","status":"completed","conclusion":"failure"}]}]}`))
	})
	mux.HandleFunc("/repos/o/n/actions/jobs/53/logs", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("2026-11-05T12:24Z build step output\n::error::compile error in ui/gogui/studio.go\n"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	root := t.TempDir() // NO artifact part at all
	opts := Options{PartsRoot: root, FetchJobLogs: true, API: NewGitHubClient(srv.URL, "o/n", "")}
	res, raw := collectWithMeta(t, &opts, RunMeta{
		Workflow: "build.yml", RunID: "1", Attempt: "1",
		Jobs: []JobSpec{{Name: "build-windows", Result: ResultFailure, Artifact: "windows-logs-part", Required: true}},
	})

	if len(res.RecoveredFromAPI) != 1 {
		t.Fatalf("recovered = %v, want exactly one RECOVERED_FROM_API entry", res.RecoveredFromAPI)
	}
	if !strings.Contains(raw, "message=missing required log source: build-windows") {
		t.Fatal("artifact must state the missing artifact as an ERROR record")
	}
	if !strings.Contains(raw, "diagnostics recovered from the API job log") {
		t.Fatal("artifact must state the RECOVERED_FROM_API fact as a WARNING record")
	}
	if !strings.Contains(raw, "compile error in ui/gogui/studio.go") {
		t.Fatalf("diagnostics extracted from the recovered API log must appear as records:\n%s", raw)
	}
	if strings.Contains(raw, "#### API JOB LOG") || strings.Contains(raw, "2026-11-05T12:24Z build step output") {
		t.Fatal("recovered RAW log text must never enter the artifact; only its diagnostic records")
	}
	if res.Overall != OverallFail {
		t.Fatalf("failed upstream job must stay red after recovery; overall=%s", res.Overall)
	}
}

// TestCompletedJob404IsAPIUnavailable (fixture 35/36): a completed job with
// neither artifact nor API log is API_LOG_UNAVAILABLE; the report is still
// written and the run fails closed.
func TestCompletedJob404IsAPIUnavailable(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/n/actions/runs/1/attempts/1/jobs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"total_count":1,"jobs":[{"id":54,"name":"build-linux (ubuntu-26.04, x64)",
                        "status":"completed","conclusion":"failure","started_at":"2026-11-05T12:21:00Z","completed_at":"2026-11-05T12:24:00Z","steps":[]}]}`))
	})
	mux.HandleFunc("/repos/o/n/actions/jobs/54/logs", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	root := t.TempDir()
	opts := Options{PartsRoot: root, FetchJobLogs: true, API: NewGitHubClient(srv.URL, "o/n", "")}
	res, raw := collectWithMeta(t, &opts, RunMeta{
		Workflow: "build.yml", RunID: "1", Attempt: "1",
		Jobs: []JobSpec{{Name: "build-linux", Result: ResultFailure, Artifact: "linux-logs-part", Required: true}},
	})

	found := false
	for _, u := range res.APIUnavailableLogs {
		if strings.Contains(u, "API_LOG_UNAVAILABLE") {
			found = true
		}
	}
	if !found {
		t.Fatalf("404 for a completed job must classify API_LOG_UNAVAILABLE: %v", res.APIUnavailableLogs)
	}
	if !strings.Contains(raw, "API_LOG_UNAVAILABLE") {
		t.Fatalf("artifact must list the unavailable raw-log source as a WARNING record:\n%s", raw)
	}
	if res.Coverage != CoverageIncomplete {
		t.Fatalf("coverage = %s, want INCOMPLETE", res.Coverage)
	}
	// Fixture 40: the report is always produced before a nonzero exit —
	// the collector returns res (with report written) instead of a hard error.
}

// TestDeduplicatedFailureShowsBothSources (fixture 38): one job failing in
// BOTH the manifest and the API appears ONCE, with both sources.
func TestDeduplicatedFailureShowsBothSources(t *testing.T) {
	srv := apiServer(t) // preflight success + build-windows failure + collect-logs success
	root := t.TempDir()
	opts := Options{PartsRoot: root, FetchJobLogs: true, API: NewGitHubClient(srv.URL, "o/n", "")}
	res, raw := collectWithMeta(t, &opts, RunMeta{
		Workflow: "build.yml", RunID: "1", Attempt: "1",
		Jobs: []JobSpec{
			{Name: "preflight", Result: ResultSuccess, Artifact: "preflight-logs-part", Required: true},
			{Name: "build-windows", Result: ResultFailure, Artifact: "windows-logs-part", Required: true},
		},
	})

	count := strings.Count(raw, "message=required job did not succeed: workflow conclusion failure; API job conclusion: failure")
	if count != 1 {
		t.Fatalf("the build-windows failure record appears %d times; dedup rule requires exactly 1\n---\n%s", count, raw)
	}
	if len(res.FailedJobs) != 1 {
		t.Fatalf("failed jobs = %v, want exactly one entry", res.FailedJobs)
	}
}

// TestReconciledAggregateRow (fixture 46, inverse): a manifest row carrying
// the AGGREGATE caller result ("failure" because one sibling matrix leg
// failed) that the API proves SUCCESSFUL is reconciled — the succeeded job
// is never converted into a failure, and a real failure is never masked.
func TestReconciledAggregateRow(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/n/actions/runs/1/attempts/1/jobs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"total_count":2,"jobs":[
                        {"id":61,"name":"Analyze (go)","status":"completed","conclusion":"failure","steps":[]},
                        {"id":62,"name":"Analyze (actions)","status":"completed","conclusion":"success","steps":[]}]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	root := t.TempDir()
	opts := Options{PartsRoot: root, API: NewGitHubClient(srv.URL, "o/n", "")}
	res, raw := collectWithMeta(t, &opts, RunMeta{
		Workflow: "build.yml", RunID: "1", Attempt: "1",
		CodeQL: &CodeQLSpec{Workflow: "codeql.yml", Job: "analyze", Result: "failure"},
		Jobs: []JobSpec{
			{Name: "analyze (go)", Result: ResultFailure, Artifact: "codeql-logs-part-go", Required: true},
			{Name: "analyze (actions)", Result: ResultFailure, Artifact: "codeql-logs-part-actions", Required: true},
		},
	})

	if len(res.FailedJobs) != 1 || !strings.Contains(res.FailedJobs[0], "analyze (go)") {
		t.Fatalf("failed jobs = %v, want exactly the failed go leg", res.FailedJobs)
	}
	if len(res.RecoveredFromAPI) != 0 {
		t.Fatalf("unexpected recovery entries: %v", res.RecoveredFromAPI)
	}
	// The succeeded actions leg is reconciled, not reported as a failure.
	summary := readFile(t, opts.SummaryPath)
	if !strings.Contains(summary, "analyze (actions)") || !strings.Contains(summary, "reconciled") {
		t.Fatal("succeeded leg must be reconciled from the aggregate row in the summary")
	}
	// Exactly ONE authoritative job-failure record exists: the failed go
	// leg. The reconciled actions leg must never produce one.
	if n := strings.Count(raw, "message=required job did not succeed"); n != 1 {
		t.Fatalf("required-job-failure records = %d, want exactly 1 (only the go leg)\n---\n%s", n, raw)
	}
	if res.Overall != OverallFail {
		t.Fatalf("the failed go leg must keep the run red; overall=%s", res.Overall)
	}
}

// TestGatesNotReachedStatement (fixture 39): a required job that ended
// without any structured gate record is reported GATES NOT REACHED — the
// run-42026322748 "0 failed / 0 skipped / 0 passed" trap.
func TestGatesNotReachedStatement(t *testing.T) {
	root := t.TempDir()
	opts := Options{PartsRoot: root}
	meta := RunMeta{
		Workflow: "build.yml", RunID: "1", Attempt: "1",
		Jobs: []JobSpec{{Name: "preflight", Result: ResultFailure, Artifact: "preflight-logs-part", Required: true}},
	}
	// The artifact directory EXISTS but carries only a bootstrap note — no
	// structured command record was ever captured (bootstrap worked, gates
	// never ran: exactly the run-42026322748 shape).
	dir := root + "/preflight-logs-part"
	if err := mkdirAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(dir+"/bootstrap.log", "log bootstrap: PASS\njob=preflight\n"); err != nil {
		t.Fatal(err)
	}

	res, raw := collectWithMeta(t, &opts, meta)
	if len(res.GatesNotReached) != 1 || res.GatesNotReached[0] != "preflight" {
		t.Fatalf("gates not reached = %v, want [preflight]", res.GatesNotReached)
	}
	if !strings.Contains(raw, "required job failed before reaching any gate; gate results are unknown, not passed") {
		t.Fatalf("artifact must state GATES NOT REACHED as an ERROR record:\n%s", raw)
	}
}

// TestBlockedGateIsDistinctState (fixture 20/25 collector side): a
// `BLOCKED: gate ...` record parses into the distinct BLOCKED state with
// its reason, never as a failed or passed gate.
func TestBlockedGateIsDistinctState(t *testing.T) {
	root := t.TempDir()
	opts := Options{PartsRoot: root}
	meta := RunMeta{
		Workflow: "build.yml", RunID: "1", Attempt: "1",
		Jobs: []JobSpec{{Name: "build-linux", Result: ResultFailure, Artifact: "linux-logs-part", Required: true}},
	}
	dir := root + "/linux-logs-part"
	if err := mkdirAll(dir); err != nil {
		t.Fatal(err)
	}
	content := "BLOCKED: gate go-test (go toolchain not found on PATH)\n" +
		"BLOCKED: gate go-vet (go toolchain not found on PATH)\n"
	if err := writeFile(dir+"/phase2-build.log", content); err != nil {
		t.Fatal(err)
	}

	res, raw := collectWithMeta(t, &opts, meta)
	if res.BlockedGateCount != 2 {
		t.Fatalf("blocked gates = %d, want 2", res.BlockedGateCount)
	}
	if res.FailedGateCount != 0 {
		t.Fatalf("BLOCKED gates must not count as failures; got %d", res.FailedGateCount)
	}
	if !strings.Contains(raw, "BLOCKED  -     [build-linux | linux-logs-part/phase2-build.log:") &&
		!strings.Contains(raw, "go-test (go toolchain not found on PATH)") {
		t.Fatal("gate table must show the BLOCKED state with its reason")
	}
}

// TestSilentCodeQLBuildRecordIsPass (fixture 47): the traced build's
// structured record with EMPTY stdout and an explicit `EXIT_CODE: 0 PASS`
// line is a PASS gate — an empty body is never missing evidence.
func TestSilentCodeQLBuildRecordIsPass(t *testing.T) {
	root := t.TempDir()
	opts := Options{PartsRoot: root}
	meta := RunMeta{
		Workflow: "build.yml", RunID: "1", Attempt: "1",
		Jobs: []JobSpec{{Name: "analyze (go)", Result: ResultSuccess, Artifact: "codeql-logs-part-go", Required: true}},
	}
	dir := root + "/codeql-logs-part-go"
	if err := mkdirAll(dir); err != nil {
		t.Fatal(err)
	}
	record := ":: COMMAND BEGIN ::\n" +
		"JOB: analyze (go)\n" +
		"STEP: codeql-go-build-traced\n" +
		"STARTED: 2026-11-05T12:30:00Z\n" +
		"WORKDIR: /home/runner/work/repo/repo\n" +
		"COMMAND: go build -tags goffi_static ./...\n" +
		"TOOL_CONTEXT: gate:codeql-traced-build (INSIDE CodeQL tracing)\n" +
		":: OUTPUT (stdout+stderr from first byte) ::\n" +
		":: END OUTPUT ::\n" +
		"EXIT_CODE: 0 PASS\n" +
		"ENDED: 2026-11-05T12:30:41Z\n" +
		":: COMMAND END ::\n"
	if err := writeFile(dir+"/codeql-go-build.log", record); err != nil {
		t.Fatal(err)
	}

	res, raw := collectWithMeta(t, &opts, meta)
	if res.PassedGateCount != 1 || res.FailedGateCount != 0 {
		t.Fatalf("silent build must be exactly one PASS gate (passed=%d failed=%d)", res.PassedGateCount, res.FailedGateCount)
	}
	if raw != "" {
		t.Fatalf("a PASS gate produces no record; artifact must be empty, got:\n%s", raw)
	}
	summary := readFile(t, opts.SummaryPath)
	if !strings.Contains(summary, "codeql-go-build-traced") || !strings.Contains(summary, "PASS") {
		t.Fatal("summary gate table must show the traced build as PASS")
	}
}

// TestOverlayWarningStaysDiagnostic (fixture 50): an improved-incremental
// analysis warning is a diagnostic in the warning index; it never turns
// into a failed gate, and SARIF upload status is represented separately.
func TestOverlayWarningStaysDiagnostic(t *testing.T) {
	root := t.TempDir()
	opts := Options{PartsRoot: root}
	meta := RunMeta{
		Workflow: "build.yml", RunID: "1", Attempt: "1",
		Jobs: []JobSpec{{Name: "analyze (go)", Result: ResultSuccess, Artifact: "codeql-logs-part-go", Required: true}},
	}
	dir := root + "/codeql-logs-part-go"
	if err := mkdirAll(dir); err != nil {
		t.Fatal(err)
	}
	content := "WARNING: improved incremental analysis was skipped (overlay precondition failed for CodeQL 2.27.4)\n" +
		"note: the normal full analysis continued and the SARIF upload succeeded\n"
	if err := writeFile(dir+"/codeql-analysis.log", content); err != nil {
		t.Fatal(err)
	}

	res, raw := collectWithMeta(t, &opts, meta)
	if res.Overall != OverallPass {
		t.Fatalf("warning-only run must stay PASS; overall=%s", res.Overall)
	}
	if res.WarningRecords != 1 {
		t.Fatalf("warnings = %d, want 1", res.WarningRecords)
	}
	if !strings.Contains(raw, "improved incremental analysis was skipped") {
		t.Fatalf("warning record must preserve the overlay diagnostic:\n%s", raw)
	}
	if !strings.Contains(readFile(t, opts.SummaryPath), "does not certify a warning-free run") {
		t.Fatal("the summary must keep the UI-annotation limitation explicit")
	}
}
