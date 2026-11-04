// Package cilogreport builds the deterministic consolidated CI run report
// for SHEYTAN Music Visualizer from namespaced per-job log artifacts.
//
// Core contract (Phase 1 mission §8):
//   - Artifact namespaces are preserved: files live under
//     <parts-root>/<artifact-name>/<relative path> and are never flattened,
//     so two jobs may legitimately ship same-named files without collision.
//   - Jobs that never executed (result "skipped") are reported as
//     NEVER_STARTED — this is distinct from a MISSING LOG SOURCE, which is a
//     job that ran (or was cancelled/failed) but contributed no expected
//     log artifact.
//   - ERROR INDEX and WARNING INDEX are extracted with file/line provenance.
//   - The report is always written — including for red runs and runs with
//     missing sources — and the exit status signals coverage: 0 = COMPLETE,
//     1 = INCOMPLETE, 2 = internal failure.
//   - No truncation of log content; no network, database, or external tools.
package cilogreport

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Result values for JobSpec.Result.
const (
	ResultSuccess   = "success"
	ResultFailure   = "failure"
	ResultCancelled = "cancelled"
	ResultSkipped   = "skipped"
)

// Coverage states.
const (
	CoverageComplete   = "COMPLETE"
	CoverageIncomplete = "INCOMPLETE"
)

// Exit codes returned by Execute.
const (
	ExitOK         = 0 // coverage COMPLETE
	ExitIncomplete = 1 // coverage INCOMPLETE (report still written)
	ExitInternal   = 2 // internal failure
)

// JobSpec describes one expected workflow job and its log artifacts.
type JobSpec struct {
	Name              string   `json:"name"`
	Result            string   `json:"result"` // success|failure|cancelled|skipped
	ExpectedArtifacts []string `json:"expected_artifacts"`
}

// Manifest is the run metadata plus the expected job table. It is provided as
// a small JSON file by the collect-logs job (and by local fixture tests).
type Manifest struct {
	Product      string    `json:"product,omitempty"`
	WorkflowFile string    `json:"workflow_file"`
	RunID        string    `json:"run_id"`
	Attempt      string    `json:"attempt"`
	Commit       string    `json:"commit"`
	Ref          string    `json:"ref"`
	Event        string    `json:"event"`
	StartedAt    string    `json:"started_at"`
	RunnerOS     string    `json:"runner_os,omitempty"`
	Jobs         []JobSpec `json:"jobs"`
}

// strictUnmarshal decodes JSON while rejecting unknown fields, so a
// typo in a manifest is a visible error rather than silent data loss.
func strictUnmarshal(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("cilogreport: trailing data after JSON manifest")
	}
	return nil
}

// LoadManifest reads and validates a manifest JSON file.
func LoadManifest(path string) (*Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cilogreport: read manifest: %w", err)
	}
	var m Manifest
	if err := strictUnmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("cilogreport: parse manifest %s: %w", path, err)
	}
	if strings.TrimSpace(m.WorkflowFile) == "" {
		return nil, errors.New("cilogreport: manifest missing workflow_file")
	}
	if len(m.Jobs) == 0 {
		return nil, errors.New("cilogreport: manifest lists no jobs")
	}
	seen := map[string]bool{}
	for i, j := range m.Jobs {
		if strings.TrimSpace(j.Name) == "" {
			return nil, fmt.Errorf("cilogreport: job %d has empty name", i)
		}
		if seen[j.Name] {
			return nil, fmt.Errorf("cilogreport: duplicate job name %q", j.Name)
		}
		seen[j.Name] = true
		switch strings.ToLower(strings.TrimSpace(j.Result)) {
		case ResultSuccess, ResultFailure, ResultCancelled, ResultSkipped:
		default:
			return nil, fmt.Errorf("cilogreport: job %q has invalid result %q (want success|failure|cancelled|skipped)", j.Name, j.Result)
		}
	}
	return &m, nil
}

// Finding is one indexed error or warning with provenance.
type Finding struct {
	Job       string
	File      string // namespace + relative path
	Line      int
	Text      string
	Truncated bool // text display was trimmed for the index only
}

// MissingSource records one expected-but-absent log source.
type MissingSource struct {
	Job      string
	Artifact string
	Reason   string
}

// LogFile is one preserved source file.
type LogFile struct {
	Job      string
	Artifact string
	RelPath  string // path within the artifact namespace
	Size     int64
	Path     string // absolute path on disk
}

// Report is the fully scanned run report.
type Report struct {
	Manifest    *Manifest
	GeneratedAt time.Time

	JobsWithParts   int
	CountSuccess    int
	CountFailure    int
	CountCancelled  int
	CountSkipped    int
	CountUnknownRes int

	Errors       []Finding
	Warnings     []Finding
	NeverStarted []string
	Missing      []MissingSource
	Unreferenced []string // artifact dirs present but not expected by any job
	Files        []LogFile

	FailedCommandRecords int
	Coverage             string
}

var (
	// indexErrorPatterns match the structured failure records emitted by the
	// capture helpers plus GitHub error annotations and go test failures.
	indexErrorPatterns = []*regexp.Regexp{
		regexp.MustCompile(`^ERROR:`),
		regexp.MustCompile(`::error::`),
		regexp.MustCompile(`^EXIT_CODE: [1-9][0-9]* FAIL\b`),
		regexp.MustCompile(`^--- FAIL:`),
		regexp.MustCompile(`^FAIL\b`),
	}
	indexWarningPatterns = []*regexp.Regexp{
		regexp.MustCompile(`^WARNING:`),
		regexp.MustCompile(`::warning::`),
	}
	failedCommandRe = regexp.MustCompile(`^EXIT_CODE: [1-9][0-9]* FAIL\b`)
)

func normalizeResult(r string) string {
	return strings.ToLower(strings.TrimSpace(r))
}

// Build scans partsRoot according to the manifest and produces the Report.
// It never fails for missing artifacts — missing sources are recorded; the
// caller decides the exit status from Coverage.
func Build(m *Manifest, partsRoot string, now time.Time) (*Report, error) {
	if m == nil {
		return nil, errors.New("cilogreport: nil manifest")
	}
	r := &Report{Manifest: m, GeneratedAt: now.UTC()}

	expectedArtifacts := map[string][]string{}
	for _, j := range m.Jobs {
		switch normalizeResult(j.Result) {
		case ResultSuccess:
			r.CountSuccess++
		case ResultFailure:
			r.CountFailure++
		case ResultCancelled:
			r.CountCancelled++
		case ResultSkipped:
			r.CountSkipped++
		default:
			r.CountUnknownRes++
		}
		expectedArtifacts[j.Name] = j.ExpectedArtifacts
	}

	// Scan jobs in manifest order (deterministic).
	for _, j := range m.Jobs {
		res := normalizeResult(j.Result)
		if res == ResultSkipped {
			r.NeverStarted = append(r.NeverStarted, j.Name)
			continue
		}
		for _, art := range j.ExpectedArtifacts {
			dir := filepath.Join(partsRoot, filepath.FromSlash(art))
			files, err := listFiles(j.Name, dir)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					r.Missing = append(r.Missing, MissingSource{Job: j.Name, Artifact: art,
						Reason: "artifact directory not found under parts root"})
				} else {
					r.Missing = append(r.Missing, MissingSource{Job: j.Name, Artifact: art,
						Reason: fmt.Sprintf("artifact directory unreadable: %v", err)})
				}
				continue
			}
			if len(files) == 0 {
				r.Missing = append(r.Missing, MissingSource{Job: j.Name, Artifact: art,
					Reason: "artifact directory exists but contains no files"})
				continue
			}
			r.Files = append(r.Files, files...)
		}
	}

	// A job "with captured parts" shipped at least one expected file.
	hadPart := map[string]bool{}
	for _, f := range r.Files {
		if !hadPart[f.Job] {
			hadPart[f.Job] = true
			r.JobsWithParts++
		}
	}

	// Unreferenced artifact directories (informational hygiene).
	entries, err := os.ReadDir(partsRoot)
	if err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			referenced := false
			for _, arts := range expectedArtifacts {
				for _, a := range arts {
					if a == e.Name() {
						referenced = true
					}
				}
			}
			if !referenced {
				r.Unreferenced = append(r.Unreferenced, e.Name())
			}
		}
		sort.Strings(r.Unreferenced)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("cilogreport: read parts root: %w", err)
	}

	// Index scan across all captured files, in the same deterministic order.
	for _, f := range r.Files {
		fnd, err := scanIndex(f)
		if err != nil {
			return nil, err
		}
		r.Errors = append(r.Errors, fnd.errors...)
		r.Warnings = append(r.Warnings, fnd.warnings...)
	}
	for _, fnd := range r.Errors {
		if failedCommandRe.MatchString(fnd.Text) {
			r.FailedCommandRecords++
		}
	}

	if len(r.Missing) > 0 {
		r.Coverage = CoverageIncomplete
	} else {
		r.Coverage = CoverageComplete
	}
	return r, nil
}

type indexed struct {
	errors   []Finding
	warnings []Finding
}

func scanIndex(f LogFile) (indexed, error) {
	var out indexed
	file, err := os.Open(f.Path)
	if err != nil {
		return out, fmt.Errorf("cilogreport: open %s: %w", f.Path, err)
	}
	defer file.Close()

	display := f.Artifact + "/" + filepath.ToSlash(f.RelPath)
	sc := bufio.NewScanner(file)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	lineNo := 0
	for sc.Err() == nil {
		if !sc.Scan() {
			break
		}
		lineNo++
		text := sc.Text()
		for _, re := range indexErrorPatterns {
			if re.MatchString(text) {
				out.errors = append(out.errors, Finding{
					Job: f.Job, File: display, Line: lineNo,
					Text: text, Truncated: len(text) > 400,
				})
				break
			}
		}
		for _, re := range indexWarningPatterns {
			if re.MatchString(text) {
				out.warnings = append(out.warnings, Finding{
					Job: f.Job, File: display, Line: lineNo,
					Text: text, Truncated: len(text) > 400,
				})
				break
			}
		}
	}
	if err := sc.Err(); err != nil {
		return out, fmt.Errorf("cilogreport: scan %s: %w", f.Path, err)
	}
	return out, nil
}

// listFiles walks dir recursively and returns sorted, namespace-preserving
// entries. Empty directories yield an empty (non-error) slice.
func listFiles(job, dir string) ([]LogFile, error) {
	var out []LogFile
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil // skip symlinks/irregular entries defensively
		}
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil {
			return rerr
		}
		st, serr := d.Info()
		if serr != nil {
			return serr
		}
		art := filepath.Base(dir)
		out = append(out, LogFile{
			Job:      job,
			Artifact: art,
			RelPath:  rel,
			Size:     st.Size(),
			Path:     p,
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, k int) bool {
		return out[i].RelPath < out[k].RelPath
	})
	return out, nil
}

// jobForArtifact maps an artifact directory back to its owning job. The
// caller overwrites this field with the manifest job during Build ordering,
// but listFiles needs a deterministic fallback derived from the directory
// name itself.
func jobForArtifact(artifact string) string { return artifact }

// Render writes the deterministic plain-text report. It copies full file
// content verbatim — no truncation.
func (r *Report) Render(w io.Writer) error {
	m := r.Manifest
	bar := strings.Repeat("=", 78)
	sub := strings.Repeat("-", 78)

	fmt.Fprintln(w, bar)
	fmt.Fprintln(w, "SHEYTAN MUSIC VISUALIZER — CONSOLIDATED CI RUN LOG")
	fmt.Fprintln(w, bar)
	prod := m.Product
	if prod == "" {
		prod = "SHEYTAN Music Visualizer (SHEYTAN™)"
	}
	fmt.Fprintf(w, "Product: %s\n", prod)
	fmt.Fprintf(w, "Workflow file: %s\n", m.WorkflowFile)
	fmt.Fprintf(w, "Run ID: %s  Attempt: %s\n", orNA(m.RunID), orNA(m.Attempt))
	fmt.Fprintf(w, "Commit: %s  Ref: %s  Event: %s\n", orNA(m.Commit), orNA(m.Ref), orNA(m.Event))
	fmt.Fprintf(w, "Run started (UTC): %s\n", orNA(m.StartedAt))
	fmt.Fprintf(w, "Report generated (UTC): %s\n", r.GeneratedAt.Format("2006-01-02T15:04:05Z"))
	fmt.Fprintf(w, "Runner OS: %s\n", orNA(m.RunnerOS))
	fmt.Fprintln(w)

	fmt.Fprintln(w, sub)
	fmt.Fprintln(w, "RUN SUMMARY")
	fmt.Fprintln(w, sub)
	fmt.Fprintf(w, "Expected jobs: %d\n", len(m.Jobs))
	fmt.Fprintf(w, "Jobs with captured log parts: %d\n", r.JobsWithParts)
	fmt.Fprintf(w, "Outcomes: success=%d failure=%d cancelled=%d skipped=%d unknown=%d\n",
		r.CountSuccess, r.CountFailure, r.CountCancelled, r.CountSkipped, r.CountUnknownRes)
	fmt.Fprintf(w, "Missing log sources: %d\n", len(r.Missing))
	fmt.Fprintf(w, "Failed command records: %d\n", r.FailedCommandRecords)
	fmt.Fprintf(w, "Warnings: %d\n", len(r.Warnings))
	fmt.Fprintf(w, "Log files consolidated: %d\n", len(r.Files))
	fmt.Fprintln(w)

	renderFindings := func(title string, items []Finding) {
		fmt.Fprintln(w, sub)
		fmt.Fprintln(w, title)
		fmt.Fprintln(w, sub)
		if len(items) == 0 {
			fmt.Fprintln(w, "(none)")
		}
		for _, f := range items {
			text := f.Text
			if f.Truncated {
				text = text[:400] + " …[display trimmed; full line preserved in the log section below]"
			}
			fmt.Fprintf(w, "[%s] %s:%d %s\n", f.Job, f.File, f.Line, text)
		}
		fmt.Fprintln(w)
	}
	renderFindings("ERROR INDEX", r.Errors)
	renderFindings("WARNING INDEX", r.Warnings)

	fmt.Fprintln(w, sub)
	fmt.Fprintln(w, "NEVER_STARTED (skipped jobs — these never executed and are NOT missing-log failures)")
	fmt.Fprintln(w, sub)
	if len(r.NeverStarted) == 0 {
		fmt.Fprintln(w, "(none)")
	}
	for _, n := range r.NeverStarted {
		fmt.Fprintf(w, "%s: SKIPPED / NEVER_STARTED\n", n)
	}
	fmt.Fprintln(w)

	fmt.Fprintln(w, sub)
	fmt.Fprintln(w, "MISSING LOG SOURCE (job ran, or was cancelled/failed, but contributed no expected log part)")
	fmt.Fprintln(w, sub)
	if len(r.Missing) == 0 {
		fmt.Fprintln(w, "(none)")
	}
	for _, ms := range r.Missing {
		fmt.Fprintf(w, "job=%q artifact=%q reason=%s\n", ms.Job, ms.Artifact, ms.Reason)
	}
	fmt.Fprintln(w)

	if len(r.Unreferenced) > 0 {
		fmt.Fprintln(w, sub)
		fmt.Fprintln(w, "UNREFERENCED ARTIFACT DIRECTORIES (informational)")
		fmt.Fprintln(w, sub)
		for _, u := range r.Unreferenced {
			fmt.Fprintf(w, "%s\n", u)
		}
		fmt.Fprintln(w)
	}

	fmt.Fprintln(w, bar)
	fmt.Fprintln(w, "FULL LOGS — deterministic order: manifest job order, then lexicographic file order")
	fmt.Fprintln(w, bar)
	for _, f := range r.Files {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "%s\n", sub)
		fmt.Fprintf(w, "[JOB: %s | ARTIFACT: %s | FILE: %s | %d bytes]\n", f.Job, f.Artifact, filepath.ToSlash(f.RelPath), f.Size)
		fmt.Fprintf(w, "%s\n", sub)
		if f.Size == 0 {
			fmt.Fprintln(w, "(empty file — captured as evidence of a present-but-empty log source)")
			continue
		}
		if err := copyFile(w, f.Path); err != nil {
			return fmt.Errorf("cilogreport: copy %s: %w", f.Path, err)
		}
		fmt.Fprintln(w)
	}

	fmt.Fprintln(w)
	fmt.Fprintln(w, bar)
	fmt.Fprintf(w, "COVERAGE: %s\n", r.Coverage)
	if r.Coverage == CoverageComplete {
		fmt.Fprintln(w, "All expected log sources were captured and are preserved above.")
	} else {
		fmt.Fprintf(w, "%d missing log source(s). This report was still written for evidence, but the run FAILED log-coverage requirements.\n", len(r.Missing))
	}
	fmt.Fprintln(w, bar)
	return nil
}

func copyFile(w io.Writer, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(w, file)
	return err
}

func orNA(s string) string {
	if strings.TrimSpace(s) == "" {
		return "N/A"
	}
	return s
}

// Execute is the full pipeline: build the report, write it to reportPath,
// and return the report plus the process exit code. A missing-source run
// still produces the report file and returns ExitIncomplete.
func Execute(m *Manifest, partsRoot, reportPath string, now time.Time) (*Report, int, error) {
	r, err := Build(m, partsRoot, now)
	if err != nil {
		return nil, ExitInternal, err
	}
	if err := os.MkdirAll(filepath.Dir(reportPath), 0o755); err != nil {
		return nil, ExitInternal, fmt.Errorf("cilogreport: create report directory: %w", err)
	}
	out, err := os.Create(reportPath)
	if err != nil {
		return nil, ExitInternal, fmt.Errorf("cilogreport: create report: %w", err)
	}
	defer out.Close()
	if err := r.Render(out); err != nil {
		return nil, ExitInternal, err
	}
	if err := out.Sync(); err != nil {
		return nil, ExitInternal, fmt.Errorf("cilogreport: sync report: %w", err)
	}
	if r.Coverage == CoverageComplete {
		return r, ExitOK, nil
	}
	return r, ExitIncomplete, nil
}
