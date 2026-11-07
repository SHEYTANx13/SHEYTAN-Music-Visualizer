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
	"strconv"
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

// Sentinel classification errors for the per-job raw-log fetch policy
// (run 42026322748 brief §1.I). These are NOT application failures:
// a skipped job has no raw job log at all, and an in-progress job's raw
// log is not final; neither may be reported as a failed fetch.
var (
	errSkippedNoLog = fmt.Errorf("NEVER_STARTED (conclusion: skipped) — the job has no raw job log; this 404 class is not a failure")
	errInProgress   = fmt.Errorf("IN_PROGRESS_AT_SNAPSHOT — the job had not reached a terminal state; its raw log is not final")
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
	// Kind states what this row IS in the workflow model, so manifest
	// evidence records are never presented as workflow jobs:
	//
	//      "workflow-job"                — a top-level job of the calling workflow;
	//      "nested-reusable-workflow-job" — a job inside a reusable workflow called
	//                                       by this run; the manifest carries the
	//                                       CALLER's aggregate result, per-language
	//                                       conclusions come from the job's own
	//                                       artifact/API observation.
	//      "standalone-collector-job"    — the evidence collection job itself.
	//
	// Empty means "workflow-job" (the historical shape).
	Kind string `json:"kind,omitempty"`
	// ResultBasis documents WHERE the declared result came from (e.g.
	// "needs.build-windows.result", "caller aggregate needs.codeql.result"),
	// so an aggregate value is never mistaken for a per-job observation.
	ResultBasis string `json:"result_basis,omitempty"`
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
	// FailedGateCount/SkippedGateCount/BlockedGateCount/PassedGateCount
	// come from the AUTHORITATIVE gate table (ParseGates): the summary
	// counts that must agree with the listed gates, and that must never
	// inherit a passing gate into the failed list (run 41915116613 class).
	// BLOCKED gates are missing-prerequisite gates, distinct from
	// intentionally SKIPPED gates (run 42026322748 brief §10.C).
	FailedGateCount  int
	SkippedGateCount int
	BlockedGateCount int
	PassedGateCount  int
	// Gates holds every distinct parsed gate in deterministic order.
	Gates            []GateRecord
	FailedJobs       []string // authoritative required-job failures
	FailedSteps      []string // authoritative failed steps "job/step"
	APIJobsFetched   int
	APIJobsWithSteps int
	APILogsFetched   int
	APIError         string
	AlertsFetched    int
	AlertsError      string
	ReportPath       string
	// RecoveredFromAPI names jobs whose artifact was missing but whose
	// complete raw job log was recovered through the API (brief §1.I:
	// both facts are reported separately, never conflated).
	RecoveredFromAPI []string
	// APIUnavailableLogs names jobs whose raw log could not be fetched
	// from the API, with the classified reason (API_LOG_UNAVAILABLE for a
	// completed job's missing/404 log; IN_PROGRESS_AT_SNAPSHOT for a job
	// that was still running when the snapshot was taken).
	APIUnavailableLogs []string
	// GatesNotReached names required jobs that contributed NO structured
	// gate records and did not succeed: their gates were never reached
	// (bootstrap/setup failure) — never reported as zero failures
	// (run 42026322748 evidence C/J: "0 failed / 0 skipped / 0 passed" is
	// NOT a successful preflight).
	GatesNotReached []string
}

type logFile struct {
	job      JobSpec
	artifact string
	rel      string // slash-separated relative path inside the artifact dir
	abs      string
}

type finding struct {
	// line is the matched text (identical copies from repeated log
	// sections are deduplicated into one entry).
	line string
	// wheres lists every provenance of the match:
	// "[job | artifact/file:line]" — provenance is never lost.
	wheres []string
}

// addFinding appends provenance to an existing finding with identical
// text or creates a new one. Order of first appearance is stable.
func addFinding(list []finding, line, where string) []finding {
	for i := range list {
		if list[i].line == line {
			list[i].wheres = appendUnique(list[i].wheres, where)
			return list
		}
	}
	return append(list, finding{line: line, wheres: []string{where}})
}

var (
	errorPatterns = []*regexp.Regexp{
		regexp.MustCompile(`^EXIT_CODE: [1-9][0-9]* FAIL\b`),
		regexp.MustCompile(`::error::`),
		regexp.MustCompile(`##\[error\]`),
		regexp.MustCompile(`^--- FAIL: `),
		regexp.MustCompile(`^FAIL[ \t]+\S`),
		regexp.MustCompile(`^ERROR:`),
	}
	warningPatterns = []*regexp.Regexp{
		regexp.MustCompile(`::warning::`),
		regexp.MustCompile(`##\[warning\]`),
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
	contributed := map[string]bool{} // declared job name -> >=1 captured log file
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
		contributed[j.Name] = true
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

	// Scan for error/warning findings with provenance. Identical lines
	// repeated by log copies are deduplicated into one finding with
	// every provenance preserved (never silently dropped).
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
			where := fmt.Sprintf("[%s | %s/%s:%d]", f.job.Name, f.artifact, f.rel, lineNo)
			for _, re := range errorPatterns {
				if re.MatchString(line) {
					errFindings = addFinding(errFindings, line, where)
					break
				}
			}
			for _, re := range warningPatterns {
				if re.MatchString(line) {
					warnFindings = addFinding(warnFindings, line, where)
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

	// AUTHORITATIVE gate status: parsed from the structured command
	// records (and explicit SKIPPED/BLOCKED markers) of every captured
	// log. This is the single source the summary counts must agree with:
	// a gate with an explicit `EXIT_CODE: 0 PASS` can never be listed as
	// failed, SKIPPED/BLOCKED are not command failures, and the
	// failed-gate count equals the number of distinct failed gates listed.
	gates, gerr := ParseGates(files)
	if gerr != nil {
		return res, fmt.Errorf("cilog: parsing gate records: %w", gerr)
	}
	res.Gates = gates
	gatesPerJob := map[string]int{}
	for _, g := range gates {
		gatesPerJob[g.Job]++
		switch g.Status {
		case GateFail:
			res.FailedGateCount++
		case GateSkipped:
			res.SkippedGateCount++
		case GateBlocked:
			res.BlockedGateCount++
		default:
			res.PassedGateCount++
		}
	}
	// GATES NOT REACHED (run 42026322748 evidence C): a required job that
	// did not succeed and captured no gate record at all did not pass —
	// its gates were never reached. Reported explicitly, never as zero
	// failed gates.
	for _, j := range meta.Jobs {
		if !j.Required || j.Result == ResultSuccess {
			continue
		}
		if gatesPerJob[j.Name] == 0 {
			res.GatesNotReached = append(res.GatesNotReached, j.Name)
		}
	}

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
		// logErrs records the classified fetch outcome per job id so the
		// report can distinguish API_LOG_UNAVAILABLE (a completed job whose
		// raw log is gone) from IN_PROGRESS_AT_SNAPSHOT (the job had not
		// finished; its raw log is not final) and from access limitations.
		logErrs map[int]error
	}
	var en enrich
	en.logs = map[int]string{}
	en.logErrs = map[int]error{}
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
				if !opts.FetchJobLogs {
					continue
				}
				// Classified fetch policy (run 42026322748 brief §1.I):
				// a skipped job has NO raw job log and an in-progress job's
				// raw log is not final — neither 404 may be reported as a
				// failed application command.
				if j.Conclusion == ResultSkipped {
					en.logErrs[j.ID] = errSkippedNoLog
					continue
				}
				if j.Conclusion == "" && (j.Status == "in_progress" || j.Status == "queued" || j.Status == "pending") {
					en.logErrs[j.ID] = errInProgress
					continue
				}
				logText, lerr := opts.API.FetchJobLog(ctx, j.ID)
				if lerr != nil {
					en.logErrs[j.ID] = lerr
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
		cancel()
	}

	// Authoritative failure derivation. A required job declared failed/
	// cancelled in the manifest is a failure even with zero text markers;
	// the same holds for API conclusions of matching jobs. Manifest and
	// API conclusions are deduplicated per stable job id (one entry,
	// both sources shown; aggregate rows proven successful are
	// reconciled, never double-counted).
	overallFailures, reconciled := deriveFailures(meta, en.jobs)
	res.FailedJobs = overallFailures
	res.FailedSteps = deriveFailedSteps(meta, en.jobs)

	// Per-job log-source classification (run 42026322748 brief §1.I):
	// an artifact that is missing and a raw log that was recovered are
	// two separate facts, and a skipped/in-progress job's missing log
	// is never misclassified as a completed-job failure.
	for _, j := range meta.Jobs {
		if j.Result == ResultSkipped {
			continue // NEVER_STARTED is recorded elsewhere
		}
		var apiObs *JobConclusion
		for idx := range en.jobs {
			if _, ok := matchDeclaredJob([]JobSpec{j}, en.jobs[idx].Name); ok {
				apiObs = &en.jobs[idx]
				break
			}
		}
		artifactOK := false
		if j.Artifact != "" {
			if info, err := os.Stat(filepath.Join(opts.PartsRoot, j.Artifact)); err == nil && info.IsDir() {
				artifactOK = true
			}
		}
		if artifactOK {
			continue
		}
		if apiObs != nil && en.logs[apiObs.ID] != "" {
			res.RecoveredFromAPI = append(res.RecoveredFromAPI,
				fmt.Sprintf("%s — artifact %q missing; complete raw job log RECOVERED_FROM_API and consolidated below", j.Name, j.Artifact))
			continue
		}
		if apiObs != nil && en.logErrs[apiObs.ID] == errSkippedNoLog {
			continue // classified as NEVER_STARTED, not unavailable
		}
		if apiObs != nil && en.logErrs[apiObs.ID] == errInProgress {
			res.APIUnavailableLogs = append(res.APIUnavailableLogs,
				fmt.Sprintf("%s — artifact %q missing; IN_PROGRESS_AT_SNAPSHOT (raw log not final at collection time)", j.Name, j.Artifact))
			continue
		}
		if apiObs != nil && en.logErrs[apiObs.ID] != nil {
			res.APIUnavailableLogs = append(res.APIUnavailableLogs,
				fmt.Sprintf("%s — artifact %q missing; API_LOG_UNAVAILABLE (%v)", j.Name, j.Artifact, en.logErrs[apiObs.ID]))
			continue
		}
		if apiObs == nil && j.Artifact != "" {
			res.APIUnavailableLogs = append(res.APIUnavailableLogs,
				fmt.Sprintf("%s — artifact %q missing; no API observation available to recover a raw log", j.Name, j.Artifact))
		}
	}

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
	writeReport(&b, opts, meta, res, files, en.jobs, en.logs, en.logErrs, reconciled, alerts, errFindings, warnFindings, overallFailures, red, now().UTC())

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

// deriveFailures returns (requiredJobsThatDidNotSucceed, reconciledJobs).
//
// Status authority and deduplication rule (run 42026322748 evidence J):
// the manifest carries the CALLER's needs.<job>.result per declared job;
// the API carries the per-job conclusion (and, for reusable-workflow
// matrix legs, the precise per-leg conclusion). For every declared job
// with a non-empty API conclusion, the API conclusion is authoritative;
// the manifest conclusion is retained as a second evidence source. ONE
// job appears AT MOST ONCE in the returned failure list, and when both
// sources observed the failure they are shown on that one entry — the
// same failure is never double-counted because two sources describe it.
// A manifest "failure" row that the API proves SUCCESSFUL (the expected
// case for the succeeded leg of a reusable-workflow matrix whose
// manifest row carries the aggregate caller result) is RECONCILED, not
// failed. Jobs without any API observation fall back to the manifest
// conclusion.
func deriveFailures(meta RunMeta, apiJobs []JobConclusion) (failed, reconciled []string) {
	apiByName := map[string]JobConclusion{}
	for _, aj := range apiJobs {
		if aj.Conclusion == "" {
			continue
		}
		if declared, ok := matchDeclaredJob(meta.Jobs, aj.Name); ok {
			if prev, seen := apiByName[declared.Name]; !seen || (prev.Conclusion == ResultSuccess && aj.Conclusion != ResultSuccess) {
				// Prefer the worst conclusion when several API rows map to
				// one declared job (e.g. matrix leg display variants).
				apiByName[declared.Name] = aj
			}
		}
	}

	for _, j := range meta.Jobs {
		if !j.Required {
			continue
		}
		manifestBad := ""
		switch j.Result {
		case ResultSuccess:
			// manifest says success; an authoritative API failure below
			// still counts (the manifest row may be an aggregate).
		case ResultFailure, ResultCancelled:
			manifestBad = j.Result
		case ResultSkipped:
			manifestBad = "skipped (NEVER_STARTED)"
		default:
			manifestBad = j.Result
		}
		aj, haveAPI := apiByName[j.Name]
		apiBad := ""
		if haveAPI && aj.Conclusion != "" &&
			aj.Conclusion != ResultSuccess && aj.Conclusion != ResultSkipped {
			apiBad = aj.Conclusion
		}
		switch {
		case haveAPI && aj.Conclusion == ResultSuccess:
			// API is authoritative: the job genuinely succeeded. A manifest
			// failure/skipped row for it (aggregate caller result, skipped
			// condition, or stale evidence) is reconciled, never counted.
			if manifestBad != "" {
				reconciled = append(reconciled, fmt.Sprintf("%s — API job conclusion: success; workflow conclusion %q was an aggregate/stale row and is reconciled to success (not a failure)", j.Name, j.Result))
			}
		case manifestBad != "" && apiBad != "":
			failed = append(failed, fmt.Sprintf("%s — workflow conclusion: %s; API job conclusion: %s", j.Name, manifestBad, apiBad))
		case manifestBad != "":
			failed = append(failed, fmt.Sprintf("%s — workflow conclusion: %s", j.Name, manifestBad))
		case apiBad != "":
			failed = append(failed, fmt.Sprintf("%s — API job conclusion: %s", j.Name, apiBad))
		}
	}
	return failed, reconciled
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
// Nested reusable-workflow rows use evidence-record names like
// "analyze (go)" while the API reports the real nested job name
// ("CodeQL Analyze (Go, manual traced build)"); an ordered whole-token
// comparison resolves those without conflating distinct languages
// ("analyze (go)" never matches the actions analysis job, and the
// reusable-workflow caller wrapper "codeql (reusable analysis)" never
// matches a declared analysis row).
func matchDeclaredJob(jobs []JobSpec, apiName string) (JobSpec, bool) {
	an := strings.ToLower(strings.TrimSpace(apiName))
	for _, j := range jobs {
		jn := strings.ToLower(strings.TrimSpace(j.Name))
		if an == jn || strings.HasPrefix(an, jn+" ") || strings.HasPrefix(an, jn+"(") {
			return j, true
		}
	}
	for _, j := range jobs {
		if tokenSubsequence(apiName, j.Name) {
			return j, true
		}
	}
	return JobSpec{}, false
}

// tokenSubsequence reports whether every whole alphanumeric token of
// want appears in have, in order (lower-cased, punctuation-boundary
// tokens). Requires want to have at least two tokens, so short generic
// names can never sweep unrelated jobs.
func tokenSubsequence(have, want string) bool {
	ht := tokenizeTokens(have)
	wt := tokenizeTokens(want)
	if len(wt) < 2 || len(wt) > len(ht) {
		return false
	}
	i := 0
	for _, w := range wt {
		found := false
		for ; i < len(ht); i++ {
			if ht[i] == w {
				found = true
				i++
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func tokenizeTokens(s string) []string {
	var out []string
	cur := strings.Builder{}
	for _, r := range strings.ToLower(s) {
		isAlnum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAlnum {
			cur.WriteRune(r)
		} else if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
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

func writeReport(w *strings.Builder, opts Options, meta RunMeta, res Result, files []logFile, apiJobs []JobConclusion, apiLogs map[int]string, logErrs map[int]error, reconciled []string, alerts []CodeScanningAlert, errs, warns []finding, failedJobs []string, red *Redactor, generated time.Time) {
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
	// Explicit job model (run 42231355353 reconciliation repair): the
	// manifest declares EVIDENCE RECORDS, which are not the same thing
	// as the workflow's top-level jobs or the jobs nested inside a
	// reusable workflow. Every row states its own kind and where its
	// declared result came from, so an aggregate value is never
	// presented as a per-job observation.
	fmt.Fprintf(w, "Manifest evidence records: %d (succeeded %d, failed %d, cancelled %d, skipped %d)\n",
		res.JobsExpected, jobsSucceeded, jobsFailed, jobsCancelled, jobsSkipped)
	fmt.Fprintf(w, "Row kinds: workflow-job = top-level job of the calling workflow; nested-reusable-workflow-job = a job\n")
	fmt.Fprintf(w, "inside a reusable workflow called by this run (the row carries the CALLER's aggregate conclusion; the\n")
	fmt.Fprintf(w, "per-language conclusion comes from that row's own artifact and/or API observation). The reusable-workflow\n")
	fmt.Fprintf(w, "caller wrapper and every other API-observed job are listed separately below the declared rows.\n\n")
	fmt.Fprintf(w, "%-28s %-30s %-9s %-22s %-14s %s\n", "JOB", "KIND", "REQUIRED", "CONCLUSION (workflow)", "API CONCLUSION", "LOG SOURCE")
	for _, j := range meta.Jobs {
		req := "yes"
		if !j.Required {
			req = "no"
		}
		var apiObs *JobConclusion
		for idx := range apiJobs {
			if _, ok := matchDeclaredJob([]JobSpec{j}, apiJobs[idx].Name); ok {
				apiObs = &apiJobs[idx]
				break
			}
		}
		// Classified LOG SOURCE (run 42026322748 brief §1.I). A skipped
		// job is NEVER_STARTED, not a missing artifact; a missing artifact
		// with a recovered raw log states both facts; a completed job's
		// unavailable log is API_LOG_UNAVAILABLE, never a fake pass.
		src := "-"
		switch {
		case j.Result == ResultSkipped:
			src = "NEVER_STARTED (skipped; no log expected)"
		case j.Artifact == "":
			src = "-"
		case containsPrefix(res.MissingSources, j.Name):
			src = j.Artifact + " ARTIFACT_MISSING"
			if apiObs != nil && apiLogs[apiObs.ID] != "" {
				src += "; RECOVERED_FROM_API"
			} else if apiObs != nil && logErrs[apiObs.ID] == errInProgress {
				src += "; IN_PROGRESS_AT_SNAPSHOT"
			} else if apiObs != nil && logErrs[apiObs.ID] != nil {
				src += "; API_LOG_UNAVAILABLE"
			} else if apiObs == nil {
				src += "; no API observation"
			}
		default:
			src = j.Artifact
		}
		apiConc := "unavailable"
		if j.Result == ResultSkipped {
			apiConc = "not run"
		}
		if apiObs != nil {
			apiConc = orNote(apiObs.Conclusion, "none (status "+apiObs.Status+")")
		}
		kind := j.Kind
		if kind == "" {
			kind = "workflow-job"
		}
		fmt.Fprintf(w, "%-28s %-30s %-9s %-22s %-14s %s\n", j.Name, kind, req, j.Result, apiConc, src)
		if j.ResultBasis != "" {
			fmt.Fprintf(w, "%-28s %-30s declared result basis: %s\n", "", "", j.ResultBasis)
		}
	}
	if len(apiJobs) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "API-observed workflow jobs NOT represented as manifest evidence records (reusable-workflow caller wrapper, collection job, ...):")
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

	// B. AUTHORITATIVE GATE / COMMAND STATUS. The single source for
	// gate status: each gate's own structured EXIT_CODE record. The
	// summary counts MUST agree with this table — this is the fix for
	// the run-41915116613 class, where a text-grep summary listed
	// passing gates (EXIT_CODE: 0 PASS) among failed gates.
	fmt.Fprintf(w, "\nB. GATE / COMMAND STATUS (authoritative — parsed from structured command records):\n")
	fmt.Fprintf(w, "   Rule: a gate's status comes ONLY from its own command record's EXIT_CODE line.\n")
	fmt.Fprintf(w, "   A gate with `EXIT_CODE: 0 PASS` is never listed as failed; `SKIPPED: gate ...` marks an\n")
	fmt.Fprintf(w, "   intentionally not-run gate, `BLOCKED: gate ...` a missing prerequisite; neither is a\n")
	fmt.Fprintf(w, "   command failure; the failed-gate count equals the number of distinct failed gates here.\n\n")
	if len(res.Gates) == 0 {
		fmt.Fprintf(w, "   (no structured command records found in the captured logs)\n")
	} else {
		fmt.Fprintf(w, "   %-22s %-26s %-8s %-5s %s\n", "JOB", "GATE/STEP", "STATUS", "EXIT", "PROVENANCE")
		for _, g := range res.Gates {
			exit := "-"
			if g.Status != GateSkipped && g.Status != GateBlocked {
				exit = strconv.Itoa(g.ExitCode)
			}
			detail := ""
			if g.Status == GateSkipped || g.Status == GateBlocked {
				detail = " (" + g.Reason + ")"
			}
			fmt.Fprintf(w, "   %-22s %-26s %-8s %-5s %s%s\n",
				g.Job, g.Step, g.Status, exit, strings.Join(g.Wheres, ", "), detail)
		}
	}
	failedNames := make([]string, 0, res.FailedGateCount)
	skippedNames := make([]string, 0, res.SkippedGateCount)
	blockedNames := make([]string, 0, res.BlockedGateCount)
	for _, g := range res.Gates {
		switch g.Status {
		case GateFail:
			failedNames = append(failedNames, g.Job+"/"+g.Step)
		case GateSkipped:
			skippedNames = append(skippedNames, g.Job+"/"+g.Step)
		case GateBlocked:
			blockedNames = append(blockedNames, g.Job+"/"+g.Step)
		}
	}
	fmt.Fprintf(w, "\n   Distinct failed gates: %d", res.FailedGateCount)
	if len(failedNames) > 0 {
		fmt.Fprintf(w, " (%s)", strings.Join(failedNames, ", "))
	}
	fmt.Fprintf(w, "\n   Distinct skipped gates: %d", res.SkippedGateCount)
	if len(skippedNames) > 0 {
		fmt.Fprintf(w, " (%s)", strings.Join(skippedNames, ", "))
	}
	fmt.Fprintf(w, "\n   Distinct blocked gates: %d", res.BlockedGateCount)
	if len(blockedNames) > 0 {
		fmt.Fprintf(w, " (%s)", strings.Join(blockedNames, ", "))
	}
	fmt.Fprintf(w, "\n   Distinct passed gates: %d\n", res.PassedGateCount)

	// GATES NOT REACHED (run 42026322748 evidence C/J): zero parsed gate
	// records for a required job that did not succeed is NOT a passing
	// preflight — the gates never executed. This must be distinguishable
	// from a run with zero failed gates.
	if len(res.GatesNotReached) > 0 {
		fmt.Fprintf(w, "\n   GATES NOT REACHED (required jobs that ended without ANY structured gate record —\n")
		fmt.Fprintf(w, "   this is NOT zero failures and never a pass):\n")
		for _, name := range res.GatesNotReached {
			fmt.Fprintf(w, "   - %s: the job did not succeed and captured no gate command record;\n", name)
			fmt.Fprintf(w, "     its gates were NOT EXECUTED / NOT REACHED (setup or bootstrap failure).\n")
		}
	}

	fmt.Fprintf(w, "\nC. TEXT-DERIVED ERROR RECORDS (supplemental; provenance included; copies deduplicated):\n")
	if len(errs) == 0 {
		fmt.Fprintf(w, "   (no failed command records, ::error:: lines, failed Go test summaries or ERROR: lines found)\n")
	} else {
		for _, f := range errs {
			fmt.Fprintf(w, "   %s %s\n", strings.Join(f.wheres, ", "), red.Apply(f.line))
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
			fmt.Fprintf(w, "%s %s\n", strings.Join(f.wheres, ", "), red.Apply(f.line))
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
				if declared, ok := matchDeclaredJob(meta.Jobs, aj.Name); ok &&
					(declared.Name == meta.CodeQL.Job || strings.HasPrefix(declared.Name, orDash(meta.CodeQL.Job)+" (")) {
					// Prefer the worst observed conclusion across the
					// matched matrix legs (a succeeded leg must not
					// mask a failed one).
					if observed == "" || (observed == ResultSuccess && aj.Conclusion != "" && aj.Conclusion != ResultSuccess) {
						observed = aj.Conclusion
					}
				}
			}
		}
		// Classified API-observation note (run 42231355353 repair): the
		// note must never point at an "API ENRICHMENT LIMITATION" section
		// that was not actually emitted. The three unavailable reasons are
		// distinct: an API fetch error (the section above exists), no API
		// client configured, and an API response that matched no declared
		// record.
		note := ""
		switch {
		case observed != "":
			// an authoritative observation exists; no note needed
		case res.APIError != "":
			note = "unavailable (see API ENRICHMENT LIMITATION above)"
		case len(apiJobs) == 0:
			note = "unavailable (API enrichment not configured — no API client/token for this collection)"
		default:
			note = "unavailable (no API-observed job matched this declared record; see the OBSERVED rows above)"
		}
		fmt.Fprintf(w, "API-observed conclusion: %s\n", orNote(observed, note))
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
	fmt.Fprintf(w, "Failed command/error records:  %d (deduplicated; supplemental — see 5.C)\n", res.FailedCommandRecords)
	fmt.Fprintf(w, "Warning records:               %d\n", res.WarningRecords)
	fmt.Fprintf(w, "Distinct gates parsed:         %d (failed %d, skipped %d, blocked %d, passed %d — see 5.B)\n",
		len(res.Gates), res.FailedGateCount, res.SkippedGateCount, res.BlockedGateCount, res.PassedGateCount)
	if len(res.NeverStarted) == 0 {
		fmt.Fprintf(w, "Never-started (skipped) jobs:  (none)\n")
	} else {
		for _, name := range res.NeverStarted {
			fmt.Fprintf(w, "NEVER_STARTED: %s — the job did not run (workflow conclusion: skipped); absence of its logs is not a coverage failure and its API raw-log 404 class is NOT a failure.\n", name)
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
	if len(res.RecoveredFromAPI) == 0 {
		fmt.Fprintf(w, "Raw-log API recovery:          (none needed)\n")
	} else {
		for _, r := range res.RecoveredFromAPI {
			fmt.Fprintf(w, "RECOVERED_FROM_API: %s\n", r)
		}
	}
	if len(res.APIUnavailableLogs) == 0 {
		fmt.Fprintf(w, "API-unavailable raw logs:      (none)\n")
	} else {
		for _, u := range res.APIUnavailableLogs {
			fmt.Fprintf(w, "API LOG UNAVAILABLE: %s\n", u)
		}
	}
	if len(reconciled) == 0 {
		fmt.Fprintf(w, "Reconciled job rows:           0 — no manifest evidence record was an aggregate/stale row that the API proved successful.\n")
	} else {
		fmt.Fprintf(w, "Reconciled job rows:           %d (aggregate/stale manifest rows proven successful by the API; never counted as failures)\n", len(reconciled))
		for _, r := range reconciled {
			fmt.Fprintf(w, "RECONCILED: %s\n", r)
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
