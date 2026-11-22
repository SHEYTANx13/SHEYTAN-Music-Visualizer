#!/usr/bin/env bash
# emergency-report.sh - tested fallback for the collect-logs job.
#
# PRIORITY-A CONTRACT (record format): the final artifact contains ONLY
# WARNING|/ERROR| records in the stable schema, strict ASCII/LF. This
# fallback emits records for the authoritative conclusions and the
# collection failure itself - never raw logs, never metadata sections.
# It runs ONLY when cmd/ci-log-collector could not be built or hard-
# failed before writing the report; coverage is UNVERIFIED and the
# collecting job fails closed after the upload (the caller marks
# collector-fallback.used).
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

# Secret hygiene for the emergency path: scrub the common token/Bearer
# shapes before anything leaves the runner (same pattern classes as the
# internal redactors).
redact() {
  sed -E \
    -e 's/gh[pousr]_[A-Za-z0-9_]{20,}/***REDACTED***/g' \
    -e 's/github_pat_[A-Za-z0-9_]{20,}/***REDACTED***/g' \
    -e 's/([Bb]earer[ \t]+)[A-Za-z0-9._-]{16,}/\1***REDACTED***/g'
}

# emit_record <severity> <platform> <job> <step> <gate> <message...>
emit_record() {
  sev="$1"; plat="$2"; job="$3"; step="$4"; gate="$5"; shift 5
  msg="$(printf '%s\n' "$*" | redact | tr '\t\r\n' '   ' | cut -c1-512)"
  printf '%s|platform=%s|job=%s|step=%s|gate=%s|command=emergency-fallback|tool=emergency-fallback|source=NOT_PROVIDED_BY_TOOL|message=%s\n' \
    "$sev" "$plat" "$job" "$step" "$gate" "$msg"
}

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT

{
  emit_record ERROR diagnostics collect-logs report diagnostics \
    "emergency fallback report in use: the primary collector could not produce the report (reason: ${reason:-not provided}); consolidated coverage is UNVERIFIED and the collecting job fails closed"
  emit_record ERROR preflight preflight job preflight \
    "workflow conclusion: ${result_preflight}"
  emit_record ERROR windows-x64 build-windows job desktop-build \
    "workflow conclusion: ${result_windows}"
  emit_record ERROR linux-x64 build-linux job desktop-build \
    "workflow conclusion: ${result_linux}"
  if [ "$result_codeql" != "success" ] && [ "$result_codeql" != "unknown" ]; then
    emit_record WARNING codeql codeql-external job codeql-analysis \
      "declared CodeQL workflow conclusion: ${result_codeql} (independent workflow, informational here)"
  fi
  manifest_state="MISSING"
  if [ -f "$manifest" ]; then
    if [ -s "$manifest" ]; then manifest_state="PRESENT"; else manifest_state="EMPTY (0 bytes)"; fi
  fi
  case "$manifest_state" in
    "EMPTY (0 bytes)")
      emit_record ERROR diagnostics collect-logs log-source diagnostics \
        "run manifest EMPTY (0 bytes): the manifest writer failed after file creation and before writing content; emergency coverage cannot be reconciled against declared jobs" ;;
    MISSING)
      emit_record ERROR diagnostics collect-logs log-source diagnostics \
        "run manifest MISSING: the manifest step did not create it; emergency coverage cannot be reconciled against declared jobs" ;;
  esac
  if [ ! -d "$parts_root" ]; then
    emit_record ERROR diagnostics collect-logs log-source diagnostics \
      "parts root missing (${parts_root}): no per-job log parts were consolidated"
  fi
} > "$tmp"

mv "$tmp" "$out"
echo "emergency-report.sh: wrote $out ($(wc -c < "$out") bytes, record format)"
