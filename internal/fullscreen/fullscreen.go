// Package fullscreen owns the SINGLE fullscreen/window-state authority
// for SHEYTAN Music Visualizer. It keeps the user's intent
// (desired state, persisted) strictly separate from the platform's
// observed state (Window.FullScreen()), centralizes every transition,
// logs requested-vs-observed outcomes with a reason, and reconciles
// ONLY on explicit events — never with a timer loop.
//
// Design rules (roadmap fullscreen contract):
//
//   - Desired vs observed: a fullscreen REQUEST is not fullscreen
//     FACT. Every apply re-reads Window.FullScreen() and records the
//     truth; a refused request leaves the app usable windowed and
//     never claims fullscreen.
//   - Persistence: the user's explicit choice is stored
//     (config/window-state.json) and restored on the next launch.
//   - Reconciliation: Reconcile(reason) re-applies the persisted
//     intent at most once per call and at most once per debounce
//     interval, so a genuine external fullscreen loss (window manager,
//     display change) can be recovered without fighting the OS, focus
//     stealing, or a hot loop.
//   - User exit wins: an explicit Exit clears the persisted intent; no
//     later Reconcile re-enters fullscreen behind the user's back.
//   - Geometry: the last windowed content size is captured before
//     entering and restored on exit. Fullscreen is never faked by
//     resizing the window to guessed monitor dimensions.
//
// The controller is framework-agnostic (no Fyne import): the UI layer
// provides a Window adapter, tests provide fakes.
package fullscreen

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Size is a window content size in Fyne units.
type Size struct {
	Width  float32
	Height float32
}

// Valid reports whether the size was actually measured (Fyne reports
// 0x0 before the window is shown).
func (s Size) Valid() bool { return s.Width > 0 && s.Height > 0 }

// Window is the platform surface the controller drives. The UI layer
// adapts fyne.Window to this interface; tests use fakes.
type Window interface {
	// FullScreen reports the OBSERVED platform state.
	FullScreen() bool
	// SetFullScreen makes the fullscreen REQUEST.
	SetFullScreen(on bool)
	// ContentSize returns the current content size (geometry
	// preservation).
	ContentSize() Size
	// Resize requests a content size (windowed restore only).
	Resize(s Size)
}

// Store persists the user's fullscreen intent. Implementations must be
// safe for the UI thread; the file store is mutex-guarded.
type Store interface {
	LoadFullscreen() (on bool, found bool, err error)
	SaveFullscreen(on bool) error
}

// Controller is the single fullscreen state authority.
type Controller struct {
	mu sync.Mutex

	win    Window
	store  Store
	logger *slog.Logger

	desired  bool // user intent (persisted)
	observed bool // last observed platform state

	windowedSize Size
	// lastReconcileApply bounds re-apply bursts from Reconcile only
	// (event-driven debounce; the initial Enter is never throttled).
	lastReconcileApply time.Time
	// ReapplyInterval bounds how often Reconcile may re-issue a
	// request (event-driven debounce; never a polling loop).
	ReapplyInterval time.Duration
}

// New constructs the controller. When store reports a persisted
// intent, it becomes the desired state; otherwise the default is
// windowed (a fresh install never silently goes fullscreen). The
// observed state is initialized from the platform.
func New(w Window, s Store, logger *slog.Logger) *Controller {
	c := &Controller{
		win:             w,
		store:           s,
		logger:          logger,
		ReapplyInterval: 500 * time.Millisecond,
	}
	if s != nil {
		if on, found, err := s.LoadFullscreen(); err != nil {
			c.logWarn("fullscreen preference unread; default windowed", err)
		} else if found {
			c.desired = on
		}
	}
	if w != nil {
		c.observed = w.FullScreen()
	}
	return c
}

// Desired returns the persisted user intent.
func (c *Controller) Desired() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.desired
}

// Observed returns the last observed platform state.
func (c *Controller) Observed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.observed
}

// Toggle flips the user intent (F11 handler).
func (c *Controller) Toggle(reason string) {
	c.mu.Lock()
	want := !c.desired
	c.mu.Unlock()
	if want {
		c.Enter(reason)
	} else {
		c.Exit(reason)
	}
}

// Enter requests fullscreen, persists the intent and records the
// truth about whether the platform applied it. The last windowed
// geometry is captured first.
func (c *Controller) Enter(reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.desired = true
	c.persist(true)
	if c.win == nil {
		c.logState("enter-without-window", reason, false, false)
		return
	}
	if s := c.win.ContentSize(); s.Valid() && !c.observed {
		c.windowedSize = s
	}
	c.apply("enter", reason)
}

// Exit clears the intent (persisted), leaves fullscreen and restores
// the saved windowed geometry. An explicit user exit is final: later
// Reconcile calls never re-enter.
func (c *Controller) Exit(reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.desired = false
	c.persist(false)
	if c.win == nil {
		c.logState("exit-without-window", reason, false, false)
		return
	}
	c.win.SetFullScreen(false)
	c.observed = c.win.FullScreen()
	if !c.observed && c.windowedSize.Valid() {
		c.win.Resize(c.windowedSize)
	}
	c.logState("exit", reason, false, c.observed)
}

// Reconcile re-applies the persisted intent when the platform drifted
// (OS/window manager exited fullscreen while the user still wanted
// it). Bounded by construction: at most one request per call and at
// most one request per ReapplyInterval — never a retry loop. An
// explicit user exit (desired=false) is never overridden.
func (c *Controller) Reconcile(reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.desired || c.win == nil {
		return
	}
	c.observed = c.win.FullScreen()
	if c.observed {
		return // platform already agrees; nothing to do
	}
	if !c.lastReconcileApply.IsZero() && time.Since(c.lastReconcileApply) < c.ReapplyInterval {
		c.logState("reconcile-debounced", reason, c.desired, c.observed)
		return
	}
	c.lastReconcileApply = time.Now()
	c.apply("reconcile", reason)
}

// SyncObserved re-reads the platform state without requesting anything
// (used after dialog open/close and navigation to keep the truth
// current). It returns the observed value.
func (c *Controller) SyncObserved(reason string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.win == nil {
		return false
	}
	c.observed = c.win.FullScreen()
	if c.desired != c.observed {
		c.logState("sync", reason, c.desired, c.observed)
	}
	return c.observed
}

// apply issues the fullscreen request and records requested-vs-
// observed truth. Caller holds c.mu.
func (c *Controller) apply(event, reason string) {
	c.win.SetFullScreen(true)
	c.observed = c.win.FullScreen()
	if !c.observed {
		// The platform refused (or has not applied) the request. Log
		// the truth, keep the intent, stay usable windowed. No blind
		// retry here; a later Reconcile may try again on a real event.
		c.logWarn(fmt.Sprintf("platform did not honor fullscreen request (%s)", reason), nil)
	}
	c.logState(event, reason, c.desired, c.observed)
}

// ApplyPersistedAtStartup applies the persisted intent after the
// window is shown and the event loop is ready (the correct moment per
// the Fyne contract). Without a persisted intent it does nothing.
func (c *Controller) ApplyPersistedAtStartup(reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.desired || c.win == nil {
		return
	}
	if s := c.win.ContentSize(); s.Valid() && !c.observed {
		c.windowedSize = s
	}
	c.apply("startup-restore", reason)
}

func (c *Controller) persist(on bool) {
	if c.store == nil {
		return
	}
	if err := c.store.SaveFullscreen(on); err != nil {
		c.logWarn("fullscreen preference could not be saved", err)
	}
}

func (c *Controller) logState(event, reason string, desired, observed bool) {
	if c.logger == nil {
		return
	}
	c.logger.Info("fullscreen transition",
		slog.String("event", event),
		slog.String("reason", reason),
		slog.Bool("desired", desired),
		slog.Bool("observed", observed))
}

func (c *Controller) logWarn(msg string, err error) {
	if c.logger == nil {
		return
	}
	if err != nil {
		c.logger.Warn(msg, slog.String("err", err.Error()))
		return
	}
	c.logger.Warn(msg)
}

// ---- file-backed store -------------------------------------------------

// stateFile is the persisted window-state document.
type stateFile struct {
	// Fullscreen is the user's persisted intent.
	Fullscreen bool `json:"fullscreen"`
}

// FileStore persists the intent in the app config directory
// (window-state.json). Missing file = no preference (found=false).
type FileStore struct {
	mu   sync.Mutex
	path string
}

// NewFileStore builds a store writing to path (typically
// <layout.Config>/window-state.json).
func NewFileStore(path string) *FileStore {
	return &FileStore{path: path}
}

// Path returns the backing file path (for diagnostics).
func (s *FileStore) Path() string { return s.path }

// LoadFullscreen implements Store.
func (s *FileStore) LoadFullscreen() (bool, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, false, nil
		}
		return false, false, fmt.Errorf("fullscreen: cannot read %q: %w", s.path, err)
	}
	var st stateFile
	if err := json.Unmarshal(raw, &st); err != nil {
		return false, false, fmt.Errorf("fullscreen: cannot parse %q: %w", s.path, err)
	}
	return st.Fullscreen, true, nil
}

// SaveFullscreen implements Store. The directory must exist
// (apppaths.Bootstrap creates it); creation errors are returned.
func (s *FileStore) SaveFullscreen(on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if dir := filepath.Dir(s.path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("fullscreen: cannot create config dir %q: %w", dir, err)
		}
	}
	raw, err := json.Marshal(stateFile{Fullscreen: on})
	if err != nil {
		return fmt.Errorf("fullscreen: cannot encode state: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return fmt.Errorf("fullscreen: cannot write %q: %w", tmp, err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("fullscreen: cannot finalize %q: %w", s.path, err)
	}
	return nil
}
