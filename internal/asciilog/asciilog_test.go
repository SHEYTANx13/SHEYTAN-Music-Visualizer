package asciilog

import (
	"bytes"
	"testing"
)

func TestSanitizeClampsSeparatorRuns(t *testing.T) {
	in := []byte("===== COMMAND BEGIN =====\n--- FAIL: TestX\n====")
	got := Sanitize(in)
	want := "== COMMAND BEGIN ==\n-- FAIL: TestX\n=="
	if string(got) != want {
		t.Fatalf("sanitize mismatch:\n got %q\nwant %q", got, want)
	}
	if rep := Validate(got); rep.Violations() != 0 {
		t.Fatalf("sanitized output still violates the contract: %+v", rep)
	}
}

func TestSanitizeStripsANSIAndBOM(t *testing.T) {
	in := append([]byte{0xEF, 0xBB, 0xBF}, []byte("\x1b[31mred\x1b[0m plain \x1b]0;title\x07end \x1b[1;32mgreen\x1b[m")...)
	got := Sanitize(in)
	want := "red plain end green"
	if string(got) != want {
		t.Fatalf("ansi/bom mismatch: got %q want %q", got, want)
	}
}

func TestSanitizeNormalizesCRLFAndControlsAndNonASCII(t *testing.T) {
	in := []byte("a\r\nb\rc\u00e9\u4e2d\x00\x7f\tend")
	got := Sanitize(in)
	// U+00E9 is two UTF-8 bytes -> two '?'; U+4E2D is three bytes ->
	// three '?'. NUL and DEL become '?'. CRLF and lone CR normalize to
	// LF; TAB survives.
	want := "a\nb\nc???????\tend"
	if string(got) != want {
		t.Fatalf("sanitize mismatch:\n got %q\nwant %q", got, want)
	}
	if bytes.Contains(got, []byte{0x00}) || bytes.Contains(got, []byte{0x7F}) {
		t.Fatalf("control bytes survived: %q", got)
	}
	if bytes.ContainsAny(got, "\u00e9\u4e2d") {
		t.Fatalf("non-ASCII survived: %q", got)
	}
	if !bytes.HasSuffix(got, []byte("\tend")) {
		t.Fatalf("TAB must survive: %q", got)
	}
	if rep := Validate(got); rep.Violations() != 0 {
		t.Fatalf("sanitized output violates contract: %+v", rep)
	}
}

func TestValidateCountsMatchRun44330231458Metrics(t *testing.T) {
	// Mirror of the exact byte classes measured on the run log. The BOM
	// counts once as a BOM and its three bytes also count as non-ASCII
	// (raw byte semantics, matching the evidence scan).
	in := []byte("\xef\xbb\xbf\x1b[2J===x===\xff---y---\x01")
	rep := Validate(in)
	if rep.BOM != 1 || rep.ANSIEscape != 1 || rep.SeqEquals != 2 || rep.SeqDash != 2 || rep.NonASCII != 4 || rep.Controls != 1 {
		t.Fatalf("unexpected counts: %+v", rep)
	}
	if rep.Violations() != 6 {
		t.Fatalf("violations = %d, want 6", rep.Violations())
	}
	if rep.Bytes != int64(len(in)) {
		t.Fatalf("bytes = %d, want %d", rep.Bytes, len(in))
	}
}

func TestValidateCleanOutput(t *testing.T) {
	clean := []byte("EXIT_CODE: 0 PASS\nSTEP: go-test\nnothing exotic here\n")
	if rep := Validate(clean); rep.Violations() != 0 {
		t.Fatalf("clean buffer reported violations: %+v", rep)
	}
}

func TestSanitizeIdempotent(t *testing.T) {
	in := []byte("===== x ===== \x1b[31mcaf\xc3\xa9 --- \xff====")
	once := Sanitize(in)
	twice := Sanitize(once)
	if !bytes.Equal(once, twice) {
		t.Fatalf("sanitize is not idempotent: %q vs %q", once, twice)
	}
}
