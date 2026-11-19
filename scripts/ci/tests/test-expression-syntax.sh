#!/usr/bin/env bash
# test-expression-syntax.sh - workflow expression syntax and YAML parse
# gate (run 44449832903 repair).
#
# The hosted run 44449832903 failed BEFORE any job ran with three
# annotations against the then-active .github/workflows/build-desktop.yml:
#
#   line 1154, column 13: Unexpected symbol: '${'. Located at position 1
#     within expression: ${ always() && !cancelled() }
#   line 1163, column 13: same error.
#   line 1172, column 13: same error.
#
# Root cause: three collector download steps used the malformed
# single-brace form `if: ${ always() && !cancelled() }` instead of either
# the brace-free form `if: always()` or the exact `${{ expression }}`
# form. GitHub Actions parses the YAML successfully but then fails the
# whole workflow file when it evaluates the `if` expression, so a defect
# of this class is TOTAL: zero jobs run, nothing builds, nothing uploads.
#
# This gate makes the defect class impossible to reintroduce silently.
# For EVERY file in .github/workflows it asserts:
#   1. The file parses with a real YAML parser (PyYAML). A workflow that
#      cannot be parsed cannot run at all.
#   2. Every `if:` condition value (job level and step level) contains
#      no `${` that is not `${{` (the malformed single-brace form), and
#      every `${{` in an `if:` value has a matching `}}`.
#   3. In the canonical build workflow (build.yml) the central
#      collect-logs job-level condition is exactly `always()` - the
#      `&& !cancelled()` suffix must never return there, because it
#      would suppress the consolidated report after a user cancellation
#      (latest-update override section A).
#   4. NEGATIVE CONTROLS: the detector is fed the three historical
#      malformed strings plus an unbalanced `${{` sample and MUST flag
#      all of them. A detector that no longer sees the known-broken
#      patterns is rotten and fails this gate.
#
# Exit 0 = all workflows conform; 1 = violation detected.
set -Eeuo pipefail

fail=0
# Three levels up from scripts/ci/tests = repository root.
workflows_dir="$(cd "$(dirname "$0")/../../../.github/workflows" && pwd)"
if [ ! -d "$workflows_dir" ]; then
  echo "ERROR: workflows directory not found: $workflows_dir"
  exit 1
fi
build_workflow="$workflows_dir/build.yml"

# ---------------------------------------------------------------------------
# 4. NEGATIVE CONTROLS FIRST (rot guard): the detector must flag the
# historical run-44449832903 malformed strings and an unbalanced sample.
# ---------------------------------------------------------------------------
negative_detected=0
negative_total=4
while IFS= read -r sample; do
  if ! printf '%s\n' "$sample" | python3 scripts/ci/tests/expression_syntax_check.py --check-value >/dev/null 2>&1; then
    negative_detected=$((negative_detected + 1))
  else
    echo "ERROR: expression detector did NOT flag the known-broken sample: $sample"
    fail=1
  fi
done <<'NEGATIVES'
${ always() && !cancelled() }
${{ always() && !cancelled() }
if: ${ always() && !cancelled() }
${{ needs.build-windows.result
NEGATIVES
if [ "$negative_detected" -eq "$negative_total" ]; then
  echo "negative controls: $negative_detected/$negative_total known-broken expressions correctly rejected"
else
  echo "ERROR: negative controls incomplete ($negative_detected/$negative_total flagged)"
  fail=1
fi

# ---------------------------------------------------------------------------
# 1+2. YAML parse + if-value expression scan for every workflow file.
# ---------------------------------------------------------------------------
for wf in "$workflows_dir"/*.yml "$workflows_dir"/*.yaml; do
  [ -e "$wf" ] || continue
  if out="$(python3 scripts/ci/tests/expression_syntax_check.py --scan "$wf" 2>&1)"; then
    echo "expression syntax: OK ($(basename "$wf"))"
  else
    echo "ERROR: expression syntax violations in $wf:"
    printf '%s\n' "$out"
    fail=1
  fi
done

# ---------------------------------------------------------------------------
# 3. The canonical build workflow's central collector must use the plain
# always() job-level condition (no `&& !cancelled()`).
# ---------------------------------------------------------------------------
if [ ! -e "$build_workflow" ]; then
  echo "ERROR: canonical build workflow missing: $build_workflow"
  fail=1
else
  collector_if="$(python3 scripts/ci/tests/expression_syntax_check.py --job-if "$build_workflow" collect-logs 2>/dev/null || echo '')"
  if [ "$collector_if" != "always()" ]; then
    echo "ERROR: build.yml collect-logs job-level if is '$collector_if' - must be exactly 'always()' (run 44449832903 / override section A)"
    fail=1
  else
    echo "collect-logs job condition: always() (collector reports cancelled/missing evidence)"
  fi
fi

if [ "$fail" -ne 0 ]; then
  echo "FAIL: workflow expression syntax gate detected violation(s)"
  exit 1
fi
echo "PASS: all workflows parse, all if: conditions use valid expression syntax, collector uses always()"
