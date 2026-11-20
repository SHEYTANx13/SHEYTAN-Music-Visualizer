#!/usr/bin/env bash
# test-sanitizer-portable.sh — capture-time sanitizer regression fixture
# (run 44462492262 repair). Deterministic, no network, no credentials.
#
# Proves, on BOTH GNU and BSD userlands:
#   P1. scripts/ci/sanitize_bytes.py --self-test passes (exit 0): the
#       embedded fixture corpus (byte-identical to the Go test corpora in
#       internal/asciilog/redact_test.go and cmd/cisanitize/main_test.go)
#       and the idempotency checks hold.
#   P2. cmd/cisanitize builds (SKIP with a clear line when no Go
#       toolchain is available — the python path is still fully
#       exercised) and its output is BYTE-IDENTICAL to the python
#       fallback for every fixture in the corpus.
#   P3. The sanitized corpus passes the strict byte-level validator
#       (go run ./cmd/civalidate) when Go is available.
#   P4. scripts/ci/capture.sh — live contract: ci_capture with a
#       BOM+ANSI+CRLF+separator+embedded-secret producer, once through
#       the Go binary (CI_SANITIZER_BIN set) and once through the python
#       fallback (CI_SANITIZER_BIN unset): the log contains "hello" and
#       REDACTED, no ESC byte, no "===" and no CR; the gate-results JSON
#       exists with non-empty job/gate/command fields; CI_CAPTURE_EXIT=7
#       and the function returns 7 (the ORIGINAL exit code is preserved).
#   P5. capture.sh contains no sed invocation outside comments (the
#       GNU-sed byte classes broke the BSD macOS capture).
#
# Exit code 0 = every fixture PASS; nonzero = at least one FAILED.
# Follows the reporting conventions of scripts/ci/tests/test-ci-plumbing.sh.
set -u
# Analyzer notes: see .shellcheckrc in this directory. SC1091: capture.sh
# is sourced from the repo root resolved at runtime. SC2030/SC2031: the
# per-contract subshells deliberately isolate env changes.
cd "$(dirname "${BASH_SOURCE[0]}")/../../.." || exit 3   # repo root
REPO_ROOT="$PWD"
PASSED=0; FAILED=0

report() { # report <PASS|FAIL> <fixture-id> <detail>
  case "$1" in
    PASS) PASSED=$((PASSED+1)); printf 'PASS    %-8s %s\n' "$2" "$3" ;;
    FAIL) FAILED=$((FAILED+1)); printf 'FAIL    %-8s %s\n' "$2" "$3" ;;
  esac
}
skip() { printf 'SKIP    %-8s %s\n' "$1" "$2"; }

T="$(mktemp -d "${TMPDIR:-/tmp}/sheytan-sanitizer-portable.XXXXXX")" || exit 3
trap 'rm -rf "$T"' EXIT

esc=$(printf '\033')
cr=$(printf '\r')

# --------------------------------------------------------------------------
# P1: python fallback self-test (must exit 0).
# --------------------------------------------------------------------------
if python3 "$REPO_ROOT/scripts/ci/sanitize_bytes.py" --self-test >"$T/selftest.out" 2>&1; then
  report PASS "P1" "sanitize_bytes.py --self-test: $(grep -c '^PASS' "$T/selftest.out") checks passed"
else
  report FAIL "P1" "sanitize_bytes.py --self-test failed (tail below)"; tail -5 "$T/selftest.out"
fi

# --------------------------------------------------------------------------
# P2/P3: fixture corpus (BOM + ANSI + CRLF + UTF-8 + "===" + secrets),
# byte-identical outputs from BOTH implementations, civalidate-clean.
# --------------------------------------------------------------------------
corpus="$T/corpus"
mkdir -p "$corpus/raw" "$corpus/go" "$corpus/py" || exit 3
printf '\xef\xbb\xbf\x1b[31mhello===world\x1b[0m\r\n'                          > "$corpus/raw/f1-bom-ansi-crlf.bin"
printf 'caf\xc3\xa9 \xe4\xb8\xad\x00\x7f\tkeep\n'                              > "$corpus/raw/f2-utf8-controls.bin"
printf 'token=ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abcdefgh\n'              > "$corpus/raw/f3-github-token.bin"
printf 'Authorization: Bearer sk-live-fixture12345\n'                          > "$corpus/raw/f4-bearer.bin"
printf 'https://alice:s3cret@example.com/path?password=zz\n'                   > "$corpus/raw/f5-url-creds.bin"
printf 'id=AKIAIOSFODNN7EXAMPLE\nGITHUB_TOKEN=fixturevalue\n'                  > "$corpus/raw/f6-aws-env.bin"
printf 'bad=\xef\xbb\xbf\x1b[31msecret_ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abcdefgh===x\r\n' > "$corpus/raw/f7-embedded-token.bin"
printf 'plain step line\nEXIT_CODE: 0 PASS\n'                                  > "$corpus/raw/f8-ordinary.bin"

GO_BIN=""
if command -v go >/dev/null 2>&1; then
  if (cd "$REPO_ROOT" && go build -o "$T/bin/cisanitize" ./cmd/cisanitize) >"$T/go-build.out" 2>&1; then
    GO_BIN="$T/bin/cisanitize"
    report PASS "P2" "cmd/cisanitize built into the temp dir"
  else
    report FAIL "P2" "go build ./cmd/cisanitize failed"; head -5 "$T/go-build.out"
  fi
else
  skip "P2" "go toolchain unavailable on this host — the python fallback path is still fully exercised"
fi

for raw in "$corpus/raw"/*.bin; do
  name="$(basename "$raw" .bin)"
  if ! python3 "$REPO_ROOT/scripts/ci/sanitize_bytes.py" <"$raw" >"$corpus/py/$name.out" 2>"$T/py.err"; then
    report FAIL "P2" "python sanitize failed for $name: $(head -2 "$T/py.err")"
    continue
  fi
  # Contract invariants on the python output (always asserted).
  if grep -q "$esc" "$corpus/py/$name.out" || grep -q "$cr" "$corpus/py/$name.out" || grep -qF '===' "$corpus/py/$name.out"; then
    report FAIL "P2" "python output for $name carries ESC/CR/=== (contract violation)"
  fi
  if [ -n "$GO_BIN" ]; then
    if ! "$GO_BIN" <"$raw" >"$corpus/go/$name.out" 2>"$T/go.err"; then
      report FAIL "P2" "go sanitize failed for $name: $(head -2 "$T/go.err")"
      continue
    fi
    if cmp -s "$corpus/go/$name.out" "$corpus/py/$name.out"; then
      report PASS "P2" "byte-identical output (go == python) for $name"
    else
      report FAIL "P2" "go and python outputs differ for $name"
    fi
    if grep -q "$esc" "$corpus/go/$name.out" || grep -q "$cr" "$corpus/go/$name.out" || grep -qF '===' "$corpus/go/$name.out"; then
      report FAIL "P2" "go output for $name carries ESC/CR/=== (contract violation)"
    fi
  fi
done

if [ -n "$GO_BIN" ]; then
  if grep -q 'REDACTED' "$corpus/py/f3-github-token.out" \
     && grep -q 'REDACTED' "$corpus/py/f7-embedded-token.out" \
     && grep -q 'hello==world' "$corpus/py/f1-bom-ansi-crlf.out"; then
    report PASS "P2" "redaction and separator-clamp semantics present in sanitized corpus"
  else
    report FAIL "P2" "sanitized corpus lost REDACTED or clamp semantics"
  fi
  if (cd "$REPO_ROOT" && go run ./cmd/civalidate "$corpus/go" "$corpus/py") >"$T/civalidate.out" 2>&1; then
    report PASS "P3" "civalidate: $(tail -1 "$T/civalidate.out")"
  else
    report FAIL "P3" "sanitized corpus failed the strict validator"; tail -5 "$T/civalidate.out"
  fi
else
  skip "P3" "civalidate cross-check requires the Go toolchain"
fi

# --------------------------------------------------------------------------
# P4: live ci_capture contract through capture.sh — Go-binary path and
# python-fallback path, isolated per subshell (suite convention).
# --------------------------------------------------------------------------
run_capture_fixture() { # $1 = rc-file, $2 = log-file, $3 = extra export line
  (
    cd "$T" || exit 3
    mkdir -p ci-logs
    export CI_LOG_FILE="$2"
    # shellcheck source=../ci/capture.sh
    source "$REPO_ROOT/scripts/ci/capture.sh"
    eval "$3"
    ci_capture TESTJOB TESTSTEP bash -c 'echo hello; printf "bad=\xef\xbb\xbf\x1b[31m%s===x\r\n" secret_ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abcdefgh; exit 7'
    rc=$?
    printf 'rc=%d\nexit_var=%s\n' "$rc" "${CI_CAPTURE_EXIT:-unset}" > "$1"
  ) >"$T/capture-console-$4.log" 2>&1
}

assert_capture_log() { # $1 = log file, $2 = rc file, $3 = label
  local label="$3"
  if [ ! -f "$1" ]; then
    report FAIL "$label" "capture log missing: $1"
    return
  fi
  if grep -q 'hello' "$1" && grep -q 'REDACTED' "$1"; then
    report PASS "$label" "log carries hello + REDACTED (secret redacted at capture time)"
  else
    report FAIL "$label" "log lost 'hello' or REDACTED ($(wc -c <"$1") bytes)"
  fi
  if grep -q "$esc" "$1"; then
    report FAIL "$label" "log still carries an ESC byte"
  elif grep -qF '===' "$1"; then
    report FAIL "$label" "log still carries a '===' sequence"
  elif grep -q "$cr" "$1"; then
    report FAIL "$label" "log still carries a CR byte"
  else
    report PASS "$label" "no ESC byte, no '===', no CR in the log"
  fi
  if [ "$(cat "$2")" = "rc=7
exit_var=7" ]; then
    report PASS "$label" "original exit code preserved: return 7 and CI_CAPTURE_EXIT=7"
  else
    report FAIL "$label" "exit-code contract broken: $(tr '\n' ' ' <"$2")"
  fi
  if python3 - "$T/ci-logs/gate-results/TESTSTEP.result.json" <<'PYEOF'
import json, sys
d = json.load(open(sys.argv[1]))
assert d.get("job"), "job field empty"
assert d.get("gate"), "gate field empty"
assert isinstance(d.get("command"), list) and d["command"], "command field empty"
assert d.get("exit_code") == 7 and d.get("verdict") == "FAIL", d
PYEOF
  then
    report PASS "$label" "gate-results JSON present with non-empty job/gate/command"
  else
    report FAIL "$label" "gate-results JSON missing or fields empty"
  fi
}

run_capture_fixture "$T/go-path.rc" "$T/ci-logs/go-path.log" "export CI_SANITIZER_BIN=\"$T/bin/cisanitize\"" "go"
if [ -n "$GO_BIN" ]; then
  assert_capture_log "$T/ci-logs/go-path.log" "$T/go-path.rc" "P4-go"
else
  skip "P4-go" "go sanitizer unavailable — go-binary capture path skipped"
fi

run_capture_fixture "$T/py-path.rc" "$T/ci-logs/py-path.log" "unset CI_SANITIZER_BIN || true" "py"
assert_capture_log "$T/ci-logs/py-path.log" "$T/py-path.rc" "P4-python"

# --------------------------------------------------------------------------
# P5: capture.sh must contain no sed invocation outside comments.
# grep -n 'sed' hits must all be comment lines; an awk word-boundary scan
# (POSIX ERE, GNU and BSD) exempts substrings like elapsed_ms.
# --------------------------------------------------------------------------
noncomment_sed="$(grep -n 'sed' "$REPO_ROOT/scripts/ci/capture.sh" \
  | grep -Ev '^[0-9]+:[[:space:]]*#' \
  | awk '{ line=$0; sub(/^[0-9]+:/, "", line); if (line ~ /(^|[^A-Za-z0-9_])sed([^A-Za-z0-9_]|$)/) print }' || true)"
if [ -z "$noncomment_sed" ]; then
  report PASS "P5" "capture.sh contains no sed invocation outside comments"
else
  report FAIL "P5" "capture.sh still invokes sed outside comments:"; printf '%s\n' "$noncomment_sed"
fi

# --------------------------------------------------------------------------
# Summary.
# --------------------------------------------------------------------------
printf -- '--------------------------------------------------------------\n'
printf 'RESULT: PASS=%d FAIL=%d\n' "$PASSED" "$FAILED"
if [ "$FAILED" -ne 0 ]; then
  printf 'sanitizer-portable: %d fixture(s) FAILED\n' "$FAILED"
  exit 1
fi
exit 0
