#!/usr/bin/env bash
# forbid-bundled-media-tools.sh — NEGATIVE package gate (bash side).
#
# Contract (run 43712071111): the distributed application archive must
# contain NO bundled media tools. FFmpeg/ffprobe are app-managed
# downloads (the in-app Media Tools & Codecs manager installs the pinned
# pair under <DataRoot>/media-tools/current/bin, never into the package).
# The gate extracts the archive to a temporary directory, walks the
# EXTRACTED files and also scans the raw archive entry list, and FAILS
# when any of the following is present:
#
#   1. any file (or entry) whose basename is ffmpeg/ffprobe in any case,
#      with or without the .exe suffix;
#   2. any *.dll under a tools/ directory;
#   3. any nested archive (*.zip, *.tar, *.tar.gz, *.tar.xz, *.7z);
#   4. any single file larger than 40 MiB (41943040 bytes).
#
# The gate ALWAYS prints the archive's total size, the packaged file
# count and the top-10 largest files, so a failure is diagnosable from
# the captured log alone.
#
# Usage:
#   scripts/ci/forbid-bundled-media-tools.sh <archive> [max-file-bytes]
#     archive         .tar.gz/.tar.xz (Linux package) or .zip
#     max-file-bytes  per-file budget in bytes (default 41943040 = 40 MiB)
#
# Exit codes: 0 = clean, 1 = violation found, 2 = usage/IO error.
#
# This script is deliberately reusable so the packaging-gate fixtures
# (scripts/ci/tests/test-package-gates.sh, fixture P11) can prove BOTH
# directions: a synthetic package containing a fake tools/ffmpeg/bin/
# ffmpeg binary is REJECTED, and a clean compact package is ACCEPTED.
set -Eeuo pipefail

if [ "$#" -lt 1 ] || [ "$#" -gt 2 ]; then
  echo "usage: $0 <archive> [max-file-bytes]" >&2
  exit 2
fi
ARCHIVE="$1"
MAX_FILE_BYTES="${2:-41943040}"
case "$MAX_FILE_BYTES" in
  '' | *[!0-9]*) echo "ERROR: max-file-bytes must be a positive integer: '$MAX_FILE_BYTES'" >&2 ; exit 2 ;;
esac
if [ ! -f "$ARCHIVE" ]; then
  echo "ERROR: archive not found: $ARCHIVE" >&2
  exit 2
fi

TMP="$(mktemp -d)"
trap 'rm -rf -- "$TMP"' EXIT

# ---- entry-name safety + listing (BEFORE any extraction) -------------------
case "$ARCHIVE" in
  *.zip)
    python3 - "$ARCHIVE" > "$TMP/entries.txt" <<'PYLIST' || { echo "ERROR: cannot list zip entries: $ARCHIVE" >&2; exit 2; }
import sys, zipfile
with zipfile.ZipFile(sys.argv[1]) as z:
    for name in z.namelist():
        print(name)
PYLIST
    ;;
  *)
    tar -tf "$ARCHIVE" > "$TMP/entries.txt" 2>/dev/null || { echo "ERROR: cannot list tar entries: $ARCHIVE" >&2; exit 2; }
    ;;
esac
if grep -Eq '(^|/)\.\.(/|$)|^/' "$TMP/entries.txt"; then
  echo "ERROR: archive contains unsafe entry names (path traversal / absolute paths):" >&2
  grep -E '(^|/)\.\.(/|$)|^/' "$TMP/entries.txt" | head -5 >&2
  exit 2
fi

# ---- extraction (the walk covers every regular file) -----------------------
mkdir -p "$TMP/extracted"
case "$ARCHIVE" in
  *.zip) python3 -m zipfile -e "$ARCHIVE" "$TMP/extracted" >/dev/null ;;
  *) tar -xf "$ARCHIVE" -C "$TMP/extracted" ;;
esac

: > "$TMP/violations.txt"
violation() { printf 'VIOLATION: %s\n' "$1" >> "$TMP/violations.txt"; }

# ---- walk the extracted tree: size<TAB>relative-path ------------------------
find "$TMP/extracted" -type f -printf '%s\t%P\n' > "$TMP/files.txt"

while IFS=$'\t' read -r SIZE REL; do
  [ -n "${REL:-}" ] || continue
  BASE="${REL##*/}"
  LBASE="${BASE,,}"
  case "$LBASE" in
    ffmpeg | ffmpeg.exe | ffprobe | ffprobe.exe)
      violation "bundled media-tool binary: $REL" ;;
  esac
  case "$LBASE" in
    *.zip | *.7z | *.tar | *.tar.gz | *.tar.xz)
      violation "nested archive inside the package: $REL" ;;
  esac
  case "$LBASE" in
    *.dll)
      case "$REL" in
        tools/* | */tools/*) violation "DLL under tools/: $REL" ;;
      esac
      ;;
  esac
  case "$REL" in
    */sheytan-music-visualizer|*/sheytan-music-visualizer.exe|\
*/app-gogui|*/app-gogui.exe)
      # The application executables are exempt from the media-asset
      # per-file bound (a Go+go-gui+wgpu binary is legitimately
      # ~35-50 MiB; run 45297707372 ships the native Go-Gui shell
      # app-gogui beside the CLI authority); the name match is exact
      # and everything else — including any tool/DLL pack file —
      # stays under the bound.
      ;;
    *)
      if [ "$SIZE" -gt "$MAX_FILE_BYTES" ]; then
        violation "oversized file ($SIZE bytes > $MAX_FILE_BYTES): $REL"
      fi
      ;;
  esac
done < "$TMP/files.txt"

# ---- belt and braces: the same basename rule against raw entry names -------
while IFS= read -r ENTRY; do
  [ -n "${ENTRY:-}" ] || continue
  case "$ENTRY" in */) continue ;; esac
  BASE="${ENTRY##*/}"
  LBASE="${BASE,,}"
  case "$LBASE" in
    ffmpeg | ffmpeg.exe | ffprobe | ffprobe.exe)
      violation "bundled media-tool binary (archive entry): $ENTRY" ;;
  esac
done < "$TMP/entries.txt"

# ---- report ----------------------------------------------------------------
TOTAL_BYTES="$(stat -c%s "$ARCHIVE")"
FILE_COUNT="$(wc -l < "$TMP/files.txt" | tr -d ' ')"
echo "forbid-bundled-media-tools: $ARCHIVE"
echo "archive total size: $TOTAL_BYTES bytes"
echo "packaged files: $FILE_COUNT"
echo "per-file budget: $MAX_FILE_BYTES bytes"
echo "top-10 largest files (bytes / package path):"
sort -rn "$TMP/files.txt" > "$TMP/files.sorted.txt"
N=0
while IFS=$'\t' read -r SIZE REL; do
  N=$((N + 1))
  [ "$N" -le 10 ] || break
  printf '  %12s  %s\n' "$SIZE" "$REL"
done < "$TMP/files.sorted.txt"

VCOUNT="$(grep -c '^VIOLATION: ' "$TMP/violations.txt" || true)"
if [ "$VCOUNT" -gt 0 ]; then
  echo "ERROR: $VCOUNT forbid-bundled-media-tools violation(s):"
  cat "$TMP/violations.txt"
  exit 1
fi
echo "forbid-bundled-media-tools PASSED: no bundled media tools, no nested archives, no DLLs under tools/, no oversized files"
exit 0
