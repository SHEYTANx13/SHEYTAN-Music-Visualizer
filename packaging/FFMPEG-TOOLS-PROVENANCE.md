# FFmpeg tools provenance and redistribution record (run 43612608237)

The portable test packages carry their OWN FFmpeg/FFprobe pair under
`tools/ffmpeg/bin/`. This file records exactly what is bundled, where it
came from, and the redistribution obligations that ship with it. The
packaging scripts verify every fact below at staging time and refuse a
`VERIFIED` package status when any check fails.

## Pinned build (both platforms)

| Field | Value |
| --- | --- |
| Upstream version | FFmpeg n9.0.2 (9.0.2 release branch; latest stable of the 9.0 line at execution time) |
| Build source | BtbN/FFmpeg-Builds release tag `autobuild-2026-11-08-12-35`, git source snapshot `n9.0.2-48-gd4070a14ce` |
| Linux x64 asset | `ffmpeg-n9.0.2-48-gd4070a14ce-linux64-gpl-9.0.tar.xz` |
| Linux SHA-256 | `bf5e935a51afb65ddd14bf67104361508bcc7c77026a66d7e193a05a56ab8d77` |
| Windows x64 asset | `ffmpeg-n9.0.2-48-gd4070a14ce-win64-gpl-9.0.zip` |
| Windows SHA-256 | `40608cfdd40b0e7929704059daee4af369f7a9984ea7aa8ee416d78cce2e8aae` |
| Observed binary version string | `ffmpeg version n9.0.2-48-gd4070a14ce-20261108` (verified locally for the Linux build; the Windows zip is verified by the Windows packaging gate from the extracted binaries) |
| Build configuration (observed, Linux) | `--enable-gpl --enable-version3`; **no `--enable-nonfree`**; `libx264` and `libopenh264` H.264 encoders present |

## Why this build

* Official FFmpeg upstream publishes source only; the official download
  page lists the BtbN/FFmpeg-Builds project as a well-known
  redistributable build provider. The exact release tag and asset names
  above are pinned (never "latest"), the archives are hash-verified
  before staging, and the staged binaries are re-verified functionally
  (`-version`, `-buildconf`, `-encoders`) before a package can be
  labelled VERIFIED.
* The GPL configuration (not LGPL) is required because the LGPL build
  cannot carry `libx264`, and MP4 H.264 software export depends on it.
  `libopenh264` (Apache-2.0 licensed codec implementation) is also
  present as an alternative software H.264 encoder.
* The build carries NO `--enable-nonfree` component (verified from
  `-buildconf`); nothing in the bundled toolchain requires a separate
  commercial license.

## Redistribution obligations shipped with the package

FFmpeg under `--enable-gpl --enable-version3` is licensed as
GPL-3.0-or-later (with the GPL-2.0-or-later option the project also
offers). The packages ship:

* `tools/ffmpeg/COPYING.GPLv2` and `tools/ffmpeg/COPYING.GPLv3`
  (verbatim from the FFmpeg n9.0.2 source tree, also committed under
  `packaging/ffmpeg-licenses/` in this repository).
* `tools/ffmpeg/FFMPEG-TOOLS-PROVENANCE.txt` — the machine-generated
  record of the asset URL, SHA-256, observed `-version`/`-buildconf`
  summary and encoder checks of the exact staged binaries.
* The corresponding-source offer: the FFmpeg source for git commit
  `d4070a14ce` (tag `n9.0.2` plus 48 commits) is available from
  `https://ffmpeg.org/download.html` and
  `https://github.com/FFmpeg/FFmpeg`; the build scripts that produced
  these binaries are published in
  `https://github.com/BtbN/FFmpeg-Builds`. Written offer per GPL §6 is
  satisfied by these public, no-charge sources for the exact snapshot
  named above.

The application communicates with FFmpeg through a separate process
(exec with explicit argv, no in-process linking), so the application's
own proprietary license is unaffected; the bundled tools remain
independently licensed exactly as recorded here.

## Verification gate contract (both platforms)

A package may only report `package_status = VERIFIED` when ALL of the
following have been proven from the EXTRACTED package:

1. `tools/ffmpeg/bin/ffmpeg` and `tools/ffmpeg/bin/ffprobe` exist, are
   executable, and report the pinned version string.
2. `-buildconf` shows no `--enable-nonfree`; the GPL flag is present.
3. `-encoders` lists `libx264` (the default software H.264 export
   encoder).
4. The extracted application, run with a PATH that does NOT contain any
   system FFmpeg, resolves the app-local pair and completes a real
   headless export (`--export-cli`) whose output is validated by the
   packaged ffprobe.
5. Package integrity (commit identity, license byte-identity, archive
   layout) passes as before.

When tools cannot be staged (offline environment, hash mismatch,
buildconf violation), the staging records the exact reason, the package
status is `TEST_ONLY` at best, and the workflow upload labels it
TEST-ONLY. An empty `tools/ffmpeg/bin/` can never be `VERIFIED`.
