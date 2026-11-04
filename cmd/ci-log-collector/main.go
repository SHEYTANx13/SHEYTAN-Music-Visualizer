// ci-log-collector builds the consolidated plain-text CI run report for
// SHEYTAN Music Visualizer from namespaced per-job log artifacts.
//
// Usage:
//
//	ci-log-collector -manifest run-manifest.json -parts-root ci-parts -report consolidated-run-log.txt
//
// The manifest is a small JSON file describing run metadata and the expected
// job table (see internal/cilogreport.Manifest). The parts root contains one
// directory per downloaded log artifact, preserving namespaces:
//
//	ci-parts/
//	  preflight-logs-part/preflight-build.log
//	  windows-logs-part/windows-build.log
//	  linux-logs-part/linux-build.log
//
// Exit codes:
//
//	0  coverage COMPLETE — all expected log sources captured
//	1  coverage INCOMPLETE — report written, but expected sources are missing
//	2  internal failure (bad manifest, unreadable parts root, I/O error)
//
// Standard library only: this tool must stay runnable even when the
// application dependency graph is broken.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/cilogreport"
)

func main() {
	manifestPath := flag.String("manifest", "", "path to the run manifest JSON (required)")
	partsRoot := flag.String("parts-root", "ci-parts", "root directory containing namespaced artifact directories")
	reportPath := flag.String("report", "consolidated-run-log.txt", "path of the consolidated plain-text report to write")
	flag.Parse()

	if *manifestPath == "" {
		fmt.Fprintln(os.Stderr, "ci-log-collector: -manifest is required")
		flag.Usage()
		os.Exit(cilogreport.ExitInternal)
	}

	m, err := cilogreport.LoadManifest(*manifestPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ci-log-collector: %v\n", err)
		os.Exit(cilogreport.ExitInternal)
	}

	r, code, err := cilogreport.Execute(m, *partsRoot, *reportPath, time.Now())
	if err != nil {
		fmt.Fprintf(os.Stderr, "ci-log-collector: %v\n", err)
		fmt.Fprintf(os.Stderr, "ci-log-collector: report could not be produced; caller must write a fallback report\n")
		os.Exit(cilogreport.ExitInternal)
	}

	fmt.Printf("ci-log-collector: wrote %s (coverage %s, missing sources: %d, failed command records: %d)\n",
		*reportPath, r.Coverage, len(r.Missing), r.FailedCommandRecords)
	os.Exit(code)
}
