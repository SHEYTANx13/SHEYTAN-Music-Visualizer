# roadmap.md — SHEYTAN Music Visualizer product direction and phase status

Status vocabulary (enforced): `PLANNED`, `IN PROGRESS`, `DELIVERED`,
`BLOCKED`, `DEFERRED`. A feature is `DELIVERED` only when code, tests and/or
CI prove it. Future features are labeled as planned, never described as
existing. Dates are ISO-8601.

---

## Product outcome (complete product, future state)

A premium local desktop music-visualizer studio that analyzes each song and
generates a unique visual identity from its frequency, rhythm, dynamics and
musical structure. Users customize the scene and export a synchronized MP4
music video up to 3840×2160 (4K). Everything runs locally — no cloud
rendering, no remote project state, no mandatory accounts, no telemetry.

## Foundation architecture (binding for all phases)

- **Go** owns application lifecycle, project model, audio-analysis
  coordination, deterministic frame timeline, render scheduling,
  configuration, diagnostics, and export orchestration.
- **Fyne** owns the native desktop UI only — never the export-frame
  renderer or the media pipeline. The studio preview must not be the source
  of truth for exported frames; UI, preview and offline export share the
  same deterministic visual/render model.
- **Local storage** owns project metadata, presets, thumbnails and user
  settings; source media is never copied unnecessarily.
- Audio, graphics and video backend integrations sit behind **narrow Go
  interfaces**, with clear CPU fallbacks where meaningful.
- No cloud dependency and no remote render path anywhere in the product.

---

## Phase 1 — foundation (this delivery, completed 2026-11-04)

| Item | Status | Evidence |
|---|---|---|
| Five root documents (`license.md`, `readme.md`, `agent.md`, `roadmap.md`, `ai-notes.md`) | DELIVERED | files in repo root; consistency check in `evidence/phase1-build.log` |
| Go/Fyne application scaffold; brand + buildinfo authorities | DELIVERED | `go build ./...`, `go test ./...` captured in evidence log |
| Minimal Fyne window (exact product title, Phase 1 placeholder) | DELIVERED | compiled + event-loop runtime verified under Xvfb virtual display (headless host); physical-display runtime not claimed |
| `--version` / `--self-check` CLI | DELIVERED | captured runs, exit 0 |
| Structured local logging (slog, app-owned dir, bounded rotation) | DELIVERED | `internal/logging` + tests |
| Build scripts with first-byte log capture and true exit codes | DELIVERED | `scripts/build/*`, capture helpers |
| `build-desktop.yml`: preflight + Windows UCRT64 + Linux + consolidated logs | DELIVERED (definition) | actionlint v1.7.12 PASS, YAML parse PASS, static contract tests PASS; hosted run: NOT RUN |
| `codeql.yml`: Go (manual build mode) + Actions | DELIVERED (definition) | actionlint PASS, contract tests PASS; hosted scan: NOT RUN |
| Consolidated log collector + fixtures (missing vs skipped distinction) | DELIVERED | `internal/cilogreport` tests, CLI E2E script |
| Phase 1 source archive + external handoff note | DELIVERED | ZIP validation recorded in evidence log |

Phase 2 functionality has **not** started; see the boundary statement in
`agent.md` §2.

---

## Phase 2 — studio (next mission, PLANNED, not implemented)

Organized as milestones in execution order. Each milestone lists its
acceptance gate; a milestone is not delivered until its gate is met on real
evidence.

1. **Studio foundation UI/UX** — library, import panel, central preview
   surface, transport, inspector, export settings; adaptive desktop layout;
   hellish black/red theme from `internal/brand` tokens; accessible
   contrast; resizable window.
   *Gate:* window operates correctly on a real Windows desktop (resize,
   focus, DPI), theme contrast verified against WCAG AA for text roles.
2. **Media foundation** — local file import, metadata, duration/sample
   rate/channel info, decode strategy, cancellation, large-file handling
   (no whole-track memory load).
   *Gate:* 3-hour track imports with bounded memory; cancel mid-import is
   clean; corrupted file produces a typed error, not a crash.
3. **Audio playback and synchronization** — reliable seek/pause/resume,
   monotonic playback clock, buffering/latency correction, exact frame-time
   mapping, sample-rate and device changes.
   *Gate:* seek accuracy and clock drift tests pass on real hardware;
   frame-time mapping is deterministic under load.
4. **DSP / song analysis** — FFT/STFT with documented windowing/hop size;
   RMS/envelope; frequency band energies; spectral centroid/rolloff/flux;
   onset detection; beat/tempo estimation with confidence and
   half/double-time ambiguity handling; manual tempo/offset correction.
   Analysis must be deterministic, tested against known generated fixtures,
   and estimates must never be presented as certainty.
   *Gate:* golden-fixture tests (synthetic signals with known
   frequencies/tempos) pass within documented tolerances; identical input +
   settings reproduce identical analysis output.
5. **Song-specific identity (`Song DNA`)** — deterministic DNA derived from
   analysis + explicit user seed/customization; same source and settings
   reproduce the same baseline visual; users can save/override styles.
   *Gate:* byte-identical visual timeline for identical input, seed and
   settings; stored projects reload across sessions.
6. **Visualizer engines** — waveform/oscilloscope, spectrum bars, radial
   spectrum, particle field, and future layered compositions. Each engine
   responds to appropriate audio features, has bounded complexity and
   explicit parameters, and renders consistently in preview and export.
   *Gate:* preview frames and offline-rendered frames for the same
   timeline match within documented tolerance.
7. **GPU backend** — research and benchmark actual Go-accessible
   Direct3D 12 / WGPU options on supported Windows hardware; real adapter +
   workload proof; CPU fallback; controlled resources; no fake GPU claims
   ever.
   *Gate:* adapter name, vendor/device IDs, backend and driver version
   reported from an actual submitted workload with verifiable readback;
   benchmark vs CPU fallback on the same deterministic workload; tested
   fallback on unsupported drivers. `detected ≠ executed ≠ verified`.
8. **Export pipeline** — deterministic offline frame rendering; 720p /
   1080p / 1440p / 4K targets; 24/30/60 FPS where viable; H.264 MP4 with
   compatible AAC audio; audio/video duration sync; cancellation/resume;
   progress; disk-space checks; stream validation (`ffprobe`-equivalent or
   native).
   *Gate:* end-to-end export of a short real fixture at 1080p60 and 4K30
   validated by an independent probe tool; sync error within tolerance;
   cancel/resume verified.
9. **Presets / projects** — local project save/load, per-song styles,
   versioned settings schema, backwards migration tests.
   *Gate:* schema migration tests; project files from earlier Phase 2
   versions load correctly.
10. **Quality / security hardening** — performance benchmarks, memory
    bounds, GPU-device-loss recovery, corrupted media handling, path
    safety, dependency/license audit, Windows packaging, signing decision,
    regression suite.
    *Gate:* documented benchmark results; fuzz/robustness tests for
    decoders; `govulncheck` clean or triaged; license audit current.

## Research questions (open gates — do not lock in Phase 1)

No library is adopted below until its gate is measured. These are recorded
decisions to research, not mandates:

- **DSP/FFT:** which Go FFT implementation offers sufficient numerical
  correctness, maintenance, performance, license compatibility and Windows
  behavior? Gonum is a conservative candidate to evaluate — not an
  unmeasured mandate.
- **Decoding:** which audio decoders cover the required formats? Prefer
  Go-native libraries/OS codec interfaces; document coverage and licensing.
  An external FFmpeg installation must not become a silent mandatory
  runtime requirement — if it is the only viable path, present the exact
  licensing/runtime trade-off to the user first.
- **Playback:** which Go playback library provides device stability,
  seeking, buffer/clock observability and Windows support? Evaluate
  candidates against measurable requirements, not demos.
- **GPU API:** which Go-accessible API works reliably with Windows
  Direct3D 12 and can expose real execution/readback? Treat a young or
  pre-1.0 WGPU wrapper as experimental until tested on target drivers.
- **Encoding:** what is the defensible local MP4/H.264/AAC path on Windows?
  Evaluate Go bindings to Windows Media Foundation and codec
  availability/redistribution terms.
- **GPU encoding:** which GPU video-encoding APIs are truly available on
  target devices and drivers? GPU rendering and hardware video encoding are
  separate capabilities — never conflate them.
