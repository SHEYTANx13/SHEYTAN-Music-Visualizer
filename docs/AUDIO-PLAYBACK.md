# Audio Playback — state machine, confirmation contract and diagnostics

Updated 2026-11-17 (V5 work order §3 — silent-Play root cause, async
first play, full request-outcome logging contract). Implementation:
`internal/playback/transport.go` (state machine + clock),
`internal/playback/output.go` (device outputs), `internal/studio/studio.go`
(service), `ui/gogui/studio_panel.go` + `ui/gogui/actions.go` (UI +
logging; the file references below predate the ui/fyne removal and keep
their historical paths).

## V5 repair — the app(6).log silent Play (root cause)

`app(6).log` showed `play.requested session=0 position=0s` followed by
NO further events, no dialog and no state change. Root cause verified
from source: the first `Play` (the only path that opens the audio
device) called `ensureOutput` while already holding the studio mutex —
and `ensureOutput` locks the same mutex inside `sync.Once.Do`. Go
mutexes are not reentrant: the first Play **self-deadlocked** the
calling goroutine. In the pre-V5 synchronous code that goroutine was
the UI thread itself — the window froze with the studio mutex held,
nothing was ever logged, and no dialog could appear. Exactly the
app(6).log signature.

The V5 fix removes the entire defect class, not just the symptom:

1. `ensureOutput` now publishes its result through an atomic pointer;
   the `sync.Once` body takes NO studio locks, so it can never
   self-deadlock under any caller.
2. The first play runs on a worker goroutine (`startPlaybackAsync`):
   device open + PCM cache open never block the UI thread (V5
   non-negotiable 6). A second click during preparation is a harmless
   no-op (`play.validation`, no duplicate session).
3. A track import closes and clears the previous transport, so the
   next Play can never keep playing the PREVIOUS track's PCM
   (stale-transport track-switch defect).
4. The `importing` flag clears in the same critical section that
   publishes the track, BEFORE `EventImportDone` — a Play triggered by
   the done event can never see a stale "importing" state.
5. Every request outcome is now logged through the play event sink
   (contract below): no Play request can silently vanish.

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

Every player spawn allocates the next value of a **process-global
monotonic counter** (V5 §3.2: monotonic per playback attempt, never
reset per transport), exposed as `ClockInfo.Session` and carried by
every structured event. Pause/resume keeps the session; Stop+Play,
seek-while-playing and track replacement create new sessions — and
sessions from different tracks can never collide in one log. The
transport's transitions are synchronous under one mutex, so stale
async callbacks cannot arrive by construction; the session ID gives
log consumers the same correlation/rejection ability for observations
and future async sources.

## Clock contract (unchanged from the previous design)

The playhead is derived from bytes actually handed to the device minus
the device-buffered remainder (Oto `BufferedSize`), never from wall
clock. Every transition captures the epoch triple
`(basePos, baseCount, baseBuf)`; ticks delivered after a transition are
excluded by the frozen-state rule. Paused playheads never move.
`ClockInfo.Latency` reports the honest device-buffer uncertainty
(~100 ms default). Full details in `internal/playback/transport.go`'s
header comment and the deterministic test suite.

## Structured play lifecycle events (v3 §C + V5 §3.6 vocabulary)

`Transport.SetEventSink(func(PlayEvent))` emits:

`play.preroll.started` (Priming entered) · `play.preroll.completed` +
**`play.output.started`** + `play.position.advanced` +
`play.state.changed` (Playing confirmed — output.started is emitted
exactly once, only after non-zero samples were handed to the device) ·
**`play.clock.advanced`** (low-rate aggregate, at most one every 3 s
while playing) · `play.state.changed` (every transition) · `play.eos`
(Ended) · `play.stop.requested` (Stop entered) ·
`play.resources.released` (player closed/dropped) · `play.error`
(spawn failure, typed error).

The studio service emits its request-outcome events through the SAME
sink (V5 §3.6 — every request terminates in a logged outcome):

**`play.no-track`** (no playable track selected; actionable detail) ·
**`play.validation`** (request refused: import still running, or
playback already starting) · **`play.session.created`** (first play
spawned a session; carries the asset fingerprint, never a path) ·
**`play.device.selected`** (backend=oto;device=real, or
backend=silent(fallback) with the exact reason — a real device is
never claimed before one is proven) · **`play.failed`** (typed stage:
open-transport — audio device open or PCM cache read — or play) ·
**`play.superseded`** (a concurrent track import replaced the
transport out from under an in-flight first play).

Each event carries: session/generation ID, state, position, total,
timestamp, error and a detail string where applicable. The studio
service (`Studio.SetPlayEventSink`) attaches the sink to every
transport it creates and emits the studio-level events itself; the
Fyne UI writes the events to the runtime log at Info/Error level. The
UI additionally logs `play.requested` / `play.pause.requested` (from
the transport button/keyboard handler) with session and position.
**No media bytes, track titles, paths or user library contents are
ever logged** — the only identifiers are session IDs, positions and
the asset fingerprint. (The remaining v3 event names —
`play.asset.resolved`, `play.decoder.selected`, `play.pipeline.created`,
`play.buffering`, `play.underrun` — map to the import/decode stage
that runs before the transport exists and to Oto behaviors the current
backend does not expose; they are documented as not-yet-emitted rather
than faked.)

## Test evidence (this execution, Linux, Go 1.27.4)

All commands: `go test -tags goffi_static`, exit 0, race detector on
for the changed packages:

- `internal/playback` — the v3 §C contract tests (Priming-until-
  confirmed, pause-during-priming freeze, natural EOS→Ended,
  replay-after-end, session increments per spawn, full event stream)
  plus the V5 tests (output.started exactly once and only after
  samples advance; clock.advanced rate limiting) and all
  pre-existing deterministic clock-contract tests (pause-freeze,
  seek-while-paused, buffered arithmetic, 60-cycle stress, concurrent
  readers, zero-duration track, EOF clamp, idempotent close,
  goroutine-leak) — none weakened or skipped.
- `internal/studio` — V5 play-path suite: no-track actionable + logged
  outcome; import-in-progress logged; FIRST PLAY full lifecycle
  (session.created with fingerprint → device.selected truth-matched →
  output.started with non-zero session → advancing playhead → pause
  freeze → resume → stop) on the honest silent pacer (virtual output,
  explicitly labelled); double-first-play creates exactly one session;
  track switch never keeps a stale transport (two distinct sessions,
  the second on the new fingerprint); cold-cache play fails with the
  actionable `audio cache missing` event. `-race -count=2` green.

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
