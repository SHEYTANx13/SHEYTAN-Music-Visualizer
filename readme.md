# SHEYTAN Music Visualizer

**SHEYTAN™** · Licensor: Parsaetak · © 2024–2026 Parsaetak. All rights reserved.

SHEYTAN Music Visualizer is a **local-first desktop studio** that analyzes a
song and generates a unique, music-reactive visual identity from its
frequency, rhythm, dynamics and musical structure — and eventually exports
synchronized MP4 music videos up to 3840×2160 (4K). Nothing runs in the
cloud: analysis, rendering and export happen on your machine.

> Phase 1 status: this repository is the **foundation build**. It compiles,
> starts a minimal placeholder window, and proves the build/CI/log-capture
> pipeline. No media, visualizer, GPU or export functionality exists yet —
> see [roadmap.md](roadmap.md) for the staged plan.

---

## Visual identity

The design direction is **black and red — hellish / infernal**, executed in a
premium, cinematic, restrained way: black dominates, red is the accent, and
no skulls, glow storms or saturated surfaces. The identity tokens are defined
once in `internal/brand/brand.go` (obsidian background `#080808`, volcanic
surfaces, dark-blood crimson `#9F0B18`, infernal action red `#D51D2A`, ember
highlights). Phase 1 only records these rules; the visual design itself is a
Phase 2 deliverable.

## Technology

| Aspect | Value |
|---|---|
| Implementation language | Go (1.27 toolchain line, per `go.mod`) |
| Desktop framework | Fyne v2.8.x (`fyne.io/fyne/v2`) |
| Initial target | Windows x64 (Linux x64 is a required CI build target) |
| Product principle | Local-only: no cloud rendering, no accounts, no telemetry |
| Future goal | Real GPU acceleration for rendering, with a tested CPU fallback |

## Architecture (current and planned)

- **Go application core** — owns lifecycle, project model, analysis
  coordination, render scheduling, configuration, diagnostics and (future)
  export orchestration. First-party logic stays Go-only and local.
- **Fyne desktop shell** — owns the native desktop window/UI only; it is not
  the media renderer. *(Only a minimal placeholder window exists today.)*
- **Audio analysis / DSP** — *(planned, Phase 2)* deterministic FFT/STFT,
  band energies, onset and tempo estimation behind narrow Go interfaces.
- **Deterministic visual rendering** — *(planned, Phase 2)* one shared
  render model for UI preview and exported frames; the preview is never the
  source of truth for export.
- **GPU backend** — *(planned, Phase 2)* Direct3D 12 / WGPU research with a
  mandatory proof gate (adapter + workload + readback evidence) and a CPU
  fallback. Phase 1 claims no acceleration.
- **Local video encoding** — *(planned, Phase 2)* MP4/H.264/AAC export up to
  4K with duration sync and cancellation.

## Phase 1 — what actually exists

| Area | Status |
|---|---|
| Minimal Go + Fyne app: window title, placeholder, `--version`, `--self-check` | DELIVERED |
| Central brand identity + build metadata authorities (`internal/brand`, `internal/buildinfo`) | DELIVERED |
| Local structured logging with bounded rotation (`internal/logging`) | DELIVERED |
| CI log capture + consolidated report collector with fixture tests (`internal/cilog`, `cmd/ci-log-collector`) | DELIVERED |
| Build scripts (Windows PowerShell, Linux Bash) with first-byte capture | DELIVERED |
| GitHub Actions: `build-desktop.yml` (Windows x64 UCRT64 + Linux x64) and `codeql.yml` (Go + Actions) | DELIVERED (workflow definitions, locally validated; hosted runs: NOT RUN) |
| Import, playback, FFT, BPM detection, visualizer presets, GPU, MP4 export, studio UI | PLANNED — Phase 2, not implemented |

## Repository map

```text
.github/workflows/    build-desktop.yml, codeql.yml
cmd/
  sheytan-music-visualizer/   desktop app entry point (--version, --self-check)
  ci-log-collector/           consolidated run-log collector (stdlib only)
internal/
  brand/          single source of truth: names, trademark, palette tokens
  buildinfo/      single version/build metadata authority
  logging/        local structured logging (slog, bounded rotation)
  cilog/          consolidated CI report engine + fixture tests
ui/fyne/          minimal Fyne launch surface (Phase 1 placeholder)
scripts/
  build/          build-linux.sh, build-windows.ps1
  ci/             capture.sh (Bash), Capture-Step.ps1 (PowerShell)
evidence/         phase1-build.log (packaged local verification transcript)
license.md        sole licensing authority (Parsaetak Proprietary License v1.1)
roadmap.md        full product plan and phase boundary
agent.md          operating manual for coding agents
ai-notes.md       durable AI engineering record
```

## Build prerequisites

- **Go** at the `go.mod` toolchain (1.27 line). The desktop build uses CGO.
- **Linux**: `gcc`, `pkg-config` and the Fyne native libraries
  (`libgl1-mesa-dev libegl1-mesa-dev libx11-dev libxcursor-dev
  libxrandr-dev libxinerama-dev libxi-dev libxxf86vm-dev libwayland-dev
  libxkbcommon-dev xorg-dev` on Ubuntu 24.04).
- **Windows**: MSYS2 **UCRT64** toolchain (not the deprecated MINGW64):
  `mingw-w64-ucrt-x86_64-gcc` and `mingw-w64-ucrt-x86_64-pkgconf`, with
  `CGO_ENABLED=1 GOOS=windows GOARCH=amd64`.

## Build and verify

Linux (Bash):

```bash
scripts/build/build-linux.sh --mode desktop   # gates + exe + --version + --self-check
scripts/build/build-linux.sh --mode preflight # static gates only
```

Windows (PowerShell 7+, UCRT64 tools on PATH):

```powershell
./scripts/build/build-windows.ps1
```

Every command's stdout/stderr is captured from the first byte into
`ci-logs/phase1-build.log` with structured records and true exit codes; the
script exits non-zero if any required gate fails.

## Run

```bash
go run ./cmd/sheytan-music-visualizer --version      # product + build metadata
go run ./cmd/sheytan-music-visualizer --self-check   # foundation invariants, no window
go run ./cmd/sheytan-music-visualizer                # minimal Phase 1 placeholder window
```

## Logs

- **Application logs** (runtime): written by the app to
  `<user-config-dir>/SHEYTAN Music Visualizer/logs/app.log` (on Windows:
  `%AppData%\SHEYTAN Music Visualizer\logs\app.log`; on Linux:
  `$XDG_CONFIG_HOME` or `~/.config/...`). Rotation keeps one bounded backup;
  `SHEYTAN_LOG_DIR` overrides the directory explicitly.
- **Build/CI logs**: each build script writes `ci-logs/phase1-build.log`;
  CI runs upload per-job log parts and one consolidated report produced by
  `cmd/ci-log-collector` (see `agent.md` for the evidence rules).
- The packaged Phase 1 verification transcript lives at
  `evidence/phase1-build.log`.

## License

Proprietary — see [license.md](license.md) (Parsaetak Proprietary License
v1.1). SHEYTAN™ is a trademark of Parsaetak; third-party dependencies remain
under their own licenses as inventoried there.

No CI badges are shown because no public repository URL exists yet; badges
will be added when the remote is established.
