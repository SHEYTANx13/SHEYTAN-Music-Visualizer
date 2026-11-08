#!/usr/bin/env bash
# emergency-report.sh — tested fallback consolidated report for the
# collect-logs job (brief §9: "If collector binary compilation fails, a
# small tested fallback path must still write and attempt to upload a
# truthful emergency log.txt").
#
# This script runs ONLY when cmd/ci-log-collector could not be built or the
# collector hard-failed before writing ci-out/log.txt. It writes a
# deterministic, truthful plain-text report from the downloaded log parts
# and the run manifest — never invents conclusions, never marks a red run
# green. The calling workflow still fails closed afterwards (coverage is
# unverified in this path).
#
# Usage:
#   scripts/ci/emergency-report.sh --out ci-out/log.txt \
#       --parts-root ci-parts --manifest ci-parts/manifest.json \
#       --reason "<why the primary collector path failed>" \
#       [--result-preflight success] [--result-build-windows failure] ...
#
# Exit codes: 0 report written; 2 could not write (upload step then fails).
set -Eeuo pipefail

out="" parts_root="" manifest="" reason=""
result_preflight="${RESULT_PREFLIGHT:-unknown}"
result_windows="${RESULT_WINDOWS:-unknown}"
result_linux="${RESULT_LINUX:-unknown}"
result_codeql="${RESULT_CODEQL:-unknown}"

while [ $# -gt 0 ]; do
  case "$1" in
    --out) out="$2"; shift 2 ;;
    --parts-root) parts_root="$2"; shift 2 ;;
    --manifest) manifest="$2"; shift 2 ;;
    --reason) reason="$2"; shift 2 ;;
    --result-preflight) result_preflight="$2"; shift 2 ;;
    --result-build-windows) result_windows="$2"; shift 2 ;;
    --result-build-linux) result_linux="$2"; shift 2 ;;
    --result-codeql) result_codeql="$2"; shift 2 ;;
    *) echo "emergency-report.sh: unknown argument: $1" >&2; exit 2 ;;
  esac
done

[ -n "$out" ] || { echo "emergency-report.sh: --out is required" >&2; exit 2; }
[ -n "$parts_root" ] || { echo "emergency-report.sh: --parts-root is required" >&2; exit 2; }
[ -n "$manifest" ] || { echo "emergency-report.sh: --manifest is required" >&2; exit 2; }
mkdir -p "$(dirname "$out")"

# Best-effort secret hygiene for the emergency path: the workflow never
# writes tokens into ci-logs, and this filter additionally scrubs the
# common token/Bearer shapes before anything leaves the runner.
redact() {
  sed -E \
    -e 's/gh[pousr]_[A-Za-z0-9_]{20,}/***REDACTED***/g' \
    -e 's/github_pat_[A-Za-z0-9_]{20,}/***REDACTED***/g' \
    -e 's/([Bb]earer[ \t]+)[A-Za-z0-9._-]{16,}/\1***REDACTED***/g'
}

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT

{
  echo "=============================================================="
  echo "SHEYTAN CONSOLIDATED RUN REPORT — EMERGENCY FALLBACK PATH"
  echo "=============================================================="
  echo
  echo "0. WHY THIS IS AN EMERGENCY REPORT"
  echo "The primary collector (cmd/ci-log-collector) could not produce the"
  echo "report for this run. Reason: ${reason:-not provided}."
  echo "This fallback report is written by scripts/ci/emergency-report.sh so"
  echo "the run still has consolidated evidence. Coverage in this path is"
  echo "UNVERIFIED: the collecting job fails closed after the upload."
  echo
  echo "1. RUN METADATA (from the run manifest as written by the workflow)"
  echo "--------------------------------------------------------------"
  # Precise diagnosis (run-42376176493 phase 2.4): "jq unavailable",
  # "manifest missing", "manifest empty (0 bytes)" and "manifest present
  # but not valid JSON" are four different causes and are reported as
  # such — never combined into one ambiguous message.
  manifest_state="MISSING"
  if [ -f "$manifest" ]; then
    if [ -s "$manifest" ]; then manifest_state="PRESENT"; else manifest_state="EMPTY (0 bytes)"; fi
  fi
  echo "manifest_path: $manifest"
  echo "manifest_state: $manifest_state"
  echo "jq_available: $(command -v jq >/dev/null 2>&1 && echo yes || echo no)"
  if [ "$manifest_state" = "PRESENT" ] && command -v jq >/dev/null 2>&1; then
    if jq -r '"Product:          \(.product // "-")",
           "Repository:       \(.repository // "-")",
           "Workflow:         \(.workflow // "-")",
           "Run ID:           \(.run_id // "-")",
           "Attempt:          \(.attempt // "-")",
           "Commit SHA:       \(.commit_sha // "-")",
           "Ref:              \(.ref // "-")",
           "Event:            \(.event // "-")",
           "Start time (UTC): \(.start_time_utc // "-")"' "$manifest" 2>/dev/null; then
      :
    else
      echo "(manifest is present but is NOT valid JSON — raw manifest follows)"
      cat "$manifest"
    fi
  elif [ "$manifest_state" = "PRESENT" ]; then
    echo "(jq unavailable — raw manifest follows)"
    cat "$manifest"
  elif [ "$manifest_state" = "EMPTY (0 bytes)" ]; then
    echo "MANIFEST FILE EMPTY (0 bytes): the manifest writer failed after file creation and before writing content."
    echo "Run 42376176493 signature class: a jq-only writer leaves an empty file behind when jq is unavailable."
    echo "The repaired writer (scripts/ci/write-manifest.py) writes atomically and cannot leave this state."
  else
    echo "MANIFEST FILE MISSING: the manifest step did not create it."
  fi
  echo
  echo "2. JOB CONCLUSIONS (authoritative: workflow needs.*.result from the manifest;"
  echo "   the per-job results below are exactly what the calling workflow observed)"
  echo "--------------------------------------------------------------"
  if [ "$manifest_state" = "PRESENT" ] && command -v jq >/dev/null 2>&1; then
    if ! jq -r '.jobs[]? | "\(.name // "-") : \(.result // "-")\n   artifact: \(.artifact // "-")  required: \(.required // false)"' \
      "$manifest" 2>/dev/null; then
      echo "(manifest present but not valid JSON — env-reported results shown below)"
      echo "preflight      : $result_preflight"
      echo "build-windows  : $result_windows"
      echo "build-linux    : $result_linux"
      echo "codeql (call)  : $result_codeql"
    fi
  else
    echo "preflight      : $result_preflight"
    echo "build-windows  : $result_windows"
    echo "build-linux    : $result_linux"
    echo "codeql (call)  : $result_codeql"
    echo "(manifest state: $manifest_state; jq available: $(command -v jq >/dev/null 2>&1 && echo yes || echo no) — env-reported results shown above)"
  fi
  echo
  echo "CodeQL declared result: ${RESULT_CODEQL:-unknown}"
  echo
  echo "OVERALL RESULT: FAIL (emergency fallback path; the run cannot be"
  echo "reported green because consolidated coverage was not verified by the"
  echo "primary collector)."
  echo
  echo "3. CAPTURED LOG PARTS (verbatim, deterministic order, secrets redacted)"
  echo "--------------------------------------------------------------"
  if [ ! -d "$parts_root" ]; then
    echo "PARTS ROOT MISSING: $parts_root (no log parts were consolidated)"
  else
    # Deterministic: sort part directories by name, then files by path.
    while IFS= read -r f; do
      echo
      echo "#### PART FILE: ${f#"$parts_root"/}"
      echo "#### LOG CONTENT BEGIN"
      redact < "$f" || true
      echo "#### LOG CONTENT END"
    done < <(find "$parts_root" -type f -print | LC_ALL=C sort)
  fi
  echo
  echo "=============================================================="
  echo "END OF EMERGENCY CONSOLIDATED RUN REPORT"
  echo "=============================================================="
} > "$tmp"

mv "$tmp" "$out"
echo "emergency-report.sh: wrote $out ($(wc -c < "$out") bytes)"
