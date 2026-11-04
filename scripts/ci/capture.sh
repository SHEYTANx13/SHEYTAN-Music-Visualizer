#!/usr/bin/env bash
# capture.sh — first-byte command capture for SHEYTAN Music Visualizer
# build and CI logs. Adapted from the proven SHEYTAN-LA contract.
#
# Usage:
#   capture.sh <job> <step-name> <logfile> -- <command...>
#
# Optional environment:
#   CAPTURE_INDEX  path of a summary index file; one line is appended per
#                  captured command: JOB|STEP|logfile|EXIT|VERDICT|STARTED|ENDED
#
# Contract:
#   - stdout AND stderr are captured from the first byte (tee, pipefail).
#   - The original exit code is preserved and propagated — never swallowed.
#   - A structured record block is appended to the log file:
#       STARTED / EXIT_CODE <n> PASS|FAIL / ENDED, plus command + workdir.
#   - The caller's `set -e` still fires on FAIL because this helper exits
#     with the command's own status. Never wrap the captured command in
#     `|| true`; record the failure and return a non-zero aggregate instead.
set -uo pipefail

job="${1:?capture.sh: job name required}"
step="${2:?capture.sh: step name required}"
logfile="${3:?capture.sh: log path required}"
shift 3
[ "${1:-}" = "--" ] && shift
[ $# -gt 0 ] || { echo "capture.sh: no command given" >&2; exit 64; }

mkdir -p "$(dirname "$logfile")"

start_utc="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
{
  echo "=== COMMAND RECORD ==="
  echo "JOB: $job"
  echo "STEP: $step"
  echo "STARTED: $start_utc"
  echo "WORKDIR: $PWD"
  echo "COMMAND: $*"
} >>"$logfile"

"$@" > >(tee -a "$logfile") 2> >(tee -a "$logfile" >&2)
status=$?

end_utc="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

ver=""
if command -v go >/dev/null 2>&1; then
  ver="go $(go version 2>/dev/null | awk '{print $3}')"
fi
if command -v gcc >/dev/null 2>&1; then
  ver="$ver; gcc $(gcc -dumpversion 2>/dev/null)"
fi
if command -v pkg-config >/dev/null 2>&1; then
  ver="$ver; pkg-config $(pkg-config --version 2>/dev/null)"
fi
[ -n "$ver" ] || ver="system tools"

verdict="FAIL"
[ "$status" -eq 0 ] && verdict="PASS"

{
  echo "EXIT_CODE: $status $verdict"
  echo "ENDED: $end_utc"
  echo "TOOLCONTEXT: $ver"
  echo "=== END COMMAND RECORD ==="
} >>"$logfile"

if [ -n "${CAPTURE_INDEX:-}" ]; then
  echo "$job|$step|$logfile|$status|$verdict|$start_utc|$end_utc" >>"$CAPTURE_INDEX"
fi

exit $status
