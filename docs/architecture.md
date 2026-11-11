# Architecture — SHEYTAN Music Visualizer

A one-page map of the package responsibilities and the contracts that
hold the product together. Deep dives live beside the code they
describe; this page is the orientation layer.

## Package responsibility map

| Package | Owns |
| --- | --- |
| `cmd/sheytan-music-visualizer` | Process entry: flag parsing, `--version`, `--self-check` (foundation invariants, machine-readable result file), recoverable-panic GUI boundary. |
| `cmd/ci-log-collector` | Consolidated plain-text run report (`log.txt`); stdlib-only so it builds even when the app dependency graph is broken. |
| `internal/apppaths` | THE data-layout authority: portable vs installed mode resolution, directory bootstrap, legacy-log migration (copy, never move). |
| `internal/logging` | Runtime log: rotation, stderr fallback, verify-writable probe, non-destructive legacy migration helper. |
| `internal/brand` | Product identity constants, palette tokens, attribution block. |
| `internal/buildinfo` | Build metadata (`-X` injected version/commit/date/builder), honest unknown defaults. |
| `internal/cilog` | CI evidence domain: gate-record parsing, redaction, GitHub API client, report assembly. |
| `internal/dsp` | Streaming analysis core: Hann-windowed FFT-2048/hop-512, bands, centroid, flux/onsets, tempo with confidence, waveform envelope. Deterministic; chunked bins backing store (see `docs/performance-baseline.md`). |
| `internal/audio` | Decoder registry (WAV/MP3/FLAC/Ogg first-party + optional local-FFmpeg AAC family), canonical PCM pipeline (48 kHz stereo float32), resampler. |
| `internal/playback` | Oto v3 playback, transport state, buffering-aware playhead. |
| `internal/songdna` | Deterministic per-track visual identity from measured features. |
| `internal/visualizer` | Visualization modes/config/presets (pure logic, no I/O). |
| `internal/render` | Renderer selection + CPU renderer (deterministic software raster) + wgpu GPU renderer with verified readback; capability states. |
| `internal/export` | MP4 export engine: FFmpeg discovery, streaming frame pipe, progress, cancellation, ffprobe validation. |
| `internal/fflocate` | THE FFmpeg discovery authority (explicit override → app-local `tools/ffmpeg/bin` → PATH). |
| `internal/fullscreen` | Framework-agnostic fullscreen controller: desired vs observed state, persisted intent, event-driven reconciliation. |
| `internal/studio` | Studio service: library, projects, analysis orchestration, session state. |
| `internal/project` | Versioned project JSON, atomic save, validation, relink. |
| `internal/sysprofile` | Local system profile: CPU/RAM/OS/display/GPU/audio/FFmpeg facts, capability classification, bounded resource-policy modes. |
| `internal/perf` | Startup stage recorder anchored at true process start; runtime snapshot. |
| `internal/procattr` | Child-process policy: Windows `HideWindow` + `CREATE_NO_WINDOW` so FFmpeg/ffprobe never flash a console. |
| `ui/fyne` | The Fyne surface: single main window, studio shell, System Integration + Help/Diagnostics dialogs, theme, fullscreen adapter, policy/preference stores. |

## Application startup sequence

1. `main.run` parses flags; `--version`/`--self-check` exit without a
   window.
2. `runGUI` installs the recoverable-panic boundary (log + stderr stack).
3. `ui/fyne.Run` starts `internal/perf` (anchored at true process start).
4. `initRuntime` resolves the data layout, bootstraps directories,
   migrates legacy logs by copy and opens the runtime log BEFORE any
   subsystem or window construction; a broken log degrades to stderr,
   never aborts.
5. The first structured record `application starting` carries
   version/commit/builder/date and the resolved data/log directories.
6. The single `fyne.Window` is created (exactly one `NewWindow` in the
   package — locked by `ui/fyne/one_window_test.go`).
7. The studio service initializes; persisted renderer preference and
   resource-policy mode load with revalidation and logged reasons.
8. The UI shell builds; `ShowAndRun` starts the event loop.
9. On first event-loop iteration: `first-window-usable` mark, persisted
   fullscreen restore, the `startup stages complete` record and the
   one-per-launch runtime snapshot.
10. On close: fullscreen observed-state sync, `shutdown` record, teardown
    in dependency order (UI → service).

## System-profile flow

`internal/sysprofile` collects lightweight facts first (CPU model,
effective parallelism, RAM, OS, FFmpeg presence via `internal/fflocate`);
expensive enumeration (GPU adapters, display geometry) runs
asynchronously on the scan goroutine — opened on demand or via the
explicit Refresh action in the System Integration dialog, never polled.
All reads stay local; absent APIs yield explicit `unknown`/`unavailable`
values. The capability state model (DETECTED → SUPPORTED → SELECTED →
EXECUTING → VERIFIED) and its evidence rules are documented in
`docs/diagnostics.md`.

## CPU/GPU/audio/export data flow

Import → canonical cache (48 kHz stereo float32, SHA-256-content-keyed,
provenance sidecar) → DSP analysis (chunked, cache-backed) → Song DNA →
per-frame `visualizer.FrameInput` → renderer (CPU always available;
GPU only after classification and a real pipeline validation with
verified readback; software adapters never impersonate hardware) →
preview (`fyne.Do`-marshaled widget updates) or export (streamed frames
piped to local FFmpeg with hidden windows, real progress, cancellation
that reaches FFmpeg, ffprobe validation before atomic finalize).

## Fyne main-thread and single-window contract

- Exactly ONE primary `fyne.Window`; all other surfaces are dialogs or
  in-window navigation (no splash window, no hidden second instance).
- Every widget mutation from a non-UI goroutine is marshaled with
  `fyne.Do`/`fyne.DoAndWait` (Fyne 2.8 threading contract); the UI
  thread never blocks on adapter enumeration, FFmpeg probes, filesystem
  scans or project analysis.
- Audio/DSP/render run off the UI thread with bounded queues and context
  cancellation; backpressure protects playback latency.
- Keyboard transport (Space/←/→) and fullscreen (F11/Esc) are window
  event handlers reconciled by `internal/fullscreen` (desired vs
  observed, persisted intent, no force loops).
