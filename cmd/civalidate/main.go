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
package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/asciilog"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "civalidate: usage: civalidate <path> [path...]")
		os.Exit(2)
	}
	totalViolations := 0
	filesChecked := 0
	for _, arg := range os.Args[1:] {
		info, err := os.Stat(arg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "civalidate: %v\n", err)
			totalViolations++
			continue
		}
		if !info.IsDir() {
			filesChecked++
			if bad := checkFile(arg); bad {
				totalViolations++
			}
			continue
		}
		var paths []string
		err = filepath.WalkDir(arg, func(p string, d fs.DirEntry, werr error) error {
			if werr != nil {
				return werr
			}
			if d.IsDir() {
				return nil
			}
			paths = append(paths, p)
			return nil
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "civalidate: walk %s: %v\n", arg, err)
			totalViolations++
			continue
		}
		sort.Strings(paths)
		for _, p := range paths {
			filesChecked++
			if bad := checkFile(p); bad {
				totalViolations++
			}
		}
	}
	fmt.Printf("civalidate: checked %d file(s), %d violation(s)\n", filesChecked, totalViolations)
	if totalViolations > 0 {
		os.Exit(1)
	}
}

// checkFile scans one file and prints its contract report. It returns
// true when the file violates the contract.
func checkFile(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "civalidate: read %s: %v\n", path, err)
		return true
	}
	rep := asciilog.Validate(b)
	if rep.Violations() == 0 {
		return false
	}
	fmt.Printf("VIOLATION: %s\n", path)
	fmt.Printf("  bytes=%d non_ascii=%d ansi_escape=%d bom=%d seq_equals=%d seq_dash=%d controls=%d\n",
		rep.Bytes, rep.NonASCII, rep.ANSIEscape, rep.BOM, rep.SeqEquals, rep.SeqDash, rep.Controls)
	return true
}
