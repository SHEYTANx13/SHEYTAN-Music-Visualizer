# roadmap.md — SHEYTAN Music Visualizer Product Direction

SHEYTAN™ · Licensor: Parsaetak · © 2024–2026 Parsaetak. All rights reserved.

This document is the authority for the full product direction and phase
statuses. Future features are described as planned; nothing here may be
read as delivered without code, tests and/or CI evidence.

Status vocabulary (only these): `PLANNED`, `IN PROGRESS`, `DELIVERED`,
`BLOCKED`, `DEFERRED`. A feature is `DELIVERED` only when code, tests
and/or CI prove it.

---

## Product outcome

A premium local desktop **music-visualizer studio** that analyzes each song
and generates a unique visual identity from its frequency, rhythm, dynamics
and musical structure. Users customize the scene and export synchronized
MP4 music videos up to 3840×2160 (4K). Everything — analysis, rendering,
encoding — runs locally. No cloud dependency, no account, no telemetry.

Visual direction: black and red (hellish / infernal), premium, cinematic,
restrained. Black dominates; red is the accent. Identity tokens live in
`internal/brand/brand.go`.

## Foundation architecture

- **Go** owns application lifecycle, project model, audio-analysis
  coordination, the deterministic frame timeline, render scheduling,
  configuration, diagnostics and export orchestration. First-party engines
  are Go-only and local.
- **Fyne** owns the native desktop UI only — never the media renderer or
  export-frame renderer.
- **Local storage** owns project metadata, presets, thumbnails and user
  settings; source media is referenced, not copied unnecessarily.
- **Audio, graphics and video backend integrations sit behind narrow Go
  interfaces** with clear CPU fallbacks where meaningful.
- **UI, preview and offline export share the same deterministic visual
  render model.** The UI preview is never the source of truth for exported
  frames.
- No cloud render path, remote visualizer service, or mandatory account —
  ever.

---

## Phase 1 — foundation mission (this phase)

| Item | Status | Evidence |
|---|---|---|
| Root documents (`license.md`, `readme.md`, `agent.md`, `roadmap.md`, `ai-notes.md`) | DELIVERED | files present, mutually consistent |
| Go/Fyne application scaffold, identity, buildinfo, logging authorities | DELIVERED | `internal/*`, `cmd/*`, `ui/fyne`; `go build ./...`, `go test ./...` PASS |
| `--version` / `--self-check` CLI contracts | DELIVERED | captured in `evidence/phase1-build.log` |
| Build scripts with first-byte capture and true exit codes (Linux + Windows) | DELIVERED (Linux executed; Windows script statically validated, NOT RUN — no Windows host) | `evidence/phase1-build.log` |
| `build-desktop.yml` (preflight, Windows x64 UCRT64, Linux x64, collect-logs) | DELIVERED as workflow definition; 2026-11-06 repair: guaranteed log bootstrap in every job, fully independent evidence DAG (a failed preflight can no longer skip Windows/Linux), attempt-suffixed artifact identity, emergency fallback report, GATES NOT REACHED semantics. 2026-11-07 repair (run 42231355353): the "Verify UCRT64 toolchain" step is EXECUTED (scripts/ci/Verify-UCRT64.ps1 invoked as a file — the bare script-block-into-Tee-Object false green is gone), manifest rows carry kind + result basis, and the Linux job installs xvfb + x11-utils so the GUI smoke can prove a mapped window. HOSTED CI: NOT RUN (no push was made) | actionlint + YAML parse + semantic spot checks + `scripts/ci/tests/test-ci-plumbing.sh` PASS (`evidence/phase-workflow-validation.log`) |
| `codeql.yml` (go manual + actions none) | DELIVERED as workflow definition; 2026-11-06 repair: structured capture for the traced build, per-leg attempt-suffixed parts, least-privilege caller. Go-file coverage 40/66 explained: 66 = 40 production + 26 test files (extract_tests=false); `scripts/ci/tests/test-codeql-coverage-inventory.sh` reproduces the proof. CodeQL execution itself: NOT RUN (no push) | `evidence/codeql-coverage-inventory.txt`, `evidence/phase-codeql.log` |
| Log capture + consolidation subsystem with fixture tests | DELIVERED | `internal/cilog` tests PASS |
| Source archive + external handoff note | DELIVERED | ZIP integrity + extracted-tree verification |
| Runtime logging users can attach (run 42481815519) | DELIVERED: `internal/logging` rewritten as a normal-startup subsystem (readable UTC text `app.log`, 5 MiB rotation with one retained generation, append never truncate, stderr fallback surfaced, legacy `%APPDATA%`/`~/.config` logs preserved by copy as `app.legacy.log`, concurrent-writer safe, `--debug` verbose mode without changing default volume). Startup wiring resolves the data layout BEFORE any subsystem. Probe evidence: real binary run in portable mode with a space-containing path — banner, subsystem events, concurrent records, clean shutdown, second launch appends | `internal/logging`, `ui/fyne/runtime.go`, `cmd/sheytan-music-visualizer` |
| Predictable app-local data layout (run 42481815519) | DELIVERED: single resolver `internal/apppaths` (portable marker / installed per-user LOCALAPPDATA / XDG / visible fallback with reason), directory bootstrap with write probes, non-destructive migration; `studio.DataDir()` no longer invents its own `os.UserConfigDir()` path; packages stage the full tree (exe, license.md, readme.md, `portable.mode`, `tools/ffmpeg/`, `data/`) with ONE top-level folder | `internal/apppaths`, `internal/studio`, `scripts/build/*`, `packaging/` |
| Fullscreen state controller (run 42481815519) | DELIVERED: `internal/fullscreen` — desired (persisted `config/window-state.json`) vs observed (`Window.FullScreen()`) separation, F11 toggle + Esc exit, event-driven debounced reconciliation (no timers, no focus stealing, user exit is final), geometry preservation, refusal handling with truthful logging. State-transition tests PASS; interactive desktop flows: NOT RUN (headless host) | `internal/fullscreen`, `ui/fyne` |
| FFmpeg single resolution authority + app-local tools (run 42481815519) | DELIVERED: `internal/fflocate` (configured override > app-local `tools/ffmpeg/bin` > PATH) used by BOTH export and the optional audio adapter; dead `pathOverride` removed. No binary bundled — redistribution cannot be legally verified for this proprietary package within this task; documented in `packaging/FFMPEG-TOOLS-LICENSING.md` | `internal/fflocate`, `internal/export/ffmpeg.go`, `internal/audio/decoders_ffmpeg.go` |
| Direct artifact delivery (run 42481815519) | DELIVERED: final `log.txt` uploaded with upload-artifact v7 `archive: false` (plain-text download, ZIP-signature gate added in the workflow), Windows portable ZIP and Linux portable tar.gz uploaded non-archived (no double ZIP); artifact id/URL recorded to the job summary after upload | `.github/workflows/build-desktop.yml` |
| Package license identity gates (run 42481815519) | DELIVERED: byte-for-byte SHA-256/cmp comparison canonical → staged → extracted archive in both platform build scripts; gates fail the run on any mismatch | `scripts/build/build-windows.ps1`, `scripts/build/build-linux.sh` |
| Virtual-display GUI smoke with window proof | DELIVERED: the gui-smoke gate polls Xvfb readiness (xdpyinfo, bounded), asserts a MAPPED application window (xwininfo root tree, product-title match), keeps it under observation, scans the UI log for panics and closes gracefully via SIGTERM with a bounded wait; SIGKILL is cleanup-only and recorded as a failed graceful-close assertion. Tooling (xvfb, x11-utils) is installed by the Linux CI job. It is virtual-display evidence and never real display-driver/physical-GPU evidence. | `scripts/build/build-linux.sh` (gui-smoke gate), `scripts/ci/tests/test-ci-plumbing.sh` D26 |

## Phase 2 — studio and media implementation (Phase 2 slice, 2026-11-04)

Status after the Phase 2 session (evidence: `evidence/phase2-build.log`,
`ai-notes.md`). Statuses distinguish what is implemented+verified locally
from what still requires hardware or hosts this session never had.

1. **Studio foundation UI/UX** — DELIVERED. Full studio shell (top bar,
   library/presets rail, live preview of actual renderer output, inspector,
   audio-clock transport, export dialog) with the hellish black/red theme
   driving real widgets. Keyboard navigation beyond tab order: DELIVERED
   (Space = play/pause, Left/Right = ±5 s seek through the same service
   actions as the transport buttons; `ui/fyne/studio.go`).
2. **Media foundation** — DELIVERED. WAV (int16/int24/int32/float32), MP3,
   FLAC, Ogg/Vorbis import through first-party Go decoders; canonical
   48 kHz stereo float32 disk cache with fingerprint-keyed sidecars,
   bounded streaming, cancellation; malformed-input tests.
   M4A/AAC/ALAC input: DELIVERED as an OPTIONAL adapter through the
   user's local FFmpeg/ffprobe (no bundled codec, no new dependency;
   the formats register only when FFmpeg is present and the registry,
   UI file dialog and cache sidecars report the provenance truthfully;
   `internal/audio/decoders_ffmpeg.go`). First-party AAC decoding
   without FFmpeg remains deliberately not promised.
3. **Audio playback and synchronization** — DELIVERED (Linux). Oto v3
   single context/player, seek/pause/stop/volume, buffering-aware playhead
   with documented latency uncertainty. Windows playback runtime: NOT RUN
   on this session's Linux host. Manual tempo/offset correction: DELIVERED
   (per-project tempo override 30–300 BPM that bypasses the confidence
   gate, ±5 s sync offset; both reach the shared frame model and persist
   in projects; `internal/studio`, `internal/project`).
4. **DSP / song analysis** — DELIVERED. Hann FFT-2048/hop-512 (Gonum),
   RMS envelope, bass/mid/treble bands, spectral centroid, flux/onset
   detection with adaptive threshold, BPM estimate with confidence,
   half/double-time handling, waveform envelope and spectral rolloff
   (85% energy accumulation frequency). Verified against generated
   fixtures (pure tones → bands/centroid/rolloff; 120 BPM click → BPM;
   broadband clicks → high rolloff; steady tone → unreliable).
5. **Song-specific identity (`Song DNA`)** — DELIVERED. Deterministic
   derivation from measured features + content fingerprint + user seed;
   reproducibility verified by test; measured-vs-artistic split documented
   in the UI.
6. **Visualizer engines** — DELIVERED. Spectrum Bars, Radial Spectrum,
   Waveform and Particle Field all respond to measured audio features with
   bounded complexity; preview and export share the same logic, config and
   scene contract; determinism verified per mode. Layered
   compositions: DELIVERED on the CPU renderer (a dimmed background
   mode behind the primary mode, user-selectable with bounded
   intensity 0–0.6, deterministic, preview and export share it; the
   GPU shader path covers the primary radial mode only and reports
   honestly; `internal/visualizer.Config.BackgroundLayer`,
   `internal/render/cpu.go`).
7. **GPU backend** — DELIVERED as code with an honest evidence chain.
   `github.com/gogpu/wgpu` v0.34.7 renders the Radial Spectrum mode
   through a WGSL fragment shader with submit + verified readback; the
   full pipeline proof passes on the software adapter and the renderer
   refuses to label software/noop adapters as GPU. 2026-11-07 repair
   (run 42231355353): adapter classification is PROPERTY-based
   (`classifyAdapterInfo`: PCI identity — Microsoft's 0x1414/0x008c
   software adapter — device type, backend) and every renderer reports a
   device class of hardware / software / virtual / unknown; only
   `hardware` can ever be selected as the GPU backend, so a dx12-reported
   "Microsoft Basic Render Driver" (WARP) can never be presented as
   physical GPU acceleration even when the D3D layer mislabels its device
   type. Hardware-GPU execution (Vulkan/DX12/Metal): **NOT AVAILABLE** —
   the verification host has no GPU/display; must be proven on real
   hardware before any acceleration claim. CPU fallback always available
   and verified.
8. **Export pipeline** — DELIVERED (Linux). Deterministic frame schedule,
   streamed RGBA to local FFmpeg, canonical-WAV audio, H.264/AAC,
   720p/1080p/1440p/4K at 24/30/60, quality presets, real progress,
   cancellation, atomic finalize, ffprobe validation. Verified: 1080p30
   and 4K24 short-fixture proofs with metadata/duration/codec checks.
   Windows export runtime: NOT RUN (no Windows host). Hardware encoders
   (NVENC/QSV/AMF) are offered only after a proof encode; none proven on
   this host.
9. **Presets/projects** — DELIVERED. Versioned JSON with validation,
   atomic save, unknown-field tolerance, media relinking, four built-in
   presets (Hellfire Pulse, Crimson Orbit, Obsidian Wave, Ember Storm);
   round-trip tested.
10. **Quality/security** — PARTIALLY DELIVERED. Race tests pass on the
    concurrent core packages; `go mod verify`/`tidy -diff` clean;
    govulncheck (pinned v1.9.0): **no vulnerabilities found** after the
    minimal safe bump (x/net v0.62.0, x/text v0.43.0, x/image v0.45.0);
    licence inventory updated. Performance benchmarks: DELIVERED
    (`internal/dsp/bench_test.go`, `internal/render/cpu_bench_test.go`;
    reference numbers in `ai-notes.md`). GPU-device-loss recovery
    drills: the recovery PATH is drilled (injected ErrDeviceLost →
    FALLBACK state + explicit "device lost:" detail + valid CPU frame;
    `internal/render/gpu_recovery_test.go`); a drill on real hardware
    remains open. Windows packaging/signing: portable staging+ZIP
    proven in the hosted run; code signing still NOT DONE (no
    certificate).

## Research questions kept open for Phase 2

Do not lock unproven libraries during Phase 1. Each item below is an
explicit research gate with measurable requirements:

- **DSP/FFT**: which Go implementation offers numerical correctness,
  maintenance, performance and license compatibility on Windows? Gonum is
  a conservative candidate to evaluate, not a mandate.
- **Decoding**: which decoders cover the required formats? Prefer Go-native
  libraries or OS codec interfaces; document coverage and licensing. Do
  not silently make an external FFmpeg install a mandatory runtime
  requirement.
- **Playback**: which Go playback library gives device stability, seeking,
  buffer/clock observability and Windows support?
- **GPU**: which Go-accessible API works reliably with Windows Direct3D 12
  and can expose real execution/readback? Treat a young or pre-1.0 WGPU
  wrapper as experimental until tested on target hardware.
- **Encoding**: what is the defensible local MP4/H.264/AAC path on Windows?
  Evaluate Windows Media Foundation bindings and codec redistribution
  terms. If an external codec runtime would be required, stop and present
  the exact licensing/runtime trade-off instead of silently adopting it.
- **Hardware encode**: which GPU video-encoding APIs exist on target
  devices/drivers? GPU rendering and hardware video encoding are separate
  capabilities; never conflate them.

---

## CI repair sessions (runs 42231355353 / 42376176493)

The run-42231355353 repairs (Windows build-metadata `-X` contract, executed
UCRT64 identity gate, header-aware `_UCRT` probe, gate tag isolation,
gofmt exit handling, collector report reconciliation) are recorded in
`ai-notes.md` (2026-11-07 session).

The run-42376176493 repairs (2026-11-08, all local, no push) close three
further CI-integrity defects: (1) the manifest is now written by a
deterministic, atomic, self-validating writer (`scripts/ci/write-manifest.py`)
so a missing JSON tool can never again leave an empty manifest behind and
fail the primary collector (rc=2) the way this run did; (2) the UCRT64
gate locates `_mingw.h` at the UCRT64 target header root (verified against
the pacman package database) instead of GCC's private include directory,
with a separate compile/link probe — the run's false negative class is
covered by runtime fixtures; (3) the Linux GUI smoke asserts the real
X11 viewability contract (`Map State: IsViewable` at readiness, through
the observation window and before shutdown) instead of XID existence, and
records headless audio as a structured environment limitation with a
test-only null-PCM configuration. Verification:
`evidence/local-verification-run-42376176493-repair.log`. Hosted
confirmation on the exact repair commit remains NOT RUN (no push was
made).

## CI repair session (2026-11-06, run 42026322748)

The consolidated-log/workflow repair for GitHub Actions run 42026322748 is
complete and locally verified. Root cause and fix map: `evidence/run-42026322748-forensic-analysis.log`.
What changed and why: `ai-notes.md` (2026-11-06 section) and `note.md`
(external handoff). Local verification: full Linux ladder PASS (tests, race,
export proofs, GUI smoke on Xvfb, govulncheck), collector fixtures PASS,
workflow validation PASS. Everything hosted (the actual workflow run,
Windows-target build, CodeQL alerts) remains NOT RUN / NOT OBSERVED because
no push was made — the status vocabulary above was not inflated.

## Phase boundary rule

Phase 1 delivered the foundation only. Phase 2 (this session) implemented
and locally verified the studio slice listed above; every NOT DONE / NOT
RUN / NOT AVAILABLE item is stated exactly and requires either real
hardware (Windows host, hardware GPU) or explicit follow-up work. See
`ai-notes.md` for the evidence-backed state and the next narrow mission.
