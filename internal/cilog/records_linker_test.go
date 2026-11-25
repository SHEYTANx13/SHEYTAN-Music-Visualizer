package cilog

// records_linker_test.go — contract tests for the run 45532045379
// diagnostic-extraction repair: compiler positions WITHOUT a severity
// token and linker diagnostics (duplicate/undefined symbols, unhandled
// relocations) were invisible to the extractor, so hosted gate failures
// surfaced as bare exit code 1 with no consolidated-log record. Every
// rule here is paired with a negative case that must STAY
// non-diagnostic (the filter contract: genuine diagnostics only, never
// ordinary output).

import "testing"

func TestNoSeverityGoPositionIsExtracted(t *testing.T) {
	r, ok := extractDiag("cmd/app-gogui/main.go:42:5: undefined: foo")
	if !ok {
		t.Fatal("go build error (no severity token) not recognized")
	}
	if r.Tool != "go-compiler" || r.File != "cmd/app-gogui/main.go" || r.Line != 42 || r.Column != 5 || r.Source != SourceExact {
		t.Fatalf("record = %+v", r)
	}
	if r.Severity != SevError {
		t.Fatalf("severity = %s, want ERROR", r.Severity)
	}
}

func TestLinkerDuplicateDefinitionIsExtracted(t *testing.T) {
	line := "link: duplicated definition of symbol _cgo_init, from github.com/go-webgpu/goffi/internal/fakecgo and github.com/ebitengine/purego/internal/fakecgo"
	r, ok := extractDiag(line)
	if !ok {
		t.Fatal("go linker duplicate-definition error not recognized")
	}
	if r.Tool != "go-linker" || r.Severity != SevError || r.Source != SourceNotProvided {
		t.Fatalf("record = %+v", r)
	}
}

func TestClangUndefinedSymbolsIsExtracted(t *testing.T) {
	r, ok := extractDiag("Undefined symbols for architecture arm64:")
	if !ok {
		t.Fatal("clang undefined-symbols header not recognized")
	}
	if r.Tool != "go-linker" || r.Severity != SevError {
		t.Fatalf("record = %+v", r)
	}
}

func TestUnhandledRelocationIsExtracted(t *testing.T) {
	r, ok := extractDiag("goffi_errno_location_stub: unhandled relocation for goffi_errno_location (type 57 (SDYNIMPORT) rtype 7 (R_CALL))")
	if !ok {
		t.Fatal("internal linker relocation failure not recognized")
	}
	if r.Tool != "go-linker" || r.Severity != SevError {
		t.Fatalf("record = %+v", r)
	}
}

func TestGenericNoSeverityPositionWhitelist(t *testing.T) {
	if r, ok := extractDiag("engine/audio.c:10:3: expected ';'"); !ok || r.Tool != "c-compiler" {
		t.Fatalf("c source position: ok=%v tool=%q", ok, r.Tool)
	}
	// Unknown extensions and text-shaped lines stay non-diagnostic.
	for _, line := range []string{
		"12:30: meeting at the office",
		"note: this documents how a warning looks",
		"https://example.com/x.go:1:2: not a diagnostic",
	} {
		if _, ok := extractDiag(line); ok {
			t.Fatalf("non-diagnostic line extracted: %q", line)
		}
	}
}
