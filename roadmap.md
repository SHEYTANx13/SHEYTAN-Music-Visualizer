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
| Minimal window actually launched on screen | NOT RUN on the Phase 1 agent host (headless; no display server) — compiled and `--self-check` verified only | `evidence/phase1-build.log` |

## Phase 2 — studio and media implementation (not started)

Organized as milestones in delivery order. Each milestone lists its
acceptance gate; nothing below is implemented in Phase 1.

1. **Studio foundation UI/UX** — library, import panel, central preview
   surface, transport, inspector, export settings; adaptive desktop layout;
   hellish black/red theme; accessible contrast; resizable window.
   Gate: theme tokens drive real widgets; keyboard navigation works.
2. **Media foundation** — local file import, metadata, duration/sample
   rate/channels, decode strategy, cancellation, large-file handling (no
   whole-track memory load). Gate: import of a real fixture with bounded
   memory and cancellation proven by test.
3. **Audio playback and synchronization** — reliable seek/pause/resume,
   monotonic playback clock, buffering/latency correction, exact frame-time
   mapping, sample-rate and device changes. Gate: frame-time mapping test
   against a known fixture with drift assertions.
4. **DSP / song analysis** — FFT/STFT with documented windowing/hop size;
   RMS/envelope; frequency band energies; spectral centroid/rolloff/flux;
   onset detection; beat/tempo estimation with confidence and
   half/double-time ambiguity handling; manual tempo/offset correction.
   Analysis must be deterministic and tested against known generated
   fixtures; estimates are never presented as certainty.
5. **Song-specific identity (`Song DNA`)** — deterministic DNA derived from
   analysis + explicit user seed/customization; the same source and
   settings reproduce the same baseline visual; users can save/override
   styles. Gate: reproducibility test (same input → identical output hash).
6. **Visualizer engines** — waveform/oscilloscope, spectrum bars, radial
   spectrum, particle field, and layered compositions; each responds to
   appropriate audio features with bounded complexity and parameters, and
   renders consistently in preview and export. Gate: golden-frame tests per
   engine at fixed timestamps.
7. **GPU backend** — research and benchmark actual Go-accessible Direct3D
   12 / WGPU options on supported Windows hardware; real adapter/workload
   proof (adapter name, vendor/device IDs, backend, driver version,
   executed workload, readback); CPU fallback tested on unsupported
   drivers. Separate `detected`, `available`, `selected`, `executed`,
   `verified` states; never fake GPU telemetry.
8. **Export pipeline** — deterministic offline frame rendering; 720p /
   1080p / 1440p / 4K targets; 24/30/60 FPS where viable; H.264 MP4 with
   compatible AAC; audio/video duration sync; cancellation/resume;
   progress; disk-space checks; stream validation (ffprobe-equivalent or
   native). Gate: a short real fixture exported end-to-end before any
   delivery claim.
9. **Presets/projects** — local project save/load, per-song styles,
   versioned settings schema with backwards migration tests.
10. **Quality/security** — performance benchmarks, memory bounds,
    GPU-device-loss recovery, corrupted-media handling, path safety,
    dependency/license audit, Windows packaging and signing decision,
    regression discipline.

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

Phase 1 delivers the foundation only. Phase 2 functions above are `PLANNED`
and were **not started** as of 2026-11-04. See `ai-notes.md` for the
evidence-backed current state and the next narrow mission.
