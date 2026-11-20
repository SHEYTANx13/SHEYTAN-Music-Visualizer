// Redaction pass of the SHEYTAN build-and-runtime log ASCII contract
// (run 44462492262 remediation). This file only ADDS functionality to
// the package; the byte rules themselves live in asciilog.go.
//
// Redact applies a small, conservative, fully tested set of
// credential-redaction patterns to ASCII text (input is expected to
// have already passed Sanitize, so it contains no escape bytes, no
// CR, no non-ASCII bytes and no stray controls). Patterns, applied in
// this fixed order:
//
//  1. URLs with embedded credentials: "scheme://user:password@" keeps
//     the scheme and the user name and replaces the password with
//     REDACTED ("scheme://user:REDACTED@").
//  2. "Authorization: Bearer <token>" (case-insensitive) keeps the
//     header shape and replaces the token with REDACTED.
//  3. GitHub fine-grained personal access tokens: a "github_pat_"
//     prefix followed by a token run of at least 40 token characters
//     becomes REDACTED (the whole run).
//  4. GitHub classic tokens: "ghp_", "gho_", "ghs_" or "ghr_" prefixes
//     followed by a run of at least 36 alphanumeric characters become
//     REDACTED (the whole run).
//  5. AWS access key IDs: "\bAKIA[0-9A-Z]{16}\b" becomes REDACTED.
//  6. Credential-bearing query parameters: "token=", "password=",
//     "secret=", "api_key=", "apikey=", "access_token="
//     (case-insensitive) keep the parameter name and replace the value
//     (up to "&", whitespace or a quote) with REDACTED.
//  7. Private environment value dumps: a line matching
//     "^[A-Z_]*(TOKEN|SECRET|PASSWORD|API_KEY)[A-Z_]*=" keeps the
//     variable name and replaces the whole value with REDACTED.
//
// Every pattern is written so that its own output is a fixed point:
// replacing an already-redacted value yields the same bytes, so Redact
// is idempotent and never double-redacts. The patterns deliberately
// err toward redaction (a secret in a log is unrecoverable; an extra
// REDACTED marker is merely noisy), while requiring a realistic token
// shape (length and character class) so ordinary log lines are never
// altered.
package asciilog

import "regexp"

// redactMarker is the literal replacement for every redacted secret.
const redactMarker = "REDACTED"

// redactionRule is one ordered credential-redaction pattern.
type redactionRule struct {
	name string
	re   *regexp.Regexp
	repl string
}

// redactionRules is the ordered rule table. Order matters only for
// readability of overlapping cases (for example a Bearer header
// carrying a GitHub token is fully consumed by rule 2 before rule 4
// could see it); every rule is a fixed point, so any order produces
// idempotent output.
var redactionRules = []redactionRule{
	{
		name: "url-embedded-credentials",
		re:   regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)([^\s/:@]+):[^\s/@]+@`),
		repl: `${1}${2}:` + redactMarker + `@`,
	},
	{
		name: "authorization-bearer-header",
		re:   regexp.MustCompile(`(?i)\b(Authorization[ \t]*:[ \t]*Bearer[ \t]+)[^\s"']+`),
		repl: `${1}` + redactMarker,
	},
	{
		name: "github-fine-grained-token",
		re:   regexp.MustCompile(`github_pat_[0-9A-Za-z_]{40,}`),
		repl: redactMarker,
	},
	{
		name: "github-classic-token",
		re:   regexp.MustCompile(`(?:ghp|gho|ghs|ghr)_[0-9A-Za-z]{36,}`),
		repl: redactMarker,
	},
	{
		name: "aws-access-key-id",
		re:   regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
		repl: redactMarker,
	},
	{
		name: "credential-query-parameter",
		re:   regexp.MustCompile(`(?i)\b(token|password|secret|api_key|apikey|access_token)=[^\s&"']*`),
		repl: `${1}=` + redactMarker,
	},
	{
		name: "environment-value-dump",
		re:   regexp.MustCompile(`(?m)^([A-Z_]*(?:TOKEN|SECRET|PASSWORD|API_KEY)[A-Z_]*)=.*$`),
		repl: `${1}=` + redactMarker,
	},
}

// Redact applies the credential-redaction patterns to b and returns
// the redacted bytes. Redact expects ASCII input (run it after
// Sanitize) and never introduces a byte outside printable ASCII plus
// LF and TAB: replacements are fixed ASCII literals. Redact is
// deterministic, allocation-bounded by input length, and idempotent.
func Redact(b []byte) []byte {
	out := b
	for _, rule := range redactionRules {
		out = rule.re.ReplaceAll(out, []byte(rule.repl))
	}
	return out
}

// SanitizeAndRedact is the full capture-time pipeline: the ASCII byte
// contract first (Sanitize), then credential redaction on the
// compliant ASCII text (Redact). This is the function capture
// producers (cmd/cisanitize and its fallbacks) apply to every byte
// that enters a CI log.
func SanitizeAndRedact(b []byte) []byte {
	return Redact(Sanitize(b))
}
