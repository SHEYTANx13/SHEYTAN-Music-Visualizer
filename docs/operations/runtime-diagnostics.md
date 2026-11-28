# Runtime diagnostics — queues, jobs, startup and playback evidence

Canonical operations reference for run-44010666873 and later.
Historical CI-side diagnostics: `docs/CI-DIAGNOSTICS.md`; user-facing
troubleshooting: `docs/TROUBLESHOOTING.md`; full player event contract:
`docs/AUDIO-PLAYBACK.md`; adapter classes: `docs/GPU-RENDERING.md`.

## Startup and playback event evidence

(run-46121470729 docs merge: this section is the canonical home of the
former standalone `docs/RUNTIME-DIAGNOSTICS.md`, which duplicated this
document's category and was not listed in `docs/INDEX.md`. Its
still-valid content is preserved here verbatim-in-meaning; the retired
file is gone from the tree and remains in git history.)

### Startup baseline (app(5).log, Windows x64 portable, commit 9f7e16d)

Single observed launch of the current evidence build (2026-11-16,
Intel Arc adapter detected, auto renderer preference):

| Stage | Time |
|---|---|
| runtime entry | 54 ms |
| logger/data paths | 56 ms |
| shell created | 74 ms |
| studio service/audio init | 82 ms |
| UI shell built | 143 ms |
| first-window-usable | **329 ms** |

These numbers are the comparison anchor for the SAME hardware/build
conditions only. This is ONE observation: no cold/warm distribution
(10× each with P50/P95) has been measured — that measurement requires
the owner's machine and is tracked as NOT RUN in the traceability
ledger (R29). Heap snapshot in the same log: 57,663,064 bytes
(~55.0 MiB) at the recorded instant, 22 logical workers, six GC
cycles — a single observation, not a memory budget; steady-state and
peak under project load / 3D / playback / export remain to be measured
on real hardware.

The log records adapter DETECTION (`Intel(R) Arc(TM) Graphics …
available`), which is not evidence of render execution: the codebase
enforces the detected / available / selected / executed / verified
separation in `internal/render` (adapter-class tests; a software
renderer is never reported as a proven hardware adapter), and the
self-check prints the same honesty line on every run.

### Play session correlation

Every playback generation carries a session ID (`ClockInfo.Session`),
incremented per device-player spawn. Play lifecycle events and clock
snapshots join renderer/startup events in the same runtime log by
session and timestamp.

Structured play events — `play.requested` / `play.pause.requested`
(UI handler) and `play.preroll.started`, `play.preroll.completed`,
`play.state.changed`, `play.position.advanced`, `play.eos`,
`play.stop.requested`, `play.resources.released`, `play.error`
(transport) — are emitted through the studio sink into the runtime
log with session/state/position/total fields. No media bytes, titles,
paths or library contents are logged. The events NOT yet emitted
(`play.asset.resolved`, `play.decoder.selected`,
`play.output.selected`, `play.pipeline.created`, `play.buffering`,
`play.underrun`) are documented as absent rather than faked — they
belong to the import/decode stage and to device behaviors the current
Oto backend does not expose. Why this matters: a supplied runtime log
with no play-related lines now localizes the next real-hardware
failure stage (request accepted? player spawned? samples advancing?
EOS?) instead of showing only a healthy startup.

### Diagnostics export

`--self-check` (re-verified on Linux, exit 0) reports build identity,
data layout, decoder registry, DSP fixture, visualizer config,
renderer selection (with explicit NOT-proven-physical-GPU labeling)
and export environment (local FFmpeg identity + encoder list, with
the honest note that actual MP4 export is proven only by the executed
export tests). A full no-user-media diagnostics bundle export remains
staged (see roadmap).

## Queue and job evidence

`studio.JobsOverview()` (backed by `internal/jobs`) is the Job Center
data model:

- per-lane: waiting/active counts, high-water marks, accepted/
  coalesced/rejected-full/other outcomes, terminal counts
  (succeeded/failed/cancelled/timed-out), aggregate queue-wait and
  run durations;
- global: CPU-heavy token budget, tokens in use, tokens set aside by
  pressure adaptation, temporary-disk reservation vs budget,
  goroutine count;
- live job snapshots: type, lane, state, attempt, stage, monotonic
  progress counters, timestamps, error category + safe summary,
  correlation ID, cancellability.

All fields are copied snapshots — reading them never blocks the UI
thread. `studio.CancelJob(id)` cancels any live job;
`studio.SetSchedulerPressure(level)` adapts the CPU-heavy budget down
under audio underruns / UI frame-time pressure and back up on
recovery.

## Job journal

Job metadata transitions are appended to
`<DataRoot>/pcm-cache/job-journal.jsonl` (JSON lines, schema 1,
flushed per record, atomically compacted past 4096 records). After a
crash, reopening the journal marks every entry that was still queued/
scheduled/running as `interrupted` — an orphaned job is never
implicitly successful and never resurrects. The journal contains only
scalar metadata: no pointers, handles, file contents or full sensitive
paths.

## Truthfulness rules

- `queue depth`, `rejected`, `coalesced` and high-water counters are
  measured, not estimated.
- Progress is stage-based and monotonic; regressions are dropped;
  indeterminate stages report no percentage. No fabricated queue
  position or ETA.
- A scheduler that returned from Shutdown with a non-cooperative job
  still stuck reports it via `Stalled()` — a drain is never claimed
  where none happened.
- GPU evidence states (detected/available/selected/executed/verified)
  remain owned by `internal/render` (see `docs/GPU-RENDERING.md`).

## Recovery

On restart the journal is reconciled (interrupted marking). Project
transactions and content-library safety are owned by
`internal/project` / `internal/content`; the scheduler never
auto-executes recovered work — resume happens only through explicit
user action creating a new bounded job.

## Status

Implemented and tested: the data model, metrics, cancellation,
pressure adaptation, journal + crash reconciliation. Future work
(explicitly not claimed): the in-app web-shell Job Center surface
rendering these snapshots; watchdog/heartbeat for UI/audio/scheduler
stalls; goroutine-profile capture behind an explicit opt-in.

## Diagnostics bundle (log catcher, run 46168717789)

The product shell opens the runtime log itself (before this repair
only the CLI did, so the windowed app produced no `app.log` at all).
Diagnostics reports the LIVE writer state: an attached file log proves
its path; a stderr fallback (data root unwritable) is an explicit
check failure with the data-layout reason — never a silent fake path.

`diagnostics.export` writes a single ZIP through the native save
dialog: `app.log` (and its rotated generation when present), the
bounded `crashes/*.txt` reports, `build-info.json` and
`data-layout.txt`. Every entry is already-sanitized app evidence (the
ASCII log contract owns the bytes); the bundle contains no media
content and the chosen path never crosses into the web layer.
