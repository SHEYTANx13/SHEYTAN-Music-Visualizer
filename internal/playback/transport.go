package playback

// transport.go — the track transport over the canonical PCM cache.
// The transport wraps the cache file in a bounded section reader with
// byte counting so the playhead reflects handed-to-device samples
// minus the device-buffered remainder.
//
// State model (v3 §C): Stopped -> Priming -> Playing -> Paused ->
// (Priming|Playing) -> Ended. Priming is entered synchronously by
// Play()/Seek-while-playing; Playing is CONFIRMED lazily by the clock
// accessors once the processed-sample counter has actually advanced
// beyond the transition epoch — the transport never reports Playing
// merely because a Play call was accepted (requirement 26). Ended is
// the natural end-of-track terminal state (all samples handed, device
// buffer drained); the next Play restarts (from the start when at the
// end, otherwise from the last seek target). Every spawn increments a
// session (playback generation) ID, exposed through ClockInfo and the
// structured PlayEvent stream (SetEventSink), so consumers can
// correlate and discard stale-generation observations.
//
// Clock contract (one coherent state-transition model):
//
//   - The canonical position source is the epoch triple
//     (basePos, baseCount, baseBuf). It is captured under the transport
//     mutex at every state transition (spawn, pause, resume, seek,
//     stop), which is the acknowledgement protocol for device ticks:
//     ticks delivered before the transition are folded into the epoch,
//     ticks delivered after it are excluded by the frozen-state rule.
//   - While playing, the playhead is
//     basePos + (counting.n - baseCount) - (buffered - baseBuf),
//     clamped to [0, total]. Bytes read while the device is paused are
//     excluded from the epoch on resume, so they can never move the
//     playhead.
//   - Once Pause() returns, the visible position is frozen at the
//     position captured by the pause transition: no late tick, buffered
//     fill or in-flight read can move it until a later successful
//     Play(). Seek while paused re-captures the epoch at the new
//     offset; resume starts from the seek position.
//   - Play/Pause/Stop/Seek are idempotent and spawn or release at most
//     one device player per transition; teardown closes owned resources
//     exactly once.

import (
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// PlayEventKind names follow the v3 §C diagnostics contract (extended
// by the V5 §3.6 play logging contract) so log consumers can rely on a
// stable event vocabulary. Every Play request terminates in
// play.output.started, play.failed, or a clear validation outcome
// (play.no-track / play.validation) — no request may silently vanish.
const (
	EvStateChanged      = "play.state.changed"
	EvPrerollStarted    = "play.preroll.started"
	EvPrerollCompleted  = "play.preroll.completed"
	EvOutputStarted     = "play.output.started"
	EvPositionAdvanced  = "play.position.advanced"
	EvClockAdvanced     = "play.clock.advanced"
	EvEOS               = "play.eos"
	EvStopRequested     = "play.stop.requested"
	EvResourcesReleased = "play.resources.released"
	EvPlayError         = "play.error"

	// Studio-level lifecycle events (emitted by the service layer
	// around the transport, through the same sink).
	EvValidation     = "play.validation"
	EvNoTrack        = "play.no-track"
	EvSessionCreated = "play.session.created"
	EvDeviceSelected = "play.device.selected"
	EvFailed         = "play.failed"
	EvSupersededPlay = "play.superseded"
)

// sessionCounter is the process-global monotonic playback generation
// counter: every player spawn anywhere gets a unique non-zero ID, so
// log correlation can never confuse sessions across track switches
// or transport replacements (V5 §3.2 — monotonic per playback attempt,
// not per transport object).
var sessionCounter atomic.Uint64

// PlayEvent is one structured transport lifecycle event. Events are
// delivered on the caller's goroutine WITHOUT the transport mutex held,
// so a sink may safely call back into the transport.
type PlayEvent struct {
	Kind     string
	Session  uint64
	State    State
	Position time.Duration
	Total    time.Duration
	Err      error
	At       time.Time
	// Detail carries stage/device context (never paths, media bytes or
	// user library metadata).
	Detail string
}

// Transport is one track's playback state machine. It is safe for
// concurrent use (UI goroutines call it; the device goroutine consumes
// the counting reader).
type Transport struct {
	mu sync.Mutex

	out      Output
	player   Player
	counting *countingReader
	file     *os.File
	total    int64 // total PCM bytes (frames*8)
	done     chan struct{}
	closed   bool

	volume float64
	state  State

	// session increments on every player spawn; it is the playback
	// generation ID. All state transitions and events carry it so a
	// consumer can correlate and discard observations from an obsolete
	// generation (v3 §C: reject stale callbacks).
	session uint64
	// sink receives structured lifecycle events (may be nil).
	sink func(PlayEvent)

	// Clock epoch, captured together under mu at every transition.
	basePos   int64 // canonical playhead at the transition
	baseCount int64 // counting.n at the transition
	baseBuf   int   // player.BufferedSize() at the transition
	// deviceLatency is the documented uncertainty of the playhead.
	deviceLatency time.Duration
	// lastClockAdv bounds the play.clock.advanced aggregate to at most
	// one event every few seconds while playing (V5 §3.6).
	lastClockAdv time.Time
}

// State is the transport state.
type State int

const (
	StateStopped State = iota
	StatePlaying
	StatePaused
	StatePriming // device started, playhead advance not yet confirmed
	StateEnded   // end of track reached naturally (terminal until next transition)
)

// String renders the state.
func (s State) String() string {
	switch s {
	case StatePlaying:
		return "playing"
	case StatePaused:
		return "paused"
	case StatePriming:
		return "priming"
	case StateEnded:
		return "ended"
	default:
		return "stopped"
	}
}

// NewTransport creates a transport over a canonical PCM file.
func NewTransport(out Output, pcmPath string) (*Transport, error) {
	f, err := os.Open(pcmPath)
	if err != nil {
		return nil, fmt.Errorf("playback: open pcm: %w", err)
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("playback: stat pcm: %w", err)
	}
	t := &Transport{
		out:           out,
		file:          f,
		total:         fi.Size(),
		done:          make(chan struct{}),
		volume:        0.9,
		deviceLatency: 100 * time.Millisecond,
	}
	return t, nil
}

// TotalDuration returns the track duration.
func (t *Transport) TotalDuration() time.Duration {
	return framesToDuration(int64(t.total / BytesPerFrame))
}

// framesToDuration converts canonical frames to duration.
func framesToDuration(frames int64) time.Duration {
	return time.Duration(frames*int64(time.Second)) / SampleRate
}

// durationToBytes converts a position to a canonical byte offset.
func durationToBytes(d time.Duration) int64 {
	frames := int64(d) * SampleRate / int64(time.Second)
	return frames * BytesPerFrame
}

// SetEventSink installs a structured lifecycle event sink (optional;
// events are dropped when nil). It must be called before Play.
func (t *Transport) SetEventSink(f func(PlayEvent)) {
	t.mu.Lock()
	t.sink = f
	t.mu.Unlock()
}

// emitLocked snapshots the sink under mu; the actual delivery happens
// in emitAfter, outside the lock.
func (t *Transport) emitLocked(ev PlayEvent) {
	ev.Session = t.session
	ev.State = t.state
	ev.Total = framesToDuration(int64(t.total / BytesPerFrame))
	if ev.At.IsZero() {
		ev.At = time.Now()
	}
	ev.Position = framesToDuration(t.rawPositionLocked() / BytesPerFrame)
	f := t.sink
	if f == nil {
		return
	}
	// Deliver after unlock via a tiny trampoline: the sink must be able
	// to call transport accessors without deadlocking.
	go func() { defer func() { _ = recover() }(); f(ev) }()
}

// Play starts or resumes playback. Resuming from a paused position
// re-captures the clock epoch so counters moved while paused are
// excluded from the resumed playhead.
//
// v3 §C confirmation contract: Play does NOT report StatePlaying. The
// transport enters StatePriming; the state is promoted to Playing only
// once the device has actually consumed samples beyond the transition
// epoch (the processed-sample counter advanced), which is observed by
// the clock accessors (Clock/Position/IsPlaying). Play is idempotent:
// repeated calls during Priming or Playing never spawn a second device
// player. Play after a natural end (StateEnded) restarts from the
// beginning when the playhead is at the end, or from the last seek
// target otherwise.
func (t *Transport) Play() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ErrClosed
	}
	if t.state == StatePlaying || t.state == StatePriming {
		return nil // idempotent: no second pipeline, no decoder respawn
	}
	if t.state == StateEnded && t.basePos >= t.total {
		// replay after a natural end restarts from the start
		t.basePos = 0
	}
	if t.player == nil {
		if err := t.spawnPlayerLocked(); err != nil {
			t.emitLocked(PlayEvent{Kind: EvPlayError, Err: err})
			return err
		}
	}
	t.player.SetVolume(t.volume)
	t.player.Play()
	t.captureEpochLocked()
	t.state = StatePriming
	t.emitLocked(PlayEvent{Kind: EvPrerollStarted})
	t.emitLocked(PlayEvent{Kind: EvStateChanged})
	t.confirmLocked()
	return nil
}

// spawnPlayerLocked creates the oto player from the current canonical
// position. Caller holds mu.
func (t *Transport) spawnPlayerLocked() error {
	if t.player != nil {
		return nil
	}
	// a fresh section reader from the current position
	r := io.NewSectionReader(t.file, t.basePos, t.total-t.basePos)
	pr := &countingReader{r: r}
	t.session = sessionCounter.Add(1) // new playback generation (unique process-wide)
	t.player = t.out.NewPlayer(pr)
	t.counting = pr
	if err := t.player.Err(); err != nil {
		// drop the broken player so the next Play spawns fresh
		t.player = nil
		t.counting = nil
		return fmt.Errorf("playback: player: %w", err)
	}
	t.captureEpochLocked()
	return nil
}

// handedLocked returns the bytes handed to the device beyond the
// current epoch. Caller holds mu.
func (t *Transport) handedLocked() int64 {
	if t.counting == nil {
		return 0
	}
	t.counting.mu.Lock()
	d := t.counting.n - t.baseCount
	t.counting.mu.Unlock()
	return d
}

// captureEpochLocked records the current counter values as the new
// clock epoch. Caller holds mu.
func (t *Transport) captureEpochLocked() {
	if t.counting != nil {
		t.counting.mu.Lock()
		t.baseCount = t.counting.n
		t.counting.mu.Unlock()
	} else {
		t.baseCount = 0
	}
	if t.player != nil {
		t.baseBuf = t.player.BufferedSize()
	} else {
		t.baseBuf = 0
	}
}

// confirmLocked promotes Priming to Playing once the processed-sample
// counter has advanced beyond the transition epoch (the device has
// consumed real samples — v3 §C requirement 26; the promotion also emits
// play.output.started, the V5 §3.6 confirmation that non-zero output is
// real), and detects natural end-of-track (all samples handed and the
// device buffer drained). Caller holds mu.
func (t *Transport) confirmLocked() {
	if t.state == StatePriming {
		if t.handedLocked() >= BytesPerFrame {
			t.state = StatePlaying
			t.emitLocked(PlayEvent{Kind: EvPrerollCompleted})
			t.emitLocked(PlayEvent{Kind: EvOutputStarted,
				Detail: "non-zero samples confirmed handed to the output device"})
			t.emitLocked(PlayEvent{Kind: EvPositionAdvanced})
			t.emitLocked(PlayEvent{Kind: EvStateChanged})
			t.lastClockAdv = time.Now()
		}
	}
	if t.state == StatePlaying {
		// Low-rate aggregate advance observation (at most every 3s).
		if time.Since(t.lastClockAdv) >= 3*time.Second {
			t.lastClockAdv = time.Now()
			t.emitLocked(PlayEvent{Kind: EvClockAdvanced})
		}
	}
	if t.state == StatePlaying || t.state == StatePriming {
		if t.player != nil && t.livePositionLocked() >= t.total && t.player.BufferedSize() == 0 {
			t.state = StateEnded
			t.basePos = t.total
			t.emitLocked(PlayEvent{Kind: EvEOS})
			t.emitLocked(PlayEvent{Kind: EvStateChanged})
		}
	}
}

// countingReader counts bytes actually read (handed to the device).
type countingReader struct {
	r  io.Reader
	mu sync.Mutex
	n  int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.mu.Lock()
	c.n += int64(n)
	c.mu.Unlock()
	return n, err
}

func (c *countingReader) Seek(offset int64, whence int) (int64, error) {
	if s, ok := c.r.(io.Seeker); ok {
		n, err := s.Seek(offset, whence)
		c.mu.Lock()
		if err == nil {
			// seek inside the section reader: account position
			c.n = n
		}
		c.mu.Unlock()
		return n, err
	}
	return 0, fmt.Errorf("playback: reader not seekable")
}

// Pause pauses playback, freezing the playhead at the position captured
// by this transition. Idempotent: pausing a paused, stopped or ended
// transport is a no-op. Pausing during Priming is legal: the playhead
// freezes at the epoch the same way. The device player is kept so
// resume keeps its buffer, but every counter the device moves after
// this transition is excluded from the frozen position and re-based on
// resume.
func (t *Transport) Pause() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || (t.state != StatePlaying && t.state != StatePriming) {
		return nil
	}
	// Capture the live playhead from the playing epoch before
	// stopping the device; all under mu, so the freeze is atomic.
	p := t.livePositionLocked()
	t.player.Pause()
	t.basePos = p
	t.captureEpochLocked()
	t.state = StatePaused
	t.emitLocked(PlayEvent{Kind: EvStateChanged})
	return nil
}

// Stop stops playback and returns to the start.
func (t *Transport) Stop() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ErrClosed
	}
	t.emitLocked(PlayEvent{Kind: EvStopRequested})
	t.releasePlayerLocked()
	t.basePos = 0
	t.baseCount = 0
	t.baseBuf = 0
	t.state = StateStopped
	t.emitLocked(PlayEvent{Kind: EvStateChanged})
	return nil
}

// releasePlayerLocked closes and drops the current player. Caller
// holds mu.
func (t *Transport) releasePlayerLocked() {
	if t.player != nil {
		t.player.Pause()
		_ = t.player.Close()
		t.player = nil
		t.counting = nil
		t.emitLocked(PlayEvent{Kind: EvResourcesReleased})
	}
	t.baseCount = 0
	t.baseBuf = 0
}

// Seek repositions the playhead. The canonical position moves to the
// clamped offset and the device buffer is flushed by replacing the
// player. Playback state is preserved: a playing track continues from
// the new position, a paused track stays paused exactly at it, and a
// stopped track starts from it on the next Play.
func (t *Transport) Seek(d time.Duration) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ErrClosed
	}
	off := durationToBytes(d)
	if off < 0 {
		off = 0
	}
	if off > t.total {
		off = t.total
	}
	off -= off % BytesPerFrame // frame-align the canonical offset
	wasPlaying := t.state == StatePlaying || t.state == StatePriming
	t.releasePlayerLocked()
	t.basePos = off
	if wasPlaying {
		if err := t.spawnPlayerLocked(); err != nil {
			t.state = StatePaused
			t.emitLocked(PlayEvent{Kind: EvPlayError, Err: err})
			return err
		}
		t.player.SetVolume(t.volume)
		t.player.Play()
		t.state = StatePriming
		t.emitLocked(PlayEvent{Kind: EvPrerollStarted})
		t.emitLocked(PlayEvent{Kind: EvStateChanged})
		t.confirmLocked()
	} else {
		t.emitLocked(PlayEvent{Kind: EvStateChanged})
	}
	return nil
}

// SetVolume sets the volume (0..1).
func (t *Transport) SetVolume(v float64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	t.volume = v
	if t.player != nil {
		t.player.SetVolume(v)
	}
}

// Volume returns the current volume.
func (t *Transport) Volume() float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.volume
}

// livePositionLocked computes the playhead from the current epoch while
// the device is consuming. Caller holds mu; state must be playing.
func (t *Transport) livePositionLocked() int64 {
	handed := t.basePos
	if t.counting != nil {
		t.counting.mu.Lock()
		handed += t.counting.n - t.baseCount
		t.counting.mu.Unlock()
	}
	buffered := t.baseBuf
	if t.player != nil {
		buffered = t.player.BufferedSize()
	}
	pos := handed - int64(buffered-t.baseBuf)
	if pos < 0 {
		pos = 0
	}
	if pos > t.total {
		pos = t.total
	}
	return pos
}

// rawPositionLocked returns the canonical playhead. While paused,
// priming, stopped or ended it is the frozen epoch base; device
// counters moved after the transition are ignored by design. Caller
// holds mu.
func (t *Transport) rawPositionLocked() int64 {
	if (t.state != StatePlaying && t.state != StatePriming) || t.player == nil {
		return t.basePos
	}
	if t.state == StatePriming {
		// during priming the epoch base is the honest position:
		// nothing is confirmed advancing yet
		return t.basePos
	}
	return t.livePositionLocked()
}

// Position returns the current playhead position. After Pause returns,
// this value is frozen until a later successful Play.
func (t *Transport) Position() time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.confirmLocked()
	return framesToDuration(t.rawPositionLocked() / BytesPerFrame)
}

// ClockInfo reports the playhead and its honest uncertainty.
type ClockInfo struct {
	Position   time.Duration `json:"position"`
	Total      time.Duration `json:"total"`
	State      State         `json:"state"`
	Latency    time.Duration `json:"latency"` // device-buffer uncertainty
	DeviceReal bool          `json:"deviceReal"`
	Session    uint64        `json:"session"` // playback generation ID
}

// Clock returns the full clock snapshot. It first applies the
// confirmation transitions (Priming -> Playing once samples actually
// advance; Playing -> Ended at natural end of track), so the reported
// state is never ahead of what the device has really done.
func (t *Transport) Clock() ClockInfo {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.confirmLocked()
	state := t.state
	_, real := t.out.(*OtoOutput)
	return ClockInfo{
		Position:   framesToDuration(t.rawPositionLocked() / BytesPerFrame),
		Total:      framesToDuration(int64(t.total / BytesPerFrame)),
		State:      state,
		Latency:    t.deviceLatency,
		DeviceReal: real,
		Session:    t.session,
	}
}

// IsPlaying reports whether audio is confirmed playing (the playhead
// has actually advanced on the device; a transport that just accepted a
// Play command reports false until then).
func (t *Transport) IsPlaying() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.confirmLocked()
	return t.state == StatePlaying
}

// Close stops playback and releases the device player and file.
// Idempotent: a second Close is a no-op.
func (t *Transport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true
	t.releasePlayerLocked()
	err := t.file.Close()
	close(t.done)
	return err
}

// Done returns a channel closed when the transport closes.
func (t *Transport) Done() <-chan struct{} { return t.done }
