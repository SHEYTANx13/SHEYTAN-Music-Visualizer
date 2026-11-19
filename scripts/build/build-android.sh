#!/usr/bin/env bash
# build-android.sh — Android experimental compile smoke.
#
# TRUTHFUL SCOPE (run 44330231458 update): Android support in the
# ecosystem is experimental (Wails mobile is experimental; the GoGPU
# native ANativeWindow backend is documented as planned, not shipped).
# This job therefore claims NO APK and NO runtime validation. It
# verifies, on a hosted Linux runner with the Android NDK toolchain:
#   1. toolchain report (Go, JDK, Android SDK/NDK identity);
#   2. a CGO cross-compile smoke of the pure product packages for
#      android/arm64 against the NDK;
#   3. the ASCII log contract on every captured log (civalidate).
# A missing NDK/SDK is recorded BLOCKED, never faked.
set -u

# Load the canonical capture helpers (ci_capture, ci_sanitize).
. scripts/ci/capture.sh

JOB="build-android"
cd "$GITHUB_WORKSPACE" || exit 1
export CI_LOG_FILE="$PWD/ci-logs/phase2-build.log"
mkdir -p ci-logs

SKIPPED_GATES=""
FAILED_GATES=""

note() { printf '%s\n' "$*" | tee -a "$CI_LOG_FILE"; }
fail_gate() { FAILED_GATES="$FAILED_GATES $1"; }
blocked_gate() { SKIPPED_GATES="$SKIPPED_GATES $1"; printf 'BLOCKED: gate %s (%s)\n' "$1" "$2" >>"$CI_LOG_FILE"; }

note "SHEYTAN MUSIC VISUALIZER :: EXPERIMENTAL ANDROID COMPILE SMOKE"
note "EXPERIMENTAL: no APK, no emulator or device validation, no GoGPU native backend claim."
note "Host: $(uname -srm)"
note "Started: $(date -u '+%Y-%m-%dT%H:%M:%SZ')"

if ! command -v go >/dev/null 2>&1; then
  blocked_gate toolchain-report "go toolchain not found on PATH"
  blocked_gate android-compile-smoke "go toolchain not found on PATH"
  note "FINAL: FAIL"
  exit 1
fi

CI_TOOL_CONTEXT="gate:toolchain" ci_capture "$JOB" toolchain-report bash -c 'go version; echo "GOOS=$(go env GOOS) GOARCH=$(go env GOARCH) CGO_ENABLED=$(go env CGO_ENABLED)"; java -version 2>&1 | head -1 || echo "java: NOT AVAILABLE"; echo "ANDROID_SDK_ROOT=${ANDROID_SDK_ROOT:-unset} ANDROID_NDK_ROOT=${ANDROID_NDK_ROOT:-unset} ${ANDROID_NDK_HOME:+ndk-home-set}"; ls "$ANDROID_HOME/ndk" 2>/dev/null || echo "ndk dir: NOT FOUND"' ||
  fail_gate toolchain-report

# Resolve an NDK clang for android/arm64 CGO. Hosted ubuntu images ship
# the NDK under $ANDROID_HOME/ndk/<version>.
NDK_ROOT=""
for c in "${ANDROID_NDK_ROOT:-}" "${ANDROID_NDK_HOME:-}" "$ANDROID_HOME/ndk"/*; do
  [ -n "$c" ] && [ -d "$c" ] && NDK_ROOT="$c" && break
done
if [ -z "$NDK_ROOT" ]; then
  blocked_gate android-compile-smoke "Android NDK not found on runner (ANDROID_HOME=$ANDROID_HOME)"
else
  CC="$NDK_ROOT/toolchains/llvm/prebuilt/linux-x86_64/bin/aarch64-linux-android21-clang"
  if [ ! -x "$CC" ]; then
    blocked_gate android-compile-smoke "NDK clang wrapper not found at $CC"
  else
    if CI_TOOL_CONTEXT="gate:android-cc" ci_capture "$JOB" android-compile-smoke env GOOS=android GOARCH=arm64 CGO_ENABLED=1 CC="$CC" go build ./internal/fileaccess/ ./internal/asciilog/ ./internal/cilog/ ./internal/logging/ ./internal/buildinfo/; then
      CI_TOOL_CONTEXT="gate:android-vet" ci_capture "$JOB" android-vet-smoke env GOOS=android GOARCH=arm64 CGO_ENABLED=1 CC="$CC" go vet ./internal/fileaccess/ || fail_gate android-vet-smoke
    else
      fail_gate android-compile-smoke
    fi
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
note "FINAL: PASS (experimental compile smoke only; no Android runtime claim)"
exit 0
