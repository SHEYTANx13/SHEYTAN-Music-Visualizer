#!/usr/bin/env bash
# startup-bench.sh — reproducible cold/warm startup measurement for the
# SHEYTAN Music Visualizer desktop executable (run 42837709122 §5.4).
#
# Method:
#   - The executable is launched N times under a virtual X display
#     (Xvfb). Each launch runs until the structured log record
#     "startup stages complete" appears (the app records its own
#     first-window-usable stage from the true OS process-start anchor
#     /proc/self/stat), then receives SIGTERM for a clean shutdown.
#   - COLD runs use a FRESH data root each time (first-launch data
#     creation included). WARM runs reuse one data root (normal launch),
#     detecting each new record by log line offset.
#   - The reported number is the app-recorded stage, not process wall
#     time and not build time.
#
# Requirements on the measurement host: bash, the built executable
# (bin/sheytan-music-visualizer), Xvfb + a working GL stack for the
# window system. The numbers are HOST-SPECIFIC samples — they are never
# universal claims; compare only same-host same-commit runs.
#
# Usage: scripts/bench/startup-bench.sh [samples] [bin path]
set -u
SAMPLES="${1:-5}"
BIN="${2:-bin/sheytan-music-visualizer}"
WORK="$(mktemp -d)"
trap 'pkill -f "Xvfb :77" 2>/dev/null; rm -rf "$WORK"' EXIT

cd "$(dirname "${BASH_SOURCE[0]}")/../.." || exit 3
REPO="$PWD"
# Run-43541087097 repair: an ABSOLUTE bin path is used as-is; a relative
# path is resolved against the repository root (the old code checked
# -x "$BIN" but always launched "$REPO/$BIN", so an absolute path passed
# the check and then launched a nonexistent file — every sample FAILed).
case "$BIN" in
  /*) LAUNCH_BIN="$BIN" ;;
  *)  LAUNCH_BIN="$REPO/$BIN" ;;
esac
if [ ! -x "$LAUNCH_BIN" ]; then
  echo "ERROR: executable not found: $LAUNCH_BIN (build it first: go build -tags goffi_static -o bin/sheytan-music-visualizer ./cmd/sheytan-music-visualizer)" >&2
  exit 2
fi
command -v Xvfb >/dev/null 2>&1 || { echo "ERROR: Xvfb not available (cannot measure a GUI launch on this host)" >&2; exit 2; }

(Xvfb :77 -screen 0 1280x800x24 >/dev/null 2>&1 &)
sleep 1

extract_ms() { # extract_ms <logfile> [from-line] -> ms or empty
  local f="$1" from="${2:-1}"
  tail -n +"$from" "$f" 2>/dev/null | grep "startup stages complete" | head -1 | \
    sed -n 's/.*first-window-usable=\([0-9]\+\)ms.*/\1/p'
}

run_cold() { # run_cold <data-root> -> prints elapsed ms or FAIL
  local root="$1" logfile ms pid waited=0 start_size
  rm -rf "$root"; mkdir -p "$root"
  DISPLAY=:77 SHEYTAN_DATA_DIR="$root" "$LAUNCH_BIN" >/dev/null 2>&1 &
  pid=$!
  logfile="$root/logs/app.log"
  while [ $waited -lt 300 ]; do
    if [ -f "$logfile" ] && grep -q "startup stages complete" "$logfile" 2>/dev/null; then break; fi
    kill -0 "$pid" 2>/dev/null || break
    sleep 0.1; waited=$((waited+1))
  done
  ms=$(extract_ms "$logfile")
  kill "$pid" 2>/dev/null
  wait "$pid" 2>/dev/null
  [ -n "$ms" ] && echo "$ms" || echo "FAIL"
}

run_warm() { # run_warm <shared-data-root> -> prints elapsed ms or FAIL
  local root="$1" logfile ms pid waited=0 start_size
  logfile="$root/logs/app.log"
  start_size=$(wc -l < "$logfile" 2>/dev/null || echo 0)
  DISPLAY=:77 SHEYTAN_DATA_DIR="$root" "$LAUNCH_BIN" >/dev/null 2>&1 &
  pid=$!
  while [ $waited -lt 300 ]; do
    if [ -f "$logfile" ] && tail -n +"$((start_size+1))" "$logfile" 2>/dev/null | grep -q "startup stages complete"; then break; fi
    kill -0 "$pid" 2>/dev/null || break
    sleep 0.1; waited=$((waited+1))
  done
  ms=$(extract_ms "$logfile" "$((start_size+1))")
  kill "$pid" 2>/dev/null
  wait "$pid" 2>/dev/null
  [ -n "$ms" ] && echo "$ms" || echo "FAIL"
}

median() {
  sort -n | awk '{a[NR]=$1} END {if (NR%2) print a[(NR+1)/2]; else print (a[NR/2]+a[NR/2+1])/2}'
}

echo "startup-bench: host=$(uname -srm) samples=$SAMPLES bin=$BIN"
echo "cpu: $(grep -m1 'model name' /proc/cpuinfo 2>/dev/null | cut -d: -f2 | sed 's/^ *//')"

COLD_VALS=""
for i in $(seq 1 "$SAMPLES"); do
  ms=$(run_cold "$WORK/cold-$i")
  echo "cold[$i]: ${ms}ms"
  [ "$ms" != "FAIL" ] && COLD_VALS="$COLD_VALS $ms"
done

WARM_ROOT="$WORK/warm-root"; mkdir -p "$WARM_ROOT"
run_cold "$WARM_ROOT" >/dev/null   # prime: first-launch data creation
WARM_VALS=""
for i in $(seq 1 "$SAMPLES"); do
  ms=$(run_warm "$WARM_ROOT")
  echo "warm[$i]: ${ms}ms"
  [ "$ms" != "FAIL" ] && WARM_VALS="$WARM_VALS $ms"
done

echo "---"
echo "cold median (first-window-usable): $(echo $COLD_VALS | tr ' ' '\n' | median) ms"
echo "warm median (first-window-usable): $(echo $WARM_VALS | tr ' ' '\n' | median) ms"
echo "commit: $(git -C "$REPO" rev-parse HEAD 2>/dev/null || echo unknown)"
echo "NOTE: host-specific samples; compare only same-host same-commit runs."
