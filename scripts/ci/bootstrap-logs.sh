#!/usr/bin/env bash
# bootstrap-logs.sh — guaranteed CI log-directory bootstrap (bash side).
#
# This is THE fix for the primary failure of run 42026322748: the preflight
# job teed into ci-logs/runner-image.log before ci-logs/ existed, tee failed
# with "No such file or directory (os error 2)", the step went red, the next
# step (which contained the only mkdir) was skipped, and the always() artifact
# upload then failed with "No files were found with the provided path".
#
# Contract (scripts/ci logging contract, agent.md §6):
#   - EVERY evidence-producing job runs this immediately after checkout and
#     BEFORE any step that redirects into ci-logs/ (runner-image capture,
#     gate runners, CodeQL vet/build capture, collector evidence).
#   - Creates the whole directory layout up front and a NON-EMPTY
#     bootstrap.log, so every job's if:always() upload has a real file even
#     when a later setup step fails before any command can run.
#   - Records only allowlisted, non-secret GitHub/RUNNER context variables.
#   - Idempotent: re-running never truncates existing logs.
#   - Fails loudly (set -Eeuo pipefail) if the directory cannot be created;
#     a broken workspace must not masquerade as a healthy run.
set -Eeuo pipefail

LOG_DIR="ci-logs"

mkdir -p "$LOG_DIR" "$LOG_DIR/steps" "$LOG_DIR/app-logs" "$LOG_DIR/manifests"

# First bootstrap for this workspace: write the header. Re-runs append an
# idempotency line instead of truncating (the file must never end up empty).
if [ ! -s "$LOG_DIR/bootstrap.log" ]; then
  {
    printf 'log bootstrap: PASS\n'
    printf 'job=%s\n' "${GITHUB_JOB:-unknown}"
    printf 'run_id=%s\n' "${GITHUB_RUN_ID:-unknown}"
    printf 'run_attempt=%s\n' "${GITHUB_RUN_ATTEMPT:-unknown}"
    printf 'repository=%s\n' "${GITHUB_REPOSITORY:-unknown}"
    printf 'runner_os=%s\n' "${RUNNER_OS:-unknown}"
    printf 'runner_name=%s\n' "${RUNNER_NAME:-unknown}"
    printf 'image_os=%s\n' "${ImageOS:-unknown}"
    printf 'image_version=%s\n' "${ImageVersion:-unknown}"
    date -u '+bootstrap_utc=%Y-%m-%dT%H:%M:%SZ'
  } > "$LOG_DIR/bootstrap.log"
else
  printf 'log bootstrap: re-run (directories ensured; existing evidence preserved) %s\n' \
    "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" >> "$LOG_DIR/bootstrap.log"
fi

cat "$LOG_DIR/bootstrap.log"
