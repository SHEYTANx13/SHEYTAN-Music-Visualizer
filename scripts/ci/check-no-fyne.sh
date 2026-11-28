#!/usr/bin/env bash
# check-no-fyne.sh — the production-source Fyne-removal gate.
#
# Contract (Go-Gui/Wails migration, run 44462492262): Fyne was the
# production UI and was removed ONLY after feature parity in the new
# shells. From that point the repository must stay clean:
#   1. ui/fyne does not exist.
#   2. No production Go source file imports fyne.io/fyne.
#   3. go.mod / go.sum do not reference Fyne modules.
#   4. cmd/sheytan-music-visualizer does not instantiate a window
#      (it is the toolkit-free CLI authority; the shells have their
#      own entry points).
#
# Historical mentions inside comments, changelogs and evidence notes
# are allowed (history stays history); this gate checks IMPORTS,
# MODULE DEPENDENCIES and the production directory, not prose.
# Repair/run-46033044814: the retired-UI guard now covers BOTH removed
# toolkits — Fyne (run 44462492262) and the Go-Gui desktop shell
# (ui/gogui + cmd/app-gogui, removed when cmd/app-wails became the only
# desktop UI). The gomobile Android carrier (cmd/app-android) is the
# one sanctioned remaining native entry.
set -Eeuo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

fail=0

if [ -d ui/fyne ]; then
  echo "FAIL: ui/fyne exists; the Fyne production UI must be removed after verified parity"
  fail=1
fi

if [ -d ui/gogui ]; then
  echo "FAIL: ui/gogui exists; the Go-Gui desktop shell was removed (the desktop product is cmd/app-wails)"
  fail=1
fi

if [ -d cmd/app-gogui ]; then
  echo "FAIL: cmd/app-gogui exists; the Go-Gui desktop shell was removed (the Android carrier is cmd/app-android)"
  fail=1
fi

# Production Go imports (exclude testdata and vendor if ever present).
while IFS= read -r -d '' f; do
  echo "FAIL: production Go file imports Fyne: $f"
  fail=1
done < <(find . -type f -name '*.go' -not -path './.git/*' -not -path './vendor/*' -print0 \
  | xargs -0 grep -l 'fyne\.io/fyne' 2>/dev/null || true)

if grep -q 'fyne' go.mod 2>/dev/null; then
  echo "FAIL: go.mod still references Fyne"
  fail=1
fi
if grep -q 'fyne' go.sum 2>/dev/null; then
  echo "FAIL: go.sum still references Fyne"
  fail=1
fi
if grep -q 'go-gui' go.mod 2>/dev/null; then
  echo "FAIL: go.mod still references the go-gui module"
  fail=1
fi
if grep -q 'go-gui' go.sum 2>/dev/null; then
  echo "FAIL: go.sum still references the go-gui module"
  fail=1
fi

# The CLI authority must not open windows: importing a shell package is
# a second production UI by stealth.
if grep -rn 'SHEYTAN-Music-Visualizer/ui/' cmd/sheytan-music-visualizer/ >/dev/null 2>&1; then
  echo "FAIL: cmd/sheytan-music-visualizer imports a shell UI package; it must stay toolkit-free"
  fail=1
fi

if [ "$fail" -ne 0 ]; then
  echo "check-no-fyne: FAIL"
  exit 1
fi
echo "check-no-fyne: PASS (no Fyne or Go-Gui production UI, imports, or module dependencies)"
