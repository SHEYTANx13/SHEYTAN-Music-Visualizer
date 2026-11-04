#!/usr/bin/env bash
# collector-fixture-e2e.sh — end-to-end CLI evidence for cmd/ci-log-collector.
#
# The Go package tests (internal/cilogreport) cover the report engine; this
# wrapper additionally proves the compiled CLI binary wires flags, manifests,
# parts roots and exit codes correctly on the real filesystem:
#   green case  -> exit 0, COMPLETE
#   red case    -> exit 1, INCOMPLETE, MISSING LOG SOURCE + NEVER_STARTED
#
# Usage: collector-fixture-e2e.sh [workdir]   (default: a fresh temp dir)
set -u

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="${1:-$(mktemp -d)}"
mkdir -p "$WORK"
cd "$ROOT"

echo "[e2e] workdir: $WORK"
echo "[e2e] building collector CLI..."
go build -o "$WORK/ci-log-collector" ./cmd/ci-log-collector || exit 2

fail=0

# ---------- green case ----------
GREEN="$WORK/green"
mkdir -p "$GREEN/parts/preflight-logs-part" "$GREEN/parts/windows-logs-part" "$GREEN/parts/linux-logs-part"
printf 'toolchain ok\n' > "$GREEN/parts/preflight-logs-part/preflight-build.log"
printf 'EXIT_CODE: 0 PASS\n' > "$GREEN/parts/windows-logs-part/windows-build.log"
printf 'built\n' > "$GREEN/parts/linux-logs-part/linux-build.log"
cat > "$GREEN/manifest.json" <<'JSON'
{
  "product": "SHEYTAN Music Visualizer",
  "workflow_file": ".github/workflows/build-desktop.yml",
  "run_id": "e2e-green",
  "attempt": "1",
  "commit": "e2e",
  "ref": "refs/heads/main",
  "event": "push",
  "started_at": "2026-11-04T00:00:00Z",
  "jobs": [
    {"name": "preflight", "result": "success", "expected_artifacts": ["preflight-logs-part"]},
    {"name": "build-windows", "result": "success", "expected_artifacts": ["windows-logs-part"]},
    {"name": "build-linux", "result": "success", "expected_artifacts": ["linux-logs-part"]}
  ]
}
JSON
"$WORK/ci-log-collector" -manifest "$GREEN/manifest.json" -parts-root "$GREEN/parts" -report "$GREEN/report.txt"
rc=$?
if [ $rc -eq 0 ] && grep -q "COVERAGE: COMPLETE" "$GREEN/report.txt"; then
  echo "[e2e] GREEN case: PASS (exit 0, COMPLETE)"
else
  echo "[e2e] GREEN case: FAIL (exit $rc)"; fail=1
fi

# ---------- red case: failed job + missing source + skipped job ----------
RED="$WORK/red"
mkdir -p "$RED/parts/preflight-logs-part"
printf 'EXIT_CODE: 2 FAIL\nERROR: go vet flagged something\n' > "$RED/parts/preflight-logs-part/preflight-tests.log"
cat > "$RED/manifest.json" <<'JSON'
{
  "product": "SHEYTAN Music Visualizer",
  "workflow_file": ".github/workflows/build-desktop.yml",
  "run_id": "e2e-red",
  "attempt": "1",
  "commit": "e2e",
  "ref": "refs/heads/main",
  "event": "push",
  "started_at": "2026-11-04T00:00:00Z",
  "jobs": [
    {"name": "preflight", "result": "failure", "expected_artifacts": ["preflight-logs-part"]},
    {"name": "build-windows", "result": "skipped", "expected_artifacts": ["windows-logs-part"]},
    {"name": "build-linux", "result": "success", "expected_artifacts": ["linux-logs-part"]}
  ]
}
JSON
set +e
"$WORK/ci-log-collector" -manifest "$RED/manifest.json" -parts-root "$RED/parts" -report "$RED/report.txt"
rc=$?
set -u
ok=0
[ $rc -eq 1 ] && ok=$((ok+1))
grep -q "COVERAGE: INCOMPLETE" "$RED/report.txt" && ok=$((ok+1))
grep -q "MISSING LOG SOURCE" "$RED/report.txt" && ok=$((ok+1))
grep -q 'job="build-linux"' "$RED/report.txt" && ok=$((ok+1))
grep -q "build-windows: SKIPPED / NEVER_STARTED" "$RED/report.txt" && ok=$((ok+1))
grep -q "EXIT_CODE: 2 FAIL" "$RED/report.txt" && ok=$((ok+1))
if [ $ok -eq 6 ]; then
  echo "[e2e] RED case: PASS (exit 1, INCOMPLETE, missing+skipped+failed all distinguished)"
else
  echo "[e2e] RED case: FAIL ($ok/6 assertions)"; fail=1
fi

echo "[e2e] result: $([ $fail -eq 0 ] && echo PASS || echo FAIL)"
exit $fail
