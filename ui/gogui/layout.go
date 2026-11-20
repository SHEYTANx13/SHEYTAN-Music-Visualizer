// layout.go — breakpoint-driven information architecture for the Gio
// shell: the responsive classes come from the canonical design token
// document (phone / tablet / desktop / wide), the split-vs-stacked
// switch is derived from them, and the workspace tab strip persists its
// order through the shared versioned LayoutStore.

package gogui

import (
	"errors"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/design"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/presentation"
)

// Breakpoint is one responsive class from the token document.
type Breakpoint string

// The responsive classes; names mirror design/tokens.json exactly.
const (
	BreakpointPhone   Breakpoint = "phone"
	BreakpointTablet  Breakpoint = "tablet"
	BreakpointDesktop Breakpoint = "desktop"
	BreakpointWide    Breakpoint = "wide"
)

// LayoutMode is the composition mode a breakpoint selects.
type LayoutMode string

// The layout modes; names mirror design/tokens.json exactly.
const (
	ModeStacked     LayoutMode = "stacked"
	ModeStackedWide LayoutMode = "stacked-wide"
	ModeSplit       LayoutMode = "split"
	ModeSplitCapped LayoutMode = "split-capped"
)

// Breakpoints is the threshold set derived from the token document.
// Widths are logical dp.
type Breakpoints struct {
	TabletMin  float32 // >= tablet (600)
	DesktopMin float32 // >= desktop (960)
	WideMin    float32 // >= wide (1680)
}

// NewBreakpoints derives the threshold set from the token document
// (phone < tabletMin, tablet < desktopMin, desktop < wideMin, wide
// beyond). A missing token entry falls back to the documented defaults,
// but the canonical document always carries all four entries.
func NewBreakpoints(doc design.TokenDocument) Breakpoints {
	bp := Breakpoints{TabletMin: 600, DesktopMin: 960, WideMin: 1680}
	if v := doc.Breakpoints.Tablet.MinDp; v > 0 {
		bp.TabletMin = float32(v)
	}
	if v := doc.Breakpoints.Desktop.MinDp; v > 0 {
		bp.DesktopMin = float32(v)
	}
	if v := doc.Breakpoints.Wide.MinDp; v > 0 {
		bp.WideMin = float32(v)
	}
	return bp
}

// For classifies a window width into the responsive class.
func (b Breakpoints) For(widthDp float32) Breakpoint {
	switch {
	case widthDp < b.TabletMin:
		return BreakpointPhone
	case widthDp < b.DesktopMin:
		return BreakpointTablet
	case widthDp < b.WideMin:
		return BreakpointDesktop
	default:
		return BreakpointWide
	}
}

// LayoutMode returns the composition mode for the breakpoint.
func (b Breakpoints) LayoutMode(bp Breakpoint) LayoutMode {
	switch bp {
	case BreakpointPhone:
		return ModeStacked
	case BreakpointTablet:
		return ModeStackedWide
	case BreakpointDesktop:
		return ModeSplit
	default:
		return ModeSplitCapped
	}
}

// IsSplit reports whether the breakpoint uses the split composition
// (side rail + main pane) rather than the stacked single column.
func (b Breakpoints) IsSplit(bp Breakpoint) bool {
	switch b.LayoutMode(bp) {
	case ModeSplit, ModeSplitCapped:
		return true
	default:
		return false
	}
}

// breakpoints is the process-wide threshold set, derived once from the
// canonical token document (design.MustLoad fails closed at startup).
var breakpoints = NewBreakpoints(design.MustLoad())

// breakpointFor classifies a window width (logical dp) using the token
// thresholds. Pure function; unit-tested against the token values.
func breakpointFor(widthDp float32) Breakpoint {
	return breakpoints.For(widthDp)
}

// layoutModeFor returns the composition mode for a width.
func layoutModeFor(widthDp float32) LayoutMode {
	return breakpoints.LayoutMode(breakpointFor(widthDp))
}

// isSplitFor reports the split/stacked decision for a width.
func isSplitFor(widthDp float32) bool {
	return breakpoints.IsSplit(breakpointFor(widthDp))
}

// TabState is the workspace navigation state: the persisted tab order,
// the active destination and whether the persisted document had to be
// recovered (surfaced honestly in diagnostics, never silently).
type TabState struct {
	Layout    presentation.WorkspaceLayoutDTO
	Recovered bool
	LoadErr   error
}

// LoadTabs reads the persisted workspace layout through the shared
// LayoutStore. A missing file yields the canonical default; a corrupt
// document yields the default with Recovered=true so the UI can report
// the recovery. Any I/O error is returned for the diagnostics surface.
func LoadTabs(store *presentation.LayoutStore) (TabState, error) {
	lay, err := store.Load()
	res := TabState{Layout: lay}
	switch {
	case err == nil:
	case errors.Is(err, presentation.ErrRecovered):
		res.Recovered = true
		res.Layout = presentation.DefaultLayout()
	default:
		res.Recovered = true
		res.Layout = presentation.DefaultLayout()
		res.LoadErr = err
	}
	if err := presentation.ValidateDestinationOrder(res.Layout.TabOrder); err != nil {
		res.Recovered = true
		res.Layout = presentation.DefaultLayout()
	}
	if res.Layout.ActiveTab == "" {
		res.Layout.ActiveTab = presentation.DestHome
	}
	return res, res.LoadErr
}

// ActiveIndex returns the strip position of the active destination
// (-1 when the active destination is somehow absent — callers reset to
// the canonical default in that case).
func (t *TabState) ActiveIndex() int {
	for i, d := range t.Layout.TabOrder {
		if d == t.Layout.ActiveTab {
			return i
		}
	}
	return -1
}

// SetActive switches the active destination (no-op for unknown ones).
func (t *TabState) SetActive(d presentation.Destination) {
	if err := presentation.ValidateDestination(d); err != nil {
		return
	}
	t.Layout.ActiveTab = d
}

// TabForShortcut maps the navigate shortcut index to the ordered tab
// position: mod+1..9 select tabs 1..9, mod+0 selects tab 10. Index 0
// is out of range. There are eleven destinations; tab 11 has no
// shortcut (as the canonical shortcut table documents: mod+1..9,0).
func TabForShortcut(order []presentation.Destination, index int) (presentation.Destination, bool) {
	if index < 1 || index > 10 || index > len(order) {
		return "", false
	}
	return order[index-1], true
}

// Reset restores the canonical default layout (order, home tab).
func (t *TabState) Reset() {
	t.Layout = presentation.DefaultLayout()
}

// MoveActive swaps the ACTIVE destination one strip position toward
// the start (delta<0) or the end (delta>0). It returns false when the
// move is impossible (edge position or the active tab is somehow
// absent). This is the state mutation behind the tab-strip reorder
// controls and the mod+alt+left/right chords (run 44648357437 item
// 4.3): the pointer/touch gesture and the keyboard alternative share
// one persisted-order mutation.
func (t *TabState) MoveActive(delta int) bool {
	i := t.ActiveIndex()
	if i < 0 {
		return false
	}
	j := i + delta
	if j < 0 || j >= len(t.Layout.TabOrder) {
		return false
	}
	t.Layout.TabOrder[i], t.Layout.TabOrder[j] = t.Layout.TabOrder[j], t.Layout.TabOrder[i]
	return true
}

// SideCollapsed reports whether the ACTIVE destination's side rail is
// collapsed (persisted per destination in the shared versioned layout
// document; old documents without the field load as expanded).
func (t *TabState) SideCollapsed() bool {
	active := t.Layout.ActiveTab
	for _, d := range t.Layout.Collapsed {
		if d == active {
			return true
		}
	}
	return false
}

// SetSideCollapsed adds/removes the ACTIVE destination from the
// persisted collapsed set (deduplicated; other destinations keep
// their state).
func (t *TabState) SetSideCollapsed(v bool) {
	active := t.Layout.ActiveTab
	out := make([]presentation.Destination, 0, len(t.Layout.Collapsed))
	found := false
	for _, d := range t.Layout.Collapsed {
		if d == active {
			found = true
			continue
		}
		out = append(out, d)
	}
	if v && !found {
		out = append(out, active)
	}
	t.Layout.Collapsed = out
}

// OrderedCatalog returns the destination catalog entries in the
// persisted tab order.
func (t *TabState) OrderedCatalog() []presentation.DestinationInfo {
	catalog := presentation.DestinationCatalog()
	byID := make(map[presentation.Destination]presentation.DestinationInfo, len(catalog))
	for _, d := range catalog {
		byID[d.ID] = d
	}
	out := make([]presentation.DestinationInfo, 0, len(t.Layout.TabOrder))
	for _, id := range t.Layout.TabOrder {
		if info, ok := byID[id]; ok {
			out = append(out, info)
		}
	}
	return out
}

// InfoFor returns the catalog entry for one destination.
func InfoFor(d presentation.Destination) (presentation.DestinationInfo, bool) {
	for _, info := range presentation.DestinationCatalog() {
		if info.ID == d {
			return info, true
		}
	}
	return presentation.DestinationInfo{}, false
}
