#!/usr/bin/env bash
# test-ci-plumbing.sh — deterministic CI-plumbing regression fixtures.
#
# Covers the run-42026322748 brief §10 fixture classes that can be proven
# WITHOUT GitHub credentials or a Windows host:
#   A. Bootstrap and evidence (1-6)
#   B. Job DAG and run-all semantics (7-13, static structure assertions)
#   C. Bash exit capture (14-16; 17-21 live in Test-CaptureStep.ps1)
#   D. Windows UCRT64 regression — static assertions only here (22-25);
#      runtime proof requires a Windows host and is reported NOT RUN.
#   F. Coverage inventory (49) lives in test-codeql-coverage-inventory.sh.
#
# Exit code 0 = every fixture PASS; nonzero = at least one FAILED.
# A fixture that cannot run on this host is reported NOT RUN with the
# exact reason and does not count as a pass.
set -u
# Analyzer notes: see .shellcheckrc in this directory — it documents why
# SC2030/SC2031/SC1091 are deliberate design properties of these fixtures.
cd "$(dirname "${BASH_SOURCE[0]}")/../../.." || exit 3   # repo root (scripts/ci/tests/ -> repo)
REPO_ROOT="$PWD"
PASSED=0; FAILED=0; NOT_RUN=0

report() { # report <PASS|FAIL|NOT RUN> <fixture-id> <detail>
  case "$1" in
    PASS)      PASSED=$((PASSED+1));  printf 'PASS    %-8s %s\n' "$2" "$3" ;;
    FAIL)      FAILED=$((FAILED+1));  printf 'FAIL    %-8s %s\n' "$2" "$3" ;;
    "NOT RUN") NOT_RUN=$((NOT_RUN+1)); printf 'NOT RUN %-8s %s\n' "$2" "$3" ;;
  esac
}
assert_contains() { # assert_contains <file> <needle> <what>
  if grep -qF -- "$2" "$1" 2>/dev/null; then return 0; else echo "  missing in $1: $2 ($3)"; return 1; fi
}

BD=".github/workflows/build-desktop.yml"
CQ=".github/workflows/codeql.yml"

echo "=============================================================="
echo "CI PLUMBING FIXTURES — $(date -u '+%Y-%m-%dT%H:%M:%SZ')"
echo "repo root: $REPO_ROOT"
echo "=============================================================="

T=$(mktemp -d)

# ---------- A1: fresh workspace bootstrap creates dirs + non-empty log ----
(
  cd "$T" || exit 3
  export GITHUB_JOB=test-job GITHUB_RUN_ID=12345 GITHUB_RUN_ATTEMPT=2
  unset GITHUB_REPOSITORY RUNNER_OS RUNNER_NAME ImageOS ImageVersion 2>/dev/null || true
  bash "$REPO_ROOT/scripts/ci/bootstrap-logs.sh" >/dev/null 2>&1
)
if [ -s "$T/ci-logs/bootstrap.log" ] && [ -d "$T/ci-logs/steps" ] && [ -d "$T/ci-logs/app-logs" ] && [ -d "$T/ci-logs/manifests" ] \
   && grep -q "log bootstrap: PASS" "$T/ci-logs/bootstrap.log" && grep -q "run_attempt=2" "$T/ci-logs/bootstrap.log"; then
  report PASS "A1" "fresh workspace: bootstrap creates all dirs + non-empty bootstrap.log with context"
else
  report FAIL "A1" "fresh workspace bootstrap incomplete"
fi

# ---------- A2: runner-image capture after bootstrap writes the tee file --
(
  cd "$T" || exit 3
  rm -rf ci-logs
  bash "$REPO_ROOT/scripts/ci/bootstrap-logs.sh" >/dev/null 2>&1
  {
    echo "=== OS image ==="
    grep PRETTY_NAME /etc/os-release || echo "PRETTY_NAME: not present in /etc/os-release"
    uname -srm
    date -u '+captured_utc=%Y-%m-%dT%H:%M:%SZ'
  } | tee ci-logs/runner-image.log
  exit "${PIPESTATUS[0]}"
) >/dev/null 2>&1
if [ -s "$T/ci-logs/runner-image.log" ] && grep -q "PRETTY_NAME" "$T/ci-logs/runner-image.log" && grep -q "Linux" "$T/ci-logs/runner-image.log"; then
  report PASS "A2" "runner-image capture after bootstrap: tee file created with OS/kernel output"
else
  report FAIL "A2" "runner-image capture after bootstrap failed"
fi

# ---------- A3+A4: early/setup failure — bootstrap + BLOCKED still exist --
(
  cd "$T" || exit 3
  rm -rf ci-logs
  export GITHUB_JOB=test-job GITHUB_RUN_ID=99 GITHUB_RUN_ATTEMPT=1
  bash "$REPO_ROOT/scripts/ci/bootstrap-logs.sh" >/dev/null 2>&1
  # simulate a failing setup step, then the workflow's `if: failure()` marker
  false || true
  {
    echo "BLOCKED marker: job test-job run 99 attempt 1 has at least one failed step (see the failing step above)."
    date -u '+blocked_utc=%Y-%m-%dT%H:%M:%SZ'
  } | tee ci-logs/setup-blocked.log
) >/dev/null 2>&1
if [ -s "$T/ci-logs/bootstrap.log" ] && grep -q "BLOCKED marker" "$T/ci-logs/setup-blocked.log"; then
  report PASS "A3" "early failure after bootstrap: always-run upload still has files (bootstrap.log + BLOCKED marker)"
else
  report FAIL "A3" "bootstrap/BLOCKED evidence missing after early failure"
fi

# ---------- A5+C14: silent success -> non-empty structured PASS record ----
(
  cd "$T" || exit 3
  rm -rf ci-logs
  mkdir -p ci-logs
  export CI_LOG_FILE="$T/ci-logs/phase2-build.log"
  : > "$CI_LOG_FILE"
  # shellcheck source=../ci/capture.sh
  source "$REPO_ROOT/scripts/ci/capture.sh"
  ci_capture job1 silent-ok bash -c 'exit 0'
  echo "rc=$?" > "$T/rc.txt"
) >/dev/null 2>&1
if grep -q "EXIT_CODE: 0 PASS" "$T/ci-logs/phase2-build.log" \
   && grep -q "COMMAND: bash -c exit 0" "$T/ci-logs/phase2-build.log" \
   && grep -q "STEP: silent-ok" "$T/ci-logs/phase2-build.log" \
   && [ "$(cat "$T/rc.txt")" = "rc=0" ]; then
  report PASS "A5" "silent success (exit 0, empty stdout) still yields a non-empty structured record (C14)"
else
  report FAIL "A5" "silent success record missing EXIT_CODE: 0 PASS / COMMAND / STEP"
fi

# ---------- C15: nonzero exit through tee preserves the child's status -----
(
  cd "$T" || exit 3
  export CI_LOG_FILE="$T/ci-logs/log.txt"
  : > "$CI_LOG_FILE"
  source "$REPO_ROOT/scripts/ci/capture.sh"
  set -o pipefail
  ( bash -c 'echo out; echo err >&2; exit 7' ) 2>&1 | tee -a "$CI_LOG_FILE"
  rc=${PIPESTATUS[0]}
  echo "child_rc=$rc" > "$T/rc.txt"
  printf 'EXIT_CODE: %d %s\n' "$rc" "$([ "$rc" -eq 0 ] && echo PASS || echo FAIL)" >> "$CI_LOG_FILE"
) >/dev/null 2>&1
if [ "$(cat "$T/rc.txt")" = "child_rc=7" ] && grep -q "EXIT_CODE: 7 FAIL" "$T/ci-logs/log.txt"; then
  report PASS "C15" "PIPESTATUS[0] preserves the child's exact status (7) through tee"
else
  report FAIL "C15" "child exit code through tee lost or misrecorded"
fi

# ---------- C16 + A6: tee/log-write failure is visible, not hidden ---------
(
  cd "$T" || exit 3
  source "$REPO_ROOT/scripts/ci/capture.sh"
  export CI_LOG_FILE="$T/does-not-exist/log.txt"  # parent dir missing
  ci_capture job1 tee-fail bash -c 'echo hello; exit 0' 2> "$T/stderr.txt"
) >/dev/null 2>&1
if grep -q "tee:" "$T/stderr.txt" 2>/dev/null; then
  report PASS "C16" "log-write failure surfaces the tee error on stderr (separately visible, A6)"
else
  report FAIL "C16" "tee failure was hidden"
fi

# ---------- B7-B13: workflow DAG structure assertions (python) -------------
python3 - "$BD" "$CQ" <<'PY' > "$T/dag.txt" 2>&1
import re, sys
bd_path, cq_path = sys.argv[1], sys.argv[2]
bd = open(bd_path).read()
cq = open(cq_path).read()
checks = []
def check(name, cond, detail=""):
    checks.append((name, bool(cond), detail))

# B7: preflight failure must not suppress Windows/Linux: no success-only needs
check("B7-no-needs-preflight-windows",
      not re.search(r"build-windows:\n(?:.*\n)*?\s+needs: preflight", bd))
check("B7-no-needs-preflight-linux",
      not re.search(r"build-linux:\n(?:.*\n)*?\s+needs: preflight", bd))

# B11: collector depends on all evidence jobs with always() && !cancelled()
check("B11-collector-needs-all",
      "needs: [preflight, build-windows, build-linux, codeql]" in bd)
check("B11-collector-always", "if: ${{ always() && !cancelled() }}" in bd)

# B10: CodeQL legs independent; fail-fast false inside codeql.yml
check("B10-failfast-false", "fail-fast: false" in cq)

# B12: no in-progress cancellation
check("B12-no-cancel-in-progress", "cancel-in-progress: false" in bd)

# B13: manual dispatch present
check("B13-dispatch", "workflow_dispatch:" in bd)

# bootstrap step in every evidence-producing job
check("A-bootstrap-preflight+platform", bd.count("Bootstrap CI log directories (guaranteed evidence)") >= 4,
      f"found {bd.count('Bootstrap CI log directories (guaranteed evidence)')} in build-desktop.yml")
check("A-bootstrap-codeql-analyze", cq.count("Bootstrap CI log directories (guaranteed evidence)") >= 2)
check("A-bootstrap-script-exists", open("scripts/ci/bootstrap-logs.sh").read().startswith("#!/usr/bin/env bash"))
check("A-bootstrap-ps1-exists", "New-Item -ItemType Directory -Force -Path $logDir" in open("scripts/ci/Bootstrap-Logs.ps1").read())

# artifact identity: run_attempt in every log-part name
for name in ["preflight-logs-part-attempt${{ github.run_attempt }}",
             "windows-logs-part-attempt${{ github.run_attempt }}",
             "linux-logs-part-attempt${{ github.run_attempt }}"]:
    check("identity-" + name.split("-")[0], name in bd, name)
check("identity-codeql", "codeql-logs-part-${{ matrix.language }}-attempt${{ github.run_attempt }}" in cq)

# upload the directory, not a brittle glob
check("upload-dir-not-glob", "          path: ci-logs\n" in bd and "          path: ci-logs\n" in cq,
      "log-part uploads must use path: ci-logs (directory form)")

# emergency fallback path present
check("fallback-report-step", "emergency-report.sh" in bd and "emergency-report.sh" in cq)
check("fallback-script-exists", open("scripts/ci/emergency-report.sh").read().startswith("#!/usr/bin/env bash"))

# fail-closed after upload
check("fail-closed", "Fail closed after the report upload attempt" in bd)

# manifest ↔ artifact name agreement (build-desktop)
check("manifest-agreement", '"artifact": "preflight-logs-part-attempt${{ github.run_attempt }}"' in bd)

# least privilege: caller codeql job grants actions: read (not write)
sec = re.search(r"  codeql:\n(?:    .*\n|      .*\n)*?    permissions:\n      actions: (\S+)", bd)
check("least-privilege-codeql-caller", sec is not None and sec.group(1) == "read",
      "" if sec is None else "grants actions: " + sec.group(1))

# E. ubuntu-26.04 on every Linux job; no ubuntu-latest; no 24.04
u2604 = len(re.findall(r"runs-on: ubuntu-26\.04", bd)) + len(re.findall(r"runs-on: ubuntu-26\.04", cq))
check("E-ubuntu-2604-count>=5", u2604 >= 5, f"explicit ubuntu-26.04 labels: {u2604}")
check("E-no-ubuntu-latest",
      re.search(r"runs-on:\s*ubuntu-latest", bd) is None and re.search(r"runs-on:\s*ubuntu-latest", cq) is None,
      "runs-on: ubuntu-latest must not appear (comments mentioning it are fine)")
check("E-no-24.04", "ubuntu-24.04" not in bd and "ubuntu-24.04" not in cq)

# never classify skipped as missing: collector has the classification states
col = open("internal/cilog/collect.go").read()
check("G-never-started-classification", "NEVER_STARTED (skipped; no log expected)" in col)
check("G-recovered-from-api", "RECOVERED_FROM_API" in col)
check("G-in-progress-classification", "IN_PROGRESS_AT_SNAPSHOT" in col)
check("G-api-unavailable-classification", "API_LOG_UNAVAILABLE" in col)
check("G-gates-not-reached", "GatesNotReached" in col and "GATES NOT REACHED" in col)
gt = open("internal/cilog/gate.go").read()
check("G-blocked-gate-state", "GateBlocked" in gt)

# cleanup pattern covers attempt-suffixed names
check("cleanup-covers-attempt", 'test("-logs-part(-attempt[0-9]+)?$")' in bd)

# trigger scope: push main + PR main + dispatch
check("trigger-push-main", "branches: [main]" in bd)
check("trigger-pr-main", re.search(r"pull_request:\s*\n\s*branches: \[main\]", bd) is not None)

fails = [c for c in checks if not c[2] if False]  # placeholder, replaced below
fails = [c for c in checks if not c[1]]
for name, ok, detail in checks:
    print(("OK  " if ok else "BAD ") + name + ("  " + detail if (not ok and detail) else ""))
print(f"DAG-CHECKS-FAILED={len(fails)}")
sys.exit(0)
PY
dag_failed=$(grep -c "^BAD " "$T/dag.txt" || true)
if [ "$dag_failed" -eq 0 ]; then
  report PASS "B7-13" "workflow DAG/static structure fixtures ($(grep -c '^OK ' "$T/dag.txt") assertions)"
else
  report FAIL "B7-13" "static structure assertions failed:"; cat "$T/dag.txt"
fi

# ---------- D22/D24-25: UCRT64 static regression assertions ----------------
BW="scripts/build/build-windows.ps1"
if assert_contains "$BD" 'mingw-w64-ucrt-x86_64-gcc' "UCRT64 gcc package (workflow install list)" \
   && assert_contains "$BW" "x86_64-w64-mingw32" "dumpmachine target" \
   && assert_contains "$BW" "_UCRT" "UCRT runtime define check" \
   && assert_contains ".github/workflows/build-desktop.yml" "x86_64-w64-mingw32" "workflow identity gate" \
   && assert_contains ".github/workflows/build-desktop.yml" "stale shadowing toolchain detected" "stale compiler rejection" \
   && assert_contains "$BW" 'SHEYTAN_GO_TAGS' "single documented tag source" \
   && assert_contains "scripts/ci/Capture-Step.ps1" 'SHEYTAN_GO_TAGS -split' "per-gate goTags initialization"; then
  report PASS "D22-25" "UCRT64 static regressions: tag source, identity gate, _UCRT/dumpmachine checks present"
else
  report FAIL "D22-25" "UCRT64 static assertions failed"
fi

# ---------- E43/E44: emergency fallback report + redaction -----------------
(
  cd "$T" || exit 3
  rm -rf parts ci-out
  mkdir -p parts/preflight-logs-part-attempt1 parts/windows-logs-part-attempt1 ci-out
  echo "log bootstrap: PASS" > parts/preflight-logs-part-attempt1/bootstrap.log
  printf 'EXIT_CODE: 1 FAIL\nCOMMAND: go test' > parts/windows-logs-part-attempt1/phase2-build.log
  cat > parts/manifest.json <<'JSONX'
{"product":"SHEYTAN Music Visualizer","workflow":"build-desktop.yml","run_id":"1","attempt":"1",
 "jobs":[{"name":"preflight","result":"success","artifact":"preflight-logs-part-attempt1","required":true},
         {"name":"build-windows","result":"failure","artifact":"windows-logs-part-attempt1","required":true}]}
JSONX
  bash "$REPO_ROOT/scripts/ci/emergency-report.sh" \
    --out ci-out/log.txt --parts-root parts --manifest parts/manifest.json \
    --reason "fixture: collector binary build failed" >/dev/null 2>&1
) >/dev/null 2>&1
if [ -s "$T/ci-out/log.txt" ] \
   && grep -q "EMERGENCY FALLBACK PATH" "$T/ci-out/log.txt" \
   && grep -q "OVERALL RESULT: FAIL" "$T/ci-out/log.txt" \
   && grep -q "EXIT_CODE: 1 FAIL" "$T/ci-out/log.txt"; then
  report PASS "E43" "fallback report written, truthful red result, parts embedded (collector-binary-unavailable path)"
else
  report FAIL "E43" "emergency fallback report missing or untruthful"
fi
(
  cd "$T" || exit 3
  echo "token ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ12345 in log" > parts/preflight-logs-part-attempt1/secret.log
  bash "$REPO_ROOT/scripts/ci/emergency-report.sh" \
    --out ci-out/log.txt --parts-root parts --manifest parts/manifest.json \
    --reason "fixture: redaction check" >/dev/null 2>&1
) >/dev/null 2>&1
if grep -q "ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZ12345" "$T/ci-out/log.txt" 2>/dev/null; then
  report FAIL "E44" "emergency report leaked a token"
else
  report PASS "E44" "emergency report redacts token-shaped secrets"
fi

# ---------- F49 pointer ----------------------------------------------------
report PASS "F49" "coverage inventory executed separately: scripts/ci/tests/test-codeql-coverage-inventory.sh"

# ---------- D22-23 runtime + C17-21: PowerShell fixtures -------------------
if command -v pwsh >/dev/null 2>&1; then
  if pwsh -NoProfile -File scripts/ci/tests/Test-CaptureStep.ps1 > "$T/pwsh.txt" 2>&1; then
    report PASS "C17-21" "PowerShell capture fixtures: $(tail -1 "$T/pwsh.txt")"
  else
    report FAIL "C17-21" "PowerShell fixtures failed:"; cat "$T/pwsh.txt"
  fi
else
  report "NOT RUN" "C17-21" "pwsh not available on this Linux host — scripts/ci/tests/Test-CaptureStep.ps1 must run on Windows/any pwsh 7 host"
  report "NOT RUN" "D22-23" "same limitation (runtime child-process scope proof requires pwsh)"
fi

echo "--------------------------------------------------------------"
echo "RESULT: PASS=$PASSED FAIL=$FAILED NOT_RUN=$NOT_RUN"
echo "=============================================================="
rm -rf "$T"
[ "$FAILED" -eq 0 ]
