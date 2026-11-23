package cilog

// format_upgrade_test.go — run-45042834042 format-upgrade contract tests:
// annotation properties, embedded compiler positions, no bogus exit=0,
// and the hint= next-action field.
import (
	"strings"
	"testing"
)

// The exact failing annotation class of run 45042834042 (build-android):
// a workflow-command error line carrying a raw Go compile error.
const compileErrorLine = "##[error]internal/mediatools/archive.go:459:76: cannot use maxTotalBytes (untyped int constant 3221225472) as int value in argument to fmt.Errorf (overflows)"

func TestExtractDiagWorkflowCommandEmbeddedPosition(t *testing.T) {
	r, ok := extractDiag(compileErrorLine)
	if !ok {
		t.Fatal("workflow-command compile error not recognized as a diagnostic")
	}
	if r.File != "internal/mediatools/archive.go" {
		t.Fatalf("file = %q, want internal/mediatools/archive.go", r.File)
	}
	if r.Line != 459 || r.Column != 76 {
		t.Fatalf("line/column = %d/%d, want 459/76", r.Line, r.Column)
	}
	if r.Source != SourceExact {
		t.Fatalf("source = %q, want EXACT", r.Source)
	}
	if r.Tool != "go-compiler" {
		t.Fatalf("tool = %q, want go-compiler", r.Tool)
	}
	if r.ExitCode != -1 {
		t.Fatalf("exit code = %d, want -1 (unavailable; never a bogus 0)", r.ExitCode)
	}
	if !strings.HasPrefix(r.Message, "cannot use maxTotalBytes") {
		t.Fatalf("message = %q, want the position prefix stripped", r.Message)
	}
	if strings.Contains(r.Message, "archive.go:459") {
		t.Fatalf("message still embeds the position: %q", r.Message)
	}
}

func TestExtractDiagAnnotationProperties(t *testing.T) {
	r, ok := extractDiag("::error file=pkg/widget.go,line=12,col=3,endLine=12,endColumn=9,title=build::undefined: widget")
	if !ok {
		t.Fatal("annotation not recognized")
	}
	if r.File != "pkg/widget.go" || r.Line != 12 || r.Column != 3 {
		t.Fatalf("location = %s:%d:%d, want pkg/widget.go:12:3", r.File, r.Line, r.Column)
	}
	if r.Source != SourceExact {
		t.Fatalf("source = %q, want EXACT (file+line+col reported by the tool)", r.Source)
	}
	if r.ExitCode != -1 {
		t.Fatalf("exit code = %d, want -1", r.ExitCode)
	}
	if r.Message != "undefined: widget" {
		t.Fatalf("message = %q, want 'undefined: widget'", r.Message)
	}
}

func TestExtractDiagAnnotationFileLineOnly(t *testing.T) {
	r, ok := extractDiag("::warning file=docs/x.md,line=8::stale link")
	if !ok {
		t.Fatal("annotation not recognized")
	}
	if r.File != "docs/x.md" || r.Line != 8 || r.Column != 0 {
		t.Fatalf("location = %s:%d:%d, want docs/x.md:8:0", r.File, r.Line, r.Column)
	}
	if r.Source != SourceFileLine {
		t.Fatalf("source = %q, want FILE_AND_LINE (no column reported)", r.Source)
	}
}

func TestURLIsNeverAPosition(t *testing.T) {
	r, ok := extractDiag("::error https://example.com/x.go:12:34: boom")
	if !ok {
		t.Fatal("annotation not recognized")
	}
	if r.File != "" {
		t.Fatalf("file = %q, want empty (a URL is not a source position)", r.File)
	}
	if r.Message != "https://example.com/x.go:12:34: boom" {
		t.Fatalf("message = %q, want unchanged", r.Message)
	}
}

func TestSchemaLineHintField(t *testing.T) {
	r, _ := extractDiag(compileErrorLine)
	line := r.SchemaLine()
	if !strings.Contains(line, "hint=widen-the-constant-to-int64-value-exceeds-32-bit-int") {
		t.Fatalf("schema line missing overflow hint: %s", line)
	}
	if !strings.Contains(line, "file=internal/mediatools/archive.go|line=459|column=76|source=EXACT|hint=") {
		t.Fatalf("schema line field order wrong: %s", line)
	}

	clean, _ := extractDiag("ERROR: some unknown failure text")
	line2 := clean.SchemaLine()
	if strings.Contains(line2, "hint=") {
		t.Fatalf("unmatched diagnostic must not carry a hint: %s", line2)
	}
}

func TestSchemaLineOmitsUnavailableExit(t *testing.T) {
	r, _ := extractDiag("ERROR: APK missing or empty")
	line := r.SchemaLine()
	if strings.Contains(line, "exit=") {
		t.Fatalf("finding record must not invent exit=0: %s", line)
	}
	if !strings.Contains(line, "hint=apk-not-produced-inspect-android-build-steps-above") {
		t.Fatalf("APK-missing hint missing: %s", line)
	}
}

func TestToolRecordKeepsExitMinusOne(t *testing.T) {
	r := toolRecord("go-compiler", "a/b.go", "3", "7", "error", "boom")
	if r.ExitCode != -1 {
		t.Fatalf("tool record exit = %d, want -1", r.ExitCode)
	}
	if r.Source != SourceExact {
		t.Fatalf("source = %q, want EXACT", r.Source)
	}
}
