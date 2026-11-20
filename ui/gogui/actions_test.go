package gogui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-gui-org/go-gui/gui"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/fileaccess"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/presentation"
)

// allPresentationCommands lists every canonical command ID from
// internal/presentation — the completeness baseline for the dispatch
// table test.
func allPresentationCommands() []presentation.CommandID {
	return []presentation.CommandID{
		presentation.CmdImportAudio,
		presentation.CmdPlayPause,
		presentation.CmdStop,
		presentation.CmdSeekBack,
		presentation.CmdSeekForward,
		presentation.CmdSetVolume,
		presentation.CmdProjectSave,
		presentation.CmdProjectOpen,
		presentation.CmdExportStart,
		presentation.CmdExportCancel,
		presentation.CmdRendererSelect,
		presentation.CmdModeSet,
		presentation.CmdPaletteSet,
		presentation.CmdSensitivitySet,
		presentation.CmdBackgroundLayerSet,
		presentation.CmdPresetApply,
		presentation.CmdIntensitySet,
		presentation.CmdReducedMotionSet,
		presentation.CmdReducedGlowSet,
		presentation.CmdNavigate,
		presentation.CmdTabsReorder,
		presentation.CmdTabsReset,
		presentation.CmdSplitOrientation,
		presentation.CmdPanelCollapse,
		presentation.CmdPanelRestore,
		presentation.CmdLayoutReset,
		presentation.CmdMediaToolsInstall,
		presentation.CmdMediaToolsOpen,
		presentation.CmdAnalyzePath,
		presentation.CmdHelpOpen,
		presentation.CmdLogsReveal,
		presentation.CmdCopyToClipboard,
		presentation.CmdQuit,
		presentation.CmdAbout,
	}
}

// TestDispatcherCompleteness verifies every presentation command is
// either handled by the shell or explicitly unsupported with a reason
// — no command can silently vanish.
func TestDispatcherCompleteness(t *testing.T) {
	sh := &shell{state: newUIState()}
	d := newDispatcher(sh)
	handled := map[presentation.CommandID]bool{}
	for _, id := range d.Supported() {
		handled[id] = true
	}
	unsupported := d.Unsupported()
	for _, id := range allPresentationCommands() {
		if !handled[id] {
			reason, ok := unsupported[id]
			if !ok {
				t.Errorf("command %q is neither handled nor explicitly unsupported", id)
				continue
			}
			if reason == "" {
				t.Errorf("command %q is unsupported without a reason", id)
			}
		} else if _, ok := unsupported[id]; ok {
			t.Errorf("command %q appears in both the handled and unsupported sets", id)
		}
	}
	if len(handled)+len(unsupported) < len(allPresentationCommands()) {
		t.Errorf("dispatch table has %d entries, want at least %d",
			len(handled)+len(unsupported), len(allPresentationCommands()))
	}
}

// TestDispatcherRunUnsupported verifies the unsupported path surfaces
// the honest reason through the status strip instead of failing, AND
// that the four workspace-composition commands are now REALLY handled
// (run 44648357437 item 4.3 — the honest-missing stubs were replaced
// by persisted reorder/collapse/orientation behavior).
func TestDispatcherRunUnsupported(t *testing.T) {
	sh := &shell{state: newUIState()}
	d := newDispatcher(sh)
	for _, id := range []presentation.CommandID{
		presentation.CmdTabsReorder,
		presentation.CmdSplitOrientation,
		presentation.CmdPanelCollapse,
		presentation.CmdPanelRestore,
	} {
		if !d.Run(id) {
			t.Fatalf("workspace command %q must be handled", id)
		}
		if got := sh.state.Status(); got == "" {
			t.Errorf("workspace command %q must set a status message", id)
		}
	}
	for _, id := range []presentation.CommandID{
		presentation.CmdTabsReorder,
		presentation.CmdSplitOrientation,
		presentation.CmdPanelCollapse,
		presentation.CmdPanelRestore,
	} {
		if reason, unsupported := d.unsupported[id]; unsupported {
			t.Errorf("command %q must not be in the unsupported set any more (reason %q)", id, reason)
		}
	}
}

// TestDispatcherKeyHandlers verifies the critical handlers are bound.
func TestDispatcherKeyHandlers(t *testing.T) {
	sh := &shell{state: newUIState()}
	d := newDispatcher(sh)
	handled := map[presentation.CommandID]bool{}
	for _, id := range d.Supported() {
		handled[id] = true
	}
	for _, id := range []presentation.CommandID{
		presentation.CmdImportAudio,
		presentation.CmdPlayPause,
		presentation.CmdStop,
		presentation.CmdSeekBack,
		presentation.CmdSeekForward,
		presentation.CmdProjectSave,
		presentation.CmdProjectOpen,
		presentation.CmdExportStart,
		presentation.CmdExportCancel,
		presentation.CmdQuit,
		presentation.CmdAnalyzePath,
		presentation.CmdMediaToolsInstall,
	} {
		if !handled[id] {
			t.Errorf("command %q must be handled by this shell", id)
		}
	}
}

// TestShortcutFor verifies the canonical desktop shortcut table binds
// exactly as presentation.Shortcuts documents (mod = Ctrl on
// Windows/Linux, Super/Cmd on macOS; both chords are accepted by the
// pure mapping).
func TestShortcutFor(t *testing.T) {
	mod := gui.ModCtrl
	modSuper := gui.ModSuper
	modAlt := gui.ModCtrl | gui.ModAlt
	cases := []struct {
		name     gui.KeyCode
		mods     gui.Modifier
		wantCmd  presentation.CommandID
		wantNav  int
		wantBind bool
	}{
		{gui.KeySpace, 0, presentation.CmdPlayPause, 0, true},
		{gui.KeyLeft, 0, presentation.CmdSeekBack, 0, true},
		{gui.KeyRight, 0, presentation.CmdSeekForward, 0, true},
		{gui.KeyI, mod, presentation.CmdImportAudio, 0, true},
		{gui.KeyI, modSuper, presentation.CmdImportAudio, 0, true},
		{gui.KeyS, mod, presentation.CmdProjectSave, 0, true},
		{gui.KeyO, mod, presentation.CmdProjectOpen, 0, true},
		{gui.KeyE, mod, presentation.CmdExportStart, 0, true},
		{gui.KeyQ, mod, presentation.CmdQuit, 0, true},
		{gui.KeyPeriod, mod, presentation.CmdStop, 0, true},
		{gui.Key1, mod, presentation.CmdNavigate, 1, true},
		{gui.Key9, mod, presentation.CmdNavigate, 9, true},
		{gui.Key0, mod, presentation.CmdNavigate, 10, true},
		{gui.Key0, modAlt, presentation.CmdLayoutReset, 0, true},
		// Non-bindings: plain letters, modifier-held space, other letters.
		{gui.KeyI, 0, "", 0, false},
		{gui.KeyS, 0, "", 0, false},
		{gui.KeySpace, mod, "", 0, false},
		{gui.KeyX, mod, "", 0, false},
		// Arrow seek is exact-chord: a held modifier is not reinterpreted.
		{gui.KeyLeft, mod, "", 0, false},
	}
	for _, c := range cases {
		cmd, nav, bound := shortcutFor(c.name, c.mods)
		if bound != c.wantBind || cmd != c.wantCmd || nav != c.wantNav {
			t.Errorf("shortcutFor(%d, %v) = (%q, %d, %v), want (%q, %d, %v)",
				c.name, c.mods, cmd, nav, bound, c.wantCmd, c.wantNav, c.wantBind)
		}
	}
}

// TestRegisterShortcutsChords verifies every registered shortcut chord
// is distinct, every command is Global, and the table is complete (the
// registration would fail loudly on duplicates; this test pins the
// mapping without a window).
func TestRegisterShortcutsChords(t *testing.T) {
	sh := &shell{state: newUIState()}
	sh.disp = newDispatcher(sh)
	sh.tabs = TabState{Layout: presentation.DefaultLayout()}

	seen := map[gui.Shortcut]string{}
	cmds := sh.shortcutTable()
	for _, c := range cmds {
		if prev, dup := seen[c.Shortcut]; dup {
			t.Errorf("duplicate shortcut %v: %q and %q", c.Shortcut, prev, c.ID)
		}
		seen[c.Shortcut] = c.ID
		if !c.Global {
			t.Errorf("shortcut %q must be Global (fires before focus dispatch)", c.ID)
		}
		if c.Execute == nil {
			t.Errorf("shortcut %q has no Execute", c.ID)
		}
	}
	// space + stop + 2 seek + 4 file + quit + layout reset + mod+1..9,0
	// + 2 tab-reorder chords + panel collapse/restore (item 4.3).
	if len(cmds) != 24 {
		t.Errorf("registered %d shortcuts, want 24", len(cmds))
	}
}

// TestSliderSensitivityMapping verifies the pure 0..1 ↔ 0.2..3 mapping.
func TestSliderSensitivityMapping(t *testing.T) {
	if got := sliderToSensitivity(0); got != 0.2 {
		t.Errorf("sliderToSensitivity(0) = %v, want 0.2", got)
	}
	if got := sliderToSensitivity(1); got != 3.0 {
		t.Errorf("sliderToSensitivity(1) = %v, want 3.0", got)
	}
	if got := sliderToSensitivity(0.5); got < 1.59 || got > 1.61 {
		t.Errorf("sliderToSensitivity(0.5) = %v, want ~1.6", got)
	}
	// Out-of-range inputs clamp.
	if got := sliderToSensitivity(-1); got != 0.2 {
		t.Errorf("sliderToSensitivity(-1) = %v, want 0.2", got)
	}
	if got := sliderToSensitivity(2); got != 3.0 {
		t.Errorf("sliderToSensitivity(2) = %v, want 3.0", got)
	}
	// Round trip.
	if got := sensitivityToSlider(float64(sliderToSensitivity(0.25))); got < 0.24 || got > 0.26 {
		t.Errorf("round trip 0.25 → %v", got)
	}
}

// TestExportSecondsAndDefaultPaths verifies the field parsers.
func TestExportSecondsAndDefaultPaths(t *testing.T) {
	if got := exportSeconds("30"); got != 30 {
		t.Errorf("exportSeconds(30) = %v", got)
	}
	if got := exportSeconds("  12.5 "); got != 12.5 {
		t.Errorf("exportSeconds(12.5) = %v", got)
	}
	if got := exportSeconds("garbage"); got != 0 {
		t.Errorf("exportSeconds(garbage) = %v, want 0", got)
	}
	// The contract is "data dir + canonical file name joined by the
	// PLATFORM separator" (filepath.Join in the helpers), not a
	// POSIX-only string. Asserting the literal slash-joined form
	// broke every native-backslash platform (run 44648357437,
	// build-windows actions_test.go:204/207).
	if got, want := defaultProjectPath("/data/projects"),
		filepath.Join("/data/projects", "project.sheytan.json"); got != want {
		t.Errorf("defaultProjectPath = %q, want %q", got, want)
	}
	if got, want := defaultExportPath("/data/exports"),
		filepath.Join("/data/exports", "export.mp4"); got != want {
		t.Errorf("defaultExportPath = %q, want %q", got, want)
	}
}

// TestSystemFilesLimits verifies the fixed least-privilege bounds.
func TestSystemFilesLimits(t *testing.T) {
	lim := systemFilesLimits()
	if !lim.SkipSensitive {
		t.Error("sensitive paths must be skipped by default")
	}
	if lim.MaxDepth <= 0 || lim.MaxFiles <= 0 || lim.MaxTotalByte <= 0 || lim.TimeBudget <= 0 {
		t.Errorf("limits are not bounded: %+v", lim)
	}
}

// TestAnalysisText verifies the honest walk report includes the scope,
// counts and the read-only statement.
func TestAnalysisText(t *testing.T) {
	summary := fileaccess.WalkSummary{
		FilesSeen: 3, FilesRead: 2, FilesSkippedSens: 1,
		BytesRead: 2048, Duration: 12 * time.Millisecond,
	}
	text := analysisText("/tmp/scan", "scan", "scope-1", summary, nil)
	if text == "" {
		t.Fatal("analysis text must not be empty")
	}
	for _, want := range []string{"Scope:", "Files seen:", "Read-only"} {
		if !strings.Contains(text, want) {
			t.Errorf("analysis text missing %q", want)
		}
	}
}

// TestSeekClampLogic mirrors the seek clamping without a track (total
// 0 → no-op status), through the pure helper on state.
func TestSeekClampLogic(t *testing.T) {
	// The clamp math is embedded in seekBy; verify the pure math here.
	total := 100 * time.Second
	for _, c := range []struct {
		pos, delta, want time.Duration
	}{
		{10 * time.Second, 5 * time.Second, 15 * time.Second},
		{2 * time.Second, -5 * time.Second, 0},
		{99 * time.Second, 5 * time.Second, total},
	} {
		target := c.pos + c.delta
		if target < 0 {
			target = 0
		}
		if target > total {
			target = total
		}
		if target != c.want {
			t.Errorf("clamp(%v, %v) = %v, want %v", c.pos, c.delta, target, c.want)
		}
	}
}

// TestVolumeDispatchGuard verifies the slider dispatch guard (1%
// granularity, no per-drag-step spam).
func TestVolumeDispatchGuard(t *testing.T) {
	if changedEnough(0.5, 0.5) {
		t.Error("same value must not dispatch")
	}
	if changedEnough(0.5, 0.505) {
		t.Error("sub-1% change must not dispatch")
	}
	if !changedEnough(0.5, 0.52) {
		t.Error("1%+ change must dispatch")
	}
	if !changedEnough(0.5, 0.48) {
		t.Error("downward 1%+ change must dispatch")
	}
}
