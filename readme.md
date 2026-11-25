# SHEYTAN Music Visualizer

**SHEYTAN™** · Licensor / rights holder: [Parsaetak](https://linktr.ee/Parsaetak) ·
Product/brand/project home: [SHEYTAN](https://github.com/SHEYTANx13) ·
© 2024–2026 Parsaetak. All rights reserved.

SHEYTAN Music Visualizer is a **local-first desktop studio** that imports a
song, analyzes it locally, derives a deterministic per-track visual identity
("Song DNA") and renders music-reactive visuals in a native Go studio shell —
then exports synchronized H.264/AAC MP4 videos up to 3840×2160 (4K). Nothing
runs in the cloud: decoding, analysis, rendering and encoding happen on your
machine. No account, no telemetry, no bundled codec binaries.

> Shell architecture (2026-11-19/20 migration, run 44462492262): one
> toolkit-free Go application core (`internal/*`) with TWO alternative
> presentation shells and ONE renderer subsystem behind a contract:
> - **Native Go-Gui shell** (`cmd/app-gogui`, the actual Go-Gui library
>   `github.com/go-gui-org/go-gui v0.99.1` — NOT Gio `gioui.org`, which was
>   removed in the 2026-11-21 migration) —
>   the production desktop shell: one window, the eleven workspace tabs,
>   SHEYTAN design tokens, PixelSurface CPU-frame preview bridge, honest GPU
>   evidence, cgo-free Linux desktop (pure-Go X11, runtime GL dlopen).
> - **Wails v3 web shell** (`cmd/app-wails`, `wailsapp/wails/v3 v3.1.0`) —
>   an alternative cgo-free web-technology shell (purego profile,
>   `sheytan_nogpu` build tag: the web-shell profile renders through the
>   honest CPU renderer and never claims native GPU execution).
> - **GoGPU renderer** (`github.com/gogpu/wgpu v0.34.15`) — the
>   specialized GPU renderer behind `internal/render`; adapter evidence
>   states (detected/available/selected/executed/verified) are exact and
>   software adapters are never labeled GPU.

## Launching the app (run 45297707372 ship-the-shell repair)

The packaged product executable is the **native Go-Gui shell** —
`app-gogui.exe` (Windows), `app-gogui` (Linux), or the `.app` bundle's
`SHEYTAN-Music-Visualizer` binary (macOS). Launch THAT file; it opens
the real window, carries the SHEYTAN icon on the executable file, and
its version label is BETA. The package also ships
`sheytan-music-visualizer` — the CLI authority — as a CONSOLE companion
for scripting, `--version`, `--self-check` and headless export; it
opens no window by design.

Every launch writes first-byte diagnostics: the startup banner (exact
build identity, OS/arch, PID, data mode, log path) is the FIRST record
in `logs/app.log` under the data root; every startup phase logs success
or its exact failure reason; recovered panics are persisted as
`crashes/crash-<utc>.txt`; the process exit code is recorded at exit.
See docs/TROUBLESHOOTING.md ("App does not open").
## Product build matrix (priority-B publication contract)

| Platform | Required output | Publication |
|---|---|---|
| Windows x64 | `SHEYTAN-Music-Visualizer-windows-x64.zip` | VERIFIED-only candidate → workflow-wide publisher |
| Linux x64 | `SHEYTAN-Music-Visualizer-linux-x64.tar.gz` | VERIFIED-only candidate → workflow-wide publisher |
| macOS arm64 | `SHEYTAN-Music-Visualizer-macos-arm64.app.zip` (real `.app` bundle; unsigned test build, honestly labeled) | candidate → publisher |
| Android arm64 | `SHEYTAN-Music-Visualizer-android-arm64.apk` (real APK; debug-signed, honestly labeled) | REQUIRED product gate → publisher |
| Web (Wails shell) | cgo-free alternative profile, no package published | alternative shell |

Platform jobs never publish under the final public artifact name: they
produce internal `*-candidate-attemptN` artifacts and the single
`publish-products` job re-validates and republishes under the final
names. A red run publishes ONLY the diagnostics artifact. Internal
candidates and log parts are removed by `cleanup-internal`; a cleanup
failure makes the final verdict red.

## Music analysis v3

`internal/dsp` measures (streaming, deterministic, chunk-size
independent): stereo metrics with anti-phase detection, nine
calibrated frequency bands, spectral descriptors + chroma/tuning,
multi-resolution spectra, HPSS evidence, rhythm (beats/downbeats/meter
with honest confidence), key/chords with confidence floors, vocal-like
phrase/flow estimates, heuristic instrument roles, structural
sections, and BS.1770-5 loudness. The composition system consumes the
same measured timeline in preview and export. See
[docs/ANALYSIS-V3.md](docs/ANALYSIS-V3.md).

> The former Fyne production UI (`ui/fyne`) was removed only after the
> replacement surface reached parity; no hidden Fyne fallback remains
> (enforced by `scripts/ci/check-no-fyne.sh` in CI).

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
> recovery/duplicate builds. See [docs/roadmap.md](docs/roadmap.md) for the honest
> status of every capability and [docs/ai-notes.md](docs/ai-notes.md) for the
> evidence trail. The 2026-11-13 session (run 43090342995) repaired the playback clock contract, delivered one authoritative shutdown path with a close confirmation, a responsive breakpoint shell (320×568 phone → 3840×2160 ultrawide), first-frame deferral of the renderer probe and audio-device open, and machine-readable build identity (`--build-info-json`) as the package commit authority.

---

## Visual identity

The design direction is **black and red — hellish / infernal**, executed in a
premium, cinematic, restrained way: black dominates, red is the accent, warm
white is reserved for text and peaks. The identity tokens are defined once in
`internal/brand/brand.go` (obsidian background `#080808`, volcanic surfaces,
dark-blood crimson `#9F0B18`, infernal action red `#D51D2A`, ember
highlights) are the identity source. The canonical, framework-independent presentation
encoding of the same direction lives in `design/tokens.json` (intensity
presets Subtle/Ritual/Infernal, reduced-motion and reduced-glow switches,
responsive breakpoints, accessibility contrast pairs) and is consumed by
BOTH shells via the `design` package — see
[docs/DESIGN-SYSTEM.md](docs/DESIGN-SYSTEM.md) — plus the four brand
palettes used by the visualizer (`internal/visualizer`).

## What works today (Phase 2 slice)

| Capability | Status |
|---|---|
| Studio shell: top command bar, eleven workspace tabs (Home/Studio/Worlds/Templates/Library/Export/Requirements/Downloads/System Files/Settings/Diagnostics), library, live preview, inspector, transport, export dialog | DELIVERED (native Go-Gui shell `cmd/app-gogui` on `github.com/go-gui-org/go-gui v0.99.1` with SHEYTAN token theme; Wails v3 web shell `cmd/app-wails` as the alternative cgo-free profile) |
| Import WAV (int16/int24/int32 + IEEE float32), MP3, FLAC, Ogg/Vorbis via first-party Go decoders | DELIVERED (`internal/audio`) |
| Import AAC/M4A/ALAC via the user's local FFmpeg (optional adapter, never bundled) | DELIVERED as an optional adapter (`internal/audio/decoders_ffmpeg.go`; registers only when FFmpeg is present) |
| Layered compositions: a dimmed background mode behind the primary mode | DELIVERED (CPU renderer, deterministic, bounded intensity; `internal/render`) |
| Manual tempo override and audio/visual sync offset, persisted per project | DELIVERED (`internal/studio`, `internal/project`) |
| Canonical PCM pipeline: 48 kHz stereo float32, disk-backed cache, Kaiser-sinc resampling, atomic writes | DELIVERED |
| Playback: Oto v3 single context/player, play/pause/stop/seek/volume, buffering-aware playhead | DELIVERED (device-less hosts fall back to an honest silent mode) |
| DSP: Hann FFT-2048/hop-512, bass/mid/treble bands, RMS, spectral centroid, flux/onsets, BPM with confidence | DELIVERED (`internal/dsp`) |
| Song DNA: deterministic per-track identity from measured features + content fingerprint + user seed | DELIVERED (`internal/songdna`) |
| Visualizer modes: Spectrum Bars, Radial Spectrum, Waveform, Particle Field, Spectrogram Waterfall, Pulse Tunnel, Stereo Wave Ribbons, Frequency Bloom — eight distinct renderers with stable IDs, categories and one tuned preset per mode | DELIVERED (`internal/visualizer`, `internal/render/modes_extra.go`) |
| CPU renderer: deterministic software rendering used by preview and export | DELIVERED |
| GPU renderer: wgpu (Go-controlled WebGPU) radial-mode shader with verified readback; hardware adapters on Vulkan/DX12/Metal hosts | DELIVERED as code + software-adapter proof; **hardware-GPU execution: NOT AVAILABLE on the verification host** |
| MP4 export: 1280×720 → 3840×2160 at 24/30/60/**120** fps, H.264 + AAC via FFmpeg, streamed frames, real progress, cancellation, ffprobe validation; 4K120/4K60 functional export tests with exact-cadence ffprobe checks | DELIVERED (`internal/export`; run 43612608237: 4K120 verified locally — 120 frames, exact 1.000 s, ffprobe-validated) |
| Projects: versioned JSON, atomic save, validation, relink, presets (Hellfire Pulse, Crimson Orbit, Obsidian Wave, Ember Storm) | DELIVERED (`internal/project`) |
| Fullscreen: single state controller, persisted intent vs observed platform state, event-driven reconciliation (never a force loop) | CONTROLLER DELIVERED (`internal/fullscreen`, state-transition tests PASS); the desktop shell wiring is NOT WIRED since the ui/fyne removal — outstanding work (go-gui exposes `Window.SetFullscreen`/`IsFullscreen`; the adapter + F11/Esc handling is the next slice) |
| Runtime logging in every interactive launch: UTC text log, rotation, stderr fallback, startup/subsystem/error records, panic stacks | DELIVERED (`internal/logging` + startup wiring; real-run probe evidence captured) |
| User-facing diagnostics: Diagnostics destination panel with the diagnostics summary, resolved data mode + log path, Open Logs Folder action | DELIVERED (`ui/gogui` panels + actions; button execution needs an interactive desktop: NOT RUN here) |
| Predictable app-local data layout: portable `data/` beside the executable (portable.mode marker) or installed per-user location (Windows LOCALAPPDATA, Linux XDG), with visible fallback and non-destructive legacy migration | DELIVERED (`internal/apppaths` + package staging gates; resolver/bootstrap/migration tests PASS) |
| Windows x64 build | DELIVERED as script/workflow definition producing exactly one canonical package (`SHEYTAN-Music-Visualizer-windows-x64.zip`, uploaded VERIFIED or TEST-ONLY); **native Windows execution: NOT RUN on this Linux verification host — hosted `build-windows` owns it** |

## Supported formats

Input (first-party Go decoders, no external codec process for import):

- **WAV** — RIFF PCM int16/int24/int32 and IEEE float32
- **MP3** — MPEG Layer III (LAME/Xing gapless aware) via `llehouerou/go-mp3`
- **FLAC** — via `tphakala/go-flac` (streaming PCM API)
- **Ogg/Vorbis** — via `jfreymuth/oggvorbis`

**AAC family (optional, through the app-managed or local FFmpeg):** when
`ffmpeg` and `ffprobe` are available, the app additionally registers
**M4A/MP4 (AAC)**, **raw AAC/ADTS** and **ALAC** import through a bounded
FFmpeg adapter (`SHEYTAN_FFMPEG` overrides the binary directory). Nothing
is bundled in the package: without a resolvable FFmpeg pair those formats
stay unregistered, and the UI's file dialog and the decoder registry agree
on the registered list by construction. The cache sidecars record the
adapter provenance truthfully (`NativeDecoder: false`).

Export: MP4 (H.264 + HEVC video, AAC audio) through the app-managed
**Media Tools & Codecs** download. The published packages are COMPACT:
they ship **no FFmpeg binaries** — the optional pinned GPL build
(~184 MB Windows / ~143 MB Linux download) is installed by the in-app
Media Tools & Codecs manager into `<DataRoot>/media-tools/current/bin`
(see `packaging/FFMPEG-TOOLS-PROVENANCE.md` for the exact pinned build,
hashes and GPL labeling). Tool resolution order: `SHEYTAN_FFMPEG`
override → app-managed `<DataRoot>/media-tools/current/bin` → app-local
`<exeDir>/tools/ffmpeg/bin` → `PATH`. Without a resolvable pair, export
is blocked with a clear message.

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
- **Linux**: `gcc` (the GoGPU wgpu renderer stack compiles cgo) and the
  RUNTIME UI libraries only — the go-gui v0.99.1 desktop backend is
  cgo-free (pure-Go X11 via jezek/xgb; libEGL/libGL/wayland/xkbcommon are
  dlopened at run time, so no `-dev` headers or pkg-config UI modules are
  needed):
  `libegl1 libgl1 libvulkan1 mesa-vulkan-drivers libwayland-client0
  libxkbcommon0 libx11-6` (mesa-vulkan-drivers for software-Vulkan smoke
  runs). `xvfb` + `x11-utils` for the virtual-display GUI smoke.
- **Windows**: MSYS2 **UCRT64** toolchain (not the deprecated MINGW64):
  `mingw-w64-ucrt-x86_64-gcc` and `mingw-w64-ucrt-x86_64-pkgconf`, with
  `CGO_ENABLED=1 GOOS=windows GOARCH=amd64`.
- **FFmpeg/ffprobe** (optional; app-managed download via the in-app Media
  Tools & Codecs manager, the `SHEYTAN_FFMPEG` override, the app-local
  `tools/ffmpeg/bin` folder, or PATH; 6+ for the optional AAC/M4A adapter)
  — only needed for MP4 export and the AAC-family import adapter, not for
  built-in import/playback/preview.
- Build tag: `-tags goffi_static` is **required** for binaries that link
  Fyne (CGO) and wgpu (goffi FFI) together; the build scripts and CI apply
  it automatically. See `docs/ai-notes.md` for the linker-conflict evidence.

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
- [docs/TROUBLESHOOTING.md](docs/TROUBLESHOOTING.md) — log layout and rotation,
  startup metrics, benchmark/profiling commands, and how to read the
  DETECTED/SUPPORTED/SELECTED/EXECUTING/VERIFIED capability states.
- [docs/performance-baseline.md](docs/performance-baseline.md) —
  measured baselines and before/after records of every accepted
  optimization.
- [docs/CHANGELOG.md](docs/CHANGELOG.md) — what changed, grouped by session.
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
  configuration (`SHEYTAN_FFMPEG`), then the app-managed pair under
  `<DataRoot>/media-tools/current/bin` (installed by the in-app Media
  Tools & Codecs manager), then the app-local `tools/ffmpeg/bin` folder,
  then `PATH`. No FFmpeg binary ships inside the package; see
  `packaging/FFMPEG-TOOLS-LICENSING.md` for the legal position and
  `packaging/FFMPEG-TOOLS-PROVENANCE.md` for the pinned download.
- **Build/CI logs**: `ci-logs/` (bootstrapped by `scripts/ci/bootstrap-logs.sh`
  / `Bootstrap-Logs.ps1` before anything writes into it). Every executed
  gate writes a structured text record AND a machine-readable
  `ci-logs/gate-results/<gate>.result.json`; unexecuted gates have no
  record — absence is never a pass. CI uploads one namespaced,
  attempt-suffixed log-part artifact per evidence-producing job
  (`preflight-logs-part-attempt<N>`, `windows-logs-part-attempt<N>`,
  `linux-logs-part-attempt<N>`; CodeQL runs as the independent `codeql`
  workflow with its own consolidated `log.txt` artifact),
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

Proprietary — see [docs/license.md](docs/license.md) (Parsaetak Proprietary License
v1.1), the sole licensing artifact; the root [`LICENSE`](LICENSE) file is a
discoverability pointer with no independent legal authority. SHEYTAN™ is a
trademark of Parsaetak; third-party dependencies remain under their own
licenses as inventoried there.

- Licensor / rights holder: Parsaetak — https://linktr.ee/Parsaetak
- Product / brand / project home: SHEYTAN — https://github.com/SHEYTANx13

No CI badges are shown because hosted workflow runs have not been observed
(no push/PR was made from this session).
