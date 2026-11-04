// Command ci-log-collector builds the deterministic consolidated
// plain-text run report for the build-desktop workflow.
//
// Standard library only: the collector must stay runnable even when the
// application dependency graph is broken.
//
// Usage:
//
//	ci-log-collector --report <report.txt> --parts-root <ci-parts/> --manifest <manifest.json>
//
// Exit codes:
//
//	0  report written; coverage COMPLETE
//	1  report written; coverage INCOMPLETE (missing log sources) —
//	   the calling job must fail AFTER uploading the report
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
	report := fs.String("report", "", "path of the consolidated report to write (required)")
	partsRoot := fs.String("parts-root", "", "root directory containing namespaced artifact directories (required)")
	manifest := fs.String("manifest", "", "path of the run manifest JSON (required)")
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
	res, err := cilog.Collect(cilog.Options{
		Meta:       meta,
		PartsRoot:  *partsRoot,
		ReportPath: *report,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	fmt.Fprintf(os.Stdout, "ci-log-collector: report %q written; coverage=%s; jobs=%d with-parts=%d missing=%d skipped=%d log-files=%d failed-records=%d warnings=%d\n",
		res.ReportPath, res.Coverage, res.JobsExpected, res.JobsWithParts,
		len(res.MissingSources), len(res.NeverStarted), res.LogFiles,
		res.FailedCommandRecords, res.WarningRecords)
	switch res.Coverage {
	case cilog.CoverageComplete:
		os.Exit(0)
	case cilog.CoverageIncomplete:
		os.Exit(1)
	default:
		os.Exit(2)
	}
}
