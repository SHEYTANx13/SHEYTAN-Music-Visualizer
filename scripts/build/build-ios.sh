#!/usr/bin/env bash
# build-ios.sh — iOS experimental compile smoke.
#
# TRUTHFUL SCOPE (run 44330231458 update): iOS requires macOS + full
# Xcode; Wails classifies iOS as experimental and the GoGPU native
# CAMetalLayer backend is documented as planned, not shipped. This job
# claims NO simulator test and NO signed artifact. It verifies, on a
# hosted macOS runner with Xcode installed:
#   1. toolchain report (Go, Xcode identity);
#   2. a CGO cross-compile smoke of the pure product packages for
#      ios/arm64 (simulator slice not claimed);
#   3. the ASCII log contract on every captured log (civalidate).
set -u

# Load the canonical capture helpers (ci_capture, ci_sanitize).
. scripts/ci/capture.sh

JOB="build-ios"
cd "$GITHUB_WORKSPACE" || exit 1
export CI_LOG_FILE="$PWD/ci-logs/phase2-build.log"
mkdir -p ci-logs

SKIPPED_GATES=""
FAILED_GATES=""

note() { printf '%s\n' "$*" | tee -a "$CI_LOG_FILE"; }
fail_gate() { FAILED_GATES="$FAILED_GATES $1"; }
blocked_gate() { SKIPPED_GATES="$SKIPPED_GATES $1"; printf 'BLOCKED: gate %s (%s)\n' "$1" "$2" >>"$CI_LOG_FILE"; }

note "SHEYTAN MUSIC VISUALIZER :: EXPERIMENTAL IOS COMPILE SMOKE"
note "EXPERIMENTAL: no simulator validation, no signing, no GoGPU native backend claim."
note "Host: $(uname -srm)"
note "Started: $(date -u '+%Y-%m-%dT%H:%M:%SZ')"

if ! command -v go >/dev/null 2>&1; then
  blocked_gate toolchain-report "go toolchain not found on PATH"
  blocked_gate ios-compile-smoke "go toolchain not found on PATH"
  note "FINAL: FAIL"
  exit 1
fi

CI_TOOL_CONTEXT="gate:toolchain" ci_capture "$JOB" toolchain-report bash -c 'go version; xcodebuild -version 2>/dev/null | head -2 || echo "xcodebuild: NOT AVAILABLE"; xcrun --sdk iphoneos --find clang 2>/dev/null || echo "iphoneos clang: NOT AVAILABLE"' ||
  fail_gate toolchain-report

IOS_CLANG="$(xcrun --sdk iphoneos --find clang 2>/dev/null || true)"
if [ -z "$IOS_CLANG" ]; then
  blocked_gate ios-compile-smoke "iphoneos clang not found (Xcode not installed?)"
else
  if CI_TOOL_CONTEXT="gate:ios-cc" ci_capture "$JOB" ios-compile-smoke env GOOS=ios GOARCH=arm64 CGO_ENABLED=1 CC="$IOS_CLANG" go build ./internal/fileaccess/ ./internal/asciilog/ ./internal/cilog/ ./internal/logging/ ./internal/buildinfo/; then
    CI_TOOL_CONTEXT="gate:ios-vet" ci_capture "$JOB" ios-vet-smoke env GOOS=ios GOARCH=arm64 CGO_ENABLED=1 CC="$IOS_CLANG" go vet ./internal/fileaccess/ || fail_gate ios-vet-smoke
  else
    fail_gate ios-compile-smoke
  fi
fi

note ""
note "RUN SUMMARY :: $JOB"
note "Failed gates:${FAILED_GATES:- none}"
note "Blocked gates:${SKIPPED_GATES:- none}"
if [ -n "$FAILED_GATES" ]; then
  note "FINAL: FAIL"
  exit 1
fi
note "FINAL: PASS (experimental compile smoke only; no iOS runtime claim)"
exit 0
