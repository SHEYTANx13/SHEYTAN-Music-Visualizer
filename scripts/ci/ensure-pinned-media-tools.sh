#!/usr/bin/env bash
# ensure-pinned-media-tools.sh — ensure the PINNED FFmpeg asset for the
# Linux package proofs is downloaded, SHA-256-verified and extracted
# under the runner temp directory (always OUTSIDE the repository, the
# dist/ tree and the package stage tree; never bundled into any
# artifact — the package ships no media-tool binaries).
#
# Pinned asset (packaging/FFMPEG-TOOLS-PROVENANCE.md is the documented
# record; internal/mediatools/manifest.json is the machine-readable
# source of truth consumed by the in-app Media Tools & Codecs manager):
#   URL:    https://github.com/BtbN/FFmpeg-Builds/releases/download/autobuild-2026-11-22-12-36/ffmpeg-n9.0.2-61-gd59078794d-linux64-gpl-9.0.tar.xz
#   SHA256: 12b0097290a6289794d34c3b65e23962afc55c4bf03efca273bd6f052ff46811
#   inner:  ffmpeg-n9.0.2-61-gd59078794d-linux64-gpl-9.0/bin/ (ffmpeg, ffprobe)
#
# Run-45042834042 repair: the previously pinned autobuild-2026-11-08-12-35
# release was ROTATED AWAY upstream (BtbN keeps a rolling window of dated
# autobuild releases), so every download 404'd and both desktop lanes
# failed the media-tools-pinned-install and verify-extracted-path-with-
# spaces gates. The pin was rotated to the newest existing autobuild and
# the 404 diagnostic below now names the rotation cause and the exact
# re-pin procedure so the next rotation is a two-line fix, not a mystery.
#
# The download is CACHED: a second call (the packaged-export proof gate)
# reuses the verified archive and extraction instead of downloading
# twice. The caller owns cleanup — build-linux.sh removes the cache
# directory after the last consumer gate has finished.
#
# Cache location: $SHEYTAN_MEDIA_TOOLS_CACHE_DIR when set by the caller,
# else $RUNNER_TEMP/media-tools, else ${TMPDIR:-/tmp}/sheytan-media-tools.
#
# Usage: scripts/ci/ensure-pinned-media-tools.sh
# Output (stdout, shell-quoted key=value lines; consume via eval):
#   SHEYTAN_MEDIA_ARCHIVE=<path to the SHA-256-verified archive>
#   SHEYTAN_MEDIA_BIN=<path to the extracted inner bin dir (ffmpeg, ffprobe)>
#   SHEYTAN_MEDIA_SHA256=<pinned sha256>
# Exit: 0 on success; nonzero on download/hash/layout failure (never a
# partial "trust me" result).
set -Eeuo pipefail

URL="https://github.com/BtbN/FFmpeg-Builds/releases/download/autobuild-2026-11-22-12-36/ffmpeg-n9.0.2-61-gd59078794d-linux64-gpl-9.0.tar.xz"
SHA256="12b0097290a6289794d34c3b65e23962afc55c4bf03efca273bd6f052ff46811"
INNER_BIN="ffmpeg-n9.0.2-61-gd59078794d-linux64-gpl-9.0/bin"

if [ -n "${SHEYTAN_MEDIA_TOOLS_CACHE_DIR:-}" ]; then
  BASE="$SHEYTAN_MEDIA_TOOLS_CACHE_DIR"
elif [ -n "${RUNNER_TEMP:-}" ] && [ -d "${RUNNER_TEMP}" ]; then
  BASE="$RUNNER_TEMP/media-tools"
else
  BASE="${TMPDIR:-/tmp}/sheytan-media-tools"
fi
mkdir -p "$BASE"

ARCHIVE="$BASE/ffmpeg-n9.0.2-61-gd59078794d-linux64-gpl-9.0.tar.xz"

if [ -s "$ARCHIVE" ] && echo "$SHA256  $ARCHIVE" | sha256sum -c - >/dev/null 2>&1; then
  echo "pinned FFmpeg archive already cached and SHA-256-verified: $ARCHIVE" >&2
else
  rm -f "$ARCHIVE"
  echo "downloading pinned FFmpeg archive: $URL" >&2
  if ! curl -fsSL --retry 2 -o "$ARCHIVE" "$URL"; then
    echo "ERROR: pinned FFmpeg download failed (HTTP error, most likely 404): $URL" >&2
    echo "ERROR: cause: the pinned BtbN autobuild release was rotated away upstream (dated autobuild releases are removed after a rolling window)." >&2
    echo "ERROR: fix: pick the newest existing tag from https://github.com/BtbN/FFmpeg-Builds/releases, then update URL/SHA256/INNER_BIN here, in scripts/ci/MediaTools-Pinned.ps1, internal/mediatools/manifest.json and packaging/FFMPEG-TOOLS-PROVENANCE.md (they must stay identical)." >&2
    exit 1
  fi
  echo "$SHA256  $ARCHIVE" | sha256sum -c - >&2 || { echo "ERROR: pinned FFmpeg download SHA-256 mismatch: $ARCHIVE" >&2; rm -f "$ARCHIVE"; exit 1; }
fi

EXTRACT_DIR="$BASE/extracted-linux"
BIN_DIR="$EXTRACT_DIR/$INNER_BIN"
if [ ! -x "$BIN_DIR/ffmpeg" ] || [ ! -x "$BIN_DIR/ffprobe" ]; then
  rm -rf "$EXTRACT_DIR"
  mkdir -p "$EXTRACT_DIR"
  tar -xJf "$ARCHIVE" -C "$EXTRACT_DIR" || { echo "ERROR: pinned FFmpeg archive extraction failed" >&2; exit 1; }
fi
if [ ! -x "$BIN_DIR/ffmpeg" ] || [ ! -x "$BIN_DIR/ffprobe" ]; then
  echo "ERROR: pinned archive layout unexpected — executable pair missing under $BIN_DIR" >&2
  exit 1
fi

# Shell-quoted key=value output: callers consume with
#   eval "$(scripts/ci/ensure-pinned-media-tools.sh)"
printf 'SHEYTAN_MEDIA_ARCHIVE=%q\n' "$ARCHIVE"
printf 'SHEYTAN_MEDIA_BIN=%q\n' "$BIN_DIR"
printf 'SHEYTAN_MEDIA_SHA256=%q\n' "$SHA256"
exit 0
