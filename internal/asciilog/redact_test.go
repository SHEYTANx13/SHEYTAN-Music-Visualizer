package asciilog

import (
	"bytes"
	"strings"
	"testing"
)

// The shared fixture corpus below is mirrored BYTE FOR BYTE in
// scripts/ci/sanitize_bytes.py (--self-test) and exercised end-to-end by
// scripts/ci/tests/test-sanitizer-portable.sh, which asserts that the Go
// binary (cmd/cisanitize) and the Python fallback produce identical bytes
// for every fixture. Fake token shapes only: every secret here is a
// realistic-shaped fixture, never a live credential.

const fixtureAlpha36 = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789" // exactly 36 alnum

// fixtureCorpus is the canonical fixture table: name, raw input bytes,
// expected SanitizeAndRedact output bytes.
var fixtureCorpus = []struct {
	name string
	in   []byte
	want []byte
}{
	{
		name: "bom-ansi-crlf-separator-clamp",
		in:   []byte("\xef\xbb\xbf\x1b[31mERROR\x1b[0m: boom===\r\nnext line\n"),
		want: []byte("ERROR: boom==\nnext line\n"),
	},
	{
		name: "utf8-and-controls-degrade",
		in:   []byte("caf\xc3\xa9 \xe4\xb8\xad \x00\x7f\tkeep\n"),
		want: []byte("caf?? ??? ??\tkeep\n"),
	},
	{
		name: "github-classic-tokens",
		in:   []byte("classic=ghp_" + fixtureAlpha36 + " oauth=gho_" + fixtureAlpha36 + " server=ghs_" + fixtureAlpha36 + " refresh=ghr_" + fixtureAlpha36 + "\n"),
		want: []byte("classic=REDACTED oauth=REDACTED server=REDACTED refresh=REDACTED\n"),
	},
	{
		name: "github-fine-grained-token",
		in:   []byte("pat=github_pat_11ABCDEFGHIJKLMNOPQRSTUVWXYZ0_" + strings.Repeat("a", 59) + "\n"),
		want: []byte("pat=REDACTED\n"),
	},
	{
		name: "aws-access-key-ids",
		in:   []byte("key=AKIAIOSFODNN7EXAMPLE short=AKIA123 long=AKIAABCDEFGHIJKLMNOPQ\n"),
		want: []byte("key=REDACTED short=AKIA123 long=AKIAABCDEFGHIJKLMNOPQ\n"),
	},
	{
		name: "bearer-authorization-headers",
		in:   []byte("Authorization: Bearer sk-live-abc123XYZ\nauthorization:Bearer  tok2\nX-Header: Authorization: Bearer ab\n"),
		want: []byte("Authorization: Bearer REDACTED\nauthorization:Bearer  REDACTED\nX-Header: Authorization: Bearer REDACTED\n"),
	},
	{
		name: "url-credentials-and-query-params",
		in:   []byte("clone https://alice:s3cret@github.com/example/repo.git ok\npsql postgres://bob:pw@db.internal:5432/app?password=zz&sslmode=require\nplain https://example.com/no-creds-here untouched\n"),
		want: []byte("clone https://alice:REDACTED@github.com/example/repo.git ok\npsql postgres://bob:REDACTED@db.internal:5432/app?password=REDACTED&sslmode=require\nplain https://example.com/no-creds-here untouched\n"),
	},
	{
		name: "environment-value-dumps",
		in:   []byte("GITHUB_TOKEN=supersecretvalue\nLANG=en_US.UTF-8\nMY_DB_PASSWORD=hunter2\napi_key=abc123\nunrelated_key=keepme\nAWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI\n"),
		want: []byte("GITHUB_TOKEN=REDACTED\nLANG=en_US.UTF-8\nMY_DB_PASSWORD=REDACTED\napi_key=REDACTED\nunrelated_key=keepme\nAWS_SECRET_ACCESS_KEY=REDACTED\n"),
	},
	{
		// The capture.sh regression fixture shape (test-sanitizer-portable.sh):
		// a classic token embedded after "secret_" with no word boundary
		// before the prefix must still be redacted as a whole run.
		name: "embedded-token-without-word-boundary",
		in:   []byte("bad=\xef\xbb\xbf\x1b[31msecret_ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abcdefgh===x\r\n"),
		want: []byte("bad=???secret_REDACTED==x\n"),
	},
	{
		name: "ordinary-log-lines-unchanged",
		in:   []byte("STEP: go-test\nEXIT_CODE: 0 PASS\n2026/11/14 12:00:00 server listening on :8080\npackage github.com/SHEYTANx13/SHEYTAN-Music-Visualizer\nworkdir token count: 42\n"),
		want: []byte("STEP: go-test\nEXIT_CODE: 0 PASS\n2026/11/14 12:00:00 server listening on :8080\npackage github.com/SHEYTANx13/SHEYTAN-Music-Visualizer\nworkdir token count: 42\n"),
	},
	{
		// Mirror of the verification pipeline fixture: ESC + CSI + "===" +
		// CRLF + a 44-character classic token on its own line.
		name: "verify-pipeline-fixture",
		in:   []byte("x\x1b[31my===z\r\nghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abcdefgh\n"),
		want: []byte("xy==z\nREDACTED\n"),
	},
}

// TestSanitizeAndRedactCorpus drives the whole shared fixture corpus
// through SanitizeAndRedact and checks the exact expected bytes.
func TestSanitizeAndRedactCorpus(t *testing.T) {
	for _, tc := range fixtureCorpus {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeAndRedact(tc.in)
			if !bytes.Equal(got, tc.want) {
				t.Fatalf("sanitize+redact mismatch for %s:\n got %q\nwant %q", tc.name, got, tc.want)
			}
			if rep := Validate(got); rep.Violations() != 0 {
				t.Fatalf("output violates the ASCII contract: %+v", rep)
			}
		})
	}
}

// TestRedactAloneOnAsciiInput verifies the documented pattern behavior
// on input that is already ASCII (the state Redact is contractually
// applied in).
func TestRedactAloneOnAsciiInput(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "classic-token-run",
			in:   "ghp_" + fixtureAlpha36,
			want: "REDACTED",
		},
		{
			name: "short-run-is-conservative-no-match",
			in:   "ghp_SHORT123", // 8 trailing chars: not a realistic token run
			want: "ghp_SHORT123",
		},
		{
			name: "wrong-prefix-untouched",
			in:   "ghx_" + fixtureAlpha36,
			want: "ghx_" + fixtureAlpha36,
		},
		{
			name: "akia-too-short-untouched",
			in:   "id=AKIA123 no match",
			want: "id=AKIA123 no match",
		},
		{
			name: "akia-17-chars-untouched",
			in:   "id=AKIAABCDEFGHIJKLMNOPQ",
			want: "id=AKIAABCDEFGHIJKLMNOPQ",
		},
		{
			name: "akia-exact-shape",
			in:   "id=AKIAIOSFODNN7EXAMPLE",
			want: "id=REDACTED",
		},
		{
			name: "bearer-token",
			in:   "Authorization: Bearer tok-abc \"quoted next\"",
			want: "Authorization: Bearer REDACTED \"quoted next\"",
		},
		{
			name: "url-credentials",
			in:   "https://user:pw123@host.example/x",
			want: "https://user:REDACTED@host.example/x",
		},
		{
			name: "url-without-credentials-untouched",
			in:   "https://host.example/x",
			want: "https://host.example/x",
		},
		{
			name: "query-params-all-names",
			in:   "?token=a&password=b&secret=c&api_key=d&apikey=e&access_token=f&plain=g",
			want: "?token=REDACTED&password=REDACTED&secret=REDACTED&api_key=REDACTED&apikey=REDACTED&access_token=REDACTED&plain=g",
		},
		{
			name: "query-param-stops-at-quote",
			in:   "href=\"?token=abc123&next=1\"",
			want: "href=\"?token=REDACTED&next=1\"",
		},
		{
			name: "token-inside-larger-word-untouched",
			in:   "access_token_scopes=read,write",
			want: "access_token_scopes=read,write",
		},
		{
			name: "env-dump-line",
			in:   "CI_DEPLOY_PASSWORD=hunter2\nexport PATH=/usr/bin\n",
			want: "CI_DEPLOY_PASSWORD=REDACTED\nexport PATH=/usr/bin\n",
		},
		{
			name: "env-dump-lowercase-name-untouched-by-env-rule",
			in:   "deploy_password=hunter2",
			want: "deploy_password=hunter2",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := Redact([]byte(tc.in))
			if string(got) != tc.want {
				t.Fatalf("redact mismatch:\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}

// TestRedactDoesNotAlterOrdinaryLines proves the conservative contract:
// ordinary log lines (no credential shape) pass through byte-exact.
func TestRedactDoesNotAlterOrdinaryLines(t *testing.T) {
	ordinary := []byte("STEP: go-test\nEXIT_CODE: 0 PASS\npackage github.com/SHEYTANx13/SHEYTAN-Music-Visualizer\n2026/11/14 12:00:00 server listening on :8080\ngofmt clean\nunrelated_key=keepme\n")
	got := Redact(ordinary)
	if !bytes.Equal(got, ordinary) {
		t.Fatalf("ordinary log line altered:\n got %q\nwant %q", got, ordinary)
	}
	if len(got) != len(ordinary) {
		t.Fatalf("byte count changed on ordinary input: got %d want %d", len(got), len(ordinary))
	}
}

// TestRedactIdempotentAndNoDoubleRedaction proves idempotency and that
// the marker itself can never be re-parsed into another redaction.
func TestRedactIdempotentAndNoDoubleRedaction(t *testing.T) {
	gnarly := []byte(
		"\xef\xbb\xbf\x1b[31mERROR\x1b[0m: boom===\r\n" +
			"Authorization: Bearer sk-live-abc123XYZ\n" +
			"clone https://alice:s3cret@github.com/example/repo.git\n" +
			"classic=ghp_" + fixtureAlpha36 + "\n" +
			"GITHUB_TOKEN=supersecretvalue\n" +
			"key=AKIAIOSFODNN7EXAMPLE\n")
	once := SanitizeAndRedact(gnarly)
	twice := SanitizeAndRedact(once)
	third := Redact(twice)
	if !bytes.Equal(once, twice) {
		t.Fatalf("sanitize+redact is not idempotent:\n once %q\n twice %q", once, twice)
	}
	if !bytes.Equal(twice, third) {
		t.Fatalf("redact alone is not idempotent:\n twice %q\n third %q", twice, third)
	}
	if bytes.Count(twice, []byte(redactMarker)) != bytes.Count(once, []byte(redactMarker)) {
		t.Fatalf("marker count changed across passes (double redaction)")
	}
	// The redacted output must still contain no secret-shaped token.
	if bytes.Contains(twice, []byte("ghp_")) || bytes.Contains(twice, []byte("AKIA")) ||
		bytes.Contains(twice, []byte("s3cret")) || bytes.Contains(twice, []byte("hunter2")) {
		t.Fatalf("secret material survived redaction: %q", twice)
	}
}

// TestRedactByteCounts pins the documented byte counts for the corpus:
// redaction changes length only where a secret was replaced, and the
// sanitize-only classes keep their exact documented sizes.
func TestRedactByteCounts(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		wantBytes int
	}{
		{name: "classic-token-44-run", in: "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abcdefgh", wantBytes: len("REDACTED")},   // 48 -> 8
		{name: "classic-token-exact-36-run", in: "ghp_" + fixtureAlpha36, wantBytes: len("REDACTED")},                        // 40 -> 8
		{name: "akia-key", in: "id=AKIAIOSFODNN7EXAMPLE", wantBytes: len("id=") + len("REDACTED")},                           // 22 -> 11
		{name: "bearer-header", in: "Authorization: Bearer tok-abc", wantBytes: len("Authorization: Bearer REDACTED")},       // header shape kept
		{name: "url-creds", in: "https://user:pw123@host.example/x", wantBytes: len("https://user:REDACTED@host.example/x")}, // pw -> marker
		{name: "query-param", in: "x?access_token=abcdef", wantBytes: len("x?access_token=REDACTED")},                        // value -> marker
		{name: "env-dump", in: "MY_DB_PASSWORD=hunter2", wantBytes: len("MY_DB_PASSWORD=") + len("REDACTED")},                // value -> marker
		{name: "ordinary-line-byte-exact", in: "EXIT_CODE: 0 PASS", wantBytes: len("EXIT_CODE: 0 PASS")},                     // unchanged
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := Redact([]byte(tc.in))
			if len(got) != tc.wantBytes {
				t.Fatalf("byte count mismatch for %s: got %d want %d (%q)", tc.name, len(got), tc.wantBytes, got)
			}
		})
	}
	// Sanitize keeps UTF-8 degradation at exactly one '?' per byte.
	// U+00E9 is two bytes, U+4E2D is three bytes.
	if got, want := len(Sanitize([]byte("caf\xc3\xa9\xe4\xb8\xad"))), len("caf?????"); got != want {
		t.Fatalf("utf8 degradation byte count: got %d want %d", got, want)
	}
}
