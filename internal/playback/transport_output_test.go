package playback

// transport_output_test.go — V5 §3.6 contract: the transport must emit
// play.output.started exactly once, at the Priming→Playing promotion
// (only after non-zero samples were handed to the device), and
// play.clock.advanced as a low-rate aggregate while playing.

import (
	"sync"
	"testing"
	"time"
)

func recordEvents(t *testing.T, tr *Transport) (func() []PlayEvent, func(string) PlayEvent) {
	t.Helper()
	var mu sync.Mutex
	var evs []PlayEvent
	tr.SetEventSink(func(ev PlayEvent) {
		mu.Lock()
		evs = append(evs, ev)
		mu.Unlock()
	})
	snapshot := func() []PlayEvent {
		mu.Lock()
		defer mu.Unlock()
		return append([]PlayEvent{}, evs...)
	}
	waitFor := func(kind string) PlayEvent {
		deadline := time.Now().Add(2 * time.Second)
		for {
			for _, ev := range snapshot() {
				if ev.Kind == kind {
					return ev
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("event %q never arrived", kind)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	_ = waitFor
	return snapshot, waitFor
}

func countKinds(evs []PlayEvent, kind string) int {
	n := 0
	for _, ev := range evs {
		if ev.Kind == kind {
			n++
		}
	}
	return n
}

func TestOutputStartedOnlyAfterSamplesAdvance(t *testing.T) {
	tr, _ := newManualTransport(t, 2.0)
	defer tr.Close()
	snapshot, waitFor := recordEvents(t, tr)

	if err := tr.Play(); err != nil {
		t.Fatal(err)
	}
	// While still priming (no samples consumed): no output.started yet.
	if n := countKinds(snapshot(), EvOutputStarted); n != 0 {
		t.Fatal("play.output.started emitted before any sample was consumed")
	}
	// Device consumes one frame → promotion.
	tr.player.(*manualPlayer).pump(1)
	_ = tr.Clock()
	ev := waitFor(EvOutputStarted)
	if ev.Session == 0 {
		t.Fatal("play.output.started must carry a non-zero session")
	}
	if got := countKinds(snapshot(), EvOutputStarted); got != 1 {
		t.Fatalf("play.output.started emitted %d times, want exactly 1", got)
	}
	if !tr.IsPlaying() {
		t.Fatal("transport must be Playing after the confirmed promotion")
	}
}

func TestClockAdvancedIsRateLimited(t *testing.T) {
	tr, _ := newManualTransport(t, 10.0)
	defer tr.Close()
	snapshot, waitFor := recordEvents(t, tr)

	if err := tr.Play(); err != nil {
		t.Fatal(err)
	}
	tr.player.(*manualPlayer).pump(4800)
	_ = tr.Clock()
	waitFor(EvOutputStarted)
	if n := countKinds(snapshot(), EvClockAdvanced); n != 0 {
		t.Fatal("play.clock.advanced must not fire immediately after promotion")
	}
	// Force the aggregate window to elapse, then keep playing.
	tr.mu.Lock()
	tr.lastClockAdv = time.Now().Add(-4 * time.Second)
	tr.mu.Unlock()
	tr.player.(*manualPlayer).pump(4800)
	_ = tr.Clock()
	waitFor(EvClockAdvanced)
	if n := countKinds(snapshot(), EvClockAdvanced); n != 1 {
		t.Fatalf("play.clock.advanced fired %d times within one window, want 1", n)
	}
}
