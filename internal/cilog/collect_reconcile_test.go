package cilog

// collect_reconcile_test.go — run-42231355353 report reconciliation
// fixtures: explicit manifest-evidence-record model (kind + result
// basis), nested reusable-workflow job matching, and conditional API
// limitation notes that never reference a section that was not emitted.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The report must present manifest rows as EVIDENCE RECORDS with their
// own kind and result basis — never as plain "Declared jobs", so the
// caller's aggregate CodeQL conclusion is not mistaken for a per-language
// observation.
func TestManifestEvidenceRecordsLabelAndKinds(t *testing.T) {
	root := t.TempDir()
	opts := Options{PartsRoot: root, ReportPath: filepath.Join(t.TempDir(), "report.txt"), Now: fixedNow}
	mk := func(name, content string) {
		t.Helper()
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "job.log"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("preflight-logs-part-attempt1", "EXIT_CODE: 0 PASS\n")
	mk("codeql-logs-part-go-attempt1", "analysis_job_status=success\n")

	opts.Meta.Jobs = []JobSpec{
		{Name: "preflight", Kind: "workflow-job", Result: ResultSuccess, Artifact: "preflight-logs-part-attempt1", Required: true,
			ResultBasis: "needs.preflight.result (workflow conclusion)"},
		{Name: "analyze (go)", Kind: "nested-reusable-workflow-job", Result: ResultSuccess, Artifact: "codeql-logs-part-go-attempt1", Required: true,
			ResultBasis: "caller aggregate needs.codeql.result; per-language conclusion from this row's own artifact/API observation"},
	}
	res, report := mustCollect(t, opts)
	if res.Coverage != CoverageComplete {
		t.Fatalf("coverage = %s, want COMPLETE", res.Coverage)
	}
	if !strings.Contains(report, "Manifest evidence records: 2") {
		t.Fatal("report must label manifest rows as evidence records with the count")
	}
	if strings.Contains(report, "Declared jobs:") {
		t.Fatal("report must not mislabel manifest evidence records as Declared jobs")
	}
	if !strings.Contains(report, "nested-reusable-workflow-job") || !strings.Contains(report, "workflow-job ") {
		t.Fatal("report must print each row's kind")
	}
	if !strings.Contains(report, "declared result basis: caller aggregate needs.codeql.result") {
		t.Fatal("report must print the declared result basis for aggregate rows")
	}
}

// Nested reusable-workflow jobs are matched against the API's real job
// names without conflating the languages, and the reusable-workflow
// caller wrapper is never matched onto a declared row.
func TestNestedReusableJobMatchesAPINames(t *testing.T) {
	jobs := []JobSpec{
		{Name: "analyze (go)"},
		{Name: "analyze (actions)"},
	}
	cases := []struct {
		apiName string
		want    string // declared job name; "" = must NOT match
	}{
		{"CodeQL Analyze (Go, manual traced build)", "analyze (go)"},
		{"CodeQL Analyze (GitHub Actions workflows)", "analyze (actions)"},
		{"codeql (reusable analysis)", ""},
		{"collect-logs (single log.txt evidence)", ""},
		{"build-windows (windows-latest, x64, UCRT64)", ""},
		{"CodeQL Analyze (Go, manual traced build) #2", "analyze (go)"},
	}
	for _, tc := range cases {
		got, ok := matchDeclaredJob(jobs, tc.apiName)
		if tc.want == "" {
			if ok {
				t.Errorf("API job %q must not match any declared row, matched %q", tc.apiName, got.Name)
			}
			continue
		}
		if !ok || got.Name != tc.want {
			t.Errorf("API job %q matched %q (ok=%v), want %q", tc.apiName, got.Name, ok, tc.want)
		}
	}
}

// deriveFailures must keep treating the caller-aggregate row as failure
// evidence for the nested children (conservative: an aggregate failure
// fails both language rows; an aggregate success never masks anything).
func TestAggregateFailureFailsNestedRows(t *testing.T) {
	meta := RunMeta{Jobs: []JobSpec{
		{Name: "analyze (go)", Result: ResultSuccess, Required: true},
		{Name: "analyze (actions)", Result: ResultSuccess, Required: true},
	}}
	// The caller row failed (one nested leg failed) while the manifest
	// rows carry the stale aggregate-less success: the API conclusion is
	// authoritative and must fail the corresponding declared rows.
	apiJobs := []JobConclusion{
		{ID: 1, Name: "CodeQL Analyze (Go, manual traced build)", Conclusion: ResultFailure, Status: "completed"},
		{ID: 2, Name: "CodeQL Analyze (GitHub Actions workflows)", Conclusion: ResultSuccess, Status: "completed"},
		{ID: 3, Name: "codeql (reusable analysis)", Conclusion: ResultFailure, Status: "completed"},
	}
	failed, _ := deriveFailures(meta, apiJobs)
	joined := strings.Join(failed, "; ")
	if !strings.Contains(joined, "analyze (go)") {
		t.Fatalf("failed rows must include analyze (go), got: %v", failed)
	}
	if strings.Contains(joined, "analyze (actions)") {
		t.Fatalf("successful actions leg must not be failed, got: %v", failed)
	}
	if strings.Contains(joined, "codeql (reusable analysis)") {
		t.Fatalf("the caller wrapper must never be counted as a declared row failure, got: %v", failed)
	}
}

// Without a configured API client the report must say "API enrichment
// not configured" — it must NOT reference an "API ENRICHMENT LIMITATION"
// section that was never emitted (the dangling-reference defect from the
// run-42231355353 consolidated report).
func TestAPINotesAreConditionalWithoutClient(t *testing.T) {
	opts, _ := fixture(t, []tJob{
		{"analyze (go)", ResultSuccess, "codeql-logs-part-go-attempt1", false, map[string]string{"codeql-go-result.log": "analysis_job_status=success\n"}},
	})
	opts.Meta.CodeQL = &CodeQLSpec{Workflow: "codeql.yml", Job: "analyze", Result: ResultSuccess}
	res, report := mustCollect(t, opts)
	if res.Coverage != CoverageComplete {
		t.Fatalf("coverage = %s, want COMPLETE", res.Coverage)
	}
	if strings.Contains(report, "API ENRICHMENT LIMITATION") {
		t.Fatal("report without an API client must not emit an API ENRICHMENT LIMITATION section")
	}
	if strings.Contains(report, "see API ENRICHMENT LIMITATION above") {
		t.Fatal("dangling reference: note points at a section that was not emitted")
	}
	if !strings.Contains(report, "API enrichment not configured") {
		t.Fatal("report must state the exact unavailable reason (API enrichment not configured)")
	}
}

// A failed alerts query (HTTP 403/404/429) must be reported as
// NOT RETRIEVED with its reason — never as zero open alerts
// (run-42231355353 evidence-integrity rule).
func TestAlertFetchFailureIsNotZeroAlerts(t *testing.T) {
	for _, code := range []int{http.StatusForbidden, http.StatusNotFound, http.StatusTooManyRequests} {
		mux := http.NewServeMux()
		mux.HandleFunc("/repos/o/n/code-scanning/alerts", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
		})
		srv := httptest.NewServer(mux)
		opts, _ := fixture(t, []tJob{
			{"preflight", ResultSuccess, "preflight-logs-part-attempt1", false, map[string]string{"b.log": "ok\n"}},
		})
		opts.Meta.CodeQL = &CodeQLSpec{Workflow: "codeql.yml", Job: "analyze", Result: ResultSuccess}
		opts.Meta.CommitSHA = "dbedaa455dd3e5704452dc64bd1f56b02fc8e42c"
		opts.API = NewGitHubClient(srv.URL, "o/n", "t")
		_, report := mustCollect(t, opts)
		if strings.Contains(report, "zero open alerts were actually retrieved") {
			t.Fatalf("HTTP %d must never be reported as zero open alerts", code)
		}
		if !strings.Contains(report, "NOT RETRIEVED") {
			t.Fatalf("HTTP %d must be reported as NOT RETRIEVED with the exact reason", code)
		}
		srv.Close()
	}
}
