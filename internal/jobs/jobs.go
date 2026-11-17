// Package jobs is the single app-owned bounded work orchestration
// service for SHEYTAN Music Visualizer.
//
// One scheduler owns every long-running non-real-time workload lane
// with explicit capacities, typed admission outcomes, weighted fair
// sharing of the CPU-heavy budget, monotonic aging, cooperative
// cancellation, panic containment at the worker boundary, a bounded
// job journal with crash reconciliation, and a truthful metrics
// snapshot for the Job Center / diagnostics surfaces.
//
// The real-time audio callback/device thread and the UI event loop are
// NOT worker-pool lanes and are never scheduled here (queue classes A
// and B of the orchestration contract): the audio path is owned by the
// playback package, the UI thread by the Fyne runtime.
//
// Capacity contract (hard ceilings, conservative first implementation):
//
//	LaneInteractiveIO      12 waiting /  2 active
//	LaneAudioAnalysis       4 waiting /  2 active (CPU-heavy)
//	LaneScenePrep           2 waiting /  1 active (CPU-heavy)
//	LaneGPUUploadRender     2 waiting /  1 active submission owner
//	LaneExport              1 waiting /  1 active (CPU-heavy)
//	LaneMaintenance         4 waiting /  1 active (CPU-heavy)
//	LaneTrustedPrivileged   1 outstanding
//
// The waiting caps total 26 pending jobs across the lanes. Excess work
// is rejected with a typed outcome and an actionable message, or
// coalesced only for jobs that carry an explicit coalesce key. There is
// no overflow list, spillover channel or per-job goroutine.
//
// The CPU-heavy concurrency budget shared by analysis, scene
// preparation, CPU-side export work and maintenance is
// max(1, min(3, floor(GOMAXPROCS/3))). The audio callback and the UI
// event loop never wait for these tokens.
//
// Pause/Resume is deliberately NOT offered in this first
// implementation: no current job type implements and tests safe
// pause, and the Job Center never shows a control that silently does
// nothing (Section 2.9). The state is reserved in the lifecycle for a
// future, separately tested implementation.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"
)

// Lane identifies a bounded scheduling lane.
type Lane string

const (
	LaneInteractiveIO     Lane = "interactive_io"
	LaneAudioAnalysis     Lane = "audio_analysis"
	LaneScenePrep         Lane = "scene_prep"
	LaneGPUUploadRender   Lane = "gpu_upload_render"
	LaneExport            Lane = "export"
	LaneMaintenance       Lane = "maintenance"
	LaneTrustedPrivileged Lane = "trusted_privileged"
)

// laneCap is the immutable per-lane ceiling (waiting, active).
type laneCap struct {
	Waiting int
	Active  int
	// CPUHeavy: admission requires one shared CPU-heavy token.
	CPUHeavy bool
	// Weight is the relative fair-share weight among token-bound
	// lanes (interactive I/O 4, audio analysis 2, scene prep 1,
	// export 1, maintenance 1 — Section 2.8). Lanes without waiting
	// jobs lend their unused shares to contending lanes.
	Weight int
	// MemCapMB caps the sum of estimated working sets of concurrently
	// active jobs in this lane (conservative default; the governor may
	// run below it, never above).
	MemCapMB int64
}

var defaultLaneCaps = map[Lane]laneCap{
	LaneInteractiveIO:     {Waiting: 12, Active: 2, Weight: 4, MemCapMB: 512},
	LaneAudioAnalysis:     {Waiting: 4, Active: 2, CPUHeavy: true, Weight: 2, MemCapMB: 2048},
	LaneScenePrep:         {Waiting: 2, Active: 1, CPUHeavy: true, Weight: 1, MemCapMB: 1024},
	LaneGPUUploadRender:   {Waiting: 2, Active: 1, Weight: 1, MemCapMB: 512},
	LaneExport:            {Waiting: 1, Active: 1, CPUHeavy: true, Weight: 1, MemCapMB: 2048},
	LaneMaintenance:       {Waiting: 4, Active: 1, CPUHeavy: true, Weight: 1, MemCapMB: 256},
	LaneTrustedPrivileged: {Waiting: 1, Active: 1, Weight: 1, MemCapMB: 128},
}

// cpuHeavyBudget derives the shared CPU-heavy token count:
// max(1, min(3, floor(GOMAXPROCS/3))). It deliberately does NOT scale
// with every logical processor: a 22-CPU machine still gets 3
// CPU-heavy tokens so playback, UI and preview keep headroom.
func cpuHeavyBudget(gomaxprocs int) int {
	if gomaxprocs < 1 {
		gomaxprocs = 1
	}
	b := gomaxprocs / 3
	if b > 3 {
		b = 3
	}
	if b < 1 {
		b = 1
	}
	return b
}

// State is the job lifecycle state. Terminal states never transition
// back to running; a retry is a new attempt on the same record, never
// a silent resurrection.
type State string

const (
	StateCreated    State = "created"
	StateValidated  State = "validated"
	StateQueued     State = "queued"
	StateScheduled  State = "scheduled"
	StateRunning    State = "running"
	StatePaused     State = "paused" // reserved; no job type uses it yet
	StateSucceeded  State = "succeeded"
	StateFailed     State = "failed"
	StateCancelled  State = "cancelled"
	StateTimedOut   State = "timed_out"
	StateSuperseded State = "superseded" // coalesced away while waiting
)

// Terminal reports whether s is a terminal state.
func (s State) Terminal() bool {
	switch s {
	case StateSucceeded, StateFailed, StateCancelled, StateTimedOut, StateSuperseded:
		return true
	}
	return false
}

// Outcome is the typed result of every enqueue operation. UI-callable
// enqueue paths are non-blocking and must report one of these; a bare
// error string is never a substitute.
type Outcome string

const (
	OutcomeAccepted             Outcome = "accepted"
	OutcomeCoalesced            Outcome = "coalesced"
	OutcomeRejectedFull         Outcome = "rejected_full"
	OutcomeRejectedShutdown     Outcome = "rejected_shutdown"
	OutcomeRejectedUnauthorized Outcome = "rejected_unauthorized"
	OutcomeRejectedBudget       Outcome = "rejected_budget"
)

// ErrorCategory classifies a job failure for retry decisions and user
// summaries. Only ErrorTransient is retry-eligible.
type ErrorCategory string

const (
	ErrorNone         ErrorCategory = ""
	ErrorTransient    ErrorCategory = "transient"
	ErrorPermanent    ErrorCategory = "permanent"
	ErrorCancelled    ErrorCategory = "cancelled"
	ErrorTimeout      ErrorCategory = "timeout"
	ErrorPanic        ErrorCategory = "panic"
	ErrorUnauthorized ErrorCategory = "unauthorized"
	ErrorInterrupted  ErrorCategory = "interrupted" // journal reconciliation after a crash
)

// TransientError marks a failure as retry-eligible (bounded attempts,
// no backoff loop in this first implementation: each retry is a new
// queued attempt). Everything else — authorization denial, invalid
// input, cancellation, budget denial — is never retried.
type TransientError struct{ Err error }

func (t *TransientError) Error() string { return t.Err.Error() }
func (t *TransientError) Unwrap() error { return t.Err }

// Priority guidance. Aging may raise a waiting job's effective
// priority by one level per full 30 seconds of queue wait, but never
// above PriorityCeiling: background work can never outrank the
// audio-continuity and UI-interaction classes, which live outside this
// scheduler by design.
const (
	PriorityLow     = 1
	PriorityNormal  = 3
	PriorityHigh    = 5
	PriorityCeiling = 6
	agingInterval   = 30 * time.Second
)

// Job is the record of one unit of work. Immutable identity fields are
// safe for concurrent reads; mutable state is guarded by the job's own
// mutex and only transitioned by the scheduler.
type Job struct {
	// Immutable identity.
	ID             uint64
	IdempotencyKey string
	CoalesceKey    string
	Type           string
	Lane           Lane
	ProjectID      string
	SourceFP       string // source input fingerprint (opaque)
	CorrelationID  string

	// Contract flags.
	Cancellable bool
	Pausable    bool // reserved: no job type implements safe pause yet

	// Estimates (admission-time budget checks).
	EstMemMB  int64
	EstDiskMB int64
	Deadline  time.Duration // 0 = no per-job deadline

	// Execution. Run is written only while the job is StateQueued
	// under the scheduler lock (coalescing replaces the payload of a
	// still-waiting job); the dispatch channel handoff to the worker
	// provides the happens-before edge for the read.
	Run       func(ctx context.Context, j *JobHandle) error
	Authorize func() error // TrustedPrivileged lane only
	OnDone    func(j *Job) // optional completion callback

	mu           sync.RWMutex
	state        State
	attempt      int
	retryMax     int
	basePriority int
	seq          uint64 // FIFO tiebreak within a lane
	queuedAt     time.Time
	startedAt    time.Time
	endedAt      time.Time
	stage        string
	itemsDone    uint64
	itemsTotal   uint64
	bytesDone    uint64
	errCategory  ErrorCategory
	errText      string // safe user-facing summary, redacted by the producer
	cancel       context.CancelFunc
	waitingSince time.Time
	ageBoost     int
}

// State returns the current lifecycle state.
func (j *Job) State() State { j.mu.RLock(); defer j.mu.RUnlock(); return j.state }

// Snapshot returns a consistent copy of the mutable record fields.
func (j *Job) Snapshot() JobSnapshot {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return JobSnapshot{
		ID: j.ID, Type: j.Type, Lane: j.Lane, State: j.state,
		Attempt: j.attempt, Stage: j.stage,
		ItemsDone: j.itemsDone, ItemsTotal: j.itemsTotal, BytesDone: j.bytesDone,
		QueuedAt: j.queuedAt, StartedAt: j.startedAt, EndedAt: j.endedAt,
		ErrorCategory: j.errCategory, Error: j.errText,
		CorrelationID: j.CorrelationID, IdempotencyKey: j.IdempotencyKey,
		Cancellable: j.Cancellable,
	}
}

// JobSnapshot is the immutable copy used by the Job Center.
type JobSnapshot struct {
	ID             uint64
	Type           string
	Lane           Lane
	State          State
	Attempt        int
	Stage          string
	ItemsDone      uint64
	ItemsTotal     uint64
	BytesDone      uint64
	QueuedAt       time.Time
	StartedAt      time.Time
	EndedAt        time.Time
	ErrorCategory  ErrorCategory
	Error          string
	CorrelationID  string
	IdempotencyKey string
	Cancellable    bool
}

// QueueWait returns how long the job waited between queue admission
// and start (0 for unstarted jobs).
func (s JobSnapshot) QueueWait() time.Duration {
	if s.StartedAt.IsZero() {
		return 0
	}
	return s.StartedAt.Sub(s.QueuedAt)
}

// RunDur returns the execution duration of the finished attempt.
func (s JobSnapshot) RunDur() time.Duration {
	if s.EndedAt.IsZero() || s.StartedAt.IsZero() {
		return 0
	}
	return s.EndedAt.Sub(s.StartedAt)
}

// JobHandle is handed to Run: the only way a job reports progress.
// Progress is stage-based and monotonic; a regression is dropped, the
// scheduler never fabricates progress, queue position or ETA.
type JobHandle struct{ j *Job }

// ReportProgress records the current stage and monotonic counters.
// total == 0 means "indeterminate" — no percentage is derived from it.
func (h *JobHandle) ReportProgress(stage string, done, total uint64) {
	j := h.j
	j.mu.Lock()
	defer j.mu.Unlock()
	j.stage = stage
	if done >= j.itemsDone { // monotonic; regressions dropped
		j.itemsDone = done
	}
	if total >= j.itemsTotal {
		j.itemsTotal = total
	}
}

// ReportBytes records a monotonic byte counter.
func (h *JobHandle) ReportBytes(done uint64) {
	j := h.j
	j.mu.Lock()
	defer j.mu.Unlock()
	if done >= j.bytesDone {
		j.bytesDone = done
	}
}

// EnqueueResult is the typed admission result.
type EnqueueResult struct {
	Outcome Outcome
	JobID   uint64
	Job     *Job
	// Reason is actionable and user-safe (never a raw internal error).
	Reason string
}

// Accepted reports whether the job entered the system.
func (r EnqueueResult) Accepted() bool { return r.Outcome == OutcomeAccepted }

// WasCoalesced reports whether an existing pending job absorbed this
// request (newest-wins) or an idempotency-key duplicate was folded in.
func (r EnqueueResult) WasCoalesced() bool { return r.Outcome == OutcomeCoalesced }

// Submit describes one unit of work at enqueue time.
type Submit struct {
	Type           string
	Lane           Lane
	Run            func(ctx context.Context, j *JobHandle) error
	IdempotencyKey string
	CoalesceKey    string
	Priority       int
	ProjectID      string
	SourceFP       string
	CorrelationID  string
	EstMemMB       int64
	EstDiskMB      int64
	Deadline       time.Duration
	Cancellable    bool
	// RetryMax bounds total attempts (1 = no retry). Retries apply
	// ONLY to TransientError failures.
	RetryMax  int
	Authorize func() error
	OnDone    func(j *Job)
}

// Config overrides the conservative defaults (tests and explicit,
// separately measured changes only). Zero fields keep the defaults.
// Only downward CPU overrides are honored.
type Config struct {
	CPUBudget    int           // > 0 and below the derived default: reduce tokens
	DiskBudgetMB int64         // 0 = default 8192
	JournalPath  string        // enables the bounded job journal when non-empty
	DrainTimeout time.Duration // bounds graceful shutdown drain (0 = 10s)
}

// laneState is the scheduler-owned per-lane bookkeeping.
type laneState struct {
	cap       laneCap
	waiting   []*Job // FIFO by seq; admission picks by effective priority
	active    int
	activeMem int64
	dispatch  chan *Job // buffered to cap.Active
	deficit   int       // weighted-fair accumulator (CPU-heavy lanes)

	highWait   int
	highActive int

	accepted   uint64
	coalesced  uint64
	rejFull    uint64
	rejOther   uint64
	succeeded  uint64
	failed     uint64
	cancelled  uint64
	timedOut   uint64
	superseded uint64
	waitSumMs  int64
	runSumMs   int64
}

// Metrics is the consistent snapshot for diagnostics.
type Metrics struct {
	CPUBudget      int
	CPUTokensUsed  int
	TokensAside    int // removed from circulation by pressure adaptation
	DiskReservedMB int64
	DiskBudgetMB   int64
	Lanes          map[Lane]LaneMetrics
	Goroutines     int
}

// LaneMetrics are the per-lane counters.
type LaneMetrics struct {
	Waiting        int
	Active         int
	WaitingHigh    int
	ActiveHigh     int
	Accepted       uint64
	Coalesced      uint64
	RejectedFull   uint64
	RejectedOther  uint64
	Succeeded      uint64
	Failed         uint64
	Cancelled      uint64
	TimedOut       uint64
	Superseded     uint64
	SumQueueWaitMs int64
	SumRunMs       int64
}

// Scheduler is the single bounded work orchestrator.
type Scheduler struct {
	mu          sync.Mutex
	lanes       map[Lane]*laneState
	jobsByID    map[uint64]*Job
	jobsByKey   map[string]*Job // idempotency + coalesce index (live jobs only)
	cpuTokens   chan struct{}
	cpuBudget   int
	tokensAside int
	diskCap     int64
	diskUsed    int64
	nextID      uint64
	nextSeq     uint64
	shutdown    bool
	stopped     bool
	stalled     bool // shutdown returned with non-cooperative workers stuck
	wake        chan struct{}
	drain       time.Duration
	journal     *journal
	wg          sync.WaitGroup
}

// New creates the scheduler with the conservative default capacities
// and starts its fixed worker set (9 workers total across lanes) plus
// one scheduler goroutine. No goroutine is ever created per job; the
// OnDone completion callback is the only additional goroutine and it
// is short-lived and recover()ed.
func New(cfg Config) (*Scheduler, error) {
	cpu := cpuHeavyBudget(runtime.GOMAXPROCS(0))
	if cfg.CPUBudget > 0 && cfg.CPUBudget < cpu {
		cpu = cfg.CPUBudget // only downward overrides allowed
	}
	disk := cfg.DiskBudgetMB
	if disk <= 0 {
		disk = 8192
	}
	s := &Scheduler{
		lanes:     map[Lane]*laneState{},
		jobsByID:  map[uint64]*Job{},
		jobsByKey: map[string]*Job{},
		cpuTokens: make(chan struct{}, cpu),
		cpuBudget: cpu,
		diskCap:   disk,
		wake:      make(chan struct{}, 1),
		drain:     cfg.DrainTimeout,
	}
	if s.drain <= 0 {
		s.drain = 10 * time.Second
	}
	for lane, cap := range defaultLaneCaps {
		s.lanes[lane] = &laneState{cap: cap, dispatch: make(chan *Job, cap.Active)}
	}
	if cfg.JournalPath != "" {
		var err error
		s.journal, err = openJournal(cfg.JournalPath)
		if err != nil {
			return nil, fmt.Errorf("jobs: journal: %w", err)
		}
	}
	for _, ls := range s.lanes {
		for i := 0; i < ls.cap.Active; i++ {
			s.wg.Add(1)
			go s.worker(ls)
		}
	}
	s.wg.Add(1)
	go s.loop()
	return s, nil
}

// Enqueue admits one unit of work. It performs only O(1) bounded work
// (no blocking channel sends, no allocations proportional to queue
// depth) and returns a typed outcome, so it is safe to call from the
// UI thread.
func (s *Scheduler) Enqueue(sub Submit) EnqueueResult {
	if sub.Run == nil {
		return EnqueueResult{Outcome: OutcomeRejectedBudget, Reason: "jobs: Run function is required"}
	}
	ls, ok := s.lanes[sub.Lane]
	if !ok {
		return EnqueueResult{Outcome: OutcomeRejectedBudget, Reason: fmt.Sprintf("jobs: unknown lane %q", sub.Lane)}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shutdown || s.stopped {
		return EnqueueResult{Outcome: OutcomeRejectedShutdown, Reason: "jobs: scheduler is shutting down"}
	}
	// Trusted-privileged work requires explicit authorization BEFORE
	// admission; denial is a normal outcome and is never retried here.
	if sub.Lane == LaneTrustedPrivileged {
		if sub.Authorize == nil {
			return EnqueueResult{Outcome: OutcomeRejectedUnauthorized, Reason: "jobs: trusted-privileged work requires an Authorize check"}
		}
		if err := sub.Authorize(); err != nil {
			ls.rejOther++
			return EnqueueResult{Outcome: OutcomeRejectedUnauthorized, Reason: "jobs: authorization denied: " + err.Error()}
		}
	}
	// Budget sanity: an estimate that can never fit is rejected up
	// front with an actionable reason (never a silent queue-stall).
	if sub.EstMemMB > ls.cap.MemCapMB {
		ls.rejOther++
		return EnqueueResult{Outcome: OutcomeRejectedBudget, Reason: fmt.Sprintf(
			"jobs: %s needs a %d MB working set, above this lane's %d MB ceiling — reduce the workload",
			sub.Lane, sub.EstMemMB, ls.cap.MemCapMB)}
	}
	if sub.EstDiskMB > s.diskCap {
		ls.rejOther++
		return EnqueueResult{Outcome: OutcomeRejectedBudget, Reason: fmt.Sprintf(
			"jobs: %s needs %d MB of temporary disk, above the %d MB budget", sub.Lane, sub.EstDiskMB, s.diskCap)}
	}
	// Idempotency: a live job with the same key absorbs this request.
	if sub.IdempotencyKey != "" {
		if prev := s.jobsByKey["idem:"+sub.IdempotencyKey]; prev != nil && !prev.State().Terminal() {
			ls.coalesced++
			return EnqueueResult{Outcome: OutcomeCoalesced, JobID: prev.ID, Job: prev,
				Reason: "jobs: an identical job is already pending or running"}
		}
	}
	// Coalescing: newest pending state per key wins; a running job is
	// never preempted — the newer payload replaces the still-waiting
	// one and keeps its queue position.
	if sub.CoalesceKey != "" {
		if prev := s.jobsByKey["co:"+sub.CoalesceKey]; prev != nil && prev.State() == StateQueued {
			prev.mu.Lock()
			prev.Run, prev.OnDone = sub.Run, sub.OnDone
			prev.mu.Unlock()
			ls.coalesced++
			s.journal.record(prev, "coalesced")
			return EnqueueResult{Outcome: OutcomeCoalesced, JobID: prev.ID, Job: prev,
				Reason: "jobs: replaced the older pending update for this key (newest wins)"}
		}
	}
	if len(ls.waiting) >= ls.cap.Waiting {
		ls.rejFull++
		return EnqueueResult{Outcome: OutcomeRejectedFull, Reason: fmt.Sprintf(
			"jobs: the %s queue is full (%d waiting) — cancel a running job, wait, or retry later",
			sub.Lane, ls.cap.Waiting)}
	}
	s.nextID++
	s.nextSeq++
	now := time.Now()
	prio := sub.Priority
	if prio < 0 {
		prio = 0
	}
	if prio > PriorityCeiling {
		prio = PriorityCeiling
	}
	j := &Job{
		ID: s.nextID, IdempotencyKey: sub.IdempotencyKey, CoalesceKey: sub.CoalesceKey,
		Type: sub.Type, Lane: sub.Lane, ProjectID: sub.ProjectID, SourceFP: sub.SourceFP,
		CorrelationID: sub.CorrelationID, Cancellable: sub.Cancellable,
		EstMemMB: sub.EstMemMB, EstDiskMB: sub.EstDiskMB, Deadline: sub.Deadline,
		Run: sub.Run, Authorize: sub.Authorize, OnDone: sub.OnDone,
		retryMax: sub.RetryMax, state: StateQueued, attempt: 1,
		basePriority: prio, seq: s.nextSeq, queuedAt: now, waitingSince: now,
	}
	if j.retryMax < 1 {
		j.retryMax = 1
	}
	s.jobsByID[j.ID] = j
	if sub.IdempotencyKey != "" {
		s.jobsByKey["idem:"+sub.IdempotencyKey] = j
	}
	if sub.CoalesceKey != "" {
		s.jobsByKey["co:"+sub.CoalesceKey] = j
	}
	ls.waiting = append(ls.waiting, j)
	if len(ls.waiting) > ls.highWait {
		ls.highWait = len(ls.waiting)
	}
	ls.accepted++
	s.journal.record(j, "queued")
	s.signal()
	return EnqueueResult{Outcome: OutcomeAccepted, JobID: j.ID, Job: j}
}

// Cancel cancels a job: a waiting job leaves the queue immediately; a
// scheduled job is marked so the worker skips it; a running job gets a
// cooperative context cancellation. Idempotent — cancelling a job that
// is already cancelled returns true; cancelling any other terminal job
// is a no-op returning false, never an error.
func (s *Scheduler) Cancel(id uint64) bool {
	s.mu.Lock()
	j, ok := s.jobsByID[id]
	if !ok {
		s.mu.Unlock()
		return false
	}
	st := j.State()
	if st.Terminal() {
		s.mu.Unlock()
		return st == StateCancelled // idempotent double-cancel
	}
	switch st {
	case StateQueued:
		s.removeWaitingLocked(j)
		s.finalizeCancelLocked(j)
		s.mu.Unlock()
		s.signal()
		return true
	case StateScheduled:
		// In dispatch, not yet on a worker: mark terminal; the worker
		// observes it and skips execution, releasing reservations.
		s.finalizeCancelLocked(j)
		s.mu.Unlock()
		return true
	default: // running
		j.mu.RLock()
		cancel := j.cancel
		j.mu.RUnlock()
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		return true
	}
}

// finalizeCancelLocked marks j cancelled and releases its indexes
// (caller holds s.mu; job is waiting or scheduled, so no worker
// reservations were taken yet or the worker will release them).
func (s *Scheduler) finalizeCancelLocked(j *Job) {
	j.mu.Lock()
	j.state = StateCancelled
	j.endedAt = time.Now()
	j.errCategory = ErrorCancelled
	j.mu.Unlock()
	s.lanes[j.Lane].cancelled++
	s.cleanupKeysLocked(j)
	s.journal.record(j, "cancelled")
}

// CancelAll cancels every live job (shutdown path).
func (s *Scheduler) CancelAll() {
	s.mu.Lock()
	ids := make([]uint64, 0, len(s.jobsByID))
	for id, j := range s.jobsByID {
		if !j.State().Terminal() {
			ids = append(ids, id)
		}
	}
	s.mu.Unlock()
	for _, id := range ids {
		s.Cancel(id)
	}
}

// SetPressure adapts the CPU-heavy budget DOWN under measured system
// pressure (audio underruns, UI frame-time rise, memory pressure).
// level 0 = normal; each level above 0 sets one token aside (floor 1
// token). Recovery is the caller's responsibility: raise the level
// immediately on pressure, lower it gradually (hysteresis) — the
// scheduler itself never oscillates. It never raises the budget above
// the configured ceiling.
func (s *Scheduler) SetPressure(level int) {
	if level < 0 {
		level = 0
	}
	if level > s.cpuBudget-1 {
		level = s.cpuBudget - 1
	}
	s.mu.Lock()
	s.tokensAside = level
	s.mu.Unlock()
	s.signal()
}

// Stalled reports whether a previous Shutdown returned while a
// non-cooperative job was still stuck on a worker (observable
// diagnostic; the process exit path owns the final reap).
func (s *Scheduler) Stalled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stalled
}

// Snapshot returns a consistent metrics snapshot for diagnostics.
func (s *Scheduler) Snapshot() Metrics {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := Metrics{
		CPUBudget: s.cpuBudget, CPUTokensUsed: len(s.cpuTokens), TokensAside: s.tokensAside,
		DiskBudgetMB: s.diskCap, DiskReservedMB: s.diskUsed,
		Lanes: map[Lane]LaneMetrics{}, Goroutines: runtime.NumGoroutine(),
	}
	for lane, ls := range s.lanes {
		m.Lanes[lane] = LaneMetrics{
			Waiting: len(ls.waiting), Active: ls.active,
			WaitingHigh: ls.highWait, ActiveHigh: ls.highActive,
			Accepted: ls.accepted, Coalesced: ls.coalesced,
			RejectedFull: ls.rejFull, RejectedOther: ls.rejOther,
			Succeeded: ls.succeeded, Failed: ls.failed, Cancelled: ls.cancelled,
			TimedOut: ls.timedOut, Superseded: ls.superseded,
			SumQueueWaitMs: ls.waitSumMs, SumRunMs: ls.runSumMs,
		}
	}
	return m
}

// LiveJobs returns snapshots of all non-terminal jobs (Job Center).
func (s *Scheduler) LiveJobs() []JobSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]JobSnapshot, 0, 8)
	for _, j := range s.jobsByID {
		if !j.State().Terminal() {
			out = append(out, j.Snapshot())
		}
	}
	return out
}

// liveCountLocked counts non-terminal jobs.
func (s *Scheduler) liveCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, j := range s.jobsByID {
		if !j.State().Terminal() {
			n++
		}
	}
	return n
}

// Shutdown stops accepting new work and drains gracefully: live jobs
// get the drain deadline to finish; stragglers are cancelled at the
// deadline; workers and the scheduler goroutine exit within a further
// hard bound, after which Shutdown returns even if a non-cooperative
// job is still stuck (that stall is observable via LiveJobs; the
// process exit path owns the final reap — Shutdown never blocks
// forever). Idempotent.
func (s *Scheduler) Shutdown(deadline time.Duration) {
	s.mu.Lock()
	if s.shutdown {
		s.mu.Unlock()
		return
	}
	s.shutdown = true
	s.mu.Unlock()
	if deadline <= 0 {
		deadline = s.drain
	}
	s.signal()

	// Bounded graceful drain.
	end := time.Now().Add(deadline)
	for time.Now().Before(end) && s.liveCount() > 0 {
		time.Sleep(20 * time.Millisecond)
	}
	// Deadline reached: cancel everything still live (cooperative).
	s.CancelAll()
	s.signal()
	hardEnd := time.Now().Add(5 * time.Second)
	for time.Now().Before(hardEnd) && s.liveCount() > 0 {
		time.Sleep(10 * time.Millisecond)
	}
	// Stop the workers (idempotent, guarded).
	s.mu.Lock()
	if !s.stopped {
		s.stopped = true
		for _, ls := range s.lanes {
			close(ls.dispatch)
		}
	}
	s.mu.Unlock()
	s.signal()
	// Bounded worker-exit wait: a job that ignores cooperative
	// cancellation cannot block Shutdown forever. If workers are still
	// stuck past the bound, Shutdown returns with the stall observable
	// through LiveJobs/Snapshot — the process exit path owns the final
	// reap. This is the documented behavior; it never pretends the
	// drain succeeded.
	wgDone := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(wgDone)
	}()
	select {
	case <-wgDone:
	case <-time.After(5 * time.Second):
		s.mu.Lock()
		s.stalled = true
		s.mu.Unlock()
	}
	if s.journal != nil {
		s.journal.close()
	}
}

// signal wakes the scheduler goroutine (never blocks: capacity 1).
func (s *Scheduler) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// loop is the single scheduler/admission authority plus the aging
// ticker (one goroutine, one wakeup per second at most). It exits when
// shutdown has been observed and no live jobs remain.
func (s *Scheduler) loop() {
	defer s.wg.Done()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-s.wake:
		case <-tick.C:
		}
		s.mu.Lock()
		shutdown := s.shutdown
		s.admitLocked()
		s.mu.Unlock()
		if shutdown {
			if s.liveCount() == 0 {
				return
			}
		}
	}
}

// ageWaiting applies monotonic aging: one effective priority level per
// full agingInterval of queue wait, capped at PriorityCeiling.
func (s *Scheduler) ageWaiting() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	changed := false
	for _, ls := range s.lanes {
		for _, j := range ls.waiting {
			j.mu.Lock()
			age := int(now.Sub(j.waitingSince) / agingInterval)
			if age > j.ageBoost {
				j.ageBoost = age
				changed = true
			}
			j.mu.Unlock()
		}
	}
	if changed {
		s.admitLocked()
	}
}

// effPriorityLocked computes the effective priority (base + aging,
// capped at the ceiling so background work can never outrank the
// audio/UI classes that live outside this scheduler).
func effPriorityLocked(j *Job) int {
	j.mu.RLock()
	defer j.mu.RUnlock()
	p := j.basePriority + j.ageBoost
	if p > PriorityCeiling {
		p = PriorityCeiling
	}
	return p
}

// admitLocked admits waiting jobs while lane workers, CPU tokens and
// memory/disk budgets allow. CPU-heavy lanes share tokens through
// weighted deficit round robin; unused shares are borrowable.
func (s *Scheduler) admitLocked() {
	progress := true
	for progress {
		progress = false
		// Non-token lanes first (interactive I/O, the single GPU
		// submission owner, the trusted helper): they contend only on
		// their own workers and the memory/disk budgets. Admission is
		// peek-then-commit: a job that does not fit the memory/disk
		// budget STAYS in the waiting list (no lost jobs).
		for _, lane := range []Lane{LaneInteractiveIO, LaneGPUUploadRender, LaneTrustedPrivileged} {
			ls := s.lanes[lane]
			for ls.active < ls.cap.Active {
				j := s.pickPeekLocked(ls)
				if j == nil {
					break
				}
				if !s.memFitsLocked(ls, j) {
					break // wait for active memory/disk to release
				}
				s.removeWaitingJobLocked(ls, j)
				s.startLocked(ls, j)
				progress = true
			}
		}
		// Token-bound lanes: weighted fair share via deficit.
		if s.tokenFreeLocked() > 0 {
			if j := s.pickTokenJobLocked(); j != nil {
				ls := s.lanes[j.Lane]
				if s.memFitsLocked(ls, j) {
					s.removeWaitingJobLocked(ls, j)
					s.cpuTokens <- struct{}{}
					if ls.deficit > 0 {
						ls.deficit -= 1
					}
					s.startLocked(ls, j)
					progress = true
				}
			}
		}
	}
}

// tokenFreeLocked reports free CPU-heavy tokens (capacity minus held
// minus pressure-set-aside).
func (s *Scheduler) tokenFreeLocked() int {
	free := s.cpuBudget - len(s.cpuTokens) - s.tokensAside
	if free < 0 {
		return 0
	}
	return free
}

// pickTokenJobLocked accrues weights to contending CPU-heavy lanes and
// returns (WITHOUT removing) the waiting job that most deserves the
// next token (weighted round robin with borrowing: lanes without
// waiting jobs accrue nothing and lend their share). Effective
// priority dominates; the deficit breaks ties, then FIFO sequence.
// The caller removes the job and charges the lane's deficit only
// after the memory/disk budget check passes, so a job that cannot be
// admitted right now is never lost.
func (s *Scheduler) pickTokenJobLocked() *Job {
	order := []Lane{LaneAudioAnalysis, LaneScenePrep, LaneExport, LaneMaintenance}
	for _, lane := range order {
		ls := s.lanes[lane]
		if ls.cap.CPUHeavy && len(ls.waiting) > 0 && ls.active < ls.cap.Active {
			ls.deficit += ls.cap.Weight
		}
	}
	var best *Job
	bestScore := -1
	for _, lane := range order {
		ls := s.lanes[lane]
		if !ls.cap.CPUHeavy || len(ls.waiting) == 0 || ls.active >= ls.cap.Active {
			continue
		}
		j := s.pickPeekLocked(ls)
		if j == nil {
			continue
		}
		score := effPriorityLocked(j)*10000 + ls.deficit
		if score > bestScore {
			bestScore, best = score, j
		}
	}
	return best
}

// pickPeekLocked returns the waiting job admission would pick without
// removing it.
func (s *Scheduler) pickPeekLocked(ls *laneState) *Job {
	best := -1
	bp := -1
	for i, j := range ls.waiting {
		p := effPriorityLocked(j)
		if p > bp || (p == bp && best >= 0 && j.seq < ls.waiting[best].seq) {
			bp, best = p, i
		}
	}
	if best < 0 {
		return nil
	}
	return ls.waiting[best]
}

// pickWaitingLocked is retained for tests: it selects and removes the
// highest-effective-priority waiting job (FIFO among equals).
func (s *Scheduler) pickWaitingLocked(ls *laneState) *Job {
	j := s.pickPeekLocked(ls)
	if j != nil {
		s.removeWaitingJobLocked(ls, j)
	}
	return j
}

func (s *Scheduler) removeWaitingJobLocked(ls *laneState, j *Job) {
	for i, w := range ls.waiting {
		if w == j {
			ls.waiting = append(ls.waiting[:i], ls.waiting[i+1:]...)
			return
		}
	}
}

// memFitsLocked checks the lane memory ceiling and the global
// temporary-disk reservation for admitting j now.
func (s *Scheduler) memFitsLocked(ls *laneState, j *Job) bool {
	if ls.activeMem+j.EstMemMB > ls.cap.MemCapMB {
		return false
	}
	if s.diskUsed+j.EstDiskMB > s.diskCap {
		return false
	}
	return true
}

// startLocked moves a job from waiting to dispatch. The dispatch
// buffer equals the lane's active cap and active is incremented before
// the send, so the send can never block (invariant
// len(dispatch) <= active <= cap).
func (s *Scheduler) startLocked(ls *laneState, j *Job) {
	j.mu.Lock()
	j.state = StateScheduled
	j.mu.Unlock()
	ls.active++
	ls.activeMem += j.EstMemMB
	s.diskUsed += j.EstDiskMB
	if ls.active > ls.highActive {
		ls.highActive = ls.active
	}
	ls.dispatch <- j
}

// removeWaitingLocked drops j from its lane's waiting list.
func (s *Scheduler) removeWaitingLocked(j *Job) {
	s.removeWaitingJobLocked(s.lanes[j.Lane], j)
}

// cleanupKeysLocked releases the idempotency/coalesce index entry.
func (s *Scheduler) cleanupKeysLocked(j *Job) {
	if j.IdempotencyKey != "" && s.jobsByKey["idem:"+j.IdempotencyKey] == j {
		delete(s.jobsByKey, "idem:"+j.IdempotencyKey)
	}
	if j.CoalesceKey != "" && s.jobsByKey["co:"+j.CoalesceKey] == j {
		delete(s.jobsByKey, "co:"+j.CoalesceKey)
	}
}

// worker is one fixed lane worker. It recovers job panics at this
// boundary (recorded, contained, one job's failure never kills the
// worker, lane or app) and always returns to service until the
// dispatch channel is closed.
func (s *Scheduler) worker(ls *laneState) {
	defer s.wg.Done()
	for j := range ls.dispatch {
		if !j.State().Terminal() {
			s.runJob(j)
		}
		s.mu.Lock()
		s.jobDoneLocked(ls, j)
		s.mu.Unlock()
		s.signal()
	}
}

// runJob executes one attempt with panic containment, deadline
// enforcement and cooperative cancellation.
func (s *Scheduler) runJob(j *Job) {
	j.mu.Lock()
	j.state = StateRunning
	j.startedAt = time.Now()
	var ctx context.Context
	if j.Deadline > 0 {
		ctx, j.cancel = context.WithTimeout(context.Background(), j.Deadline)
	} else {
		ctx, j.cancel = context.WithCancel(context.Background())
	}
	j.mu.Unlock()
	s.journal.record(j, "running")

	h := &JobHandle{j: j}
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("panic in %s job: %v", j.Type, r)
				j.mu.Lock()
				j.errCategory = ErrorPanic
				j.mu.Unlock()
			}
		}()
		err = j.Run(ctx, h)
	}()
	j.mu.RLock()
	cancel := j.cancel
	j.mu.RUnlock()
	if cancel != nil {
		cancel() // release context resources promptly
	}

	j.mu.Lock()
	j.endedAt = time.Now()
	cat := ErrorPermanent
	switch {
	case err == nil:
		cat = ErrorNone
	case j.errCategory == ErrorPanic:
		cat = ErrorPanic
	case errors.Is(err, context.Canceled):
		cat = ErrorCancelled
	case errors.Is(err, context.DeadlineExceeded):
		cat = ErrorTimeout
	case isTransient(err):
		cat = ErrorTransient
	}
	j.errCategory = cat
	if err != nil {
		j.errText = err.Error()
	}
	j.mu.Unlock()
}

// jobDoneLocked finalizes a finished attempt: reservations are
// released on EVERY path (success, error, panic, timeout, cancellation,
// skip), the terminal state is recorded (or a classified transient
// failure becomes a NEW queued attempt — never a resurrection), and
// index entries are freed. Caller holds s.mu.
func (s *Scheduler) jobDoneLocked(ls *laneState, j *Job) {
	j.mu.RLock()
	state, cat, attempt, retryMax := j.state, j.errCategory, j.attempt, j.retryMax
	startedAt, queuedAt := j.startedAt, j.queuedAt
	j.mu.RUnlock()

	ls.active--
	ls.activeMem -= j.EstMemMB
	s.diskUsed -= j.EstDiskMB
	if ls.cap.CPUHeavy {
		<-s.cpuTokens
	}
	if !startedAt.IsZero() {
		ls.runSumMs += time.Since(startedAt).Milliseconds()
		ls.waitSumMs += startedAt.Sub(queuedAt).Milliseconds()
	}

	if state == StateCancelled {
		// Cancelled while waiting/scheduled: terminal already recorded
		// by Cancel; nothing further to do here.
		s.cleanupKeysLocked(j)
		return
	}
	switch cat {
	case ErrorNone:
		j.mu.Lock()
		j.state = StateSucceeded
		j.mu.Unlock()
		ls.succeeded++
	case ErrorCancelled:
		j.mu.Lock()
		j.state = StateCancelled
		j.mu.Unlock()
		ls.cancelled++
	case ErrorTimeout:
		j.mu.Lock()
		j.state = StateTimedOut
		j.mu.Unlock()
		ls.timedOut++
	default:
		// Retry ONLY classified transient failures, bounded by the
		// attempt budget; the retry is a new attempt on the same
		// record with a fresh queue-wait clock.
		if cat == ErrorTransient && attempt < retryMax {
			j.mu.Lock()
			j.attempt = attempt + 1
			j.state = StateQueued
			j.waitingSince = time.Now()
			j.ageBoost = 0
			j.errCategory, j.errText = ErrorNone, ""
			j.mu.Unlock()
			ls.waiting = append(ls.waiting, j)
			s.journal.record(j, "retry")
			return
		}
		j.mu.Lock()
		j.state = StateFailed
		j.mu.Unlock()
		ls.failed++
	}
	s.journal.record(j, "terminal")
	s.cleanupKeysLocked(j)
	s.gcLocked()

	j.mu.RLock()
	onDone := j.OnDone
	j.mu.RUnlock()
	if onDone != nil {
		go func() {
			defer func() { _ = recover() }()
			onDone(j)
		}()
	}
}

// gcLocked bounds memory: terminal records are dropped once more than
// 512 accumulate (the Job Center keeps its own bounded ring from
// metrics and snapshots; the journal keeps durable history).
func (s *Scheduler) gcLocked() {
	if len(s.jobsByID) <= 512 {
		return
	}
	for id, j := range s.jobsByID {
		if j.State().Terminal() {
			delete(s.jobsByID, id)
		}
		if len(s.jobsByID) <= 512 {
			return
		}
	}
}

func isTransient(err error) bool {
	var t *TransientError
	return errors.As(err, &t)
}
