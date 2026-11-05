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
| `build-desktop.yml` (preflight, Windows x64 UCRT64, Linux x64, collect-logs) | DELIVERED as workflow definition; HOSTED CI: NOT RUN | actionlint + YAML parse PASS |
| `codeql.yml` (go manual + actions none) | DELIVERED as workflow definition; CodeQL: NOT RUN | actionlint + YAML parse PASS |
| Log capture + consolidation subsystem with fixture tests | DELIVERED | `internal/cilog` tests PASS |
| Source archive + external handoff note | DELIVERED | ZIP integrity + extracted-tree verification |
| Minimal window actually launched on screen | DELIVERED as observed behavior on a virtual display (Xvfb): the studio window was MAPPED, survived the event loop and exited cleanly on SIGTERM; screenshot captured. Physical-desktop observation on real hardware remains open. | `evidence/gui-smoke-xvfb.png`, `evidence/log.txt` (gui-smoke gate), `ai-notes.md` |

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
   refuses to label software/noop adapters as GPU. Hardware-GPU execution
   (Vulkan/DX12/Metal): **NOT AVAILABLE** — the verification host has no
   GPU/display; must be proven on real hardware before any acceleration
   claim. CPU fallback always available and verified.
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

## Phase boundary rule

Phase 1 delivered the foundation only. Phase 2 (this session) implemented
and locally verified the studio slice listed above; every NOT DONE / NOT
RUN / NOT AVAILABLE item is stated exactly and requires either real
hardware (Windows host, hardware GPU) or explicit follow-up work. See
`ai-notes.md` for the evidence-backed state and the next narrow mission.
