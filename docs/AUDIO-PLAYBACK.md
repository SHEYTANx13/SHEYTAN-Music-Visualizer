# Audio Playback — state machine, confirmation contract and diagnostics

Updated 2026-11-16 (v3 work order §C). Implementation:
`internal/playback/transport.go` (state machine + clock),
`internal/playback/output.go` (device outputs), `internal/studio/studio.go`
(service), `ui/fyne/studio.go` + `ui/fyne/actions.go` (UI + logging).

## Backend

The production audio backend is **Oto v3** (`github.com/ebitengine/oto/v3`)
driving the canonical PCM cache (48 kHz stereo float32). It is NOT
GStreamer; the work order's GStreamer-1.28.9 branch does not apply and
no engine migration was performed (non-negotiable 27). When no audio
device is available, `NewOtoOutput` fails honestly (oto context error
surfaced) and the studio falls back to `SilentOutput`, a real-time-paced
silent pacer used for visualizer preview timing; the UI is told the
output is NOT device-real (`ClockInfo.DeviceReal=false`) and the state
is surfaced, never disguised.

## State model

```
Stopped ──Play──► Priming ──device consumed ≥1 frame──► Playing
   ▲                  │                                   │
   │                  ├─Pause─► Paused ──Play──► Priming  │
   │                  │                                   │
   │                  └─all samples handed + buffer       │
   │                   drained──► Ended (terminal) ◄──────┘
   └──────Stop────── (from any state; resets playhead to 0)
```

Key rules (v3 requirement 26 — the false-"Playing" defect class):

- `Play()` returns after entering **Priming**. The transport NEVER
  reports `Playing` merely because a Play call was accepted.
- Promotion Priming → Playing happens lazily in the clock accessors
  (`Clock`, `Position`, `IsPlaying`) and only once the processed-sample
  counter has advanced beyond the transition epoch — i.e. the device
  actually consumed at least one frame.
- `IsPlaying()` is therefore an honest "confirmed playing" query; the
  UI play button and transport labels follow `Clock().State` (the
  30 fps render tick applies the promotion within one frame).
- Natural end of track: when all samples have been handed to the
  device AND the device buffer is drained, the state becomes
  **Ended** and the playhead freezes at the total. `Play` after Ended
  restarts from the start when the playhead is at the end, or from the
  last seek target when the user repositioned after the end.
- `Play` is idempotent: repeated calls during Priming or Playing never
  spawn a second device player/pipeline. `Pause` is legal during
  Priming (freezes at the epoch); pausing a paused/stopped/ended
  transport is a no-op. `Stop` from any state releases the player and
  resets to 0.

## Session (playback generation) ID

Every player spawn increments a session counter, exposed as
`ClockInfo.Session` and carried by every structured event. Pause/resume
keeps the session; Stop+Play, seek-while-playing and track replacement
create new sessions. The transport's transitions are synchronous under
one mutex, so stale async callbacks cannot arrive by construction; the
session ID gives log consumers the same correlation/rejection ability
for observations and future async sources.

## Clock contract (unchanged from the previous design)

The playhead is derived from bytes actually handed to the device minus
the device-buffered remainder (Oto `BufferedSize`), never from wall
clock. Every transition captures the epoch triple
`(basePos, baseCount, baseBuf)`; ticks delivered after a transition are
excluded by the frozen-state rule. Paused playheads never move.
`ClockInfo.Latency` reports the honest device-buffer uncertainty
(~100 ms default). Full details in `internal/playback/transport.go`'s
header comment and the deterministic test suite.

## Structured play lifecycle events (v3 §C vocabulary)

`Transport.SetEventSink(func(PlayEvent))` emits:

`play.preroll.started` (Priming entered) · `play.preroll.completed`
+ `play.position.advanced` + `play.state.changed` (Playing confirmed) ·
`play.state.changed` (every transition) · `play.eos` (Ended) ·
`play.stop.requested` (Stop entered) · `play.resources.released`
(player closed/dropped) · `play.error` (spawn failure, typed error).

Each event carries: session/generation ID, state, position, total,
timestamp, and error where applicable. The studio service
(`Studio.SetPlayEventSink`) attaches the sink to every transport it
creates; the Fyne UI writes the events to the runtime log at Info/Error
level. The UI additionally logs `play.requested` /
`play.pause.requested` (from the transport button/keyboard handler)
with session and position. **No media bytes, track titles, paths or
user library contents are ever logged** — the only identifiers are
session IDs and positions. (The remaining v3 event names —
`play.asset.resolved`, `play.decoder.selected`, `play.output.selected`,
`play.pipeline.created`, `play.buffering`, `play.underrun` — map to the
import/decode stage that runs before the transport exists and to Oto
behaviors the current backend does not expose; they are documented as
not-yet-emitted rather than faked.)

## Test evidence (this execution, Linux, Go 1.27.4)

All commands: `go test -tags goffi_static`, exit 0, race detector on:

- `internal/playback` — 23 tests: the six new v3 §C contract tests
  (Priming-until-confirmed, pause-during-priming freeze, natural
  EOS→Ended, replay-after-end, session increments per spawn, full
  event stream) plus all pre-existing deterministic clock-contract
  tests (pause-freeze, seek-while-paused, buffered arithmetic,
  60-cycle stress, concurrent readers, zero-duration track, EOF clamp,
  idempotent close, goroutine-leak) — none weakened or skipped.
- `internal/studio`, `ui/fyne` — full suites green (sink wiring
  compiles and integrates).

## Honest platform limits of this evidence

- Deterministic tests use a manual output (device consumption pumped
  synchronously by the test) and the silent pacer — they prove the
  state machine and clock arithmetic, NOT audible output.
- No physical audio device, no Windows machine and no loopback capture
  exist in this execution sandbox. Real-device confirmation
  (load → play → advancing playhead/output → pause → resume → seek →
  stop → replay → EOS, plus device-loss/corrupt-file/absent-device
  cases on Windows AND Linux) remains a hosted-runner /
  physical-machine gate. The app(5).log symptom (no play-related log
  lines at all) cannot be reproduced or root-caused from startup logs;
  the missing `play.*` diagnostics that would localize it on the next
  occurrence are exactly what this change adds.
