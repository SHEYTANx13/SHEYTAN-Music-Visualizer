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

  # GUI smoke on a virtual display when Xvfb is available (roadmap:
  # the window launch was historically never observed on a screen).
  # SKIPPED honestly when no display tooling exists on this host.
  if command -v Xvfb >/dev/null 2>&1; then
    CI_TOOL_CONTEXT="gate:gui-smoke" ci_capture "$JOB" gui-smoke bash -c '
      set -u
      Xvfb :99 -screen 0 1280x800x24 >/dev/null 2>&1 &
      XP=$!
      sleep 1.5
      if ! kill -0 "$XP" 2>/dev/null; then
        echo "ERROR: Xvfb failed to start"; exit 1
      fi
      DISPLAY=:99 SHEYTAN_LOG_DIR="$(pwd)/ci-logs/gui-smoke-logs" ./bin/sheytan-music-visualizer >/dev/null 2>&1 &
      AP=$!
      sleep 8
      ALIVE=0
      if kill -0 "$AP" 2>/dev/null; then ALIVE=1; fi
      kill "$AP" 2>/dev/null
      sleep 1.5
      kill -9 "$AP" 2>/dev/null
      wait "$AP" 2>/dev/null
      kill "$XP" 2>/dev/null
      if [ "$ALIVE" -ne 1 ]; then
        echo "ERROR: app did not stay alive on the virtual display (window/event-loop failure)"
        exit 1
      fi
      echo "GUI smoke PASS: the studio window survived 8 s on Xvfb and terminated"
    ' || fail_gate gui-smoke
  else
    skip_gate gui-smoke "Xvfb not available on this host"
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
