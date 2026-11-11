#!/usr/bin/env bash
# ci_capture — Phase 1 command-capture helper (Bash side).
#
# Contract:
#   - Capture stdout AND stderr from the first byte into $CI_LOG_FILE.
#   - Preserve the ORIGINAL command exit code (never the exit code of
#     tee or of the capture machinery) and return it to the caller.
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
# Usage:
#   export CI_LOG_FILE=/abs/path/phase1-build.log   # before first call
#   ci_capture <JOB> <STEP> <command> [args...]
#   rc=$?   # original command exit code
#
# Optional: export CI_TOOL_CONTEXT="go1.27.3 / gcc 14.2" before a call
# to embed tool context in the record.

# _ci_json_str — emit a JSON string literal for one argument.
_ci_json_str() {
  printf '"'
  printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' -e 's/\t/\\t/g' | tr -d '\n\r'
  printf '"'
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
  local job="$1" step="$2"
  shift 2
  local workdir started ended rc verdict
  workdir="$(pwd)"
  started="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  {
    printf '\n===== COMMAND BEGIN =====\n'
    printf 'JOB: %s\n' "$job"
    printf 'STEP: %s\n' "$step"
    printf 'STARTED: %s\n' "$started"
    printf 'WORKDIR: %s\n' "$workdir"
    printf 'COMMAND: %s\n' "$*"
    printf 'TOOL_CONTEXT: %s\n' "${CI_TOOL_CONTEXT:--}"
    printf -- '--- OUTPUT (stdout+stderr from first byte) ---\n'
  } >>"$CI_LOG_FILE"

  set -o pipefail
  "$@" 2>&1 | tee -a "$CI_LOG_FILE"
  rc=${PIPESTATUS[0]}
  set +o pipefail

  ended="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  if [ "$rc" -eq 0 ]; then verdict="PASS"; else verdict="FAIL"; fi
  # Elapsed milliseconds (spec §4.1): derived from the epoch, immune to
  # date-format differences between runner images.
  elapsed_ms=$(( $(date -u '+%s') - $(date -u -d "$started" '+%s' 2>/dev/null || date -u '+%s') * 1 ))
  [ "$elapsed_ms" -lt 0 ] && elapsed_ms=0
  {
    printf -- '--- END OUTPUT ---\n'
    printf 'EXIT_CODE: %d %s\n' "$rc" "$verdict"
    printf 'ENDED: %s\n' "$ended"
    printf '===== COMMAND END =====\n'
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
