# FFmpeg in `tools/ffmpeg/bin` — technical and legal contract

SHEYTAN Music Visualizer uses FFmpeg/ffprobe as an **optional, local
encoder/decoder adapter** for MP4 export and the AAC/M4A/ALAC input
family. This package ships **no FFmpeg binary** (run 43712071111
compact-package contract: a hard negative gate fails the package when
any ffmpeg/ffprobe binary, tool DLL pack or nested archive appears in
the application archive). The optional tools are provided by the
user, through the in-app **Media Tools & Codecs** manager, which
downloads a pinned, digest-verified pack on explicit request into the
user data directory (never into the application package).

## Why no binary ships in this package

The project's own `license.md` is a proprietary license. FFmpeg builds
carry their own license obligations that depend on the exact build
configuration (LGPL-only vs GPL components vs nonfree `--enable`
flags), including notice files and, for LGPL builds, corresponding
source availability. Whether a given third-party build may be
redistributed inside this proprietary package is a legal decision that
must be verified against the exact upstream build, version and
configuration — it is not a build-script decision. Until that
verification is performed and recorded, no binary is bundled and no
claim of redistribution rights is made.

## Where the application looks for FFmpeg (resolution order)

1. Explicit user configuration (`SHEYTAN_FFMPEG` environment variable
   naming a directory that contains `ffmpeg` and `ffprobe`).
2. **App-managed tools**: `<user data root>/media-tools/current/bin`
   — installed by the in-app Media Tools & Codecs manager (pinned
   manifest, HTTPS-only, SHA-256 verified before extraction, bounded
   traversal-safe extraction, binary verification incl. a real encode
   + probe smoke test, atomic swap with rollback). Licenses and the
   provenance record are copied beside the pair under
   `media-tools/current/licenses/`.
3. **This directory**: `<application folder>/tools/ffmpeg/bin` —
   the app-local portable location (shipped EMPTY). Place `ffmpeg` +
   `ffprobe` here (`ffmpeg.exe`/`ffprobe.exe` on Windows) to make the
   package self-contained without touching PATH.
4. The system `PATH` (external fallback).

The resolved source (override / app-managed / app-local / path), the
FFmpeg version
and the actually available encoders are recorded in the runtime log
and shown in Help / Diagnostics and the export dialog. MP4 export is
proven only by a real, ffprobe-validated export — never by
`ffmpeg -version` alone. Without FFmpeg, export reports
`NOT AVAILABLE` honestly and everything else keeps working.

## If you place an FFmpeg build here yourself

You accept responsibility for complying with that build's license:
keep the complete notice/license files that accompany the binary next
to it (this directory is the right place), respect any
corresponding-source obligations of the exact build you chose, and do
not select a nonfree configuration build. Verify the binaries come
from a trusted source (for example the official ffmpeg.org release
pages) and record the provider, version and license you used.

This file is technical documentation; it is not legal advice.
