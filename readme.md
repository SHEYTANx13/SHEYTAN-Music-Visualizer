# SHEYTAN Music Visualizer

**SHEYTAN™** — a local-first desktop studio for music-reactive visuals.

SHEYTAN Music Visualizer will analyze each song and generate a unique visual
identity from its frequency, rhythm, dynamics and musical structure. You
customize the scene, and the studio exports a synchronized MP4 music video —
up to 3840×2160 (4K). Everything runs locally: no cloud rendering, no
accounts, no telemetry.

> Current state: **Phase 1 — Foundation.** This repository contains the
> buildable Go/Fyne application skeleton, documentation, CI pipelines and the
> log-capture subsystem. It does **not** yet import audio, play music,
> visualize anything, or export video. See [Status](#status) and
> [roadmap.md](roadmap.md).

---

## Visual identity

Black and red — hellish, infernal, **restrained**. Obsidian backgrounds
(`#080808`), deep charcoal surfaces, volcanic dark-red accents
(`#9F0B18`, `#D51D2A`), ember highlights used sparingly (`#FF453A`). Premium,
cinematic, precise. Black dominates; red is the accent — no flames, no
skulls, no glow saturation. The full token set lives in
`internal/brand/brand.go`; the Phase 1 UI deliberately does not implement the
theme yet.

## Technology

- **Go** — all first-party application logic, analysis orchestration, render
  scheduling, project state, diagnostics and export coordination (planned for
  Phase 2; Phase 1 ships the foundation packages).
- **Fyne** — the native desktop shell. Fyne owns windows and UI only; it is
  never the media renderer.
- **Local-only** — audio analysis, rendering and export run on your machine.
  No telemetry, no outbound runtime network activity.
- **Windows x64 first**, with Linux x64 as a required CI build target.
- **GPU acceleration** is a Phase 2 goal with a proof gate and CPU fallback —
  never assumed, never faked (see [roadmap.md](roadmap.md)).

## Architecture (overview)

```
┌────────────────────────────────────────────────────────────┐
│ Fyne desktop shell (window, panels, transport, inspector)  │   planned
├────────────────────────────────────────────────────────────┤
│ Application core: project model, analysis coordination,    │
│ deterministic frame timeline, render scheduling, config,   │   planned
│ diagnostics, export orchestration                          │
├──────────────────┬───────────────────┬─────────────────────┤
│ Audio analysis   │ Visual render     │ Export pipeline     │
│ FFT/STFT, bands, │ engines, GPU      │ deterministic       │   planned
│ onsets, tempo    │ backend + CPU     │ offline frames →    │
│ (deterministic)  │ fallback          │ H.264/AAC MP4       │
└──────────────────┴───────────────────┴─────────────────────┘
        Phase 1 delivers only: identity constants, build info,
        structured local logging, minimal window, CI + log capture.
```

Planned areas are explicitly marked. The full staged plan with acceptance
gates is [roadmap.md](roadmap.md).

## Status — Phase 1 (delivered foundation)

| Area | State |
|---|---|
| Five root documents (`license.md`, `readme.md`, `agent.md`, `roadmap.md`, `ai-notes.md`) | DELIVERED |
| Go module, brand identity authority, build metadata authority | DELIVERED |
| Structured local logging (`log/slog`, bounded rotation, app-owned dir) | DELIVERED |
| Minimal Fyne window with exact product title and Phase 1 placeholder | DELIVERED |
| `--version` and `--self-check` CLI | DELIVERED |
| `cmd/ci-log-collector` consolidated run-log builder + fixture tests | DELIVERED |
| Windows/Linux build scripts with first-byte log capture | DELIVERED |
| `.github/workflows/build-desktop.yml` (preflight, Windows UCRT64, Linux, log consolidation) | DELIVERED (definition; hosted runs: NOT RUN) |
| `.github/workflows/codeql.yml` (Go + Actions, manual Go build mode) | DELIVERED (definition; hosted runs: NOT RUN) |
| Audio import/playback, DSP, visualizers, GPU, MP4 export, studio UI | PLANNED — Phase 2, not started |

## Repository map

```
.github/workflows/     build-desktop.yml, codeql.yml
cmd/sheytan-music-visualizer/   desktop app entry point
cmd/ci-log-collector/           consolidated CI log builder (stdlib-only)
internal/brand/        single source of product identity + palette
internal/buildinfo/    single version/build metadata authority
internal/logging/      structured local file logging (slog)
internal/cilogreport/  consolidated CI report engine + tests
ui/fyne/               minimal Phase 1 window
scripts/build/         build-windows.ps1, build-linux.sh
scripts/ci/            capture.sh, Capture-Step.ps1, collector E2E, contract test
evidence/              phase1-build.log (captured local verification transcript)
license.md  readme.md  agent.md  roadmap.md  ai-notes.md
```

## Building

Prerequisites:

- **Go 1.27** (module pins `toolchain go1.27.3` — the single version
  authority).
- **Windows:** MSYS2 **UCRT64** toolchain (`mingw-w64-ucrt-x86_64-gcc`) on
  PATH. Deprecated MINGW64 is not supported.
- **Linux:** CGO prerequisites for Fyne — `gcc`, `pkg-config`, and the
  X11/OpenGL/Wayland development libraries listed in
  `.github/workflows/build-desktop.yml`.

### Windows (PowerShell)

```powershell
git clone <repository-url>   # remote to be provisioned; see ai-notes.md
cd SHEYTAN-Music-Visualizer
./scripts/build/build-windows.ps1 -Msys2Bin "<msys2-root>\ucrt64\bin"
```

The script runs gofmt verification, `go mod tidy`, `go test ./...`,
`go vet ./...`, `go build ./...`, builds
`dist\sheytan-music-visualizer-windows-x64.exe`, runs `--version` and
`--self-check`, and writes full first-byte logs to `ci-logs\windows\`.

### Linux (Bash)

```bash
./scripts/build/build-linux.sh
```

Same gate ladder on Linux, logs in `ci-logs/linux/`, binary at
`dist/sheytan-music-visualizer-linux-x64`.

### Quick runtime checks

```bash
./dist/sheytan-music-visualizer-linux-x64 --version      # identity banner
./dist/sheytan-music-visualizer-linux-x64 --self-check   # foundation invariants, no window
./dist/sheytan-music-visualizer-linux-x64                # minimal Phase 1 window
```

## Logs

- **Application logs:** `<UserConfigDir>/SHEYTAN-Music-Visualizer/logs/`
  (Windows: `%AppData%\SHEYTAN-Music-Visualizer\logs\`; Linux:
  `~/.config/SHEYTAN-Music-Visualizer/logs/`). Structured JSON via `log/slog`,
  size-bounded with one rotation generation. Logs never contain audio
  contents, media bytes or credentials.
- **Build/CI logs:** per-step logs under `ci-logs/`; hosted CI runs publish
  one consolidated plain-text report (the `consolidated-run-log` artifact)
  built by `cmd/ci-log-collector` — including on failed runs, with
  `MISSING LOG SOURCE` and `NEVER_STARTED` states distinguished.

## CI

Workflow definitions live in `.github/workflows/`: `build-desktop.yml`
(Go/Fyne builds on Windows x64 UCRT64 + Linux x64, with consolidated log
collection) and `codeql.yml` (Go + Actions analysis, manual Go build mode).
All actions are pinned to full commit SHAs. No hosted run has been triggered
from this repository yet; status is reported honestly as NOT RUN in
`ai-notes.md` — workflow files are not CI evidence.

## License

Proprietary — Parsaetak Proprietary License v1.1. See [license.md](license.md)
(the sole licensing authority, including the third-party attribution
inventory). SHEYTAN™ and the SHEYTAN logo are trademarks of Parsaetak.
