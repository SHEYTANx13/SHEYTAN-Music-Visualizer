# SHEYTAN Music Visualizer

**SHEYTAN™** · Licensor / rights holder: [Parsaetak](https://linktr.ee/Parsaetak) ·
Product/brand/project home: [SHEYTAN](https://github.com/SHEYTANx13) ·
© 2024–2026 Parsaetak. All rights reserved.

SHEYTAN Music Visualizer is a **local-first desktop studio** that imports a
song, analyzes it locally, derives a deterministic per-track visual identity
("Song DNA") and renders music-reactive visuals in a native Fyne studio —
then exports synchronized H.264/AAC MP4 videos up to 3840×2160 (4K). Nothing
runs in the cloud: decoding, analysis, rendering and encoding happen on your
machine. No account, no telemetry, no bundled codec binaries.

> Phase 2 status: the studio slice is implemented and locally verified on
> Linux x64 (build, tests, race tests, real MP4 export proofs at 1080p and
> 4K, wgpu render/readback proof, GUI smoke on a virtual display). The
> 2026-11-05 repair session additionally delivered the optional AAC/M4A
> input adapter, layered compositions, manual tempo/offset correction,
> spectral rolloff, keyboard transport shortcuts, benchmarks and the
> consolidated-diagnostics gate repair. The 2026-11-11 sessions delivered
> the run-42921967187 packaging-gate repair with machine-readable gate
> evidence, and the run-42939743766 repair: the GPU palette-to-shader
> defect was fixed at its source (fragment write mask + formalized
> Go↔WGSL uniform contract, [docs/GPU-RENDERING.md](docs/GPU-RENDERING.md)),
> the workflow-security scanner now runs pinned zizmor 1.32.0 in one
> isolated uvx environment, CI diagnostics gained per-gate JSON records
> and root-cause grouping ([docs/CI-DIAGNOSTICS.md](docs/CI-DIAGNOSTICS.md)),
> and GitHub Actions now produces EXACTLY ONE Windows and ONE Linux
> application package plus the plain-text diagnostics report — no
> recovery/duplicate builds. See [roadmap.md](roadmap.md) for the honest
> status of every capability and [ai-notes.md](ai-notes.md) for the
> evidence trail. The 2026-11-13 session (run 43090342995) repaired the playback clock contract, delivered one authoritative shutdown path with a close confirmation, a responsive breakpoint shell (320×568 phone → 3840×2160 ultrawide), first-frame deferral of the renderer probe and audio-device open, and machine-readable build identity (`--build-info-json`) as the package commit authority.

---

## Visual identity

The design direction is **black and red — hellish / infernal**, executed in a
premium, cinematic, restrained way: black dominates, red is the accent, warm
white is reserved for text and peaks. The identity tokens are defined once in
`internal/brand/brand.go` (obsidian background `#080808`, volcanic surfaces,
dark-blood crimson `#9F0B18`, infernal action red `#D51D2A`, ember
highlights) and drive the real Fyne theme (`ui/fyne/theme.go`) plus the four
brand palettes used by the visualizer (`internal/visualizer`).

## What works today (Phase 2 slice)

| Capability | Status |
|---|---|
| Studio shell: top bar, library/presets rail, live preview, inspector, transport, export dialog | DELIVERED (Fyne v2.8.2, hellish theme) |
| Import WAV (int16/int24/int32 + IEEE float32), MP3, FLAC, Ogg/Vorbis via first-party Go decoders | DELIVERED (`internal/audio`) |
| Import AAC/M4A/ALAC via the user's local FFmpeg (optional adapter, never bundled) | DELIVERED as an optional adapter (`internal/audio/decoders_ffmpeg.go`; registers only when FFmpeg is present) |
| Layered compositions: a dimmed background mode behind the primary mode | DELIVERED (CPU renderer, deterministic, bounded intensity; `internal/render`) |
| Manual tempo override and audio/visual sync offset, persisted per project | DELIVERED (`internal/studio`, `internal/project`) |
| Canonical PCM pipeline: 48 kHz stereo float32, disk-backed cache, Kaiser-sinc resampling, atomic writes | DELIVERED |
| Playback: Oto v3 single context/player, play/pause/stop/seek/volume, buffering-aware playhead | DELIVERED (device-less hosts fall back to an honest silent mode) |
| DSP: Hann FFT-2048/hop-512, bass/mid/treble bands, RMS, spectral centroid, flux/onsets, BPM with confidence | DELIVERED (`internal/dsp`) |
| Song DNA: deterministic per-track identity from measured features + content fingerprint + user seed | DELIVERED (`internal/songdna`) |
| Visualizer modes: Spectrum Bars, Radial Spectrum, Waveform, Particle Field (bounded, seeded particles) | DELIVERED (`internal/visualizer`, `internal/render`) |
| CPU renderer: deterministic software rendering used by preview and export | DELIVERED |
| GPU renderer: wgpu (Go-controlled WebGPU) radial-mode shader with verified readback; hardware adapters on Vulkan/DX12/Metal hosts | DELIVERED as code + software-adapter proof; **hardware-GPU execution: NOT AVAILABLE on the verification host** |
| MP4 export: 1280×720 → 3840×2160 at 24/30/60 fps, H.264 + AAC via local FFmpeg, streamed frames, real progress, cancellation, ffprobe validation | DELIVERED (`internal/export`; verified 1080p30 and 4K24 proofs) |
| Projects: versioned JSON, atomic save, validation, relink, presets (Hellfire Pulse, Crimson Orbit, Obsidian Wave, Ember Storm) | DELIVERED (`internal/project`) |
| Fullscreen: single state controller, persisted intent vs observed platform state, F11 toggle, Esc exit, event-driven reconciliation (never a force loop) | DELIVERED (`internal/fullscreen`, `ui/fyne`; state-transition tests PASS — interactive desktop behavior: NOT RUN on this headless verification host) |
| Runtime logging in every interactive launch: UTC text log, rotation, stderr fallback, startup/subsystem/error records, panic stacks | DELIVERED (`internal/logging` + startup wiring; real-run probe evidence captured) |
| User-facing diagnostics: Help / Diagnostics dialog with Open Logs Folder, Copy Log Path, resolved data mode + log path | DELIVERED (`ui/fyne` actions; button execution needs an interactive desktop: NOT RUN here) |
| Predictable app-local data layout: portable `data/` beside the executable (portable.mode marker) or installed per-user location (Windows LOCALAPPDATA, Linux XDG), with visible fallback and non-destructive legacy migration | DELIVERED (`internal/apppaths` + package staging gates; resolver/bootstrap/migration tests PASS) |
| Windows x64 build | DELIVERED as script/workflow definition producing exactly one canonical package (`SHEYTAN-Music-Visualizer-windows-x64.zip`, uploaded VERIFIED or TEST-ONLY); **native Windows execution: NOT RUN on this Linux verification host — hosted `build-windows` owns it** |

## Supported formats

Input (first-party Go decoders, no external codec process for import):

- **WAV** — RIFF PCM int16/int24/int32 and IEEE float32
- **MP3** — MPEG Layer III (LAME/Xing gapless aware) via `llehouerou/go-mp3`
- **FLAC** — via `tphakala/go-flac` (streaming PCM API)
- **Ogg/Vorbis** — via `jfreymuth/oggvorbis`

**AAC family (optional, through your local FFmpeg):** when `ffmpeg` and
`ffprobe` are available, the app additionally registers **M4A/MP4 (AAC)**,
**raw AAC/ADTS** and **ALAC** import through a bounded local-FFmpeg adapter
(`SHEYTAN_FFMPEG` overrides the binary directory). Nothing is bundled or
downloaded: without FFmpeg those formats stay unregistered, and the UI's
file dialog and the decoder registry agree on the registered list by
construction. The cache sidecars record the adapter provenance truthfully
(`NativeDecoder: false`).

Export: MP4 (H.264 video + AAC audio) through your **locally installed
FFmpeg/ffprobe**. The application never downloads or bundles an encoder
binary; export is blocked with a clear message when FFmpeg is missing.

## Audio pipeline in one paragraph

Every imported track is decoded once by an owned worker goroutine into a
**canonical cache**: 48 000 Hz, stereo, float32 little-endian, stored as a
raw `.f32` file, a provenance sidecar `.json` (fingerprint, decoder,
resampler versions) and a canonical `.wav` wrapper used for export audio.
Cache identity is the SHA-256 content fingerprint — never filename or mtime.
Playback, analysis and export all read the finished cache; non-thread-safe
decoders are never shared across goroutines.

## Build prerequisites

- **Go** at the `go.mod` toolchain (1.27 line). The desktop build uses CGO.
- **Linux**: `gcc`, `pkg-config` and the Fyne native libraries
  (`libgl1-mesa-dev libegl1-mesa-dev libx11-dev libxcursor-dev
  libxrandr-dev libxinerama-dev libxi-dev libxxf86vm-dev libwayland-dev
  libxkbcommon-dev xorg-dev` on Debian/Ubuntu).
- **Windows**: MSYS2 **UCRT64** toolchain (not the deprecated MINGW64):
  `mingw-w64-ucrt-x86_64-gcc` and `mingw-w64-ucrt-x86_64-pkgconf`, with
  `CGO_ENABLED=1 GOOS=windows GOARCH=amd64`.
- **FFmpeg/ffprobe 6+** on PATH (or a user-configured location) — only
  needed for MP4 export, not for import/playback/preview.
- Build tag: `-tags goffi_static` is **required** for binaries that link
  Fyne (CGO) and wgpu (goffi FFI) together; the build scripts and CI apply
  it automatically. See `ai-notes.md` for the linker-conflict evidence.

## Build and verify

Linux (Bash):

```bash
scripts/build/build-linux.sh --mode desktop   # gates + exe + runtime + race + export/GPU proofs
scripts/build/build-linux.sh --mode preflight # static gates only
```

Windows (PowerShell 7+, UCRT64 tools on PATH):

```powershell
./scripts/build/build-windows.ps1
```

Every command's stdout/stderr is captured from the first byte into
`ci-logs/phase2-build.log` with structured records and true exit codes; the
script exits non-zero if any required gate fails.

CI regression fixtures (deterministic, no GitHub credentials needed):

```bash
scripts/ci/tests/test-ci-plumbing.sh                 # bootstrap/capture/DAG/UCRT64-static/fallback fixtures
scripts/ci/tests/test-package-gates.sh               # packaging/process/root-resolution fixtures (P1–P7)
scripts/ci/tests/test-codeql-coverage-inventory.sh   # CodeQL file-coverage proof
scripts/ci/tests/Test-CaptureStep.ps1                # PowerShell capture fixtures (needs pwsh 7)
scripts/ci/tests/Test-ResolveRepoRoot.ps1            # repository-root resolution scenarios (needs pwsh 7)
go test ./internal/cilog/                            # collector correctness fixtures
```

## Documentation

Start from [docs/INDEX.md](docs/INDEX.md) — the canonical index of every maintained document. map

- [docs/architecture.md](docs/architecture.md) — package map, startup
  sequence, threading and single-window contracts.
- [docs/ci.md](docs/ci.md) — build requirements, the exact gate list,
  the repository-root resolution contract, one-canonical-package-per-OS
  artifacts,
  and how to reproduce a failed gate locally.
- [docs/diagnostics.md](docs/diagnostics.md) — log layout and rotation,
  startup metrics, benchmark/profiling commands, and how to read the
  DETECTED/SUPPORTED/SELECTED/EXECUTING/VERIFIED capability states.
- [docs/performance-baseline.md](docs/performance-baseline.md) —
  measured baselines and before/after records of every accepted
  optimization.
- [CHANGELOG.md](CHANGELOG.md) — what changed, grouped by session.
- [.github/SECURITY.md](.github/SECURITY.md) — private security
  reporting; [.github/CONTRIBUTING.md](.github/CONTRIBUTING.md) —
  ground rules and workflow.

## GPU backend preference

The renderer selects a real hardware adapter when one can be proven
(adapter + device + executed draw + verified readback); software/noop
adapters are never reported as hardware GPU. The optional
`SHEYTAN_GPU_PREFERENCE` environment variable selects a backend:
`auto` (default; hardware first — Vulkan, then DirectX 12 on Windows),
`vulkan`, `dx12` or `software`. An explicit selection either selects
that backend or reports the exact reason it could not; it never
silently falls back to software while claiming the requested backend.
The live backend identity (adapter, device type, backend API and the
fallback reason) is shown in the studio transport bar and in the About
dialog.

## Run

```bash
go run -tags goffi_static ./cmd/sheytan-music-visualizer --version      # product + build metadata
go run -tags goffi_static ./cmd/sheytan-music-visualizer --self-check   # invariants incl. decoders, DSP fixture, renderer, ffmpeg probe
go run -tags goffi_static ./cmd/sheytan-music-visualizer --debug       # the studio window with debug-level runtime log records
go run -tags goffi_static ./cmd/sheytan-music-visualizer                # the studio window
```

Keyboard: **Space** play/pause, **←/→** seek, **F11** fullscreen toggle,
**Esc** exit fullscreen.

## Studio walkthrough

1. **Import Audio…** — pick a WAV/MP3/FLAC/Ogg file. Import runs in the
   background (real progress; cancellable) and writes the canonical cache.
2. The analyzer starts automatically; the transport shows the analysis
   state and the Song DNA line reports **measured** features (brightness,
   warmth, punch, drive, loudness) plus the suggested mode.
3. Press **Play**. The central preview renders the actual renderer output
   synchronized to the audio clock (position derives from samples handed to
   the device minus the buffered remainder — never a free-running timer).
4. The **inspector** controls (mode, sensitivity, smoothing, glow, palette,
   particle density, pulse response, title/subtitle overlay, watermark)
   mutate the real preview state — the same configuration drives export.
5. **Save Project** writes a versioned JSON (media reference + fingerprint
   + settings); **Open Project** restores it and relinks the media.
6. Keyboard transport: **Space** toggles play/pause, **←/→** seek ∓5 s.
7. **Export MP4…** shows the detected FFmpeg version and proven H.264
   encoders, resolution presets through 4K UHD, 24/30/60 fps, quality
   presets, real progress (rendered frames + encoder frames), cancel, and
   an ffprobe validation verdict before the file is finalized atomically.

## Logs and data layout

- **Where your runtime log lives.** The application resolves ONE data
  layout at startup (`internal/apppaths`):
  - Portable package (a `portable.mode` marker sits beside the
    executable): everything under `<package>/data/` —
    `logs/app.log`, `config/`, `cache/`, `projects/`, `exports/`,
    `temp/`, `crashes/`.
  - Installed build: the same tree under the writable per-user
    location — Windows `%LOCALAPPDATA%\SHEYTAN Music Visualizer`
    (deliberately never the roaming profile), Linux
    `$XDG_DATA_HOME/SHEYTAN Music Visualizer` (default
    `~/.local/share/...`).
  - If nothing is writable, a visible temporary fallback is used and
    the exact reason is recorded (never silent, never scattered).
- **Finding the log in the app**: Help / Diagnostics shows the data
  mode, the resolved data root and the exact log path, with
  **Open Logs Folder** and **Copy Log Path** actions.
- **Log format and retention**: readable one-line text records with
  UTC timestamps and severity (`app.log`, 5 MiB rotation, one retained
  generation `app.log.1`). Startup records version/commit/build
  date/builder, Go version, OS/arch, PID, data mode and log path;
  subsystem initialization, renderer/audio/FFmpeg decisions, export
  start/result/failure, fullscreen requested-vs-observed transitions
  and recoverable panic stacks are recorded. No audio content,
  credentials or environment dumps are ever logged. A broken log file
  never aborts the launch: a stderr fallback is used and surfaced.
- **Overrides** (explicit configuration, never silent fallbacks):
  `SHEYTAN_DATA_DIR` selects the whole data root; `SHEYTAN_LOG_DIR`
  selects the log directory (used by CI and tests).
- **Migration**: logs written by older versions under the previous
  location (`%APPDATA%` on Windows, `~/.config` on Linux) are copied
  once as `app.legacy.log` — originals are never moved or deleted.
- **FFmpeg resolution order** (`internal/fflocate`): explicit
  configuration (`SHEYTAN_FFMPEG`), then the app-local
  `tools/ffmpeg/bin` folder, then `PATH`. No binary is downloaded or
  bundled; see `packaging/FFMPEG-TOOLS-LICENSING.md` for the legal
  position and the user-approved local setup path.
- **Build/CI logs**: `ci-logs/` (bootstrapped by `scripts/ci/bootstrap-logs.sh`
  / `Bootstrap-Logs.ps1` before anything writes into it). Every executed
  gate writes a structured text record AND a machine-readable
  `ci-logs/gate-results/<gate>.result.json`; unexecuted gates have no
  record — absence is never a pass. CI uploads one namespaced,
  attempt-suffixed log-part artifact per evidence-producing job
  (`preflight-logs-part-attempt<N>`, `windows-logs-part-attempt<N>`,
  `linux-logs-part-attempt<N>`, `codeql-logs-part-{go,actions}-attempt<N>`),
  then `cmd/ci-log-collector` aggregates them (plus the authoritative GitHub
  job/step conclusions and open code-scanning alerts) into ONE artifact named
  exactly `log.txt`. That final artifact is uploaded with
  `actions/upload-artifact` v7 `archive: false`: **the download is the plain
  `log.txt` file itself, not a ZIP containing it** (validated in-workflow:
  no ZIP signature, exactly one consolidated artifact). The pre-compressed
  Windows portable ZIP and the Linux portable tar.gz are likewise uploaded
  non-archived, so downloads are never double-wrapped. The internal log parts
  are deleted only after the final upload is validated. Every Linux CI job
  runs on the explicit `ubuntu-26.04` runner label. Details:
  [docs/ci.md](docs/ci.md).
- The packaged Phase 2 verification transcript lives in `evidence/`.

## License

Proprietary — see [license.md](license.md) (Parsaetak Proprietary License
v1.1). SHEYTAN™ is a trademark of Parsaetak; third-party dependencies remain
under their own licenses as inventoried there.

- Licensor / rights holder: Parsaetak — https://linktr.ee/Parsaetak
- Product / brand / project home: SHEYTAN — https://github.com/SHEYTANx13

No CI badges are shown because hosted workflow runs have not been observed
(no push/PR was made from this session).
