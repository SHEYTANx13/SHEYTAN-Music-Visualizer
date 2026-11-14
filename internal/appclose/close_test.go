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
	if got := c.Result().Duration; got <= 0 {
		t.Fatalf("duration not recorded")
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
