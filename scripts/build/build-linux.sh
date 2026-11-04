#!/usr/bin/env bash
# build-linux.sh — Phase 1 local build/verification entry point (Linux).
#
# Usage:
#   scripts/build/build-linux.sh [--mode preflight|desktop]
#
# Modes:
#   preflight  Static gates only: toolchain report, gofmt, tidy, tests,
#              vet, package build. Used by the CI preflight job.
#   desktop    All preflight gates plus the native Linux x64 desktop
#              executable, --version and --self-check runtime checks.
#              Default.
#
# Contract:
#   - Every command's stdout+stderr is captured from the first byte into
#     ci-logs/phase1-build.log with a structured record and the original
#     exit code.
#   - Independent gates keep running after a failure; dependent gates are
#     recorded as SKIPPED, never as passed.
#   - The script exits non-zero if any required gate failed.

set -u
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
CI_LOG_FILE="$LOG_DIR/phase1-build.log"
export CI_LOG_FILE
: >"$CI_LOG_FILE" # fresh local run; the packaged copy is evidence/phase1-build.log

# shellcheck source=../ci/capture.sh
source "$REPO_ROOT/scripts/ci/capture.sh"

JOB="phase1-linux-build"
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

note "=============================================================="
note "SHEYTAN MUSIC VISUALIZER — PHASE 1 LOCAL VERIFICATION (LINUX)"
note "Product:  SHEYTAN Music Visualizer (SHEYTAN™, licensor Parsaetak)"
note "Mode:     $MODE"
note "Host:     $(uname -srm)"
note "Started:  $(date -u '+%Y-%m-%dT%H:%M:%SZ')"
note "=============================================================="

# ---- toolchain report ------------------------------------------------------
CI_TOOL_CONTEXT="gate:toolchain" ci_capture "$JOB" toolchain-report bash -c 'go version; echo "GOOS=$(go env GOOS) GOARCH=$(go env GOARCH) CGO_ENABLED=$(go env CGO_ENABLED) GOTOOLCHAIN=$(go env GOTOOLCHAIN)"; echo "GOMOD=$(go env GOMOD)"; gcc --version | head -1; pkg-config --version' ||
  fail_gate toolchain-report

# ---- static gates (all independent; always run) ----------------------------
CI_TOOL_CONTEXT="gate:gofmt" ci_capture "$JOB" gofmt-verify bash -c 'out=$(gofmt -l .); if [ -n "$out" ]; then printf "%s\n" "$out"; echo "ERROR: gofmt reported unformatted files"; exit 1; fi; echo "gofmt clean"' ||
  fail_gate gofmt-verify

CI_TOOL_CONTEXT="gate:go-mod-tidy" ci_capture "$JOB" go-mod-tidy go mod tidy ||
  fail_gate go-mod-tidy

CI_TOOL_CONTEXT="gate:go-mod-tidy" ci_capture "$JOB" go-mod-tidy-verify go mod tidy -diff ||
  fail_gate go-mod-tidy-verify

CI_TOOL_CONTEXT="gate:go-test" ci_capture "$JOB" go-test go test ./... ||
  fail_gate go-test

CI_TOOL_CONTEXT="gate:go-vet" ci_capture "$JOB" go-vet go vet ./... ||
  fail_gate go-vet

CI_TOOL_CONTEXT="gate:go-build" ci_capture "$JOB" go-build-packages go build ./... ||
  fail_gate go-build-packages

if [ "$MODE" = "preflight" ]; then
  note ""
  note "Preflight mode requested: desktop executable and runtime checks are NOT part of this run."
fi

# ---- desktop executable + runtime checks (mode: desktop) -------------------
EXE_OK=0
if [ "$MODE" = "desktop" ]; then
  LDFLAGS=()
  if [ -n "${SHEYTAN_BUILD_VERSION:-}" ]; then
    LDFLAGS=(-ldflags "-X github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/buildinfo.version=${SHEYTAN_BUILD_VERSION}")
  fi
  if CI_TOOL_CONTEXT="gate:build-exe" ci_capture "$JOB" build-executable go build -trimpath "${LDFLAGS[@]+"${LDFLAGS[@]}"}" -o bin/sheytan-music-visualizer ./cmd/sheytan-music-visualizer; then
    EXE_OK=1
  else
    fail_gate build-executable
  fi

  if [ "$EXE_OK" -eq 1 ]; then
    CI_TOOL_CONTEXT="runtime:--version" ci_capture "$JOB" run-version ./bin/sheytan-music-visualizer --version ||
      fail_gate run-version
    # --self-check writes the app log under the user config directory;
    # on headless CI hosts this is $HOME/.config and is writable.
    CI_TOOL_CONTEXT="runtime:--self-check" ci_capture "$JOB" run-self-check ./bin/sheytan-music-visualizer --self-check ||
      fail_gate run-self-check
  else
    skip_gate run-version "executable build failed"
    skip_gate run-self-check "executable build failed"
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
if [ -n "$SKIPPED_GATES" ]; then note "Skipped gate list:$SKIPPED_GATES"; fi
note ""
note "FAILURE INDEX (EXIT_CODE records with non-zero codes):"
if grep -n 'EXIT_CODE: [1-9][0-9]* FAIL' "$CI_LOG_FILE" >/dev/null 2>&1; then
  grep -n 'EXIT_CODE: [1-9][0-9]* FAIL' "$CI_LOG_FILE"
else
  note "(none — all captured commands returned exit code 0)"
fi
note ""
note "SKIPPED GATES:"
if grep -n '^SKIPPED: ' "$CI_LOG_FILE" >/dev/null 2>&1; then
  grep -n '^SKIPPED: ' "$CI_LOG_FILE"
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
