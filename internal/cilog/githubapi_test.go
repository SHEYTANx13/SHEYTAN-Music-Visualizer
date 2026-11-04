package cilog

// githubapi_test.go — deterministic httptest fixtures proving that the
// collector uses GitHub job/step conclusions as the authoritative status
// layer: a failed job and a failed step with NO textual error marker in
// any log still appears in the failure index, the step table is written,
// and code scanning alerts for the same commit are summarized. A failing
// API is recorded as a limitation, never fabricated.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

const jobsJSON = `{
  "total_count": 3,
  "jobs": [
    {
      "id": 11, "name": "preflight (ubuntu-24.04)", "status": "completed",
      "conclusion": "success", "started_at": "2026-11-04T18:39:10Z", "completed_at": "2026-11-04T18:43:07Z",
      "labels": ["ubuntu-24.04"],
      "steps": [
        {"number": 1, "name": "Checkout", "status": "completed", "conclusion": "success"},
        {"number": 2, "name": "Preflight gates", "status": "completed", "conclusion": "success"}
      ]
    },
    {
      "id": 22, "name": "build-windows (windows-latest, x64, UCRT64)", "status": "completed",
      "conclusion": "failure", "started_at": "2026-11-04T18:43:08Z", "completed_at": "2026-11-04T18:44:21Z",
      "labels": ["windows-latest"],
      "steps": [
        {"number": 1, "name": "Checkout", "status": "completed", "conclusion": "success"},
        {"number": 2, "name": "Verify UCRT64 toolchain", "status": "completed", "conclusion": "success"},
        {"number": 3, "name": "Build and verify Windows x64 desktop app", "status": "completed", "conclusion": "failure"}
      ]
    },
    {
      "id": 33, "name": "collect-logs (consolidated evidence)", "status": "completed",
      "conclusion": "success", "started_at": "2026-11-04T18:44:30Z", "completed_at": "2026-11-04T18:44:54Z",
      "labels": ["ubuntu-24.04"],
      "steps": [{"number": 1, "name": "Run collector", "status": "completed", "conclusion": "success"}]
    }
  ]
}`

const job22Log = "2026-11-04T18:44:20.9Z == Group: Build and verify Windows x64 desktop app ==\n" +
	"everything looks quiet, no error marker at all\n" +
	"##[error]Process completed with exit code 1.\n"

// The alerts endpoint flattens tool/path fields into the top level of
// each alert object in the real API response; the fixture mirrors the
// fields this collector actually consumes.
func alertsHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("state") != "open" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`[
      {"number": 7, "rule_id": "go/insecure-landing", "rule_description": "Insecure landing zone",
       "rule_severity": "medium", "tool": "CodeQL", "state": "open",
       "html_url": "https://github.com/o/n/security/code-scanning/7",
       "path": "cmd/x/main.go", "start_line": 42}
    ]`))
}

func mkdirAll(dir string) error { return os.MkdirAll(dir, 0o755) }

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(raw)
}

func apiServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/n/actions/runs/1/attempts/1/jobs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(jobsJSON))
	})
	mux.HandleFunc("/repos/o/n/actions/jobs/11/logs", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("preflight raw log, unremarkable\n"))
	})
	mux.HandleFunc("/repos/o/n/actions/jobs/22/logs", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(job22Log))
	})
	mux.HandleFunc("/repos/o/n/code-scanning/alerts", alertsHandler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestAPIConclusionsAreAuthoritativeWithoutErrorMarkers(t *testing.T) {
	srv := apiServer(t)
	root := t.TempDir()
	opts := Options{
		PartsRoot:    root,
		ReportPath:   root + "/log.txt",
		FetchJobLogs: true,
		Now:          fixedNow,
		API:          NewGitHubClient(srv.URL, "o/n", "test-token-abc"),
	}
	opts.Meta = RunMeta{
		Product: "SHEYTAN Music Visualizer", Workflow: "build-desktop.yml",
		RunID: "1", Attempt: "1", CommitSHA: "7c830c0", Ref: "refs/heads/main",
		Event: "push", CodeQL: &CodeQLSpec{Workflow: "codeql.yml", Job: "codeql", Result: "success"},
		Jobs: []JobSpec{
			{Name: "preflight", Result: ResultSuccess, Artifact: "preflight-logs-part", Required: true},
			{Name: "build-windows", Result: ResultFailure, Artifact: "windows-logs-part", Required: true},
		},
	}
	// One artifact part with benign content for the failed job: the
	// captured text carries no error marker at all.
	dir := root + "/windows-logs-part"
	if err := mkdirAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(dir+"/w.log", "all quiet on windows\n"); err != nil {
		t.Fatal(err)
	}

	res, err := Collect(opts)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	raw := readFile(t, opts.ReportPath)

	if res.APIJobsFetched != 3 {
		t.Fatalf("api jobs fetched = %d, want 3", res.APIJobsFetched)
	}
	if len(res.FailedSteps) != 1 || !strings.Contains(res.FailedSteps[0], "Build and verify Windows x64 desktop app") {
		t.Fatalf("failed steps = %v, want the failing build step", res.FailedSteps)
	}
	if res.APILogsFetched != 2 {
		t.Fatalf("api logs fetched = %d, want 2", res.APILogsFetched)
	}
	for _, want := range []string{
		"STEP FAILURE: build-windows / step 3 \"Build and verify Windows x64 desktop app\": failure",
		"JOB FAILURE: build-windows (workflow conclusion: failure)",
		"JOB FAILURE: build-windows (API job conclusion: failure)",
		"3. STEP STATUS TABLE",
		"5. FAILURE INDEX",
		"OVERALL RESULT: FAIL",
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("report missing %q\n---\n%s", want, raw)
		}
	}
	// The API raw job log section must be present.
	if !strings.Contains(raw, "#### API JOB LOG: build-windows (windows-latest, x64, UCRT64)") {
		t.Fatal("API job log not embedded in raw section")
	}
	// The token must never appear in the report.
	if strings.Contains(raw, "test-token-abc") {
		t.Fatal("report leaked the API token")
	}
}

func TestAlertsFetchedForSameCommit(t *testing.T) {
	srv := apiServer(t)
	root := t.TempDir()
	opts := Options{PartsRoot: root, ReportPath: root + "/log.txt", Now: fixedNow}
	opts.API = NewGitHubClient(srv.URL, "o/n", "")
	opts.Meta.CommitSHA = "7c830c0"
	opts.Meta.Jobs = []JobSpec{{Name: "build", Result: ResultSuccess, Required: true}}
	res, err := Collect(opts)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if res.AlertsFetched != 1 {
		t.Fatalf("alerts fetched = %d, want 1", res.AlertsFetched)
	}
	raw := readFile(t, opts.ReportPath)
	if !strings.Contains(raw, "alert #7 [medium] go/insecure-landing — cmd/x/main.go:42") {
		t.Fatalf("alert summary not rendered: %s", raw)
	}
}

func TestAPIFailureRecordedAsLimitation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Resource not accessible by integration"}`))
	}))
	t.Cleanup(srv.Close)
	root := t.TempDir()
	opts := Options{PartsRoot: root, ReportPath: root + "/log.txt", Now: fixedNow}
	opts.API = NewGitHubClient(srv.URL, "o/n", "")
	opts.Meta.Jobs = []JobSpec{{Name: "build", Result: ResultSuccess, Artifact: "build-part", Required: true}}
	dir := root + "/build-part"
	if err := mkdirAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(dir+"/b.log", "fine\n"); err != nil {
		t.Fatal(err)
	}
	res, err := Collect(opts)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if res.APIJobsFetched != 0 || res.APIError == "" {
		t.Fatalf("expected recorded API limitation, got jobs=%d err=%q", res.APIJobsFetched, res.APIError)
	}
	raw := readFile(t, opts.ReportPath)
	if !strings.Contains(raw, "API ENRICHMENT LIMITATION: access denied (HTTP 403)") {
		t.Fatalf("limitation not recorded truthfully:\n%s", raw)
	}
	// The manifest-based evidence still stands and the run stays green
	// on its own conclusions.
	if !strings.Contains(raw, "OVERALL RESULT: PASS") {
		t.Fatal("manifest success with complete coverage must remain PASS when the API is unavailable")
	}
}

func TestFetchRunJobsPaginationAndContext(t *testing.T) {
	srv := apiServer(t)
	c := NewGitHubClient(srv.URL, "o/n", "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	jobs, err := c.FetchRunJobs(ctx, "1", "1")
	if err != nil {
		t.Fatalf("FetchRunJobs: %v", err)
	}
	if len(jobs) != 3 || jobs[1].Conclusion != "failure" {
		t.Fatalf("jobs = %+v", jobs)
	}
}
