package cilog

// records_test.go — contract tests for the warning/error record model
// (§4.4/§4.5/§4.6/§4.8): schema shape, source-location rules, severity
// preservation, distinct failures stay distinct, deduplication, size
// cap fail-closed behavior, platform provenance, and message hygiene.

import (
	"strings"
	"testing"
)

func TestGoCompilerLocationsPreservedExactly(t *testing.T) {
	r, ok := extractDiag("internal/example.go:42:7: error: undefined: widget")
	if !ok {
		t.Fatal("Go diagnostic not recognized")
	}
	if r.Severity != SevError || r.Tool != "go-compiler" || r.File != "internal/example.go" || r.Line != 42 || r.Column != 7 || r.Source != SourceExact {
		t.Fatalf("record = %+v", r)
	}
	line := r.SchemaLine()
	if !strings.Contains(line, "file=internal/example.go|line=42|column=7|source=EXACT") {
		t.Fatalf("schema line lost location: %s", line)
	}
}

func TestGoLineOnlyNeverInventsColumn(t *testing.T) {
	r, ok := extractDiag("internal/example.go:42: some vet complaint")
	if !ok {
		t.Fatal("go line-only diagnostic not recognized")
	}
	if r.Column != 0 || r.Source != SourceFileLine || r.Line != 42 {
		t.Fatalf("column invented or source wrong: %+v", r)
	}
	if strings.Contains(r.SchemaLine(), "column=") {
		t.Fatal("column field must be omitted when the tool did not report one")
	}
}

func TestNoLocationBecomesNotProvided(t *testing.T) {
	r, ok := extractDiag("error: linker failed with status 1")
	if !ok {
		t.Fatal("plain error not recognized")
	}
	if r.Source != SourceNotProvided || r.File != "" {
		t.Fatalf("source provenance wrong: %+v", r)
	}
	if !strings.Contains(r.SchemaLine(), "source=NOT_PROVIDED_BY_TOOL") {
		t.Fatalf("schema must mark missing location: %s", r.SchemaLine())
	}
}

func TestSeverityPreservedBothWays(t *testing.T) {
	e, _ := extractDiag("::error::boom")
	w, _ := extractDiag("::warning::caution")
	if e.Severity != SevError || w.Severity != SevWarning {
		t.Fatalf("severities = %s / %s", e.Severity, w.Severity)
	}
	if !strings.HasPrefix(e.SchemaLine(), "ERROR|") || !strings.HasPrefix(w.SchemaLine(), "WARNING|") {
		t.Fatal("severity prefixes lost in schema")
	}
}

func TestMultipleErrorsFromOneCommandRemainDistinct(t *testing.T) {
	a, _ := extractDiag("internal/a.go:10:1: error: first failure")
	b, _ := extractDiag("internal/a.go:99:9: error: second failure")
	c, _ := extractDiag("internal/a.go:10:1: error: first failure")
	recs := DedupeRecords([]Record{a, b, c})
	if len(recs) != 2 {
		t.Fatalf("dedupe = %d records, want 2 (same-file failures with distinct text stay; identical collapse)", len(recs))
	}
}

func TestDocumentationWarningTextIsNotADiagnostic(t *testing.T) {
	for _, line := range []string{
		"// always handle the warning case in this comment",
		"    if x == nil { log.Println(\"no warnings\") }",
		"see the section on error handling below",
		"+ grep -q error handler.sh",
		"STEP: go-test",
		"ok      example.com/pkg 0.5s",
	} {
		if _, ok := extractDiag(line); ok {
			t.Fatalf("documentation/chatter line classified as diagnostic: %q", line)
		}
	}
}

func TestAnnotationTimestampPrefixStripped(t *testing.T) {
	r, ok := extractDiag("2026-11-04T21:20:33.1234567Z ##[error]Process completed with exit code 1.")
	if !ok {
		t.Fatal("timestamp-prefixed ##[error] not recognized")
	}
	if strings.Contains(r.Message, "##[") || strings.Contains(r.Message, "2026-11-04") {
		t.Fatalf("wrapper syntax leaked into message: %q", r.Message)
	}
}

func TestSchemaLineShape(t *testing.T) {
	r := Record{
		Severity: SevError, Platform: "windows-x64", Job: "build-windows",
		Step: "compiler", Gate: "desktop-build", Command: "go-build",
		ExitCode: 1, Tool: "go-compiler", File: "internal/example.go",
		Line: 42, Column: 7, Source: SourceExact, Message: "example compilation failed",
	}
	want := "ERROR|platform=windows-x64|job=build-windows|step=compiler|gate=desktop-build|command=go-build|exit=1|tool=go-compiler|file=internal/example.go|line=42|column=7|source=EXACT|message=example compilation failed"
	if got := r.SchemaLine(); got != want {
		t.Fatalf("schema mismatch:\n got %s\nwant %s", got, want)
	}
}

func TestSchemaLineSanitizesHostileValues(t *testing.T) {
	r := Record{
		Severity: SevError, Platform: "win\tows", Job: "j|j=x", Tool: "t",
		Source: SourceNotProvided, Message: "multi\nline\tmsg === and---long",
	}
	line := r.SchemaLine()
	if strings.Contains(line, "\t") || strings.Contains(line, "===") || strings.Contains(line, "---") {
		t.Fatalf("hostile values survived: %s", line)
	}
	if strings.Count(line, "|") < 3 {
		t.Fatal("field separators lost")
	}
}

func TestPlatformDerivation(t *testing.T) {
	cases := map[string]string{
		"build-windows":    "windows-x64",
		"build-linux":      "linux-x64",
		"build-macos":      "macos-arm64",
		"build-android":    "android-arm64",
		"collect-logs":     "diagnostics",
		"publish-products": "publication",
		"preflight":        "preflight",
		"mystery":          "unknown",
	}
	for job, want := range cases {
		if got := platformForJob(job); got != want {
			t.Fatalf("platformForJob(%q) = %q, want %q", job, got, want)
		}
	}
}

func TestCapRecordsFailsClosed(t *testing.T) {
	// Build a record set whose rendered size exceeds the 256 KiB ceiling
	// with distinct messages (deduplication cannot collapse them).
	var recs []Record
	big := strings.Repeat("distinct failure text ", 30)
	for i := 0; i < 12000; i++ {
		recs = append(recs, Record{
			Severity: SevError, Platform: "linux-x64", Job: "build-linux",
			Tool: "go-compiler", Source: SourceNotProvided,
			Message: big + string(rune('a'+i%26)) + "-" + itoaForTest(i),
		})
	}
	errRec, over, size := CapRecords(recs)
	if !over || errRec == nil {
		t.Fatalf("over-cap set not detected (size=%d)", size)
	}
	if !strings.Contains(errRec.Message, "diagnostic record limit exceeded") {
		t.Fatalf("overflow explanation missing: %q", errRec.Message)
	}
	if _, over, _ := CapRecords(recs[:1]); over {
		t.Fatal("single record must not be reported over cap")
	}
}

func itoaForTest(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func TestRenderRecordsEmptySetIsEmptyFile(t *testing.T) {
	if got := string(RenderRecords(nil)); got != "" {
		t.Fatalf("empty record set rendered %q, want empty file", got)
	}
}

func TestExitCodeFieldIncludedWhenKnown(t *testing.T) {
	r := Record{Severity: SevError, Tool: "gate-runner", Source: SourceNotProvided, ExitCode: 2, Message: "m"}
	if !strings.Contains(r.SchemaLine(), "exit=2|") {
		t.Fatalf("exit code lost: %s", r.SchemaLine())
	}
	r.ExitCode = -1
	if strings.Contains(r.SchemaLine(), "exit=") {
		t.Fatal("unknown exit code must be omitted, never fabricated as 0")
	}
}
