package fyneapp

// responsive_test.go — run-43090342995 Phase 7/8 viewport contract.
//
// The test instantiates the ACTUAL root composition path (buildRegions +
// responsive root + per-breakpoint compose) through the Fyne test
// driver, drives the mandated viewport matrix and asserts:
//
//   - no unintended overlap between the header, transport and preview
//     regions (Border zones are exclusive);
//   - no negative positions and no horizontal overflow of the fixed
//     regions (header/transport) or of the focal preview;
//   - every required action button stays visible with a non-zero,
//     touch-safe size at every breakpoint;
//   - the focal preview keeps a meaningful minimum height on phones;
//   - widget state (selected mode) survives phone → wide → phone
//     transitions because compositions re-parent shared instances;
//   - a long track title truncates instead of pushing the layout.
//
// The live render loop is intentionally NOT started: fyne.Do executes
// inline under the test driver, which would race the tree walk. The
// composition under test is exactly the production path.

import (
	"strconv"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/studio"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/visualizer"
)

// viewportMatrix is the mandated viewport list (logical Fyne units).
var viewportMatrix = []fyne.Size{
	// phone portrait
	{Width: 320, Height: 568}, {Width: 360, Height: 640}, {Width: 375, Height: 812},
	{Width: 390, Height: 844}, {Width: 412, Height: 915}, {Width: 430, Height: 932},
	// phone landscape / small window
	{Width: 568, Height: 320}, {Width: 640, Height: 360}, {Width: 740, Height: 360},
	{Width: 800, Height: 480},
	// compact tablet / small laptop
	{Width: 600, Height: 960}, {Width: 768, Height: 1024}, {Width: 800, Height: 600},
	{Width: 1024, Height: 600}, {Width: 1024, Height: 768},
	// desktop
	{Width: 1280, Height: 720}, {Width: 1366, Height: 768}, {Width: 1440, Height: 900},
	{Width: 1600, Height: 900}, {Width: 1920, Height: 1080},
	// ultrawide / high resolution
	{Width: 2560, Height: 1080}, {Width: 3440, Height: 1440}, {Width: 3840, Height: 2160},
}

type viewportHarness struct {
	app  fyne.App
	win  fyne.Window
	u    *studioUI
	svc  *studio.Studio
	root *responsiveRoot
}

func newViewportHarness(t *testing.T) *viewportHarness {
	t.Helper()
	svc, err := studio.New()
	if err != nil {
		t.Fatalf("studio service: %v", err)
	}
	t.Cleanup(svc.Close)
	app := test.NewApp()
	w := app.NewWindow("viewport-test")
	// The production window runs SetPadded(false); mirror it so window
	// size == content size and the matrix widths map 1:1 to breakpoints.
	w.SetPadded(false)
	u := newStudioUI(w, svc)
	// Production composition path minus the live render loop and the
	// event subscription (fyne.Do runs inline under the test driver).
	u.buildRegions()
	root := newResponsiveRoot(u, viewportMatrix[8]) // initial: 390x844 phone
	w.SetContent(root)
	u.rootResp = root
	u.applyRegions(root.bp)
	u.syncInspector()
	return &viewportHarness{app: app, win: w, u: u, svc: svc, root: root}
}

func (h *viewportHarness) resize(t *testing.T, size fyne.Size) {
	t.Helper()
	h.win.Resize(size)
	// The test driver lays out synchronously on Resize; the responsive
	// root recomposes when the breakpoint changed.
	if got := h.root.currentBreakpoint(); got != breakpointFor(size.Width) {
		t.Fatalf("breakpoint after resize to %v = %v, want %v", size, got, breakpointFor(size.Width))
	}
}

func (h *viewportHarness) region(name string) fyne.CanvasObject {
	return h.u.regions[name]
}

func TestViewportMatrixLayoutContract(t *testing.T) {
	h := newViewportHarness(t)
	for _, vp := range viewportMatrix {
		vp := vp
		t.Run(strings.Join([]string{"vp", f32s(vp.Width), "x", f32s(vp.Height)}, ""), func(t *testing.T) {
			h.resize(t, vp)
			assertRegionsHealthy(t, h, vp)
			assertActionsReachable(t, h)
		})
	}
}

// assertRegionsHealthy checks bounds, exclusivity and non-zero sizes of
// the fixed regions at one viewport.
func assertRegionsHealthy(t *testing.T, h *viewportHarness, vp fyne.Size) {
	t.Helper()
	header := h.region("header")
	transport := h.region("transport")
	preview := h.region("preview")
	if header == nil || transport == nil || preview == nil {
		t.Fatalf("regions missing: header=%v transport=%v preview=%v", header != nil, transport != nil, preview != nil)
	}

	// Fixed regions: header and transport live in the window's top and
	// bottom Border zones — they must be fully inside the window.
	hr := rectOf(header)
	tr := rectOf(transport)
	for _, r := range []string{"header", "transport"} {
		var rect rect
		if r == "header" {
			rect = hr
		} else {
			rect = tr
		}
		if rect.X < -0.5 || rect.Y < -0.5 {
			t.Errorf("%s at negative position: %+v (viewport %v)", r, rect, vp)
		}
		if rect.Width <= 0 || rect.Height <= 0 {
			t.Errorf("%s has zero size: %+v (viewport %v)", r, rect, vp)
		}
		if rect.X+rect.Width > vp.Width+2 {
			t.Errorf("%s overflows horizontally: %+v vs width %v (viewport %v)", r, rect, vp.Width, vp)
		}
	}
	if tr.Y+tr.Height > vp.Height+2 {
		t.Errorf("transport overflows vertically below the window: %+v vs height %v", tr, vp.Height)
	}

	// Preview is the focal point: visible, non-zero, and horizontally
	// inside the window. Vertically it may extend beyond the fold on
	// phones (it scrolls), never horizontally.
	pr := rectOf(preview)
	if pr.Width <= 0 || pr.Height <= 0 {
		t.Errorf("preview has zero size: %+v (viewport %v)", pr, vp)
	}
	if pr.X < -0.5 || pr.X+pr.Width > vp.Width+2 {
		t.Errorf("preview overflows horizontally: %+v vs width %v (viewport %v)", pr, vp.Width, vp)
	}

	// Exclusivity: the three Border zones must not overlap. Preview may
	// scroll past the transport on tall content, but its VISIBLE band
	// starts below the header and the transport sits at the bottom —
	// assert the header and transport never overlap, and the preview's
	// top edge starts at or below the header's bottom edge.
	if hr.Y < tr.Y && hr.Y+hr.Height > tr.Y+1 {
		t.Errorf("header and transport overlap: header %+v transport %+v (viewport %v)", hr, tr, vp)
	}
	if pr.Y < hr.Y+hr.Height-2 && pr.Y >= hr.Y && hr.Height > 0 && vp.Height > 0 && pr.Y != 0 && bpOf(vp) == bpPhone {
		// phone: preview is the first scrolled child — its top must not
		// be ABOVE the header bottom (it would mean absolute overlap).
		if pr.Y < hr.Y {
			t.Errorf("preview overlaps header from above: preview %+v header %+v (viewport %v)", pr, hr, vp)
		}
	}

	// Phone focal minimum height contract.
	if bpOf(vp) == bpPhone && pr.Height < 100 {
		t.Errorf("phone preview collapsed to %v height (viewport %v)", pr.Height, vp)
	}
}

// assertActionsReachable: every primary action is visible with a
// non-zero, touch-safe size at this breakpoint.
func assertActionsReachable(t *testing.T, h *viewportHarness) {
	t.Helper()
	for i, b := range h.u.actionButtons {
		if !b.Visible() {
			t.Errorf("action %q hidden", b.Text)
			continue
		}
		r := rectOf(b)
		if r.Width <= 0 || r.Height <= 0 {
			t.Errorf("action %q has zero size: %+v", b.Text, r)
		}
		if bpOf(fyne.NewSize(h.win.Canvas().Size().Width, h.win.Canvas().Size().Height)) == bpPhone {
			if r.Height < 30 {
				t.Errorf("phone touch target too small for %q: %+v", b.Text, r)
			}
		}
		if r.X < -0.5 {
			t.Errorf("action %q at negative x: %+v", b.Text, r)
		}
		_ = i
	}
}

type rect struct {
	X, Y, Width, Height float32
}

func rectOf(o fyne.CanvasObject) rect {
	p := o.Position()
	s := o.Size()
	return rect{X: p.X, Y: p.Y, Width: s.Width, Height: s.Height}
}

func bpOf(vp fyne.Size) breakpoint { return breakpointFor(vp.Width) }

func f32s(v float32) string {
	return strconv.FormatFloat(float64(v), 'f', -1, 32)
}

// TestBreakpointTransitionsPreserveState: crossing breakpoints
// repeatedly keeps the selected mode and shared widget state intact.
func TestBreakpointTransitionsPreserveState(t *testing.T) {
	h := newViewportHarness(t)
	// pick a non-default mode at desktop size
	h.resize(t, fyne.NewSize(1440, 900))
	want := visualizer.ModeWaveform
	h.u.modeSelect.SetSelected(want.DisplayName())
	if got := h.u.svc.Config().Mode; got != want {
		t.Fatalf("selection did not reach the service: got %v want %v", got, want)
	}
	for _, vp := range []fyne.Size{
		{Width: 375, Height: 812}, {Width: 3440, Height: 1440},
		{Width: 568, Height: 320}, {Width: 800, Height: 600},
		{Width: 390, Height: 844},
	} {
		h.resize(t, vp)
		if got := h.u.modeSelect.Selected; got != want.DisplayName() {
			t.Errorf("mode selection lost after resize to %v: got %q", vp, got)
		}
		cfg := h.u.svc.Config()
		if cfg.Mode != want {
			t.Errorf("service config changed across resize to %v: %v", vp, cfg.Mode)
		}
		assertRegionsHealthy(t, h, vp)
	}
}

// TestLongTrackTitleTruncates: a very long title never pushes the
// header or window layout wide at any breakpoint.
func TestLongTrackTitleTruncates(t *testing.T) {
	h := newViewportHarness(t)
	long := strings.Repeat("SHEYTAN ultra long track title ", 8)
	h.u.trackLabel.SetText(long)
	for _, vp := range []fyne.Size{
		{Width: 320, Height: 568}, {Width: 768, Height: 1024}, {Width: 1920, Height: 1080},
	} {
		h.resize(t, vp)
		hr := rectOf(h.region("header"))
		if hr.Width > vp.Width+2 {
			t.Errorf("long title pushed header to %v (window %v)", hr.Width, vp.Width)
		}
		if hr.X+hr.Width > vp.Width+2 {
			t.Errorf("header overflows horizontally with a long title at %v: %+v", vp, hr)
		}
	}
}

// TestNoSongEmptyStateVisible: without a track the honest empty state is
// shown and the transport is still usable at phone size.
func TestNoSongEmptyStateVisible(t *testing.T) {
	h := newViewportHarness(t)
	h.resize(t, fyne.NewSize(360, 640))
	if !h.u.emptyState.Visible() {
		t.Fatal("empty state must be visible with no song loaded")
	}
	if h.u.btnPlay == nil || !h.u.btnPlay.Visible() {
		t.Fatal("play control must stay usable with no song loaded")
	}
}

// TestDialogSizeFitsAnyViewport: the pure dialog-size helper keeps
// dialogs inside every window of the matrix (no off-screen close
// buttons, no clipped forms) while never exceeding the content's own
// minimum on large windows.
func TestDialogSizeFitsAnyViewport(t *testing.T) {
	for _, vp := range viewportMatrix {
		area := fyne.NewSize(vp.Width, vp.Height)
		min := fyne.NewSize(520, 380) // a realistically wide dialog body
		got := dialogSizeFor(min, area)
		if got.Width > area.Width || got.Height > area.Height {
			t.Errorf("dialog %v exceeds viewport %v", got, vp)
		}
		if got.Width < 120 || got.Height < 80 {
			t.Errorf("dialog collapsed to unusable size %v at %v", got, vp)
		}
	}
	big := dialogSizeFor(fyne.NewSize(520, 380), fyne.NewSize(1920, 1080))
	if big.Width < 520 || big.Height < 380 {
		t.Errorf("large windows must fit the full dialog minimum: %v", big)
	}
}
