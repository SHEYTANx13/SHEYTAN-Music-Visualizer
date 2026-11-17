# Queue orchestration — the bounded work scheduler

Canonical reference for `internal/jobs` and its studio wiring
(`internal/studio/jobs.go`), introduced for run 44010666873.

## Scope

One app-owned scheduler owns every long-running non-real-time
workload. It replaces the previous pattern where each studio subsystem
spawned its own goroutine with no shared admission control, no
resource governor and no queue visibility.

## Queue classes and ownership

| Class | Owner | Notes |
| --- | --- | --- |
| A — REALTIME_AUDIO | `internal/playback` (Oto device thread) | NOT a worker-pool lane. Never scheduled here; the audio clock is owned by the transport. |
| B — UI_INTERACTION | Fyne event loop | NOT a worker-pool lane. UI handlers only do small state changes and non-blocking enqueues. |
| C — INTERACTIVE_IO | scheduler lane `interactive_io` | first-play preparation (device open + PCM cache open) |
| D — AUDIO_ANALYSIS | scheduler lane `audio_analysis` | media import (decode), DSP/Song-DNA analysis — CPU-heavy |
| E — SCENE_PREP | scheduler lane `scene_prep` | reserved for 3D scene preparation (not yet produced by the app) |
| F — GPU_UPLOAD_RENDER | scheduler lane `gpu_upload_render` | single submission owner; frames-in-flight is bounded by the render backend |
| G — EXPORT | scheduler lane `export` | offline export through `internal/export` |
| H — MAINTENANCE | scheduler lane `maintenance` | cache cleanup etc. |
| I — TRUSTED_PRIVILEGED | scheduler lane `trusted_privileged` | requires an `Authorize` check at enqueue; no such operation exists in the product today (see `docs/security/file-access-and-privileges.md`) |

## Hard capacity ceilings (conservative first implementation)

| Lane | Waiting | Active | Memory ceiling |
| --- | --- | --- | --- |
| interactive_io | 12 | 2 | 512 MB |
| audio_analysis | 4 | 2 | 2048 MB |
| scene_prep | 2 | 1 | 1024 MB |
| gpu_upload_render | 2 | 1 | 512 MB |
| export | 1 | 1 | 2048 MB |
| maintenance | 4 | 1 | 256 MB |
| trusted_privileged | 1 | 1 | 128 MB |

The waiting caps total **26** pending jobs across all lanes. Excess
work is rejected with a typed outcome and an actionable message, or
coalesced only for jobs carrying an explicit coalesce key. There is no
overflow list, spillover channel or per-job goroutine.

The shared **CPU-heavy budget** is `max(1, min(3, floor(GOMAXPROCS/3)))`
tokens; a 22-logical-processor machine still gets 3 tokens so playback,
UI and preview keep headroom. The budget adapts DOWN under measured
pressure (`SetPressure`, fed by the studio) and never above the ceiling.

## Typed admission outcomes

Every enqueue is O(1), non-blocking and returns one of:
`accepted`, `coalesced` (newest-wins coalesce key or idempotency
duplicate), `rejected_full`, `rejected_shutdown`,
`rejected_unauthorized`, `rejected_budget`. The UI receives a job ID
only after successful admission.

## Job lifecycle and records

`created -> validated -> queued -> scheduled -> running ->
succeeded | failed | cancelled | timed_out` (plus `superseded` for
coalesced-away waiting jobs). Terminal states never transition back to
running; a retry of a classified transient failure is a NEW attempt on
the same record (bounded by `RetryMax`, default 1 = no retry).
Authorization denial, invalid input, cancellation and budget denial are
never retried.

Each record carries: stable ID, idempotency/coalesce keys, type, lane,
project/source fingerprints, correlation ID, timestamps and durations,
priority, memory/disk estimates, attempt count, error category with a
safe user-facing summary, stage-based monotonic progress counters, and
a cancellation context. Progress regressions are dropped; the scheduler
never fabricates progress, queue position or ETA.

## Fairness, priority and aging

Weighted fair sharing among the CPU-heavy lanes (interactive I/O 4,
audio analysis 2, scene prep 1, export 1, maintenance 1) via deficit
round robin; unused shares are borrowable. Aging raises a waiting
job's effective priority by one level per full 30 seconds of queue
wait, monotonically, capped at `PriorityCeiling` (6) — background work
can never outrank the audio/UI classes, which live outside the
scheduler by design. Export is never starved (tested).

## Cancellation, containment and shutdown

Cancellation is cooperative and propagates through the job context;
waiting jobs leave the queue immediately; scheduled jobs are skipped by
the worker; running jobs observe their context. Panics are recovered at
the worker boundary, recorded as `panic` failures with the worker
returned to service — one job's failure never kills the scheduler, the
UI or audio. `Shutdown(deadline)` stops admissions, drains gracefully,
cancels stragglers at the deadline and returns within documented
bounds even if a non-cooperative job is stuck (observable via
`Stalled()`/`LiveJobs()`; the process exit path owns the final reap).

Pause/Resume is deliberately NOT offered: no current job type
implements and tests safe pause, and the Job Center never shows a
control that silently does nothing.

## Job journal (crash reconciliation)

When configured (`Config.JournalPath`; the studio uses
`<DataRoot>/pcm-cache/job-journal.jsonl`), job metadata transitions
are appended as JSON lines (schema version 1), flushed per record,
compacted atomically (tmp + rename) past 4096 lines down to the most
recent 1024. On open, entries whose last state is still queued/
scheduled/running are marked `interrupted` — a crash can never leave a
job implicitly successful, and orphaned running jobs surface as
interrupted, never as live. Only scalar metadata is persisted; live
pointers, handles, device objects and secret content are never
serialized.

## Job Center data model

`studio.JobsOverview()` returns lane metrics (waiting/active,
high-water marks, terminal counts, aggregate durations) plus live job
snapshots; `studio.CancelJob(id)` cancels. The in-app Fyne panel
rendering this is future work — the data model, bounds and cancellation
are implemented and tested.

## Tests

`internal/jobs/jobs_test.go` covers: capacity + typed rejection,
priority ordering, aging monotonicity and ceiling, cancellation at
every stage, deadline timeout, idempotency dedup, newest-wins
coalescing, budget rejections, concurrent-memory-ceiling enforcement,
panic containment, failure isolation, shutdown rejection/cancel/
graceful drain, bounded goroutine count, enqueue-storm accounting,
trusted-privileged authorization, bounded transient retries, pressure
adaptation, journal crash reconciliation, export non-starvation and the
mixed-workload stress scenario. All run clean under `-race`.
`internal/studio/studio_jobs_test.go` covers the studio-level mixed
workload, the overview data model and bounded close under load.
