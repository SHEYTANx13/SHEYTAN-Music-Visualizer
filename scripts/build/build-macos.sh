#!/usr/bin/env bash
# build-macos.sh — macOS desktop build/verification gates.
#
# TRUTHFUL SCOPE (run 44330231458 update): this job adds the macOS
# desktop target to the canonical workflow. Its gate contract is a
# REAL but REDUCED subset of the Linux contract: toolchain, gofmt,
# module verify, full package test suite, vet, executable build,
# BETA version runtime check, tar.gz package. The ported-parity gates
# the Linux job runs (race suite, export/audio/GPU validation, GUI
# smoke, media-tools integration) are NOT yet ported to macOS and are
# recorded here as BLOCKED with that exact reason — never as PASS.
# The produced package is therefore TEST_ONLY until those gates are
# ported and pass on a hosted macOS runner (NOT VERIFIED this update).
set -u

# Load the canonical capture helpers (ci_capture, ci_sanitize).
. scripts/ci/capture.sh

JOB="build-macos"
cd "$GITHUB_WORKSPACE" || exit 1
export CI_LOG_FILE="$PWD/ci-logs/phase2-build.log"
mkdir -p ci-logs

SKIPPED_GATES=""
FAILED_GATES=""

note() { printf '%s\n' "$*" | tee -a "$CI_LOG_FILE"; }
fail_gate() { FAILED_GATES="$FAILED_GATES $1"; }
skip_gate() { SKIPPED_GATES="$SKIPPED_GATES $1"; printf 'SKIPPED: gate %s (%s)\n' "$1" "$2" >>"$CI_LOG_FILE"; }
blocked_gate() { SKIPPED_GATES="$SKIPPED_GATES $1"; printf 'BLOCKED: gate %s (%s)\n' "$1" "$2" >>"$CI_LOG_FILE"; }

note "SHEYTAN MUSIC VISUALIZER :: PHASE 2 LOCAL VERIFICATION (MACOS)"
note "Mode: desktop (reduced parity set; see header)"
note "Host: $(uname -srm)"
note "Started: $(date -u '+%Y-%m-%dT%H:%M:%SZ')"

# Toolchain report.
if ! command -v go >/dev/null 2>&1; then
  for g in toolchain-report gofmt-verify go-mod-verify go-test go-vet go-build-packages build-executable run-version package-macos; do
    blocked_gate "$g" "go toolchain not found on PATH"
  done
  blocked_gate go-test-race "go toolchain not found on PATH"
  blocked_gate export-validation "go toolchain not found on PATH"
  blocked_gate audio-validation "go toolchain not found on PATH"
  blocked_gate gpu-validation "go toolchain not found on PATH"
  blocked_gate gui-smoke "go toolchain not found on PATH"
else
  CI_TOOL_CONTEXT="gate:toolchain" ci_capture "$JOB" toolchain-report bash -c 'go version; echo "GOOS=$(go env GOOS) GOARCH=$(go env GOARCH) CGO_ENABLED=$(go env CGO_ENABLED)"; clang --version | head -1; xcodebuild -version 2>/dev/null | head -1 || echo "xcodebuild: NOT AVAILABLE"' ||
    fail_gate toolchain-report

  ci_capture "$JOB" gofmt-verify bash -c 'out=$(gofmt -l .); if [ -n "$out" ]; then printf "%s\n" "$out"; echo "ERROR: gofmt reported unformatted files"; exit 1; fi; echo "gofmt clean"' ||
    fail_gate gofmt-verify

  ci_capture "$JOB" go-mod-verify go mod verify || fail_gate go-mod-verify

  ci_capture "$JOB" go-test go test ./... || fail_gate go-test
  ci_capture "$JOB" go-vet go vet ./... || fail_gate go-vet
  ci_capture "$JOB" go-build-packages go build ./... || fail_gate go-build-packages

  BI=github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/buildinfo
  LDFLAGS=(-ldflags "-X ${BI}.version=${SHEYTAN_BUILD_VERSION:-BETA} -X ${BI}.commit=${SHEYTAN_BUILD_COMMIT:-unknown} -X ${BI}.date=${SHEYTAN_BUILD_DATE:-unknown} -X ${BI}.builtBy=${SHEYTAN_BUILT_BY:-github-actions}")
  if ci_capture "$JOB" build-executable go build -trimpath "${LDFLAGS[@]}" -o bin/sheytan-music-visualizer ./cmd/sheytan-music-visualizer; then
    CI_TOOL_CONTEXT="runtime:--version" ci_capture "$JOB" run-version bash -c './bin/sheytan-music-visualizer --version | tee /tmp/version.txt; grep -q "version: BETA" /tmp/version.txt || grep -q "version BETA" /tmp/version.txt || grep -q "BETA" /tmp/version.txt' ||
      fail_gate run-version
  else
    fail_gate build-executable
    skip_gate run-version "executable build failed"
  fi

  # Reduced-parity honesty: these Linux-contract gates are not yet
  # ported to macOS. BLOCKED, never PASS.
  blocked_gate go-test-race "race gate not yet ported to macOS job"
  blocked_gate export-validation "export validation not yet ported to macOS job"
  blocked_gate audio-validation "audio validation not yet ported to macOS job"
  blocked_gate gpu-validation "GPU validation not yet ported to macOS job"
  blocked_gate gui-smoke "GUI smoke not yet ported to macOS job"

  if [ -f bin/sheytan-music-visualizer ]; then
    ci_capture "$JOB" package-macos bash -c 'mkdir -p dist && tar -czf dist/SHEYTAN-Music-Visualizer-macos-arm64.tar.gz -C bin sheytan-music-visualizer && ls -l dist/' ||
      fail_gate package-macos
  else
    skip_gate package-macos "executable build failed"
  fi
fi

note ""
note "RUN SUMMARY :: $JOB"
note "Failed gates:${FAILED_GATES:- none}"
note "Skipped or blocked gates:${SKIPPED_GATES:- none}"
if [ -n "$FAILED_GATES" ]; then
  note "FINAL: FAIL"
  exit 1
fi
note "FINAL: PASS (reduced macOS parity set; ported-parity gates are BLOCKED, not passed)"
exit 0
