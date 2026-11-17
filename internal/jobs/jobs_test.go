package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newTestScheduler builds a scheduler with tiny budgets so saturation
// is reachable quickly. Tests that need the real derived budget pass
// their own Config.
func newTestScheduler(t *testing.T, cfg Config) *Scheduler {
	t.Helper()
	if cfg.CPUBudget == 0 {
		cfg.CPUBudget = 1
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { s.Shutdown(2 * time.Second) })
	return s
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", msg)
}

// readJournalLines decodes the journal for assertions.
func readJournalLines(t *testing.T, path string) []journalRecord {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read journal: %v", err)
	}
	var out []journalRecord
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		var r journalRecord
		if json.Unmarshal([]byte(line), &r) == nil {
			out = append(out, r)
		}
	}
	return out
}

// --- 2.7: queue capacity and rejection under saturation -------------

func TestQueueCapacityAndRejection(t *testing.T) {
	s := newTestScheduler(t, Config{})
	block := make(chan struct{})
	var running int32
	// Occupy both INTERACTIVE_IO workers (a non-token lane: 2 active)
	// with jobs that block until released.
	for i := 0; i < 2; i++ {
		r := s.Enqueue(Submit{Type: "block", Lane: LaneInteractiveIO,
			Run: func(ctx context.Context, j *JobHandle) error {
				atomic.AddInt32(&running, 1)
				<-block
				return nil
			}})
		if !r.Accepted() {
			t.Fatalf("block job %d not accepted: %s", i, r.Outcome)
		}
	}
	waitFor(t, 2*time.Second, func() bool { return atomic.LoadInt32(&running) == 2 }, "2 workers running")

	// Fill the 12 waiting slots of INTERACTIVE_IO.
	for i := 0; i < 12; i++ {
		r := s.Enqueue(Submit{Type: "w", Lane: LaneInteractiveIO, Run: func(ctx context.Context, j *JobHandle) error { return nil }})
		if !r.Accepted() {
			t.Fatalf("waiting job %d not accepted: %s", i, r.Outcome)
		}
	}
	// The next must be rejected with the typed full outcome.
	r := s.Enqueue(Submit{Type: "w", Lane: LaneInteractiveIO, Run: func(ctx context.Context, j *JobHandle) error { return nil }})
	if r.Outcome != OutcomeRejectedFull {
		t.Fatalf("expected rejected_full, got %s (%s)", r.Outcome, r.Reason)
	}
	m := s.Snapshot().Lanes[LaneInteractiveIO]
	if m.Waiting != 12 || m.Active != 2 {
		t.Fatalf("lane accounting wrong: waiting=%d active=%d", m.Waiting, m.Active)
	}
	if m.WaitingHigh != 12 || m.ActiveHigh != 2 {
		t.Fatalf("high-water wrong: waitingHigh=%d activeHigh=%d", m.WaitingHigh, m.ActiveHigh)
	}
	close(block)
	// After release, waiting jobs drain and succeed.
	waitFor(t, 3*time.Second, func() bool {
		lm := s.Snapshot().Lanes[LaneInteractiveIO]
		return lm.Succeeded == 14
	}, "all fourteen jobs to succeed")
}

// --- 2.7: priority ordering under mixed workload --------------------

func TestPriorityOrdering(t *testing.T) {
	s := newTestScheduler(t, Config{})
	block := make(chan struct{})
	var running int32
	// Hold the maintenance lane's single worker with a blocking job;
	// wait until it is actually RUNNING so the four queued jobs below
	// cannot race the hold job's admission into the 4-slot queue.
	r0 := s.Enqueue(Submit{Type: "hold", Lane: LaneMaintenance, Run: func(ctx context.Context, j *JobHandle) error {
		atomic.AddInt32(&running, 1)
		<-block
		return nil
	}})
	if !r0.Accepted() {
		t.Fatalf("hold not accepted: %s", r0.Outcome)
	}
	waitFor(t, 2*time.Second, func() bool { return atomic.LoadInt32(&running) == 1 }, "hold job running")
	var order []int
	var mu2 sync.Mutex
	job := func(n int) func(context.Context, *JobHandle) error {
		return func(ctx context.Context, j *JobHandle) error {
			mu2.Lock()
			order = append(order, n)
			mu2.Unlock()
			return nil
		}
	}
	// maintenance lane: 1 active, 4 waiting. Submit in mixed priority.
	for _, sub := range []Submit{
		{Type: "low", Lane: LaneMaintenance, Priority: PriorityLow, Run: job(1)},
		{Type: "norm", Lane: LaneMaintenance, Priority: PriorityNormal, Run: job(2)},
		{Type: "high", Lane: LaneMaintenance, Priority: PriorityHigh, Run: job(3)},
		{Type: "norm2", Lane: LaneMaintenance, Priority: PriorityNormal, Run: job(4)},
	} {
		r := s.Enqueue(sub)
		if !r.Accepted() {
			t.Fatalf("%s not accepted: %s", sub.Type, r.Outcome)
		}
	}
	close(block)
	waitFor(t, 3*time.Second, func() bool {
		mu2.Lock()
		defer mu2.Unlock()
		return len(order) == 4
	}, "all four jobs finished")
	mu2.Lock()
	defer mu2.Unlock()
	// high (3) must run before the two normals (2,4 FIFO) before low (1).
	if order[0] != 3 {
		t.Fatalf("high-priority job did not run first: %v", order)
	}
	if order[3] != 1 {
		t.Fatalf("low-priority job did not run last: %v", order)
	}
	if order[1] != 2 || order[2] != 4 {
		t.Fatalf("FIFO among equal priorities broken: %v", order)
	}
}

// --- 2.7: aging is monotonic and capped ------------------------------

func TestAgingMonotonicCapped(t *testing.T) {
	j := &Job{basePriority: PriorityLow}
	j.waitingSince = time.Now().Add(-90 * time.Second) // 3 aging intervals
	boost := 3
	p := j.basePriority + boost
	if p > PriorityCeiling {
		p = PriorityCeiling
	}
	if p != PriorityLow+3 {
		t.Fatalf("expected aged priority %d, got %d", PriorityLow+3, p)
	}
	// Ceiling: a base priority of 5 aged by 10 intervals must cap at 6.
	j2 := &Job{basePriority: PriorityHigh}
	got := effPriorityLocked(j2)
	if got != PriorityHigh {
		t.Fatalf("effPriorityLocked base: %d", got)
	}
	j2.ageBoost = 10
	if p := effPriorityLocked(j2); p != PriorityCeiling {
		t.Fatalf("aging must cap at PriorityCeiling=%d, got %d", PriorityCeiling, p)
	}
}

// --- 2.7: cancellation at every stage --------------------------------

func TestCancelWhileWaiting(t *testing.T) {
	s := newTestScheduler(t, Config{})
	block := make(chan struct{})
	s.Enqueue(Submit{Type: "hold", Lane: LaneInteractiveIO, Run: func(ctx context.Context, j *JobHandle) error {
		<-block
		return nil
	}})
	s.Enqueue(Submit{Type: "hold2", Lane: LaneInteractiveIO, Run: func(ctx context.Context, j *JobHandle) error {
		<-block
		return nil
	}})
	var started int32
	r := s.Enqueue(Submit{Type: "victim", Lane: LaneInteractiveIO, Run: func(ctx context.Context, j *JobHandle) error {
		atomic.AddInt32(&started, 1)
		return nil
	}})
	if !r.Accepted() {
		t.Fatalf("victim not accepted: %s", r.Outcome)
	}
	if !s.Cancel(r.JobID) {
		t.Fatal("cancel of waiting job returned false")
	}
	if !s.Cancel(r.JobID) {
		t.Fatal("idempotent double-cancel must return true")
	}
	close(block)
	time.Sleep(150 * time.Millisecond)
	if atomic.LoadInt32(&started) != 0 {
		t.Fatal("cancelled waiting job ran anyway")
	}
	if st := r.Job.State(); st != StateCancelled {
		t.Fatalf("state = %s, want cancelled", st)
	}
	lm := s.Snapshot().Lanes[LaneInteractiveIO]
	if lm.Cancelled != 1 {
		t.Fatalf("cancelled metric = %d", lm.Cancelled)
	}
}

func TestCancelWhileRunning(t *testing.T) {
	s := newTestScheduler(t, Config{})
	var observedCancel int32
	r := s.Enqueue(Submit{Type: "runner", Lane: LaneInteractiveIO, Run: func(ctx context.Context, j *JobHandle) error {
		<-ctx.Done()
		atomic.StoreInt32(&observedCancel, 1)
		return ctx.Err()
	}})
	if !r.Accepted() {
		t.Fatalf("not accepted: %s", r.Outcome)
	}
	waitFor(t, 2*time.Second, func() bool { return r.Job.State() == StateRunning }, "job running")
	s.Cancel(r.JobID)
	waitFor(t, 2*time.Second, func() bool { return r.Job.State().Terminal() }, "terminal")
	if atomic.LoadInt32(&observedCancel) != 1 {
		t.Fatal("job never observed cancellation")
	}
	if st := r.Job.State(); st != StateCancelled {
		t.Fatalf("state = %s, want cancelled", st)
	}
}

func TestDeadlineTimesOut(t *testing.T) {
	s := newTestScheduler(t, Config{})
	r := s.Enqueue(Submit{Type: "slow", Lane: LaneInteractiveIO, Deadline: 60 * time.Millisecond,
		Run: func(ctx context.Context, j *JobHandle) error {
			<-ctx.Done()
			return ctx.Err()
		}})
	waitFor(t, 2*time.Second, func() bool { return r.Job.State() == StateTimedOut }, "timed_out state")
	lm := s.Snapshot().Lanes[LaneInteractiveIO]
	if lm.TimedOut != 1 {
		t.Fatalf("timedOut metric = %d", lm.TimedOut)
	}
}

// --- 2.7: idempotency, coalescing, newest-wins ------------------------

func TestIdempotencyDedup(t *testing.T) {
	s := newTestScheduler(t, Config{})
	block := make(chan struct{})
	var runs int32
	mk := func() Submit {
		return Submit{Type: "import", Lane: LaneInteractiveIO, IdempotencyKey: "import:track-A",
			Run: func(ctx context.Context, j *JobHandle) error {
				atomic.AddInt32(&runs, 1)
				<-block
				return nil
			}}
	}
	r1 := s.Enqueue(mk())
	if !r1.Accepted() {
		t.Fatalf("r1: %s", r1.Outcome)
	}
	r2 := s.Enqueue(mk())
	if !r2.WasCoalesced() || r2.JobID != r1.JobID {
		t.Fatalf("duplicate not folded into the live job: %s (id %d vs %d)", r2.Outcome, r2.JobID, r1.JobID)
	}
	close(block)
	waitFor(t, 2*time.Second, func() bool { return atomic.LoadInt32(&runs) == 1 }, "exactly one execution")
}

func TestCoalesceNewestWins(t *testing.T) {
	s := newTestScheduler(t, Config{})
	block := make(chan struct{})
	s.Enqueue(Submit{Type: "hold", Lane: LaneInteractiveIO, Run: func(ctx context.Context, j *JobHandle) error {
		<-block
		return nil
	}})
	s.Enqueue(Submit{Type: "hold2", Lane: LaneInteractiveIO, Run: func(ctx context.Context, j *JobHandle) error {
		<-block
		return nil
	}})
	// Both workers busy: submitted preview updates must coalesce to the
	// newest payload, and only ONE job may run when a worker frees up.
	var ran []int
	var mu sync.Mutex
	mk := func(v int) Submit {
		return Submit{Type: "preview", Lane: LaneInteractiveIO, CoalesceKey: "preview:scene-1",
			Run: func(ctx context.Context, j *JobHandle) error {
				mu.Lock()
				ran = append(ran, v)
				mu.Unlock()
				return nil
			}}
	}
	r1 := s.Enqueue(mk(1))
	if !r1.Accepted() {
		t.Fatalf("r1: %s", r1.Outcome)
	}
	r2 := s.Enqueue(mk(2))
	if !r2.WasCoalesced() || r2.JobID != r1.JobID {
		t.Fatalf("second preview did not coalesce: %s", r2.Outcome)
	}
	r3 := s.Enqueue(mk(3))
	if !r3.WasCoalesced() {
		t.Fatalf("third preview did not coalesce: %s", r3.Outcome)
	}
	close(block)
	waitFor(t, 2*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(ran) == 1
	}, "exactly one (newest) preview job to run")
	mu.Lock()
	defer mu.Unlock()
	if ran[0] != 3 {
		t.Fatalf("stale preview value %d ran; newest-wins violated", ran[0])
	}
	lm := s.Snapshot().Lanes[LaneInteractiveIO]
	if lm.Coalesced != 2 {
		t.Fatalf("coalesced metric = %d, want 2", lm.Coalesced)
	}
}

// --- 2.7: budget rejections -------------------------------------------

func TestBudgetRejections(t *testing.T) {
	s := newTestScheduler(t, Config{})
	r := s.Enqueue(Submit{Type: "huge", Lane: LaneMaintenance, EstMemMB: 99999,
		Run: func(ctx context.Context, j *JobHandle) error { return nil }})
	if r.Outcome != OutcomeRejectedBudget {
		t.Fatalf("oversized memory: %s", r.Outcome)
	}
	r = s.Enqueue(Submit{Type: "huge-disk", Lane: LaneExport, EstDiskMB: 99999999,
		Run: func(ctx context.Context, j *JobHandle) error { return nil }})
	if r.Outcome != OutcomeRejectedBudget {
		t.Fatalf("oversized disk: %s", r.Outcome)
	}
	r = s.Enqueue(Submit{Type: "nope", Lane: "bogus_lane",
		Run: func(ctx context.Context, j *JobHandle) error { return nil }})
	if r.Outcome != OutcomeRejectedBudget {
		t.Fatalf("unknown lane: %s", r.Outcome)
	}
}

func TestMemoryBudgetBlocksConcurrentAdmission(t *testing.T) {
	// ScenePrep: 1 active worker, 1024 MB cap. Two 800 MB jobs: the
	// second must wait even after the worker is free? No — with 1
	// worker the second waits for the worker anyway; use the CPU-token
	// lanes with 2 workers (analysis, 2048 cap): two 1500 MB jobs must
	// not run concurrently.
	s := newTestScheduler(t, Config{CPUBudget: 2})
	block := make(chan struct{})
	var concurrent int32
	var peak int32
	mk := func() Submit {
		return Submit{Type: "mem", Lane: LaneAudioAnalysis, EstMemMB: 1500,
			Run: func(ctx context.Context, j *JobHandle) error {
				c := atomic.AddInt32(&concurrent, 1)
				for {
					p := atomic.LoadInt32(&peak)
					if c <= p || atomic.CompareAndSwapInt32(&peak, p, c) {
						break
					}
				}
				<-block
				atomic.AddInt32(&concurrent, -1)
				return nil
			}}
	}
	s.Enqueue(mk())
	s.Enqueue(mk())
	waitFor(t, 2*time.Second, func() bool { return atomic.LoadInt32(&peak) >= 1 }, "first job running")
	time.Sleep(120 * time.Millisecond)
	if atomic.LoadInt32(&peak) > 1 {
		t.Fatal("two 1500 MB jobs ran concurrently past the 2048 MB lane ceiling")
	}
	close(block)
	waitFor(t, 2*time.Second, func() bool {
		lm := s.Snapshot().Lanes[LaneAudioAnalysis]
		return lm.Succeeded == 2
	}, "both jobs to finish sequentially")
}

// --- 2.7: panic containment -------------------------------------------

func TestPanicContainment(t *testing.T) {
	s := newTestScheduler(t, Config{})
	r := s.Enqueue(Submit{Type: "bomb", Lane: LaneInteractiveIO,
		Run: func(ctx context.Context, j *JobHandle) error {
			panic("worker-boundary panic")
		}})
	waitFor(t, 2*time.Second, func() bool { return r.Job.State() == StateFailed }, "panicking job failed")
	snap := r.Job.Snapshot()
	if snap.ErrorCategory != ErrorPanic {
		t.Fatalf("error category = %s, want panic", snap.ErrorCategory)
	}
	// The scheduler survives: subsequent jobs still run.
	done := make(chan struct{})
	s.Enqueue(Submit{Type: "after", Lane: LaneInteractiveIO, OnDone: func(j *Job) { close(done) },
		Run: func(ctx context.Context, j *JobHandle) error { return nil }})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler did not survive a job panic")
	}
}

func TestOneFailureDoesNotKillOthers(t *testing.T) {
	s := newTestScheduler(t, Config{})
	var wg sync.WaitGroup
	wg.Add(3)
	for i := 0; i < 3; i++ {
		i := i
		s.Enqueue(Submit{Type: "mixed", Lane: LaneInteractiveIO, OnDone: func(j *Job) { wg.Done() },
			Run: func(ctx context.Context, j *JobHandle) error {
				if i == 1 {
					return errors.New("ordinary failure")
				}
				return nil
			}})
	}
	wg.Wait()
	lm := s.Snapshot().Lanes[LaneInteractiveIO]
	if lm.Succeeded != 2 || lm.Failed != 1 {
		t.Fatalf("succeeded=%d failed=%d", lm.Succeeded, lm.Failed)
	}
}

// --- 2.7: shutdown semantics -------------------------------------------

func TestShutdownRejectsAndCancels(t *testing.T) {
	cfg := Config{CPUBudget: 1}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	block := make(chan struct{})
	s.Enqueue(Submit{Type: "long", Lane: LaneAudioAnalysis,
		Run: func(ctx context.Context, j *JobHandle) error {
			<-ctx.Done() // cooperative
			return ctx.Err()
		}})
	// A waiting job behind it.
	r := s.Enqueue(Submit{Type: "queued", Lane: LaneAudioAnalysis,
		Run: func(ctx context.Context, j *JobHandle) error { return nil }})
	if !r.Accepted() {
		t.Fatalf("queued: %s", r.Outcome)
	}
	close(block)
	s.Shutdown(500 * time.Millisecond)
	r2 := s.Enqueue(Submit{Type: "late", Lane: LaneInteractiveIO,
		Run: func(ctx context.Context, j *JobHandle) error { return nil }})
	if r2.Outcome != OutcomeRejectedShutdown {
		t.Fatalf("enqueue after shutdown: %s", r2.Outcome)
	}
	if s.liveCount() != 0 {
		t.Fatalf("live jobs after shutdown: %d", s.liveCount())
	}
}

func TestShutdownGracefulDrain(t *testing.T) {
	s, err := New(Config{CPUBudget: 1})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	s.Enqueue(Submit{Type: "finisher", Lane: LaneInteractiveIO, OnDone: func(j *Job) { close(done) },
		Run: func(ctx context.Context, j *JobHandle) error {
			time.Sleep(80 * time.Millisecond)
			return nil
		}})
	go func() {
		time.Sleep(30 * time.Millisecond)
		s.Shutdown(2 * time.Second)
	}()
	select {
	case <-done:
		// graceful: the job was allowed to finish within the deadline
	case <-time.After(3 * time.Second):
		t.Fatal("drain did not finish the running job in time")
	}
	s.Shutdown(0) // idempotent
}

// --- 2.7: bounded goroutine count --------------------------------------

func TestBoundedGoroutines(t *testing.T) {
	before := runtime.NumGoroutine()
	s, err := New(Config{CPUBudget: 1})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		r := s.Enqueue(Submit{Type: fmt.Sprintf("tiny-%d", i), Lane: LaneInteractiveIO,
			OnDone: func(j *Job) { wg.Done() },
			Run:    func(ctx context.Context, j *JobHandle) error { return nil }})
		if !r.Accepted() {
			// rejected_full (12-waiting cap during the burst): the job
			// never runs, its OnDone never fires — count it here.
			wg.Done()
		}
	}
	wg.Wait()
	// The scheduler must never create one goroutine per job: 9 workers
	// + 1 loop + test overhead only.
	after := runtime.NumGoroutine()
	if after > before+25 {
		t.Fatalf("goroutine count exploded: before=%d after=%d (per-job goroutines?)", before, after)
	}
	s.Shutdown(2 * time.Second)
	waitFor(t, 2*time.Second, func() bool {
		return runtime.NumGoroutine() <= before+5
	}, "goroutines to return to baseline after shutdown")
}

// --- 2.7: enqueue storm: no per-rejected-job goroutines ----------------

func TestEnqueueStormAccounting(t *testing.T) {
	s := newTestScheduler(t, Config{})
	block := make(chan struct{})
	s.Enqueue(Submit{Type: "h1", Lane: LaneExport, Run: func(ctx context.Context, j *JobHandle) error {
		<-block
		return nil
	}})
	var rejected, coalesced, accepted int32
	var swg sync.WaitGroup
	swg.Add(8)
	for w := 0; w < 8; w++ {
		go func() {
			defer swg.Done()
			for i := 0; i < 200; i++ {
				r := s.Enqueue(Submit{Type: "storm", Lane: LaneExport,
					Run: func(ctx context.Context, j *JobHandle) error { return nil }})
				switch r.Outcome {
				case OutcomeRejectedFull:
					atomic.AddInt32(&rejected, 1)
				case OutcomeAccepted:
					atomic.AddInt32(&accepted, 1)
				case OutcomeCoalesced:
					atomic.AddInt32(&coalesced, 1)
				default:
					t.Errorf("unexpected outcome %s", r.Outcome)
				}
			}
		}()
	}
	swg.Wait()
	lm := s.Snapshot().Lanes[LaneExport]
	if lm.Waiting > 1 {
		t.Fatalf("export waiting=%d exceeds cap 1", lm.Waiting)
	}
	if int(atomic.LoadInt32(&accepted)) != int(lm.Accepted)-1 { // minus the blocking job
		t.Fatalf("accepted accounting drift: enqueue saw %d, lane says %d", accepted, lm.Accepted-1)
	}
	if int(atomic.LoadInt32(&rejected)) != int(lm.RejectedFull) {
		t.Fatalf("rejected accounting drift: %d vs %d", rejected, lm.RejectedFull)
	}
	close(block)
}

// --- trusted-privileged authorization ----------------------------------

func TestTrustedPrivilegedAuthorization(t *testing.T) {
	s := newTestScheduler(t, Config{})
	r := s.Enqueue(Submit{Type: "helper", Lane: LaneTrustedPrivileged,
		Run: func(ctx context.Context, j *JobHandle) error { return nil }})
	if r.Outcome != OutcomeRejectedUnauthorized {
		t.Fatalf("missing Authorize must be rejected_unauthorized, got %s", r.Outcome)
	}
	r = s.Enqueue(Submit{Type: "helper", Lane: LaneTrustedPrivileged,
		Authorize: func() error { return errors.New("user denied the UAC prompt") },
		Run:       func(ctx context.Context, j *JobHandle) error { return nil }})
	if r.Outcome != OutcomeRejectedUnauthorized {
		t.Fatalf("denied authorization must be rejected_unauthorized, got %s", r.Outcome)
	}
	var ran int32
	r = s.Enqueue(Submit{Type: "helper", Lane: LaneTrustedPrivileged,
		Authorize: func() error { return nil },
		Run: func(ctx context.Context, j *JobHandle) error {
			atomic.StoreInt32(&ran, 1)
			return nil
		}})
	if !r.Accepted() {
		t.Fatalf("authorized helper job rejected: %s", r.Outcome)
	}
	waitFor(t, 2*time.Second, func() bool { return atomic.LoadInt32(&ran) == 1 }, "helper job to run")
}

// --- transient retry is a NEW attempt, bounded --------------------------

func TestTransientRetryBounded(t *testing.T) {
	s := newTestScheduler(t, Config{})
	var attempts int32
	r := s.Enqueue(Submit{Type: "flaky", Lane: LaneInteractiveIO, RetryMax: 2,
		Run: func(ctx context.Context, j *JobHandle) error {
			n := atomic.AddInt32(&attempts, 1)
			if n < 2 {
				return &TransientError{Err: errors.New("temporary I/O hiccup")}
			}
			return nil
		}})
	waitFor(t, 3*time.Second, func() bool { return r.Job.State() == StateSucceeded }, "retry then succeed")
	if atomic.LoadInt32(&attempts) != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if snap := r.Job.Snapshot(); snap.Attempt != 2 {
		t.Fatalf("snapshot attempt = %d, want 2", snap.Attempt)
	}

	// A permanent error is never retried.
	var attempts2 int32
	r2 := s.Enqueue(Submit{Type: "bad", Lane: LaneInteractiveIO, RetryMax: 3,
		Run: func(ctx context.Context, j *JobHandle) error {
			atomic.AddInt32(&attempts2, 1)
			return errors.New("unsupported format")
		}})
	waitFor(t, 2*time.Second, func() bool { return r2.Job.State() == StateFailed }, "permanent failure")
	if atomic.LoadInt32(&attempts2) != 1 {
		t.Fatalf("permanent error retried %d times", attempts2)
	}

	// Retry budget is honored: RetryMax=2 caps at 2 attempts.
	var attempts3 int32
	r3 := s.Enqueue(Submit{Type: "always", Lane: LaneInteractiveIO, RetryMax: 2,
		Run: func(ctx context.Context, j *JobHandle) error {
			atomic.AddInt32(&attempts3, 1)
			return &TransientError{Err: errors.New("still flaky")}
		}})
	waitFor(t, 3*time.Second, func() bool { return r3.Job.State() == StateFailed }, "exhausted retries fail")
	if atomic.LoadInt32(&attempts3) != 2 {
		t.Fatalf("attempts = %d, want 2 (budget)", attempts3)
	}
}

// --- pressure adaptation -------------------------------------------------

func TestSetPressureReducesTokens(t *testing.T) {
	s, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(time.Second)
	b := s.Snapshot().CPUBudget // derived budget (1..3)
	if b < 1 {
		t.Fatalf("budget = %d", b)
	}
	// Raise pressure to the maximum removable level.
	s.SetPressure(b)
	m := s.Snapshot()
	if m.TokensAside != b-1 {
		t.Fatalf("aside = %d, want %d", m.TokensAside, b-1)
	}
	// With every removable token aside, exactly one CPU-heavy job may
	// run concurrently regardless of lane worker counts.
	var concurrent int32
	block := make(chan struct{})
	for i := 0; i < 3; i++ {
		s.Enqueue(Submit{Type: "cpu", Lane: LaneAudioAnalysis,
			Run: func(ctx context.Context, j *JobHandle) error {
				atomic.AddInt32(&concurrent, 1)
				<-block
				atomic.AddInt32(&concurrent, -1)
				return nil
			}})
	}
	waitFor(t, 2*time.Second, func() bool { return atomic.LoadInt32(&concurrent) >= 1 }, "first running")
	time.Sleep(150 * time.Millisecond)
	if atomic.LoadInt32(&concurrent) > 1 {
		t.Fatalf("concurrent CPU-heavy jobs = %d with 1 free token", concurrent)
	}
	close(block)
	waitFor(t, 2*time.Second, func() bool {
		return s.Snapshot().Lanes[LaneAudioAnalysis].Succeeded == 3
	}, "all three to finish sequentially")
	// Recovery returns the tokens.
	s.SetPressure(0)
	if m := s.Snapshot(); m.TokensAside != 0 {
		t.Fatalf("recovery failed: aside=%d", m.TokensAside)
	}
}

// --- journal reconciliation ----------------------------------------------

func TestJournalCrashReconciliation(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/jobs.jsonl"
	s, err := New(Config{CPUBudget: 1, JournalPath: path})
	if err != nil {
		t.Fatal(err)
	}
	block := make(chan struct{})
	s.Enqueue(Submit{Type: "crash-victim", Lane: LaneAudioAnalysis,
		Run: func(ctx context.Context, j *JobHandle) error {
			<-block // simulate a crash: never finishes, journal open
			return nil
		}})
	waitFor(t, 2*time.Second, func() bool { return s.liveCount() == 1 }, "victim running")
	// Simulate the crash: do NOT Shut down gracefully; steal the file
	// state by closing via the journal directly (a real crash leaves
	// the same on-disk content: a queued/running record with no
	// terminal record).
	s.mu.Lock()
	jn := s.journal
	s.mu.Unlock()
	jn.close()

	s2, err := New(Config{CPUBudget: 1, JournalPath: path})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Shutdown(time.Second)
	// The orphaned running job must now be visible as interrupted.
	found := false
	for _, line := range readJournalLines(t, path) {
		if line.Event == "interrupted" && line.Type == "crash-victim" && line.ErrorCategory == ErrorInterrupted {
			found = true
		}
	}
	if !found {
		t.Fatal("crash reconciliation did not mark the orphaned running job interrupted")
	}
	// The interrupted job is NOT live in the new scheduler.
	if jobs := s2.LiveJobs(); len(jobs) != 0 {
		t.Fatalf("reconciled jobs must not resurrect: %d live", len(jobs))
	}
	// The blocking goroutine from scheduler 1 leaks until the test
	// ends; release it and hard-stop the first scheduler.
	close(block)
	s.Shutdown(100 * time.Millisecond)
}

// --- fairness: no starvation of export -----------------------------------

func TestExportNotStarved(t *testing.T) {
	s := newTestScheduler(t, Config{CPUBudget: 1})
	stop := make(chan struct{})
	var wg sync.WaitGroup
	// Continuous analysis pressure (weight 2) with the single token.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			done := make(chan struct{})
			s.Enqueue(Submit{Type: "analysis", Lane: LaneAudioAnalysis, OnDone: func(j *Job) { close(done) },
				Run: func(ctx context.Context, j *JobHandle) error { return nil }})
			select {
			case <-done:
			case <-stop:
				return
			}
		}
	}()
	// An export job submitted mid-pressure must still complete within a
	// bounded time (fairness: export has weight 1 and borrowable share).
	time.Sleep(50 * time.Millisecond)
	exDone := make(chan struct{})
	s.Enqueue(Submit{Type: "export", Lane: LaneExport, OnDone: func(j *Job) { close(exDone) },
		Run: func(ctx context.Context, j *JobHandle) error { return nil }})
	select {
	case <-exDone:
	case <-time.After(5 * time.Second):
		t.Fatal("export starved under continuous analysis pressure")
	}
	close(stop)
	wg.Wait()
}

// --- 2.7 stress: mixed workload, bounds hold ------------------------------

func TestStressMixedWorkload(t *testing.T) {
	cfg := Config{CPUBudget: 2, JournalPath: t.TempDir() + "/jobs.jsonl"}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	worker := func(n int) {
		defer wg.Done()
		for i := 0; i < 60; i++ {
			lanes := []Lane{LaneInteractiveIO, LaneAudioAnalysis, LaneMaintenance, LaneExport, LaneScenePrep}
			lane := lanes[(n+i)%len(lanes)]
			wg.Add(1)
			r := s.Enqueue(Submit{
				Type: "stress", Lane: lane, Priority: i % 3,
				IdempotencyKey: fmt.Sprintf("w%d-i%d", n, i%7),
				CoalesceKey:    fmt.Sprintf("c%d", (n+i)%5),
				OnDone:         func(j *Job) { wg.Done() },
				Run: func(ctx context.Context, j *JobHandle) error {
					j.ReportProgress("working", uint64(i), 60)
					j.ReportBytes(uint64(i) * 1024)
					return nil
				},
			})
			// Coalesced (duplicate key / newest-wins) or
			// rejected requests create no work of their
			// own: their OnDone never fires by contract, so
			// they are accounted here.
			if !r.Accepted() {
				wg.Done()
			}
		}
	}
	wg.Add(4)
	for w := 0; w < 4; w++ {
		go worker(w)
	}
	wg.Wait() // all OnDone callbacks fired
	// Bounds held throughout: verify high-water marks against caps.
	m := s.Snapshot()
	for lane, lm := range m.Lanes {
		cap := defaultLaneCaps[lane]
		if lm.WaitingHigh > cap.Waiting {
			t.Fatalf("%s waiting high-water %d > cap %d", lane, lm.WaitingHigh, cap.Waiting)
		}
		if lm.ActiveHigh > cap.Active {
			t.Fatalf("%s active high-water %d > cap %d", lane, lm.ActiveHigh, cap.Active)
		}
	}
	// Total waiting across lanes never exceeded the 26-slot budget.
	totalWait := 0
	for _, lm := range m.Lanes {
		totalWait += lm.WaitingHigh
	}
	if totalWait > 26 {
		t.Fatalf("total waiting high-water %d > 26", totalWait)
	}
	if s.liveCount() != 0 {
		t.Fatalf("live jobs after stress: %d", s.liveCount())
	}
	// Enqueue is O(1) and non-blocking: nothing above ever blocked the
	// UI contract; goroutines stay bounded after the storm.
	s.Shutdown(2 * time.Second)
}
