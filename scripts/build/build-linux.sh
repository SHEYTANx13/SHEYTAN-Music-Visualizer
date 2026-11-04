#!/usr/bin/env bash
# build-linux.sh — repeatable local/CI build gates for SHEYTAN Music
# Visualizer on Linux x64 (the required CI build target).
#
# Gates (each captured from the first byte, original exit codes preserved):
#   1. gofmt verification        (no unformatted files)
#   2. go mod tidy               (no module-graph drift vs git, when git exists)
#   3. go test ./...
#   4. go vet ./...
#   5. go build ./...            (all packages, CGO/Fyne)
#   6. native app executable     (dist/sheytan-music-visualizer-linux-x64)
#   7. --version runtime check
#   8. --self-check runtime check (no window opened)
#   9. log-collector fixture tests (verbose, named)
#
# Independent gates keep running after a failure; dependent gates are marked
# SKIPPED (never silently passed). The script exits non-zero if any required
# gate failed or was skipped-as-dependent.
#
# Output: per-step logs under ci-logs/linux/ plus CAPTURE_INDEX summary at
# ci-logs/linux/index.txt. No verbose shell tracing (secret hygiene).
set -u

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

LOGDIR="$ROOT/ci-logs/${CI_JOB_NAME:-linux}"
INDEX="$LOGDIR/index.txt"
mkdir -p "$LOGDIR"
: > "$INDEX"
export CAPTURE_INDEX="$INDEX"

CAPTURE="$ROOT/scripts/ci/capture.sh"
FAILURES=0
SKIPPED=0

note() { printf '%s\n' "$*" | tee -a "$LOGDIR/summary-notes.log"; }

# Optional generic hook for hosts without root where Fyne native dev files
# are vendored under a local prefix (see ai-notes.md). CI hosts using the
# documented apt package set do not need this.
if [ -n "${FYNE_DEPS_PREFIX:-}" ]; then
  export PKG_CONFIG_PATH="$FYNE_DEPS_PREFIX/usr/lib/x86_64-linux-gnu/pkgconfig:$FYNE_DEPS_PREFIX/usr/share/pkgconfig:${PKG_CONFIG_PATH:-}"
  export CGO_CFLAGS="-I$FYNE_DEPS_PREFIX/usr/include${CGO_CFLAGS:+ $CGO_CFLAGS}"
  export CGO_LDFLAGS="-L$FYNE_DEPS_PREFIX/usr/lib/x86_64-linux-gnu${CGO_LDFLAGS:+ $CGO_LDFLAGS}"
  note "FYNE_DEPS_PREFIX active: $FYNE_DEPS_PREFIX"
fi

note "=== SHEYTAN Music Visualizer — Linux build gates ==="
note "host: $(uname -s -m)  kernel: $(uname -r)"
note "started (UTC): $(date -u +%Y-%m-%dT%H:%M:%SZ)"

# --- toolchain report (metadata only; no environment dumps) ---
{
  echo "go: $(go version 2>&1)"
  echo "gcc: $(gcc --version 2>&1 | head -1)"
  echo "pkg-config: $(pkg-config --version 2>&1)"
  echo "go env GOOS/GOARCH/CGO_ENABLED: $(go env GOOS GOARCH CGO_ENABLED 2>&1 | tr '\n' ' ')"
  echo "git: $(git --version 2>&1)"
} > "$LOGDIR/toolchain.log" 2>&1
cat "$LOGDIR/toolchain.log"
command -v go >/dev/null 2>&1 || { echo "FATAL: go not found" | tee -a "$LOGDIR/summary-notes.log"; exit 2; }
command -v gcc >/dev/null 2>&1 || { echo "FATAL: gcc not found (Fyne requires CGO)" | tee -a "$LOGDIR/summary-notes.log"; exit 2; }

# --- 1. gofmt ---
if "$CAPTURE" linux gofmt "$LOGDIR/01-gofmt.log" -- bash -c 'unformatted=$(gofmt -l . ); if [ -n "$unformatted" ]; then echo "$unformatted"; exit 1; fi'; then :; else FAILURES=$((FAILURES+1)); fi

# --- 2. go mod tidy (no drift) ---
if "$CAPTURE" linux tidy "$LOGDIR/02-tidy.log" -- go mod tidy; then
  if command -v git >/dev/null 2>&1 && git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    if "$CAPTURE" linux tidy-drift "$LOGDIR/02-tidy-drift.log" -- git diff --exit-code -- go.mod go.sum; then :; else FAILURES=$((FAILURES+1)); fi
  else
    note "SKIPPED tidy-drift: no git worktree (extracted archive run)"
  fi
else
  FAILURES=$((FAILURES+1))
fi

# --- 3. go test ---
if "$CAPTURE" linux tests "$LOGDIR/03-test.log" -- go test ./...; then :; else FAILURES=$((FAILURES+1)); fi

# --- 4. go vet ---
if "$CAPTURE" linux vet "$LOGDIR/04-vet.log" -- go vet ./...; then :; else FAILURES=$((FAILURES+1)); fi

# --- 5. go build all packages ---
if "$CAPTURE" linux build-packages "$LOGDIR/05-build-all.log" -- go build ./...; then :; else FAILURES=$((FAILURES+1)); fi

# --- 6. native app executable ---
EXE="$ROOT/dist/sheytan-music-visualizer-linux-x64"
if [ "$FAILURES" -gt 0 ] && ! go build ./... >/dev/null 2>&1; then
  note "SKIPPED native-executable: package build failed; dependent gates cannot run"
  SKIPPED=$((SKIPPED+1))
  echo "SKIPPED: native-executable (dependent on go build)" >> "$INDEX"
else
  if "$CAPTURE" linux native-executable "$LOGDIR/06-build-exe.log" -- go build -o "$EXE" ./cmd/sheytan-music-visualizer; then
    # --- 7. --version ---
    if "$CAPTURE" linux version "$LOGDIR/07-version.log" -- "$EXE" --version; then :; else FAILURES=$((FAILURES+1)); fi
    # --- 8. --self-check (headless safe: never opens a window) ---
    if "$CAPTURE" linux self-check "$LOGDIR/08-self-check.log" -- "$EXE" --self-check; then :; else FAILURES=$((FAILURES+1)); fi
  else
    FAILURES=$((FAILURES+1))
    note "SKIPPED version+self-check: executable build failed"
    SKIPPED=$((SKIPPED+2))
    echo "SKIPPED: version+self-check (dependent on executable build)" >> "$INDEX"
  fi
fi

# --- 9. collector fixture tests (explicit, verbose) ---
if "$CAPTURE" linux collector-fixtures "$LOGDIR/09-collector-fixtures.log" -- go test -v ./internal/cilogreport/; then :; else FAILURES=$((FAILURES+1)); fi

# --- summary ---
ended="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
{
  echo "=== RUN SUMMARY — Linux build gates ==="
  echo "product: SHEYTAN Music Visualizer (SHEYTAN™)"
  echo "host: $(uname -s -m)"
  echo "started: $(sed -n 's/^started (UTC): //p' "$LOGDIR/summary-notes.log" | head -1)"
  echo "ended (UTC): $ended"
  echo
  echo "Per-command records (from $INDEX):"
  awk -F'|' '{ printf "  %-18s exit=%-4s %-4s log=%s\n", $2, $4, $5, $3 }' "$INDEX"
  if [ "$SKIPPED" -gt 0 ]; then
    echo "Skipped-as-dependent gates: $SKIPPED (listed above; never counted as passing)"
  fi
  echo
  if [ "$FAILURES" -gt 0 ]; then
    echo "FAILURE INDEX — $FAILURES failed gate(s):"
    awk -F'|' '$5 == "FAIL" { printf "  %-18s exit=%s log=%s\n", $2, $4, $3 }' "$INDEX"
  else
    echo "FAILURE INDEX: (none)"
  fi
} > "$LOGDIR/summary.log"
cat "$LOGDIR/summary.log"

if [ "$FAILURES" -gt 0 ]; then
  exit 1
fi
exit 0
