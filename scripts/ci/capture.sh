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
  {
    printf -- '--- END OUTPUT ---\n'
    printf 'EXIT_CODE: %d %s\n' "$rc" "$verdict"
    printf 'ENDED: %s\n' "$ended"
    printf '===== COMMAND END =====\n'
  } >>"$CI_LOG_FILE"

  CI_CAPTURE_EXIT="$rc"
  return "$rc"
}
