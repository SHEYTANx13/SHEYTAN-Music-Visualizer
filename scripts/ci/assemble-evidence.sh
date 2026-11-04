#!/usr/bin/env bash
# assemble-evidence.sh — build the single consolidated evidence transcript
# evidence/phase1-build.log from all captured step logs.
#
# The transcript is a real record, not a narrative: it concatenates the
# captured per-command logs (which already contain, for every command:
# timestamp, working directory, command text, stdout+stderr from the first
# byte, real exit code, PASS/FAIL verdict and tool context) in deterministic
# order, wrapped in a run header and closed with a summary and failure index.
set -u

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

OUT="$ROOT/evidence/phase1-build.log"
LOGROOT="$ROOT/ci-logs"
JOB="${CI_JOB_NAME:-phase1-local}"

mkdir -p "$ROOT/evidence"
: > "$OUT"

{
  echo "=============================================================================="
  echo "SHEYTAN MUSIC VISUALIZER — PHASE 1 LOCAL VERIFICATION TRANSCRIPT"
  echo "=============================================================================="
  echo "product: SHEYTAN Music Visualizer (SHEYTAN™)"
  echo "phase: Phase 1 — Foundation"
  echo "generated (UTC): $(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "host: $(uname -s -m), kernel $(uname -r)"
  echo "go: $(go version 2>/dev/null || echo 'go not on PATH')"
  echo "gcc: $(gcc --version 2>/dev/null | head -1 || echo 'gcc not on PATH')"
  echo "git: $(git --version 2>/dev/null || echo 'git not on PATH')"
  echo "repo root: $ROOT"
  if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    echo "branch: $(git rev-parse --abbrev-ref HEAD 2>/dev/null)"
    echo "HEAD: $(git rev-parse HEAD 2>/dev/null)"
    echo "worktree: $(git status --porcelain | wc -l) modified/untracked path(s) at assembly time"
  else
    echo "git: not a worktree (extracted archive run)"
  fi
  echo "capture job: $JOB"
  echo "note: every command below was captured from the first byte with its real"
  echo "      exit code. Retries and earlier failures are preserved, never overwritten."
  echo "note: the delivered source archive is rebuilt after this log is assembled;"
  echo "      final archive integrity and extracted-tree results are reported in the"
  echo "      handoff note (note.md) and ai-notes.md."
  echo
} >> "$OUT"

order_file="$(mktemp)"
{
  ls "$LOGROOT/phase1-local"/*.log 2>/dev/null | sort
  ls "$LOGROOT/ladder"/*.log 2>/dev/null | sort
  ls "$LOGROOT/package"/*.log 2>/dev/null | sort
  if [ "$JOB" != "phase1-local" ] && [ "$JOB" != "ladder" ] && [ "$JOB" != "package" ]; then
    ls "$LOGROOT/$JOB"/*.log 2>/dev/null | sort
  fi
} > "$order_file"

while IFS= read -r f; do
  [ -f "$f" ] || continue
  {
    echo "------------------------------------------------------------------------------"
    echo "### LOG: ${f#"$ROOT"/}"
    echo "------------------------------------------------------------------------------"
    cat "$f"
    echo
  } >> "$OUT"
done < "$order_file"
rm -f "$order_file"

{
  echo
  echo "=============================================================================="
  echo "RUN SUMMARY"
  echo "=============================================================================="
  idx=0
  fails=0
  total=0
  for idxfile in "$LOGROOT/phase1-local/index.txt" "$LOGROOT/ladder/index.txt" "$LOGROOT/package/index.txt"; do
    [ -f "$idxfile" ] || continue
    while IFS='|' read -r job step logfile code verdict started ended; do
      [ -n "$job" ] || continue
      total=$((total+1))
      printf '%-34s exit=%-4s %-4s started=%s log=%s\n' "$step" "$code" "$verdict" "$started" "${logfile#"$ROOT"/}"
      [ "$verdict" = "FAIL" ] && fails=$((fails+1))
    done < "$idxfile"
  done
  echo
  echo "captured command records: $total"
  echo "failed command records:   $fails"
  if [ "$fails" -gt 0 ]; then
    echo
    echo "FAILURE INDEX:"
    for idxfile in "$LOGROOT/phase1-local/index.txt" "$LOGROOT/ladder/index.txt" "$LOGROOT/package/index.txt"; do
      [ -f "$idxfile" ] || continue
      awk -F'|' -v root="$ROOT" '$5 == "FAIL" { printf "  %-24s exit=%s log=%s\n", $2, $4, substr($3, length(root)+2) }' "$idxfile"
    done
  fi
  echo
  echo "END OF PHASE 1 VERIFICATION TRANSCRIPT"
} >> "$OUT"

echo "evidence assembled: ${OUT#"$ROOT"/} ($(wc -l < "$OUT") lines)"
