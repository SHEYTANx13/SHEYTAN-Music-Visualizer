# Troubleshooting

Status: current as of run 43541087097 (2026-11-14). This is the canonical
user-facing diagnostics + troubleshooting document (the former separate
`docs/diagnostics.md` was merged here — one subject, one document).

## Runtime log

- **Location**: resolved by `internal/apppaths` at startup —
  `<data-root>/logs/app.log` where the data root is the portable
  `data/` directory (when `portable.mode` sits beside the executable) or
  the per-user app-data tree (Windows `%LOCALAPPDATA%\SHEYTAN Music
  Visualizer`, Linux `$XDG_DATA_HOME/SHEYTAN Music Visualizer`).
- **Rotation**: `app.log` rotates at 5 MiB with one retained generation
  (`app.log.1`). The active failure log is never truncated at startup;
  the startup record is appended.
- **Fallback**: an unwritable log file never aborts the launch — a
  stderr-backed logger takes over and the exact error is surfaced in the
  Help / Diagnostics dialog.
- **Content**: structured one-line records (UTC timestamp, severity,
  component, event, safe contextual values). The startup path emits, in
  order: `application starting` (version, commit, builder, build date,
  data mode, data root, log dir), `runtime log opened`, resource-policy
  resolution, renderer preference application, `window shown` (renderer
  summary + fullscreen intent), `startup stages complete` (measured
  stage timings anchored at true process start) and the one-per-launch
  `runtime snapshot` (GOMAXPROCS, NumCPU, heap, GC cycles). Shutdown
  logs `shutdown` with the observed fullscreen state. Subsystem
  failures log full error chains. Repetitive states are bounded/rate-
  limited; there is exactly one record per startup — never per frame,
  never per audio sample.
- **Privacy**: no audio content, no credentials, no full sensitive paths,
  no environment dumps.

## In-app diagnostics

Help / Diagnostics (inside the single main window) shows the resolved
data mode, data root and exact log path, with **Open Logs Folder**,
**Copy Log Path** and a copy-diagnostics action that never copies secrets
or personal media metadata. The System Integration dialog reports the
machine profile with provenance states only — fabricated real-time
utilization percentages are never shown ("not reported" is the honest
value when no stable source exists).

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
yourself, you dropped the linker flag — see [ci.md](ci.md). FFmpeg and
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
passed all package-level checks while another gate failed. Since run
43541087097 the overall verdict is the dedicated `final-verdict` job:
`collect-logs` green means the consolidated report WAS collected;
`final-verdict` red means a required gate failed.

## Startup metrics and benchmarks

`internal/perf` anchors a recorder at true OS process start (procfs on
Linux, `GetProcessTimes` on Windows; the Linux anchor carries a documented
up-to-±1 s bound from second-granularity `btime`) and marks:
`runtime-entry`, `logger-and-data-paths`, `app-shell-created`,
`studio-service-and-audio-init`, `ui-shell-built`, `first-window-usable`
(first event-loop iteration — the honest "window visible/usable" stage,
distinct from "all optional checks done") and `startup-recorded`. The
complete stage list is one structured record (`startup stages complete`,
field `first-window-usable=<N>ms`). Reproduce on a display-capable host
with:

    scripts/bench/startup-bench.sh <samples> [bin path]

Cold runs use a fresh data root; warm runs reuse one. Numbers are
host-specific samples — compare only same-host same-commit runs.

Benchmarks and profiling:

    go test -tags goffi_static -bench . -benchmem ./internal/dsp/ ./internal/render/ ./internal/audio/
    go test -tags goffi_static -bench BenchmarkCPURender1080pBars -cpuprofile cpu.prof ./internal/render/
    go tool pprof -top cpu.prof

Measured baselines and the before/after records of every accepted
optimization live in [performance-baseline.md](performance-baseline.md).
Rules of the road: baseline first, profile second, optimize real hotspots
third; never speculative rewrites; outputs must stay bit-identical unless
the change is explicitly reviewed as a visual change.

## Reading capability states

Each acceleration path (GPU render, audio device, FFmpeg export) moves
through separate, honest states — a device being listed by the OS is
only DETECTED, never proof of acceleration:

| State | Meaning | Evidence required |
| --- | --- | --- |
| DETECTED | The OS/inventory lists it | Enumeration only |
| SUPPORTED | The API/backend class is usable by this build | Backend probe without claiming execution |
| SELECTED | The app chose it for the pipeline | Renderer/adapter decision record |
| EXECUTING | The real pipeline is running on it | Live render/export path usage |
| VERIFIED | Output validated (readback/ffprobe) | Measured artifact check |

Interpretation rules baked into the UI and the logs:

- `Microsoft Basic Render Driver`, software rasterizers and virtual
  adapters are always reported as software/virtual — never as physical
  GPU acceleration.
- An ALSA null sink (used by headless CI) proves no audio playback.
- `FFmpeg absent` is reported explicitly as NOT AVAILABLE; MP4 export
  stays NOT PROVEN until a real export has run and ffprobe-validated on
  that platform. A Linux runner proof never proves Windows export.
- Self-check renderer evidence distinguishes the selected backend, the
  device class and the exact fallback reason on one line
  (`backend=… gpu=… api=… adapter=… devtype=… class=…`).
