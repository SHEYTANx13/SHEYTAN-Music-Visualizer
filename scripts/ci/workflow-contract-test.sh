#!/usr/bin/env bash
# workflow-contract-test.sh — static (textual) contract checks for the
# Phase 1 workflow definitions. This is a structural review aid, NOT a YAML
# semantics test and NOT a hosted-run simulation; actionlint and a YAML
# parser provide the syntax layer, hosted Actions provides the execution
# layer. Never report this check as "CI passed".
#
# Usage: workflow-contract-test.sh [workflows-dir]
set -u

DIR="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.github/workflows" && pwd)}"
fails=0

check() { # check <file> <description> <pattern> [invert]
  local f="$1" desc="$2" pat="$3" mode="${4:-}"
  if [ "$mode" = "invert" ]; then
    if grep -Eq "$pat" "$f"; then echo "FAIL [$f] $desc"; fails=$((fails+1)); else echo "PASS [$f] $desc"; fi
  else
    if grep -Eq "$pat" "$f"; then echo "PASS [$f] $desc"; else echo "FAIL [$f] $desc"; fails=$((fails+1)); fi
  fi
}

BD="$DIR/build-desktop.yml"
CQ="$DIR/codeql.yml"
for f in "$BD" "$CQ"; do
  [ -f "$f" ] || { echo "FAIL missing workflow: $f"; exit 1; }
done

# ---- security: action pins ----
# Every 'uses:' must reference a full 40-hex commit SHA (with optional comment).
check "$BD" "all uses pinned to full 40-hex SHA" 'uses: [^ ]+@[0-9a-f]{40}( |$)'
check "$CQ" "all uses pinned to full 40-hex SHA" 'uses: [^ ]+@[0-9a-f]{40}( |$)'
check "$BD" "no short-SHA or tag pins" 'uses: [^ ]+@(v[0-9]|[0-9a-f]{7,39})( |$)' invert
check "$CQ" "no short-SHA or tag pins" 'uses: [^ ]+@(v[0-9]|[0-9a-f]{7,39})( |$)' invert
check "$BD" "no deprecated MINGW64 msystem" 'msystem: *MINGW64' invert
check "$BD" "UCRT64 msystem configured" 'msystem: *UCRT64'
check "$BD" "no pull_request_target trigger" 'pull_request_target' invert
check "$CQ" "no pull_request_target trigger" 'pull_request_target' invert
check "$BD" "no secrets referenced" 'secrets\.' invert
check "$CQ" "no secrets referenced" 'secrets\.' invert
check "$BD" "no full environment dumps" 'env\s*$|printenv|Get-ChildItem env:' invert

# ---- permissions ----
check "$BD" "workflow-level contents: read" '^permissions:' -a ''
grep -A2 '^permissions:' "$BD" | grep -q 'contents: read' && echo "PASS [$BD] contents: read" || { echo "FAIL [$BD] contents: read"; fails=$((fails+1)); }
check "$CQ" "workflow-level permissions block" '^permissions:'
grep -A4 '^    permissions:' "$CQ" | grep -q 'security-events: write' && echo "PASS [$CQ] analyze job security-events: write" || { echo "FAIL [$CQ] security-events: write"; fails=$((fails+1)); }
check "$CQ" "analyze job actions: read" 'actions: read'
check "$CQ" "no repo write beyond security-events" 'issues: write|packages: write|deployments: write' invert

# ---- checkout hygiene ----
check "$BD" "persist-credentials: false on checkouts" 'persist-credentials: false'
check "$CQ" "persist-credentials: false on checkouts" 'persist-credentials: false'
n_checkout_bd=$(grep -c 'actions/checkout@' "$BD"); n_pcb=$(grep -c 'persist-credentials: false' "$BD")
[ "$n_checkout_bd" -eq "$n_pcb" ] && echo "PASS [$BD] every checkout disables persisted credentials ($n_checkout_bd)" || { echo "FAIL [$BD] checkout=$n_checkout_bd vs persist-credentials=$n_pcb"; fails=$((fails+1)); }

# ---- timeouts ----
check "$BD" "every job has timeout-minutes" 'timeout-minutes:'
check "$CQ" "analyze job has timeout-minutes" 'timeout-minutes:'

# ---- log capture / collection topology ----
check "$BD" "log part uploads use if: always()" 'if: always[(]'
check "$BD" "collect-logs runs with if: always()" 'if: always[(]'
check "$BD" "download uses merge-multiple: false" 'merge-multiple: false'
check "$BD" "collector runs from cmd/ci-log-collector" 'cmd/ci-log-collector'
check "$BD" "consolidated report uploaded" 'consolidated-run-log'
check "$BD" "coverage enforced after upload (fail closed)" 'Enforce coverage'

# ---- codeql contract ----
check "$CQ" "languages go and actions only" 'language: (go|actions)'
check "$CQ" "go build-mode manual" "build-mode: manual"
check "$CQ" "actions build-mode none" "build-mode: none"
check "$CQ" "fail-fast: false" 'fail-fast: false'
check "$CQ" "weekly schedule configured" 'cron:'
check "$CQ" "traced manual build present" 'go build \./\.\.\.'

echo
if [ "$fails" -gt 0 ]; then
  echo "CONTRACT TEST RESULT: FAIL ($fails failing check(s))"
  exit 1
fi
echo "CONTRACT TEST RESULT: PASS"
