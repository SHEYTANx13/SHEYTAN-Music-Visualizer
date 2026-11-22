package cilog

// records.go — the strict warning/error record model of the FINAL
// consolidated diagnostics artifact (contract: warning/error-only
// records, stable parseable schema, byte-level ASCII/LF rules).
//
// The final artifact contains ONLY lines with these prefixes:
//
//	WARNING|...
//	ERROR|...
//
// in the fixed field order
//
//	<SEVERITY>|platform=<p>|job=<j>|step=<s>|gate=<g>|command=<c>|
//	exit=<n>|tool=<t>|file=<f>|line=<n>|column=<n>|source=<s>|message=<m>
//
// Fields that are genuinely unavailable are omitted. The source field
// is always emitted: EXACT (file+line+column reported by the tool),
// FILE_AND_LINE (file+line only; a column is never invented), or
// NOT_PROVIDED_BY_TOOL. Two distinct failures that mention the same
// file stay distinct (the normalized message is part of the dedupe
// key); identical records collapse deterministically.

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Severity of one diagnostic record.
type Severity string

const (
	SevError   Severity = "ERROR"
	SevWarning Severity = "WARNING"
)

// Source-location provenance values.
const (
	SourceExact       = "EXACT"
	SourceFileLine    = "FILE_AND_LINE"
	SourceNotProvided = "NOT_PROVIDED_BY_TOOL"
)

// Record is one warning or error diagnostic with full provenance.
type Record struct {
	Severity Severity
	Platform string
	Job      string
	Step     string
	Gate     string
	Command  string
	// ExitCode is the recorded process exit code; -1 = unavailable.
	ExitCode int
	Tool     string
	File     string
	Line     int // 0 = not reported
	Column   int // 0 = not reported (never invented)
	// Source is SourceExact, SourceFileLine or SourceNotProvided.
	Source  string
	Message string
}

// MaxMessageBytes bounds one record's message (kept concise per
// contract; the full text stays in the GitHub job logs).
const MaxMessageBytes = 512

// maxReportBytes is the 256 KiB target ceiling of the final artifact.
const maxReportBytes = 256 * 1024

// Line renders the record in the fixed schema. Deterministic; message
// is already single-line ASCII by construction, but Line() re-enforces
// the invariants defensively.
func (r Record) SchemaLine() string {
	var b strings.Builder
	if r.Severity == SevWarning {
		b.WriteString("WARNING|")
	} else {
		b.WriteString("ERROR|")
	}
	add := func(k, v string) {
		if v == "" {
			return // genuinely unavailable fields are omitted
		}
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(v)
		b.WriteString("|")
	}
	add("platform", asciiField(r.Platform))
	add("job", asciiField(r.Job))
	add("step", asciiField(r.Step))
	add("gate", asciiField(r.Gate))
	add("command", asciiField(r.Command))
	if r.ExitCode >= 0 {
		add("exit", strconv.Itoa(r.ExitCode))
	}
	add("tool", asciiField(r.Tool))
	add("file", asciiField(r.File))
	if r.Line > 0 {
		add("line", strconv.Itoa(r.Line))
	}
	if r.Column > 0 && r.Line > 0 {
		add("column", strconv.Itoa(r.Column))
	}
	if r.Source == "" {
		r.Source = SourceNotProvided
	}
	add("source", r.Source)
	msg := singleLineMessage(r.Message)
	if msg == "" {
		msg = "unspecified diagnostic"
	}
	b.WriteString("message=")
	b.WriteString(msg)
	return b.String()
}

// asciiField forces a field value through the printable-ASCII, no-
// separator contract ('|' and '=' inside values become '/'), tabs
// become spaces, runs of '=' and '-' clamp, non-ASCII becomes '?'.
func asciiField(v string) string {
	if v == "" {
		return ""
	}
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c == '|' || c == '=':
			b.WriteByte('/')
		case c == '\t' || c == '\r' || c == '\n':
			b.WriteByte(' ')
		case c >= 0x20 && c <= 0x7E:
			b.WriteByte(c)
		default:
			b.WriteByte('?') // non-ASCII / control bytes
		}
	}
	s := b.String()
	s = clampEqDash(s)
	return s
}

// clampEqDash clamps runs of '=' or '-' longer than two.
func clampEqDash(s string) string {
	var b strings.Builder
	run := 0
	var prev byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == prev && (c == '=' || c == '-') {
			run++
			if run >= 3 {
				continue
			}
		} else {
			run = 1
			prev = c
		}
		b.WriteByte(c)
	}
	return b.String()
}

// singleLineMessage normalizes a message: single line, tabs/CR/NEL
// become spaces, collapsed whitespace runs, bounded length.
func singleLineMessage(m string) string {
	var b strings.Builder
	space := false
	for i := 0; i < len(m); i++ {
		c := m[i]
		switch {
		case c == '\t' || c == '\r' || c == '\n' || c == 0x0B || c == 0x0C:
			space = true
		case c >= 0x20 && c <= 0x7E:
			if space {
				b.WriteByte(' ')
				space = false
			}
			b.WriteByte(c)
		default:
			b.WriteByte('?') // non-ASCII degrades, by contract
			space = false
		}
	}
	out := b.String()
	// Clamp separator runs (=== ---) defensively.
	out = clampEqDash(out)
	if len(out) > MaxMessageBytes {
		out = out[:MaxMessageBytes-3] + "..."
	}
	return out
}

// ---------------------------------------------------------------------------
// Diagnostic extraction
// ---------------------------------------------------------------------------

var (
	// Go compiler/test/vet: path.go:LINE:COL: severity: text
	reGoDiag = regexp.MustCompile(`^(\S{1,512}?\.go):([0-9]+):([0-9]+): (error|warning|note): (.+)$`)
	// Generic compilers/linkers (gcc/clang/MSVC/kotlinc/swiftc): file:LINE:COL: severity: text
	reGenericDiag = regexp.MustCompile(`^(\S{1,512}?):([0-9]+):([0-9]+): (?:fatal |)(error|warning): (.+)$`)
	// Go tools without a column (vet, some linker forms): path.go:LINE: text
	reGoLineOnly = regexp.MustCompile(`^(\S{1,512}?\.go):([0-9]+): (.+)$`)
	// GitHub workflow annotations.
	// Raw API job logs prefix annotation lines with a timestamp; both
	// bare and timestamp-prefixed forms are recognized and the wrapper
	// syntax never reaches the record message.
	reAnnotation  = regexp.MustCompile(`^(?:[0-9TZ:.\-]+ )?::(error|warning)(?:[ ][^:]*)?::(.*)$`)
	reWorkflowCmd = regexp.MustCompile(`^(?:[0-9TZ:.\-]+ )?##\[(error|warning)\]\s*(.*)$`)
	// Plain severity prefixes.
	rePlainError   = regexp.MustCompile(`(?i)^error:\s*(.+)$`)
	rePlainWarning = regexp.MustCompile(`(?i)^warning:\s*(.+)$`)
	// Go test failures.
	reGoTestFail = regexp.MustCompile(`^--- FAIL: (\S+)(?: \((\d+\.\d+)s\))?$`)
	reGoPkgFail  = regexp.MustCompile(`^FAIL[ \t]+(\S+?)[ \t]+\[build failed\]`)
	reGoPkgFail2 = regexp.MustCompile(`^FAIL[ \t]+(\S+?)[ \t]+([0-9.]+s)`)
)

// extractDiag parses one raw log line into a Record when it carries an
// actual diagnostic. The returned Record has Job/Step left empty (the
// caller fills provenance); ok=false when the line is not a diagnostic
// (documentation text, echoed source, shell chatter).
func extractDiag(line string) (Record, bool) {
	trim := strings.TrimRight(line, " \t\r")
	if m := reGoDiag.FindStringSubmatch(trim); m != nil {
		return toolRecord("go-compiler", m[1], m[2], m[3], m[4], m[5]), true
	}
	if m := reGenericDiag.FindStringSubmatch(trim); m != nil {
		return toolRecord(toolFromPath(m[1]), m[1], m[2], m[3], m[4], m[5]), true
	}
	if m := reGoLineOnly.FindStringSubmatch(trim); m != nil {
		r := toolRecord("go-tool", m[1], m[2], "", "", m[3])
		return r, true
	}
	if m := reAnnotation.FindStringSubmatch(trim); m != nil {
		sev := SevError
		if m[1] == "warning" {
			sev = SevWarning
		}
		return Record{Severity: sev, Tool: "github-annotation", Source: SourceNotProvided, Message: m[2]}, true
	}
	if m := reWorkflowCmd.FindStringSubmatch(trim); m != nil {
		sev := SevError
		if m[1] == "warning" {
			sev = SevWarning
		}
		return Record{Severity: sev, Tool: "github-workflow-command", Source: SourceNotProvided, Message: m[2]}, true
	}
	if m := reGoTestFail.FindStringSubmatch(trim); m != nil {
		return Record{Severity: SevError, Tool: "go-test", Source: SourceNotProvided, Message: "test failure: " + m[1]}, true
	}
	if m := reGoPkgFail.FindStringSubmatch(trim); m != nil {
		return Record{Severity: SevError, Tool: "go-test", Source: SourceNotProvided, Message: "package build failed: " + m[1]}, true
	}
	if m := reGoPkgFail2.FindStringSubmatch(trim); m != nil {
		return Record{Severity: SevError, Tool: "go-test", Source: SourceNotProvided, Message: "package failed: " + m[1]}, true
	}
	if m := rePlainError.FindStringSubmatch(trim); m != nil {
		return Record{Severity: SevError, Tool: "generic", Source: SourceNotProvided, Message: m[1]}, true
	}
	if m := rePlainWarning.FindStringSubmatch(trim); m != nil {
		return Record{Severity: SevWarning, Tool: "generic", Source: SourceNotProvided, Message: m[1]}, true
	}
	return Record{}, false
}

// toolRecord builds a location-carrying Record. A column is kept ONLY
// when the tool reported one; a line-only report never invents one.
func toolRecord(tool, file, lineS, colS, severity, message string) Record {
	line, _ := strconv.Atoi(lineS)
	col := 0
	if colS != "" {
		col, _ = strconv.Atoi(colS)
	}
	sev := SevError
	if severity == "warning" {
		sev = SevWarning
	}
	r := Record{
		Severity: sev,
		Tool:     tool,
		File:     file,
		Line:     line,
		Source:   SourceFileLine,
		Message:  message,
	}
	if col > 0 {
		r.Column = col
		r.Source = SourceExact
	}
	return r
}

// toolFromPath guesses the tool family from a diagnostic file path.
func toolFromPath(path string) string {
	p := strings.ToLower(path)
	switch {
	case strings.HasSuffix(p, ".go"):
		return "go-compiler"
	case strings.HasSuffix(p, ".ps1"):
		return "powershell"
	case strings.HasSuffix(p, ".sh") || strings.HasSuffix(p, ".bash"):
		return "shell"
	case strings.HasSuffix(p, ".kt") || strings.HasSuffix(p, ".kts") || strings.HasSuffix(p, ".gradle"):
		return "gradle-kotlin"
	case strings.HasSuffix(p, ".swift"):
		return "xcode-swift"
	case strings.HasSuffix(p, ".m") || strings.HasSuffix(p, ".mm"):
		return "clang-objective-c"
	case strings.HasSuffix(p, ".rs"):
		return "rustc"
	case strings.HasSuffix(p, ".c") || strings.HasSuffix(p, ".h") || strings.HasSuffix(p, ".cpp") || strings.HasSuffix(p, ".hpp"):
		return "c-compiler"
	case strings.HasSuffix(p, ".yml") || strings.HasSuffix(p, ".yaml"):
		return "actionlint"
	default:
		return "generic-compiler"
	}
}

// platformForJob derives the platform tag from a declared job name.
func platformForJob(job string) string {
	l := strings.ToLower(job)
	switch {
	case strings.Contains(l, "windows"):
		return "windows-x64"
	case strings.Contains(l, "macos") || strings.Contains(l, "darwin"):
		return "macos-arm64"
	case strings.Contains(l, "android"):
		return "android-arm64"
	case strings.Contains(l, "ios"):
		return "ios-compile-smoke"
	case strings.Contains(l, "codeql") || strings.Contains(l, "analyze"):
		return "codeql"
	case strings.Contains(l, "linux"):
		return "linux-x64"
	case strings.Contains(l, "publish"):
		return "publication"
	case strings.Contains(l, "collect") || strings.Contains(l, "log"):
		return "diagnostics"
	case strings.Contains(l, "preflight"):
		return "preflight"
	case strings.Contains(l, "verdict"):
		return "verdict"
	default:
		return "unknown"
	}
}

// ---------------------------------------------------------------------------
// Deduplication and ordering
// ---------------------------------------------------------------------------

// dedupeKey is the stable identity of one diagnostic record.
func (r Record) dedupeKey() string {
	return strings.Join([]string{
		string(r.Severity),
		r.Platform, r.Job, r.Step, r.Gate, r.Command,
		strconv.Itoa(r.ExitCode),
		r.Tool, r.File, strconv.Itoa(r.Line), strconv.Itoa(r.Column),
		normalizeForDedupe(r.Message),
	}, "\x1f")
}

func normalizeForDedupe(m string) string {
	return strings.Join(strings.FieldsFunc(singleLineMessage(m), func(r rune) bool { return r == ' ' }), " ")
}

// DedupeRecords collapses identical records deterministically: the
// first occurrence wins, later provenance does not create duplicates.
// Distinct messages that mention the same file remain distinct.
func DedupeRecords(in []Record) []Record {
	seen := map[string]bool{}
	out := make([]Record, 0, len(in))
	for _, r := range in {
		k := r.dedupeKey()
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, r)
	}
	return out
}

// SortRecords orders records deterministically: ERROR before WARNING,
// then platform, job, step, gate, tool, file, line, column, message.
func SortRecords(rs []Record) {
	sort.SliceStable(rs, func(i, k int) bool {
		a, b := rs[i], rs[k]
		if a.Severity != b.Severity {
			return a.Severity == SevError
		}
		for _, pair := range [][2]string{
			{a.Platform, b.Platform}, {a.Job, b.Job}, {a.Step, b.Step},
			{a.Gate, b.Gate}, {a.Tool, b.Tool}, {a.File, b.File},
		} {
			if pair[0] != pair[1] {
				return pair[0] < pair[1]
			}
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Column != b.Column {
			return a.Column < b.Column
		}
		return a.Message < b.Message
	})
}

// RenderRecords joins records into the final artifact body (one line
// each, LF-terminated).
func RenderRecords(rs []Record) []byte {
	var b strings.Builder
	for _, r := range rs {
		b.WriteString(r.SchemaLine())
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// CapRecords applies the 256 KiB contract: after deduplication the
// distinct record set must fit; when it cannot, the caller fails the
// collector closed with ErrReportCap (no silent truncation, no raw-log
// workaround). overCap reports the post-dedup byte size.
func CapRecords(rs []Record) (errRecord *Record, overCap bool, size int) {
	size = len(RenderRecords(rs))
	if size <= maxReportBytes {
		return nil, false, size
	}
	r := Record{
		Severity: SevError,
		Platform: "diagnostics",
		Job:      "collect-logs",
		Step:     "report",
		Gate:     "diagnostics",
		Command:  "ci-log-collector",
		ExitCode: -1,
		Tool:     "ci-log-collector",
		Source:   SourceNotProvided,
		Message: fmt.Sprintf("diagnostic record limit exceeded: deduplicated warning/error records need %d bytes > %d byte ceiling; distinct records are NOT truncated; full details remain in the GitHub Actions job logs",
			size, maxReportBytes),
	}
	return &r, true, size
}
