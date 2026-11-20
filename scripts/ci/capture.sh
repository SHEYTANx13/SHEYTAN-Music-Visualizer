#!/usr/bin/env bash
# ci_capture — Phase 1 command-capture helper (Bash side).
#
# Contract:
#   - Capture stdout AND stderr from the first byte into $CI_LOG_FILE.
#   - Preserve the ORIGINAL command exit code (never the exit code of
#     tee, of the sanitizer, or of the capture machinery) and return it
#     to the caller.
#   - Append a structured command record: JOB, STEP, STARTED, WORKDIR,
#     COMMAND, TOOL_CONTEXT, output, EXIT_CODE with PASS|FAIL verdict,
#     ENDED.
#   - Machine-readable gate record (run-42921967187 diagnostics
#     contract): every executed command also writes ONE
#     `<log-dir>/gate-results/<step>.result.json` record with the
#     command vector, working directory, UTC window, exit status and
#     verdict — trustworthy per-gate evidence that survives even when
#     later steps fail, and is parseable without text scraping. Every
#     NOT-executed gate has no record: absence is NOT PASS.
#   - The caller decides whether to continue after a failure; this
#     helper never masks a non-zero status (no `|| true` inside).
#
# Sanitizer contract (run 44462492262 repair): the build-log ASCII
# contract is implemented at CAPTURE TIME by exactly two producers and
# NEVER by sed:
#   1. cmd/cisanitize (Go, canonical; package internal/asciilog) —
#      point CI_SANITIZER_BIN at the built binary
#      (`go build -o "$CI_BIN/cisanitize" ./cmd/cisanitize`).
#   2. scripts/ci/sanitize_bytes.py (pure-Python bootstrap fallback;
#      used only when the Go toolchain is not yet available in a
#      bootstrap context; resolved relative to this file).
# The former GNU-sed byte-class rewrite that lived here was the macOS
# capture killer (run 44462492262: BSD sed rejects the `\x00-\x08`
# byte-class expressions with "RE error: invalid character range", so
# JOB/STEP/COMMAND records rendered empty and the capture exited 1/141).
# sed is therefore banned from this file: every remaining mention of
# sed below is this comment. ci_sanitize resolves the canonical binary
# first, then the Python fallback, and only then passes raw bytes
# through behind a loud marker so the strict validator fails the log
# (never silently, never masked).
#
# Usage:
#   export CI_LOG_FILE=/abs/path/phase1-build.log   # before first call
#   ci_capture <JOB> <STEP> <command> [args...]
#   rc=$?   # original command exit code
#
# Optional: export CI_SANITIZER_BIN=/abs/cisanitize before first call to
# pin the canonical Go sanitizer; export CI_TOOL_CONTEXT="go1.27.3 /
# gcc 14.2" before a call to embed tool context in the record.

# _CI_CAPTURE_DIR — absolute directory of this file, resolved at source
# time from BASH_SOURCE so the Python fallback script is found relative
# to capture.sh regardless of the caller's working directory.
_CI_CAPTURE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" 2>/dev/null && pwd)" || _CI_CAPTURE_DIR="."

# _ci_unavailable_marker — the loud fallback banner emitted when no
# sanitizer is available. It goes to stderr AND into the log so the
# failure is visible in the job console and in the uploaded evidence.
_ci_unavailable_marker() {
  printf '[ci_capture] SANITIZER UNAVAILABLE — raw output preserved; the strict validator will fail this log\n'
}

# ci_sanitize — streaming implementation of the build-log ASCII
# contract plus credential redaction. Byte rules (canonical Go
# implementation: internal/asciilog, applied by cmd/cisanitize): strip
# UTF-8 BOM; strip ANSI CSI/OSC sequences and leftover ESC bytes;
# normalize CR/CRLF to LF; map bytes at or above 0x80 to '?'; map C0
# controls (except LF/TAB) and DEL to '?'; clamp runs of '=' or '-' to
# two so the sequences "===" and "---" can never appear; then redact
# credential-shaped material (URL credentials, Bearer headers, GitHub
# tokens, AWS key IDs, credential query parameters, private
# environment value dumps) to REDACTED.
ci_sanitize() {
  if [ -n "${CI_SANITIZER_BIN:-}" ] && [ -x "${CI_SANITIZER_BIN:-}" ]; then
    "$CI_SANITIZER_BIN"
    return
  fi
  if command -v python3 >/dev/null 2>&1 && [ -f "$_CI_CAPTURE_DIR/sanitize_bytes.py" ]; then
    python3 "$_CI_CAPTURE_DIR/sanitize_bytes.py"
    return
  fi
  _ci_unavailable_marker >&2
  if [ -n "${CI_LOG_FILE:-}" ]; then
    _ci_unavailable_marker >>"$CI_LOG_FILE" 2>/dev/null
  fi
  cat
}

# ci_sanitize_str — one-line string variant for record header values
# and JSON gate-record fields.
ci_sanitize_str() {
  printf '%s' "$1" | ci_sanitize | tr -d '\n'
}

# _ci_json_str — emit a JSON string literal for one argument. Pure bash
# parameter expansion plus tr (the former sed escaping pipeline is
# banned from this file); escaping order matches the removed sed
# pipeline exactly: backslash first, then quote, then tab.
_ci_json_str() {
  local s
  s="${1//\\/\\\\}"
  s="${s//\"/\\\"}"
  s="${s//$'\t'/\\t}"
  printf '"%s"' "$(printf '%s' "$s" | tr -d '\n\r')"
}

ci_capture() {
  if [ "$#" -lt 3 ]; then
    echo "ci_capture: usage: ci_capture <JOB> <STEP> <command> [args...]" >&2
    CI_CAPTURE_EXIT=2
    return 2
  fi
  if [ -z "${CI_LOG_FILE:-}" ]; then
    echo "ci_capture: CI_LOG_FILE must be set to the run log path" >&2
    CI_CAPTURE_EXIT=2
    return 2
  fi
  local job step
  job="$(ci_sanitize_str "$1")"
  step="$(ci_sanitize_str "$2")"
  shift 2
  local workdir started ended started_epoch rc verdict
  workdir="$(pwd)"
  started="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  # Portable epoch anchor (run 44462492262 repair): elapsed time is
  # derived from plain UTC epoch seconds. The former
  # `date -u -d "$started" '+%s'` reverse-parse is GNU-only (BSD date
  # has no -d) and broke the macOS capture.
  started_epoch="$(date -u '+%s')"
  {
    printf '\n:: COMMAND BEGIN ::\n'
    printf 'JOB: %s\n' "$job"
    printf 'STEP: %s\n' "$step"
    printf 'STARTED: %s\n' "$started"
    printf 'WORKDIR: %s\n' "$workdir"
    # Run 44449832903 repair (A5 fixture): ci_sanitize_str takes its
    # input as a PARAMETER, not stdin. The former pipe form
    # `printf '%s' "$*" | ci_sanitize_str` called it with no argument,
    # so the COMMAND record line rendered empty on every host.
    printf 'COMMAND: %s\n' "$(ci_sanitize_str "$*")"
    printf 'TOOL_CONTEXT: %s\n' "$(ci_sanitize_str "${CI_TOOL_CONTEXT:--}")"
    printf ':: OUTPUT (stdout+stderr from first byte) ::\n'
  } >>"$CI_LOG_FILE"

  set -o pipefail
  "$@" 2>&1 | ci_sanitize | tee -a "$CI_LOG_FILE"
  rc=${PIPESTATUS[0]}
  set +o pipefail

  ended="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  if [ "$rc" -eq 0 ]; then verdict="PASS"; else verdict="FAIL"; fi
  # Elapsed milliseconds (spec §4.1): derived from the epoch anchors
  # above, portable across GNU and BSD date.
  elapsed_ms=$(( $(date -u '+%s') - started_epoch ))
  [ "$elapsed_ms" -lt 0 ] && elapsed_ms=0
  {
    printf ':: END OUTPUT ::\n'
    printf 'EXIT_CODE: %d %s\n' "$rc" "$verdict"
    printf 'ENDED: %s\n' "$ended"
    printf ':: COMMAND END ::\n'
  } >>"$CI_LOG_FILE"

  # Machine-readable per-gate record (run-42921967187 contract). A
  # write failure here must not mask the command's own exit code: the
  # record is evidence, not control flow.
  local result_dir result_file
  result_dir="$(dirname "$CI_LOG_FILE")/gate-results"
  result_file="$result_dir/${step}.result.json"
  if mkdir -p "$result_dir" 2>/dev/null; then
    {
      printf '{\n'
      printf '  "job": %s,\n' "$(_ci_json_str "$job")"
      printf '  "gate": %s,\n' "$(_ci_json_str "$step")"
      printf '  "tool_context": %s,\n' "$(_ci_json_str "${CI_TOOL_CONTEXT:--}")"
      printf '  "command": ['
      local first=1 a
      for a in "$@"; do
        if [ "$first" -eq 1 ]; then first=0; else printf ', '; fi
        printf '%s' "$(_ci_json_str "$a")"
      done
      printf '],\n'
      printf '  "workdir": %s,\n' "$(_ci_json_str "$workdir")"
      printf '  "log_file": %s,\n' "$(_ci_json_str "$CI_LOG_FILE")"
      printf '  "started_utc": %s,\n' "$(_ci_json_str "$started")"
      printf '  "ended_utc": %s,\n' "$(_ci_json_str "$ended")"
      printf '  "elapsed_ms": %d,\n' "$elapsed_ms"
      printf '  "run_id": %s,\n' "$(_ci_json_str "${GITHUB_RUN_ID:-local}")"
      printf '  "attempt": %s,\n' "$(_ci_json_str "${GITHUB_RUN_ATTEMPT:-1}")"
      printf '  "commit": %s,\n' "$(_ci_json_str "${SHEYTAN_BUILD_COMMIT:-${GITHUB_SHA:-unknown}}")"
      printf '  "platform": %s,\n' "$(_ci_json_str "${RUNNER_OS:-$(uname -s 2>/dev/null || echo unknown)}")"
      printf '  "exit_code": %d,\n' "$rc"
      printf '  "verdict": %s,\n' "$(_ci_json_str "$verdict")"
      printf '  "executed": true\n'
      printf '}\n'
    } >"$result_file" 2>/dev/null || rm -f "$result_file"
  fi

  # CI_CAPTURE_EXIT is the documented post-call contract variable (callers
  # may read it in addition to the return code).
  # shellcheck disable=SC2034
  CI_CAPTURE_EXIT="$rc"
  return "$rc"
}
