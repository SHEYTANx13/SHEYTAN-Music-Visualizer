package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// The fixture corpus below mirrors internal/asciilog's redact_test.go
// corpus and scripts/ci/sanitize_bytes.py --self-test; the portable test
// (scripts/ci/tests/test-sanitizer-portable.sh) additionally proves the
// Go binary and the Python fallback emit identical bytes for the same
// inputs. Fake token shapes only: every secret here is a realistic
// fixture, never a live credential.
var cliFixtures = []struct {
	name string
	in   []byte
	want []byte
}{
	{
		name: "bom",
		in:   []byte("\xef\xbb\xbfplain text stays\n"),
		want: []byte("plain text stays\n"),
	},
	{
		name: "ansi-csi",
		in:   []byte("\x1b[31mred\x1b[0m\x1b[1;32mgreen\x1b[m\x1b]0;title\x07end\n"),
		want: []byte("redgreenend\n"),
	},
	{
		name: "crlf-and-cr-normalize",
		in:   []byte("a\r\nb\rc\n"),
		want: []byte("a\nb\nc\n"),
	},
	{
		name: "utf8-degrades-to-question-marks",
		in:   []byte("caf\xc3\xa9 \xe4\xb8\xad\n"),
		want: []byte("caf?? ???\n"),
	},
	{
		name: "separator-run-clamped",
		in:   []byte("===== begin =====\n--- FAIL: TestX\n"),
		want: []byte("== begin ==\n-- FAIL: TestX\n"),
	},
	{
		name: "secret-redaction",
		in:   []byte("token=ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abcdefgh\n"),
		want: []byte("token=REDACTED\n"),
	},
	{
		name: "everything-at-once",
		in:   []byte("\xef\xbb\xbf\x1b[31mERROR\x1b[0m: boom===\r\nAuthorization: Bearer sk-live-abc123XYZ\n"),
		want: []byte("ERROR: boom==\nAuthorization: Bearer REDACTED\n"),
	},
}

// TestRunFixtures pipes every fixture through run — the exact function
// main() uses — and checks the output bytes.
func TestRunFixtures(t *testing.T) {
	for _, tc := range cliFixtures {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			out, err := run(tc.in, false)
			if err != nil {
				t.Fatalf("run returned error: %v", err)
			}
			if !bytes.Equal(out, tc.want) {
				t.Fatalf("mismatch for %s:\n got %q\nwant %q", tc.name, out, tc.want)
			}
		})
	}
}

// TestRunReportLine verifies the --report stderr line and that enabling
// it never changes the transformed bytes.
func TestRunReportLine(t *testing.T) {
	dir := t.TempDir()
	f, err := os.CreateTemp(dir, "stderr")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	defer os.Remove(f.Name())

	old := os.Stderr
	os.Stderr = f
	out, rerr := run([]byte("abc===\n"), true)
	os.Stderr = old
	f.Close()

	if rerr != nil {
		t.Fatalf("run returned error: %v", rerr)
	}
	if string(out) != "abc==\n" {
		t.Fatalf("report mode changed output: %q", out)
	}
	data, rerr := os.ReadFile(f.Name())
	if rerr != nil {
		t.Fatalf("read captured stderr: %v", rerr)
	}
	want := "cisanitize: bytes_in=7 bytes_out=6\n"
	if string(data) != want {
		t.Fatalf("report line mismatch:\n got %q\nwant %q", data, want)
	}
}

// TestRunEmptyInput pins the empty-stream behavior: zero bytes in, zero
// bytes out, no error.
func TestRunEmptyInput(t *testing.T) {
	out, err := run(nil, false)
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("empty input produced output: %q", out)
	}
}

// TestRunLargeWithinBound exercises a multi-chunk input (larger than
// any single internal read) to prove streaming across chunk boundaries
// keeps the contract: no split escape sequence or separator run leaks.
func TestRunLargeWithinBound(t *testing.T) {
	chunk := "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abcdefgh ===x\x1b[31m\r\n"
	var in bytes.Buffer
	for i := 0; i < 5000; i++ {
		in.WriteString(chunk)
	}
	out, err := run(in.Bytes(), false)
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if bytes.Contains(out, []byte("\x1b")) || bytes.Contains(out, []byte("\r")) || bytes.Contains(out, []byte("===")) {
		t.Fatalf("contract violation survived in large stream")
	}
	if bytes.Contains(out, []byte("ghp_")) {
		t.Fatalf("token survived in large stream")
	}
	if want := len("REDACTED ==x\n") * 5000; len(out) != want {
		t.Fatalf("large-stream byte count: got %d want %d", len(out), want)
	}
}

// TestMaxInputBytesBound pins the documented 512 MiB bound.
func TestMaxInputBytesBound(t *testing.T) {
	if maxInputBytes != 512<<20 {
		t.Fatalf("maxInputBytes = %d, want %d", maxInputBytes, 512<<20)
	}
}

// TestRunIdempotent proves the CLI pipeline can be safely re-applied to
// already-sanitized bytes (the capture.sh raw-passthrough marker relies
// on this).
func TestRunIdempotent(t *testing.T) {
	in := []byte("\xef\xbb\xbf\x1b[31mhello===world\r\nghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abcdefgh\n")
	once, err := run(in, false)
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	twice, err := run(once, false)
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if !bytes.Equal(once, twice) {
		t.Fatalf("not idempotent:\n once %q\n twice %q", once, twice)
	}
	if !strings.Contains(string(once), "REDACTED") {
		t.Fatalf("expected REDACTED in output: %q", once)
	}
}
