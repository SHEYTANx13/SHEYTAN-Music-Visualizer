// Package appclose owns the ONE authoritative close/shutdown state
// machine for the application. Every close surface — titlebar close
// (via the Fyne close intercept), menu Quit, keyboard shortcut and any
// future OS termination path — enters through RequestClose; teardown
// runs exactly once in dependency order: cancel active work, flush
// state, release resources, finalize (explicit Window.Close).
//
// Contract (run 43090342995 shutdown-lifecycle requirements, hardened
// in run 43541087097):
//
//   - States are explicit and transitions idempotent:
//     running → closeRequested → (confirming) → cancelling → flushing
//     → closing → closed. A cancelled confirmation returns to running.
//   - A second RequestClose while a confirmation is open never creates a
//     second dialog; requests during cancelling/flushing/closing/closed
//     are recorded and ignored (duplicate close events are normal).
//   - Confirmation callbacks are generation-validated and once-only: a
//     stale proceed() from a superseded or cancelled confirmation can
//     never start teardown, and double-invoking proceed() runs the
//     teardown exactly once.
//   - Close-request admission, the work snapshot and the transition into
//     confirming/cancelling happen under one lock hold, so a concurrent
//     no-work close can never start teardown while another caller's
//     confirmation dialog is opening.
//   - The caller stays responsive: teardown work runs on provided
//     goroutines; nothing here sleeps unboundedly — every stage is
//     bounded by the configured deadline and its outcome is reported.
//   - A close request while active work runs (playback, export, unsaved
//     project) first offers exactly one confirmation surface; the user
//     can stay in the app. Confirm-without-work closes directly.
//   - The window is finalized (explicit Close) ONLY after cleanup
//     completed or the deadline elapsed — never forgotten, never twice.
//   - Flush errors are preserved as evidence (ShutdownResult) and never
//     prevent the window from closing.
//   - Duration portability (run 43541087097 Windows regression): the
//     monotonic clock on some platforms (notably Windows VMs) can
//     measure a sub-resolution teardown as zero. The public contract
//     promises Duration > 0 and a non-zero CompletedAt for every
//     COMPLETED sequence, so a non-positive measured span is normalized
//     to MinCompletedDuration without sleeping. The complete immutable
//     result is published BEFORE Done is closed, so Result() after
//     <-Done() always observes the final values.
//
// The controller is framework-agnostic: the UI layer injects hooks, so
// the state machine itself is unit-testable without a window.
package appclose

import (
	"fmt"
	"sync"
	"time"
)

// MinCompletedDuration is the documented floor for the Duration of a
// completed shutdown sequence. Some platforms observe a sub-resolution
// teardown as a zero elapsed span (the Windows run-43541087097
// regression: TestSingleConfirmationSurfaceForDuplicates failed with
// "duration not recorded" because the whole teardown completed inside
// one monotonic clock tick). A completed sequence always reports at
// least MinCompletedDuration; the value is a contract token, not a
// measured delay — no sleeping is ever involved.
const MinCompletedDuration = 1 * time.Nanosecond

// State names the shutdown machine's explicit states.
type State string

const (
	StateRunning        State = "running"
	StateCloseRequested State = "closeRequested"
	StateConfirming     State = "confirming"
	StateCancelling     State = "cancelling"
	StateFlushing       State = "flushing"
	StateClosing        State = "closing"
	StateClosed         State = "closed"
)

// WorkReport is the honest snapshot of active work that must be either
// cancelled or preserved before teardown. Snapshot must be cheap and
// non-blocking; it is taken while the controller lock is held, so it
// must never call back into the controller.
type WorkReport struct {
	Playback       bool // audio transport is playing
	Export         bool // an export job is running
	UnsavedProject bool // unsaved project/config changes exist
	Startup        bool // startup probes are still running
}

// Any reports whether confirmation-worthy work is active.
func (w WorkReport) Any() bool {
	return w.Playback || w.Export || w.UnsavedProject || w.Startup
}

// Describe renders the report for the confirmation surface.
func (w WorkReport) Describe() string {
	s := ""
	add := func(label string) {
		if s != "" {
			s += ", "
		}
		s += label
	}
	if w.Playback {
		add("playback will stop")
	}
	if w.Export {
		add("the active export will be cancelled")
	}
	if w.UnsavedProject {
		add("unsaved project settings will be saved automatically")
	}
	if w.Startup {
		add("startup probes will be cancelled")
	}
	if s == "" {
		return "no active work"
	}
	return s
}

// Hooks are the app-owned teardown stages. Each runs at most once per
// close sequence.
type Hooks struct {
	// Snapshot reports the active-work state (non-blocking; called
	// with the controller lock held — never call the controller
	// back from here).
	Snapshot func() WorkReport
	// Confirm shows ONE confirmation surface for the report. It must
	// eventually call exactly one of proceed/stay (the machine guards
	// duplicates itself). It runs on the caller's (UI) thread.
	Confirm func(report WorkReport, proceed func())
	// CancelWork cancels playback/export/startup probes. Bounded.
	CancelWork func() error
	// Flush persists config/project/log state. Bounded. Errors are
	// evidence, not blockers.
	Flush func() error
	// Release stops the render loop, renderer, watchers and child
	// processes. Bounded.
	Release func() error
	// Finalize closes the window (the UI layer wraps Window.Close in
	// whatever thread discipline the framework requires).
	Finalize func()
}

// ShutdownResult is the evidence record of one shutdown sequence.
type ShutdownResult struct {
	States      []State       // the transition trail (ordered, deduplicated)
	CancelErr   error         // non-nil when cancelling work failed
	FlushErr    error         // non-nil when flushing failed (evidence, kept)
	ReleaseErr  error         // non-nil when releasing resources failed
	TimedOut    bool          // a stage exceeded its deadline slice
	CompletedAt time.Time     // when the window was finalized (never the zero time)
	Duration    time.Duration // > 0 for every completed sequence (see MinCompletedDuration)
}

// Controller is the authoritative close state machine. It is safe for
// concurrent use; RequestClose is expected from UI threads.
type Controller struct {
	mu         sync.Mutex
	state      State
	confirmD   bool   // a confirmation surface is currently open
	confirmGen uint64 // generation token invalidating stale proceed callbacks
	states     []State
	clock      func() time.Time
	log        func(msg string, args ...any)
	hooks      Hooks
	deadline   time.Duration

	doneOnce sync.Once
	done     chan struct{}
	result   ShutdownResult
}

// New creates a controller starting in StateRunning. deadline bounds
// EACH bounded stage (cancel/flush/release slice); a non-positive
// deadline defaults to 10s.
func New(hooks Hooks, deadline time.Duration, log func(msg string, args ...any)) *Controller {
	return newController(hooks, deadline, log, time.Now)
}

// newController is the injectable-clock constructor used by New and by
// the deterministic duration tests (run 43541087097: a fixed clock
// reproduces the Windows sub-resolution teardown without any sleeping).
func newController(hooks Hooks, deadline time.Duration, log func(msg string, args ...any), clock func() time.Time) *Controller {
	if deadline <= 0 {
		deadline = 10 * time.Second
	}
	if log == nil {
		log = func(string, ...any) {}
	}
	if clock == nil {
		clock = time.Now
	}
	c := &Controller{
		state:    StateRunning,
		states:   []State{StateRunning},
		clock:    clock,
		log:      log,
		hooks:    hooks,
		deadline: deadline,
		done:     make(chan struct{}),
	}
	return c
}

// State returns the current machine state.
func (c *Controller) State() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// Done is closed when the window has been finalized (State closed).
func (c *Controller) Done() <-chan struct{} { return c.done }

// Result returns the shutdown evidence after Done is closed.
func (c *Controller) Result() ShutdownResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.result
}

func (c *Controller) transition(to State) {
	c.mu.Lock()
	if c.state != to {
		c.state = to
		c.states = append(c.states, to)
	}
	c.mu.Unlock()
	c.log("shutdown state", "state", string(to))
}

// RequestClose is the ONE entry point for every close surface.
// Idempotent: duplicate requests never duplicate dialogs or teardown.
// from is a diagnostic label (e.g. "titlebar", "menu-quit", "esc").
func (c *Controller) RequestClose(from string) {
	c.mu.Lock()
	switch c.state {
	case StateClosed, StateCancelling, StateFlushing, StateClosing:
		// Teardown already in progress: duplicate close events are
		// recorded and ignored, never re-run.
		c.log("duplicate close request ignored", "from", from, "state", string(c.state))
		c.mu.Unlock()
		return
	case StateConfirming:
		// A confirmation is open (or its callback is still in
		// flight): never stack dialogs and never start a parallel
		// teardown — the pending confirmation owns the decision.
		c.log("close request while confirm open; dialog kept single", "from", from)
		c.mu.Unlock()
		return
	}
	// Running or CloseRequested: record the request atomically with
	// the work snapshot, so a racing pair of requests can never
	// interleave into "one opens a dialog while the other starts
	// teardown".
	if c.state == StateRunning {
		c.state = StateCloseRequested
		c.states = append(c.states, StateCloseRequested)
	}
	report := WorkReport{}
	if c.hooks.Snapshot != nil {
		report = c.hooks.Snapshot()
	}
	if report.Any() {
		c.state = StateConfirming
		c.states = append(c.states, StateConfirming)
		c.confirmD = true
		c.confirmGen++
		gen := c.confirmGen
		c.mu.Unlock()
		c.log("shutdown state", "state", string(StateConfirming))
		proceed := c.newProceed(from, gen)
		if c.hooks.Confirm != nil {
			c.hooks.Confirm(report, proceed)
		} else {
			proceed()
		}
		return
	}
	c.mu.Unlock()
	go c.runTeardown(from)
}

// newProceed builds the once-only, generation-validated proceed
// callback handed to the Confirm hook. The callback is honoured only
// while it is still the CURRENT open confirmation; a stale callback
// (the user chose Stay, or a newer confirmation superseded it) is
// logged and ignored. When honoured, the Confirming→Cancelling
// transition happens under the same lock hold that validated the
// generation, so a concurrent CancelClose either wins (the callback
// then sees the bumped generation and stays harmless) or loses (it
// finds a state that is no longer confirming and becomes a no-op).
func (c *Controller) newProceed(from string, gen uint64) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			c.mu.Lock()
			if c.confirmGen != gen || c.state != StateConfirming || !c.confirmD {
				c.mu.Unlock()
				c.log("stale proceed callback ignored", "from", from, "state", string(c.state))
				return
			}
			c.confirmD = false
			c.state = StateCancelling
			c.states = append(c.states, StateCancelling)
			c.mu.Unlock()
			c.log("shutdown state", "state", string(StateCancelling))
			go c.teardown(from)
		})
	}
}

// CancelClose returns from an open confirmation to running (the user
// chose to stay). Idempotent. Bumping confirmGen invalidates any
// proceed callback the closing dialog may still invoke afterwards.
func (c *Controller) CancelClose(reason string) {
	c.mu.Lock()
	if c.state != StateConfirming {
		c.mu.Unlock()
		return
	}
	c.confirmD = false
	c.confirmGen++
	c.state = StateRunning
	c.states = append(c.states, StateRunning)
	c.mu.Unlock()
	c.log("close cancelled by user", "reason", reason)
}

// runTeardown is the direct (confirmation-free) teardown admission:
// exactly one caller atomically moves the machine into cancelling and
// runs the stages. Entry from confirming is impossible here — that
// path is reserved to the generation-validated proceed callback.
func (c *Controller) runTeardown(from string) {
	c.mu.Lock()
	switch c.state {
	case StateRunning, StateCloseRequested:
		c.state = StateCancelling
		c.states = append(c.states, StateCancelling)
		c.mu.Unlock()
		c.log("shutdown state", "state", string(StateCancelling))
		c.teardown(from)
	default:
		c.log("duplicate teardown request ignored", "from", from, "state", string(c.state))
		c.mu.Unlock()
	}
}

// teardown runs the bounded cancel → flush → release stages and the
// single Finalize, then publishes the immutable result BEFORE closing
// Done. Only reachable through an admitted (exactly-once) transition
// into cancelling.
func (c *Controller) teardown(from string) {
	start := c.clock()
	res := ShutdownResult{}
	var timedOut bool

	// Cancel active work (bounded).
	if c.hooks.CancelWork != nil {
		res.CancelErr, timedOut = bounded(c.deadline, "cancel", c.hooks.CancelWork, c.log)
		res.TimedOut = res.TimedOut || timedOut
	}
	c.transition(StateFlushing)

	// Flush state (bounded; errors are evidence only).
	if c.hooks.Flush != nil {
		res.FlushErr, timedOut = bounded(c.deadline, "flush", c.hooks.Flush, c.log)
		res.TimedOut = res.TimedOut || timedOut
	}
	c.transition(StateClosing)

	// Release renderer/watchers/children (bounded).
	if c.hooks.Release != nil {
		res.ReleaseErr, timedOut = bounded(c.deadline, "release", c.hooks.Release, c.log)
		res.TimedOut = res.TimedOut || timedOut
	}

	// Finalize: the explicit Window.Close() — required because the close
	// intercept replaced the automatic close. Exactly once.
	if c.hooks.Finalize != nil {
		c.hooks.Finalize()
	}

	end := c.clock()
	res.Duration = end.Sub(start)
	if res.Duration <= 0 {
		// Sub-resolution teardown on a coarse monotonic clock (the
		// Windows run-43541087097 regression): a completed sequence
		// still owes the contract a positive duration. Normalized,
		// never slept into existence.
		res.Duration = MinCompletedDuration
	}
	res.CompletedAt = end

	c.mu.Lock()
	c.states = append(c.states, StateClosed)
	res.States = append([]State(nil), c.states...)
	c.result = res
	c.state = StateClosed
	c.mu.Unlock()
	// Publish-before-signal: Result() after <-Done() observes the
	// complete final record (happens-before via the channel close).
	c.doneOnce.Do(func() { close(c.done) })
	c.log("shutdown complete",
		"from", from,
		"cancel_err", fmtErr(res.CancelErr),
		"flush_err", fmtErr(res.FlushErr),
		"release_err", fmtErr(res.ReleaseErr),
		"timed_out", res.TimedOut,
		"duration_ms", res.Duration.Milliseconds())
}

// bounded runs fn with a hard stage deadline. fn is NOT interrupted
// mid-call (Go cannot do that safely); a stage that exceeds the
// deadline is recorded as evidence and the sequence proceeds — the
// remaining stages still run and the app process exit stays bounded.
// This is honest degradation, never a silent hang.
func bounded(d time.Duration, stage string, fn func() error, log func(string, ...any)) (err error, timedOut bool) {
	done := make(chan error, 1)
	go func() { done <- fn() }()
	select {
	case err = <-done:
		return err, false
	case <-time.After(d):
		log("shutdown stage exceeded deadline", "stage", stage, "deadline", d.String())
		return fmt.Errorf("stage %s exceeded deadline %s", stage, d), true
	}
}

func fmtErr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
