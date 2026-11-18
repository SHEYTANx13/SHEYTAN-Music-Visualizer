#!/usr/bin/env bash
# build-linux.sh — Phase 2 local build/verification entry point (Linux).
#
# Usage:
#   scripts/build/build-linux.sh [--mode preflight|desktop]
#
# Modes:
#   preflight  CHEAP static gates only: toolchain report, gofmt,
#              go mod tidy -diff, go mod verify. The CI preflight job
#              uses this mode; tests, vet and builds belong to the
#              platform build jobs (run exactly once there).
#   desktop    Full verification: static gates plus tests, vet, package
#              build, the native Linux x64 desktop executable,
#              --version/--self-check runtime checks, race tests,
#              govulncheck and the export/GPU/audio validation
#              fixtures. Default.
#
# COMPACT PACKAGE CONTRACT (run 43712071111): the package ships NO
# media-tool binaries. FFmpeg/ffprobe are app-managed downloads (the
# in-app Media Tools & Codecs manager installs the pinned pair under
# <DataRoot>/media-tools/current/bin). CI enforces this negatively
# (forbid-bundled-media-tools, package-size-budget at 90 MiB) and
# proves the app-managed path with the media-tools gates plus REAL
# h264/hevc exports from the extracted package. The package-status
# record is derived from PERSISTED evidence only
# (scripts/ci/write-package-status.py).
#
# Contract:
#   - Every command's stdout+stderr is captured from the first byte into
#     ci-logs/phase2-build.log with a structured record and the original
#     exit code.
#   - Independent gates keep running after a failure; dependent gates are
#     recorded as SKIPPED, never as passed.
#   - The script exits non-zero if any required gate failed.
#
# Build tag note: the WGPU dependency (github.com/go-webgpu/goffi) uses
# its own fakecgo/dlopen stubs that the Go internal linker cannot place
# into a CGO binary; -tags goffi_static selects goffi's static dlopen
# path and is REQUIRED for every build that links Fyne + wgpu together
# (see ai-notes.md). The tag is harmless on Windows.

# Justifications (shellcheck):
#   SC2086: unquoted $GO_TAGS splitting is the documented argv contract
#   SC2016: single-quoted gate bodies are expanded by the inner bash -c, deliberately
#   SC1091: capture.sh is sourced from REPO_ROOT resolved at runtime
# shellcheck disable=SC2086
# shellcheck disable=SC2016
# shellcheck disable=SC1091
set -Eeuo pipefail
umask 022

MODE="desktop"
if [ "${1:-}" = "--mode" ]; then
  case "${2:-}" in
    preflight | desktop) MODE="$2" ;;
    *)
      echo "build-linux.sh: unknown mode '$2' (use preflight or desktop)" >&2
      exit 2
      ;;
  esac
fi

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"

LOG_DIR="$REPO_ROOT/ci-logs"
mkdir -p "$LOG_DIR"
CI_LOG_FILE="$LOG_DIR/phase2-build.log"
export CI_LOG_FILE
: >"$CI_LOG_FILE" # fresh local run; the packaged copy is evidence/phase2-build.log

# Persisted-evidence reset (run 43712071111): the export-proof result
# files consumed by scripts/ci/write-package-status.py are rewritten by
# the verify-extracted-path-with-spaces gate on every run; leftovers
# from an earlier LOCAL run must never survive as stale PASS evidence.
rm -f "$LOG_DIR/export-proof-h264.result.txt" "$LOG_DIR/export-proof-h265.result.txt"

# shellcheck source=../ci/capture.sh
# shellcheck disable=SC1091
source "$REPO_ROOT/scripts/ci/capture.sh"

JOB="phase2-linux-build"
FAILURES=0
FAILED_GATES=""
SKIPPED_GATES=""

note() { printf '%s\n' "$*" >>"$CI_LOG_FILE"; }
fail_gate() {
  FAILURES=$((FAILURES + 1))
  FAILED_GATES="$FAILED_GATES $1"
}
skip_gate() {
  SKIPPED_GATES="$SKIPPED_GATES $1"
  printf 'SKIPPED: gate %s (%s)\n' "$1" "$2" >>"$CI_LOG_FILE"
}
blocked_gate() {
  # Missing-prerequisite classification, distinct from SKIPPED (intentional
  # not-run) and from FAIL (a command ran and exited nonzero). The
  # consolidated report parses these into the BLOCKED gate state.
  SKIPPED_GATES="$SKIPPED_GATES $1"
  printf 'BLOCKED: gate %s (%s)\n' "$1" "$2" >>"$CI_LOG_FILE"
}

note "=============================================================="
note "SHEYTAN MUSIC VISUALIZER — PHASE 2 LOCAL VERIFICATION (LINUX)"
note "Product:  SHEYTAN Music Visualizer (SHEYTAN™, licensor Parsaetak)"
note "Mode:     $MODE"
note "Host:     $(uname -srm)"
note "Started:  $(date -u '+%Y-%m-%dT%H:%M:%SZ')"
note "Build tag: goffi_static (Fyne + wgpu link compatibility)"
note "=============================================================="

GO_TAGS="-tags goffi_static"

# Missing-prerequisite honesty (agent.md §4 vocabulary): without the Go
# toolchain NOTHING in this script can be verified. Dependent gates are
# recorded BLOCKED (never passed, never failed-with-fake-output) and the
# script fails so the CI job stays red with truthful evidence.
if ! command -v go >/dev/null 2>&1; then
  blocked_gate toolchain-report "go toolchain not found on PATH"
  blocked_gate gofmt-verify "go toolchain not found on PATH"
  blocked_gate go-mod-tidy-verify "go toolchain not found on PATH"
  blocked_gate go-mod-verify "go toolchain not found on PATH"
  if [ "$MODE" = "desktop" ]; then
    for g in go-test go-vet go-build-packages build-executable run-version run-self-check go-test-race export-validation audio-validation gpu-validation studio-integration media-tools-downloader-tests media-tools-pinned-install; do
      blocked_gate "$g" "go toolchain not found on PATH"
    done
  fi
  note ""
  note "RUN SUMMARY — $JOB"
  note "Ended:            $(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  note "Mode:             $MODE"
  note "Failed gates:     0 (prerequisite missing — this is NOT a passing run)"
  note "Blocked gate list:$SKIPPED_GATES"
  note "GATES NOT REACHED — the Go toolchain is required for every gate in this script."
  note "RUN RESULT: FAIL"
  note "=============================================================="
  exit 1
fi

# ---- toolchain report ------------------------------------------------------
CI_TOOL_CONTEXT="gate:toolchain" ci_capture "$JOB" toolchain-report bash -c 'go version; echo "GOOS=$(go env GOOS) GOARCH=$(go env GOARCH) CGO_ENABLED=$(go env CGO_ENABLED) GOTOOLCHAIN=$(go env GOTOOLCHAIN)"; echo "GOMOD=$(go env GOMOD)"; gcc --version | head -1; pkg-config --version; ffmpeg -version 2>/dev/null | head -1 || echo "ffmpeg: NOT AVAILABLE"' ||
  fail_gate toolchain-report

# ---- static gates (all independent; always run) ----------------------------
CI_TOOL_CONTEXT="gate:gofmt" ci_capture "$JOB" gofmt-verify bash -c 'out=$(gofmt -l .); if [ -n "$out" ]; then printf "%s\n" "$out"; echo "ERROR: gofmt reported unformatted files"; exit 1; fi; echo "gofmt clean"' ||
  fail_gate gofmt-verify

# Read-only module hygiene: go mod tidy -diff never mutates the
# checked-out tree (the mutating `go mod tidy` gate was removed).
CI_TOOL_CONTEXT="gate:go-mod-tidy" ci_capture "$JOB" go-mod-tidy-verify go mod tidy -diff ||
  fail_gate go-mod-tidy-verify

CI_TOOL_CONTEXT="gate:go-mod-verify" ci_capture "$JOB" go-mod-verify go mod verify ||
  fail_gate go-mod-verify

if [ "$MODE" = "preflight" ]; then
  note ""
  note "Preflight mode requested: tests, vet, builds and runtime checks belong to the platform build jobs."
  # Run-42837709122 regression fixtures for the Windows packaging/process
  # gates (ZIP root layout + robust child-process runner). Pure PowerShell
  # logic — runs on any host with pwsh; without pwsh every fixture is
  # reported NOT RUN (never silently passed).
  if CI_TOOL_CONTEXT="gate:package-gate-fixtures" ci_capture "$JOB" package-gate-fixtures bash scripts/ci/tests/test-package-gates.sh; then :; else fail_gate package-gate-fixtures; fi
  note ""
  note "RUN SUMMARY — $JOB"
  note "Ended:            $(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  note "Mode:             $MODE"
  note "Failed gates:     $FAILURES"
  if [ -n "$FAILED_GATES" ]; then note "Failed gate list:$FAILED_GATES"; fi
  if [ -n "$SKIPPED_GATES" ]; then note "Skipped/blocked gate list:$SKIPPED_GATES"; fi
  note ""
  note "FAILURE INDEX (EXIT_CODE records with non-zero codes):"
  if grep -n 'EXIT_CODE: [1-9][0-9]* FAIL' "$CI_LOG_FILE" >/dev/null 2>&1; then
    grep -n 'EXIT_CODE: [1-9][0-9]* FAIL' "$CI_LOG_FILE"
  else
    note "(none — all captured commands returned exit code 0)"
  fi
  if [ "$FAILURES" -eq 0 ]; then note "RUN RESULT: PASS"; else note "RUN RESULT: FAIL"; fi
  note "=============================================================="
  if [ "$FAILURES" -eq 0 ]; then exit 0; fi
  exit 1
fi

# Test ownership (run 42939743766 §2.4): the dedicated GPU integration
# test TestPaletteReachesGPUShader is owned EXCLUSIVELY by the
# gpu-validation gate below (it runs there once per platform with its
# readback diagnostics). It is excluded here ONLY by that exact name;
# every other internal/render test stays in the default suite.
CI_TOOL_CONTEXT="gate:go-test" ci_capture "$JOB" go-test go test $GO_TAGS -skip '^TestPaletteReachesGPUShader$' ./... ||
  fail_gate go-test

CI_TOOL_CONTEXT="gate:go-vet" ci_capture "$JOB" go-vet go vet $GO_TAGS ./... ||
  fail_gate go-vet

CI_TOOL_CONTEXT="gate:go-build" ci_capture "$JOB" go-build-packages go build $GO_TAGS ./... ||
  fail_gate go-build-packages

# ---- desktop executable + runtime checks (mode: desktop) -------------------
EXE_OK=0
if [ "$MODE" = "desktop" ]; then
  BI=github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/buildinfo
  LDFLAGS=()
  LDVALS=()
  [ -n "${SHEYTAN_BUILD_VERSION:-}" ] && LDVALS+=("-X ${BI}.version=${SHEYTAN_BUILD_VERSION}")
  [ -n "${SHEYTAN_BUILD_COMMIT:-}" ]  && LDVALS+=("-X ${BI}.commit=${SHEYTAN_BUILD_COMMIT}")
  [ -n "${SHEYTAN_BUILD_DATE:-}" ]    && LDVALS+=("-X ${BI}.date=${SHEYTAN_BUILD_DATE}")
  [ -n "${SHEYTAN_BUILT_BY:-}" ]      && LDVALS+=("-X ${BI}.builtBy=${SHEYTAN_BUILT_BY}")
  if [ "${#LDVALS[@]}" -gt 0 ]; then
    LDFLAGS=(-ldflags "${LDVALS[*]}")
  fi
  if CI_TOOL_CONTEXT="gate:build-exe" ci_capture "$JOB" build-executable go build -trimpath $GO_TAGS "${LDFLAGS[@]+"${LDFLAGS[@]}"}" -o bin/sheytan-music-visualizer ./cmd/sheytan-music-visualizer; then
    EXE_OK=1
  else
    fail_gate build-executable
  fi

  if [ "$EXE_OK" -eq 1 ]; then
    CI_TOOL_CONTEXT="runtime:--version" ci_capture "$JOB" run-version ./bin/sheytan-music-visualizer --version ||
      fail_gate run-version
    CI_TOOL_CONTEXT="runtime:--self-check" ci_capture "$JOB" run-self-check env SHEYTAN_LOG_DIR="$(pwd)/ci-logs/app-logs" ./bin/sheytan-music-visualizer --self-check ||
      fail_gate run-self-check
  else
    skip_gate run-version "executable build failed"
    skip_gate run-self-check "executable build failed"
  fi

  # Package-status evidence note (run 43712071111): the record is
  # derived from PERSISTED gate results by
  # scripts/ci/write-package-status.py AFTER every gate has run —
  # quality gates that execute later (race/export/audio/gpu/studio/
  # GUI/govulncheck) are part of that persisted evidence, so a later
  # failure downgrades the record to TEST_ONLY instead of being
  # invisible to it.
  commit_expected="${SHEYTAN_BUILD_COMMIT:-}"
  commit_observed="not-observed"
  commit_verified="false"
  commit_extracted_observed="not-observed"
  commit_reason="package missing or not built"

  # PINNED MEDIA-TOOLS PROOF CACHE (run 43712071111): the media-tools
  # CI proofs that need REAL ffmpeg/ffprobe binaries (the
  # media-tools-pinned-install integration gate and the packaged
  # app-managed export proof) download the pinned BtbN linux64 GPL
  # asset ONCE into this cache — always OUTSIDE the repository, the
  # dist/ tree and the package stage tree (scripts/ci/
  # ensure-pinned-media-tools.sh resolves it). The distributed package
  # ships NO media-tool binaries; the pinned pair is proof machinery
  # only. The parent owns cleanup after the last consumer.
  MEDIA_TOOLS_CACHE_OWNED=0
  if [ -z "${SHEYTAN_MEDIA_TOOLS_CACHE_DIR:-}" ]; then
    if [ -n "${RUNNER_TEMP:-}" ] && [ -d "${RUNNER_TEMP}" ]; then
      SHEYTAN_MEDIA_TOOLS_CACHE_DIR="$RUNNER_TEMP/media-tools"
    else
      SHEYTAN_MEDIA_TOOLS_CACHE_DIR="$(mktemp -d)/media-tools"
    fi
    export SHEYTAN_MEDIA_TOOLS_CACHE_DIR
    MEDIA_TOOLS_CACHE_OWNED=1
  fi

  # ---- portable package staging (run 42481815519 directory contract) ----
  # One top-level application folder: executable, license.md, readme.md,
  # portable.mode marker, the tools/ffmpeg app-local override location
  # (kept EMPTY — see the compact-package contract) and the documented
  # data/ bootstrap. A tar.gz preserves the executable bit (a ZIP would
  # not), so the portable contract survives extraction.
  # CANONICAL ONE-PACKAGE CONTRACT (run 42939743766 §5.1): the GitHub
  # Actions artifact is EXACTLY dist/SHEYTAN-Music-Visualizer-linux-x64.tar.gz
  # containing exactly one top-level app directory
  # SHEYTAN-Music-Visualizer-linux-x64/. The staging root below is an
  # INTERNAL directory that is never uploaded, never archived, and is
  # deleted before the source ZIP is produced; it must never be renamed
  # into a second distributable.
  STAGE_ROOT="$REPO_ROOT/dist/linux-x64-stage"
  STAGE_APP="$STAGE_ROOT/SHEYTAN-Music-Visualizer-linux-x64"
  TARBALL="$REPO_ROOT/dist/SHEYTAN-Music-Visualizer-linux-x64.tar.gz"
  rm -rf "$STAGE_ROOT" "$TARBALL"
  mkdir -p "$STAGE_APP/tools/ffmpeg/bin" "$STAGE_APP/data"
  if [ "$EXE_OK" -eq 1 ]; then
    install -m 0755 bin/sheytan-music-visualizer "$STAGE_APP/sheytan-music-visualizer"
    install -m 0644 license.md "$STAGE_APP/license.md"
    install -m 0644 readme.md "$STAGE_APP/readme.md"
    install -m 0644 packaging/portable.mode "$STAGE_APP/portable.mode"
    install -m 0644 packaging/DATA-FOLDERS.txt "$STAGE_APP/data/DATA-FOLDERS.txt"
    # COMPACT PACKAGE (run 43712071111): ONLY the licensing notice is
    # staged under tools/ffmpeg/ — the bin/ directory stays EMPTY (the
    # app-managed downloader owns the binaries; forbid-bundled-media-
    # tools rejects any archive that ships them).
    install -m 0644 packaging/FFMPEG-TOOLS-LICENSING.md "$STAGE_APP/tools/ffmpeg/FFMPEG-TOOLS-LICENSING.md"
  fi


  if [ "$EXE_OK" -eq 1 ]; then
    # The gate body documents paths RELATIVE TO THE STAGING ROOT
    # ($STAGE_ROOT), so the gate runs from there. Local lab run
    # discovery: executed from $REPO_ROOT these relative paths resolve
    # to nothing and the gate fails spuriously — the failed run never
    # reached this gate (build failure), so the defect stayed latent
    # until a local end-to-end execution exercised it. The subshell
    # keeps the caller cwd untouched; ci_capture inherits the absolute
    # CI_LOG_FILE, and the gate record shows the true WORKDIR.
    (
      cd "$STAGE_ROOT" || { echo "ERROR: staging root missing: $STAGE_ROOT"; exit 1; }
      CI_TOOL_CONTEXT="gate:stage-portable" ci_capture "$JOB" stage-portable bash -c '
        set -euo pipefail
        test -s "SHEYTAN-Music-Visualizer-linux-x64/sheytan-music-visualizer" || { echo "ERROR: staged executable missing"; exit 1; }
        test -x "SHEYTAN-Music-Visualizer-linux-x64/sheytan-music-visualizer" || { echo "ERROR: staged executable not executable"; exit 1; }
        for f in license.md readme.md portable.mode data/DATA-FOLDERS.txt tools/ffmpeg/FFMPEG-TOOLS-LICENSING.md; do
          test -s "SHEYTAN-Music-Visualizer-linux-x64/$f" || { echo "ERROR: staged file missing/empty: $f"; exit 1; }
        done
        # COMPACT PACKAGE CONTRACT (run 43712071111): tools/ffmpeg/bin is
        # the documented app-local override location and MUST ship EMPTY —
        # no media-tool binary is ever staged into the package.
        test -d "SHEYTAN-Music-Visualizer-linux-x64/tools/ffmpeg/bin" || { echo "ERROR: app-local tools/ffmpeg/bin directory missing"; exit 1; }
        if find "SHEYTAN-Music-Visualizer-linux-x64/tools/ffmpeg/bin" -mindepth 1 | grep -q .; then
          echo "ERROR: tools/ffmpeg/bin must be EMPTY (compact package ships no media-tool binaries):"
          find "SHEYTAN-Music-Visualizer-linux-x64/tools/ffmpeg/bin" -mindepth 1
          exit 1
        fi
        echo "staged portable tree (compact contract: tools/ffmpeg/bin EMPTY, licensing notice staged):"
        find "SHEYTAN-Music-Visualizer-linux-x64" -type f -printf "STAGED: %P (%s bytes)\n" | sort
      '
    ) || fail_gate stage-portable

    # BYTE-FOR-BYTE license identity gate (pre-archive): cmp on raw
    # bytes between the canonical tracked file and the staged copy.
    CI_TOOL_CONTEXT="gate:license-identity (staged)" ci_capture "$JOB" verify-package-license cmp license.md "$STAGE_APP/license.md" ||
      fail_gate verify-package-license

    # Tar.gz creation preserving permissions, then POST-ARCHIVE
    # verification: extract the actual archive and compare license
    # bytes + executable bit again. A staging check alone does not
    # prove what a user receives.
    CI_TOOL_CONTEXT="gate:create-portable-tar" ci_capture "$JOB" create-portable-tar bash -c '
      set -euo pipefail
      tar -czf "dist/SHEYTAN-Music-Visualizer-linux-x64.tar.gz" -C "dist/linux-x64-stage" "SHEYTAN-Music-Visualizer-linux-x64"
      test -s "dist/SHEYTAN-Music-Visualizer-linux-x64.tar.gz" || { echo "ERROR: tarball missing/empty"; exit 1; }
      echo "tarball contents:"
      tar -tzf "dist/SHEYTAN-Music-Visualizer-linux-x64.tar.gz"
    ' || fail_gate create-portable-tar

    if [ -s "$TARBALL" ]; then
      VERIFY_DIR="$(mktemp -d)"
      CI_TOOL_CONTEXT="gate:package-verify (extracted tarball, compact contract)" ci_capture "$JOB" verify-extracted-package bash -c '
        set -euo pipefail
        VERIFY_DIR="$1"
        tar -xzf "dist/SHEYTAN-Music-Visualizer-linux-x64.tar.gz" -C "$VERIFY_DIR"
        APP="$VERIFY_DIR/SHEYTAN-Music-Visualizer-linux-x64"
        cmp license.md "$APP/license.md" || { echo "ERROR: DISTRIBUTED license.md differs from canonical (post-archive)"; exit 1; }
        test -x "$APP/sheytan-music-visualizer" || { echo "ERROR: executable bit lost in tarball"; exit 1; }
        test -f "$APP/portable.mode" || { echo "ERROR: portable.mode missing in tarball"; exit 1; }
        test -s "$APP/tools/ffmpeg/FFMPEG-TOOLS-LICENSING.md" || { echo "ERROR: ffmpeg licensing doc missing in tarball"; exit 1; }
        # COMPACT PACKAGE CONTRACT (run 43712071111): the app-local
        # tools/ffmpeg/bin override location ships EMPTY — the
        # app-managed downloader owns the binaries, and NO ffmpeg/ffprobe
        # binary may appear anywhere in the extracted tree (the
        # archive-level scan is owned by forbid-bundled-media-tools).
        test -d "$APP/tools/ffmpeg/bin" || { echo "ERROR: app-local tools/ffmpeg/bin directory missing in tarball"; exit 1; }
        if find "$APP/tools/ffmpeg/bin" -mindepth 1 | grep -q .; then
          echo "ERROR: tools/ffmpeg/bin must be EMPTY in the distributed package (compact contract):"
          find "$APP/tools/ffmpeg/bin" -mindepth 1
          exit 1
        fi
        if find "$APP" -type f \( -name ffmpeg -o -name ffprobe -o -name ffmpeg.exe -o -name ffprobe.exe \) | grep -q .; then
          echo "ERROR: media-tool binary found in the extracted package:"
          find "$APP" -type f \( -name ffmpeg -o -name ffprobe -o -name ffmpeg.exe -o -name ffprobe.exe \)
          exit 1
        fi
        echo "extracted package verified (compact contract): license byte-identical, executable bit preserved, portable.mode present, licensing notice present, tools/ffmpeg/bin EMPTY, zero media-tool binaries"
      ' _ "$VERIFY_DIR" || fail_gate verify-extracted-package
      rm -rf "$VERIFY_DIR"

      # ---- NEGATIVE PACKAGE GATE: no bundled media tools (run 43712071111)
      # The reusable helper scans the produced tarball (raw entry list +
      # extracted tree) and fails on any ffmpeg/ffprobe file, any DLL
      # under tools/, any nested archive and any file over the 40 MiB
      # per-file budget. Media tools are app-managed downloads (the
      # in-app Media Tools & Codecs manager installs the pinned pair
      # under <DataRoot>/media-tools/current/bin, never into the
      # package).
      CI_TOOL_CONTEXT="gate:forbid-bundled-media-tools (negative: zero bundled media tools)" ci_capture "$JOB" forbid-bundled-media-tools bash "$REPO_ROOT/scripts/ci/forbid-bundled-media-tools.sh" "$TARBALL" ||
        fail_gate forbid-bundled-media-tools

      # ---- PACKAGE SIZE BUDGET (run 43712071111) --------------------------
      # The compact package (no bundled media tools) must stay UNDER
      # 90 MiB = 94371840 bytes, the SAME contract value the Windows
      # ZIP gate and the D35 static assertions own (contract drift at
      # run 44094081531: the gate had silently tightened to 70 MiB
      # while the script header, the Windows gate and the plumbing
      # test all pinned 90 MiB).
      CI_TOOL_CONTEXT="gate:package-size-budget (90 MiB = 94371840 bytes)" ci_capture "$JOB" package-size-budget bash -c '
        set -euo pipefail
        TARBALL="dist/SHEYTAN-Music-Visualizer-linux-x64.tar.gz"
        test -s "$TARBALL" || { echo "ERROR: package tarball missing: $TARBALL"; exit 1; }
        BYTES="$(stat -c%s "$TARBALL")"
        BUDGET=94371840
        echo "package:      $TARBALL"
        echo "package size: $BYTES bytes"
        echo "size budget:  $BUDGET bytes (90 MiB)"
        if [ "$BYTES" -ge "$BUDGET" ]; then
          echo "ERROR: package size $BYTES bytes meets or exceeds the $BUDGET-byte budget (90 MiB)"
          exit 1
        fi
        echo "package-size-budget PASSED: $BYTES bytes < $BUDGET bytes"
      ' || fail_gate package-size-budget
    else
      skip_gate verify-extracted-package "tarball not created"
      skip_gate forbid-bundled-media-tools "tarball not created"
      skip_gate package-size-budget "tarball not created"
    fi

    # ---- media-tools downloader fixture gate (run 43712071111) -----------
    # The in-app Media Tools & Codecs manager (internal/mediatools)
    # downloads and installs the pinned pair at runtime. Its fixture
    # suite proves the downloader, resolution order and layout contracts
    # WITHOUT network access; the env-gated real-download integration
    # test is owned by media-tools-pinned-install below.
    CI_TOOL_CONTEXT="gate:media-tools-downloader-tests (internal/mediatools fixtures)" ci_capture "$JOB" media-tools-downloader-tests go test $GO_TAGS -count=1 ./internal/mediatools/ ||
      fail_gate media-tools-downloader-tests

    # ---- pinned media-tools install integration gate (run 43712071111) --
    # Downloads the PINNED BtbN linux64 GPL asset ONCE into the runner
    # temp cache (outside repo/dist; never bundled into any artifact),
    # SHA-256-verifies it, then runs the env-gated integration test
    # TestMediaToolsInstallFromPinnedArchive with
    # SHEYTAN_TEST_MEDIA_TOOLS_ARCHIVE/SHEYTAN_TEST_MEDIA_TOOLS_SHA256
    # pointing at the verified archive. The proof cache is owned by the
    # parent (SHEYTAN_MEDIA_TOOLS_CACHE_DIR) and cleaned after the last
    # consumer; this gate never deletes it.
    CI_TOOL_CONTEXT="gate:media-tools-pinned-install (pinned BtbN linux64 asset + env-gated integration test)" ci_capture "$JOB" media-tools-pinned-install bash -c '
      set -euo pipefail
      REPO_ROOT="$1"
      helper_out="$(bash "$REPO_ROOT/scripts/ci/ensure-pinned-media-tools.sh")" || { echo "ERROR: pinned media-tools provisioning failed (download/SHA-256/layout)"; exit 1; }
      eval "$helper_out"
      echo "pinned archive: $SHEYTAN_MEDIA_ARCHIVE (SHA-256 verified)"
      echo "pinned sha256:  $SHEYTAN_MEDIA_SHA256"
      echo "pinned bin dir: $SHEYTAN_MEDIA_BIN"
      export SHEYTAN_TEST_MEDIA_TOOLS_ARCHIVE="$SHEYTAN_MEDIA_ARCHIVE"
      export SHEYTAN_TEST_MEDIA_TOOLS_SHA256="$SHEYTAN_MEDIA_SHA256"
      rc=0
      go test -tags goffi_static -count=1 -run "TestMediaToolsInstallFromPinnedArchive" ./internal/mediatools/ -v || rc=$?
      unset SHEYTAN_TEST_MEDIA_TOOLS_ARCHIVE SHEYTAN_TEST_MEDIA_TOOLS_SHA256
      if [ "$rc" -ne 0 ]; then
        echo "ERROR: TestMediaToolsInstallFromPinnedArchive failed (exit $rc) — the app-managed install path is not proven"
        exit "$rc"
      fi
      echo "media-tools pinned install integration PASSED (pinned archive SHA-256 verified, installer test green)"
    ' _ "$REPO_ROOT" || fail_gate media-tools-pinned-install

    # PATH-WITH-SPACES RUNTIME CHECK + APP-MANAGED EXPORT PROOFS
    # (run 42939743766 §5.3, compact-contract proof run 43712071111):
    # extract the SAME canonical tarball under a directory containing
    # spaces and run the extracted executable there. The package ships
    # NO binaries, so the proof installs the PINNED pair into the
    # app-managed location <DataRoot>/media-tools/current/bin and runs
    # the EXTRACTED app with SHEYTAN_DATA_DIR pointing at that data
    # root, with PATH scrubbed of every FFmpeg-carrying directory and
    # SHEYTAN_FFMPEG unset — the app MUST resolve its own app-managed
    # pair. Both codecs (h264 default, hevc software) are validated
    # with the app-managed ffprobe; the verdicts are PERSISTED to
    # ci-logs/export-proof-h264.result.txt / export-proof-h265.result.txt
    # (PASS <out> / FAIL <reason>) for scripts/ci/write-package-status.py.
    if [ -s "$TARBALL" ]; then
      SPACES_BASE="$(mktemp -d)"
      SPACES_DIR="$SPACES_BASE/SHEYTAN Spaces Dir"
      mkdir -p "$SPACES_DIR"
      CI_TOOL_CONTEXT="gate:package-verify (path with spaces + app-managed media-tools export proofs)" ci_capture "$JOB" verify-extracted-path-with-spaces bash -c '
        set -euo pipefail
        SPACES_DIR="$1"; LOG_DIR="$2"; REPO_ROOT="$3"
        PROOF_H264="$LOG_DIR/export-proof-h264.result.txt"
        PROOF_H265="$LOG_DIR/export-proof-h265.result.txt"
        rm -f "$PROOF_H264" "$PROOF_H265"
        fail_proof() { # fail_proof <h264|h265> <reason>
          case "$1" in
            h264) printf "FAIL %s\n" "$2" > "$PROOF_H264" ;;
            h265) printf "FAIL %s\n" "$2" > "$PROOF_H265" ;;
          esac
          echo "ERROR: $2"
        }
        tar -xzf "dist/SHEYTAN-Music-Visualizer-linux-x64.tar.gz" -C "$SPACES_DIR"
        APP="$SPACES_DIR/SHEYTAN-Music-Visualizer-linux-x64"
        test -x "$APP/sheytan-music-visualizer" || { echo "ERROR: executable bit lost in spaces-path extraction"; exit 1; }
        "$APP/sheytan-music-visualizer" --version || { echo "ERROR: extracted EXE --version failed from a path containing spaces"; exit 1; }
        echo "spaces-path runtime check PASSED"

        # Provision the app-managed pair from the PINNED asset (proof
        # machinery only — never part of the package).
        helper_out="$(bash "$REPO_ROOT/scripts/ci/ensure-pinned-media-tools.sh")" || { echo "ERROR: pinned media-tools provisioning failed (download/SHA-256/layout)"; exit 1; }
        eval "$helper_out"
        DATA_DIR="$SPACES_DIR/SHEYTAN App Data Dir"
        MANAGED_BIN="$DATA_DIR/media-tools/current/bin"
        mkdir -p "$MANAGED_BIN"
        cp -f "$SHEYTAN_MEDIA_BIN/ffmpeg" "$MANAGED_BIN/ffmpeg"
        cp -f "$SHEYTAN_MEDIA_BIN/ffprobe" "$MANAGED_BIN/ffprobe"
        chmod 0755 "$MANAGED_BIN/ffmpeg" "$MANAGED_BIN/ffprobe"
        echo "app-managed tools provisioned from the pinned asset: $MANAGED_BIN"

        # Scrub PATH: keep every entry that does NOT carry an
        # ffmpeg/ffprobe binary (toolchain directories stay intact).
        SCRUBBED_PATH=""
        ORIG_IFS="$IFS"
        IFS=":"
        for d in $PATH; do
          [ -n "${d:-}" ] || continue
          if [ -e "$d/ffmpeg" ] || [ -e "$d/ffprobe" ] || [ -e "$d/ffmpeg.exe" ] || [ -e "$d/ffprobe.exe" ]; then continue; fi
          if [ -n "$SCRUBBED_PATH" ]; then SCRUBBED_PATH="$SCRUBBED_PATH:$d"; else SCRUBBED_PATH="$d"; fi
        done
        IFS="$ORIG_IFS"
        echo "PATH scrubbed of FFmpeg-carrying directories for the export proof"

        HOME_DIR="$SPACES_DIR/app-home"
        mkdir -p "$HOME_DIR"
        rc=0
        for codec in h264 hevc; do
          OUT="$SPACES_DIR/packaged-export-proof-$codec.mp4"
          if [ "$codec" = "h264" ]; then PROOF="$PROOF_H264"; else PROOF="$PROOF_H265"; fi
          if env -u SHEYTAN_FFMPEG SHEYTAN_DATA_DIR="$DATA_DIR" PATH="$SCRUBBED_PATH" HOME="$HOME_DIR" \
            "$APP/sheytan-music-visualizer" --export-cli \
            --export-out "$OUT" --export-w 640 --export-h 360 \
            --export-fps 30 --export-seconds 1 --export-codec "$codec"; then
            :
          else
            fail_proof "$codec" "app-managed export --export-codec $codec failed with scrubbed PATH"
            rc=1
            continue
          fi
          if [ ! -s "$OUT" ]; then
            fail_proof "$codec" "app-managed export --export-codec $codec produced no output file"
            rc=1
            continue
          fi
          # Validate with the APP-MANAGED ffprobe (the same pair the app
          # resolved): expected codec, ~30 frames, ~1 s, size > 0.
          if ! PROBE_JSON="$("$MANAGED_BIN/ffprobe" -v error -count_frames -select_streams v:0 \
              -show_entries stream=codec_name,width,height,nb_read_frames \
              -show_entries format=duration -of json "$OUT")"; then
            fail_proof "$codec" "app-managed ffprobe validation failed for $codec"
            rc=1
            continue
          fi
          if ! RESULT="$(printf "%s" "$PROBE_JSON" | python3 -c "
import json, sys
doc = json.load(sys.stdin)
stream = (doc.get(\"streams\") or [{}])[0]
duration = float((doc.get(\"format\") or {}).get(\"duration\") or 0)
print(stream.get(\"codec_name\", \"\"), int(stream.get(\"nb_read_frames\") or 0), duration)
")"; then
            fail_proof "$codec" "app-managed ffprobe output for $codec could not be parsed"
            rc=1
            continue
          fi
          CODEC_NAME="${RESULT%% *}"
          REST="${RESULT#* }"
          FRAMES="${REST%% *}"
          DURATION="${REST#* }"
          SIZE="$(stat -c%s "$OUT")"
          echo "  $codec: codec=$CODEC_NAME frames=$FRAMES duration=${DURATION}s size=$SIZE bytes"
          PROOF_FAILURES=""
          if [ "$CODEC_NAME" != "$codec" ]; then
            PROOF_FAILURES="codec [$CODEC_NAME] does not match the requested [$codec]"
          fi
          FRAMES_N="$(printf "%s" "$FRAMES" | tr -cd "0-9")"
          [ -n "$FRAMES_N" ] || FRAMES_N=0
          if [ "$FRAMES_N" -lt 28 ] || [ "$FRAMES_N" -gt 32 ]; then
            PROOF_FAILURES="${PROOF_FAILURES:+$PROOF_FAILURES; }frame count $FRAMES is not ~30 (30 fps x 1 s)"
          fi
          DURATION_OK="$(python3 -c "import sys; d=float(sys.argv[1] or 0); print(1 if 0.8 <= d <= 1.25 else 0)" "$DURATION" 2>/dev/null || echo 0)"
          if [ "$DURATION_OK" != "1" ]; then
            PROOF_FAILURES="${PROOF_FAILURES:+$PROOF_FAILURES; }duration ${DURATION}s is not ~1 s"
          fi
          if [ "$SIZE" -le 0 ]; then
            PROOF_FAILURES="${PROOF_FAILURES:+$PROOF_FAILURES; }output is empty"
          fi
          if [ -n "$PROOF_FAILURES" ]; then
            fail_proof "$codec" "app-managed export --export-codec $codec failed validation: $PROOF_FAILURES"
            rc=1
            continue
          fi
          printf "PASS %s\n" "$OUT" > "$PROOF"
          echo "app-managed export proof PASSED: $codec ($OUT)"
        done
        if [ "$rc" -ne 0 ]; then exit 1; fi
        echo "spaces-path + app-managed media-tools export proofs PASSED (h264 + hevc software; SHEYTAN_DATA_DIR app-managed pair; PATH scrubbed of system FFmpeg; app-managed ffprobe validation)"
      ' _ "$SPACES_DIR" "$LOG_DIR" "$REPO_ROOT" || fail_gate verify-extracted-path-with-spaces
      rm -rf "$SPACES_BASE"

      # COMMIT OBSERVER (run 43090342995 item D repair): the observed
      # commit comes exclusively from the machine-readable
      # --build-info-json output of BOTH the built executable and the
      # extracted-package executable, parsed as typed JSON via
      # scripts/ci/observe_commit.py (never a regex over human
      # --version text). Human --version output stays user-facing only.
      # The PERSISTED package-status record itself is written by
      # scripts/ci/write-package-status.py at the END of this script,
      # after every gate has produced its evidence. The commit_* values
      # were initialized to their honest defaults before staging; this
      # block overwrites them with the observed evidence.
      OBSERVER="$REPO_ROOT/scripts/ci/observe_commit.py"
      bi_line=""
      bi_rc="1"
      # Run-43541087097 repair: pass the REAL --build-info-json exit
      # code into the observer (Windows already did; Linux hardcoded
      # 0, so observer rule "non-zero exit never verified" was not
      # enforced on this platform).
      if bi_line="$(./bin/sheytan-music-visualizer --build-info-json 2>/dev/null)"; then
        bi_rc="0"
      fi
      built_json="$(printf '%s' "$bi_line" | python3 "$OBSERVER" "$commit_expected" "$bi_rc" || true)"
      commit_observed="$(printf '%s' "$built_json" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("observed","not-observed"))' 2>/dev/null || echo not-observed)"
      built_verified="$(printf '%s' "$built_json" | python3 -c 'import json,sys; print("true" if json.load(sys.stdin).get("verified") else "false")' 2>/dev/null || echo false)"

      VERIFY_DIR="$(mktemp -d)"
      ex_verified="false"
      if tar -xzf "$TARBALL" -C "$VERIFY_DIR" 2>/dev/null &&
         [ -x "$VERIFY_DIR/SHEYTAN-Music-Visualizer-linux-x64/sheytan-music-visualizer" ]; then
        ex_line=""
        ex_rc="1"
        if ex_line="$("$VERIFY_DIR/SHEYTAN-Music-Visualizer-linux-x64/sheytan-music-visualizer" --build-info-json 2>/dev/null)"; then
          ex_rc="0"
        fi
        extracted_json="$(printf '%s' "$ex_line" | python3 "$OBSERVER" "$commit_expected" "$ex_rc" || true)"
        commit_extracted_observed="$(printf '%s' "$extracted_json" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("observed","not-observed"))' 2>/dev/null || echo not-observed)"
        ex_verified="$(printf '%s' "$extracted_json" | python3 -c 'import json,sys; print("true" if json.load(sys.stdin).get("verified") else "false")' 2>/dev/null || echo false)"
      fi
      rm -rf "$VERIFY_DIR"

      if [ "$built_verified" = "true" ] && [ "$ex_verified" = "true" ]; then
        commit_verified="true"
        commit_reason=""
      else
        commit_reason="built_verified=$built_verified extracted_verified=$ex_verified (see observe_commit.py contract)"
      fi
    else
      skip_gate verify-extracted-path-with-spaces "tarball not created"
    fi
  else
    skip_gate stage-portable "executable build failed"
    skip_gate verify-package-license "executable build failed"
    skip_gate create-portable-tar "executable build failed"
    skip_gate verify-extracted-package "executable build failed"
    skip_gate forbid-bundled-media-tools "executable build failed"
    skip_gate package-size-budget "executable build failed"
    skip_gate media-tools-downloader-tests "executable build failed"
    skip_gate media-tools-pinned-install "executable build failed"
    skip_gate verify-extracted-path-with-spaces "executable build failed"
  fi

  # race tests on concurrent core packages
  # Race ownership: same exact single-test exclusion as go-test — the
# dedicated GPU gate owns the palette integration test; the rest of
# internal/render stays under -race. The exclusion is NOT a
# reliability waiver.
CI_TOOL_CONTEXT="gate:go-test-race" ci_capture "$JOB" go-test-race go test -race $GO_TAGS -skip '^TestPaletteReachesGPUShader$' -timeout 480s ./internal/audio/... ./internal/playback/... ./internal/dsp/... ./internal/export/... ./internal/studio/... ./internal/project/... ./internal/render/... ./internal/ghreq/... ||
    fail_gate go-test-race

  # export validation (real FFmpeg; skips honestly when unavailable).
  # Run 43612608237: the 4K120 and 4K60 functional exports and the
  # app-local CLI export diagnostic joined the canonical export gate —
  # they are integration tests with real ffprobe validation, NOT config
  # checks, and they skip only when a local FFmpeg/FFprobe pair is
  # genuinely absent. Run 43712071111: the HEVC software 1080p30 proof,
  # the custom-frame-rate proof and the size-estimate proof joined the
  # same canonical list.
  CI_TOOL_CONTEXT="gate:export-validation" ci_capture "$JOB" export-validation go test $GO_TAGS -timeout 900s -v -run 'TestExport1080p30|TestExport4K24|TestExport4K60|TestExport4K120|TestExportCancel|TestExportMissingEncoderFails|TestRunCLIExportDiagnosticAppLocal|TestExportHEVC1080p30Software|TestExportCustomFrameRates|TestExportSizeEstimates' ./internal/export/ ||
    fail_gate export-validation

  # audio pipeline validation (decoders, resampler, cache)
  CI_TOOL_CONTEXT="gate:audio-validation" ci_capture "$JOB" audio-validation go test $GO_TAGS -timeout 240s ./internal/audio/ ./internal/playback/ ||
    fail_gate audio-validation

  # GPU validation — DEDICATED OWNER of TestPaletteReachesGPUShader
  # (run 42939743766 §2.4): the palette end-to-end proof runs EXACTLY
  # ONCE per platform, here, with verbose output and its readback
  # diagnostics. The other internal/render tests are NOT duplicated
  # here; they run in go-test and go-test-race. Honest adapter note:
  # hosted runners exercise the project's supported software adapter
  # (labelled noop/software, never reported as a physical GPU); a
  # hardware run must be executed on a real machine and recorded
  # separately.
  CI_TOOL_CONTEXT="gate:gpu-validation (dedicated palette owner)" ci_capture "$JOB" gpu-validation go test $GO_TAGS -timeout 240s -v -run '^TestPaletteReachesGPUShader$' -count=1 ./internal/render/ ||
    fail_gate gpu-validation

  # studio end-to-end integration
  CI_TOOL_CONTEXT="gate:studio-integration" ci_capture "$JOB" studio-integration go test $GO_TAGS -timeout 380s ./internal/studio/ ||
    fail_gate studio-integration

  # Virtual-display GUI smoke (roadmap: the window launch was
  # historically never observed on a screen). The gate PROVES a mapped
  # application window and a graceful close; it is virtual-display
  # evidence only and never establishes real display-driver or physical
  # GPU execution. A live process alone is NOT a pass: without a mapped
  # window the gate fails even when the process survives.
  # Blocked honestly when the display/inspection tooling is missing —
  # never passed without tooling.
  #
  # PREREQUISITE GUARD (run 42598929646 defect E repair): the smoke
  # launches ./bin/sheytan-music-visualizer. When the executable build
  # failed, the gate is recorded BLOCKED with the exact reason — it
  # must NEVER attempt to execute a nonexistent binary (that produced
  # the misleading "No such file or directory" launch failure in the
  # failed run). A BLOCKED prerequisite is not a pass and not a
  # software failure.
  if [ "$EXE_OK" -eq 1 ]; then
    if command -v Xvfb >/dev/null 2>&1 && command -v xdpyinfo >/dev/null 2>&1 && command -v xwininfo >/dev/null 2>&1; then
    CI_TOOL_CONTEXT="gate:gui-smoke (virtual display Xvfb; IsViewable proof + graceful-close proof)" ci_capture "$JOB" gui-smoke bash -c '
      set -u
      SMOKE_DIR="ci-logs/gui-smoke"
      mkdir -p "$SMOKE_DIR"
      echo "VIRTUAL-DISPLAY GUI SMOKE — proves a VIEWABLE (Map State: IsViewable) X11 window and graceful close on Xvfb;"
      echo "it does NOT establish real display-driver or physical-GPU execution, and never physical audio playback."
      echo "run-42376176493 contract: an XID listed in the root tree is NOT a mapped window; only IsViewable passes."

      # 0) Xvfb identity from the PACKAGE DATABASE. The unsupported
      #    "-version" option prints an Unrecognized-option error and was
      #    previously mislabeled a successful identity string; a help
      #    probe is at most a fallback identity hint and is never
      #    reported as a version check.
      XVFB_IDENT="unknown"
      XVFB_IDENT_METHOD="none"
      if command -v dpkg-query >/dev/null 2>&1; then
        XVFB_IDENT="$(dpkg-query -W xvfb 2>/dev/null | head -1 || true)"
        [ -n "$XVFB_IDENT" ] && XVFB_IDENT_METHOD="dpkg-query"
      fi
      if [ "$XVFB_IDENT_METHOD" = "none" ]; then
        XVFB_IDENT="$(Xvfb -help 2>&1 | head -1 || true)"
        [ -n "$XVFB_IDENT" ] && XVFB_IDENT_METHOD="help-probe (identity hint only, NOT a version check)"
      fi
      echo "xvfb_identity=$XVFB_IDENT method=$XVFB_IDENT_METHOD"
      echo "xvfb_version_option_supported=no (the -version option is unsupported on this build; identity source recorded above)"
      DISPLAY_NUM=99
      export DISPLAY=":${DISPLAY_NUM}"
      echo "display=:${DISPLAY_NUM}"

      # 0b) Test-only headless audio configuration, scoped to THIS smoke:
      #     route the ALSA default PCM to the null device so the app can
      #     initialize playback without a sound card. The null device
      #     discards samples; this is CI evidence hygiene, NEVER physical
      #     audio playback proof, and never a global stderr suppression.
      ALSA_NULL_CONF="$SMOKE_DIR/asound-null.conf"
      {
        printf "%s\n" "# Test-only headless-CI audio configuration (gui-smoke process scope only)."
        printf "%s\n" "# Routes the ALSA default PCM to the null device: written samples are discarded."
        printf "%s\n" "# This is CI evidence hygiene, NOT physical audio playback proof."
        printf "%s\n" "pcm.!default {"
        printf "%s\n" "  type null"
        printf "%s\n" "}"
      } > "$ALSA_NULL_CONF"
      echo "alsa_null_pcm_config=$ALSA_NULL_CONF scope=gui-smoke-app-process-only"

      # 1) X server readiness: bounded polling, no fixed startup sleep.
      Xvfb :${DISPLAY_NUM} -screen 0 1280x800x24 >"$SMOKE_DIR/xvfb.log" 2>&1 &
      XP=$!
      X_READY=0
      for _ in $(seq 1 60); do
        if ! kill -0 "$XP" 2>/dev/null; then
          echo "ERROR: Xvfb exited during startup (see $SMOKE_DIR/xvfb.log)"
          tail -5 "$SMOKE_DIR/xvfb.log" 2>/dev/null || true
          exit 1
        fi
        if xdpyinfo >/dev/null 2>&1; then X_READY=1; break; fi
        sleep 0.1
      done
      if [ "$X_READY" -ne 1 ]; then
        echo "ERROR: X server did not become ready within the bounded deadline (xdpyinfo poll)"
        kill "$XP" 2>/dev/null || true
        exit 1
      fi
      echo "xvfb_ready=true pid=$XP"

      # 2) App launch + bounded readiness polling for a VIEWABLE window of
      #    THIS application (title contains the product name), with early
      #    exit detection. An XID in the root tree is only an EXISTENCE
      #    signal; map state must reach IsViewable to pass. A live process
      #    without a viewable window FAILS — including the
      #    run-42376176493 case where the XID existed but stayed IsUnMapped.
      ALSA_CONFIG_PATH="$PWD/$ALSA_NULL_CONF" SHEYTAN_LOG_DIR="$(pwd)/$SMOKE_DIR/app-logs" ./bin/sheytan-music-visualizer >"$SMOKE_DIR/app.log" 2>&1 &
      AP=$!
      echo "app_pid=$AP"
      WINDOW_MAPPED=0
      WINDOW_ID=""
      LAST_STATE="UNKNOWN"
      for _ in $(seq 1 200); do
        if ! kill -0 "$AP" 2>/dev/null; then
          echo "ERROR: application exited before its window became viewable (see $SMOKE_DIR/app.log)"
          tail -20 "$SMOKE_DIR/app.log" 2>/dev/null || true
          kill "$XP" 2>/dev/null || true
          exit 1
        fi
        WID=$(xwininfo -root -tree 2>/dev/null | grep -i "SHEYTAN Music Visualizer" | head -1 | grep -oE "0x[0-9a-f]+" | head -1 || true)
        if [ -n "$WID" ]; then
          WINDOW_ID="$WID"
          ms_out="$(bash scripts/ci/x11-window-mapstate.sh "$WID" 2>&1)"
          ms_rc=$?
          LAST_STATE="$(printf "%s\n" "$ms_out" | sed -n "s/^map_state=\([^ ]*\).*/\1/p" | head -1)"
          [ -n "$LAST_STATE" ] || LAST_STATE="UNKNOWN"
          if [ "$ms_rc" -eq 0 ]; then
            WINDOW_MAPPED=1
            break
          fi
        fi
        sleep 0.1
      done
      if [ "$WINDOW_MAPPED" -ne 1 ]; then
        echo "ERROR: no VIEWABLE application window within the bounded deadline (last map_state=$LAST_STATE)"
        if [ -n "$WINDOW_ID" ]; then
          echo "window_id_seen=$WINDOW_ID (XID existed but never reached IsViewable)"
          bash scripts/ci/x11-window-mapstate.sh "$WINDOW_ID" 2>&1 || true
        else
          echo "no SHEYTAN window id appeared in the root tree at all"
        fi
        xwininfo -root -tree 2>/dev/null | head -20 || true
        tail -20 "$SMOKE_DIR/app.log" 2>/dev/null || true
        kill "$AP" 2>/dev/null || true
        sleep 1
        kill -9 "$AP" 2>/dev/null || true
        kill "$XP" 2>/dev/null || true
        exit 1
      fi
      echo "window_mapped=true id=$WINDOW_ID map_state=IsViewable (XID existence alone was never sufficient)"
      xwininfo -id "$WINDOW_ID" -stats >"$SMOKE_DIR/window-stats.txt" 2>&1 || true
      grep -E "Map State|Width|Height" "$SMOKE_DIR/window-stats.txt" | tr "\n" " " | sed "s/^/window_geometry=/" || true
      head -1 "$SMOKE_DIR/window-stats.txt" || true
      WIDTH="$(sed -n "s/^ *Width: *//p" "$SMOKE_DIR/window-stats.txt" | head -1)"
      HEIGHT="$(sed -n "s/^ *Height: *//p" "$SMOKE_DIR/window-stats.txt" | head -1)"
      case "${WIDTH:-}" in ""|*[!0-9]*) WIDTH=0 ;; esac
      case "${HEIGHT:-}" in ""|*[!0-9]*) HEIGHT=0 ;; esac
      if [ "$WIDTH" -le 0 ] || [ "$HEIGHT" -le 0 ]; then
        echo "ERROR: implausible viewable-window geometry (width=$WIDTH height=$HEIGHT)"
        kill "$AP" 2>/dev/null || true
        sleep 1
        kill -9 "$AP" 2>/dev/null || true
        kill "$XP" 2>/dev/null || true
        exit 1
      fi
      echo "window_geometry_sane=true width=$WIDTH height=$HEIGHT"
      echo "map_state_at_ready=$(bash scripts/ci/x11-window-mapstate.sh "$WINDOW_ID" 2>/dev/null | head -1 || true)"

      # 3) Event-loop health: the window must STAY viewable for a bounded
      #    observation window. Map state is RE-QUERIED on every poll; a
      #    surviving but unmapped/unviewable XID (the run-42376176493
      #    false-positive class) now FAILS the gate.
      STILL_MAPPED=1
      for _ in $(seq 1 30); do
        if ! kill -0 "$AP" 2>/dev/null; then
          echo "ERROR: application died while the window was expected to stay viewable (see $SMOKE_DIR/app.log)"
          tail -20 "$SMOKE_DIR/app.log" 2>/dev/null || true
          kill "$XP" 2>/dev/null || true
          exit 1
        fi
        ms_out="$(bash scripts/ci/x11-window-mapstate.sh "$WINDOW_ID" 2>&1)"
        ms_rc=$?
        if [ "$ms_rc" -ne 0 ]; then
          echo "ERROR: the application window lost IsViewable during the observation window: $(printf "%s\n" "$ms_out" | head -1)"
          STILL_MAPPED=0
          break
        fi
        sleep 0.1
      done
      if [ "$STILL_MAPPED" -ne 1 ]; then
        echo "ERROR: the application window was destroyed, unmapped or unviewable during the observation window"
        tail -20 "$SMOKE_DIR/app.log" 2>/dev/null || true
        kill "$AP" 2>/dev/null || true
        sleep 1
        kill -9 "$AP" 2>/dev/null || true
        kill "$XP" 2>/dev/null || true
        exit 1
      fi
      echo "window_stayed_viewable=true map_state=IsViewable (3 s observation, map state re-queried on every poll)"
      echo "map_state_after_observation=$(bash scripts/ci/x11-window-mapstate.sh "$WINDOW_ID" 2>/dev/null | head -1 || true)"

      # 4) UI log errors: a panic/fatal in the app log fails the smoke
      #    even when the process survived.
      if grep -qiE "panic:|fatal error:" "$SMOKE_DIR/app.log" 2>/dev/null; then
        echo "ERROR: application log contains panic/fatal output (see $SMOKE_DIR/app.log)"
        grep -iE "panic:|fatal error:" "$SMOKE_DIR/app.log" | head -5 || true
        kill "$AP" 2>/dev/null || true
        sleep 1
        kill -9 "$AP" 2>/dev/null || true
        kill "$XP" 2>/dev/null || true
        exit 1
      fi
      echo "ui_log_clean=true (no panic/fatal markers)"

      # 5) Graceful close through the supported shutdown path (SIGTERM;
      #    no window manager runs on Xvfb, so WM_DELETE is unavailable
      #    and the process signal IS the supported path here), with a
      #    bounded wait. kill -9 is cleanup-only: if it is ever needed,
      #    the graceful-shutdown assertion has FAILED and the gate fails.
      echo "map_state_before_shutdown=$(bash scripts/ci/x11-window-mapstate.sh "$WINDOW_ID" 2>/dev/null | head -1 || true)"
      kill -TERM "$AP" 2>/dev/null || true
      CLOSED_GRACEFULLY=0
      for _ in $(seq 1 100); do
        if ! kill -0 "$AP" 2>/dev/null; then CLOSED_GRACEFULLY=1; break; fi
        sleep 0.1
      done
      if [ "$CLOSED_GRACEFULLY" -ne 1 ]; then
        echo "ERROR: application did not exit within the bounded graceful-shutdown deadline; SIGKILL cleanup follows (graceful-shutdown assertion FAILED)"
        kill -9 "$AP" 2>/dev/null || true
        wait "$AP" 2>/dev/null || true
        kill "$XP" 2>/dev/null || true
        exit 1
      fi
      wait "$AP" 2>/dev/null
      APP_RC=$?
      kill "$XP" 2>/dev/null || true
      wait "$XP" 2>/dev/null || true
      echo "graceful_close=true term_exit_code=$APP_RC"

      # 6) Headless-audio diagnostics (structured, honest, non-failing):
      #    a hosted runner has no physical audio card. ALSA messages are
      #    recorded as an explicit environment limitation with the null-PCM
      #    configuration noted; real decode/playback test failures remain
      #    the responsibility of the audio-validation gate, and a
      #    panic/fatal (checked above) still fails this gate.
      if grep -qiE "ALSA|snd_pcm|cannot find card|Unknown PCM" "$SMOKE_DIR/app.log" 2>/dev/null; then
        echo "audio_headless_diagnostic: ALSA-related messages present in the app log:"
        grep -iE "ALSA|snd_pcm|cannot find card|Unknown PCM" "$SMOKE_DIR/app.log" | head -5 || true
        echo "audio_classification=environment-limitation (no physical audio device in headless CI; test-only null PCM config was $ALSA_NULL_CONF; never physical-playback evidence)"
      else
        echo "audio_classification=clean (no ALSA device errors under the test-only null PCM configuration; still NOT physical-playback evidence)"
      fi
      echo "GUI smoke PASS: window viewable (IsViewable at readiness, after observation and before shutdown), geometry sane, UI log clean, closed gracefully on the virtual display"
    ' || fail_gate gui-smoke
    else
      blocked_gate gui-smoke "Xvfb/xdpyinfo/xwininfo unavailable on this host (window viewability cannot be proven without tooling)"
    fi
  else
    blocked_gate gui-smoke "executable build failed — GUI smoke refuses to launch a nonexistent binary (run 42598929646 defect E)"
  fi

  # govulncheck when available
  if command -v govulncheck >/dev/null 2>&1; then
    CI_TOOL_CONTEXT="gate:govulncheck" ci_capture "$JOB" govulncheck govulncheck ./... ||
      fail_gate govulncheck
  else
    skip_gate govulncheck "govulncheck not installed on this host"
  fi

  # ---- package-status record from PERSISTED EVIDENCE (run 43712071111) --
  # CONTRACT: ci-logs/package-status-linux.json is derived by the
  # canonical writer scripts/ci/write-package-status.py from PERSISTED
  # evidence ONLY — the per-gate verdict JSONs in
  # ci-logs/gate-results/<gate>.result.json, the export-proof result
  # files (export-proof-h264/h265.result.txt) and an actual scan of the
  # produced tarball. In-memory gate bookkeeping is NEVER consulted
  # (every ci_capture gate runs as a child process; the writer folds
  # commit verification, package-gate failures, the bundled-media-tools
  # scan and the media/export proofs into VERIFIED / TEST_ONLY /
  # UNAVAILABLE semantics — identical to the Windows side).
  if command -v python3 >/dev/null 2>&1; then
    if python3 "$REPO_ROOT/scripts/ci/write-package-status.py" \
      --platform linux-x64 \
      --package "$TARBALL" \
      --package-file "dist/SHEYTAN-Music-Visualizer-linux-x64.tar.gz" \
      --app-dir "SHEYTAN-Music-Visualizer-linux-x64" \
      --log-dir "$LOG_DIR" \
      --out "$LOG_DIR/package-status-linux.json" \
      --commit-expected "$commit_expected" \
      --commit-observed "$commit_observed" \
      --commit-verified "$commit_verified" \
      --commit-source "--build-info-json (built exe + extracted package, typed JSON parse)" \
      --commit-reason "$commit_reason"; then
      :
    else
      echo "ERROR: write-package-status.py failed with exit code $? — failing closed (never fabricate a status record)"
      fail_gate package-status-record
    fi
  else
    echo "ERROR: python3 not found on PATH — ci-logs/package-status-linux.json cannot be written; failing closed (never fabricate a status record)"
    fail_gate package-status-record
  fi
  [ -s "$LOG_DIR/package-status-linux.json" ] || fail_gate package-status-record
  cat "$LOG_DIR/package-status-linux.json"

  # PINNED CACHE CLEANUP (run 43712071111): every consumer of the
  # pinned media-tools proof cache (media-tools-pinned-install, the
  # app-managed export proof) has finished — remove the cache when this
  # run owns it (a caller-preset cache directory is never touched).
  if [ "$MEDIA_TOOLS_CACHE_OWNED" -eq 1 ] && [ -n "${SHEYTAN_MEDIA_TOOLS_CACHE_DIR:-}" ] && [ -d "${SHEYTAN_MEDIA_TOOLS_CACHE_DIR}" ]; then
    rm -rf "$SHEYTAN_MEDIA_TOOLS_CACHE_DIR"
    echo "pinned media-tools proof cache cleaned: $SHEYTAN_MEDIA_TOOLS_CACHE_DIR"
  fi
fi

# ---- run summary + failure index -------------------------------------------
ENDED="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
note ""
note "=============================================================="
note "RUN SUMMARY — $JOB"
note "Ended:            $ENDED"
note "Mode:             $MODE"
note "Failed gates:     $FAILURES"
if [ -n "$FAILED_GATES" ]; then note "Failed gate list:$FAILED_GATES"; fi
if [ -n "$SKIPPED_GATES" ]; then note "Skipped/blocked gate list:$SKIPPED_GATES"; fi
note ""
note "FAILURE INDEX (EXIT_CODE records with non-zero codes):"
if grep -n 'EXIT_CODE: [1-9][0-9]* FAIL' "$CI_LOG_FILE" >/dev/null 2>&1; then
  grep -n 'EXIT_CODE: [1-9][0-9]* FAIL' "$CI_LOG_FILE"
else
  note "(none — all captured commands returned exit code 0)"
fi
note ""
note "SKIPPED/BLOCKED GATES:"
if grep -n '^SKIPPED: \|^BLOCKED: ' "$CI_LOG_FILE" >/dev/null 2>&1; then
  grep -n '^SKIPPED: \|^BLOCKED: ' "$CI_LOG_FILE"
else
  note "(none)"
fi
if [ "$FAILURES" -eq 0 ]; then
  note "RUN RESULT: PASS"
else
  note "RUN RESULT: FAIL"
fi
note "=============================================================="

if [ "$FAILURES" -eq 0 ]; then
  exit 0
fi
exit 1
