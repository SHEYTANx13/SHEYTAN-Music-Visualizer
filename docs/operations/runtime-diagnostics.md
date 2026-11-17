# Runtime diagnostics — queues, jobs and evidence

Canonical operations reference for run-44010666873 and later.
Historical CI-side diagnostics: `docs/CI-DIAGNOSTICS.md`; user-facing
troubleshooting: `docs/TROUBLESHOOTING.md`.

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
(explicitly not claimed): the in-app Fyne Job Center panel and
diagnostics page rendering these snapshots; watchdog/heartbeat for
UI/audio/scheduler stalls; goroutine-profile capture behind an
explicit opt-in.
