# Architecture — SHEYTAN Music Visualizer

A one-page map of the package responsibilities and the contracts that
hold the product together. Deep dives live beside the code they
describe; this page is the orientation layer.

## Package responsibility map

| Package | Owns |
| --- | --- |
| `cmd/sheytan-music-visualizer` | CLI authority: flag parsing, `--version`, `--self-check` (foundation invariants, machine-readable result file), `--export-cli`, world-OBJ export. It never opens a window. |
| `cmd/app-wails` | THE product desktop entry: bootstrap-first logging, then the Wails v3 single-window web shell (`internal/transport/wails.BuildApp`, `//go:build purego && sheytan_nogpu`). `--version` / `--build-info-json` print identity and exit; the desktop product executable ships from this package. |
| `cmd/app-android` | The gomobile/x-mobile carrier (`gui_mobile.go` glue, headless variant for CI self-check evidence). No go-gui, no Fyne. |
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
| `internal/transport/wails` | The Go↔web boundary: `service.go` is the toolkit-free `/api` facade (narrow, validated application services), `app.go` owns the ONE Wails window lifecycle, `host.go` the native-dialog host (chosen paths are consumed inside Go and never cross into the web layer), `service_dna.go` / `service_import_export.go` the Song-DNA and export/import projections. |
| `frontend/wails/dist` | The shared web frontend, EMBEDDED into the binary by `embed.go` (no local server, no external runtime dependency): one HTML document, vanilla ES modules (`js/`, one file per element) and per-element stylesheets (`css/`) with the shared SHEYTAN tokens; responsive via content-driven breakpoints (`responsive.css`, `adaptive.css`). The web shell presents control/status/diagnostics surfaces; it does not render the visualizer canvas (real-time preview lives with the native render stack). |

## Application startup sequence (desktop web shell, `cmd/app-wails`)

1. `main.run` parses flags; `--version` / `--build-info-json` exit
   without a window; unexpected arguments are rejected.
2. `runShell` installs the recoverable-panic boundary and runs
   FIRST-BYTE logging (`logging.Bootstrap` + startup banner + stderr
   tee) before any subsystem.
3. The data layout resolves and bootstraps the contract directories;
   the runtime log opens before window construction; a broken log
   degrades to stderr, never aborts.
4. `studio.New()` constructs the studio service; `defer st.Close()`
   guarantees the idempotent, dependency-safe teardown on EVERY exit
   path after successful construction (app.Run errors and panics never
   leak owned workers, transports or scheduler goroutines).
5. `internal/transport/wails.BuildApp` creates the ONE Wails
   application: the validated service facade registered as a Wails
   service, the embedded-asset handler (frontend plus `/api/*`), the
   brand icon (`internal/appicon`) and the single main window (obsidian
   background, so no white flash before the first webview paint). It
   does not call `app.Run`; the caller owns the blocking run.
6. The quit handler wires the web layer's `app.quit` command to the
   application's own `Quit` — the web layer holds an explicit,
   window-owned action, never process-level privileges.
7. Audio/DSP/render/export work stays off the UI thread in the bounded
   `internal/jobs` scheduler lanes (see
   `docs/architecture/queue-orchestration.md`); the web shell polls the
   read-only `/api` projections and never streams audio samples through
   the bridge.
8. On exit: `studio.Close` (analysis cancel + join, scheduler
   `Shutdown`, transport close) runs through the `appclose`
   state machine's stage discipline; the exit record (`LogExit`) and
   any recovered panic persist as a crash report.

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
preview or export (streamed frames piped to local FFmpeg with hidden
windows, real progress, cancellation that reaches FFmpeg, ffprobe
validation before atomic finalize). The web shell surfaces the
control/status state of these stages through the `/api` facade; the
real-time preview canvas is owned by the native render stack.

## Web-shell boundary and single-window contract

- Exactly ONE Wails top-level window (`main`); the workspace
  destinations are in-page tabs rendered from the canonical
  `DestinationCatalog` (`/api/destinations`), including honest empty
  states for unavailable destinations. Eleven of the twelve
  destinations are live surfaces; the Worlds tab (run 46168717789)
  serves the real per-track multi-scene world plan (`/api/worlds`,
  internal/mvplan) with honest no-track/pending/ready states. The
  retired-toolkit guard
  (`scripts/ci/check-no-fyne.sh`) keeps Fyne and go-gui out of the
  tree.
- `internal/transport/wails/service.go` is deliberately toolkit-free
  and headless-testable: the facade validates every frontend-supplied
  argument in Go before any dialog or job starts, and only the window
  lifecycle (`app.go`, `purego && sheytan_nogpu`) touches the Wails
  application package.
- **Close/shutdown:** every close surface — the web `app.quit` command
  and the window titlebar — ends in ONE teardown path: the Wails
  `Quit`, then `studio.Close` (idempotent, dependency-safe, bounded;
  see `internal/appclose` stage discipline and the queue
  `Shutdown` contract in `docs/architecture/queue-orchestration.md`).
  Duplicate quit requests never duplicate teardown; active
  playback/export jobs are cancelled and joined with recorded
  outcomes.
- **Responsive shell:** one shared frontend recomposes per breakpoint
  (`css/responsive.css`, `css/adaptive.css`) — compact <600 px (single
  column, stacked panels, touch-sized controls), intermediate 600–959
  px, standard 960–1679 px workspace, ultrawide ≥1680 px. Breakpoints
  are content-driven CSS media/container queries driven by the token
  document (`design/tokens.json`); no user-agent or device-model
  detection.
- Audio/DSP/render run off the UI thread with bounded queues and
  context cancellation; backpressure protects playback latency.

## Historical shells

The Fyne surface (`ui/fyne`) was deleted after parity (run
44330231458 update) and the Go-Gui surface (`ui/gogui`,
`cmd/app-gogui`) was retired when the Wails web shell became the
product desktop shell (run-46033044814 repair, deployed to `main` in
commit 4d1cf6d). Both remain reachable in git history; the guard
`scripts/ci/check-no-fyne.sh` fails the build if either toolkit
re-enters the module graph.
