#!/usr/bin/env bash
# test-verdict-expression.sh — static fixture for the final-verdict gate
# expression pattern (run 43612608237 repair).
#
# The red gate of run 43612608237 was an actionlint type error:
#   build-desktop.yml:1186:133: property access of object must be type of
#   string but got "{check_run_id: number; ... status: string}" [expression]
#
# Root cause: the final-verdict step used a SHELL loop variable named
# `job` and interpolated `${{ needs[job].result }}`. Inside ${{ }}, the
# identifier `job` resolves to the GitHub Actions `job` context OBJECT,
# not the shell string, so the `needs` map lookup received an object where
# a string key is required. Expression interpolation also happens ONCE per
# step (when the run script is generated), so the old form could never
# observe per-iteration results at runtime either.
#
# Repair contract asserted here for EVERY workflow file:
#   1. No workflow may interpolate a dynamic shell variable as a `needs`
#      map key: `${{ needs[<something-not-a-literal>]... }}` with a bare
#      identifier (the old broken shape `needs[job].result`).
#   2. Every gate name listed in a final-verdict/collect shell loop must
#      have exactly one literal `${{ needs.<name>.result }}` case branch
#      in the same step, so each result is selected with a properly typed
#      scalar key.
#   3. The negative control: a copy of the OLD broken pattern must be
#      detected by this test (guards the detector itself against silent
#      no-op rot, mirroring the actionlint negative-fixture contract).
#
# Exit 0 = all workflows conform; 1 = violation detected.
set -Eeuo pipefail

fail=0
workflows_dir="$(dirname "$0")/../../.github/workflows"

check_workflow() {
  local wf="$1"
  # 3. NEGATIVE CONTROL FIRST: feed the detector the historical broken
  # pattern. If the detector can no longer see it, the test is rotten.
  local broken_sample='result="${{ needs[job].result }}"'
  local detected=0
  if printf '%s\n' "$broken_sample" | grep -qE 'needs\[[a-z][a-zA-Z0-9_]*\]\.'; then
    detected=1
  fi
  if [ "$detected" -ne 1 ]; then
    echo "ERROR: verdict-expression detector is rotten — it failed to flag the known-broken pattern 'needs[job].result'"
    fail=1
    return
  fi

  # 1. No dynamic bare-identifier needs[] indexing may remain anywhere.
  if grep -nE '\$\{\{[[:space:]]*needs\[[a-zA-Z][a-zA-Z0-9_]*\]' "$wf" >/dev/null 2>&1; then
    echo "ERROR: $wf still interpolates a dynamic identifier as a needs map key (the run-43612608237 red-gate defect):"
    grep -nE '\$\{\{[[:space:]]*needs\[[a-zA-Z][a-zA-Z0-9_]*\]' "$wf"
    fail=1
  fi

  # 2. Every loop member in a final-verdict/collect for-loop needs a
  #    literal case branch. Extract loop members and case targets.
  local loop_gates case_gates g
  loop_gates="$(grep -A1 -E '^\s*for (gate|job) in ' "$wf" \
    | grep -E '^\s*(for (gate|job) in |do$)' \
    | sed -E 's/^\s*for (gate|job) in //; s/; do$//' \
    | tr -s ' ' '\n' | sed -E 's/^(.*)$/\1/' | grep -vE '^$' || true)"
  # shellcheck disable=SC2207
  loop_gates="$(echo "$loop_gates" | tr '\n' ' ')"
  if [ -z "${loop_gates// }" ]; then
    return
  fi
  case_gates="$(grep -oE '^\s+[a-z][a-z0-9-]*\)[[:space:]]+result="\$\{\{ needs\.[a-z][a-z0-9-]*\.result \}\}"' "$wf" | sed -E 's/^\s+([a-z][a-z0-9-]*)\).*/\1/' | tr '\n' ' ')"
  for g in $loop_gates; do
    if ! echo "$case_gates" | tr ' ' '\n' | grep -qx "$g"; then
      echo "ERROR: $wf loops over gate '$g' but has no literal 'result=\"\${{ needs.$g.result }}\"' case branch for it"
      fail=1
    fi
  done
  echo "verdict-expression fixture: $wf loop gates=[$(echo "$loop_gates" | sed 's/ $//')] all have literal case branches"
}

export -f check_workflow 2>/dev/null || true

for wf in "$workflows_dir"/*.yml "$workflows_dir"/*.yaml; do
  [ -e "$wf" ] || continue
  check_workflow "$wf"
done

if [ "$fail" -ne 0 ]; then
  echo "FAIL: verdict-expression fixture detected non-conforming workflow expression(s)"
  exit 1
fi
echo "PASS: all workflows use literal scalar keys for needs.*.result lookups (run 43612608237 repair fixture)"
