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
    for g in go-test go-vet go-build-packages build-executable run-version run-self-check go-test-race export-validation audio-validation gpu-validation studio-integration; do
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

CI_TOOL_CONTEXT="gate:go-test" ci_capture "$JOB" go-test go test $GO_TAGS ./... ||
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

  # ---- portable package staging (run 42481815519 directory contract) ----
  # One top-level application folder: executable, license.md, readme.md,
  # portable.mode marker, tools/ffmpeg app-local dependency location and
  # the documented data/ bootstrap. A tar.gz preserves the executable
  # bit (a ZIP would not), so the portable contract survives extraction.
  STAGE_ROOT="$REPO_ROOT/dist/SHEYTAN-Music-Visualizer-linux-x64-portable"
  STAGE_APP="$STAGE_ROOT/SHEYTAN-Music-Visualizer-linux-x64"
  TARBALL="$REPO_ROOT/dist/SHEYTAN-Music-Visualizer-linux-x64-portable.tar.gz"
  rm -rf "$STAGE_ROOT" "$TARBALL"
  mkdir -p "$STAGE_APP/tools/ffmpeg/bin" "$STAGE_APP/data"
  if [ "$EXE_OK" -eq 1 ]; then
    install -m 0755 bin/sheytan-music-visualizer "$STAGE_APP/sheytan-music-visualizer"
    install -m 0644 license.md "$STAGE_APP/license.md"
    install -m 0644 readme.md "$STAGE_APP/readme.md"
    install -m 0644 packaging/portable.mode "$STAGE_APP/portable.mode"
    install -m 0644 packaging/DATA-FOLDERS.txt "$STAGE_APP/data/DATA-FOLDERS.txt"
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
        echo "staged portable tree:"
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
      tar -czf "dist/SHEYTAN-Music-Visualizer-linux-x64-portable.tar.gz" -C "dist/SHEYTAN-Music-Visualizer-linux-x64-portable" "SHEYTAN-Music-Visualizer-linux-x64"
      test -s "dist/SHEYTAN-Music-Visualizer-linux-x64-portable.tar.gz" || { echo "ERROR: tarball missing/empty"; exit 1; }
      echo "tarball contents:"
      tar -tzf "dist/SHEYTAN-Music-Visualizer-linux-x64-portable.tar.gz"
    ' || fail_gate create-portable-tar

    if [ -s "$TARBALL" ]; then
      VERIFY_DIR="$(mktemp -d)"
      CI_TOOL_CONTEXT="gate:package-verify (extracted tarball)" ci_capture "$JOB" verify-extracted-package bash -c '
        set -euo pipefail
        VERIFY_DIR="$1"
        tar -xzf "dist/SHEYTAN-Music-Visualizer-linux-x64-portable.tar.gz" -C "$VERIFY_DIR"
        APP="$VERIFY_DIR/SHEYTAN-Music-Visualizer-linux-x64"
        cmp license.md "$APP/license.md" || { echo "ERROR: DISTRIBUTED license.md differs from canonical (post-archive)"; exit 1; }
        test -x "$APP/sheytan-music-visualizer" || { echo "ERROR: executable bit lost in tarball"; exit 1; }
        test -f "$APP/portable.mode" || { echo "ERROR: portable.mode missing in tarball"; exit 1; }
        test -f "$APP/tools/ffmpeg/FFMPEG-TOOLS-LICENSING.md" || { echo "ERROR: ffmpeg licensing doc missing in tarball"; exit 1; }
        echo "extracted package verified: license byte-identical, executable bit preserved, portable.mode present"
      ' _ "$VERIFY_DIR" || fail_gate verify-extracted-package
      rm -rf "$VERIFY_DIR"
    else
      skip_gate verify-extracted-package "tarball not created"
    fi
  else
    skip_gate stage-portable "executable build failed"
    skip_gate verify-package-license "executable build failed"
    skip_gate create-portable-tar "executable build failed"
    skip_gate verify-extracted-package "executable build failed"
  fi

  # race tests on concurrent core packages
  CI_TOOL_CONTEXT="gate:go-test-race" ci_capture "$JOB" go-test-race go test -race $GO_TAGS -timeout 480s ./internal/audio/... ./internal/playback/... ./internal/dsp/... ./internal/export/... ./internal/studio/... ./internal/project/... ./internal/render/... ||
    fail_gate go-test-race

  # export validation (real FFmpeg; skips honestly when unavailable)
  CI_TOOL_CONTEXT="gate:export-validation" ci_capture "$JOB" export-validation go test $GO_TAGS -timeout 560s -v -run 'TestExport1080p30|TestExport4K24|TestExportCancel|TestExportMissingEncoderFails' ./internal/export/ ||
    fail_gate export-validation

  # audio pipeline validation (decoders, resampler, cache)
  CI_TOOL_CONTEXT="gate:audio-validation" ci_capture "$JOB" audio-validation go test $GO_TAGS -timeout 240s ./internal/audio/ ./internal/playback/ ||
    fail_gate audio-validation

  # GPU validation: wgpu render/readback proof on the noop/software
  # adapter (honest: this host has no hardware GPU; a hardware run must
  # be executed on a real machine and recorded separately)
  CI_TOOL_CONTEXT="gate:gpu-validation" ci_capture "$JOB" gpu-validation go test $GO_TAGS -timeout 240s -v ./internal/render/ ||
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
