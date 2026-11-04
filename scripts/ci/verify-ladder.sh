#!/usr/bin/env bash
# verify-ladder.sh — the complete Phase 1 local verification ladder
# (mission contract §10). Every command is captured via scripts/ci/capture.sh
# into ci-logs/ladder/ (the Go build gates run via scripts/build/build-linux.sh
# into ci-logs/phase1-local/). At the end the full transcript is assembled
# into evidence/phase1-build.log.
#
# Exit code: 0 only if every required gate passed.
set -u

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

LOGDIR="$ROOT/ci-logs/ladder"
INDEX="$LOGDIR/index.txt"
mkdir -p "$LOGDIR"
: > "$INDEX"
export CAPTURE_INDEX="$INDEX"

CAPTURE="$ROOT/scripts/ci/capture.sh"
FAILURES=0

gate() { # gate <step> <logname> <command...>
  local step="$1" log="$LOGDIR/$2"
  shift 2
  if "$CAPTURE" ladder "$step" "$log" -- "$@"; then
    return 0
  fi
  FAILURES=$((FAILURES+1))
  return 1
}

echo "=== SHEYTAN Music Visualizer — Phase 1 verification ladder ==="

# 1. Go gates (gofmt, tidy+drift, test, vet, build, exe, version, self-check,
#    collector fixtures) — captured into ci-logs/phase1-local/.
gate "build-gates-linux" "$LOGDIR/10-build-gates.log" \
  env CI_JOB_NAME=phase1-local "$ROOT/scripts/build/build-linux.sh"

# 2. Collector CLI end-to-end fixtures.
gate "collector-e2e" "$LOGDIR/11-collector-e2e.log" \
  "$ROOT/scripts/ci/collector-fixture-e2e.sh" "$ROOT/ci-logs/ladder/collector-e2e-work"

# 3. Workflow validation — actionlint (if available), YAML parse, contracts.
if command -v actionlint >/dev/null 2>&1; then
  gate "actionlint" "$LOGDIR/12-actionlint.log" \
    bash -c 'actionlint .github/workflows/build-desktop.yml .github/workflows/codeql.yml && echo "actionlint: both workflows PASS"'
else
  echo "actionlint: NOT AVAILABLE (not installed on this host) — YAML parse + contract tests still run" | tee "$LOGDIR/12-actionlint.log"
fi

if command -v python3 >/dev/null 2>&1 && python3 -c 'import yaml' 2>/dev/null; then
  gate "yaml-parse" "$LOGDIR/13-yaml-parse.log" \
    python3 -c '
import yaml
for f in [".github/workflows/build-desktop.yml", ".github/workflows/codeql.yml"]:
    d = yaml.safe_load(open(f))
    assert "jobs" in d and d["jobs"], f
    print(f, "YAML parse OK; jobs:", list(d["jobs"].keys()))
'
else
  echo "yaml-parse: NOT AVAILABLE (python3/pyyaml missing)" | tee "$LOGDIR/13-yaml-parse.log"
fi

gate "workflow-contracts" "$LOGDIR/14-workflow-contracts.log" \
  "$ROOT/scripts/ci/workflow-contract-test.sh"

# 4. License consistency.
gate "license-consistency" "$LOGDIR/15-license-consistency.log" \
  bash -c '
set -e
[ -f license.md ] || { echo "license.md missing"; exit 1; }
count=$(ls LICENSE* license* 2>/dev/null | grep -vc "^license.md$" || true)
[ "$count" -eq 0 ] || { echo "second license file detected"; exit 1; }
grep -q "SHEYTAN Music Visualizer" license.md || { echo "license.md lacks product name"; exit 1; }
grep -q "Parsaetak Proprietary License v1.1" license.md || { echo "license.md lacks license name"; exit 1; }
grep -q "© 2024–2026 Parsaetak" license.md || { echo "license.md lacks copyright range"; exit 1; }
grep -q "https://github.com/Parsaetak" license.md || { echo "license.md lacks contact URL"; exit 1; }
grep -q "fyne.io/fyne/v2" license.md || { echo "license.md lacks Fyne notice"; exit 1; }
grep -qi "trademarks of Parsaetak" license.md || { echo "license.md lacks trademark notice"; exit 1; }
grep -q "This document is a licensing statement" license.md || { echo "license.md lacks not-legal-advice statement"; exit 1; }
echo "license.md: sole licensing authority, product identity, trademark, contact, third-party notices — PASS"
'

# 5. Document consistency (five docs, no placeholders, no markdown artifacts).
gate "docs-consistency" "$LOGDIR/16-docs-consistency.log" \
  bash -c '
set -e
for f in license.md readme.md agent.md roadmap.md ai-notes.md; do
  [ -f "$f" ] || { echo "missing required doc: $f"; exit 1; }
  if grep -nE "【|TODO|XXX|待补充" "$f"; then
    echo "placeholder/marker found in $f"; exit 1
  fi
done
grep -q "SHEYTAN Music Visualizer" readme.md && grep -q "SHEYTAN™" readme.md || { echo "readme identity missing"; exit 1; }
grep -q "Phase 2" roadmap.md || { echo "roadmap lacks phase boundary"; exit 1; }
grep -q "PLANNED" roadmap.md || { echo "roadmap lacks status vocabulary"; exit 1; }
grep -q "absolute precedence" agent.md || { echo "agent.md lacks authority rule"; exit 1; }
grep -q "ai-notes.md" agent.md || { echo "agent.md lacks ai-notes reference"; exit 1; }
echo "five root documents present and internally consistent — PASS"
'

# 6. Git hygiene.
if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  gate "git-diff-check" "$LOGDIR/17-git-diff-check.log" git diff --check
  gate "git-status" "$LOGDIR/18-git-status.log" \
    bash -c 'git status --short --branch; echo "---"; git log --oneline -5 2>/dev/null || echo "(no commits yet)"'
else
  echo "git checks: NOT APPLICABLE (no worktree; extracted archive run)" | tee "$LOGDIR/17-git-diff-check.log"
fi

# 7. GUI runtime under virtual display (Xvfb), if available and built.
if command -v Xvfb >/dev/null 2>&1 && [ -x "$ROOT/dist/sheytan-music-visualizer-linux-x64" ]; then
  gate "fyne-window-runtime-xvfb" "19-window-runtime.log" \
    bash -c '
set -u
(Xvfb :99 -screen 0 1280x720x24 >/dev/null 2>&1 &)
sleep 2
rm -f /tmp/app-run-ladder.pid
(DISPLAY=:99 ./dist/sheytan-music-visualizer-linux-x64 >/tmp/app-run-ladder.log 2>&1 & echo $! > /tmp/app-run-ladder.pid)
pid=""
for i in $(seq 1 30); do
  if [ -s /tmp/app-run-ladder.pid ]; then pid=$(cat /tmp/app-run-ladder.pid); break; fi
  sleep 1
done
if [ -z "$pid" ]; then echo "GUI RUNTIME: process never started"; exit 1; fi
sleep 10
if ! kill -0 "$pid" 2>/dev/null; then echo "GUI RUNTIME: process exited before 10s"; cat /tmp/app-run-ladder.log; exit 1; fi
echo "GUI RUNTIME: Fyne event loop ALIVE >=10s under Xvfb virtual display (pid $pid)"
echo "NOTE: host is headless; a physical-display runtime check was not possible and is NOT claimed."
kill -TERM "$pid"
sleep 2
if kill -0 "$pid" 2>/dev/null; then echo "SHUTDOWN: process still alive after SIGTERM"; exit 1; fi
echo "SHUTDOWN: clean exit after SIGTERM"
echo "--- captured app output (warnings expected on headless host) ---"
cat /tmp/app-run-ladder.log
pkill Xvfb 2>/dev/null
exit 0
'
else
  echo "fyne-window-runtime-xvfb: NOT RUN (Xvfb or executable unavailable)" | tee "$LOGDIR/19-window-runtime.log"
fi

# 8. Assemble the evidence transcript.
if "$ROOT/scripts/ci/assemble-evidence.sh"; then
  echo "evidence transcript assembled"
else
  FAILURES=$((FAILURES+1))
fi

echo
if [ "$FAILURES" -gt 0 ]; then
  echo "LADDER RESULT: FAIL ($FAILURES failing gate(s))"
  exit 1
fi
echo "LADDER RESULT: PASS"
