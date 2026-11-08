#!/usr/bin/env bash
# x11-window-mapstate.sh — report and validate the X11 map state of ONE
# window id (run-42376176493 repair, GUI-smoke phase 7).
#
# Defect this repairs: the GUI smoke set WINDOW_MAPPED=1 merely because the
# window appeared in `xwininfo -root -tree`, and its "stay mapped" loop only
# re-queried the XID. An unmapped window appears in the root tree and its
# XID keeps answering queries, so the run reported window_mapped=true and
# window_stayed_mapped=true while the saved window-stats.txt said
# `Map State: IsUnMapped` — a false positive.
#
# Xlib defines exactly three map states (XGetWindowAttributes -> map_state):
#   IsUnmapped   — the window is not mapped;
#   IsUnviewable — mapped, but an ancestor is not viewable;
#   IsViewable   — mapped and every ancestor is viewable (visible).
# Only IsViewable is evidence for a "visible window" assertion.
#
# Usage:   x11-window-mapstate.sh <window-id>
# stdout:  map_state=<IsUnmapped|IsUnviewable|IsViewable|UNKNOWN> width=<n> height=<n>
#          plus the raw xwininfo stats when the query itself fails.
# exit 0   the window IS IsViewable (the only passing state)
# exit 1   a definite non-viewable state (IsUnmapped / IsUnviewable)
# exit 2   query failure (no id given, xwininfo missing, XID gone, no server)
set -u

wid="${1:-}"
if [ -z "$wid" ]; then
  echo "map_state=UNKNOWN reason=no-window-id"
  exit 2
fi
if ! command -v xwininfo >/dev/null 2>&1; then
  echo "map_state=UNKNOWN reason=xwininfo-not-found"
  exit 2
fi

stats="$(xwininfo -id "$wid" -stats 2>&1)"
rc=$?
if [ "$rc" -ne 0 ]; then
  echo "map_state=UNKNOWN reason=xwininfo-rc-${rc}"
  printf '%s\n' "$stats"
  exit 2
fi

state="$(printf '%s\n' "$stats" | sed -n 's/^.*Map State: *//p' | head -1)"
width="$(printf '%s\n' "$stats" | sed -n 's/^.*Width: *//p' | head -1)"
height="$(printf '%s\n' "$stats" | sed -n 's/^.*Height: *//p' | head -1)"
echo "map_state=${state:-UNKNOWN} width=${width:-?} height=${height:-?}"

# xwininfo prints the map state with its own capitalization
# ("Map State: IsUnMapped" — exactly the run-42376176493 evidence), while
# the Xlib constants are IsUnmapped/IsUnviewable/IsViewable. Compare
# case-insensitively so BOTH spellings classify correctly; an unknown
# value stays a query failure (exit 2), never a silent pass.
case "$(printf '%s' "$state" | tr '[:upper:]' '[:lower:]')" in
  isviewable)
    exit 0
    ;;
  isunmapped | isunviewable)
    exit 1
    ;;
  *)
    echo "map_state_raw: $state (expected one of IsUnmapped/IsUnmapped/IsUnviewable/IsViewable)"
    exit 2
    ;;
esac
