// Package appclose owns the ONE authoritative close/shutdown state
// machine for the application. Every close surface — titlebar close
// (via the Fyne close intercept), menu Quit, keyboard shortcut and any
// future OS termination path — enters through RequestClose; teardown
// runs exactly once in dependency order: cancel active work, flush
// state, release resources, finalize (explicit Window.Close).
//
// Contract (run 43090342995 shutdown-lifecycle requirements):
//
//   - States are explicit and transitions idempotent:
//     running → closeRequested → (confirming) → cancelling → flushing
//     → closing → closed.
//   - A second RequestClose while a confirmation is open never creates a
//     second dialog; requests during cancelling/flushing/closing/closed
//     are recorded and ignored (duplicate close events are normal).
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
//
// The controller is framework-agnostic: the UI layer injects hooks, so
// the state machine itself is unit-testable without a window.
package appclose

import (
	"fmt"
	"sync"
	"time"
)

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
// non-blocking.
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
	// Snapshot reports the active-work state (non-blocking).
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
	States      []State   // the transition trail (ordered, deduplicated)
	CancelErr   error     // non-nil when cancelling work failed
	FlushErr    error     // non-nil when flushing failed (evidence, kept)
	ReleaseErr  error     // non-nil when releasing resources failed
	TimedOut    bool      // a stage exceeded its deadline slice
	CompletedAt time.Time // when the window was finalized
	Duration    time.Duration
}

// Controller is the authoritative close state machine. It is safe for
// concurrent use; RequestClose is expected from UI threads.
type Controller struct {
	mu       sync.Mutex
	state    State
	confirmD bool // a confirmation surface is currently open
	states   []State
	log      func(msg string, args ...any)
	hooks    Hooks
	deadline time.Duration

	doneOnce sync.Once
	done     chan struct{}
	result   ShutdownResult
}

// New creates a controller starting in StateRunning. deadline bounds
// EACH bounded stage (cancel/flush/release slice); a non-positive
// deadline defaults to 10s.
func New(hooks Hooks, deadline time.Duration, log func(msg string, args ...any)) *Controller {
	if deadline <= 0 {
		deadline = 10 * time.Second
	}
	if log == nil {
		log = func(string, ...any) {}
	}
	c := &Controller{
		state:    StateRunning,
		states:   []State{StateRunning},
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
		if c.confirmD {
			// A confirmation is already open: never stack dialogs.
			c.log("close request while confirm open; dialog kept single", "from", from)
			c.mu.Unlock()
			return
		}
	}
	c.mu.Unlock()

	report := WorkReport{}
	if c.hooks.Snapshot != nil {
		report = c.hooks.Snapshot()
	}
	if report.Any() {
		c.transition(StateConfirming)
		c.mu.Lock()
		if c.confirmD {
			c.mu.Unlock()
			return
		}
		c.confirmD = true
		c.mu.Unlock()
		if c.hooks.Confirm != nil {
			c.hooks.Confirm(report, func() {
				c.mu.Lock()
				c.confirmD = false
				c.mu.Unlock()
				go c.runTeardown(from)
			})
		} else {
			c.mu.Lock()
			c.confirmD = false
			c.mu.Unlock()
			go c.runTeardown(from)
		}
		return
	}
	go c.runTeardown(from)
}

// CancelClose returns from an open confirmation to running (the user
// chose to stay). Idempotent.
func (c *Controller) CancelClose(reason string) {
	c.mu.Lock()
	if c.state != StateConfirming {
		c.mu.Unlock()
		return
	}
	c.confirmD = false
	c.state = StateRunning
	c.states = append(c.states, StateRunning)
	c.mu.Unlock()
	c.log("close cancelled by user", "reason", reason)
}

func (c *Controller) runTeardown(from string) {
	// Atomic entry: the guard and the first transition happen under one
	// lock hold, so duplicate teardowns (double close, late duplicates)
	// can never interleave.
	c.mu.Lock()
	if c.state == StateClosed || c.state == StateCancelling || c.state == StateFlushing || c.state == StateClosing {
		c.mu.Unlock()
		return
	}
	c.state = StateCancelling
	c.states = append(c.states, StateCancelling)
	c.mu.Unlock()
	c.log("shutdown state", "state", string(StateCancelling))

	start := time.Now()
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
	res.Duration = time.Since(start)
	c.mu.Lock()
	res.CompletedAt = time.Now()
	c.states = append(c.states, StateClosed)
	res.States = append([]State(nil), c.states...)
	c.result = res
	c.state = StateClosed
	c.mu.Unlock()
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
