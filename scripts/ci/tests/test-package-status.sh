#!/usr/bin/env bash
# test-package-status.sh — run-43090342995 item D regression fixtures for
# scripts/ci/observe_commit.py (the bash-side package-status commit
# observer). The contract MUST stay identical to
# scripts/ci/Get-PackageCommitStatus.ps1 (cross-platform pwsh fixtures in
# scripts/ci/tests/Test-PackageStatusObserver.ps1).
#
# Matrix: correct commit, missing commit field, mismatch, trailing
# whitespace/LF/CRLF, empty output, malformed JSON, non-zero process
# exit, unknown default, blank expected, case-exact comparison.
set -u
cd "$(dirname "${BASH_SOURCE[0]}")/../../.." || exit 3
REPO_ROOT="$PWD"
OBSERVER="$REPO_ROOT/scripts/ci/observe_commit.py"
SHA="acc64d80d0029410ea6e1671edfd795afbdf06f5"
SHA2="0123456789abcdef0123456789abcdef01234567"

PASSED=0; FAILED=0
report() {
  if [ "$1" = PASS ]; then PASSED=$((PASSED+1)); printf 'PASS    %-8s %s\n' "$2" "$3"
  else FAILED=$((FAILED+1)); printf 'FAIL    %-8s %s\n' "$2" "$3"; fi
}

bi() {  # build-info JSON line with the given commit value
  printf '{"version":"1.0.0","commit":"%s","date":"2026-11-13T00:00:00Z","built_by":"ci","go_version":"go1.27.4","os":"linux","arch":"amd64"}' "$1"
}
# run <expected> <exit-code> <stdin-content> -> sets OBS JSON
run_obs() {
  printf '%s' "$3" | python3 "$OBSERVER" "$1" "$2" > /tmp/obs-out.json 2>/dev/null || true
  OBS="$(cat /tmp/obs-out.json 2>/dev/null || echo '{}')"
}
check() {  # check <json> <expected-observed> <expected-verified>
  obs="$(printf '%s' "$1" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("observed","?"))' 2>/dev/null || echo parse-error)"
  ver="$(printf '%s' "$1" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("verified","?"))' 2>/dev/null || echo parse-error)"
  [ "$obs" = "$2" ] && [ "$ver" = "$3" ]
}

echo "=============================================================="
echo "PACKAGE-STATUS COMMIT OBSERVER FIXTURES (bash/python side)"
echo "observer: $OBSERVER"
echo "=============================================================="

run_obs "$SHA" 0 "$(bi "$SHA")"
check "$OBS" "$SHA" True && report PASS O1 "correct commit verifies exactly" || report FAIL O1 "correct commit must verify"

run_obs "$SHA" 0 '{"version":"1.0.0","date":"x"}'
check "$OBS" "unknown" False && report PASS O2 "missing commit field never verifies" || report FAIL O2 "missing commit field"

run_obs "$SHA" 0 "$(bi "$SHA2")"
check "$OBS" "$SHA2" False && report PASS O3 "mismatch reports observed truthfully, stays red" || report FAIL O3 "mismatch handling"

run_obs "$SHA" 0 "$(printf '%s\n' "$(bi "$SHA")")"
check "$OBS" "$SHA" True || report FAIL O4a "LF-terminated output"
run_obs "$SHA" 0 "$(printf '%s\r\n' "$(bi "$SHA")")"
check "$OBS" "$SHA" True || report FAIL O4b "CRLF-terminated output"
run_obs "  $SHA  " 0 "  $(bi "$SHA")  "
check "$OBS" "$SHA" True || report FAIL O4c "surrounding whitespace tolerated"
report PASS O4 "LF/CRLF/whitespace never break exact verification"

run_obs "$SHA" 0 ""
check "$OBS" "not-observed" False && report PASS O5 "empty output records not-observed" || report FAIL O5 "empty output"

run_obs "$SHA" 0 "SHEYTAN Music Visualizer main (commit $SHA, ...)"
check "$OBS" "not-observed" False && report PASS O6 "human console text never accepted as JSON" || report FAIL O6 "malformed JSON"

run_obs "$SHA" 1 "$(bi "$SHA")"
check "$OBS" "not-observed" False && report PASS O7 "non-zero exit never verified regardless of output" || report FAIL O7 "non-zero exit"

run_obs "$SHA" 0 "$(bi unknown)"
check "$OBS" "unknown" False && report PASS O8 "embedded unknown default never verifies" || report FAIL O8 "unknown default"

run_obs "" 0 "$(bi "$SHA")"
check "$OBS" "$SHA" False && report PASS O9 "blank expected cannot verify" || report FAIL O9 "blank expected"

run_obs "$SHA" 0 "$(bi "$(printf '%s' "$SHA" | tr 'a-f' 'A-F')")"
check "$OBS" "$(printf '%s' "$SHA" | tr 'a-f' 'A-F')" False && report PASS O10 "comparison is exact (case-sensitive)" || report FAIL O10 "case sensitivity"

echo "SUMMARY: pass=$PASSED fail=$FAILED"
if [ "$FAILED" -gt 0 ]; then exit 1; fi
exit 0
