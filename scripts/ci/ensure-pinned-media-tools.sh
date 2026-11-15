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
#   URL:    https://github.com/BtbN/FFmpeg-Builds/releases/download/autobuild-2026-11-08-12-35/ffmpeg-n9.0.2-48-gd4070a14ce-linux64-gpl-9.0.tar.xz
#   SHA256: bf5e935a51afb65ddd14bf67104361508bcc7c77026a66d7e193a05a56ab8d77
#   inner:  ffmpeg-n9.0.2-48-gd4070a14ce-linux64-gpl-9.0/bin/ (ffmpeg, ffprobe)
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

URL="https://github.com/BtbN/FFmpeg-Builds/releases/download/autobuild-2026-11-08-12-35/ffmpeg-n9.0.2-48-gd4070a14ce-linux64-gpl-9.0.tar.xz"
SHA256="bf5e935a51afb65ddd14bf67104361508bcc7c77026a66d7e193a05a56ab8d77"
INNER_BIN="ffmpeg-n9.0.2-48-gd4070a14ce-linux64-gpl-9.0/bin"

if [ -n "${SHEYTAN_MEDIA_TOOLS_CACHE_DIR:-}" ]; then
  BASE="$SHEYTAN_MEDIA_TOOLS_CACHE_DIR"
elif [ -n "${RUNNER_TEMP:-}" ] && [ -d "${RUNNER_TEMP}" ]; then
  BASE="$RUNNER_TEMP/media-tools"
else
  BASE="${TMPDIR:-/tmp}/sheytan-media-tools"
fi
mkdir -p "$BASE"

ARCHIVE="$BASE/ffmpeg-n9.0.2-48-gd4070a14ce-linux64-gpl-9.0.tar.xz"

if [ -s "$ARCHIVE" ] && echo "$SHA256  $ARCHIVE" | sha256sum -c - >/dev/null 2>&1; then
  echo "pinned FFmpeg archive already cached and SHA-256-verified: $ARCHIVE" >&2
else
  rm -f "$ARCHIVE"
  echo "downloading pinned FFmpeg archive: $URL" >&2
  curl -fsSL --retry 2 -o "$ARCHIVE" "$URL" || { echo "ERROR: pinned FFmpeg download failed: $URL" >&2; exit 1; }
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
