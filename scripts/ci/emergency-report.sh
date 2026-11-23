#!/usr/bin/env bash
# emergency-report.sh - tested fallback for the collect-logs job.
#
# PRIORITY-A CONTRACT (record format): the final artifact contains ONLY
# WARNING|/ERROR| records in the stable schema (see internal/cilog/
# records.go), strict ASCII/LF. This fallback emits records for the
# authoritative conclusions and the collection failure itself - never
# raw logs, never metadata sections. It runs ONLY when
# cmd/ci-log-collector could not produce the report; coverage is
# UNVERIFIED and the collecting job fails closed after the upload (the
# caller marks collector-fallback.used).
#
# Run-45042834040 repair (profile-blind fallback): this script used to
# hardcode the build.yml job set (preflight/build-windows/build-linux)
# and emitted "workflow conclusion: unknown" ERROR records for jobs
# that DO NOT EXIST in the codeql workflow. Job-conclusion records are
# now derived from the run manifest's declared jobs (profile-correct by
# construction) and, only for jobs the manifest does not declare, from
# --result-* arguments the caller EXPLICITLY passed. Unpassed arguments
# no longer invent "unknown" records.
#
# Exit codes: 0 report written; 2 could not write (upload step then fails).
set -Eeuo pipefail

out="" parts_root="" manifest="" reason=""
declare -A passed_results=()

while [ $# -gt 0 ]; do
  case "$1" in
    --out) out="$2"; shift 2 ;;
    --parts-root) parts_root="$2"; shift 2 ;;
    --manifest) manifest="$2"; shift 2 ;;
    --reason) reason="$2"; shift 2 ;;
    --result-*)
      key="${1#--result-}"
      case "$key" in
        *[!A-Za-z0-9_-]*) echo "emergency-report.sh: invalid result key: $1" >&2; exit 2 ;;
      esac
      passed_results["$key"]="$2"
      shift 2 ;;
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

reason="$(printf '%s' "${reason:-not provided}" | redact | tr '\t\r\n' '   ' | cut -c1-512)"

manifest_state="PRESENT"
manifest_jobs_ok="no"
if [ ! -f "$manifest" ]; then
  manifest_state="MISSING"
elif [ ! -s "$manifest" ]; then
  manifest_state="EMPTY (0 bytes)"
elif command -v python3 >/dev/null 2>&1; then
  # python3 is guaranteed on hosted runners (both workflows fail closed
  # without it); this guard only keeps bare local runs alive.
  if python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); jobs=d["jobs"]; assert isinstance(jobs,list)' "$manifest" >/dev/null 2>&1; then
    manifest_jobs_ok="yes"
  fi
fi

# Job-conclusion rows (tab-separated: job, result, message). PRIMARY
# source: the run manifest's declared non-success jobs (profile-correct
# by construction). SECONDARY source: --result-* arguments the caller
# explicitly passed, used ONLY for jobs the manifest does not declare.
rows="$(mktemp)"
trap 'rm -f "$rows"' EXIT
if [ "$manifest_jobs_ok" = "yes" ]; then
  python3 - "$manifest" >> "$rows" <<'PYJOBS'
import json, sys

doc = json.load(open(sys.argv[1]))
for j in doc.get("jobs", []):
    name = str(j.get("name", ""))
    result = str(j.get("result", "unknown"))
    if not name or result == "success":
        continue
    msg = ("workflow conclusion: %s (declared job result at collection time; "
           "the full step evidence is in that job's own log)") % result
    print("M\t%s\t%s\t%s" % (name, result, msg))
PYJOBS
fi
for key in "${!passed_results[@]}"; do
  val="${passed_results[$key]}"
  [ "$val" = "success" ] && continue
  case "$key" in
    analyze-go) job="analyze_go" ;;
    analyze-actions) job="analyze_actions" ;;
    *) job="$key" ;;
  esac
  printf 'A\t%s\t%s\t%s\n' "$job" "$val" \
    "workflow conclusion: $val (explicitly passed conclusion argument; the manifest row was unreadable or absent)" >> "$rows"
done

python3 - \
  --manifest "$manifest" --rows "$rows" --state "$manifest_state" \
  --jobs-ok "$manifest_jobs_ok" --parts-root "$parts_root" --reason "$reason" <<'PYEMIT' > "$out"
# Single deterministic emitter for the emergency fallback report.
# Reads the manifest + tab-separated conclusion rows and prints ONLY
# WARNING|/ERROR| schema records (strict ASCII/LF, stable field order).
import argparse
import json
import os
import sys

ap = argparse.ArgumentParser()
ap.add_argument("--manifest", required=True)
ap.add_argument("--rows", required=True)
ap.add_argument("--state", required=True)
ap.add_argument("--jobs-ok", required=True)
ap.add_argument("--parts-root", required=True)
ap.add_argument("--reason", required=True)
args = ap.parse_args()


def ascii_clean(s, cap=2048):
    # Same normalization family as the Go collector: non-ASCII degrades
    # to '?', tabs/CR become spaces, separator runs clamp defensively.
    out = []
    run = 0
    prev = ""
    for ch in s:
        if ch in "\t\r\n":
            ch = " "
        o = ord(ch)
        if o < 0x20 or o > 0x7E:
            ch = "?"
        if ch == prev and ch in ("=", "-"):
            run += 1
            if run >= 3:
                continue
        else:
            run = 1
            prev = ch
        out.append(ch)
    return "".join(out)[:cap]


def rec(sev, plat, job, step, gate, hint, msg):
    fields = [sev, "platform=" + plat, "job=" + job, "step=" + step,
              "gate=" + gate, "command=emergency-fallback",
              "tool=emergency-fallback", "source=NOT_PROVIDED_BY_TOOL"]
    if hint:
        fields.append("hint=" + hint)
    fields.append("message=" + msg)
    return "|".join(fields)


lines = []

lines.append(rec(
    "ERROR", "diagnostics", "collect-logs", "report", "diagnostics",
    "collector-did-not-produce-report-inspect-collector-run-log",
    "emergency fallback report in use: the primary collector could not "
    "produce the report (reason: %s); consolidated coverage is UNVERIFIED "
    "and the collecting job fails closed" % args.reason))

# PRIMARY source: manifest-declared non-success jobs.
declared = {}
if args.jobs_ok == "yes":
    try:
        with open(args.manifest, "r", encoding="utf-8", errors="replace") as f:
            doc = json.load(f)
        for j in doc.get("jobs", []):
            declared[str(j.get("name", ""))] = str(j.get("result", "unknown"))
    except Exception:
        declared = {}

seen_jobs = set()
with open(args.rows, "r", encoding="utf-8", errors="replace") as f:
    for line in f:
        line = line.rstrip("\n")
        if not line:
            continue
        parts = line.split("\t")
        if len(parts) != 4:
            continue
        src, job, result, msg = parts
        if job in seen_jobs:
            continue
        if src == "A" and args.jobs_ok == "yes" and job in declared:
            # The manifest is authoritative when readable: every declared
            # job is already covered by its own M-row; the secondary
            # argument row for the same job is suppressed.
            continue
        if src == "M" and result == "success":
            continue  # defensive; the builder never emits success rows
        seen_jobs.add(job)
        plat = "unknown"
        low = job.lower()
        for key, val in (("windows", "windows-x64"), ("macos", "macos-arm64"),
                         ("darwin", "macos-arm64"), ("android", "android-arm64"),
                         ("codeql", "codeql"), ("analyze", "codeql"),
                         ("linux", "linux-x64"), ("preflight", "preflight"),
                         ("collect", "diagnostics")):
            if key in low:
                plat = val
                break
        lines.append(rec("ERROR", plat, job, "job", "diagnostics",
                         "required-job-did-not-succeed", ascii_clean(msg)))

# Manifest-state diagnosis (run-42376176493 signature classes).
if args.state == "EMPTY (0 bytes)":
    lines.append(rec(
        "ERROR", "diagnostics", "collect-logs", "log-source", "diagnostics",
        "manifest-empty-writer-failed-after-file-creation",
        "run manifest EMPTY (0 bytes): the manifest writer failed after file "
        "creation and before writing content (run 42376176493 signature "
        "class); emergency coverage cannot be reconciled against declared jobs"))
elif args.state == "MISSING":
    lines.append(rec(
        "ERROR", "diagnostics", "collect-logs", "log-source", "diagnostics",
        "manifest-missing-step-did-not-create-it",
        "run manifest MISSING: the manifest step did not create it (run "
        "42376176493 signature class); emergency coverage cannot be "
        "reconciled against declared jobs"))
elif args.jobs_ok != "yes":
    lines.append(rec(
        "WARNING", "diagnostics", "collect-logs", "log-source", "diagnostics",
        "manifest-unparseable-conclusions-from-arguments-only",
        "run manifest PRESENT but not parseable as JSON with a jobs array; "
        "job conclusions were taken from explicitly passed --result "
        "arguments only"))

if not os.path.isdir(args.parts_root):
    lines.append(rec(
        "ERROR", "diagnostics", "collect-logs", "log-source", "diagnostics",
        "parts-root-missing-no-parts-consolidated",
        "parts root missing (%s): no per-job log parts were consolidated"
        % args.parts_root))

sys.stdout.write("\n".join(lines) + "\n")
PYEMIT

echo "emergency-report.sh: wrote $out ($(wc -c < "$out") bytes, record format)"
