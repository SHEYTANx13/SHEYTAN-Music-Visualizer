# Diagnostics and capability documentation

How the application reports what it is doing, where to find the evidence,
and how to read honest capability states.

## Runtime log

- **Location**: resolved by `internal/apppaths` at startup —
  `<data-root>/logs/app.log` where the data root is the portable
  `data/` directory (when `portable.mode` sits beside the executable) or
  the per-user app-data tree (Windows `%LOCALAPPDATA%\SHEYTAN Music
  Visualizer`, Linux `$XDG_DATA_HOME/SHEYTAN Music Visualizer`).
- **Rotation**: `app.log` rotates at 5 MiB with one retained generation
  (`app.log.1`). The active failure log is never truncated at startup;
  the startup record is appended.
- **Fallback**: an unwritable log file never aborts the launch — a
  stderr-backed logger takes over and the exact error is surfaced in the
  Help / Diagnostics dialog.
- **Content**: structured one-line records (UTC timestamp, severity,
  component, event, safe contextual values). The startup path emits, in
  order: `application starting` (version, commit, builder, build date,
  data mode, data root, log dir), `runtime log opened`, resource-policy
  resolution, renderer preference application, `window shown` (renderer
  summary + fullscreen intent), `startup stages complete` (measured
  stage timings anchored at true process start) and the one-per-launch
  `runtime snapshot` (GOMAXPROCS, NumCPU, heap, GC cycles). Shutdown
  logs `shutdown` with the observed fullscreen state. Subsystem
  failures log full error chains. Repetitive states are bounded/rate-
  limited; there is exactly one record per startup — never per frame,
  never per audio sample.
- **Privacy**: no audio content, no credentials, no full sensitive paths,
  no environment dumps.

## In-app diagnostics

Help / Diagnostics (inside the single main window) shows the resolved
data mode, data root and exact log path, with **Open Logs Folder**,
**Copy Log Path** and a copy-diagnostics action that never copies secrets
or personal media metadata. The System Integration dialog reports the
machine profile with provenance states only — fabricated real-time
utilization percentages are never shown ("not reported" is the honest
value when no stable source exists).

## Startup metrics

`internal/perf` anchors a recorder at true OS process start (procfs on
Linux, `GetProcessTimes` on Windows) and marks: `runtime-entry`,
`logger-and-data-paths`, `app-shell-created`,
`studio-service-and-audio-init`, `ui-shell-built`,
`first-window-usable` (first event-loop iteration — the honest "window
visible/usable" stage, distinct from "all optional checks done") and
`startup-recorded`. The complete stage list is one structured record
(`startup stages complete`, field `first-window-usable=<N>ms`).

Reproduce on a display-capable host with:

    scripts/bench/startup-bench.sh <samples> [bin path]

Cold runs use a fresh data root; warm runs reuse one. Numbers are
host-specific samples — compare only same-host same-commit runs.

## Benchmarks and profiling

    go test -tags goffi_static -bench . -benchmem ./internal/dsp/ ./internal/render/ ./internal/audio/
    go test -tags goffi_static -bench BenchmarkCPURender1080pBars -cpuprofile cpu.prof ./internal/render/
    go tool pprof -top cpu.prof

Measured baselines and the before/after records of every accepted
optimization live in `docs/performance-baseline.md`. Rules of the road:
baseline first, profile second, optimize real hotspots third; never
speculative rewrites; outputs must stay bit-identical unless the change
is explicitly reviewed as a visual change.

## Reading capability states

Each acceleration path (GPU render, audio device, FFmpeg export) moves
through separate, honest states — a device being listed by the OS is
only DETECTED, never proof of acceleration:

| State | Meaning | Evidence required |
| --- | --- | --- |
| DETECTED | The OS/inventory lists it | Enumeration only |
| SUPPORTED | The API/backend class is usable by this build | Backend probe without claiming execution |
| SELECTED | The app chose it for the pipeline | Renderer/adapter decision record |
| EXECUTING | The real pipeline is running on it | Live render/export path usage |
| VERIFIED | Output validated (readback/ffprobe) | Measured artifact check |

Interpretation rules baked into the UI and the logs:

- `Microsoft Basic Render Driver`, software rasterizers and virtual
  adapters are always reported as software/virtual — never as physical
  GPU acceleration.
- An ALSA null sink (used by headless CI) proves no audio playback.
- `FFmpeg absent` is reported explicitly as NOT AVAILABLE; MP4 export
  stays NOT PROVEN until a real export has run and ffprobe-validated on
  that platform. A Linux runner proof never proves Windows export.
- Self-check renderer evidence distinguishes the selected backend, the
  device class and the exact fallback reason on one line
  (`backend=… gpu=… api=… adapter=… devtype=… class=…`).

## Consolidated CI report (`log.txt`)

The collector (`cmd/ci-log-collector` + `internal/cilog`) merges every
per-job log-part artifact and the authoritative GitHub API conclusions
into ONE plain-text `log.txt` (uploaded unzipped; redaction applied).
It contains run metadata, the job status table, the parsed gate table
(PASS/FAIL/BLOCKED/NOT RUN with the rule that only a gate's own
`EXIT_CODE` record can mark it passed), the failure index and verbatim
redacted logs. Every gate also has a machine-readable
`ci-logs/gate-results/<gate>.result.json` record (see `docs/ci.md`).
