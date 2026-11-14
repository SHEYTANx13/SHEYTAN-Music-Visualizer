package appclose

import (
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func nopLog(string, ...any) {}

func waitFor(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("timeout waiting for %s", what)
	}
}

func TestCleanIdleClose(t *testing.T) {
	var calls []string
	var mu sync.Mutex
	rec := func(s string) { mu.Lock(); calls = append(calls, s); mu.Unlock() }
	c := New(Hooks{
		CancelWork: func() error { rec("cancel"); return nil },
		Flush:      func() error { rec("flush"); return nil },
		Release:    func() error { rec("release"); return nil },
		Finalize:   func() { rec("finalize") },
	}, time.Second, nopLog)
	c.RequestClose("test")
	waitFor(t, c.Done(), "clean close")
	mu.Lock()
	defer mu.Unlock()
	want := []string{"cancel", "flush", "release", "finalize"}
	if len(calls) != len(want) {
		t.Fatalf("call order = %v, want %v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("call order = %v, want %v", calls, want)
		}
	}
	r := c.Result()
	if r.FlushErr != nil || r.CancelErr != nil || r.ReleaseErr != nil || r.TimedOut {
		t.Fatalf("clean close must be error-free: %+v", r)
	}
	if c.State() != StateClosed {
		t.Fatalf("state = %s, want closed", c.State())
	}
}

func TestDuplicateCloseRunsTeardownOnce(t *testing.T) {
	var finalizeCount, cancelCount int32
	c := New(Hooks{
		CancelWork: func() error { atomic.AddInt32(&cancelCount, 1); time.Sleep(50 * time.Millisecond); return nil },
		Finalize:   func() { atomic.AddInt32(&finalizeCount, 1) },
	}, time.Second, nopLog)
	for i := 0; i < 10; i++ {
		c.RequestClose("double-click")
	}
	waitFor(t, c.Done(), "teardown")
	time.Sleep(30 * time.Millisecond) // allow late duplicates to be observed
	if got := atomic.LoadInt32(&finalizeCount); got != 1 {
		t.Fatalf("finalize ran %d times, want exactly 1", got)
	}
	if got := atomic.LoadInt32(&cancelCount); got != 1 {
		t.Fatalf("cancel ran %d times, want exactly 1", got)
	}
}

func TestSingleConfirmationSurfaceForDuplicates(t *testing.T) {
	var confirmCount int32
	var proceed func()
	c := New(Hooks{
		Snapshot:   func() WorkReport { return WorkReport{Playback: true} },
		Confirm:    func(_ WorkReport, p func()) { atomic.AddInt32(&confirmCount, 1); proceed = p },
		CancelWork: func() error { return nil },
		Finalize:   func() {},
	}, time.Second, nopLog)
	// five rapid close events (double click + quit menu + OS duplicates)
	for i := 0; i < 5; i++ {
		c.RequestClose("surface")
	}
	if got := atomic.LoadInt32(&confirmCount); got != 1 {
		t.Fatalf("confirmation shown %d times, want exactly 1", got)
	}
	// user confirms: teardown runs once
	proceed()
	waitFor(t, c.Done(), "confirmed close")
	r := c.Result()
	if r.Duration <= 0 {
		t.Fatalf("duration not recorded")
	}
	if r.CompletedAt.IsZero() {
		t.Fatal("CompletedAt must be recorded (non-zero) for a completed close")
	}
	if got := len(r.States); got == 0 || r.States[len(r.States)-1] != StateClosed {
		t.Fatalf("result states must end in closed, got %v", r.States)
	}
}

// TestNearZeroDurationIsNormalized is the deterministic portability
// regression for the run-43541087097 Windows failure
// (TestSingleConfirmationSurfaceForDuplicates: "duration not recorded"):
// a fixed injected clock makes the measured span EXACTLY zero on every
// platform — the condition Windows hit with a sub-resolution monotonic
// tick. The public contract still requires a positive duration for a
// completed sequence, so the controller must normalize it without
// sleeping.
func TestNearZeroDurationIsNormalized(t *testing.T) {
	fixed := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return fixed }
	for i := 0; i < 100; i++ {
		c := newController(Hooks{
			CancelWork: func() error { return nil },
			Flush:      func() error { return nil },
			Release:    func() error { return nil },
			Finalize:   func() {},
		}, time.Second, nopLog, clock)
		c.RequestClose("near-zero")
		waitFor(t, c.Done(), "near-zero close")
		r := c.Result()
		if r.Duration <= 0 {
			t.Fatalf("iteration %d: zero-duration teardown must be normalized to MinCompletedDuration", i)
		}
		if r.Duration != MinCompletedDuration {
			t.Fatalf("iteration %d: fixed-clock duration = %s, want the documented minimum %s", i, r.Duration, MinCompletedDuration)
		}
		if r.CompletedAt.IsZero() {
			t.Fatalf("iteration %d: CompletedAt must be non-zero", i)
		}
		if c.State() != StateClosed {
			t.Fatalf("iteration %d: state = %s, want closed", i, c.State())
		}
	}
}

// TestDurationMeasuredOnNormalCompletion verifies the duration covers
// the full stage sequence (it must be at least the deliberate stage
// delay — a real measurement, not the normalized floor).
func TestDurationMeasuredOnNormalCompletion(t *testing.T) {
	c := New(Hooks{
		CancelWork: func() error { time.Sleep(25 * time.Millisecond); return nil },
		Release:    func() error { time.Sleep(25 * time.Millisecond); return nil },
		Finalize:   func() {},
	}, 2*time.Second, nopLog)
	c.RequestClose("normal")
	waitFor(t, c.Done(), "normal close")
	if d := c.Result().Duration; d < 50*time.Millisecond {
		t.Fatalf("duration %s must cover the staged work (>= 50ms)", d)
	}
}

// TestStaleProceedAfterStayIsHarmless pins the generation-token
// contract: after the user chooses Stay, a proceed callback captured by
// the OLD confirmation must not close the app — even if a NEWER
// confirmation is currently open.
func TestStaleProceedAfterStayIsHarmless(t *testing.T) {
	var finalized int32
	var proceedA, proceedB func()
	c := New(Hooks{
		Snapshot: func() WorkReport { return WorkReport{Export: true} },
		Confirm: func(_ WorkReport, p func()) {
			if proceedA == nil {
				proceedA = p
			} else {
				proceedB = p
			}
		},
		Finalize: func() { atomic.AddInt32(&finalized, 1) },
	}, time.Second, nopLog)
	c.RequestClose("first")
	if c.State() != StateConfirming {
		t.Fatalf("state = %s, want confirming", c.State())
	}
	c.CancelClose("user stays")
	// A second confirmation opens (the user tries to close again).
	c.RequestClose("second")
	if c.State() != StateConfirming {
		t.Fatalf("state = %s, want confirming after retry", c.State())
	}
	// The STALE proceed from the first (cancelled) confirmation fires.
	proceedA()
	time.Sleep(50 * time.Millisecond)
	if atomic.LoadInt32(&finalized) != 0 {
		t.Fatal("a stale proceed callback must never finalize the app")
	}
	if c.State() != StateConfirming {
		t.Fatalf("state = %s, want still confirming (new dialog owns the decision)", c.State())
	}
	// The CURRENT proceed still works and tears down exactly once.
	proceedB()
	waitFor(t, c.Done(), "second confirmation close")
	if got := atomic.LoadInt32(&finalized); got != 1 {
		t.Fatalf("finalize ran %d times, want exactly 1", got)
	}
}

// TestDoubleProceedRunsTeardownOnce verifies the once-only proceed
// callback: invoking proceed twice (double click on "Close anyway")
// must not run two teardowns.
func TestDoubleProceedRunsTeardownOnce(t *testing.T) {
	var finalizeCount, cancelCount int32
	var proceed func()
	c := New(Hooks{
		Snapshot:   func() WorkReport { return WorkReport{Playback: true} },
		Confirm:    func(_ WorkReport, p func()) { proceed = p },
		CancelWork: func() error { atomic.AddInt32(&cancelCount, 1); time.Sleep(20 * time.Millisecond); return nil },
		Finalize:   func() { atomic.AddInt32(&finalizeCount, 1) },
	}, time.Second, nopLog)
	c.RequestClose("double-proceed")
	proceed()
	proceed() // second invocation must be a no-op
	waitFor(t, c.Done(), "double-proceed close")
	time.Sleep(30 * time.Millisecond)
	if got := atomic.LoadInt32(&finalizeCount); got != 1 {
		t.Fatalf("finalize ran %d times, want exactly 1", got)
	}
	if got := atomic.LoadInt32(&cancelCount); got != 1 {
		t.Fatalf("cancel ran %d times, want exactly 1", got)
	}
}

// TestProceedAgainstCancelRaceIsAtomic hammers the proceed/CancelClose
// race: whichever wins the lock, the outcome is always consistent —
// either the app closes with exactly one finalize, or it stays open
// with no finalize. Never both, never neither-when-closing, never a
// panic.
func TestProceedAgainstCancelRaceIsAtomic(t *testing.T) {
	for i := 0; i < 50; i++ {
		var finalized int32
		var proceed func()
		c := New(Hooks{
			Snapshot: func() WorkReport { return WorkReport{Playback: true} },
			Confirm:  func(_ WorkReport, p func()) { proceed = p },
			Finalize: func() { atomic.AddInt32(&finalized, 1) },
		}, time.Second, nopLog)
		c.RequestClose("race")
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); proceed() }()
		go func() { defer wg.Done(); c.CancelClose("race-stay") }()
		wg.Wait()
		select {
		case <-c.Done():
			if got := atomic.LoadInt32(&finalized); got != 1 {
				t.Fatalf("iteration %d: closed with %d finalizes, want exactly 1", i, got)
			}
			if c.State() != StateClosed {
				t.Fatalf("iteration %d: Done closed but state = %s", i, c.State())
			}
		case <-time.After(80 * time.Millisecond):
			if got := atomic.LoadInt32(&finalized); got != 0 {
				t.Fatalf("iteration %d: app stayed open but finalize ran %d times", i, got)
			}
			if c.State() == StateClosed {
				t.Fatalf("iteration %d: state closed without Done", i)
			}
		}
	}
}

// TestConcurrentCloseSourcesSingleTeardown fires many simultaneous
// close surfaces (titlebar + menu + keyboard + OS) with work active;
// exactly one confirmation may open and, once confirmed, exactly one
// teardown runs.
func TestConcurrentCloseSourcesSingleTeardown(t *testing.T) {
	var confirmCount int32
	var proceed func()
	var mu sync.Mutex
	c := New(Hooks{
		Snapshot: func() WorkReport { return WorkReport{Playback: true, Export: true} },
		Confirm: func(_ WorkReport, p func()) {
			atomic.AddInt32(&confirmCount, 1)
			mu.Lock()
			proceed = p
			mu.Unlock()
		},
		CancelWork: func() error { time.Sleep(20 * time.Millisecond); return nil },
		Finalize:   func() {},
	}, time.Second, nopLog)
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			c.RequestClose([]string{"titlebar", "menu-quit", "keyboard", "os"}[n%4])
		}(i)
	}
	wg.Wait()
	if got := atomic.LoadInt32(&confirmCount); got != 1 {
		t.Fatalf("confirmation shown %d times, want exactly 1 under concurrency", got)
	}
	mu.Lock()
	p := proceed
	mu.Unlock()
	if p == nil {
		t.Fatal("the single confirmation must have delivered a proceed callback")
	}
	p()
	waitFor(t, c.Done(), "concurrent-sources close")
	if c.State() != StateClosed {
		t.Fatalf("state = %s, want closed", c.State())
	}
}

// TestResultVisibilityAfterDone guarantees the complete immutable
// result is published before Done closes: Result() after <-Done()
// always sees the final states trail, errors, non-zero CompletedAt and
// a positive duration — on every completion path.
func TestResultVisibilityAfterDone(t *testing.T) {
	flushErr := errors.New("disk full")
	withWork := []bool{false, true}
	for i, work := range withWork {
		var captured func()
		c := New(Hooks{
			Snapshot: func() WorkReport {
				if work {
					return WorkReport{Playback: true}
				}
				return WorkReport{}
			},
			Confirm:  func(_ WorkReport, p func()) { captured = p },
			Flush:    func() error { return flushErr },
			Finalize: func() {},
		}, time.Second, nopLog)
		c.RequestClose("visibility")
		if work {
			if captured == nil {
				t.Fatalf("path %d: confirmation not offered", i)
			}
			captured()
		}
		waitFor(t, c.Done(), "result visibility close")
		r := c.Result()
		if r.Duration <= 0 || r.CompletedAt.IsZero() {
			t.Fatalf("path %d: incomplete evidence record %+v", i, r)
		}
		if !errors.Is(r.FlushErr, flushErr) {
			t.Fatalf("path %d: flush evidence lost: %v", i, r.FlushErr)
		}
		if r.States[len(r.States)-1] != StateClosed {
			t.Fatalf("path %d: states trail must end closed, got %v", i, r.States)
		}
	}
}

func TestCancelCloseKeepsAppRunning(t *testing.T) {
	var finalized bool
	c := New(Hooks{
		Snapshot: func() WorkReport { return WorkReport{Export: true} },
		Confirm:  func(_ WorkReport, _ func()) {},
		Finalize: func() { finalized = true },
	}, time.Second, nopLog)
	c.RequestClose("titlebar")
	if c.State() != StateConfirming {
		t.Fatalf("state = %s, want confirming", c.State())
	}
	c.CancelClose("user stays")
	if c.State() != StateRunning {
		t.Fatalf("state = %s, want running after cancel", c.State())
	}
	time.Sleep(30 * time.Millisecond)
	if finalized {
		t.Fatal("finalize must not run when the user stays")
	}
	// after cancelling, a fresh request re-offers ONE confirmation
	c.RequestClose("titlebar")
	c.CancelClose("user stays again")
	// teardown never ran
	if c.State() == StateClosed {
		t.Fatal("close must not have completed")
	}
}

func TestBoundedStageDeadlineIsEvidence(t *testing.T) {
	slow := func() error { time.Sleep(300 * time.Millisecond); return nil }
	c := New(Hooks{
		CancelWork: slow,
		Flush:      slow,
		Release:    slow,
		Finalize:   func() {},
	}, 50*time.Millisecond, nopLog)
	start := time.Now()
	c.RequestClose("test")
	waitFor(t, c.Done(), "bounded shutdown")
	elapsed := time.Since(start)
	r := c.Result()
	if !r.TimedOut {
		t.Fatal("deadline overrun must be recorded as TimedOut evidence")
	}
	if r.CancelErr == nil || r.FlushErr == nil || r.ReleaseErr == nil {
		t.Fatalf("each overrun stage must carry its error: %+v", r)
	}
	// The window still closes: total wait is bounded, not unbounded.
	if elapsed > 2*time.Second {
		t.Fatalf("bounded shutdown took %s; must never hang", elapsed)
	}
}

func TestFlushErrorIsEvidenceNotBlocker(t *testing.T) {
	flushErr := errors.New("log write failed: disk full")
	c := New(Hooks{
		Flush:    func() error { return flushErr },
		Finalize: func() {},
	}, time.Second, nopLog)
	c.RequestClose("test")
	waitFor(t, c.Done(), "flush-error close")
	r := c.Result()
	if !errors.Is(r.FlushErr, flushErr) {
		t.Fatalf("flush error must be preserved as evidence, got %v", r.FlushErr)
	}
	if c.State() != StateClosed {
		t.Fatal("a failed flush must never prevent closing")
	}
}

func TestNoGoroutineLeakAfterClose(t *testing.T) {
	stop := make(chan struct{})
	slowWorker := func() error {
		<-stop
		return nil
	}
	c := New(Hooks{
		CancelWork: slowWorker,
		Release:    slowWorker,
		Finalize:   func() {},
	}, 100*time.Millisecond, nopLog)
	base := runtime.NumGoroutine()
	c.RequestClose("test")
	waitFor(t, c.Done(), "close")
	close(stop)
	deadline := time.Now().Add(3 * time.Second)
	for runtime.NumGoroutine() > base+2 {
		if time.Now().After(deadline) {
			t.Fatalf("goroutine leak: base %d, now %d", base, runtime.NumGoroutine())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestConfirmWithoutWorkClosesDirectly(t *testing.T) {
	var confirmed int32
	c := New(Hooks{
		Snapshot: func() WorkReport { return WorkReport{} },
		Confirm:  func(_ WorkReport, _ func()) { atomic.AddInt32(&confirmed, 1) },
		Finalize: func() {},
	}, time.Second, nopLog)
	c.RequestClose("test")
	waitFor(t, c.Done(), "no-work close")
	if atomic.LoadInt32(&confirmed) != 0 {
		t.Fatal("no confirmation must be offered when no work is active")
	}
}

func TestWorkReportDescribe(t *testing.T) {
	r := WorkReport{Playback: true, Export: true, UnsavedProject: true}
	d := r.Describe()
	for _, want := range []string{"playback", "export", "unsaved"} {
		if !containsLower(d, want) {
			t.Fatalf("Describe() = %q, want it to mention %q", d, want)
		}
	}
	if (WorkReport{}).Describe() != "no active work" {
		t.Fatal("empty report must say no active work")
	}
}

func containsLower(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
