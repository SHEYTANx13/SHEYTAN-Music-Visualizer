package cilog

// helpers.go — bridge to the canonical byte-contract implementation
// (internal/asciilog) plus small file/scan utilities shared by the
// report writers. Keeping the bridge here means the collectors and
// writers never import anything but the standard library and the
// asciilog contract package.

import (
	"bufio"
	"os"
	"regexp"
	"strings"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/asciilog"
)

// SanitizeBytes applies the captured-log ASCII contract.
func SanitizeBytes(b []byte) []byte { return asciilog.Sanitize(b) }

// ValidateBytes reports violations under the captured-log contract.
func ValidateBytes(b []byte) asciilog.Report { return asciilog.Validate(b) }

// SanitizeStrictBytes applies the STRICT final-artifact contract
// (printable ASCII + LF only; tabs become spaces).
func SanitizeStrictBytes(b []byte) []byte { return asciilog.SanitizeStrict(b) }

// ValidateStrictBytes reports violations under the strict contract.
func ValidateStrictBytes(b []byte) asciilog.Report { return asciilog.ValidateStrict(b) }

// writeFile writes body to path atomically-permissive (0644). The
// consolidated artifact is not a secret; its content is redacted
// before it ever reaches this call.
func writeArtifactFile(path string, body []byte) error {
	return os.WriteFile(path, body, 0o644)
}

// scanLogText extracts error/warning findings from one raw text source
// (an API-recovered job log) with labeled provenance. It never stores
// the raw text; only matched diagnostic lines survive, deduplicated
// with all their provenance.
func scanLogText(text, job, label string, errs, warns []finding) ([]finding, []finding) {
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := sc.Text()
		where := "[" + job + " | " + label + ":" + itoa(lineNo) + "]"
		matched := false
		for _, re := range errorPatterns {
			if re.MatchString(line) {
				errs = addFinding(errs, line, where)
				matched = true
				break
			}
		}
		if !matched {
			for _, re := range warningPatterns {
				if re.MatchString(line) {
					warns = addFinding(warns, line, where)
					break
				}
			}
		}
	}
	return errs, warns
}

// matchAny reports whether any pattern matches the line.
func matchAny(patterns []*regexp.Regexp, line string) bool {
	for _, re := range patterns {
		if re.MatchString(line) {
			return true
		}
	}
	return false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
