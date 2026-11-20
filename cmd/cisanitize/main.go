// cisanitize is the canonical capture-time sanitizer CLI of the SHEYTAN
// build-and-runtime log ASCII contract (run 44462492262 repair).
//
// It reads the raw byte stream on stdin, applies the full contract in
// one pass — internal/asciilog.Sanitize (BOM/ANSI/CR/non-ASCII/control
// byte rules and separator-run clamping) followed by
// internal/asciilog.Redact (credential redaction) — and writes the
// compliant ASCII bytes to stdout. Every byte that enters a CI log is
// expected to have passed through this tool (or its documented
// bootstrap fallbacks: scripts/ci/sanitize_bytes.py, or capture.sh's
// loud raw-passthrough marker). sed is NEVER part of this contract.
//
// Usage:
//
//	cisanitize [--report] <raw-input >compliant-output
//
// Flags:
//
//	--report  additionally print one line
//	          "cisanitize: bytes_in=N bytes_out=N" to STDERR.
//
// Exit codes:
//
//	0  success (the full sanitized output was written)
//	2  usage error (unknown flag)
//	3  internal failure: stdin read error, the 512 MiB input bound was
//	   exceeded, or a stdout write error. The tool never exits 0 with
//	   unwritten or partially written output.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/asciilog"
)

// maxInputBytes bounds the stdin buffer at 512 MiB. A runaway producer
// must fail loudly and closed instead of exhausting runner memory.
const maxInputBytes = 512 << 20 // 512 MiB

func main() {
	report := false
	for _, arg := range os.Args[1:] {
		switch arg {
		case "--report":
			report = true
		case "--help", "-h":
			usage()
			os.Exit(0)
		default:
			fmt.Fprintf(os.Stderr, "cisanitize: unknown argument %q (supported: --report)\n", arg)
			os.Exit(2)
		}
	}

	in, err := io.ReadAll(io.LimitReader(os.Stdin, maxInputBytes+1))
	if err != nil {
		fmt.Fprintf(os.Stderr, "cisanitize: read stdin: %v\n", err)
		os.Exit(3)
	}
	if len(in) > maxInputBytes {
		fmt.Fprintf(os.Stderr, "cisanitize: input exceeds the %d MiB bound (%d bytes seen); refusing to buffer — failing closed\n", maxInputBytes>>20, len(in))
		os.Exit(3)
	}

	out, err := run(in, report)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cisanitize: %v\n", err)
		os.Exit(3)
	}
	if _, err := os.Stdout.Write(out); err != nil {
		fmt.Fprintf(os.Stderr, "cisanitize: write stdout: %v\n", err)
		os.Exit(3)
	}
}

// run is the whole CLI transformation (Sanitize, then Redact) plus the
// optional stderr report line, factored out so tests drive exactly the
// logic main() uses.
func run(in []byte, report bool) ([]byte, error) {
	out := asciilog.SanitizeAndRedact(in)
	if report {
		fmt.Fprintf(os.Stderr, "cisanitize: bytes_in=%d bytes_out=%d\n", len(in), len(out))
	}
	return out, nil
}

func usage() {
	fmt.Fprint(os.Stderr, "usage: cisanitize [--report] <raw-input >compliant-output\n")
}
