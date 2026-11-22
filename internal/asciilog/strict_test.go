package asciilog

// strict_test.go — byte-level tests of the STRICT final-artifact
// contract (§4.2/§4.8): CRLF, isolated CR, tabs, ANSI CSI/OSC, BOM,
// non-ASCII, separator runs, printable-ASCII + LF-only output, and
// strict validation counts.

import (
	"bytes"
	"strings"
	"testing"
)

func TestStrictCRLFAndIsolatedCR(t *testing.T) {
	in := []byte("a\r\nb\rc\nd")
	out := SanitizeStrict(in)
	if want := "a\nb\nc\nd"; string(out) != want {
		t.Fatalf("SanitizeStrict(CRLF/CR) = %q, want %q", out, want)
	}
	rep := ValidateStrict(out)
	if rep.CarriageReturns != 0 || rep.Controls != 0 {
		t.Fatalf("strict validate on sanitized output: %+v", rep)
	}
}

func TestStrictTabsBecomeSpaces(t *testing.T) {
	in := []byte("col1\tcol2\tcol3")
	out := SanitizeStrict(in)
	if want := "col1 col2 col3"; string(out) != want {
		t.Fatalf("tabs not normalized: %q", out)
	}
	rep := ValidateStrict(in)
	if rep.Tabs != 2 || rep.Controls != 2 {
		t.Fatalf("strict validate must count tabs as violations: %+v", rep)
	}
	if rep.Violations() == 0 {
		t.Fatal("tabs must be a violation under the strict contract")
	}
}

func TestStrictANSIAndBOMAndControlsDoNotSurvive(t *testing.T) {
	in := []byte{0xEF, 0xBB, 0xBF}
	in = append(in, []byte("\x1B[31mred\x1B[0m\x1B]0;title\x07\x00\x01mid\x7Fend\n")...)
	out := SanitizeStrict(in)
	for i, b := range out {
		if b == 0x1B || b == 0x07 || b < 0x20 && b != '\n' || b == 0x7F {
			t.Fatalf("escape/control byte 0x%02x survived at %d", b, i)
		}
		if b >= 0x80 {
			t.Fatalf("non-ASCII byte survived at %d", b)
		}
	}
	if rep := ValidateStrict(out); rep.Violations() != 0 {
		t.Fatalf("sanitized output violates strict contract: %+v", rep)
	}
}

func TestStrictNonASCIIDegrades(t *testing.T) {
	in := []byte("smart quote \u201c dash \u2014 bullet \u2022\n")
	out := SanitizeStrict(in)
	if bytes.ContainsAny(out, "\u201c\u2014\u2022") {
		t.Fatal("typographic characters survived")
	}
	if !strings.Contains(string(out), "???") {
		t.Fatalf("expected '?' degradation markers, got %q", out)
	}
}

func TestStrictSeparatorRunsClamped(t *testing.T) {
	in := []byte("a====b----c=====\nd-----e\n")
	out := SanitizeStrict(in)
	if strings.Contains(string(out), "===") || strings.Contains(string(out), "---") {
		t.Fatalf("separator runs survived: %q", out)
	}
	rep := ValidateStrict(in)
	if rep.SeqEquals == 0 || rep.SeqDash == 0 {
		t.Fatalf("validator must count separator runs: %+v", rep)
	}
}

func TestStrictPrintableASCIIPlusLFOnly(t *testing.T) {
	// Every byte of sanitized output must be 0x20..0x7E or LF.
	in := []byte("normal text\nwith \x1B[1mnoise\x1B[0m and \xC3\xA9\xE4\xB8\xAD\n")
	out := SanitizeStrict(in)
	for i, b := range out {
		if (b < 0x20 || b > 0x7E) && b != '\n' {
			t.Fatalf("byte 0x%02x at %d is outside printable-ASCII+LF", b, i)
		}
	}
}

func TestStrictCleanOutputUnchanged(t *testing.T) {
	in := []byte("plain ascii line 1\nplain ascii line 2\n")
	if out := SanitizeStrict(in); !bytes.Equal(out, in) {
		t.Fatalf("clean input modified: %q", out)
	}
}

func TestStrictValidateCleanFile(t *testing.T) {
	rep := ValidateStrict([]byte("ERROR|platform=windows-x64|job=build-windows|message=example\nWARNING|message=m\n"))
	if rep.Violations() != 0 {
		t.Fatalf("clean record file must validate: %+v", rep)
	}
	if rep.Tabs != 0 || rep.CarriageReturns != 0 {
		t.Fatalf("clean file must have zero tabs/CRs: %+v", rep)
	}
}

func TestStrictIdempotent(t *testing.T) {
	in := []byte("a\tb\r\nc\x1B[2Jd \xC3\xA9 === ---\n")
	once := SanitizeStrict(in)
	twice := SanitizeStrict(once)
	if !bytes.Equal(once, twice) {
		t.Fatalf("SanitizeStrict not idempotent:\n once=%q\ntwice=%q", once, twice)
	}
}

func TestStrictEmptyInput(t *testing.T) {
	if out := SanitizeStrict(nil); len(out) != 0 {
		t.Fatalf("empty input produced %d bytes", len(out))
	}
	if rep := ValidateStrict(nil); rep.Violations() != 0 || rep.Bytes != 0 {
		t.Fatalf("empty validate = %+v", rep)
	}
}
