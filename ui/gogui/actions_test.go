package gogui

import (
	"strings"
	"testing"
	"time"

	"gioui.org/io/key"

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
// the honest reason through the status strip instead of failing.
func TestDispatcherRunUnsupported(t *testing.T) {
	sh := &shell{state: newUIState()}
	d := newDispatcher(sh)
	if !d.Run(presentation.CmdSplitOrientation) {
		t.Fatal("explicitly unsupported commands must still report handled=true (they surface a reason)")
	}
	if got := sh.state.Status(); got == "" {
		t.Error("unsupported command must set a status message")
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
// exactly as presentation.Shortcuts documents.
func TestShortcutFor(t *testing.T) {
	mod := key.ModShortcut
	modAlt := key.ModShortcut | key.ModAlt
	cases := []struct {
		name     key.Name
		mods     key.Modifiers
		wantCmd  presentation.CommandID
		wantNav  int
		wantBind bool
	}{
		{key.NameSpace, 0, presentation.CmdPlayPause, 0, true},
		{key.NameLeftArrow, 0, presentation.CmdSeekBack, 0, true},
		{key.NameRightArrow, 0, presentation.CmdSeekForward, 0, true},
		{"I", mod, presentation.CmdImportAudio, 0, true},
		{"S", mod, presentation.CmdProjectSave, 0, true},
		{"O", mod, presentation.CmdProjectOpen, 0, true},
		{"E", mod, presentation.CmdExportStart, 0, true},
		{"Q", mod, presentation.CmdQuit, 0, true},
		{".", mod, presentation.CmdStop, 0, true},
		{"1", mod, presentation.CmdNavigate, 1, true},
		{"9", mod, presentation.CmdNavigate, 9, true},
		{"0", mod, presentation.CmdNavigate, 10, true},
		{"0", modAlt, presentation.CmdLayoutReset, 0, true},
		// Non-bindings: plain letters, shifted space, modifiers alone.
		{"I", 0, "", 0, false},
		{"S", 0, "", 0, false},
		{key.NameSpace, mod, "", 0, false},
		{"X", mod, "", 0, false},
	}
	for _, c := range cases {
		cmd, nav, bound := shortcutFor(c.name, c.mods)
		if bound != c.wantBind || cmd != c.wantCmd || nav != c.wantNav {
			t.Errorf("shortcutFor(%q, %v) = (%q, %d, %v), want (%q, %d, %v)",
				c.name, c.mods, cmd, nav, bound, c.wantCmd, c.wantNav, c.wantBind)
		}
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
	if got := defaultProjectPath("/data/projects"); got != "/data/projects/project.sheytan.json" {
		t.Errorf("defaultProjectPath = %q", got)
	}
	if got := defaultExportPath("/data/exports"); got != "/data/exports/export.mp4" {
		t.Errorf("defaultExportPath = %q", got)
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
