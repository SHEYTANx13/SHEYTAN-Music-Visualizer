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
#
# W-series (run 43712071111): persisted-evidence fixtures for
# scripts/ci/write-package-status.py — the package-status record must be
# derived ONLY from gate-result JSON files, export-proof result files
# and an actual archive scan (VERIFIED / TEST_ONLY / UNAVAILABLE
# semantics of the compact-package contract).
set -u
cd "$(dirname "${BASH_SOURCE[0]}")/../../.." || exit 3
REPO_ROOT="$PWD"
OBSERVER="$REPO_ROOT/scripts/ci/observe_commit.py"
WRITER="$REPO_ROOT/scripts/ci/write-package-status.py"
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

# ============================================================================
# W-series (run 43712071111): write-package-status.py persisted-evidence
# fixtures. Every scenario builds a synthetic ci-logs evidence tree
# (gate-results/*.result.json + export-proof-*.result.txt) plus a synthetic
# package archive, invokes the canonical writer and asserts the record.
# ============================================================================
WT="$(mktemp -d)"
trap 'rm -rf "$WT"' EXIT
WAPP="SHEYTAN-Music-Visualizer-linux-x64"
# The writer's gate classification union (keep in sync with
# scripts/ci/write-package-status.py).
WALL_GATES="stage-portable verify-package-license validate-pe create-portable-zip create-portable-tar forbid-bundled-media-tools package-size-budget verify-extracted-package verify-extracted-path-with-spaces toolchain-report desktop-env-check gofmt-verify go-mod-tidy-verify go-mod-verify go-test go-vet go-build-packages build-executable run-version run-self-check verify-build-metadata gpu-validation go-test-race export-validation audio-validation studio-integration gui-smoke govulncheck media-tools-downloader-tests media-tools-pinned-install"

w_mk_evidence() { # w_mk_evidence <dir>
  mkdir -p "$1/gate-results"
  for g in $WALL_GATES; do
    printf '{"gate":"%s","verdict":"PASS","executed":true,"exit_code":0}\n' "$g" > "$1/gate-results/$g.result.json"
  done
  printf 'PASS /tmp/packaged-export-proof-h264.mp4\n' > "$1/export-proof-h264.result.txt"
  printf 'PASS /tmp/packaged-export-proof-h265.mp4\n' > "$1/export-proof-h265.result.txt"
}

w_mk_package() { # w_mk_package <archive> [extra-file]
  local stage
  stage="$(mktemp -d)"
  mkdir -p "$stage/$WAPP/tools/ffmpeg/bin" "$stage/$WAPP/data"
  printf 'MZ-dummy'  > "$stage/$WAPP/sheytan-music-visualizer"
  printf 'license'   > "$stage/$WAPP/license.md"
  printf 'readme'    > "$stage/$WAPP/readme.md"
  : >                "$stage/$WAPP/portable.mode"
  printf 'contract'  > "$stage/$WAPP/data/DATA-FOLDERS.txt"
  printf 'licensing' > "$stage/$WAPP/tools/ffmpeg/FFMPEG-TOOLS-LICENSING.md"
  if [ -n "${2:-}" ]; then
    printf 'fake-media-tool' > "$stage/$WAPP/tools/ffmpeg/bin/$2"
  fi
  ( cd "$stage" && tar -czf "$1" "$WAPP" )
  rm -rf "$stage"
}

w_write() { # w_write <evidence-dir> <archive> <out.json> <commit-verified> [commit-observed]
  python3 "$WRITER" --platform linux-x64 \
    --package "$2" \
    --package-file "dist/SHEYTAN-Music-Visualizer-linux-x64.tar.gz" \
    --app-dir "$WAPP" \
    --log-dir "$1" \
    --out "$3" \
    --commit-expected "$SHA" \
    --commit-observed "${5:-$SHA}" \
    --commit-verified "$4" \
    --commit-source "--build-info-json (built exe + extracted package, typed JSON parse)" \
    --commit-reason "" >/dev/null 2>&1
}

w_field() { # w_field <json> <python-expr>
  python3 -c "import json,sys; d=json.load(open(sys.argv[1])); print(sys.argv[2] and eval(sys.argv[2]) or '')" "$1" "$2" 2>/dev/null
}
w_get() { # w_get <json> <key>
  python3 -c "import json,sys; print(json.load(open(sys.argv[1])).get(sys.argv[2]))" "$1" "$2" 2>/dev/null
}

# W1: all-green evidence + clean archive + verified commit -> VERIFIED with
#     the full compact-package schema.
w_mk_evidence "$WT/w1"
w_mk_package "$WT/w1-package.tar.gz"
w_write "$WT/w1" "$WT/w1-package.tar.gz" "$WT/w1.json" true
if [ "$(w_get "$WT/w1.json" package_status)" = "VERIFIED" ] \
   && [ "$(w_get "$WT/w1.json" ffmpeg_binaries_bundled)" = "False" ] \
   && [ "$(w_get "$WT/w1.json" media_tools_downloader_fixture)" = "PASS" ] \
   && [ "$(w_get "$WT/w1.json" media_tools_pinned_install_integration)" = "PASS" ] \
   && [ "$(w_get "$WT/w1.json" app_launch_self_check)" = "PASS" ] \
   && [ "$(w_get "$WT/w1.json" h264_export_proof)" = "PASS" ] \
   && [ "$(w_get "$WT/w1.json" h265_software_export_proof)" = "PASS" ] \
   && [ "$(w_get "$WT/w1.json" intel_qsv_hevc_proof)" = "NOT AVAILABLE" ] \
   && [ "$(w_get "$WT/w1.json" ui_layout_matrix)" = "PASS" ] \
   && [ "$(w_get "$WT/w1.json" crash_fault_injection)" = "PASS" ] \
   && [ "$(w_get "$WT/w1.json" commit_verified)" = "True" ] \
   && [ "$(w_get "$WT/w1.json" package_file)" = "dist/SHEYTAN-Music-Visualizer-linux-x64.tar.gz" ] \
   && [ "$(w_get "$WT/w1.json" package_bytes)" -gt 0 ]; then
  report PASS W1 "all-green persisted evidence -> VERIFIED with the full compact-package schema"
else
  report FAIL W1 "all-green evidence must yield VERIFIED (record: $(cat "$WT/w1.json" 2>/dev/null | tr -d '\n'))"
fi

# W2: one unrelated quality gate FAIL -> TEST_ONLY (never VERIFIED, never
#     UNAVAILABLE — the package itself is healthy).
w_mk_evidence "$WT/w2"
printf '{"gate":"go-test","verdict":"FAIL","executed":true,"exit_code":1}\n' > "$WT/w2/gate-results/go-test.result.json"
w_write "$WT/w2" "$WT/w1-package.tar.gz" "$WT/w2.json" true
if [ "$(w_get "$WT/w2.json" package_status)" = "TEST_ONLY" ]; then
  report PASS W2 "failed quality gate with healthy package -> TEST_ONLY"
else
  report FAIL W2 "failed quality gate must yield TEST_ONLY (got: $(w_get "$WT/w2.json" package_status))"
fi

# W3: failed package gate -> UNAVAILABLE (never uploaded).
w_mk_evidence "$WT/w3"
printf '{"gate":"create-portable-tar","verdict":"FAIL","executed":true,"exit_code":1}\n' > "$WT/w3/gate-results/create-portable-tar.result.json"
w_write "$WT/w3" "$WT/w1-package.tar.gz" "$WT/w3.json" true
if [ "$(w_get "$WT/w3.json" package_status)" = "UNAVAILABLE" ]; then
  report PASS W3 "failed package gate -> UNAVAILABLE"
else
  report FAIL W3 "failed package gate must yield UNAVAILABLE (got: $(w_get "$WT/w3.json" package_status))"
fi

# W4: FAIL export-proof file -> h264_export_proof=FAIL + TEST_ONLY.
w_mk_evidence "$WT/w4"
printf 'FAIL app-managed export --export-codec h264 failed validation\n' > "$WT/w4/export-proof-h264.result.txt"
w_write "$WT/w4" "$WT/w1-package.tar.gz" "$WT/w4.json" true
if [ "$(w_get "$WT/w4.json" package_status)" = "TEST_ONLY" ] && [ "$(w_get "$WT/w4.json" h264_export_proof)" = "FAIL" ]; then
  report PASS W4 "FAIL proof file -> h264_export_proof=FAIL + TEST_ONLY"
else
  report FAIL W4 "FAIL proof file must yield TEST_ONLY + h264_export_proof=FAIL"
fi

# W5: missing proof files (gate never ran) -> NOT RUN + TEST_ONLY (absence
#     is never a pass).
w_mk_evidence "$WT/w5"
rm -f "$WT/w5/export-proof-h264.result.txt" "$WT/w5/export-proof-h265.result.txt"
w_write "$WT/w5" "$WT/w1-package.tar.gz" "$WT/w5.json" true
if [ "$(w_get "$WT/w5.json" package_status)" = "TEST_ONLY" ] \
   && [ "$(w_get "$WT/w5.json" h264_export_proof)" = "NOT RUN" ] \
   && [ "$(w_get "$WT/w5.json" h265_software_export_proof)" = "NOT RUN" ]; then
  report PASS W5 "missing proof files -> NOT RUN + TEST_ONLY (absence is not a pass)"
else
  report FAIL W5 "missing proof files must yield NOT RUN + TEST_ONLY"
fi

# W6: archive actually carrying an ffmpeg binary -> ffmpeg_binaries_bundled=true
#     + UNAVAILABLE (the archive scan is evidence, not the gate records).
w_mk_evidence "$WT/w6"
w_mk_package "$WT/w6-package.tar.gz" ffmpeg
w_write "$WT/w6" "$WT/w6-package.tar.gz" "$WT/w6.json" true
if [ "$(w_get "$WT/w6.json" package_status)" = "UNAVAILABLE" ] && [ "$(w_get "$WT/w6.json" ffmpeg_binaries_bundled)" = "True" ]; then
  report PASS W6 "archive scan detects the bundled ffmpeg -> UNAVAILABLE + ffmpeg_binaries_bundled=true"
else
  report FAIL W6 "bundled ffmpeg must yield UNAVAILABLE + ffmpeg_binaries_bundled=true"
fi

# W7: missing archive -> UNAVAILABLE.
w_mk_evidence "$WT/w7"
w_write "$WT/w7" "$WT/does-not-exist.tar.gz" "$WT/w7.json" true
if [ "$(w_get "$WT/w7.json" package_status)" = "UNAVAILABLE" ] && [ "$(w_get "$WT/w7.json" package_bytes)" = "0" ]; then
  report PASS W7 "missing archive -> UNAVAILABLE"
else
  report FAIL W7 "missing archive must yield UNAVAILABLE"
fi

# W8: unverified commit -> UNAVAILABLE regardless of green evidence.
w_mk_evidence "$WT/w8"
w_write "$WT/w8" "$WT/w1-package.tar.gz" "$WT/w8.json" false "$SHA2"
if [ "$(w_get "$WT/w8.json" package_status)" = "UNAVAILABLE" ] && [ "$(w_get "$WT/w8.json" commit_observed)" = "$SHA2" ]; then
  report PASS W8 "unverified commit -> UNAVAILABLE with the observed commit recorded truthfully"
else
  report FAIL W8 "unverified commit must yield UNAVAILABLE"
fi

# W9: failed media-tools gate -> media gate FAIL is soft (TEST_ONLY) and the
#     fixture field records the FAIL.
w_mk_evidence "$WT/w9"
printf '{"gate":"media-tools-downloader-tests","verdict":"FAIL","executed":true,"exit_code":1}\n' > "$WT/w9/gate-results/media-tools-downloader-tests.result.json"
w_write "$WT/w9" "$WT/w1-package.tar.gz" "$WT/w9.json" true
if [ "$(w_get "$WT/w9.json" package_status)" = "TEST_ONLY" ] \
   && [ "$(w_get "$WT/w9.json" media_tools_downloader_fixture)" = "FAIL" ] \
   && [ "$(w_get "$WT/w9.json" media_tools_download_supported)" = "False" ]; then
  report PASS W9 "failed media gate -> TEST_ONLY + media_tools_downloader_fixture=FAIL"
else
  report FAIL W9 "failed media gate must yield TEST_ONLY + fixture FAIL"
fi

# W10: gate records that never ran (no record) -> SKIPPED fixture + FAIL
#      self-check (never a pass from absence).
w_mk_evidence "$WT/w10"
rm -f "$WT/w10/gate-results/media-tools-downloader-tests.result.json" "$WT/w10/gate-results/run-self-check.result.json"
w_write "$WT/w10" "$WT/w1-package.tar.gz" "$WT/w10.json" true
if [ "$(w_get "$WT/w10.json" package_status)" = "TEST_ONLY" ] \
   && [ "$(w_get "$WT/w10.json" media_tools_downloader_fixture)" = "SKIPPED" ] \
   && [ "$(w_get "$WT/w10.json" app_launch_self_check)" = "FAIL" ]; then
  report PASS W10 "missing gate records -> SKIPPED/FAIL, never a pass from absence"
else
  report FAIL W10 "missing gate records must yield SKIPPED fixture + FAIL self-check"
fi

echo "SUMMARY: pass=$PASSED fail=$FAILED"
if [ "$FAILED" -gt 0 ]; then exit 1; fi
exit 0
