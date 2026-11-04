// Command ci-log-collector builds the deterministic consolidated
// plain-text run report for the build-desktop workflow. The report is
// ONE plain-text file named exactly `log.txt`, uploaded as an artifact
// named exactly `log.txt` containing exactly that one file.
//
// Standard library only: the collector must stay runnable even when the
// application dependency graph is broken.
//
// Usage:
//
//	ci-log-collector --report <log.txt> --parts-root <ci-parts/> --manifest <manifest.json>
//	    [--github-repo owner/name] [--run-id N] [--attempt N]
//	    [--github-api https://api.github.com] [--fetch-jobs=false]
//
// The GitHub token is read from the GITHUB_TOKEN or GH_TOKEN environment
// variable and is never written to the report (exact-value redaction plus
// generic secret patterns). When repository/run identifiers are provided
// the collector enriches the report with the AUTHORITATIVE job/step
// conclusions and complete raw job logs of the same run attempt; failure
// to fetch is recorded in the report as an explicit limitation.
//
// Exit codes:
//
//	0  report written; coverage COMPLETE and every required job succeeded
//	1  report written; coverage INCOMPLETE (missing log sources) —
//	   the calling job must fail AFTER uploading the report
//	3  report written, but at least one required job did not succeed —
//	   the calling job must fail AFTER uploading the report; a
//	   successful collection never masks a failed build/CodeQL job
//	2  hard error (invalid manifest, unreadable parts, unwritable report)
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/cilog"
)

func main() {
	fs := flag.NewFlagSet("ci-log-collector", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	report := fs.String("report", "", "path of the consolidated report to write (required; name it log.txt)")
	partsRoot := fs.String("parts-root", "", "root directory containing namespaced artifact directories (required)")
	manifest := fs.String("manifest", "", "path of the run manifest JSON (required)")
	repo := fs.String("github-repo", os.Getenv("GITHUB_REPOSITORY"), "GitHub repository as owner/name (default $GITHUB_REPOSITORY)")
	runID := fs.String("run-id", "", "workflow run id for authoritative job/step conclusions")
	attempt := fs.String("attempt", "1", "workflow run attempt")
	apiBase := fs.String("github-api", "https://api.github.com", "GitHub REST API base URL")
	fetchJobs := fs.Bool("fetch-jobs", true, "fetch authoritative job/step conclusions (and raw job logs) when run-id is set")
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	if *report == "" || *partsRoot == "" || *manifest == "" {
		fmt.Fprintln(os.Stderr, "ci-log-collector: --report, --parts-root and --manifest are all required")
		fs.Usage()
		os.Exit(2)
	}
	meta, err := cilog.ParseManifest(*manifest)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	opts := cilog.Options{
		Meta:         meta,
		PartsRoot:    *partsRoot,
		ReportPath:   *report,
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
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	fmt.Fprintf(os.Stdout, "ci-log-collector: report %q written; coverage=%s; overall=%s; jobs=%d with-parts=%d missing=%d skipped=%d log-files=%d api-jobs=%d api-logs=%d failed-records=%d warnings=%d\n",
		res.ReportPath, res.Coverage, res.Overall, res.JobsExpected, res.JobsWithParts,
		len(res.MissingSources), len(res.NeverStarted), res.LogFiles,
		res.APIJobsFetched, res.APILogsFetched, res.FailedCommandRecords, res.WarningRecords)
	switch {
	case res.Coverage == cilog.CoverageIncomplete:
		os.Exit(1)
	case res.Overall == cilog.OverallFail:
		os.Exit(3)
	default:
		os.Exit(0)
	}
}
