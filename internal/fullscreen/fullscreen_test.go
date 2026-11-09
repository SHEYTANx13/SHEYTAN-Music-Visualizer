package fullscreen

import (
	"errors"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// fakeWindow records every request and lets tests control what the
// platform reports and whether requests are honored.
type fakeWindow struct {
	mu       sync.Mutex
	observed bool
	honors   bool // SetFullScreen(true) actually applies
	size     Size
	resizes  []Size
	requests []bool
}

func newFakeWindow() *fakeWindow {
	return &fakeWindow{honors: true, size: Size{1440, 860}}
}

func (f *fakeWindow) FullScreen() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.observed
}

func (f *fakeWindow) SetFullScreen(on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, on)
	if !f.honors {
		return
	}
	f.observed = on
}

func (f *fakeWindow) ContentSize() Size {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.size
}

func (f *fakeWindow) Resize(s Size) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resizes = append(f.resizes, s)
	f.size = s
}

func (f *fakeWindow) dropFullscreen() { // simulate an external WM event
	f.mu.Lock()
	f.observed = false
	f.mu.Unlock()
}

func (f *fakeWindow) requestCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

type memStore struct {
	mu    sync.Mutex
	val   bool
	found bool
	err   error
	saves int
}

func (m *memStore) LoadFullscreen() (bool, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.val, m.found, m.err
}

func (m *memStore) SaveFullscreen(on bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.saves++
	if m.err != nil {
		return m.err
	}
	m.val, m.found = on, true
	return nil
}

func newCtrl(w Window, s Store) *Controller {
	return New(w, s, testLogger())
}

// testLogger returns nil (nil logger is explicitly supported so the
// controller stays usable in minimal harnesses).
func testLogger() *slog.Logger { return nil }

// TestDesiredVsObservedRefusal verifies a refused fullscreen request
// keeps desired=true, observed=false, logs the truth and never claims
// success.
func TestDesiredVsObservedRefusal(t *testing.T) {
	w := newFakeWindow()
	w.honors = false
	c := newCtrl(w, &memStore{})
	c.Enter("test-refusal")
	if !c.Desired() {
		t.Fatal("desired must stay true after the request")
	}
	if c.Observed() {
		t.Fatal("observed must remain false when the platform refuses — never claim fullscreen")
	}
}

// TestToggleAndObservedState verifies the F11 path enters and exits
// with the platform following.
func TestToggleAndObservedState(t *testing.T) {
	w := newFakeWindow()
	c := newCtrl(w, &memStore{})
	c.Toggle("f11")
	if !c.Observed() || !c.Desired() {
		t.Fatalf("after enter: desired=%v observed=%v", c.Desired(), c.Observed())
	}
	c.Toggle("f11")
	if c.Observed() || c.Desired() {
		t.Fatalf("after exit: desired=%v observed=%v", c.Desired(), c.Observed())
	}
}

// TestExplicitExitPreventsAutoReenter is the user-facing fullscreen
// stability contract: after the user explicitly exits, a subsequent
// Reconcile (dialog close, navigation, activation) must NOT re-enter.
func TestExplicitExitPreventsAutoReenter(t *testing.T) {
	w := newFakeWindow()
	c := newCtrl(w, &memStore{})
	c.Enter("user-f11")
	c.Exit("user-f11")
	w.dropFullscreen() // platform drift after exit
	before := w.requestCount()
	c.Reconcile("dialog-closed")
	if w.requestCount() != before {
		t.Fatal("Reconcile re-entered fullscreen after an explicit user exit")
	}
	if c.Desired() {
		t.Fatal("desired must remain false after explicit exit")
	}
}

// TestReconcileRestoresPersistedIntent verifies event-driven
// restoration: the OS dropped fullscreen, the user's persisted intent
// is still fullscreen, one bounded re-apply is issued.
func TestReconcileRestoresPersistedIntent(t *testing.T) {
	w := newFakeWindow()
	s := &memStore{}
	c := newCtrl(w, s)
	c.Enter("user-f11")
	w.dropFullscreen() // genuine external event
	c.Reconcile("window-activated")
	if !c.Observed() {
		t.Fatal("reconcile did not restore fullscreen while intent persisted")
	}
	if w.requestCount() != 2 { // enter + one reconcile apply
		t.Fatalf("requests = %d, want exactly one re-apply (2 total)", w.requestCount())
	}
	// A second immediate Reconcile inside the debounce window is a no-op.
	c.Reconcile("dialog-closed")
	if w.requestCount() != 2 {
		t.Fatalf("reconcile hammering detected: %d requests", w.requestCount())
	}
}

// TestReconcileAfterDebounceAllowsReapply verifies the debounce is
// time-bounded, not a permanent block.
func TestReconcileAfterDebounceAllowsReapply(t *testing.T) {
	w := newFakeWindow()
	c := newCtrl(w, &memStore{})
	c.ReapplyInterval = 0 // disable debounce for the test
	c.Enter("user-f11")
	w.dropFullscreen()
	c.Reconcile("event-1")
	w.dropFullscreen()
	c.Reconcile("event-2")
	if !c.Observed() {
		t.Fatal("second reconcile after interval should re-apply")
	}
}

// TestGeometryPreservedAcrossFullscreen verifies the last windowed
// size is captured before entering and restored on exit.
func TestGeometryPreservedAcrossFullscreen(t *testing.T) {
	w := newFakeWindow()
	c := newCtrl(w, &memStore{})
	c.Enter("user-f11")
	c.Exit("user-f11")
	if len(w.resizes) != 1 || w.resizes[0] != (Size{1440, 860}) {
		t.Fatalf("windowed geometry not restored: %+v", w.resizes)
	}
}

// TestPersistenceRoundTrip verifies the intent survives a new
// controller over the same store (next-launch restore).
func TestPersistenceRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := NewFileStore(filepath.Join(dir, "window-state.json"))
	if _, found, err := s.LoadFullscreen(); found || err != nil {
		t.Fatalf("fresh store: found=%v err=%v", found, err)
	}
	w := newFakeWindow()
	c := newCtrl(w, s)
	c.Enter("first-run")
	// Simulate next launch with a NEW controller over the same store.
	c2 := newCtrl(newFakeWindow(), s)
	if !c2.Desired() {
		t.Fatal("persisted fullscreen intent was not restored on next launch")
	}
	if err := s.SaveFullscreen(false); err != nil {
		t.Fatalf("save off: %v", err)
	}
	c3 := newCtrl(newFakeWindow(), s)
	if c3.Desired() {
		t.Fatal("persisted windowed intent was not restored")
	}
}

// TestPersistenceSaveErrorIsNotFatal verifies a failing store never
// breaks the fullscreen behavior (preference loss is logged only).
func TestPersistenceSaveErrorIsNotFatal(t *testing.T) {
	w := newFakeWindow()
	s := &memStore{err: errors.New("disk full")}
	c := newCtrl(w, s)
	c.Enter("user-f11")
	if !c.Observed() {
		t.Fatal("fullscreen must still work when persistence fails")
	}
}

// TestStartupRestoreWithoutPersistedIntentDoesNothing verifies a fresh
// install (no preference) stays windowed at startup.
func TestStartupRestoreWithoutPersistedIntentDoesNothing(t *testing.T) {
	w := newFakeWindow()
	c := newCtrl(w, &memStore{})
	c.ApplyPersistedAtStartup("launch")
	if w.requestCount() != 0 {
		t.Fatal("no fullscreen request expected without a persisted intent")
	}
}

// TestStartupRestoreAppliesPersistedIntent verifies the post-show
// restore issues exactly one request.
func TestStartupRestoreAppliesPersistedIntent(t *testing.T) {
	w := newFakeWindow()
	s := &memStore{val: true, found: true}
	c := newCtrl(w, s)
	c.ApplyPersistedAtStartup("launch")
	if w.requestCount() != 1 || !c.Observed() {
		t.Fatalf("requests=%d observed=%v", w.requestCount(), c.Observed())
	}
}

// TestSyncObservedReportsDrift verifies SyncObserved updates the truth
// without requesting anything.
func TestSyncObservedReportsDrift(t *testing.T) {
	w := newFakeWindow()
	c := newCtrl(w, &memStore{})
	c.Enter("user-f11")
	w.dropFullscreen()
	before := w.requestCount()
	got := c.SyncObserved("preset-changed")
	if got {
		t.Fatal("SyncObserved must report the drifted state")
	}
	if w.requestCount() != before {
		t.Fatal("SyncObserved must not issue requests")
	}
	if c.Observed() {
		t.Fatal("controller kept a stale observed state")
	}
}

// TestReapplyIntervalDefaultIsBounded documents the bounded default.
func TestReapplyIntervalDefaultIsBounded(t *testing.T) {
	c := New(newFakeWindow(), &memStore{}, nil)
	if c.ReapplyInterval <= 0 || c.ReapplyInterval > 5*time.Second {
		t.Fatalf("ReapplyInterval = %v, want a small bounded debounce", c.ReapplyInterval)
	}
}
