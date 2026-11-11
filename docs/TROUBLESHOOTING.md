# Troubleshooting

Status: current as of run 42939743766 repair (2026-11-11).

## GPU / renderer

**The app reports "software" or "noop" renderer.** The GPU path needs a
real adapter on a supported backend (Vulkan / DX12 / Metal / GL). On
headless machines and VMs the app falls back honestly to the CPU renderer
and records the reason. This is a capability statement, not an error.

**The status says DETECTED but not VERIFIED.** The state machine is
DETECTED → AVAILABLE → SELECTED → EXECUTED → VERIFIED. A state short of
VERIFIED means the GPU path did not complete a verified frame this session;
the exact reason is in the log (see "Logs" below). The app keeps working on
the CPU path.

**Palettes or colors look wrong.** The palette→shader contract is pinned by
`TestPaletteReachesGPUShader` and the layout tests listed in
[GPU-RENDERING.md](GPU-RENDERING.md). Run
`go test -tags goffi_static -v -run '^TestPaletteReachesGPUShader$' ./internal/render/`
on your machine; if it fails there, include the printed diagnostics
(adapter, backend, device class, staged uniform lanes) in your report.

## Command window / child processes

**A console window appears when launching the desktop app (Windows).** The
shipped EXE is linked `-H=windowsgui` (PE subsystem 2). If you built it
yourself, you dropped the linker flag — see `docs/BUILDING.md`. FFmpeg and
ffprobe child processes are always started hidden (`internal/procattr`);
if you ever see a console flash from them, report it with the log.

## FFmpeg / export

**Export says FFmpeg is missing.** MP4 export requires a working
`ffmpeg` AND `ffprobe` pair. Discovery order (documented, deterministic):
configured path (`SHEYTAN_FFMPEG`), app-local `tools/ffmpeg/bin`, then
`PATH`. A partial or broken pair fails with a clear message. Without
FFmpeg, import/playback of the built-in formats (WAV/MP3/FLAC/Ogg) works;
AAC/M4A/MP4 import and MP4 export stay explicitly unavailable — the app
never advertises them falsely.

**Export fails with encoder errors.** The app validates the encoder with a
real ffprobe pass; check your FFmpeg build includes libx264 and the AAC
encoder. Windows hosted-CI runners carry no FFmpeg, so Windows export
problems must be reproduced locally.

## Portable vs installed data locations

- **Portable mode** (a `portable.mode` marker beside the executable):
  data lives in `data/` beside the executable — used only when that
  location is writable. If it is not writable (e.g. `C:\Program Files`),
  the app reports the fallback and uses the per-user platform location
  instead; it never writes into protected install folders.
- **Installed mode**: per-user data root — Windows `%LOCALAPPDATA%`,
  Linux XDG (`~/.local/share` etc.). Config, logs, cache, projects,
  exports, temp and crash/support folders are laid out as documented in
  the package's `data/DATA-FOLDERS.txt`.

## Logs and support bundles

**Where are the logs?** The Help / Diagnostics dialog shows the resolved
data mode and log path and can open the log folder directly. Logs are UTC
text with rotation; startup phases, version/commit/OS, renderer selection
and fallback, audio/decoder events, playback, export, fullscreen, recoverable
and fatal errors and graceful shutdown are recorded. Logs never contain
secrets or personal media paths beyond the necessary diagnostic context.

**Reporting a problem:** include (1) the app `--version` line, (2) the log
file, (3) your OS and GPU/audio hardware description, and (4) whether the
issue reproduces with a fresh portable data folder. Do not send media files.

## CI failures

See [CI-DIAGNOSTICS.md](CI-DIAGNOSTICS.md) for the ordered red-run
checklist, gate-status semantics (PASS / FAIL / BLOCKED / SKIPPED) and the
root-cause grouping. A `TEST-ONLY` package label means the package itself
passed all package-level checks while another gate failed.
