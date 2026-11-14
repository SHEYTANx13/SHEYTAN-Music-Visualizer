package playback

// transport.go — the track transport over the canonical PCM cache.
// The transport wraps the cache file in a bounded section reader with
// byte counting so the playhead reflects handed-to-device samples
// minus the device-buffered remainder.
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
	"time"
)

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

	// Clock epoch, captured together under mu at every transition.
	basePos   int64 // canonical playhead at the transition
	baseCount int64 // counting.n at the transition
	baseBuf   int   // player.BufferedSize() at the transition
	// deviceLatency is the documented uncertainty of the playhead.
	deviceLatency time.Duration
}

// State is the transport state.
type State int

const (
	StateStopped State = iota
	StatePlaying
	StatePaused
)

// String renders the state.
func (s State) String() string {
	switch s {
	case StatePlaying:
		return "playing"
	case StatePaused:
		return "paused"
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

// Play starts or resumes playback. Resuming from a paused position
// re-captures the clock epoch so counters moved while paused are
// excluded from the resumed playhead.
func (t *Transport) Play() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ErrClosed
	}
	if t.state == StatePlaying {
		return nil
	}
	if t.player == nil {
		if err := t.spawnPlayerLocked(); err != nil {
			return err
		}
	}
	t.player.SetVolume(t.volume)
	t.player.Play()
	t.captureEpochLocked()
	t.state = StatePlaying
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
// by this transition. Idempotent: pausing a paused or stopped transport
// is a no-op. The device player is kept so resume keeps its buffer, but
// every counter the device moves after this transition is excluded from
// the frozen position and re-based on resume.
func (t *Transport) Pause() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed || t.state != StatePlaying {
		return nil
	}
	// Capture the live playhead from the playing epoch before
	// stopping the device; all under mu, so the freeze is atomic.
	p := t.livePositionLocked()
	t.player.Pause()
	t.basePos = p
	t.captureEpochLocked()
	t.state = StatePaused
	return nil
}

// Stop stops playback and returns to the start.
func (t *Transport) Stop() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return ErrClosed
	}
	t.releasePlayerLocked()
	t.basePos = 0
	t.baseCount = 0
	t.baseBuf = 0
	t.state = StateStopped
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
	wasPlaying := t.state == StatePlaying
	t.releasePlayerLocked()
	t.basePos = off
	if wasPlaying {
		if err := t.spawnPlayerLocked(); err != nil {
			t.state = StatePaused
			return err
		}
		t.player.SetVolume(t.volume)
		t.player.Play()
		t.state = StatePlaying
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

// rawPositionLocked returns the canonical playhead. While paused or
// stopped it is the frozen epoch base; device counters moved after the
// transition are ignored by design. Caller holds mu.
func (t *Transport) rawPositionLocked() int64 {
	if t.state != StatePlaying || t.player == nil {
		return t.basePos
	}
	return t.livePositionLocked()
}

// Position returns the current playhead position. After Pause returns,
// this value is frozen until a later successful Play.
func (t *Transport) Position() time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	return framesToDuration(t.rawPositionLocked() / BytesPerFrame)
}

// ClockInfo reports the playhead and its honest uncertainty.
type ClockInfo struct {
	Position   time.Duration `json:"position"`
	Total      time.Duration `json:"total"`
	State      State         `json:"state"`
	Latency    time.Duration `json:"latency"` // device-buffer uncertainty
	DeviceReal bool          `json:"deviceReal"`
}

// Clock returns the full clock snapshot.
func (t *Transport) Clock() ClockInfo {
	t.mu.Lock()
	defer t.mu.Unlock()
	state := t.state
	_, real := t.out.(*OtoOutput)
	return ClockInfo{
		Position:   framesToDuration(t.rawPositionLocked() / BytesPerFrame),
		Total:      framesToDuration(int64(t.total / BytesPerFrame)),
		State:      state,
		Latency:    t.deviceLatency,
		DeviceReal: real,
	}
}

// IsPlaying reports whether audio is currently playing.
func (t *Transport) IsPlaying() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
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
