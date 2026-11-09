# FFmpeg in `tools/ffmpeg/bin` — technical and legal contract

SHEYTAN Music Visualizer uses FFmpeg/ffprobe as an **optional, local
encoder/decoder adapter** for MP4 export and the AAC/M4A/ALAC input
family. The application never downloads binaries at runtime and this
package ships **no FFmpeg binary**.

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
2. **This directory**: `<application folder>/tools/ffmpeg/bin` —
   the app-local location. Place `ffmpeg` + `ffprobe` here
   (`ffmpeg.exe`/`ffprobe.exe` on Windows) to make the package
   self-contained without touching PATH.
3. The system `PATH` (external fallback).

The resolved source (override / app-local / path), the FFmpeg version
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
