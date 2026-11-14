// responsive.go — breakpoint-driven information architecture for the
// SHEYTAN studio (run 43090342995 responsive UI/UX contract).
//
// The studio keeps ONE set of shared widget instances; the responsive
// root recomposes them into breakpoint layouts when the window crosses
// a breakpoint, so user state (values, selection, focus) survives
// resize/orientation changes and no expensive widget tree is rebuilt
// per frame or per pixel.
//
// Breakpoints (logical Fyne units, from the mandated viewport matrix):
//
//	phone   width <  480  (320/360/375/390/412/430 portrait; 568/640/740/800 landscape)
//	compact 480 ≤ w < 1024 (600×960, 768×1024, 800×600, 1024×600)
//	desktop 1024 ≤ w < 1680 (1280×720 … 1600×900)
//	wide    width ≥ 1680  (1920×1080, 2560×1080, 3440×1440, 3840×2160)
//
// Per-breakpoint behavior:
//
//	phone   — one primary column: header, focal preview (min height),
//	          touch-sized action grid, library and inspector in one
//	          vertical scroll; the transport bar stays fixed at the
//	          bottom. No horizontal overflow is possible: every action
//	          row is a fixed-cell grid.
//	compact — header + transport fixed; preview and a scrollable side
//	          panel (actions, library, inspector) share the width via a
//	          draggable split.
//	desktop — the classic three-region workspace: left library rail,
//	          center preview, right inspector, full action top bar.
//	wide    — the desktop composition centered and capped at 1680 px
//	          content width over a full-bleed background, so controls
//	          never stretch across an ultrawide monitor.
//
// Fyne contract notes: no absolute placement is used — every
// composition uses Border/VBox/HBox/Grid/Scroll/Split/Stack primitives
// plus two small, unit-tested custom layouts (maxWidthLayout and
// minSizeWrapper) that exist ONLY because built-in layouts cannot
// express a centered max-width workspace or a minimum-height focal
// preview.

package fyneapp

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

type breakpoint int

const (
	bpPhone breakpoint = iota
	bpCompact
	bpDesktop
	bpWide
)

// wideContentCap is the maximum workspace width on ultrawide screens.
const wideContentCap float32 = 1680

// breakpointFor classifies a window width into the layout breakpoint.
func breakpointFor(width float32) breakpoint {
	switch {
	case width < 480:
		return bpPhone
	case width < 1024:
		return bpCompact
	case width < 1680:
		return bpDesktop
	default:
		return bpWide
	}
}

func (b breakpoint) String() string {
	switch b {
	case bpPhone:
		return "phone"
	case bpCompact:
		return "compact"
	case bpDesktop:
		return "desktop"
	default:
		return "wide"
	}
}

// responsiveRoot is the studio's root widget. It recomposes the shared
// regions when the window crosses a breakpoint; between breakpoints the
// current composition is just resized. The swap replaces the objects of
// ONE persistent container, so the paint tree never holds a stale
// duplicate of a shared widget.
type responsiveRoot struct {
	widget.BaseWidget
	u        *studioUI
	box      *fyne.Container
	bp       breakpoint
	inited   bool
	lastSize fyne.Size
}

// newResponsiveRoot builds the root widget and performs the initial
// composition for the given starting size (the pre-Resize hint).
func newResponsiveRoot(u *studioUI, initial fyne.Size) *responsiveRoot {
	r := &responsiveRoot{u: u, bp: breakpointFor(initial.Width)}
	// Max layout sizes the single composition child to the root; the
	// composition container carries its own breakpoint layout.
	r.box = container.New(layout.NewMaxLayout())
	r.box.Objects = []fyne.CanvasObject{u.compose(r.bp)}
	r.inited = true
	r.ExtendBaseWidget(r)
	return r
}

// currentBreakpoint reports the active breakpoint (test/telemetry hook).
func (r *responsiveRoot) currentBreakpoint() breakpoint {
	return r.bp
}

// Resize implements fyne.CanvasObject: crossing a breakpoint recomposes
// the shared regions; otherwise the current composition just resizes.
func (r *responsiveRoot) Resize(size fyne.Size) {
	r.lastSize = size
	r.BaseWidget.Resize(size)
	bp := breakpointFor(size.Width)
	if !r.inited || bp != r.bp {
		r.bp = bp
		r.box.Objects = []fyne.CanvasObject{r.u.compose(bp)}
		r.inited = true
		r.box.Refresh()
		return
	}
}

// CreateRenderer hosts the persistent swap container.
func (r *responsiveRoot) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(r.box)
}

// minSizeWrapper forces a minimum size on a content object while
// allowing it to grow with the available space (used to keep the
// visualizer focal at phone sizes).
type minSizeWrapper struct {
	widget.BaseWidget
	minWidth, minHeight float32
	content             fyne.CanvasObject
}

func newMinSizeWrapper(content fyne.CanvasObject, minW, minH float32) *minSizeWrapper {
	w := &minSizeWrapper{content: content, minWidth: minW, minHeight: minH}
	w.ExtendBaseWidget(w)
	return w
}

func (m *minSizeWrapper) MinSize() fyne.Size {
	c := m.content.MinSize()
	return fyne.NewSize(maxF32(c.Width, m.minWidth), maxF32(c.Height, m.minHeight))
}

func (m *minSizeWrapper) CreateRenderer() fyne.WidgetRenderer {
	return &minSizeRenderer{w: m}
}

// minSizeRenderer pins the content to (0,0) at full size on every
// layout: BaseWidget routes container layouts to the RENDERER, and a
// plain SimpleRenderer only Resizes — without the explicit Move a
// re-parented child would keep a stale position from its previous
// composition.
type minSizeRenderer struct {
	w *minSizeWrapper
}

func (r *minSizeRenderer) Layout(size fyne.Size) {
	r.w.content.Move(fyne.NewPos(0, 0))
	r.w.content.Resize(size)
}

func (r *minSizeRenderer) MinSize() fyne.Size { return r.w.MinSize() }
func (r *minSizeRenderer) Refresh()           { r.w.content.Refresh() }
func (r *minSizeRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.w.content}
}
func (r *minSizeRenderer) Destroy() {}

// maxWidthLayout centers a workspace at a maximum content width over a
// full-bleed background — the ultrawide behavior that no built-in
// layout composes (Center shrinks to min size; Border cannot center).
type maxWidthLayout struct {
	max     float32
	content fyne.CanvasObject
	bg      fyne.CanvasObject
}

func (m maxWidthLayout) Layout(objs []fyne.CanvasObject, size fyne.Size) {
	if m.bg != nil {
		m.bg.Move(fyne.NewPos(0, 0))
		m.bg.Resize(size)
	}
	w := size.Width
	if w > m.max {
		w = m.max
	}
	x := (size.Width - w) / 2
	if x < 0 {
		x = 0
	}
	m.content.Move(fyne.NewPos(x, 0))
	m.content.Resize(fyne.NewSize(w, size.Height))
}

func (m maxWidthLayout) MinSize() fyne.Size {
	c := m.content.MinSize()
	if c.Width > m.max {
		c.Width = m.max
	}
	return c
}

// maxWidthContainer hosts the wide composition.
type maxWidthContainer struct {
	widget.BaseWidget
	layout maxWidthLayout
}

func newMaxWidthContainer(content, bg fyne.CanvasObject, max float32) *maxWidthContainer {
	c := &maxWidthContainer{layout: maxWidthLayout{max: max, content: content, bg: bg}}
	c.ExtendBaseWidget(c)
	return c
}

func (c *maxWidthContainer) CreateRenderer() fyne.WidgetRenderer {
	return &maxWidthRenderer{host: c}
}

type maxWidthRenderer struct {
	host *maxWidthContainer
}

func (r *maxWidthRenderer) Layout(size fyne.Size) { r.host.layout.Layout(nil, size) }
func (r *maxWidthRenderer) MinSize() fyne.Size    { return r.host.layout.MinSize() }
func (r *maxWidthRenderer) Objects() []fyne.CanvasObject {
	objs := []fyne.CanvasObject{r.host.layout.content}
	if r.host.layout.bg != nil {
		objs = append([]fyne.CanvasObject{r.host.layout.bg}, objs...)
	}
	return objs
}
func (r *maxWidthRenderer) Refresh() {
	r.Layout(r.host.Size())
}
func (r *maxWidthRenderer) Destroy() {}

// dialogSizeFor computes a dialog size that always fits the window
// content area: never bigger than the area, never below a usable
// minimum, and never more than the content itself needs on large
// windows. Used by every dialog surface so no close button or form row
// can end up off-screen on a phone-sized window.
func dialogSizeFor(contentMin, area fyne.Size) fyne.Size {
	const margin float32 = 24
	maxW := area.Width - margin
	maxH := area.Height - margin
	if maxW < 160 {
		maxW = area.Width - 8
	}
	if maxH < 120 {
		maxH = area.Height - 8
	}
	if maxW < 0 {
		maxW = area.Width
	}
	if maxH < 0 {
		maxH = area.Height
	}
	w := contentMin.Width
	if w > maxW {
		w = maxW
	}
	h := contentMin.Height
	if h > maxH {
		h = maxH
	}
	if w < 120 {
		w = maxF32(maxW, 120)
	}
	if h < 80 {
		h = maxF32(maxH, 80)
	}
	return fyne.NewSize(w, h)
}

func maxF32(a, b float32) float32 {
	if a > b {
		return a
	}
	return b
}
