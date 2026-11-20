#!/usr/bin/env python3
"""sanitize_bytes.py - pure-Python bootstrap fallback sanitizer.

Part of the SHEYTAN build-and-runtime log ASCII contract (run
44462492262 repair). This script is used ONLY when the Go toolchain is
not yet available in a bootstrap context: the canonical implementation
is cmd/cisanitize (Go, package internal/asciilog), and capture.sh
resolves CI_SANITIZER_BIN first, then this script, then falls back to a
loud raw passthrough. sed is NEVER part of this contract: the previous
GNU-sed byte-class rewrite rejected BSD ("RE error: invalid character
range") and broke the whole macOS capture.

Contract (byte-exact mirror of internal/asciilog.Sanitize + Redact):

  Sanitize (in order):
    1. strip a leading UTF-8 BOM (EF BB BF);
    2. remove ANSI escape sequences (CSI, OSC, DCS/SOS/PM/APC and
       two-character escapes); a malformed sequence is consumed so no
       ESC-prefixed bytes survive;
    3. CR and CRLF normalize to LF;
    4. every byte >= 0x80 becomes '?';
    5. C0 control bytes other than LF and TAB, and DEL, become '?';
    6. runs of '=' or '-' longer than two clamp to exactly two, so the
       three-byte sequences "===" and "---" can never appear.

  Redact (applied AFTER sanitize, so the input is ASCII):
    1. URLs with embedded credentials: scheme://user:password@ keeps
       scheme and user, password becomes REDACTED;
    2. Authorization: Bearer <token> (case-insensitive) keeps the
       header shape, token becomes REDACTED;
    3. github_pat_ + >=40 token characters -> REDACTED;
    4. ghp_/gho_/ghs_/ghr_ + >=36 alphanumerics -> REDACTED;
    5. AKIA + exactly 16 digits/uppercase letters (word-bounded) ->
       REDACTED;
    6. token=, password=, secret=, api_key=, apikey=, access_token=
       (case-insensitive) keep the name, the value up to '&',
       whitespace or a quote becomes REDACTED;
    7. lines matching ^[A-Z_]*(TOKEN|SECRET|PASSWORD|API_KEY)[A-Z_]*=
       keep the name, the whole value becomes REDACTED.

  Every rule is a fixed point: re-applying the pipeline to its own
  output is a no-op (idempotent, no double redaction).

CLI:
  sanitize_bytes.py            stdin -> sanitized stdout (binary safe)
  sanitize_bytes.py --self-test
                               run the embedded fixture corpus (the
                               same fixtures as the Go tests in
                               internal/asciilog/redact_test.go and
                               cmd/cisanitize/main_test.go), print one
                               PASS line per fixture, exit 0 when all
                               pass, exit 1 on any mismatch.

Python 3 standard library only; no third-party imports.
"""

import re
import sys

MARKER = b"REDACTED"


# --------------------------------------------------------------------------
# Sanitize (byte rules, mirrors internal/asciilog.Sanitize exactly)
# --------------------------------------------------------------------------

def _skip_escape(data: bytes, i: int) -> int:
    """Consume one ANSI escape sequence starting at data[i] == ESC.

    Mirrors asciilog.skipEscape byte for byte, including greedy
    consumption of malformed sequences so no ESC-prefixed bytes survive.
    """
    if i + 1 >= len(data):
        return i + 1  # stray ESC at end of stream
    nxt = data[i + 1]
    if nxt == 0x5B:  # '[' CSI: parameters/intermediates then final byte
        j = i + 2
        while j < len(data) and 0x20 <= data[j] <= 0x3F:
            j += 1
        if j < len(data) and 0x40 <= data[j] <= 0x7E:
            return j + 1
        return j  # malformed: drop what was scanned
    if nxt == 0x5D:  # ']' OSC terminated by BEL or ESC \
        j = i + 2
        while j < len(data):
            if data[j] == 0x07:
                return j + 1
            if data[j] == 0x1B and j + 1 < len(data) and data[j + 1] == 0x5C:
                return j + 2
            j += 1
        return j  # unterminated OSC: consume to end
    if nxt in (0x50, 0x58, 0x5E, 0x5F):  # DCS/SOS/PM/APC terminated by ESC \
        j = i + 2
        while j < len(data):
            if data[j] == 0x1B and j + 1 < len(data) and data[j + 1] == 0x5C:
                return j + 2
            j += 1
        return j
    # Two-character escape (ESC F) or stray ESC.
    if 0x40 <= nxt <= 0x5F:
        return i + 2
    return i + 1


def sanitize(data: bytes) -> bytes:
    """Apply the byte-level ASCII log contract to data."""
    # Rule 1: strip a leading UTF-8 BOM.
    if data[:3] == b"\xef\xbb\xbf":
        data = data[3:]
    out = bytearray()
    i = 0
    n = len(data)
    while i < n:
        c = data[i]
        if c == 0x1B:  # Rule 2: ANSI escape sequences.
            i = _skip_escape(data, i)
            continue
        if c == 0x0D:  # Rule 3: CR and CRLF normalize to LF.
            out.append(0x0A)
            i += 1
            if i < n and data[i] == 0x0A:
                i += 1
            continue
        if c >= 0x80:  # Rule 4: non-ASCII degrades to '?'.
            out.append(0x3F)
            i += 1
            continue
        if (c < 0x20 and c != 0x0A and c != 0x09) or c == 0x7F:  # Rule 5.
            out.append(0x3F)
            i += 1
            continue
        out.append(c)
        i += 1
    return _clamp_runs(out)


def _clamp_runs(b: bytearray) -> bytes:
    """Rule 6: runs of '=' or '-' longer than two clamp to two."""
    out = bytearray()
    i = 0
    n = len(b)
    while i < n:
        c = b[i]
        if c == 0x3D or c == 0x2D:  # '=' or '-'
            j = i
            while j < n and b[j] == c:
                j += 1
            run = j - i
            if run >= 3:
                run = 2
            out.extend([c] * run)
            i = j
            continue
        out.append(c)
        i += 1
    return bytes(out)


# --------------------------------------------------------------------------
# Redact (mirrors internal/asciilog.Redact exactly, bytes + re module)
# --------------------------------------------------------------------------

# Rule order is fixed and mirrors redactionRules in internal/asciilog/
# redact.go. Every rule is a fixed point, so the pipeline is idempotent.
_REDACTION_RULES = [
    (
        "url-embedded-credentials",
        re.compile(rb"([A-Za-z][A-Za-z0-9+.\-]*://)([^\s/:@]+):[^\s/@]+@"),
        rb"\g<1>\g<2>:" + MARKER + b"@",
    ),
    (
        "authorization-bearer-header",
        re.compile(rb"(?i)\b(Authorization[ \t]*:[ \t]*Bearer[ \t]+)[^\s\"']+"),
        rb"\g<1>" + MARKER,
    ),
    (
        "github-fine-grained-token",
        re.compile(rb"github_pat_[0-9A-Za-z_]{40,}"),
        MARKER,
    ),
    (
        "github-classic-token",
        re.compile(rb"(?:ghp|gho|ghs|ghr)_[0-9A-Za-z]{36,}"),
        MARKER,
    ),
    (
        "aws-access-key-id",
        re.compile(rb"\bAKIA[0-9A-Z]{16}\b"),
        MARKER,
    ),
    (
        "credential-query-parameter",
        re.compile(rb"(?i)\b(token|password|secret|api_key|apikey|access_token)=[^\s&\"']*"),
        rb"\g<1>=" + MARKER,
    ),
    (
        "environment-value-dump",
        re.compile(rb"(?m)^([A-Z_]*(?:TOKEN|SECRET|PASSWORD|API_KEY)[A-Z_]*)=.*$"),
        rb"\g<1>=" + MARKER,
    ),
]


def redact(data: bytes) -> bytes:
    """Apply the credential-redaction patterns to ASCII data."""
    out = data
    for _name, pattern, repl in _REDACTION_RULES:
        out = pattern.sub(repl, out)
    return out


def sanitize_and_redact(data: bytes) -> bytes:
    """The full capture-time pipeline: Sanitize first, then Redact."""
    return redact(sanitize(data))


# --------------------------------------------------------------------------
# Embedded fixture corpus (identical to the Go test fixtures)
# --------------------------------------------------------------------------

ALPHA36 = b"ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"  # exactly 36 alnum

FIXTURES = [
    (
        "bom-ansi-crlf-separator-clamp",
        b"\xef\xbb\xbf\x1b[31mERROR\x1b[0m: boom===\r\nnext line\n",
        b"ERROR: boom==\nnext line\n",
    ),
    (
        "utf8-and-controls-degrade",
        b"caf\xc3\xa9 \xe4\xb8\xad \x00\x7f\tkeep\n",
        b"caf?? ??? ??\tkeep\n",
    ),
    (
        "github-classic-tokens",
        b"classic=ghp_" + ALPHA36 + b" oauth=gho_" + ALPHA36
        + b" server=ghs_" + ALPHA36 + b" refresh=ghr_" + ALPHA36 + b"\n",
        b"classic=REDACTED oauth=REDACTED server=REDACTED refresh=REDACTED\n",
    ),
    (
        "github-fine-grained-token",
        b"pat=github_pat_11ABCDEFGHIJKLMNOPQRSTUVWXYZ0_" + b"a" * 59 + b"\n",
        b"pat=REDACTED\n",
    ),
    (
        "aws-access-key-ids",
        b"key=AKIAIOSFODNN7EXAMPLE short=AKIA123 long=AKIAABCDEFGHIJKLMNOPQ\n",
        b"key=REDACTED short=AKIA123 long=AKIAABCDEFGHIJKLMNOPQ\n",
    ),
    (
        "bearer-authorization-headers",
        b"Authorization: Bearer sk-live-abc123XYZ\n"
        b"authorization:Bearer  tok2\n"
        b"X-Header: Authorization: Bearer ab\n",
        b"Authorization: Bearer REDACTED\n"
        b"authorization:Bearer  REDACTED\n"
        b"X-Header: Authorization: Bearer REDACTED\n",
    ),
    (
        "url-credentials-and-query-params",
        b"clone https://alice:s3cret@github.com/example/repo.git ok\n"
        b"psql postgres://bob:pw@db.internal:5432/app?password=zz&sslmode=require\n"
        b"plain https://example.com/no-creds-here untouched\n",
        b"clone https://alice:REDACTED@github.com/example/repo.git ok\n"
        b"psql postgres://bob:REDACTED@db.internal:5432/app?password=REDACTED&sslmode=require\n"
        b"plain https://example.com/no-creds-here untouched\n",
    ),
    (
        "environment-value-dumps",
        b"GITHUB_TOKEN=supersecretvalue\nLANG=en_US.UTF-8\n"
        b"MY_DB_PASSWORD=hunter2\napi_key=abc123\nunrelated_key=keepme\n"
        b"AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI\n",
        b"GITHUB_TOKEN=REDACTED\nLANG=en_US.UTF-8\n"
        b"MY_DB_PASSWORD=REDACTED\napi_key=REDACTED\nunrelated_key=keepme\n"
        b"AWS_SECRET_ACCESS_KEY=REDACTED\n",
    ),
    (
        # capture.sh regression fixture shape (test-sanitizer-portable.sh):
        # embedded token, no word boundary before the prefix; the mid-stream
        # BOM degrades to '?' because only a LEADING BOM is stripped.
        "embedded-token-without-word-boundary",
        b"bad=\xef\xbb\xbf\x1b[31msecret_ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abcdefgh===x\r\n",
        b"bad=???secret_REDACTED==x\n",
    ),
    (
        "ordinary-log-lines-unchanged",
        b"STEP: go-test\nEXIT_CODE: 0 PASS\n"
        b"2026/11/14 12:00:00 server listening on :8080\n"
        b"package github.com/SHEYTANx13/SHEYTAN-Music-Visualizer\n"
        b"workdir token count: 42\n",
        b"STEP: go-test\nEXIT_CODE: 0 PASS\n"
        b"2026/11/14 12:00:00 server listening on :8080\n"
        b"package github.com/SHEYTANx13/SHEYTAN-Music-Visualizer\n"
        b"workdir token count: 42\n",
    ),
    (
        "verify-pipeline-fixture",
        b"x\x1b[31my===z\r\nghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abcdefgh\n",
        b"xy==z\nREDACTED\n",
    ),
]


def self_test() -> int:
    """Run the embedded corpus plus idempotency checks; 0 = all pass."""
    failures = 0
    for name, raw, want in FIXTURES:
        got = sanitize_and_redact(raw)
        if got == want:
            sys.stdout.write("PASS    %-8s %s\n" % ("fixture", name))
        else:
            failures += 1
            sys.stdout.write(
                "FAIL    %-8s %s\n  got  %r\n  want %r\n" % ("fixture", name, got, want)
            )
    # Idempotency: re-applying the whole pipeline to its own output is a
    # no-op for every fixture (no double redaction).
    for name, raw, _want in FIXTURES:
        once = sanitize_and_redact(raw)
        twice = sanitize_and_redact(once)
        if once == twice:
            sys.stdout.write("PASS    %-8s %s\n" % ("idempotent", name))
        else:
            failures += 1
            sys.stdout.write(
                "FAIL    %-8s %s\n  once  %r\n  twice %r\n" % ("idempotent", name, once, twice)
            )
    # The marker itself must never be re-parsed into another redaction.
    marker_probe = b"token=REDACTED password=REDACTED\n"
    if sanitize_and_redact(marker_probe) == marker_probe:
        sys.stdout.write("PASS    %-8s %s\n" % ("idempotent", "marker-fixed-point"))
    else:
        failures += 1
        sys.stdout.write("FAIL    %-8s marker-fixed-point\n" % "idempotent")
    total = len(FIXTURES) * 2 + 1
    sys.stdout.write("self-test: %d checks, %d failure(s)\n" % (total, failures))
    return 0 if failures == 0 else 1


def main(argv) -> int:
    if "--self-test" in argv:
        return self_test()
    if len(argv) > 1:
        sys.stderr.write(
            "usage: sanitize_bytes.py [--self-test] <raw-input >compliant-output\n"
        )
        return 2
    data = sys.stdin.buffer.read()
    out = sanitize_and_redact(data)
    sys.stdout.buffer.write(out)
    sys.stdout.buffer.flush()
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
