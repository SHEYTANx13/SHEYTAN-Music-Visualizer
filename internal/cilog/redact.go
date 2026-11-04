package cilog

// redact.go — deliberate secret redaction for the consolidated report.
//
// The consolidated log must preserve raw records verbatim EXCEPT for
// secrets: workflow tokens, credential material and provider keys that
// may legitimately appear in tool output must never leave the runner
// inside a diagnostic artifact. Redaction is applied to every raw log
// section (artifact parts and API-fetched job logs alike) and to the
// index lines.

import (
	"regexp"
	"strings"
)

var (
	reGitHubToken  = regexp.MustCompile(`gh[pousr]_[A-Za-z0-9_]{20,}`)
	reGitHubPAT    = regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`)
	reAWSAccessKey = regexp.MustCompile(`AKIA[0-9A-Z]{16}`)
	reBearer       = regexp.MustCompile(`(?i)Bearer[ \t]+[A-Za-z0-9._\-]{16,}`)
	rePEMKey       = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`)
	reInlineSecret = regexp.MustCompile(`(?i)\b(token|password|secret|api[_-]?key)["':=]+[ \t]*[A-Za-z0-9._\-/+=]{12,}`)
)

const redacted = "***REDACTED***"

// RedactSecrets applies the built-in secret patterns. order matters:
// PEM blocks first, then long tokens, then inline key=value forms.
func RedactSecrets(s string) string {
	if s == "" {
		return s
	}
	s = rePEMKey.ReplaceAllString(s, redacted)
	s = reGitHubPAT.ReplaceAllString(s, redacted)
	s = reGitHubToken.ReplaceAllString(s, redacted)
	s = reAWSAccessKey.ReplaceAllString(s, redacted)
	s = reBearer.ReplaceAllString(s, "Bearer "+redacted)
	s = reInlineSecret.ReplaceAllString(s, "$1="+redacted)
	return s
}

// Redactor holds one exact extra secret (the workflow token itself)
// and scrubs it everywhere in addition to the pattern list.
type Redactor struct {
	exact []string
}

// NewRedactor returns a redactor that also removes the given exact
// secret values (matched as plain strings, never logged).
func NewRedactor(exact []string) *Redactor {
	r := &Redactor{}
	for _, s := range exact {
		if t := strings.TrimSpace(s); t != "" {
			r.exact = append(r.exact, t)
		}
	}
	return r
}

// Apply redacts the exact secrets first (so the caller's token never
// survives), then the generic patterns.
func (r *Redactor) Apply(s string) string {
	for _, secret := range r.exact {
		if secret != "" {
			s = strings.ReplaceAll(s, secret, redacted)
		}
	}
	return RedactSecrets(s)
}
