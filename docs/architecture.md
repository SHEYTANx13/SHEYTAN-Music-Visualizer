# Architecture — SHEYTAN Music Visualizer

A one-page map of the package responsibilities and the contracts that
hold the product together. Deep dives live beside the code they
describe; this page is the orientation layer.

## Package responsibility map

| Package | Owns |
| --- | --- |
| `cmd/sheytan-music-visualizer` | CLI authority: flag parsing, `--version`, `--self-check` (foundation invariants, machine-readable result file), `--export-cli`, world-OBJ export. It never opens a window. |
| `cmd/app-gogui` | THE product desktop entry: bootstrap-first logging (banner/tee/crash report/exit record) then `ui/gogui.Run` (`gui_desktop.go`, `//go:build !android`; the android gomobile glue is `gui_mobile.go`). The desktop product executable ships from this package. |
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
| `internal/fflocate` | THE FFmpeg discovery authority (explicit override → app-managed `<DataRoot>/media-tools/current/bin` → app-local `tools/ffmpeg/bin` → PATH). |
| `internal/fullscreen` | Framework-agnostic fullscreen controller: desired vs observed state, persisted intent, event-driven reconciliation. |
| `internal/studio` | Studio service: library, projects, analysis orchestration, session state. |
| `internal/project` | Versioned project JSON, atomic save, validation, relink. |
| `internal/sysprofile` | Local system profile: CPU/RAM/OS/display/GPU/audio/FFmpeg facts, capability classification, bounded resource-policy modes. |
| `internal/perf` | Startup stage recorder anchored at true process start; runtime snapshot. |
| `internal/procattr` | Child-process policy: Windows `HideWindow` + `CREATE_NO_WINDOW` so FFmpeg/ffprobe never flash a console. |
| `ui/gogui` | The native Go-Gui surface (v0.99.1): the ONE window, command bar, workspace tab strip and eleven destination panels, studio rail/main split, live PixelSurface preview with the pacer bridge, media-tools manager panel, theme (SHEYTAN token palette), responsive classes from the token document, drag-to-scroll + class-resolved scroll bars. The former Fyne surface (`ui/fyne`) was deleted after parity (run 44330231458 update; guard: `scripts/ci/check-no-fyne.sh`). |

## Application startup sequence

1. `main.run` parses flags; `--version`/`--self-check` exit without a
   window.
2. `runGUI` installs the recoverable-panic boundary (log + stderr stack).
3. `cmd/app-gogui` runs FIRST-BYTE logging (`logging.Bootstrap` +
   startup banner + stderr tee) before any subsystem; `ui/gogui.Run`
   logs every phase (design tokens, data layout, studio service,
   renderer selection, window created) with its reason.
4. The data layout resolves and bootstraps the contract directories;
   the runtime log opens before window construction; a broken log
   degrades to stderr, never aborts.
5. The first structured record (startup banner) carries
   version/commit/builder/date/go/os/arch/pid and the resolved data/log
   directories.
6. The single `gui.Window` is created (locked by
   `ui/gogui/one_window_test.go`); `gui.SetTheme(ThemeDark)` and the
   SHEYTAN token theme install BEFORE the window so the first frame
   paints in the dark polarity.
7. The studio service initializes; persisted renderer preference and
   resource-policy mode load with revalidation and logged reasons.
8. The view generator builds every frame from frame-thread-owned
   control values; `backend.Run` starts the frame loop.
9. The preview pacer goroutine publishes measured frames through the
   uiState bridge; nothing on the frame thread ever blocks on audio,
   FFmpeg, filesystem or analysis work.
10. On close (or SIGINT/SIGTERM): teardown in dependency order —
    studio service, renderer, pixel surface — with the exit record
    (`LogExit`) and any recovered panic persisted as a crash report.

## System-profile flow

`internal/sysprofile` collects lightweight facts first (CPU model,
effective parallelism, RAM, OS, FFmpeg presence via `internal/fflocate`);
expensive enumeration (GPU adapters, display geometry) runs
asynchronously on the scan goroutine — opened on demand or via the
explicit Refresh action in the System Integration dialog, never polled.
All reads stay local; absent APIs yield explicit `unknown`/`unavailable`
values. The capability state model (DETECTED → SUPPORTED → SELECTED →
EXECUTING → VERIFIED) and its evidence rules are documented in
`docs/TROUBLESHOOTING.md`.

## CPU/GPU/audio/export data flow

Import → canonical cache (48 kHz stereo float32, SHA-256-content-keyed,
provenance sidecar) → DSP analysis (chunked, cache-backed) → Song DNA →
per-frame `visualizer.FrameInput` → renderer (CPU always available;
GPU only after classification and a real pipeline validation with
verified readback; software adapters never impersonate hardware) →
preview (frames published through the pacer bridge into the window's
PixelSurface; control values ride the uiState bridge) or export
(streamed frames piped to local FFmpeg with hidden windows, real
progress, cancellation that reaches FFmpeg, ffprobe validation before
atomic finalize).

## Go-Gui frame-thread and single-window contract

- Exactly ONE primary `gui.Window`; all other surfaces are in-window
  destinations (no splash window, no hidden second instance; locked by
  `ui/gogui/one_window_test.go`).
- Every shell field is owned by the go-gui frame thread (the view
  generator and widget callbacks). go-gui widgets are rebuilt every
  frame from those control values; callbacks write them and dispatch.
  The only cross-goroutine surfaces are `uiState` and the preview
  pacer's publish path; the frame thread never blocks on adapter
  enumeration, FFmpeg probes, filesystem scans or project analysis.
- **Close/shutdown (run 43090342995):** every close surface — titlebar
  (via `Window.SetCloseIntercept`), File→Quit — enters ONE state machine
  (`internal/appclose`): running → closeRequested → confirming →
  cancelling → flushing → closing → closed. Duplicate requests never
  duplicate dialogs or teardown; active playback/export/import get ONE
  confirmation; cancel→flush→release stages are bounded and their
  outcomes recorded; `Window.Close()` is called explicitly and exactly
  once after cleanup.
- **Responsive shell (run 43090342995):** one set of shared widget
  regions is recomposed per breakpoint by `ui/fyne/responsive.go` —
  phone <480 px (single column, touch-sized action grid, focal preview),
  compact <1024 px (draggable preview/side-panel split), desktop <1680
  px (three-region workspace), ultrawide ≥1680 px. The classes come
  from the token document (`design/tokens.json`) through
  `ui/gogui/layout.go` (`Breakpoint`, `For`); spacing, touch-target and
  scroll-bar sizing resolve per class (`ShellTheme.ScrollbarCfgForWidth`,
  `SpacingProfileForWidth`). Every scroll surface carries drag-to-scroll
  (a finger or held pointer pans; taps still reach children).
- **First-frame deferral (run 43090342995):** the renderer probe runs in
  the render-loop goroutine after the window is visible ("probing…"
  state until then); the audio device opens lazily on first playback.
  While nothing plays the preview repaints at ~6 fps instead of 30.
- Audio/DSP/render run off the UI thread with bounded queues and context
  cancellation; backpressure protects playback latency.
- Keyboard transport (Space/←/→) and fullscreen (F11/Esc) are window
  event handlers reconciled by `internal/fullscreen` (desired vs
  observed, persisted intent, no force loops).
