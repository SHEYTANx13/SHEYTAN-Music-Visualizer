# Runtime diagnostics — startup, playback and renderer evidence

Updated 2026-11-16 (v3 §F). Companion docs: `docs/AUDIO-PLAYBACK.md`
(the full player event contract), `docs/CI-DIAGNOSTICS.md` (CI gates),
`docs/GPU-RENDERING.md` (adapter classes).

## Startup baseline (app(5).log, Windows x64 portable, commit 9f7e16d)

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

## What the v3 session added

- **Play session correlation.** Every playback generation now carries
  a session ID (`ClockInfo.Session`), incremented per device-player
  spawn. Play lifecycle events and clock snapshots can be joined with
  renderer/startup events in the same runtime log by session and
  timestamp.
- **Structured play events.** `play.requested` / `play.pause.requested`
  (UI handler) and `play.preroll.started`, `play.preroll.completed`,
  `play.state.changed`, `play.position.advanced`, `play.eos`,
  `play.stop.requested`, `play.resources.released`, `play.error`
  (transport) are emitted through the studio sink into the runtime
  log with session/state/position/total fields. No media bytes,
  titles, paths or library contents are logged. The events NOT yet
  emitted (`play.asset.resolved`, `play.decoder.selected`,
  `play.output.selected`, `play.pipeline.created`, `play.buffering`,
  `play.underrun`) are documented as absent rather than faked — they
  belong to the import/decode stage and to device behaviors the
  current Oto backend does not expose.
- **Why this matters for the open playback question.** The supplied
  app(5).log contains no play-related lines at all, so the next
  occurrence on real hardware will now localize the failure stage
  (request accepted? player spawned? samples advancing? EOS?) instead
  of showing only a healthy startup.

## Diagnostics export

`--self-check` (already present, re-verified this session on Linux:
exit 0) reports build identity, data layout, decoder registry, DSP
fixture, visualizer config, renderer selection (with explicit
NOT-proven-physical-GPU labeling) and export environment (local
FFmpeg identity + encoder list, with the honest note that actual MP4
export is proven only by the executed export tests). A full
no-user-media diagnostics bundle export remains staged (see roadmap).
