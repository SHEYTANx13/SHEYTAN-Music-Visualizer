package playback

// transport_confirm_test.go — v3 §C contract tests: the transport must
// never report Playing until the playhead has actually advanced on the
// device; natural end-of-track must reach Ended; replay after Ended
// restarts; every spawn increments the session (generation) ID; the
// structured event stream carries the full lifecycle.

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// TestPlayIsPrimingUntilSamplesAdvance: Play() returns, the state is
// Priming (not Playing); only after the device consumes samples beyond
// the epoch does the state become Playing. No clock accessor ever
// reports Playing before the processed-sample counter advanced.
func TestPlayIsPrimingUntilSamplesAdvance(t *testing.T) {
	tr, _ := newManualTransport(t, 2.0)
	defer tr.Close()

	if err := tr.Play(); err != nil {
		t.Fatal(err)
	}
	if st := tr.Clock().State; st != StatePriming {
		t.Fatalf("state right after Play = %v, want priming", st)
	}
	if tr.IsPlaying() {
		t.Fatal("IsPlaying must be false during priming (nothing confirmed yet)")
	}
	// position during priming is the epoch base, not a clock guess
	if got := tr.Position(); got != 0 {
		t.Fatalf("priming position = %v, want 0", got)
	}
	// repeated Play during priming must not spawn a second player
	sess := tr.Clock().Session
	if err := tr.Play(); err != nil {
		t.Fatal(err)
	}
	if tr.Clock().Session != sess {
		t.Fatal("repeated Play during priming spawned a new session/player")
	}
	// device consumes 1 frame: promotion must happen at the next read
	tr.player.(*manualPlayer).pump(1)
	if st := tr.Clock().State; st != StatePlaying {
		t.Fatalf("state after first frame consumed = %v, want playing", st)
	}
	if !tr.IsPlaying() {
		t.Fatal("IsPlaying must be true once samples advanced")
	}
}

// TestPlayPauseDuringPrimingFreezes: pausing while still priming is a
// legal, well-defined transition; the playhead freezes at the epoch.
func TestPlayPauseDuringPrimingFreezes(t *testing.T) {
	tr, _ := newManualTransport(t, 2.0)
	defer tr.Close()

	if err := tr.Play(); err != nil {
		t.Fatal(err)
	}
	if err := tr.Pause(); err != nil {
		t.Fatal(err)
	}
	if st := tr.Clock().State; st != StatePaused {
		t.Fatalf("pause during priming → state %v, want paused", st)
	}
	if got := tr.Position(); got != 0 {
		t.Fatalf("frozen priming pause position = %v, want 0", got)
	}
	// a late device tick during the priming pause is excluded
	tr.player.(*manualPlayer).pump(4800)
	if got := tr.Position(); got != 0 {
		t.Fatalf("late tick moved priming pause to %v", got)
	}
	// resume → priming again, then confirmed playing after real consumption
	if err := tr.Play(); err != nil {
		t.Fatal(err)
	}
	if st := tr.Clock().State; st != StatePriming {
		t.Fatalf("resume → %v, want priming", st)
	}
	tr.player.(*manualPlayer).pump(4800) // 100 ms really consumed
	if st := tr.Clock().State; st != StatePlaying {
		t.Fatalf("after consumption → %v, want playing", st)
	}
	if got := tr.Position(); got != 100*time.Millisecond {
		t.Fatalf("resume position = %v, want 100ms", got)
	}
}

// TestNaturalEndOfTrackReachesEnded: when every sample has been handed
// and the device buffer is drained, the state becomes Ended (not a
// stuck "playing"), the playhead is the total, and the next Play
// restarts from the beginning.
func TestNaturalEndOfTrackReachesEnded(t *testing.T) {
	tr, out := newManualTransport(t, 0.5)
	defer tr.Close()

	if err := tr.Play(); err != nil {
		t.Fatal(err)
	}
	out.setBuffered(0)
	tr.player.(*manualPlayer).pump(48000) // demand everything
	if st := tr.Clock().State; st != StateEnded {
		t.Fatalf("state at natural end = %v, want ended", st)
	}
	if got := tr.Position(); got != 500*time.Millisecond {
		t.Fatalf("ended position = %v, want 500ms", got)
	}
	if tr.IsPlaying() {
		t.Fatal("IsPlaying must be false at Ended")
	}
	// a pause at end is a no-op, not an error
	if err := tr.Pause(); err != nil {
		t.Fatal(err)
	}
	// replay after natural end restarts from the start
	if err := tr.Play(); err != nil {
		t.Fatal(err)
	}
	if got := tr.Position(); got != 0 {
		t.Fatalf("replay-after-end start position = %v, want 0", got)
	}
	if st := tr.Clock().State; st != StatePriming {
		t.Fatalf("replay-after-end state = %v, want priming", st)
	}
}

// TestEndedAfterSeekAtEndKeepsTarget: Play after Ended restarts from
// the last seek target when the user repositioned after the end.
func TestEndedAfterSeekAtEndKeepsTarget(t *testing.T) {
	tr, out := newManualTransport(t, 1.0)
	defer tr.Close()

	if err := tr.Play(); err != nil {
		t.Fatal(err)
	}
	out.setBuffered(0)
	tr.player.(*manualPlayer).pump(96000) // all of it
	if tr.Clock().State != StateEnded {
		t.Fatal("expected ended after full consumption")
	}
	if err := tr.Seek(200 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if got := tr.Position(); got != 200*time.Millisecond {
		t.Fatalf("seek-at-end position = %v, want 200ms", got)
	}
	if err := tr.Play(); err != nil {
		t.Fatal(err)
	}
	if got := tr.Position(); got != 200*time.Millisecond {
		t.Fatalf("play-after-seek-at-end position = %v, want 200ms (kept target)", got)
	}
}

// TestSessionIncrementsPerSpawn: each player spawn increments the
// session (generation) ID; pause/resume keeps it; stop/seek/track
// replacement cycles increment it.
func TestSessionIncrementsPerSpawn(t *testing.T) {
	tr, _ := newManualTransport(t, 3.0)
	defer tr.Close()

	if err := tr.Play(); err != nil {
		t.Fatal(err)
	}
	s1 := tr.Clock().Session
	if s1 != 1 {
		t.Fatalf("first session = %d, want 1", s1)
	}
	// pause + resume keep the same player/session
	if err := tr.Pause(); err != nil {
		t.Fatal(err)
	}
	if err := tr.Play(); err != nil {
		t.Fatal(err)
	}
	if s := tr.Clock().Session; s != s1 {
		t.Fatalf("resume changed session %d → %d", s1, s)
	}
	// stop releases the player; a new Play spawns a new session
	if err := tr.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := tr.Play(); err != nil {
		t.Fatal(err)
	}
	if s := tr.Clock().Session; s != s1+1 {
		t.Fatalf("session after stop/play = %d, want %d", s, s1+1)
	}
	// seek while playing replaces the player: new session
	if err := tr.Seek(1 * time.Second); err != nil {
		t.Fatal(err)
	}
	if s := tr.Clock().Session; s != s1+2 {
		t.Fatalf("session after seek = %d, want %d", s, s1+2)
	}
}

// TestPlayEventStream: the structured event stream carries the full
// lifecycle with session IDs and v3 §C event names.
func TestPlayEventStream(t *testing.T) {
	tr, out := newManualTransport(t, 0.5)
	defer tr.Close()

	var mu sync.Mutex
	var kinds []string
	var sessions []uint64
	tr.SetEventSink(func(ev PlayEvent) {
		mu.Lock()
		kinds = append(kinds, ev.Kind)
		sessions = append(sessions, ev.Session)
		mu.Unlock()
	})

	waitFor := func(kind string) {
		deadline := time.Now().Add(2 * time.Second)
		for {
			mu.Lock()
			for _, k := range kinds {
				if k == kind {
					mu.Unlock()
					return
				}
			}
			mu.Unlock()
			if time.Now().After(deadline) {
				t.Fatalf("event %q never arrived; got %v", kind, kinds)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}

	_ = tr.Play() // -> priming
	waitFor(EvPrerollStarted)
	waitFor(EvStateChanged)
	tr.player.(*manualPlayer).pump(24000) // half the track
	_ = tr.Clock()                        // promotion -> playing
	waitFor(EvPrerollCompleted)
	waitFor(EvPositionAdvanced)
	out.setBuffered(0)
	tr.player.(*manualPlayer).pump(48000) // everything
	_ = tr.Clock()                        // EOS -> ended
	waitFor(EvEOS)
	_ = tr.Stop()
	waitFor(EvStopRequested)
	waitFor(EvResourcesReleased)

	mu.Lock()
	defer mu.Unlock()
	joined := strings.Join(kinds, ",")
	for _, want := range []string{EvPrerollStarted, EvPrerollCompleted, EvPositionAdvanced, EvEOS, EvStopRequested, EvResourcesReleased, EvStateChanged} {
		if !strings.Contains(joined, want) {
			t.Fatalf("event stream %q missing %q", joined, want)
		}
	}
	for _, s := range sessions {
		if s == 0 {
			t.Fatal("events must carry the nonzero session/generation ID")
		}
	}
}
