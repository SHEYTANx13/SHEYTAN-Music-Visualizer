// Package cilog implements the consolidated CI run-log collector for
// SHEYTAN Music Visualizer. The final user-facing diagnostic output of
// the build-desktop workflow is ONE plain-text file named exactly
// `log.txt`, produced by this package.
//
// It reads a small JSON run manifest and a parts root containing
// per-job log artifacts in namespaced directories (one directory per
// artifact, never flattened), optionally queries the GitHub REST API
// for the AUTHORITATIVE job/step conclusions and complete raw job logs
// of the same run attempt, and writes one deterministic plain-text
// consolidated report containing, in this stable order:
//
//  1. run metadata (product, workflow, run id, attempt, commit, event,
//     start/end times, runner metadata);
//  2. a JOB STATUS TABLE listing every expected job (manifest/needs
//     result) and every API-observed job with its actual conclusion
//     (success/failure/cancelled/skipped/missing);
//  3. a STEP STATUS TABLE per job — including failed and cancelled
//     steps that carry no `::error::` marker in any captured text;
//  4. the complete available raw stdout/stderr, both from the captured
//     artifact parts and from the API job logs, verbatim except for
//     deliberate secret redaction, with command, working directory,
//     tool context and timestamps; missing exit codes are explicitly
//     marked, never fabricated;
//  5. a FAILURE INDEX derived from the authoritative job/step
//     conclusions AND raw text error records — a job conclusion
//     `failure` appears here even when no textual error marker exists;
//  6. a WARNING INDEX preserving actual workflow warnings and tool
//     diagnostics; a run is never called clean merely because no
//     `::warning::` text was found;
//  7. CodeQL initialization/build/analyze/SARIF results for the same
//     commit and the open code scanning alerts actually retrieved —
//     with the exact access limitation recorded when retrieval fails;
//  8. tests, build metadata, artifact collection and collection
//     failures, including which job/step log is missing and why;
//  9. the final truthful overall result derived from the required job
//     conclusions. A collector PASS can never convert a failed build
//     or CodeQL job into an overall PASS.
//
// Coverage is about evidence, not build success: a run with failed
// commands but complete log parts is COMPLETE; a run with a missing
// log part is INCOMPLETE and must fail the collecting job after the
// report has been written and uploaded. The OVERALL result is FAIL
// whenever any required job did not succeed.
//
// Standard library only: this collector must stay runnable even when
// the application dependency graph is broken.
package cilog

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// contextWithTimeout is a tiny indirection so tests and callers share
// one bounded-budget context constructor.
func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// Coverage values for Result.Coverage.
const (
	CoverageComplete   = "COMPLETE"
	CoverageIncomplete = "INCOMPLETE"
)

// Accepted job result values in the run manifest.
const (
	ResultSuccess   = "success"
	ResultFailure   = "failure"
	ResultCancelled = "cancelled"
	ResultSkipped   = "skipped"
)

// Overall result values for Result.Overall.
const (
	OverallPass = "PASS"
	OverallFail = "FAIL"
)

// JobSpec declares one expected log-producing job.
type JobSpec struct {
	Name string `json:"name"`
	// Result is one of: success, failure, cancelled, skipped.
	Result string `json:"result"`
	// Artifact is the expected artifact directory name under the parts
	// root. Empty means the job is expected to contribute no log part.
	Artifact string `json:"artifact,omitempty"`
	// Required marks jobs whose failure must fail the whole run. Empty
	// or false keeps the job informational (e.g. evidence collectors).
	Required bool `json:"required,omitempty"`
}

// CodeQLSpec carries the manifest-declared CodeQL evidence for the
// same commit; the collector enriches it with API-observed data.
type CodeQLSpec struct {
	Workflow string `json:"workflow,omitempty"`
	Job      string `json:"job,omitempty"`
	// Result is the declared conclusion of the CodeQL job as observed
	// by the calling workflow (needs.<job>.result), if known.
	Result string `json:"result,omitempty"`
}

// RunMeta is the run manifest: header metadata plus expected jobs.
type RunMeta struct {
	Product      string      `json:"product"`
	Repository   string      `json:"repository,omitempty"`
	Workflow     string      `json:"workflow"`
	RunID        string      `json:"run_id"`
	Attempt      string      `json:"attempt"`
	CommitSHA    string      `json:"commit_sha"`
	Ref          string      `json:"ref"`
	Event        string      `json:"event"`
	StartTimeUTC string      `json:"start_time_utc"`
	RunnerOS     string      `json:"runner_os,omitempty"`
	CodeQL       *CodeQLSpec `json:"codeql,omitempty"`
	Jobs         []JobSpec   `json:"jobs"`
}

// Options configures Collect.
type Options struct {
	Meta       RunMeta
	PartsRoot  string
	ReportPath string
	// API, when non-nil, is used to fetch authoritative job/step
	// conclusions and raw job logs for Meta.RunID/Meta.Attempt. Best
	// effort: failures are recorded in the report, never fatal.
	API *GitHubClient
	// FetchJobLogs additionally downloads each job's complete raw log
	// through the API (implies authoritative step evidence).
	FetchJobLogs bool
	// ExtraSecrets are exact secret values (e.g. the workflow token)
	// redacted verbatim from all output.
	ExtraSecrets []string
	// Now overrides the clock for the report-generation stamp
	// (injected by tests for deterministic output). nil = time.Now.
	Now func() time.Time
	// APIBudget bounds total API log fetching; zero = 90s.
	APIBudget time.Duration
}

// Result summarizes one Collect run.
type Result struct {
	Coverage             string
	Overall              string
	MissingSources       []string
	NeverStarted         []string
	JobsExpected         int
	JobsWithParts        int
	LogFiles             int
	FailedCommandRecords int
	WarningRecords       int
	FailedJobs           []string // authoritative required-job failures
	FailedSteps          []string // authoritative failed steps "job/step"
	APIJobsFetched       int
	APIJobsWithSteps     int
	APILogsFetched       int
	APIError             string
	AlertsFetched        int
	AlertsError          string
	ReportPath           string
}

type logFile struct {
	job      JobSpec
	artifact string
	rel      string // slash-separated relative path inside the artifact dir
	abs      string
}

type finding struct {
	where string // "[job | artifact/file:line]"
	line  string
}

var (
	errorPatterns = []*regexp.Regexp{
		regexp.MustCompile(`^EXIT_CODE: [1-9][0-9]* FAIL\b`),
		regexp.MustCompile(`::error::`),
		regexp.MustCompile(`^--- FAIL: `),
		regexp.MustCompile(`^FAIL[ \t]+\S`),
		regexp.MustCompile(`^ERROR:`),
	}
	warningPatterns = []*regexp.Regexp{
		regexp.MustCompile(`::warning::`),
		regexp.MustCompile(`^WARNING:`),
	}
)

func validResult(r string) bool {
	switch r {
	case ResultSuccess, ResultFailure, ResultCancelled, ResultSkipped:
		return true
	}
	return false
}

// Collect validates the manifest, scans the parts root, optionally
// enriches via the GitHub API, writes the consolidated report to
// opts.ReportPath, and returns the result.
//
// A missing log source is NOT a hard error: the report is still written
// (with Coverage=INCOMPLETE) so it can be uploaded before failing. Hard
// errors (invalid manifest, duplicate artifact names, unwritable
// report) return a non-nil error and no report.
func Collect(opts Options) (Result, error) {
	var res Result
	if strings.TrimSpace(opts.ReportPath) == "" {
		return res, fmt.Errorf("cilog: report path must not be empty")
	}
	meta := opts.Meta
	if len(meta.Jobs) == 0 {
		return res, fmt.Errorf("cilog: manifest must declare at least one job")
	}
	seenArtifacts := map[string]string{}
	for _, j := range meta.Jobs {
		if strings.TrimSpace(j.Name) == "" {
			return res, fmt.Errorf("cilog: manifest contains a job with an empty name")
		}
		if !validResult(j.Result) {
			return res, fmt.Errorf("cilog: job %q has invalid result %q (want success|failure|cancelled|skipped)", j.Name, j.Result)
		}
		if j.Artifact != "" {
			if prev, ok := seenArtifacts[j.Artifact]; ok {
				return res, fmt.Errorf("cilog: artifact %q declared by both %q and %q; artifact names must be unique", j.Artifact, prev, j.Name)
			}
			seenArtifacts[j.Artifact] = j.Name
		}
	}

	// Gather log files per job and detect missing sources.
	var files []logFile
	for _, j := range meta.Jobs {
		if j.Result == ResultSkipped {
			res.NeverStarted = append(res.NeverStarted, j.Name)
			continue
		}
		if j.Artifact == "" {
			continue
		}
		abs := filepath.Join(opts.PartsRoot, j.Artifact)
		info, err := os.Stat(abs)
		if err != nil || !info.IsDir() {
			res.MissingSources = append(res.MissingSources,
				fmt.Sprintf("%s (expected artifact directory %q under parts root)", j.Name, j.Artifact))
			continue
		}
		var jobFiles []logFile
		walkErr := filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			rel, rerr := filepath.Rel(abs, path)
			if rerr != nil {
				return rerr
			}
			jobFiles = append(jobFiles, logFile{
				job:      j,
				artifact: j.Artifact,
				rel:      filepath.ToSlash(rel),
				abs:      path,
			})
			return nil
		})
		if walkErr != nil {
			return res, fmt.Errorf("cilog: scanning artifact %q for job %q: %w", j.Artifact, j.Name, walkErr)
		}
		if len(jobFiles) == 0 {
			// An artifact directory with no files carries no evidence.
			res.MissingSources = append(res.MissingSources,
				fmt.Sprintf("%s (artifact directory %q exists but contains no log files)", j.Name, j.Artifact))
			continue
		}
		res.JobsWithParts++
		files = append(files, jobFiles...)
	}

	// Deterministic order: manifest job order, then lexicographic file paths.
	sort.SliceStable(files, func(i, k int) bool {
		ji, jk := jobIndex(meta.Jobs, files[i].job.Name), jobIndex(meta.Jobs, files[k].job.Name)
		if ji != jk {
			return ji < jk
		}
		return files[i].rel < files[k].rel
	})

	// Scan for error/warning findings with provenance.
	var errFindings, warnFindings []finding
	for _, f := range files {
		fh, err := os.Open(f.abs)
		if err != nil {
			return res, fmt.Errorf("cilog: reading %q: %w", f.abs, err)
		}
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		lineNo := 0
		for sc.Scan() {
			lineNo++
			line := sc.Text()
			for _, re := range errorPatterns {
				if re.MatchString(line) {
					errFindings = append(errFindings, finding{
						where: fmt.Sprintf("[%s | %s/%s:%d]", f.job.Name, f.artifact, f.rel, lineNo),
						line:  line,
					})
					break
				}
			}
			for _, re := range warningPatterns {
				if re.MatchString(line) {
					warnFindings = append(warnFindings, finding{
						where: fmt.Sprintf("[%s | %s/%s:%d]", f.job.Name, f.artifact, f.rel, lineNo),
						line:  line,
					})
					break
				}
			}
		}
		serr := sc.Err()
		cerr := fh.Close()
		if serr != nil {
			return res, fmt.Errorf("cilog: scanning %q: %w", f.abs, serr)
		}
		if cerr != nil {
			return res, fmt.Errorf("cilog: closing %q: %w", f.abs, cerr)
		}
	}
	res.FailedCommandRecords = len(errFindings)
	res.WarningRecords = len(warnFindings)
	res.LogFiles = len(files)
	res.JobsExpected = len(meta.Jobs)

	// Authoritative enrichment: GitHub API job/step conclusions, raw
	// job logs, and same-commit code scanning alerts. Best effort.
	var red *Redactor
	if opts.API != nil {
		red = NewRedactor(append(opts.ExtraSecrets, opts.API.Token))
	} else {
		red = NewRedactor(opts.ExtraSecrets)
	}

	type enrich struct {
		jobs []JobConclusion
		logs map[int]string
	}
	var en enrich
	en.logs = map[int]string{}
	if opts.API != nil {
		budget := opts.APIBudget
		if budget <= 0 {
			budget = 90 * time.Second
		}
		ctx, cancel := contextWithTimeout(budget)
		jobs, err := opts.API.FetchRunJobs(ctx, meta.RunID, meta.Attempt)
		if err != nil {
			res.APIError = err.Error()
		} else {
			en.jobs = jobs
			res.APIJobsFetched = len(jobs)
			for _, j := range jobs {
				if len(j.Steps) > 0 {
					res.APIJobsWithSteps++
				}
				if opts.FetchJobLogs {
					logText, lerr := opts.API.FetchJobLog(ctx, j.ID)
					if lerr != nil {
						if res.APIError != "" {
							res.APIError += "; "
						}
						res.APIError += fmt.Sprintf("job log %q (%d): %v", j.Name, j.ID, lerr)
						continue
					}
					en.logs[j.ID] = logText
					res.APILogsFetched++
				}
			}
		}
		cancel()
	}

	// Authoritative failure derivation. A required job declared failed/
	// cancelled in the manifest is a failure even with zero text markers;
	// the same holds for API conclusions of matching jobs.
	overallFailures := deriveFailures(meta, en.jobs)
	res.FailedJobs = overallFailures
	res.FailedSteps = deriveFailedSteps(meta, en.jobs)

	// Code scanning alerts for the exact analyzed commit.
	var alerts []CodeScanningAlert
	if opts.API != nil && strings.TrimSpace(meta.CommitSHA) != "" {
		ctx, cancel := contextWithTimeout(45 * time.Second)
		fetched, aerr := opts.API.FetchOpenAlerts(ctx, meta.CommitSHA)
		cancel()
		if aerr != nil {
			res.AlertsError = aerr.Error()
		} else {
			alerts = fetched
			res.AlertsFetched = len(alerts)
			sort.Slice(alerts, func(i, k int) bool {
				if alerts[i].RuleID != alerts[k].RuleID {
					return alerts[i].RuleID < alerts[k].RuleID
				}
				if alerts[i].FilePath != alerts[k].FilePath {
					return alerts[i].FilePath < alerts[k].FilePath
				}
				return alerts[i].Number < alerts[k].Number
			})
		}
	}

	// Final truthful overall result: every required job must have
	// succeeded AND evidence coverage must be complete.
	switch {
	case len(overallFailures) > 0:
		res.Overall = OverallFail
	case len(res.MissingSources) > 0:
		res.Overall = OverallFail
	default:
		res.Overall = OverallPass
	}

	now := opts.Now
	if now == nil {
		now = time.Now
	}

	var b strings.Builder
	writeReport(&b, opts, meta, res, files, en.jobs, en.logs, alerts, errFindings, warnFindings, overallFailures, red, now().UTC())

	if err := os.WriteFile(opts.ReportPath, []byte(b.String()), 0o644); err != nil {
		return res, fmt.Errorf("cilog: writing report %q: %w", opts.ReportPath, err)
	}
	res.ReportPath = opts.ReportPath
	if len(res.MissingSources) > 0 {
		res.Coverage = CoverageIncomplete
	} else {
		res.Coverage = CoverageComplete
	}
	return res, nil
}

// deriveFailures returns the required jobs that did not succeed,
// from the manifest results first and any API conclusions observed.
func deriveFailures(meta RunMeta, apiJobs []JobConclusion) []string {
	var failed []string
	for _, j := range meta.Jobs {
		if !j.Required {
			continue
		}
		switch j.Result {
		case ResultSuccess:
			// manifest says success; an API failure for the same job
			// would be contradictory and is checked below.
		case ResultFailure, ResultCancelled:
			failed = append(failed, fmt.Sprintf("%s (workflow conclusion: %s)", j.Name, j.Result))
			continue
		case ResultSkipped:
			failed = append(failed, fmt.Sprintf("%s (workflow conclusion: skipped)", j.Name))
			continue
		}
	}
	// Cross-check API conclusions against every declared job.
	for _, aj := range apiJobs {
		declared, ok := matchDeclaredJob(meta.Jobs, aj.Name)
		if !ok || !declared.Required {
			continue
		}
		if aj.Conclusion != "" && aj.Conclusion != ResultSuccess && aj.Conclusion != "skipped" {
			failed = append(failed, fmt.Sprintf("%s (API job conclusion: %s)", declared.Name, aj.Conclusion))
		}
	}
	return failed
}

// deriveFailedSteps lists failed/cancelled steps per declared job from
// API data, so a failing step without any textual marker is visible.
func deriveFailedSteps(meta RunMeta, apiJobs []JobConclusion) []string {
	var out []string
	for _, aj := range apiJobs {
		declared, ok := matchDeclaredJob(meta.Jobs, aj.Name)
		if !ok {
			continue
		}
		for _, st := range aj.Steps {
			if st.Conclusion == "failure" || st.Conclusion == "cancelled" ||
				st.Conclusion == "timed_out" || st.Conclusion == "startup_failure" {
				out = append(out, fmt.Sprintf("%s / step %d %q: %s", declared.Name, st.Number, st.Name, st.Conclusion))
			}
		}
	}
	return out
}

// matchDeclaredJob maps an API job name onto a declared manifest job.
// The workflow declares short names ("build-windows") while the API
// reports the display name ("build-windows (windows-latest, x64, UCRT64)").
func matchDeclaredJob(jobs []JobSpec, apiName string) (JobSpec, bool) {
	an := strings.ToLower(strings.TrimSpace(apiName))
	for _, j := range jobs {
		jn := strings.ToLower(strings.TrimSpace(j.Name))
		if an == jn || strings.HasPrefix(an, jn+" ") || strings.HasPrefix(an, jn+"(") {
			return j, true
		}
	}
	return JobSpec{}, false
}

func jobIndex(jobs []JobSpec, name string) int {
	for i, j := range jobs {
		if j.Name == name {
			return i
		}
	}
	return len(jobs)
}

const separator = "--------------------------------------------------------------"
const heavySeparator = "=============================================================="

func writeReport(w *strings.Builder, opts Options, meta RunMeta, res Result, files []logFile, apiJobs []JobConclusion, apiLogs map[int]string, alerts []CodeScanningAlert, errs, warns []finding, failedJobs []string, red *Redactor, generated time.Time) {
	fmt.Fprintf(w, "%s\nSHEYTAN CONSOLIDATED RUN REPORT (log.txt)\n%s\n", heavySeparator, heavySeparator)

	// ---- 1. RUN METADATA ------------------------------------------------
	start := meta.StartTimeUTC
	for _, j := range apiJobs {
		if j.StartedAt != "" && (start == "" || j.StartedAt < start) {
			start = j.StartedAt
		}
	}
	fmt.Fprintf(w, "\n%s\n1. RUN METADATA\n%s\n", separator, separator)
	fmt.Fprintf(w, "Product:          %s\n", meta.Product)
	fmt.Fprintf(w, "Repository:       %s\n", orDash(meta.Repository))
	fmt.Fprintf(w, "Workflow:         %s\n", meta.Workflow)
	fmt.Fprintf(w, "Run ID:           %s\n", meta.RunID)
	fmt.Fprintf(w, "Attempt:          %s\n", meta.Attempt)
	fmt.Fprintf(w, "Commit SHA:       %s\n", meta.CommitSHA)
	fmt.Fprintf(w, "Ref:              %s\n", meta.Ref)
	fmt.Fprintf(w, "Event:            %s\n", meta.Event)
	fmt.Fprintf(w, "Start time (UTC): %s\n", orNote(start, "not provided by manifest and no API job start times"))
	fmt.Fprintf(w, "Generated (UTC):  %s\n", generated.Format("2006-01-02T15:04:05Z"))
	fmt.Fprintf(w, "Runner OS:        %s\n", orDash(meta.RunnerOS))
	if len(apiJobs) > 0 {
		fmt.Fprintf(w, "Runners (API):    ")
		seen := map[string]bool{}
		var labels []string
		for _, j := range apiJobs {
			key := strings.Join(j.Labels, ",")
			if key != "" && !seen[key] {
				seen[key] = true
				labels = append(labels, j.Name+" → ["+key+"]")
			}
		}
		fmt.Fprintf(w, "%s\n", strings.Join(labels, "; "))
	}

	// ---- 2. JOB STATUS TABLE -------------------------------------------
	jobsSucceeded, jobsFailed, jobsCancelled, jobsSkipped := 0, 0, 0, 0
	for _, j := range meta.Jobs {
		switch j.Result {
		case ResultSuccess:
			jobsSucceeded++
		case ResultFailure:
			jobsFailed++
		case ResultCancelled:
			jobsCancelled++
		case ResultSkipped:
			jobsSkipped++
		}
	}
	fmt.Fprintf(w, "\n%s\n2. JOB STATUS TABLE\n%s\n", separator, separator)
	fmt.Fprintf(w, "Declared jobs: %d (succeeded %d, failed %d, cancelled %d, skipped %d)\n\n",
		res.JobsExpected, jobsSucceeded, jobsFailed, jobsCancelled, jobsSkipped)
	fmt.Fprintf(w, "%-28s %-12s %-22s %-14s %s\n", "JOB", "REQUIRED", "CONCLUSION (workflow)", "API CONCLUSION", "LOG SOURCE")
	for _, j := range meta.Jobs {
		req := "yes"
		if !j.Required {
			req = "no"
		}
		src := "-"
		if j.Artifact != "" {
			src = j.Artifact
			if containsPrefix(res.MissingSources, j.Name) {
				src += " MISSING"
			}
		}
		apiConc := "unavailable"
		if j.Result == ResultSkipped {
			apiConc = "not run"
		}
		for _, aj := range apiJobs {
			if _, ok := matchDeclaredJob([]JobSpec{j}, aj.Name); ok {
				apiConc = orNote(aj.Conclusion, "none (status "+aj.Status+")")
				break
			}
		}
		fmt.Fprintf(w, "%-28s %-12s %-22s %-14s %s\n", j.Name, req, j.Result, apiConc, src)
	}
	if len(apiJobs) > 0 {
		fmt.Fprintln(w)
		for _, aj := range apiJobs {
			if _, ok := matchDeclaredJob(meta.Jobs, aj.Name); !ok {
				fmt.Fprintf(w, "OBSERVED (not declared in manifest): %s → conclusion %s\n", aj.Name, orNote(aj.Conclusion, "none (status "+aj.Status+")"))
			}
		}
	}
	if res.APIError != "" {
		fmt.Fprintf(w, "\nAPI ENRICHMENT LIMITATION: %s\nJob/step conclusions above fall back to the manifest (needs.*.result) values.\n", res.APIError)
	}

	// ---- 3. STEP STATUS TABLE ------------------------------------------
	fmt.Fprintf(w, "\n%s\n3. STEP STATUS TABLE\n%s\n", separator, separator)
	if len(apiJobs) == 0 {
		fmt.Fprintf(w, "UNAVAILABLE — no API job/step data for this run. Raw captured logs in section 4 are the\n")
		fmt.Fprintf(w, "available evidence; a step can fail without emitting any error marker, so absence of error\n")
		fmt.Fprintf(w, "markers in section 4 must never be read as proof that every step succeeded.\n")
	} else {
		for _, aj := range apiJobs {
			name := aj.Name
			if declared, ok := matchDeclaredJob(meta.Jobs, aj.Name); ok {
				name = declared.Name + " (API: " + aj.Name + ")"
			}
			fmt.Fprintf(w, "\nJOB: %s — conclusion %s, started %s, completed %s\n",
				name, orNote(aj.Conclusion, "none (status "+aj.Status+")"), orDash(aj.StartedAt), orDash(aj.CompletedAt))
			if len(aj.Steps) == 0 {
				fmt.Fprintf(w, "  (no step list exposed for this job)\n")
				continue
			}
			for _, st := range aj.Steps {
				fmt.Fprintf(w, "  step #%-2d %-52s %s/%s\n", st.Number, st.Name, st.Status, orNote(st.Conclusion, "none"))
			}
		}
	}

	// ---- 4. RAW LOGS (verbatim, redacted) ------------------------------
	fmt.Fprintf(w, "\n%s\n4. RAW LOGS (verbatim, untruncated; secrets redacted)\n%s\n", separator, separator)
	if len(files) == 0 {
		fmt.Fprintf(w, "(no captured artifact log files were consolidated)\n")
	}
	for _, f := range files {
		fmt.Fprintf(w, "\n#### JOB: %s | ARTIFACT: %s | FILE: %s\n#### LOG CONTENT BEGIN\n", f.job.Name, f.artifact, f.rel)
		fh, err := os.Open(f.abs)
		if err != nil {
			fmt.Fprintf(w, "#### COLLECTOR ERROR: cannot open file: %v\n", err)
			continue
		}
		raw, rerr := io.ReadAll(fh)
		closeErr := fh.Close()
		if rerr != nil {
			fmt.Fprintf(w, "#### COLLECTOR ERROR: cannot read file: %v\n", rerr)
		} else {
			w.WriteString(red.Apply(string(raw)))
			if !strings.HasSuffix(w.String(), "\n") {
				w.WriteString("\n")
			}
		}
		if closeErr != nil {
			fmt.Fprintf(w, "#### COLLECTOR ERROR: cannot close file: %v\n", closeErr)
		}
		fmt.Fprintf(w, "#### LOG CONTENT END\n")
	}
	if len(apiLogs) > 0 {
		for _, aj := range apiJobs {
			logText, ok := apiLogs[aj.ID]
			if !ok || logText == "" {
				continue
			}
			fmt.Fprintf(w, "\n#### API JOB LOG: %s (id %d, complete raw log incl. post steps)\n#### LOG CONTENT BEGIN\n", aj.Name, aj.ID)
			w.WriteString(red.Apply(logText))
			if !strings.HasSuffix(w.String(), "\n") {
				w.WriteString("\n")
			}
			fmt.Fprintf(w, "#### LOG CONTENT END\n")
		}
	}

	// ---- 5. FAILURE INDEX ----------------------------------------------
	fmt.Fprintf(w, "\n%s\n5. FAILURE INDEX\n%s\n", separator, separator)
	authoritativeCount := len(failedJobs) + len(res.FailedSteps)
	if authoritativeCount > 0 {
		fmt.Fprintf(w, "A. AUTHORITATIVE JOB CONCLUSIONS (required jobs that did not succeed):\n")
		for _, f := range failedJobs {
			fmt.Fprintf(w, "   JOB FAILURE: %s\n", f)
		}
		if len(res.FailedSteps) > 0 {
			fmt.Fprintf(w, "B. AUTHORITATIVE STEP CONCLUSIONS (failed/cancelled steps):\n")
			for _, f := range res.FailedSteps {
				fmt.Fprintf(w, "   STEP FAILURE: %s\n", f)
			}
		}
	} else {
		fmt.Fprintf(w, "A. AUTHORITATIVE JOB CONCLUSIONS: none — every required job succeeded.\n")
	}
	fmt.Fprintf(w, "\nC. TEXT-DERIVED ERROR RECORDS (supplemental; provenance included):\n")
	if len(errs) == 0 {
		fmt.Fprintf(w, "   (no failed command records, ::error:: lines, failed Go test summaries or ERROR: lines found)\n")
	} else {
		for _, f := range errs {
			fmt.Fprintf(w, "   %s %s\n", f.where, red.Apply(f.line))
		}
	}
	if len(failedJobs) > 0 && len(errs) == 0 {
		fmt.Fprintf(w, "\n   NOTE: required job(s) failed WITHOUT any textual error marker in the captured logs.\n")
		fmt.Fprintf(w, "   The failure is real (see section A/2/3); text scanning is not the status authority.\n")
	}

	// ---- 6. WARNING INDEX ----------------------------------------------
	fmt.Fprintf(w, "\n%s\n6. WARNING INDEX\n%s\n", separator, separator)
	if len(warns) == 0 {
		fmt.Fprintf(w, "(no ::warning:: or WARNING: lines found in the consolidated logs)\n")
	} else {
		for _, f := range warns {
			fmt.Fprintf(w, "%s %s\n", f.where, red.Apply(f.line))
		}
	}
	fmt.Fprintf(w, "Note: GitHub UI annotations (warning toasts) are not exposed by the REST jobs API; only\n")
	fmt.Fprintf(w, "text markers inside the captured/fetched logs are indexed here. Absence of warnings in this\n")
	fmt.Fprintf(w, "index does not certify a warning-free run.\n")

	// ---- 7. CODEQL EVIDENCE --------------------------------------------
	fmt.Fprintf(w, "\n%s\n7. CODEQL EVIDENCE (same commit)\n%s\n", separator, separator)
	if meta.CodeQL == nil {
		fmt.Fprintf(w, "No CodeQL evidence was declared for this run (manifest has no codeql section).\n")
	} else {
		fmt.Fprintf(w, "Declared workflow/job: %s / %s\n", orDash(meta.CodeQL.Workflow), orDash(meta.CodeQL.Job))
		fmt.Fprintf(w, "Declared job result:   %s\n", orNote(meta.CodeQL.Result, "not provided"))
		observed := ""
		for _, aj := range apiJobs {
			if strings.Contains(strings.ToLower(aj.Name), "codeql") ||
				strings.Contains(strings.ToLower(aj.Name), "analyze") {
				if declared, ok := matchDeclaredJob(meta.Jobs, aj.Name); ok && declared.Name == meta.CodeQL.Job {
					observed = aj.Conclusion
					break
				}
			}
		}
		fmt.Fprintf(w, "API-observed conclusion: %s\n", orNote(observed, "unavailable (see API ENRICHMENT LIMITATION above)"))
		sarif := "unknown"
		for _, aj := range apiJobs {
			for _, st := range aj.Steps {
				if strings.Contains(strings.ToLower(st.Name), "codeql") && strings.Contains(strings.ToLower(st.Name), "analysis") {
					sarif = st.Conclusion
				}
			}
		}
		fmt.Fprintf(w, "Analyze/SARIF upload step conclusion: %s\n", orNote(sarif, "unavailable"))
	}
	if res.AlertsError != "" {
		fmt.Fprintf(w, "Open alerts for commit %s: NOT RETRIEVED (%s)\n", meta.CommitSHA, res.AlertsError)
	} else if alerts == nil {
		fmt.Fprintf(w, "Open alerts for commit %s: NOT RETRIEVED (API client not configured)\n", meta.CommitSHA)
	} else {
		fmt.Fprintf(w, "Open code scanning alerts for commit %s: %d\n", meta.CommitSHA, len(alerts))
		for _, a := range alerts {
			fmt.Fprintf(w, "  alert #%d [%s] %s — %s:%d — %s\n", a.Number, orDash(a.Severity), orDash(a.RuleID), orDash(a.FilePath), a.StartLine, orDash(a.RuleDesc))
		}
		if len(alerts) == 0 {
			fmt.Fprintf(w, "  (zero open alerts were actually retrieved from the API for this commit)\n")
		}
	}

	// ---- 8. TESTS / BUILD / ARTIFACT / COLLECTION -----------------------
	fmt.Fprintf(w, "\n%s\n8. TESTS, BUILD, ARTIFACT AND COLLECTION NOTES\n%s\n", separator, separator)
	fmt.Fprintf(w, "Jobs with captured log parts:  %d\n", res.JobsWithParts)
	fmt.Fprintf(w, "Captured log files read:       %d\n", res.LogFiles)
	fmt.Fprintf(w, "API job conclusions fetched:   %d (with step lists: %d)\n", res.APIJobsFetched, res.APIJobsWithSteps)
	fmt.Fprintf(w, "API raw job logs fetched:      %d\n", res.APILogsFetched)
	fmt.Fprintf(w, "Failed command/error records:  %d\n", res.FailedCommandRecords)
	fmt.Fprintf(w, "Warning records:               %d\n", res.WarningRecords)
	if len(res.NeverStarted) == 0 {
		fmt.Fprintf(w, "Never-started (skipped) jobs:  (none)\n")
	} else {
		for _, name := range res.NeverStarted {
			fmt.Fprintf(w, "NEVER_STARTED: %s — the job did not run; absence of its logs is not a coverage failure.\n", name)
		}
	}
	if len(res.MissingSources) == 0 {
		fmt.Fprintf(w, "MISSING LOG SOURCE jobs:       (none)\n")
	} else {
		fmt.Fprintf(w, "MISSING LOG SOURCE jobs:       %d\n", len(res.MissingSources))
		for _, m := range res.MissingSources {
			fmt.Fprintf(w, "MISSING LOG SOURCE: %s\n", m)
		}
	}

	// ---- 9. COVERAGE + FINAL RESULT ------------------------------------
	fmt.Fprintf(w, "\n%s\n9. COVERAGE AND FINAL RESULT\n%s\n", separator, separator)
	if len(res.MissingSources) == 0 {
		fmt.Fprintf(w, "Evidence coverage: COMPLETE — every job that ran contributed its expected log part.\n")
		fmt.Fprintf(w, "Note: COMPLETE covers evidence coverage only; it does not mean the build succeeded.\n")
		fmt.Fprintf(w, "An artifact directory that exists but contains no log files counts as a missing source.\n")
	} else {
		fmt.Fprintf(w, "Evidence coverage: INCOMPLETE — log sources are missing (see section 8). This report was\n")
		fmt.Fprintf(w, "still written and must be uploaded before the collecting job fails. The run must never be\n")
		fmt.Fprintf(w, "reported green.\n")
	}
	if res.Overall == OverallPass {
		fmt.Fprintf(w, "\nOVERALL RESULT: PASS — every required job succeeded and evidence coverage is complete.\n")
	} else {
		fmt.Fprintf(w, "\nOVERALL RESULT: FAIL — truthful result derived from required job conclusions and evidence coverage:\n")
		for _, f := range failedJobs {
			fmt.Fprintf(w, "  - required job did not succeed: %s\n", f)
		}
		if len(res.MissingSources) > 0 {
			fmt.Fprintf(w, "  - evidence coverage incomplete (%d missing log source(s))\n", len(res.MissingSources))
		}
		fmt.Fprintf(w, "A successful collector run NEVER converts failed build/CodeQL jobs into an overall PASS.\n")
	}
	fmt.Fprintf(w, "\n%s\nEND OF CONSOLIDATED RUN REPORT\n%s\n", heavySeparator, heavySeparator)
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func orNote(s, note string) string {
	if strings.TrimSpace(s) == "" {
		return "(" + note + ")"
	}
	return s
}

func containsPrefix(list []string, name string) bool {
	for _, m := range list {
		if strings.HasPrefix(m, name+" ") || strings.HasPrefix(m, name+"(") {
			return true
		}
	}
	return false
}

// ParseManifest reads and unmarshals a run manifest from path.
func ParseManifest(path string) (RunMeta, error) {
	var meta RunMeta
	raw, err := os.ReadFile(path)
	if err != nil {
		return meta, fmt.Errorf("cilog: reading manifest %q: %w", path, err)
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return meta, fmt.Errorf("cilog: parsing manifest %q: %w", path, err)
	}
	return meta, nil
}
