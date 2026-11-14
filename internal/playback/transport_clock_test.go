package playback

// transport_clock_test.go — deterministic clock-contract tests.
//
// The device consumption is driven synchronously by the test through
// manualOutput: no wall-clock pacing, no guessed sleeps, no tolerance
// windows. Every position assertion is exact.

import (
	"io"
	"runtime"
	"sync"
	"testing"
	"time"
)

// manualOutput is a test output whose device consumes bytes only when
// the test pumps them, so playhead arithmetic is fully deterministic.
type manualOutput struct {
	mu       sync.Mutex
	buffered int
}

func newManualOutput() *manualOutput { return &manualOutput{} }

func (o *manualOutput) setBuffered(n int) {
	o.mu.Lock()
	o.buffered = n
	o.mu.Unlock()
}

func (o *manualOutput) NewPlayer(r io.Reader) Player {
	return &manualPlayer{out: o, r: r}
}
func (o *manualOutput) Close() error { return nil }

type manualPlayer struct {
	out     *manualOutput
	r       io.Reader
	mu      sync.Mutex
	playing bool
}

// pump simulates the device consuming exactly frames frames from the
// transport's counting reader and returns the bytes actually read (less
// than requested only at end of track).
func (p *manualPlayer) pump(frames int) int {
	want := frames * BytesPerFrame
	buf := make([]byte, want)
	n := 0
	for n < want {
		k, err := p.r.Read(buf[n:])
		n += k
		if err != nil {
			break
		}
	}
	return n
}

func (p *manualPlayer) Play() {
	p.mu.Lock()
	p.playing = true
	p.mu.Unlock()
}
func (p *manualPlayer) Pause() {
	p.mu.Lock()
	p.playing = false
	p.mu.Unlock()
}
func (p *manualPlayer) Reset()                                       {}
func (p *manualPlayer) Seek(offset int64, whence int) (int64, error) { return 0, nil }
func (p *manualPlayer) IsPlaying() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.playing
}
func (p *manualPlayer) BufferedSize() int {
	p.out.mu.Lock()
	defer p.out.mu.Unlock()
	return p.out.buffered
}
func (p *manualPlayer) SetVolume(v float64) {}
func (p *manualPlayer) Volume() float64     { return 1 }
func (p *manualPlayer) Err() error          { return nil }
func (p *manualPlayer) Close() error        { return nil }

func newManualTransport(t *testing.T, seconds float64) (*Transport, *manualOutput) {
	dir := t.TempDir()
	pcm := dir + "/track.f32"
	writeSine(t, pcm, seconds)
	out := newManualOutput()
	tr, err := NewTransport(out, pcm)
	if err != nil {
		t.Fatal(err)
	}
	return tr, out
}

// TestPauseFreezesPendingTick: a tick the device delivers after Pause()
// returns must never move the visible position, and resume must start
// exactly at the frozen pause position.
func TestPauseFreezesPendingTick(t *testing.T) {
	tr, _ := newManualTransport(t, 2.0)
	defer tr.Close()

	if err := tr.Play(); err != nil {
		t.Fatal(err)
	}
	if n := tr.player.(*manualPlayer).pump(4800); n != 4800*BytesPerFrame {
		t.Fatalf("pump = %d bytes, want %d", n, 4800*BytesPerFrame)
	}
	if got := tr.Position(); got != 100*time.Millisecond {
		t.Fatalf("position after 100ms pump = %v, want exactly 100ms", got)
	}
	if err := tr.Pause(); err != nil {
		t.Fatal(err)
	}
	frozen := tr.Position()
	if frozen != 100*time.Millisecond {
		t.Fatalf("frozen pause position = %v, want 100ms", frozen)
	}
	// pending tick delivered after Pause returned: ignored.
	tr.player.(*manualPlayer).pump(4800)
	if got := tr.Position(); got != frozen {
		t.Fatalf("pending tick moved paused position: %v -> %v", frozen, got)
	}
	// more pending ticks still ignored
	tr.player.(*manualPlayer).pump(4800)
	if got := tr.Position(); got != frozen {
		t.Fatalf("second pending tick moved paused position: %v -> %v", frozen, got)
	}
	// resume starts exactly at the frozen position
	if err := tr.Play(); err != nil {
		t.Fatal(err)
	}
	if got := tr.Position(); got != frozen {
		t.Fatalf("resume position = %v, want exactly %v (no stale-tick drift)", got, frozen)
	}
	// the excluded stale bytes must not resurface later either
	tr.player.(*manualPlayer).pump(2400) // 50 ms
	if got := tr.Position(); got != frozen+50*time.Millisecond {
		t.Fatalf("position after 50ms pump on resume = %v, want %v", got, frozen+50*time.Millisecond)
	}
}

// TestPauseImmediateBeforeAndAfterTick: pause immediately before a tick
// and immediately after one produce the same frozen-playhead contract.
func TestPauseImmediateBeforeAndAfterTick(t *testing.T) {
	tr, _ := newManualTransport(t, 2.0)
	defer tr.Close()

	if err := tr.Play(); err != nil {
		t.Fatal(err)
	}
	// pause immediately after Play: freeze at 0.
	if err := tr.Pause(); err != nil {
		t.Fatal(err)
	}
	if got := tr.Position(); got != 0 {
		t.Fatalf("pause right after play froze at %v, want 0", got)
	}
	tr.player.(*manualPlayer).pump(960) // 20 ms "late" tick
	if got := tr.Position(); got != 0 {
		t.Fatalf("late tick moved paused position to %v, want 0", got)
	}
	if err := tr.Play(); err != nil {
		t.Fatal(err)
	}
	tr.player.(*manualPlayer).pump(960)
	if got := tr.Position(); got != 20*time.Millisecond {
		t.Fatalf("position = %v, want 20ms", got)
	}
	// pause immediately after a tick
	if err := tr.Pause(); err != nil {
		t.Fatal(err)
	}
	if got := tr.Position(); got != 20*time.Millisecond {
		t.Fatalf("pause right after tick froze at %v, want 20ms", got)
	}
}

// TestSeekWhilePausedAndResume: seek while paused updates the canonical
// position; resume starts from the seek target, not the old position.
func TestSeekWhilePausedAndResume(t *testing.T) {
	tr, _ := newManualTransport(t, 3.0)
	defer tr.Close()

	if err := tr.Play(); err != nil {
		t.Fatal(err)
	}
	tr.player.(*manualPlayer).pump(4800) // 100 ms
	if err := tr.Pause(); err != nil {
		t.Fatal(err)
	}
	if got := tr.Position(); got != 100*time.Millisecond {
		t.Fatalf("pause position = %v, want 100ms", got)
	}
	stale := tr.player.(*manualPlayer)
	if err := tr.Seek(1500 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if got := tr.Position(); got != 1500*time.Millisecond {
		t.Fatalf("seek-while-paused position = %v, want exactly 1.5s", got)
	}
	// stale device activity against the released player must not
	// move the canonical position
	stale.pump(4800)
	if got := tr.Position(); got != 1500*time.Millisecond {
		t.Fatalf("stale pump after seek moved position to %v", got)
	}
	// resume from the seek target
	if err := tr.Play(); err != nil {
		t.Fatal(err)
	}
	if got := tr.Position(); got != 1500*time.Millisecond {
		t.Fatalf("resume after seek = %v, want 1.5s", got)
	}
	tr.player.(*manualPlayer).pump(4800)
	if got := tr.Position(); got != 1600*time.Millisecond {
		t.Fatalf("position after resume pump = %v, want 1.6s", got)
	}
	// clamped seeks
	if err := tr.Seek(-5 * time.Second); err != nil {
		t.Fatal(err)
	}
	if got := tr.Position(); got != 0 {
		t.Fatalf("negative seek clamped to %v, want 0", got)
	}
	if err := tr.Seek(99 * time.Second); err != nil {
		t.Fatal(err)
	}
	if got := tr.Position(); got != tr.TotalDuration() {
		t.Fatalf("overrun seek clamped to %v, want %v", got, tr.TotalDuration())
	}
}

// TestRepeatedPauseResumeCycles: many transitions keep the playhead
// coherent, non-regressing, and never accumulate stale movement.
func TestRepeatedPauseResumeCycles(t *testing.T) {
	tr, _ := newManualTransport(t, 4.0)
	defer tr.Close()

	var last time.Duration
	for i := 0; i < 60; i++ {
		if err := tr.Play(); err != nil {
			t.Fatal(err)
		}
		tr.player.(*manualPlayer).pump(240) // 5 ms
		if err := tr.Pause(); err != nil {
			t.Fatal(err)
		}
		got := tr.Position()
		want := last + 5*time.Millisecond
		if got != want {
			t.Fatalf("cycle %d: position = %v, want %v", i, got, want)
		}
		// pending tick between cycles must stay frozen
		tr.player.(*manualPlayer).pump(240)
		if got := tr.Position(); got != want {
			t.Fatalf("cycle %d: pending tick moved frozen position to %v", i, got)
		}
		last = got
		if i%10 == 9 {
			if err := tr.Seek(1 * time.Second); err != nil {
				t.Fatal(err)
			}
			if got := tr.Position(); got != 1*time.Second {
				t.Fatalf("cycle %d: seek-while-paused = %v, want 1s", i, got)
			}
			last = 1 * time.Second
		}
	}
	// play/pause are idempotent
	if err := tr.Play(); err != nil {
		t.Fatal(err)
	}
	if err := tr.Play(); err != nil {
		t.Fatal(err)
	}
	if err := tr.Pause(); err != nil {
		t.Fatal(err)
	}
	if err := tr.Pause(); err != nil {
		t.Fatal(err)
	}
	if tr.IsPlaying() {
		t.Fatal("state must be paused after pause")
	}
	if got := tr.Position(); got != last {
		t.Fatalf("idempotent transitions moved position to %v, want %v", got, last)
	}
}

// TestLiveBufferedArithmetic: the playhead subtracts the device-buffered
// remainder relative to the epoch baseline, so buffering never moves a
// paused playhead and consumption advances it.
func TestLiveBufferedArithmetic(t *testing.T) {
	tr, out := newManualTransport(t, 2.0)
	defer tr.Close()

	out.setBuffered(3840)
	if err := tr.Play(); err != nil {
		t.Fatal(err)
	}
	if got := tr.Position(); got != 0 {
		t.Fatalf("position with buffered baseline = %v, want 0", got)
	}
	tr.player.(*manualPlayer).pump(4800) // device handed 100 ms
	if got := tr.Position(); got != 100*time.Millisecond {
		t.Fatalf("handed 100ms with stable buffer = %v, want 100ms", got)
	}
	// device consumes 10 ms of its buffer: playhead advances 10 ms
	out.setBuffered(0)
	if got := tr.Position(); got != 110*time.Millisecond {
		t.Fatalf("buffer drained 10ms: position = %v, want 110ms", got)
	}
	// paused: buffered changes must not move the frozen playhead
	if err := tr.Pause(); err != nil {
		t.Fatal(err)
	}
	out.setBuffered(1920)
	if got := tr.Position(); got != 110*time.Millisecond {
		t.Fatalf("paused buffer change moved position to %v, want 110ms", got)
	}
}

// TestConcurrentReadersDuringTransitions: parallel clock readers during
// play/pause/seek/stop transitions stay race-free and within bounds.
// Run with -race.
func TestConcurrentReadersDuringTransitions(t *testing.T) {
	tr, _ := newManualTransport(t, 3.0)
	defer tr.Close()

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				p := tr.Position()
				c := tr.Clock()
				_ = tr.IsPlaying()
				if p < 0 || p > 3*time.Second {
					t.Errorf("position out of bounds: %v", p)
				}
				if c.Position < 0 || c.Position > c.Total {
					t.Errorf("clock out of bounds: %+v", c)
				}
			}
		}()
	}
	seq := func() error { return nil }
	_ = seq
	for i := 0; i < 40; i++ {
		_ = tr.Play()
		tr.player.(*manualPlayer).pump(48) // 1 ms
		_ = tr.Pause()
		if i%5 == 4 {
			_ = tr.Seek(time.Duration(i*10) * time.Millisecond)
		}
		if i%11 == 10 {
			_ = tr.Stop()
		}
	}
	close(stop)
	wg.Wait()
	_ = tr.Stop()
	if got := tr.Position(); got != 0 {
		t.Fatalf("position after stop = %v, want 0", got)
	}
}

// TestZeroDurationTrack: a zero-length track plays, freezes and stops
// with defined behavior.
func TestZeroDurationTrack(t *testing.T) {
	dir := t.TempDir()
	pcm := dir + "/empty.f32"
	writeSine(t, pcm, 0.0)
	tr, err := NewTransport(newManualOutput(), pcm)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	if tr.TotalDuration() != 0 {
		t.Fatalf("total = %v, want 0", tr.TotalDuration())
	}
	if err := tr.Play(); err != nil {
		t.Fatal(err)
	}
	n := tr.player.(*manualPlayer).pump(4800)
	if n != 0 {
		t.Fatalf("pump on empty track read %d bytes, want 0", n)
	}
	if got := tr.Position(); got != 0 {
		t.Fatalf("position = %v, want 0", got)
	}
	if err := tr.Pause(); err != nil {
		t.Fatal(err)
	}
	if err := tr.Seek(1 * time.Second); err != nil {
		t.Fatal(err)
	}
	if got := tr.Position(); got != 0 {
		t.Fatalf("seek on empty track = %v, want clamped 0", got)
	}
	if err := tr.Stop(); err != nil {
		t.Fatal(err)
	}
}

// TestEndOfTrackClampsToTotal: pumping past the end clamps the playhead
// to the track duration without regression.
func TestEndOfTrackClampsToTotal(t *testing.T) {
	tr, _ := newManualTransport(t, 0.5)
	defer tr.Close()

	if err := tr.Play(); err != nil {
		t.Fatal(err)
	}
	tr.player.(*manualPlayer).pump(48000) // 1 s of demand against a 0.5 s track
	if got := tr.Position(); got != 500*time.Millisecond {
		t.Fatalf("position at EOF = %v, want 500ms", got)
	}
	before := tr.Position()
	tr.player.(*manualPlayer).pump(48000)
	if got := tr.Position(); got != before {
		t.Fatalf("position regressed/changed after EOF: %v -> %v", before, got)
	}
	// pause at end-of-track freezes the total
	if err := tr.Pause(); err != nil {
		t.Fatal(err)
	}
	if got := tr.Position(); got != 500*time.Millisecond {
		t.Fatalf("paused at EOF = %v, want 500ms", got)
	}
}

// TestTransportCloseIsIdempotent: close exactly once, operations after
// close are honest errors, Done is closed.
func TestTransportCloseIsIdempotent(t *testing.T) {
	tr, _ := newManualTransport(t, 1.0)
	if err := tr.Play(); err != nil {
		t.Fatal(err)
	}
	tr.player.(*manualPlayer).pump(480)
	if err := tr.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := tr.Close(); err != nil {
		t.Fatalf("second close must be a no-op, got %v", err)
	}
	if err := tr.Play(); err != ErrClosed {
		t.Fatalf("play after close = %v, want ErrClosed", err)
	}
	if err := tr.Seek(time.Second); err != ErrClosed {
		t.Fatalf("seek after close = %v, want ErrClosed", err)
	}
	if err := tr.Pause(); err != nil {
		t.Fatalf("pause after close must be a no-op, got %v", err)
	}
	select {
	case <-tr.Done():
	default:
		t.Fatal("Done must be closed after Close")
	}
}

// TestNoGoroutineLeakAfterPauseStopClose: repeated play/pause cycles on
// the silent output leave no idle pacer goroutines behind.
func TestNoGoroutineLeakAfterPauseStopClose(t *testing.T) {
	dir := t.TempDir()
	pcm := dir + "/t.f32"
	writeSine(t, pcm, 2.0)
	out := NewSilentOutput()
	defer out.Close()
	tr, err := NewTransport(out, pcm)
	if err != nil {
		t.Fatal(err)
	}

	// bounded wait for short-lived goroutines to exit; not a
	// correctness sleep — the state machine itself is verified by
	// the deterministic tests above.
	time.Sleep(50 * time.Millisecond)
	runtime.Gosched()
	base := runtime.NumGoroutine()
	for i := 0; i < 30; i++ {
		if err := tr.Play(); err != nil {
			t.Fatal(err)
		}
		if err := tr.Pause(); err != nil {
			t.Fatal(err)
		}
	}
	if err := tr.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		n := runtime.NumGoroutine()
		if n <= base+2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("goroutine leak: baseline %d, after 30 pause/play cycles + close: %d", base, n)
		}
		runtime.Gosched()
		time.Sleep(10 * time.Millisecond)
	}
}
