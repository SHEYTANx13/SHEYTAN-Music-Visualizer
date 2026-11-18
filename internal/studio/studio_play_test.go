package studio

// studio_play_test.go — V5 §3 end-to-end Play contract through the
// service layer: every Play request terminates in a logged outcome
// (play.no-track / play.validation / play.session.created +
// play.device.selected + play.output.started / play.failed); the first
// play opens the device off the caller's goroutine; a track switch
// never keeps a stale transport; a cold cache yields an actionable
// error. The audio output in this test environment is whatever the
// host provides (a real device when present, the honest silent pacer
// otherwise) — the device truth is asserted to MATCH reality, never
// assumed.

import (
	"context"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/playback"
)

// writeAudibleWav writes the V5 §1.4 reproduction fixture: a
// deterministic, license-free PCM WAV — several seconds of an
// unmistakably audible 440 Hz tone interleaved with a 880 Hz pulse
// sequence — at a supported format (48 kHz, 16-bit).
func writeAudibleWav(t *testing.T, path string, seconds float64, baseFreq float64) {
	t.Helper()
	rate := 48000
	frames := int(seconds * float64(rate))
	data := make([]byte, 44+frames*4)
	copy(data[0:4], "RIFF")
	binary.LittleEndian.PutUint32(data[4:8], uint32(36+frames*4))
	copy(data[8:12], "WAVE")
	copy(data[12:16], "fmt ")
	binary.LittleEndian.PutUint32(data[16:20], 16)
	binary.LittleEndian.PutUint16(data[20:22], 1)
	binary.LittleEndian.PutUint16(data[22:24], 1) // mono
	binary.LittleEndian.PutUint32(data[24:28], uint32(rate))
	binary.LittleEndian.PutUint32(data[28:32], uint32(rate*2))
	binary.LittleEndian.PutUint16(data[32:34], 2)
	binary.LittleEndian.PutUint16(data[34:36], 16)
	copy(data[36:40], "data")
	binary.LittleEndian.PutUint32(data[40:44], uint32(frames*2))
	for i := 0; i < frames; i++ {
		tSec := float64(i) / float64(rate)
		// 440 Hz sustained tone with a 4 Hz 880 Hz pulse layer on top
		v := 0.45*math.Sin(2*math.Pi*baseFreq*tSec) +
			0.3*math.Sin(2*math.Pi*(baseFreq*2)*tSec)*(0.5+0.5*math.Sin(2*math.Pi*4*tSec))
		binary.LittleEndian.PutUint16(data[44+i*2:], uint16(int16(v*20000)))
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// playRecorder captures the studio play event stream.
type playRecorder struct {
	mu  sync.Mutex
	evs []playback.PlayEvent
}

func (r *playRecorder) install(s *Studio) {
	s.SetPlayEventSink(func(ev playback.PlayEvent) {
		r.mu.Lock()
		r.evs = append(r.evs, ev)
		r.mu.Unlock()
	})
}

func (r *playRecorder) snapshot() []playback.PlayEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]playback.PlayEvent{}, r.evs...)
}

func (r *playRecorder) wait(t *testing.T, kind string, timeout time.Duration) playback.PlayEvent {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		for _, ev := range r.snapshot() {
			if ev.Kind == kind {
				return ev
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("play event %q never arrived; got %+v", kind, r.snapshot())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitPolling waits for a transport-confirmation event while driving
// the clock accessors the same way the production UI render tick does
// (~30 fps): the transport confirms Priming→Playing lazily, so a
// consumer that never reads the clock would legitimately stay in
// priming (the design contract, not a defect).
func (r *playRecorder) waitPolling(t *testing.T, s *Studio, kind string, timeout time.Duration) playback.PlayEvent {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		for _, ev := range r.snapshot() {
			if ev.Kind == kind {
				return ev
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("play event %q never arrived; got %+v", kind, r.snapshot())
		}
		_ = s.Clock()
		time.Sleep(10 * time.Millisecond)
	}
}

func (r *playRecorder) count(kind string) int {
	n := 0
	for _, ev := range r.snapshot() {
		if ev.Kind == kind {
			n++
		}
	}
	return n
}

func importAndWait(t *testing.T, s *Studio, path string) {
	t.Helper()
	done := make(chan struct{})
	s.Subscribe(func(ev Event) {
		if ev.Kind == EventImportDone || ev.Kind == EventImportFailed {
			select {
			case <-done:
			default:
				close(done)
			}
		}
	})
	if err := s.ImportFile(path); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("import never finished")
	}
	if s.Track() == nil {
		t.Fatal("import failed (no track after EventImportFailed)")
	}
	// Deterministic handle quiescence (run 44298066730 failure C): the
	// follow-up analysis worker holds the canonical PCM (.f32) reader
	// open until it reaches terminal state, and the import worker arms
	// its terminal-state signal BEFORE the import-done event fires. An
	// explicit await here guarantees the test never races an owned
	// worker for a cache file — no sleeps, no taskkill, no TempDir
	// cleanup failures on Windows where an unjoined handle blocks
	// removal. Await BOTH the analysis-done event (worker body finished
	// its terminal branch) and the explicit close acknowledgement (the
	// goroutine returned and every owned handle is closed).
	anaCtx, anaCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer anaCancel()
	if err := s.WaitForAnalysisIdle(anaCtx); err != nil {
		t.Fatalf("analysis never reached terminal state after import: %v", err)
	}
}

func TestPlayRequestWithoutTrackIsActionableAndLogged(t *testing.T) {
	s, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rec := &playRecorder{}
	rec.install(s)

	err = s.PlayPause()
	if err == nil {
		t.Fatal("Play without a track must fail")
	}
	if err != ErrNoTrack {
		t.Fatalf("error = %v, want ErrNoTrack", err)
	}
	if !strings.Contains(err.Error(), "import") {
		t.Fatalf("error must be actionable: %v", err)
	}
	ev := rec.wait(t, playback.EvNoTrack, 2*time.Second)
	if ev.Detail == "" {
		t.Fatal("play.no-track must carry an actionable detail")
	}
	if st := s.Clock().State; st != playback.StateStopped {
		t.Fatalf("state after rejected play = %v, want stopped", st)
	}
}

func TestPlayRequestWhileImportingIsLogged(t *testing.T) {
	s, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rec := &playRecorder{}
	rec.install(s)

	// Deterministic in-package seam: mark an import as in flight.
	s.mu.Lock()
	s.importing = true
	s.mu.Unlock()
	err = s.PlayPause()
	s.mu.Lock()
	s.importing = false
	s.mu.Unlock()
	if err == nil || err != ErrImportInProgress {
		t.Fatalf("error = %v, want ErrImportInProgress", err)
	}
	rec.wait(t, playback.EvValidation, 2*time.Second)
}

func TestFirstPlayLifecycleFullContract(t *testing.T) {
	s, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rec := &playRecorder{}
	rec.install(s)

	wav := filepath.Join(t.TempDir(), "fixture-440hz.wav")
	writeAudibleWav(t, wav, 4.0, 440)
	importAndWait(t, s, wav)
	fp := s.Track().Fingerprint

	if err := s.PlayPause(); err != nil {
		t.Fatal(err)
	}
	// The first play runs off the caller goroutine; every stage must
	// terminate in a logged event (V5 §3.6).
	started := rec.waitPolling(t, s, playback.EvOutputStarted, 15*time.Second)
	if started.Session == 0 {
		t.Fatal("play.output.started must carry a non-zero session/generation")
	}
	created := rec.wait(t, playback.EvSessionCreated, 2*time.Second)
	if created.Session == 0 || !strings.Contains(created.Detail, "fingerprint="+fp) {
		t.Fatalf("play.session.created = %+v (want fingerprint %s)", created, fp)
	}
	dev := rec.wait(t, playback.EvDeviceSelected, 2*time.Second)
	if s.DeviceReal() {
		if !strings.Contains(dev.Detail, "device=real") {
			t.Fatalf("device event contradicts DeviceReal: %+v", dev)
		}
	} else {
		// Honest virtual/silent sink — explicitly labelled, never
		// presented as a real device.
		if !strings.Contains(dev.Detail, "backend=silent(fallback)") {
			t.Fatalf("device event contradicts silent fallback: %+v", dev)
		}
	}
	// Authoritative clock must advance (samples consumed on the output).
	waitFor(t, s, 5*time.Second, func() bool {
		return s.Clock().Position > 0
	}, "playhead to advance")
	if !s.IsPlayingLike() {
		t.Fatal("studio must report playing-like after confirmed output")
	}

	// Pause freezes the visible position.
	if err := s.PlayPause(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, s, 5*time.Second, func() bool {
		return s.Clock().State == playback.StatePaused
	}, "pause to take effect")
	frozen := s.Clock().Position
	time.Sleep(150 * time.Millisecond)
	if got := s.Clock().Position; got != frozen {
		t.Fatalf("paused position moved: %v -> %v", frozen, got)
	}
	// Resume, then stop: output ends, state returns to stopped.
	if err := s.PlayPause(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, s, 5*time.Second, func() bool {
		return s.Clock().State == playback.StatePlaying
	}, "resume to reach playing")
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, s, 5*time.Second, func() bool {
		return s.Clock().State == playback.StateStopped
	}, "stop to reach stopped")
	if got := s.Clock().Position; got != 0 {
		t.Fatalf("stop position = %v, want 0", got)
	}
	rec.wait(t, playback.EvStopRequested, 2*time.Second)
}

func TestDoubleFirstPlayDoesNotDuplicateSessions(t *testing.T) {
	s, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rec := &playRecorder{}
	rec.install(s)

	wav := filepath.Join(t.TempDir(), "fixture-880hz.wav")
	writeAudibleWav(t, wav, 3.0, 880)
	importAndWait(t, s, wav)

	// Rapid double click on a cold transport.
	if err := s.PlayPause(); err != nil {
		t.Fatal(err)
	}
	if err := s.PlayPause(); err != nil {
		t.Fatal(err)
	}
	rec.waitPolling(t, s, playback.EvOutputStarted, 15*time.Second)
	// Exactly one session may be created for the pair of clicks.
	sessions := map[uint64]bool{}
	for _, ev := range rec.snapshot() {
		if ev.Kind == playback.EvSessionCreated {
			sessions[ev.Session] = true
		}
	}
	if len(sessions) != 1 {
		t.Fatalf("double click created %d sessions, want exactly 1", len(sessions))
	}
}

func TestTrackSwitchNeverKeepsStaleTransport(t *testing.T) {
	s, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rec := &playRecorder{}
	rec.install(s)

	wavA := filepath.Join(t.TempDir(), "a-440.wav")
	writeAudibleWav(t, wavA, 2.0, 440)
	importAndWait(t, s, wavA)
	if err := s.PlayPause(); err != nil {
		t.Fatal(err)
	}
	rec.waitPolling(t, s, playback.EvOutputStarted, 15*time.Second)

	// Import a different track: the old transport must be dropped so
	// the next play uses the NEW fingerprint's PCM.
	wavB := filepath.Join(t.TempDir(), "b-990.wav")
	writeAudibleWav(t, wavB, 2.0, 990)
	importAndWait(t, s, wavB)
	fpB := s.Track().Fingerprint

	s.mu.RLock()
	staleTransport := s.transport
	s.mu.RUnlock()
	if staleTransport != nil {
		t.Fatal("import of a new track must reset the stale transport")
	}

	if err := s.PlayPause(); err != nil {
		t.Fatal(err)
	}
	// The post-switch session.created must carry track B's fingerprint
	// (earlier events belong to track A's session).
	deadline := time.Now().Add(15 * time.Second)
	for {
		found := false
		for _, ev := range rec.snapshot() {
			if ev.Kind == playback.EvSessionCreated && strings.Contains(ev.Detail, "fingerprint="+fpB) {
				found = true
				break
			}
		}
		if found {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no post-switch session created for fingerprint %s; events: %+v", fpB, rec.snapshot())
		}
		time.Sleep(10 * time.Millisecond)
	}
	rec.waitPolling(t, s, playback.EvOutputStarted, 15*time.Second)
	// The confirmed output must be playing track B, not the stale A:
	// exactly two sessions total (A then B), the later one live.
	live := map[uint64]bool{}
	for _, ev := range rec.snapshot() {
		if ev.Kind == playback.EvSessionCreated {
			live[ev.Session] = true
		}
	}
	if len(live) != 2 {
		t.Fatalf("expected exactly 2 sessions across the track switch, got %d (%v)", len(live), live)
	}
}

func TestColdCachePlayFailsWithActionableEvent(t *testing.T) {
	s, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rec := &playRecorder{}
	rec.install(s)

	wav := filepath.Join(t.TempDir(), "fixture-cold.wav")
	writeAudibleWav(t, wav, 2.0, 440)
	importAndWait(t, s, wav)
	fp := s.Track().Fingerprint

	// Simulate a wiped cache (external deletion between runs).
	pcm := s.cache.RawPath(fp)
	if err := os.Remove(pcm); err != nil {
		t.Fatal(err)
	}

	if err := s.PlayPause(); err != nil {
		t.Fatal(err) // async first play: the failure arrives as an event
	}
	failed := rec.wait(t, playback.EvFailed, 15*time.Second)
	if failed.Err == nil || !strings.Contains(failed.Err.Error(), "audio cache missing") {
		t.Fatalf("cold-cache failure must be actionable: %+v", failed)
	}
	if !strings.Contains(failed.Detail, "open-transport") {
		t.Fatalf("cold-cache failure must name the stage: %+v", failed)
	}
}
