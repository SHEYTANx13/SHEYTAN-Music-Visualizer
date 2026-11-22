// civalidate is the byte-level build-log contract gate: it scans the
// given files (or walks the given directories) and fails with exit
// code 1 when any file violates the ASCII log contract implemented in
// internal/asciilog: no non-ASCII bytes, no ANSI escape bytes, no
// UTF-8 BOMs, no "===" or "---" sequences, no stray control bytes.
//
// Usage:
//
//	civalidate <path> [path...]
//
// Paths may be files or directories (walked recursively). CI jobs run
// this over ci-logs/ immediately before artifact upload so a contract
// violation fails the producing job instead of surfacing only in the
// consolidated report.
//
// With --strict the STRICT final-artifact contract is applied instead
// (printable ASCII + LF only; TAB and CR are violations). The final
// consolidated diagnostics artifact is gated with --strict; captured
// step logs use the default contract (tabs allowed).
package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/asciilog"
)

func main() {
	if rc := run(os.Args[1:], os.Stdout, os.Stderr); rc != 0 {
		os.Exit(rc)
	}
}

// run executes the validator over the given paths. Exit codes:
// 0 = all files comply, 1 = at least one violation or unreadable path,
// 2 = usage error.
func run(args []string, out, errW io.Writer) int {
	strict := false
	var paths []string
	for _, a := range args {
		if a == "--strict" {
			strict = true
			continue
		}
		if a == "--help" || a == "-h" {
			fmt.Fprintln(errW, "usage: civalidate [--strict] <path> [path...]")
			return 2
		}
		if a == "--" {
			continue
		}
		paths = append(paths, a)
	}
	if len(paths) == 0 {
		fmt.Fprintln(errW, "civalidate: usage: civalidate <path> [path...]")
		return 2
	}
	totalViolations := 0
	filesChecked := 0
	for _, arg := range paths {
		info, err := os.Stat(arg)
		if err != nil {
			fmt.Fprintf(errW, "civalidate: %v\n", err)
			totalViolations++
			continue
		}
		if !info.IsDir() {
			filesChecked++
			if bad := checkFile(arg, strict, out); bad {
				totalViolations++
			}
			continue
		}
		var found []string
		err = filepath.WalkDir(arg, func(p string, d fs.DirEntry, werr error) error {
			if werr != nil {
				return werr
			}
			if d.IsDir() {
				return nil
			}
			found = append(found, p)
			return nil
		})
		if err != nil {
			fmt.Fprintf(errW, "civalidate: walk %s: %v\n", arg, err)
			totalViolations++
			continue
		}
		sort.Strings(found)
		for _, p := range found {
			filesChecked++
			if bad := checkFile(p, strict, out); bad {
				totalViolations++
			}
		}
	}
	fmt.Fprintf(out, "civalidate: checked %d file(s), %d violation(s)\n", filesChecked, totalViolations)
	if totalViolations > 0 {
		return 1
	}
	return 0
}

// checkFile scans one file and prints its contract report. It returns
// true when the file violates the selected contract.
func checkFile(path string, strict bool, out io.Writer) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "civalidate: read %s: %v\n", path, err)
		return true
	}
	var rep asciilog.Report
	if strict {
		rep = asciilog.ValidateStrict(b)
	} else {
		rep = asciilog.Validate(b)
	}
	if rep.Violations() == 0 {
		return false
	}
	fmt.Fprintf(out, "VIOLATION: %s\n", path)
	fmt.Fprintf(out, "  bytes=%d non_ascii=%d ansi_escape=%d bom=%d seq_equals=%d seq_dash=%d controls=%d tabs=%d cr=%d\n",
		rep.Bytes, rep.NonASCII, rep.ANSIEscape, rep.BOM, rep.SeqEquals, rep.SeqDash, rep.Controls, rep.Tabs, rep.CarriageReturns)
	return true
}
