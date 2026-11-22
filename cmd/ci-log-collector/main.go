// Command ci-log-collector builds the deterministic consolidated
// diagnostics artifact for the canonical build workflow
// (.github/workflows/build.yml). The final artifact is ONE plain-text
// file named exactly `SHEYTAN-Music-Visualizer-all-logs.txt` (uploaded
// under that exact name), containing ONLY `WARNING|`/`ERROR|` records
// in the strict ASCII/LF byte contract. Clean runs produce an EMPTY
// file. Run metadata, job/step/gate tables and success summaries go to
// the job summary file (--summary), never into the warning/error
// artifact. Raw job logs are never embedded anywhere.
//
// Standard library only: the collector must stay runnable even when the
// application dependency graph is broken.
//
// Usage:
//
//	ci-log-collector --report <log.txt> --parts-root <ci-parts/> --manifest <manifest.json>
//	    [--summary <summary.txt>] [--github-repo owner/name] [--run-id N] [--attempt N]
//	    [--github-api https://api.github.com] [--fetch-jobs=false]
//
// The GitHub token is read from the GITHUB_TOKEN or GH_TOKEN environment
// variable and is never written to any output (exact-value redaction plus
// generic secret patterns). When repository/run identifiers are provided
// the collector enriches its evidence with the AUTHORITATIVE job/step
// conclusions and the diagnostic lines of the same run attempt's job
// logs; failure to fetch is recorded as an explicit limitation.
//
// Exit codes:
//
//	0  report written; coverage COMPLETE and every required job succeeded
//	1  report written; coverage INCOMPLETE (missing log sources) —
//	   the calling job must fail AFTER uploading the report
//	3  report written, but at least one required job did not succeed —
//	   the calling job must fail AFTER uploading the report; a
//	   successful collection never masks a failed build/CodeQL job
//	2  hard error — no report is written. Distinct causes are
//	   reported with distinct HARD_ERROR stderr prefixes:
//	   manifest-missing / manifest-empty / manifest-invalid
//	   (unreadable or schema-violating manifest), flag/argument
//	   errors, unreadable parts, and internal collection errors
//	   (unwritable report, record-limit overflow). The numeric
//	   contract is unchanged.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/cilog"
)

func main() {
	if rc := run(os.Args[1:], os.Stdout, os.Stderr); rc != 0 {
		os.Exit(rc)
	}
}

func run(args []string, out, errW io.Writer) int {
	fs := flag.NewFlagSet("ci-log-collector", flag.ContinueOnError)
	fs.SetOutput(errW)
	report := fs.String("report", "", "path of the consolidated warning/error report to write (required; name it log.txt)")
	summary := fs.String("summary", "", "path of the human-readable run summary for $GITHUB_STEP_SUMMARY (metadata only, never raw logs)")
	partsRoot := fs.String("parts-root", "", "root directory containing namespaced artifact directories (required)")
	manifest := fs.String("manifest", "", "path of the run manifest JSON (required)")
	repo := fs.String("github-repo", os.Getenv("GITHUB_REPOSITORY"), "GitHub repository as owner/name (default $GITHUB_REPOSITORY)")
	runID := fs.String("run-id", "", "workflow run id for authoritative job/step conclusions")
	attempt := fs.String("attempt", "1", "workflow run attempt")
	apiBase := fs.String("github-api", "https://api.github.com", "GitHub REST API base URL")
	fetchJobs := fs.Bool("fetch-jobs", true, "fetch authoritative job/step conclusions (and job-log diagnostic lines) when run-id is set")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *report == "" || *partsRoot == "" || *manifest == "" {
		fmt.Fprintln(errW, "ci-log-collector: --report, --parts-root and --manifest are all required")
		fs.Usage()
		return 2
	}
	meta, err := cilog.ParseManifest(*manifest)
	if err != nil {
		// Distinguish the rc=2 hard-error causes precisely
		// (run-42376176493 phase 6.1): a missing manifest, a
		// zero-byte manifest and a malformed manifest are three
		// different producer failures and are reported as such.
		// The documented exit-code contract (0/1/2/3) is unchanged.
		if _, statErr := os.Stat(*manifest); statErr != nil {
			fmt.Fprintf(errW,
				"ci-log-collector: HARD_ERROR manifest-missing: %q cannot be read: %v (the manifest step did not create it)\n",
				*manifest, statErr)
		} else if fi, statErr := os.Stat(*manifest); statErr == nil && fi.Size() == 0 {
			fmt.Fprintf(errW,
				"ci-log-collector: HARD_ERROR manifest-empty: %q is 0 bytes (the manifest writer failed after file creation and before writing content; see manifest-diagnostics.txt)\n",
				*manifest)
		} else {
			fmt.Fprintf(errW,
				"ci-log-collector: HARD_ERROR manifest-invalid: %v\n", err)
		}
		return 2
	}

	opts := cilog.Options{
		Meta:         meta,
		PartsRoot:    *partsRoot,
		ReportPath:   *report,
		SummaryPath:  *summary,
		FetchJobLogs: *fetchJobs,
	}
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		token = os.Getenv("GH_TOKEN")
	}
	if *runID != "" && *repo != "" {
		opts.API = cilog.NewGitHubClient(*apiBase, *repo, token)
		opts.ExtraSecrets = []string{token}
		opts.Meta.Repository = *repo
		if opts.Meta.RunID == "" {
			opts.Meta.RunID = *runID
		}
		if opts.Meta.Attempt == "" {
			opts.Meta.Attempt = *attempt
		}
	}

	res, err := cilog.Collect(opts)
	if err != nil {
		fmt.Fprintf(errW, "ci-log-collector: HARD_ERROR collect: %v\n", err)
		return 2
	}
	fmt.Fprintf(out, "ci-log-collector: report %q written (%d bytes); coverage=%s; overall=%s; error-records=%d; warning-records=%d; jobs=%d with-parts=%d missing=%d skipped=%d log-files=%d api-jobs=%d api-logs=%d gates=%d failed-gates=%d failed-records=%d warnings=%d\n",
		res.ReportPath, res.ReportBytes, res.Coverage, res.Overall,
		res.ErrorRecordCount, res.WarningRecordCount,
		res.JobsExpected, res.JobsWithParts,
		len(res.MissingSources), len(res.NeverStarted), res.LogFiles,
		res.APIJobsFetched, res.APILogsFetched,
		len(res.Gates), res.FailedGateCount, res.FailedCommandRecords, res.WarningRecords)
	if res.SummaryPath != "" {
		fmt.Fprintf(out, "ci-log-collector: summary %q written\n", res.SummaryPath)
	}
	switch {
	case res.Coverage == cilog.CoverageIncomplete:
		return 1
	case res.Overall == cilog.OverallFail:
		return 3
	default:
		return 0
	}
}
