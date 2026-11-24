package cilog

// report.go — the FINAL consolidated diagnostics artifact writer and
// the GitHub Actions job-summary writer.
//
// Contract (warning/error-only final artifact):
//
//   - the final artifact contains ONLY `WARNING|`/`ERROR|` records in
//     the stable schema of records.go; no raw logs, no command
//     chatter, no success messages, no metadata sections;
//   - run metadata, job/step status tables and success summaries go to
//     the GitHub Actions job summary (writeJobSummary), never into the
//     warning/error file;
//   - a failed required job produces an ERROR record even when no
//     textual error marker exists (authoritative conclusions);
//   - a missing log source is an ERROR record, never a clean report;
//   - clean runs produce an EMPTY artifact file;
//   - every record is secret-redacted before it is written.

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// recordsInput carries everything needed to assemble the record set.
type recordsInput struct {
	Meta           RunMeta
	Res            Result
	Gates          []GateRecord
	Errs           []finding
	Warns          []finding
	FailedJobs     []string
	FailedSteps    []string
	Reconciled     []string
	Recovered      []string
	APIUnavailable []string
	NeverStarted   []string
	Red            *Redactor
}

// buildRecords assembles the deduplicated record set from every
// evidence source. Ordering is deterministic (severity, platform, job,
// step, gate, tool, file, line, message).
func buildRecords(in recordsInput) []Record {
	var recs []Record
	red := in.Red
	redact := func(s string) string {
		if red != nil {
			return red.Apply(s)
		}
		return RedactSecrets(s)
	}

	// 1. Textual diagnostics found in captured logs (with provenance).
	for i := range in.Errs {
		recs = appendRecordFromFinding(recs, in.Errs[i], SevError, redact)
	}
	for i := range in.Warns {
		recs = appendRecordFromFinding(recs, in.Warns[i], SevWarning, redact)
	}

	// 2. Authoritative gate statuses.
	for _, g := range in.Gates {
		switch g.Status {
		case GateFail:
			msg := fmt.Sprintf("gate %s failed with exit code %d", g.Step, g.ExitCode)
			if g.RootCause != "" {
				msg += " (root cause: " + g.RootCause + ")"
			}
			recs = append(recs, Record{
				Severity: SevError,
				Platform: platformForJob(g.Job),
				Job:      g.Job,
				Step:     g.Step,
				Gate:     g.Step,
				Command:  g.Step,
				ExitCode: g.ExitCode,
				Tool:     "gate-runner",
				Source:   SourceNotProvided,
				Message:  msg,
			})
		case GateBlocked:
			recs = append(recs, Record{
				Severity: SevError,
				Platform: platformForJob(g.Job),
				Job:      g.Job,
				Step:     g.Step,
				Gate:     g.Step,
				Tool:     "gate-runner",
				ExitCode: -1,
				Source:   SourceNotProvided,
				Message:  fmt.Sprintf("required gate BLOCKED, not passed: %s (%s)", g.Step, g.Reason),
			})
		case GateSkipped:
			sev := SevWarning
			if jobRequired(in.Meta, g.Job) {
				// A skipped required gate is not a pass (final-verdict rule).
				sev = SevError
			}
			recs = append(recs, Record{
				Severity: sev,
				Platform: platformForJob(g.Job),
				Job:      g.Job,
				Step:     g.Step,
				Gate:     g.Step,
				Tool:     "gate-runner",
				ExitCode: -1,
				Source:   SourceNotProvided,
				Message:  fmt.Sprintf("gate SKIPPED: %s (%s)", g.Step, g.Reason),
			})
		}
	}

	// 2b. Failed informational (non-required) jobs are WARNINGs: their
	// failure is honest evidence but must not poison the build verdict.
	for _, j := range in.Meta.Jobs {
		if j.Required || j.Result == ResultSuccess || j.Result == ResultSkipped {
			continue
		}
		recs = append(recs, Record{
			Severity: SevWarning,
			Platform: platformForJob(j.Name),
			Job:      j.Name,
			Step:     "job",
			Gate:     defaultGateForJob(j.Name),
			Command:  "github-job",
			ExitCode: -1,
			Tool:     "github-actions",
			Source:   SourceNotProvided,
			Message:  fmt.Sprintf("informational job did not succeed: workflow conclusion %s", j.Result),
		})
	}

	// 3. Failed required jobs — one ERROR record each, even without any
	// textual error marker (authoritative workflow conclusions).
	for _, f := range in.FailedJobs {
		name, conclusion := splitJobFailure(f)
		recs = append(recs, Record{
			Severity: SevError,
			Platform: platformForJob(name),
			Job:      name,
			Step:     "job",
			Gate:     defaultGateForJob(name),
			Command:  "github-job",
			ExitCode: -1,
			Tool:     "github-actions",
			Source:   SourceNotProvided,
			Message:  fmt.Sprintf("required job did not succeed: workflow conclusion %s", conclusion),
		})
	}

	// 4. Failed steps (authoritative API step conclusions).
	for _, s := range in.FailedSteps {
		job, step, conclusion := splitStepFailure(s)
		recs = append(recs, Record{
			Severity: SevError,
			Platform: platformForJob(job),
			Job:      job,
			Step:     step,
			Gate:     defaultGateForJob(job),
			Command:  "github-step",
			ExitCode: -1,
			Tool:     "github-actions",
			Source:   SourceNotProvided,
			Message:  "step conclusion " + conclusion,
		})
	}

	// 5. Missing log sources — never evidence of a clean run.
	for _, m := range in.Res.MissingSources {
		recs = append(recs, Record{
			Severity: SevError,
			Platform: platformForJob(m),
			Job:      missingSourceJob(m),
			Step:     "log-source",
			Gate:     "diagnostics",
			Command:  "ci-log-collector",
			ExitCode: -1,
			Tool:     "ci-log-collector",
			Source:   SourceNotProvided,
			Message:  "missing required log source: " + m,
		})
	}

	// 6. Gates never reached on failed required jobs.
	for _, g := range in.Res.GatesNotReached {
		recs = append(recs, Record{
			Severity: SevError,
			Platform: platformForJob(g),
			Job:      g,
			Step:     "gates",
			Gate:     defaultGateForJob(g),
			Tool:     "ci-log-collector",
			ExitCode: -1,
			Source:   SourceNotProvided,
			Message:  "required job failed before reaching any gate; gate results are unknown, not passed",
		})
	}

	// 6b. API-recovered logs: the artifact was missing but diagnostics
	// were recovered from the authoritative job log. Both facts are
	// reported (never silently merged).
	for _, r := range in.Recovered {
		job := r
		if i := strings.Index(r, " - artifact"); i >= 0 {
			job = r[:i]
		}
		recs = append(recs, Record{
			Severity: SevWarning,
			Platform: platformForJob(job),
			Job:      job,
			Step:     "log-source",
			Gate:     "diagnostics",
			Command:  "ci-log-collector",
			ExitCode: -1,
			Tool:     "ci-log-collector",
			Source:   SourceNotProvided,
			Message:  "artifact missing; diagnostics recovered from the API job log",
		})
	}

	// 7. Collection limitations (warnings, not failures). The entry
	// text is "<job> - artifact ... <reason>": the job name is the
	// prefix before " - " (never the whole string in the job field).
	for _, u := range in.APIUnavailable {
		job := u
		if i := strings.Index(u, " - artifact"); i >= 0 {
			job = u[:i]
		} else if i := strings.Index(u, " ("); i >= 0 {
			job = u[:i]
		}
		recs = append(recs, Record{
			Severity: SevWarning,
			Platform: platformForJob(job),
			Job:      job,
			Step:     "log-source",
			Gate:     "diagnostics",
			Command:  "ci-log-collector",
			ExitCode: -1,
			Tool:     "ci-log-collector",
			Source:   SourceNotProvided,
			Message:  u,
		})
	}
	for _, n := range in.NeverStarted {
		recs = append(recs, Record{
			Severity: SevWarning,
			Platform: platformForJob(n),
			Job:      n,
			Step:     "job",
			Gate:     defaultGateForJob(n),
			Command:  "github-job",
			ExitCode: -1,
			Tool:     "github-actions",
			Source:   SourceNotProvided,
			Message:  "job never started (declared skipped in the manifest)",
		})
	}

	// Redact every record's message and free-text fields.
	for i := range recs {
		recs[i].Message = redact(recs[i].Message)
		recs[i].File = redact(recs[i].File)
	}

	recs = DedupeRecords(recs)
	SortRecords(recs)
	return recs
}

// appendRecordFromFinding parses one raw finding line into a record
// and attaches job provenance from the finding's wheres.
func appendRecordFromFinding(recs []Record, f finding, sev Severity, redact func(string) string) []Record {
	// The EXIT_CODE gate records are handled authoritatively by the
	// gate parser; they must not surface twice as raw findings.
	if strings.HasPrefix(f.line, "EXIT_CODE:") {
		return recs
	}
	r, ok := extractDiag(f.line)
	if !ok {
		// Unparsed marker lines still carry information; keep them as
		// generic records with the raw (redacted, single-line) text.
		r = Record{
			Severity: sev,
			ExitCode: -1, // a raw marker line carries no process exit code
			Tool:     "log-marker",
			Source:   SourceNotProvided,
			Message:  strings.TrimPrefix(f.line, "ERROR: "),
		}
	}
	if r.Severity == "" {
		r.Severity = sev
	}
	job := ""
	if len(f.wheres) > 0 {
		job = jobFromWhere(f.wheres[0])
	}
	r.Platform = platformForJob(job)
	r.Job = job
	r.Message = redact(r.Message)
	return append(recs, r)
}

// jobFromWhere parses "[job | artifact/file:line]" provenance.
func jobFromWhere(where string) string {
	where = strings.TrimPrefix(where, "[")
	if i := strings.Index(where, " | "); i >= 0 {
		return where[:i]
	}
	return ""
}

// splitJobFailure parses "name - workflow conclusion: <c>" entries.
func splitJobFailure(s string) (name, conclusion string) {
	if i := strings.Index(s, " - "); i >= 0 {
		name = s[:i]
		rest := s[i+3:]
		conclusion = strings.TrimPrefix(rest, "workflow conclusion: ")
		conclusion = strings.TrimPrefix(conclusion, "API job conclusion: ")
		return name, conclusion
	}
	return s, "unavailable"
}

// splitStepFailure parses "job / step N \"name\": conclusion" entries.
func splitStepFailure(s string) (job, step, conclusion string) {
	if i := strings.Index(s, " / step "); i >= 0 {
		job = s[:i]
		rest := s[i+len(" / step "):]
		// rest = `N "name": conclusion`
		if q := strings.Index(rest, `"`); q >= 0 {
			if e := strings.LastIndex(rest, `": `); e > q {
				step = rest[q+1 : e]
				conclusion = rest[e+3:]
				return job, step, conclusion
			}
		}
		conclusion = rest
		return job, "", conclusion
	}
	return "", "", s
}

// missingSourceJob parses the job name from a MissingSources entry.
func missingSourceJob(s string) string {
	if i := strings.Index(s, " ("); i >= 0 {
		return s[:i]
	}
	return s
}

func jobRequired(meta RunMeta, name string) bool {
	for _, j := range meta.Jobs {
		if j.Name == name {
			return j.Required
		}
	}
	return false
}

// defaultGateForJob maps a job to its top-level workflow gate.
func defaultGateForJob(job string) string {
	l := strings.ToLower(job)
	switch {
	case strings.Contains(l, "preflight"):
		return "preflight"
	case strings.Contains(l, "windows"):
		return "desktop-build"
	case strings.Contains(l, "linux"):
		return "desktop-build"
	case strings.Contains(l, "macos"):
		return "desktop-build"
	case strings.Contains(l, "android"):
		return "android-package"
	case strings.Contains(l, "publish"):
		return "publication"
	case strings.Contains(l, "collect") || strings.Contains(l, "log"):
		return "diagnostics"
	default:
		return "build"
	}
}

// ---------------------------------------------------------------------------
// Final artifact writer
// ---------------------------------------------------------------------------

// writeFinalReport renders, byte-validates and writes the warning/
// error-only artifact. Returns the written bytes. Fails CLOSED on any
// contract violation or size overflow: a non-compliant report is never
// written silently.
func writeFinalReport(path string, records []Record, red *Redactor) ([]byte, error) {
	errRec, overCap, size := CapRecords(records)
	if overCap {
		return nil, fmt.Errorf("cilog: %s", errRec.Message)
	}
	body := RenderRecords(records)
	if red != nil {
		body = []byte(red.Apply(string(body)))
	}
	body = SanitizeStrictBytes(body)
	if rep := ValidateStrictBytes(body); rep.Violations() > 0 {
		return nil, fmt.Errorf("cilog: final report violates the strict byte contract after sanitization: %+v", rep)
	}
	if int64(size) > maxReportBytes || int64(len(body)) > maxReportBytes {
		return nil, fmt.Errorf("cilog: final report exceeds the %d byte ceiling (%d bytes); records were not truncated", maxReportBytes, len(body))
	}
	if err := writeArtifactFile(path, body); err != nil {
		return nil, fmt.Errorf("cilog: writing report %q: %w", path, err)
	}
	return body, nil
}

// ---------------------------------------------------------------------------
// Job summary writer (metadata lives here, NOT in the final artifact)
// ---------------------------------------------------------------------------

// summaryInput carries the evidence for the human-readable job summary.
type summaryInput struct {
	Meta       RunMeta
	Res        Result
	Records    []Record
	ReportPath string
	ReportSize int
	Alerts     []CodeScanningAlert
	Generated  time.Time
}

// writeJobSummary renders the run summary for $GITHUB_STEP_SUMMARY:
// metadata, job status, gate table, failure index, record counts and
// CodeQL evidence. It NEVER contains raw logs or raw command output.
func writeJobSummary(path string, in summaryInput) error {
	var b strings.Builder
	w := func(format string, args ...interface{}) {
		fmt.Fprintf(&b, format+"\n", args...)
	}
	w("SHEYTAN Music Visualizer - consolidated run summary")
	w("")
	w("RUN METADATA")
	w("Generated (UTC): %s", in.Generated.Format(time.RFC3339))
	w("Product: %s", orDash(in.Meta.Product))
	w("Repository: %s", orDash(in.Meta.Repository))
	w("Workflow: %s", orDash(in.Meta.Workflow))
	w("Run/attempt: %s / %s", orDash(in.Meta.RunID), orDash(in.Meta.Attempt))
	w("Commit: %s", orDash(in.Meta.CommitSHA))
	w("Ref/event: %s / %s", orDash(in.Meta.Ref), orDash(in.Meta.Event))
	w("")
	w("FINAL DIAGNOSTIC ARTIFACT")
	w("  File: %s", orDash(in.ReportPath))
	w("  Size (bytes): %d", in.ReportSize)
	w("  Error records: %d", in.Res.ErrorRecordCount)
	w("  Warning records: %d", in.Res.WarningRecordCount)
	w("  Empty file means the run produced no warnings or errors.")
	w("  The record set is an index of actionable diagnostics; it does not certify a warning-free run beyond captured evidence.")
	w("")
	w("JOB STATUS (manifest evidence records; required jobs must succeed)")
	w("  Manifest evidence records: %d", len(in.Meta.Jobs))
	for _, j := range in.Meta.Jobs {
		kind := j.Kind
		if kind == "" {
			kind = "workflow-job"
		}
		req := "required"
		if !j.Required {
			req = "informational"
		}
		art := orDash(j.Artifact)
		w("  %-16s result=%-9s %-28s kind=%s artifact=%s", j.Name, j.Result, req, kind, art)
		if j.ResultBasis != "" {
			w("    declared result basis: %s", j.ResultBasis)
		}
	}
	if len(in.Res.NeverStarted) > 0 {
		w("  Never started: %s", strings.Join(in.Res.NeverStarted, ", "))
	}
	w("")
	w("GATE STATUS (authoritative command records)")
	for _, g := range in.Res.Gates {
		line := fmt.Sprintf("  %-16s %-28s %s", g.Job, g.Step, g.Status)
		if g.ExitCode != 0 {
			line += fmt.Sprintf(" (exit %d)", g.ExitCode)
		}
		if g.RootCause != "" {
			line += " root-cause: " + g.RootCause
		}
		w("%s", line)
	}
	w("  Gates passed=%d failed=%d skipped=%d blocked=%d",
		in.Res.PassedGateCount, in.Res.FailedGateCount, in.Res.SkippedGateCount, in.Res.BlockedGateCount)
	if len(in.Res.GatesNotReached) > 0 {
		w("  Gates NOT reached (failed before running): %s", strings.Join(in.Res.GatesNotReached, ", "))
	}
	w("")
	w("FAILED JOBS")
	if len(in.Res.FailedJobs) == 0 {
		w("  (none)")
	} else {
		for _, f := range in.Res.FailedJobs {
			w("  %s", f)
		}
	}
	w("")
	w("FAILED STEPS")
	if len(in.Res.FailedSteps) == 0 {
		w("  (none)")
	} else {
		for _, s := range in.Res.FailedSteps {
			w("  %s", s)
		}
	}
	w("")
	w("COVERAGE")
	w("  Jobs expected/with parts: %d / %d", in.Res.JobsExpected, in.Res.JobsWithParts)
	w("  Log files scanned: %d", in.Res.LogFiles)
	w("  Missing sources: %d", len(in.Res.MissingSources))
	for _, m := range in.Res.MissingSources {
		w("    %s", m)
	}
	if len(in.Res.RecoveredFromAPI) > 0 {
		w("  Recovered via API: %s", strings.Join(in.Res.RecoveredFromAPI, "; "))
	}
	w("  API jobs fetched / steps available / logs fetched: %d / %d / %d",
		in.Res.APIJobsFetched, in.Res.APIJobsWithSteps, in.Res.APILogsFetched)
	if in.Res.APIJobsFetched == 0 && in.Res.APIError == "" {
		w("  API enrichment not configured (no API client)")
	}
	if in.Res.APIError != "" {
		w("  API ENRICHMENT LIMITATION: %s", in.Res.APIError)
	}
	w("")
	w("RECONCILED ROWS")
	if len(in.Res.ReconciledRows) == 0 {
		w("  (none)")
	} else {
		for _, r := range in.Res.ReconciledRows {
			w("  %s", r)
		}
	}
	w("")
	w("CODEQL (independent workflow; same-commit open alerts)")
	if in.Res.AlertsError != "" {
		w("  Open alerts: NOT RETRIEVED (%s)", in.Res.AlertsError)
	} else {
		w("  Open alerts for analyzed commit: %d", in.Res.AlertsFetched)
		for _, a := range in.Alerts {
			w("  alert #%d [%s] %s - %s:%d", a.Number, orDash(a.Severity), orDash(a.RuleID), orDash(a.FilePath), a.StartLine)
		}
	}
	w("")
	w("COVERAGE AND FINAL RESULT")
	w("  Coverage: %s", in.Res.Coverage)
	w("  Overall build verdict (separate from collection success): %s", in.Res.Overall)

	out := SanitizeBytes([]byte(b.String()))
	if rep := ValidateBytes(out); rep.Violations() > 0 {
		return fmt.Errorf("cilog: job summary violates the ASCII contract: %+v", rep)
	}
	return writeArtifactFile(path, out)
}

// ---------------------------------------------------------------------------
// collect.go surgery helpers (kept here to keep Collect readable)
// ---------------------------------------------------------------------------

// finalizeReport builds records, writes the final warning/error-only
// artifact and the job summary, and stores the record counts in res.
func finalizeReport(opts Options, meta RunMeta, res Result, gates []GateRecord,
	errs, warns []finding, failedJobs, failedSteps, reconciled []string,
	apiUnavailable []string, neverStarted []string, alerts []CodeScanningAlert,
	red *Redactor, now time.Time) (Result, error) {

	res.ReconciledRows = reconciled
	records := buildRecords(recordsInput{
		Meta:           meta,
		Res:            res,
		Gates:          gates,
		Errs:           errs,
		Warns:          warns,
		FailedJobs:     failedJobs,
		FailedSteps:    failedSteps,
		Reconciled:     reconciled,
		Recovered:      res.RecoveredFromAPI,
		APIUnavailable: apiUnavailable,
		NeverStarted:   neverStarted,
		Red:            red,
	})

	body, err := writeFinalReport(opts.ReportPath, records, red)
	if err != nil {
		return res, err
	}
	res.ReportPath = opts.ReportPath
	res.ReportBytes = int64(len(body))
	res.ErrorRecordCount = 0
	res.WarningRecordCount = 0
	for _, r := range records {
		if r.Severity == SevError {
			res.ErrorRecordCount++
		} else {
			res.WarningRecordCount++
		}
	}

	if opts.SummaryPath != "" {
		serr := writeJobSummary(opts.SummaryPath, summaryInput{
			Meta:       meta,
			Res:        res,
			Records:    records,
			ReportPath: opts.ReportPath,
			ReportSize: len(body),
			Alerts:     alerts,
			Generated:  now.UTC(),
		})
		if serr != nil {
			return res, fmt.Errorf("cilog: writing job summary %q: %w", opts.SummaryPath, serr)
		}
		res.SummaryPath = opts.SummaryPath
	}
	return res, nil
}

// sortAlerts orders CodeQL alerts deterministically.
func sortAlerts(alerts []CodeScanningAlert) {
	sort.SliceStable(alerts, func(i, k int) bool {
		if alerts[i].RuleID != alerts[k].RuleID {
			return alerts[i].RuleID < alerts[k].RuleID
		}
		if alerts[i].FilePath != alerts[k].FilePath {
			return alerts[i].FilePath < alerts[k].FilePath
		}
		return alerts[i].Number < alerts[k].Number
	})
}
