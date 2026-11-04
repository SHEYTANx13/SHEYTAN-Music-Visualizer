// Package cilog implements the consolidated CI run-log collector for
// SHEYTAN Music Visualizer.
//
// It reads a small JSON run manifest and a parts root containing
// per-job log artifacts in namespaced directories (one directory per
// artifact, never flattened), and writes one deterministic plain-text
// consolidated report that contains:
//
//   - run metadata and summary;
//   - an ERROR INDEX (failed command records, ::error:: lines, failed
//     Go test summaries, ERROR: lines) with file/line provenance;
//   - a WARNING INDEX (::warning:: and WARNING: lines);
//   - a NEVER_STARTED section for skipped jobs;
//   - a MISSING LOG SOURCE section for jobs that ran (or were
//     cancelled/failed) but contributed no expected log part;
//   - the complete, verbatim, untruncated per-job logs in deterministic
//     job order and lexicographically sorted file order;
//   - a final coverage statement (COMPLETE or INCOMPLETE).
//
// Coverage is about evidence, not build success: a run with failed
// commands but complete log parts is COMPLETE; a run with a missing log
// part is INCOMPLETE and must fail the collecting job after the report
// has been written and uploaded.
//
// Standard library only: this collector must stay runnable even when
// the application dependency graph is broken.
package cilog

import (
	"bufio"
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

// JobSpec declares one expected log-producing job.
type JobSpec struct {
	Name string `json:"name"`
	// Result is one of: success, failure, cancelled, skipped.
	Result string `json:"result"`
	// Artifact is the expected artifact directory name under the parts
	// root. Empty means the job is expected to contribute no log part.
	Artifact string `json:"artifact,omitempty"`
}

// RunMeta is the run manifest: header metadata plus expected jobs.
type RunMeta struct {
	Product      string    `json:"product"`
	Workflow     string    `json:"workflow"`
	RunID        string    `json:"run_id"`
	Attempt      string    `json:"attempt"`
	CommitSHA    string    `json:"commit_sha"`
	Ref          string    `json:"ref"`
	Event        string    `json:"event"`
	StartTimeUTC string    `json:"start_time_utc"`
	RunnerOS     string    `json:"runner_os,omitempty"`
	Jobs         []JobSpec `json:"jobs"`
}

// Options configures Collect.
type Options struct {
	Meta       RunMeta
	PartsRoot  string
	ReportPath string
	// Now overrides the clock for the report-generation stamp
	// (injected by tests for deterministic output). nil = time.Now.
	Now func() time.Time
}

// Result summarizes one Collect run.
type Result struct {
	Coverage             string
	MissingSources       []string
	NeverStarted         []string
	JobsExpected         int
	JobsWithParts        int
	LogFiles             int
	FailedCommandRecords int
	WarningRecords       int
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

// Collect validates the manifest, scans the parts root, writes the
// consolidated report to opts.ReportPath, and returns the result.
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
		if files[i].rel != files[k].rel {
			return false
		}
		return jobIndex(meta.Jobs, files[i].job.Name) < jobIndex(meta.Jobs, files[k].job.Name)
	})
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

	now := opts.Now
	if now == nil {
		now = time.Now
	}

	var b strings.Builder
	writeReport(&b, opts, meta, res, files, errFindings, warnFindings, now().UTC())

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

func writeReport(w *strings.Builder, opts Options, meta RunMeta, res Result, files []logFile, errs, warns []finding, generated time.Time) {
	fmt.Fprintf(w, "%s\nSHEYTAN CONSOLIDATED RUN REPORT\n%s\n", heavySeparator, heavySeparator)
	fmt.Fprintf(w, "Product:          %s\n", meta.Product)
	fmt.Fprintf(w, "Workflow:         %s\n", meta.Workflow)
	fmt.Fprintf(w, "Run ID:           %s\n", meta.RunID)
	fmt.Fprintf(w, "Attempt:          %s\n", meta.Attempt)
	fmt.Fprintf(w, "Commit SHA:       %s\n", meta.CommitSHA)
	fmt.Fprintf(w, "Ref:              %s\n", meta.Ref)
	fmt.Fprintf(w, "Event:            %s\n", meta.Event)
	fmt.Fprintf(w, "Start time (UTC): %s\n", meta.StartTimeUTC)
	fmt.Fprintf(w, "Generated (UTC):  %s\n", generated.Format("2006-01-02T15:04:05Z"))
	fmt.Fprintf(w, "Runner OS:        %s\n", meta.RunnerOS)

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

	fmt.Fprintf(w, "\n%s\nRUN SUMMARY\n%s\n", separator, separator)
	fmt.Fprintf(w, "Expected jobs:                 %d\n", res.JobsExpected)
	fmt.Fprintf(w, "Jobs with captured log parts:  %d\n", res.JobsWithParts)
	fmt.Fprintf(w, "Jobs succeeded:                %d\n", jobsSucceeded)
	fmt.Fprintf(w, "Jobs failed:                   %d\n", jobsFailed)
	fmt.Fprintf(w, "Jobs cancelled:                %d\n", jobsCancelled)
	fmt.Fprintf(w, "Jobs skipped:                  %d\n", jobsSkipped)
	fmt.Fprintf(w, "MISSING LOG SOURCE jobs:       %d\n", len(res.MissingSources))
	fmt.Fprintf(w, "Failed command/error records:  %d\n", res.FailedCommandRecords)
	fmt.Fprintf(w, "Warning records:               %d\n", res.WarningRecords)
	fmt.Fprintf(w, "Log files consolidated:        %d\n", res.LogFiles)

	fmt.Fprintf(w, "\n%s\nERROR INDEX\n%s\n", separator, separator)
	if len(errs) == 0 {
		fmt.Fprintf(w, "(no failed command records, ::error:: lines, failed Go test summaries or ERROR: lines found)\n")
	} else {
		for _, f := range errs {
			fmt.Fprintf(w, "%s %s\n", f.where, f.line)
		}
	}

	fmt.Fprintf(w, "\n%s\nWARNING INDEX\n%s\n", separator, separator)
	if len(warns) == 0 {
		fmt.Fprintf(w, "(no ::warning:: or WARNING: lines found)\n")
	} else {
		for _, f := range warns {
			fmt.Fprintf(w, "%s %s\n", f.where, f.line)
		}
	}

	fmt.Fprintf(w, "\n%s\nNEVER_STARTED (SKIPPED) JOBS\n%s\n", separator, separator)
	if len(res.NeverStarted) == 0 {
		fmt.Fprintf(w, "(none)\n")
	} else {
		for _, name := range res.NeverStarted {
			fmt.Fprintf(w, "NEVER_STARTED: %s — the job did not run; absence of its logs is not a coverage failure.\n", name)
		}
	}

	fmt.Fprintf(w, "\n%s\nMISSING LOG SOURCE\n%s\n", separator, separator)
	if len(res.MissingSources) == 0 {
		fmt.Fprintf(w, "(none)\n")
	} else {
		for _, m := range res.MissingSources {
			fmt.Fprintf(w, "MISSING LOG SOURCE: %s\n", m)
		}
	}

	fmt.Fprintf(w, "\n%s\nCONSOLIDATED LOGS (verbatim, untruncated)\n%s\n", separator, separator)
	if len(files) == 0 {
		fmt.Fprintf(w, "(no log files were consolidated)\n")
	}
	for _, f := range files {
		fmt.Fprintf(w, "\n#### JOB: %s | ARTIFACT: %s | FILE: %s\n#### LOG CONTENT BEGIN\n", f.job.Name, f.artifact, f.rel)
		fh, err := os.Open(f.abs)
		if err != nil {
			fmt.Fprintf(w, "#### COLLECTOR ERROR: cannot open file: %v\n", err)
			continue
		}
		_, cerr := io.Copy(w, fh)
		closeErr := fh.Close()
		if cerr != nil {
			fmt.Fprintf(w, "\n#### COLLECTOR ERROR: cannot read file: %v\n", cerr)
		}
		if closeErr != nil {
			fmt.Fprintf(w, "\n#### COLLECTOR ERROR: cannot close file: %v\n", closeErr)
		}
		if !strings.HasSuffix(w.String(), "\n") {
			w.WriteString("\n")
		}
		fmt.Fprintf(w, "#### LOG CONTENT END\n")
	}

	fmt.Fprintf(w, "\n%s\nCOVERAGE STATEMENT\n%s\n", separator, separator)
	if len(res.MissingSources) == 0 {
		fmt.Fprint(w, "COMPLETE — every job that ran contributed its expected log part.\n")
		fmt.Fprint(w, "Note: COMPLETE covers evidence coverage only; it does not mean the build succeeded.\n")
		fmt.Fprint(w, "An artifact directory that exists but contains no log files counts as a missing source.\n")
	} else {
		fmt.Fprint(w, "INCOMPLETE — log sources are missing. This report was still written and must be\n")
		fmt.Fprint(w, "uploaded before the collecting job fails. The run must never be reported green.\n")
	}
	fmt.Fprintf(w, "%s\nEND OF CONSOLIDATED RUN REPORT\n%s\n", heavySeparator, heavySeparator)
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
