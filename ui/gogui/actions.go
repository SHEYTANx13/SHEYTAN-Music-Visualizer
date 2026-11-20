// actions.go — command dispatch for the Go-Gui shell: every
// presentation.CommandID used by this shell is mapped to a real handler
// or explicitly unsupported with its honest reason, and the canonical
// desktop keyboard shortcuts (from presentation.Shortcuts) are bound
// here through a pure, testable mapping registered once as go-gui
// window commands (Window.RegisterCommands, Global so they fire before
// focus dispatch).
//
// UI-thread rules: handlers run on the go-gui frame thread (widget
// callbacks and queued commands); anything that may block (import,
// export, project I/O, media-tools install, FFmpeg detection, file
// analysis) is launched on a goroutine and reports back through the
// state snapshot.

package gogui

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/go-gui-org/go-gui/gui"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/export"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/fileaccess"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/presentation"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/render"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/visualizer"
)

// dispatcher maps presentation command IDs to shell handlers.
type dispatcher struct {
	owner       *shell
	handlers    map[presentation.CommandID]func()
	unsupported map[presentation.CommandID]string
}

func newDispatcher(sh *shell) *dispatcher {
	d := &dispatcher{
		owner:       sh,
		handlers:    map[presentation.CommandID]func(){},
		unsupported: map[presentation.CommandID]string{},
	}
	d.bind(sh)
	return d
}

// Run executes one command and reports whether it was handled.
// Unsupported commands surface their honest reason in the status strip
// instead of silently doing nothing.
func (d *dispatcher) Run(id presentation.CommandID) bool {
	if fn, ok := d.handlers[id]; ok {
		fn()
		return true
	}
	if reason, ok := d.unsupported[id]; ok {
		d.owner.state.SetStatus("Not available: " + reason)
		return true
	}
	return false
}

// Supported lists the handled command IDs (audit/testing helper).
func (d *dispatcher) Supported() []presentation.CommandID {
	out := make([]presentation.CommandID, 0, len(d.handlers))
	for id := range d.handlers {
		out = append(out, id)
	}
	return out
}

// Unsupported lists the explicitly unsupported command IDs with reasons.
func (d *dispatcher) Unsupported() map[presentation.CommandID]string {
	out := make(map[presentation.CommandID]string, len(d.unsupported))
	for id, reason := range d.unsupported {
		out[id] = reason
	}
	return out
}

// bind installs every handler.
func (d *dispatcher) bind(sh *shell) {
	h := d.handlers
	st := sh.state
	svc := sh.svc

	// Transport.
	h[presentation.CmdPlayPause] = func() {
		if err := svc.PlayPause(); err != nil {
			st.SetStatus("Playback: " + err.Error())
		}
	}
	h[presentation.CmdStop] = func() {
		if err := svc.Stop(); err != nil {
			st.SetStatus("Stop: " + err.Error())
		} else {
			st.SetStatus("Stopped")
		}
	}
	h[presentation.CmdSeekBack] = func() { sh.seekBy(-5 * time.Second) }
	h[presentation.CmdSeekForward] = func() { sh.seekBy(5 * time.Second) }
	h[presentation.CmdSetVolume] = func() {
		svc.SetVolume(float64(sh.volume))
		st.SetStatus(fmt.Sprintf("Volume %.0f%%", sh.volume*100))
	}

	// Import / project.
	h[presentation.CmdImportAudio] = func() { sh.startImportPath("") }
	h[presentation.CmdProjectSave] = func() { sh.saveProjectPath("") }
	h[presentation.CmdProjectOpen] = func() { sh.openProjectPath("") }

	// Export.
	h[presentation.CmdExportStart] = func() { sh.startExport() }
	h[presentation.CmdExportCancel] = func() {
		if job := svc.ExportJob(); job != nil && job.Running() {
			job.Cancel()
			st.SetStatus("Export cancellation requested")
		} else {
			st.SetStatus("No export is running")
		}
	}

	// Visualizer configuration.
	h[presentation.CmdModeSet] = func() { sh.applyMode(visualizer.Mode(sh.modeValue)) }
	h[presentation.CmdPaletteSet] = func() { sh.applyPalette(sh.paletteValue) }
	h[presentation.CmdSensitivitySet] = func() { sh.applySensitivity() }
	h[presentation.CmdBackgroundLayerSet] = func() { sh.applyBackgroundLayer(visualizer.Mode(sh.bgValue)) }
	h[presentation.CmdPresetApply] = func() { sh.applySelectedPreset() }

	// Renderer preference: persisted and revalidated on next launch.
	// The renderer instance is closed ONLY at shutdown, so the swap can
	// never race a mid-flight frame.
	h[presentation.CmdRendererSelect] = func() { sh.applyRendererPreference(sh.prefValue) }

	// Theme.
	h[presentation.CmdIntensitySet] = func() {
		if sh.th.SetIntensity(sh.intensityValue) {
			st.SetStatus("Theme intensity: " + sh.intensityValue)
		} else {
			st.SetStatus("Unknown intensity preset: " + sh.intensityValue)
		}
	}
	h[presentation.CmdReducedMotionSet] = func() { sh.th.SetReducedMotion(sh.reducedMotion) }
	h[presentation.CmdReducedGlowSet] = func() { sh.th.SetReducedGlow(sh.reducedGlow) }

	// Navigation / workspace.
	h[presentation.CmdNavigate] = func() { sh.navigateIndex() }
	h[presentation.CmdTabsReset] = func() {
		sh.tabs.Reset()
		sh.syncTabs()
		sh.saveLayout()
		st.SetStatus("Tab order reset to the canonical order")
	}
	h[presentation.CmdLayoutReset] = func() { d.Run(presentation.CmdTabsReset) }

	// Media tools.
	h[presentation.CmdMediaToolsOpen] = func() {
		sh.tabs.SetActive(presentation.DestRequirements)
		sh.syncTabs()
	}
	h[presentation.CmdMediaToolsInstall] = func() { sh.startMediaToolsInstall() }

	// System Files analysis (two-step authorization inside the panel).
	h[presentation.CmdAnalyzePath] = func() { sh.analyzeSystemPath() }

	// Help / logs / clipboard / about.
	h[presentation.CmdHelpOpen] = func() {
		sh.tabs.SetActive(presentation.DestHome)
		sh.syncTabs()
		st.SetStatus("Help: every panel documents its own contract; the Home overview carries quick actions")
	}
	h[presentation.CmdLogsReveal] = func() { sh.revealLogs() }
	h[presentation.CmdCopyToClipboard] = func() {
		st.RequestClipboard(sh.diagnosticsText())
		st.SetStatus("Diagnostics summary queued for the clipboard")
	}
	h[presentation.CmdAbout] = func() {
		sh.tabs.SetActive(presentation.DestHome)
		sh.syncTabs()
		st.SetStatus(sh.brandAttribution())
	}

	// Quit: close the ONE window; teardown runs after backend.Run
	// returns.
	h[presentation.CmdQuit] = func() {
		if sh.win != nil {
			sh.win.Close()
		}
	}

	// Workspace composition commands (run 44648357437 item 4.3): real
	// persisted behavior behind every canonical ID — the honest-missing
	// stubs are gone. CmdTabsReorder's no-argument dispatcher form moves
	// the active tab one position toward the strip END; the
	// direction-aware paths are the strip's ‹/› controls and the
	// mod+alt+left/right chords.
	h[presentation.CmdTabsReorder] = func() {
		sh.moveActiveTab(1)
	}
	h[presentation.CmdSplitOrientation] = func() {
		sh.toggleSplitOrientation()
	}
	h[presentation.CmdPanelCollapse] = func() {
		sh.setSideCollapsed(true)
	}
	h[presentation.CmdPanelRestore] = func() {
		sh.setSideCollapsed(false)
	}
}

// shortcutModifier is the canonical desktop shortcut modifier of this
// platform: Super (Cmd) on macOS, Ctrl everywhere else. The pure
// shortcutFor mapping accepts either chord, so tests and future
// platforms share one table.
func shortcutModifier() gui.Modifier {
	if runtime.GOOS == "darwin" {
		return gui.ModSuper
	}
	return gui.ModCtrl
}

// shortcutFor maps a go-gui key event to a command and, for navigation,
// to the 1-based tab index (Ctrl+1..9, Ctrl+0 → 10). The bool reports
// whether the key combination is bound. This is the pure binding of
// presentation.Shortcuts for this shell:
//
//	space play/pause · mod+. stop · left/right seek · mod+i import
//	mod+s save · mod+o open · mod+e export · mod+1..9,0 navigate
//	mod+alt+0 layout reset · mod+q quit
//
// mod is the shortcut modifier: Ctrl (Windows/Linux) or Super (macOS).
// Unlike the Gio binding it replaces, arrow-key seek is exact-chord
// (no modifiers): go-gui matches shortcuts exactly, so a modifier
// combination is never silently reinterpreted.
func shortcutFor(code gui.KeyCode, mods gui.Modifier) (presentation.CommandID, int, bool) {
	mod := mods.HasAny(gui.ModCtrl, gui.ModSuper)
	alt := mods.Has(gui.ModAlt)
	switch code {
	case gui.KeySpace:
		if mods == 0 {
			return presentation.CmdPlayPause, 0, true
		}
	case gui.KeyLeft:
		if mods == 0 {
			return presentation.CmdSeekBack, 0, true
		}
		if mod && alt {
			// Keyboard reorder alternative (item 4.3): mod+alt+left moves
			// the ACTIVE tab toward the strip start.
			return presentation.CmdTabsReorder, -1, true
		}
	case gui.KeyRight:
		if mods == 0 {
			return presentation.CmdSeekForward, 0, true
		}
		if mod && alt {
			// Keyboard reorder alternative (item 4.3): mod+alt+right moves
			// the ACTIVE tab toward the strip end.
			return presentation.CmdTabsReorder, 1, true
		}
	case gui.KeyB:
		if mod && mods.Has(gui.ModShift) {
			return presentation.CmdPanelRestore, 0, true
		}
		if mod {
			return presentation.CmdPanelCollapse, 0, true
		}
	case gui.KeyI:
		if mod {
			return presentation.CmdImportAudio, 0, true
		}
	case gui.KeyS:
		if mod {
			return presentation.CmdProjectSave, 0, true
		}
	case gui.KeyO:
		if mod {
			return presentation.CmdProjectOpen, 0, true
		}
	case gui.KeyE:
		if mod {
			return presentation.CmdExportStart, 0, true
		}
	case gui.KeyQ:
		if mod {
			return presentation.CmdQuit, 0, true
		}
	case gui.KeyPeriod:
		if mod {
			return presentation.CmdStop, 0, true
		}
	case gui.Key0:
		if mod && alt {
			return presentation.CmdLayoutReset, 0, true
		}
		if mod {
			return presentation.CmdNavigate, 10, true
		}
	case gui.Key1, gui.Key2, gui.Key3, gui.Key4, gui.Key5,
		gui.Key6, gui.Key7, gui.Key8, gui.Key9:
		if mod {
			return presentation.CmdNavigate, int(code - gui.Key0), true
		}
	}
	return "", 0, false
}

// shortcutTable builds the global go-gui command table for the
// canonical desktop shortcuts. Pure (no window); the count and chord
// uniqueness are unit-tested.
func (sh *shell) shortcutTable() []gui.Command {
	mod := shortcutModifier()
	modAlt := mod | gui.ModAlt
	type binding struct {
		id    string
		label string
		key   gui.KeyCode
		mods  gui.Modifier
		cmd   presentation.CommandID
		nav   int
	}
	bindings := []binding{
		{"sc-play-pause", "Play/Pause", gui.KeySpace, gui.ModNone, presentation.CmdPlayPause, 0},
		{"sc-stop", "Stop", gui.KeyPeriod, mod, presentation.CmdStop, 0},
		{"sc-seek-back", "Seek −5s", gui.KeyLeft, gui.ModNone, presentation.CmdSeekBack, 0},
		{"sc-seek-forward", "Seek +5s", gui.KeyRight, gui.ModNone, presentation.CmdSeekForward, 0},
		{"sc-import", "Import audio", gui.KeyI, mod, presentation.CmdImportAudio, 0},
		{"sc-save", "Save project", gui.KeyS, mod, presentation.CmdProjectSave, 0},
		{"sc-open", "Open project", gui.KeyO, mod, presentation.CmdProjectOpen, 0},
		{"sc-export", "Export", gui.KeyE, mod, presentation.CmdExportStart, 0},
		{"sc-quit", "Quit", gui.KeyQ, mod, presentation.CmdQuit, 0},
		{"sc-layout-reset", "Reset workspace layout", gui.Key0, modAlt, presentation.CmdLayoutReset, 0},
		{"sc-tab-reorder-left", "Move tab left", gui.KeyLeft, modAlt, presentation.CmdTabsReorder, -1},
		{"sc-tab-reorder-right", "Move tab right", gui.KeyRight, modAlt, presentation.CmdTabsReorder, 1},
		{"sc-panel-collapse", "Collapse side panel", gui.KeyB, mod, presentation.CmdPanelCollapse, 0},
		{"sc-panel-restore", "Restore side panel", gui.KeyB, mod | gui.ModShift, presentation.CmdPanelRestore, 0},
		{"sc-navigate-10", "Open destination 10", gui.Key0, mod, presentation.CmdNavigate, 10},
		{"sc-navigate-1", "Open destination 1", gui.Key1, mod, presentation.CmdNavigate, 1},
		{"sc-navigate-2", "Open destination 2", gui.Key2, mod, presentation.CmdNavigate, 2},
		{"sc-navigate-3", "Open destination 3", gui.Key3, mod, presentation.CmdNavigate, 3},
		{"sc-navigate-4", "Open destination 4", gui.Key4, mod, presentation.CmdNavigate, 4},
		{"sc-navigate-5", "Open destination 5", gui.Key5, mod, presentation.CmdNavigate, 5},
		{"sc-navigate-6", "Open destination 6", gui.Key6, mod, presentation.CmdNavigate, 6},
		{"sc-navigate-7", "Open destination 7", gui.Key7, mod, presentation.CmdNavigate, 7},
		{"sc-navigate-8", "Open destination 8", gui.Key8, mod, presentation.CmdNavigate, 8},
		{"sc-navigate-9", "Open destination 9", gui.Key9, mod, presentation.CmdNavigate, 9},
	}
	cmds := make([]gui.Command, 0, len(bindings))
	for _, b := range bindings {
		b := b
		cmds = append(cmds, gui.Command{
			ID:       b.id,
			Label:    b.label,
			Shortcut: gui.Shortcut{Key: b.key, Modifiers: b.mods},
			Global:   true,
			Execute: func(e *gui.Event, w *gui.Window) {
				if b.cmd == presentation.CmdNavigate {
					if dest, ok := TabForShortcut(sh.tabs.Layout.TabOrder, b.nav); ok {
						sh.tabs.SetActive(dest)
						sh.syncTabs()
						sh.state.SetStatus("Destination: " + string(dest))
					}
					return
				}
				if b.cmd == presentation.CmdTabsReorder {
					// The nav field carries the move direction for the
					// reorder chords (item 4.3 keyboard path).
					sh.moveActiveTab(b.nav)
					return
				}
				sh.disp.Run(b.cmd)
			},
		})
	}
	return cmds
}

// registerShortcuts binds the canonical desktop shortcut table as
// global go-gui window commands (fired before focus dispatch, so a
// focused input cannot swallow them). Global commands whose chord is
// already taken by a widget would be a registration error; this table
// owns every chord it registers.
func (sh *shell) registerShortcuts(w *gui.Window) {
	if err := w.RegisterCommands(sh.shortcutTable()...); err != nil {
		// A duplicate chord is a programming bug in this table; report it
		// honestly instead of failing the window open.
		sh.state.SetStatus("Keyboard shortcuts partially unavailable: " + err.Error())
	}
}

// navigateIndex applies the mod+1..9,0 navigation selection when it
// arrives through the command table rather than a specific binding.
func (sh *shell) navigateIndex() {
	sh.state.SetStatus("Navigate: use mod+1..9,0 or the tab strip")
}

// chooseImportPath opens the native open-file dialog for an audio
// file. Where the platform has no native picker, go-gui falls back to
// its in-window file browser — the dialog never fakes success. The
// chosen path is a user-mediated selection only: every authorization
// check stays in the Go core (fileaccess) inside the import path.
func (sh *shell) chooseImportPath() {
	sh.win.NativeOpenDialog(gui.NativeOpenDialogCfg{
		Title: "Import audio file",
		Filters: []gui.NativeFileFilter{
			{Name: "Audio (wav, mp3, flac, ogg)", Extensions: []string{"wav", "mp3", "flac", "ogg"}},
		},
		OnDone: func(res gui.NativeDialogResult, w *gui.Window) {
			switch res.Status {
			case gui.DialogOK:
				paths := res.PathStrings()
				if len(paths) == 0 {
					sh.state.SetStatus("Import dialog returned no file; use the path entry")
					return
				}
				sh.startImportPath(paths[0])
			case gui.DialogError:
				sh.state.SetStatus("Import dialog unavailable (" + res.ErrorMessage + "); use the path entry")
			case gui.DialogCancel, gui.DialogDiscard:
				sh.state.SetStatus("Import canceled")
			}
		},
	})
}

// chooseProjectPath opens the native open-file dialog for a project
// file and loads it (the in-window file browser is the documented
// fallback).
func (sh *shell) chooseProjectPath() {
	sh.win.NativeOpenDialog(gui.NativeOpenDialogCfg{
		Title: "Open project",
		Filters: []gui.NativeFileFilter{
			{Name: "SHEYTAN project (json)", Extensions: []string{"json"}},
		},
		OnDone: func(res gui.NativeDialogResult, w *gui.Window) {
			switch res.Status {
			case gui.DialogOK:
				paths := res.PathStrings()
				if len(paths) == 0 {
					sh.state.SetStatus("Open dialog returned no file; use the path entry")
					return
				}
				sh.projectPath = paths[0]
				sh.openProjectPath(paths[0])
			case gui.DialogError:
				sh.state.SetStatus("Open dialog unavailable (" + res.ErrorMessage + "); use the path entry")
			case gui.DialogCancel, gui.DialogDiscard:
				sh.state.SetStatus("Open project canceled")
			}
		},
	})
}

// chooseProjectSavePath opens the native save-file dialog for the
// project write (the in-window file browser is the documented
// fallback).
func (sh *shell) chooseProjectSavePath() {
	sh.win.NativeSaveDialog(gui.NativeSaveDialogCfg{
		Title:            "Save project",
		DefaultName:      "project.sheytan.json",
		DefaultExtension: "json",
		Filters: []gui.NativeFileFilter{
			{Name: "SHEYTAN project (json)", Extensions: []string{"json"}},
		},
		ConfirmOverwrite: true,
		OnDone: func(res gui.NativeDialogResult, w *gui.Window) {
			switch res.Status {
			case gui.DialogOK:
				paths := res.PathStrings()
				if len(paths) == 0 {
					sh.state.SetStatus("Save dialog returned no path; use the path entry")
					return
				}
				sh.projectPath = paths[0]
				sh.saveProjectPath(paths[0])
			case gui.DialogError:
				sh.state.SetStatus("Save dialog unavailable (" + res.ErrorMessage + "); use the path entry")
			case gui.DialogCancel, gui.DialogDiscard:
				sh.state.SetStatus("Save project canceled")
			}
		},
	})
}

// chooseExportPath opens the native save-file dialog for the export
// output and stores the choice in the export output entry (the export
// itself only starts on the explicit Start export action).
func (sh *shell) chooseExportPath() {
	sh.win.NativeSaveDialog(gui.NativeSaveDialogCfg{
		Title:            "Choose export output",
		DefaultName:      "export.mp4",
		DefaultExtension: "mp4",
		Filters: []gui.NativeFileFilter{
			{Name: "MP4 video", Extensions: []string{"mp4"}},
		},
		ConfirmOverwrite: true,
		OnDone: func(res gui.NativeDialogResult, w *gui.Window) {
			switch res.Status {
			case gui.DialogOK:
				paths := res.PathStrings()
				if len(paths) == 0 {
					sh.state.SetStatus("Save dialog returned no path; use the path entry")
					return
				}
				sh.outPath = paths[0]
				sh.state.SetStatus("Export output: " + shortName(paths[0]))
			case gui.DialogError:
				sh.state.SetStatus("Save dialog unavailable (" + res.ErrorMessage + "); use the path entry")
			case gui.DialogCancel, gui.DialogDiscard:
				sh.state.SetStatus("Export output selection canceled")
			}
		},
	})
}

// revealLogs opens the log directory through the platform file-manager
// portal (xdg-open style). This is the ONLY external process the shell
// may launch, and failures are reported honestly.
func (sh *shell) revealLogs() {
	tool := "xdg-open"
	switch runtime.GOOS {
	case "windows":
		tool = "explorer"
	case "darwin":
		tool = "open"
	}
	cmd := execCommand(tool, sh.lay.Logs)
	if err := cmd.Start(); err != nil {
		sh.state.SetStatus(fmt.Sprintf("Could not reveal logs (%s): %v — the log directory is %s", tool, err, sh.lay.Logs))
		return
	}
	go func() { _ = cmd.Wait() }()
	sh.state.SetStatus("Log directory revealed: " + sh.lay.Logs)
}

// seekBy seeks relative to the current playhead, clamped to the track.
func (sh *shell) seekBy(delta time.Duration) {
	clk := sh.svc.Clock()
	if clk.Total <= 0 {
		sh.state.SetStatus("Nothing to seek — import a track first")
		return
	}
	target := clk.Position + delta
	if target < 0 {
		target = 0
	}
	if target > clk.Total {
		target = clk.Total
	}
	if err := sh.svc.Seek(target); err != nil {
		sh.state.SetStatus("Seek: " + err.Error())
	}
}

// startImportPath runs the import for an explicit path (the native
// dialog result) or for the path-entry field when path is empty, on a
// goroutine.
func (sh *shell) startImportPath(path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = strings.TrimSpace(sh.importPath)
	}
	if path == "" {
		sh.state.SetStatus("Import: pick a file with the Import dialog or enter an audio file path first")
		return
	}
	sh.importPath = path
	sh.rememberImportPath(path)
	sh.state.SetStatus("Import requested: " + shortName(path))
	svc, st := sh.svc, sh.state
	go func() {
		svc.UpdateMediaSource(path)
		if err := svc.ImportFile(path); err != nil {
			st.SetStatus("Import failed: " + err.Error())
		}
	}()
}

// saveProjectPath writes the project to an explicit path (the native
// save dialog result) or to the entered/default path when path is
// empty (goroutine). An empty result falls back to the default inside
// the projects data dir.
func (sh *shell) saveProjectPath(path string) {
	if sh.svc.Track() == nil {
		sh.state.SetStatus("Save project: nothing to save — import a track first")
		return
	}
	path = strings.TrimSpace(path)
	if path == "" {
		path = strings.TrimSpace(sh.projectPath)
	}
	if path == "" {
		path = defaultProjectPath(sh.lay.Projects)
	}
	sh.projectPath = path
	svc, st := sh.svc, sh.state
	go func() {
		if err := svc.SaveProject(path); err != nil {
			st.SetStatus("Save project failed: " + err.Error())
		}
	}()
}

// openProjectPath loads the project at an explicit path (the native
// open dialog result) or at the entered path when path is empty
// (goroutine).
func (sh *shell) openProjectPath(path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		path = strings.TrimSpace(sh.projectPath)
	}
	if path == "" {
		sh.state.SetStatus("Open project: pick a file with the Open dialog or enter a project .json path first")
		return
	}
	sh.projectPath = path
	svc, st := sh.svc, sh.state
	go func() {
		if err := svc.LoadProject(path); err != nil {
			st.SetStatus("Open project failed: " + err.Error())
			return
		}
		st.SetConfig(svc.Config())
		st.SetTrack(svc.Track())
	}()
}

// applyMode pushes the visualizer mode change.
func (sh *shell) applyMode(m visualizer.Mode) {
	cfg := sh.svc.Config()
	cfg.Mode = m
	cfg.Clamp()
	sh.svc.SetConfig(cfg)
	sh.state.SetConfig(sh.svc.Config())
}

// applyPalette pushes the palette index change (value "0".."3").
func (sh *shell) applyPalette(v string) {
	idx := 0
	fmt.Sscanf(v, "%d", &idx)
	cfg := sh.svc.Config()
	cfg.Palette = idx
	cfg.Clamp()
	sh.svc.SetConfig(cfg)
	sh.state.SetConfig(sh.svc.Config())
}

// applySensitivity pushes the sensitivity slider change (0..1 widget
// value mapped onto the 0.2..3 contract range).
func (sh *shell) applySensitivity() {
	cfg := sh.svc.Config()
	cfg.Sensitivity = sliderToSensitivity(float64(sh.sens))
	cfg.Clamp()
	sh.svc.SetConfig(cfg)
}

// applyGlow pushes the glow slider change (0..1 widget value mapped
// onto the 0..1.5 contract range).
func (sh *shell) applyGlow() {
	cfg := sh.svc.Config()
	cfg.Glow = float64(sh.glow) * glowMax
	cfg.Clamp()
	sh.svc.SetConfig(cfg)
}

// applyBackgroundIntensity pushes the background intensity slider.
func (sh *shell) applyBackgroundIntensity() {
	cfg := sh.svc.Config()
	cfg.BackgroundIntensity = float64(sh.bgInt) * backgroundIntensityMax
	cfg.Clamp()
	sh.svc.SetConfig(cfg)
}

// applyBackgroundLayer pushes the background composition layer change
// ("none" disables the layer).
func (sh *shell) applyBackgroundLayer(m visualizer.Mode) {
	cfg := sh.svc.Config()
	cfg.BackgroundLayer = m
	cfg.Clamp()
	sh.svc.SetConfig(cfg)
}

// applySelectedPreset applies the template selected in the panel.
func (sh *shell) applySelectedPreset() {
	name := sh.selectedPreset
	if name == "" {
		sh.state.SetStatus("Preset: select a template first")
		return
	}
	if !sh.svc.ApplyPreset(name) {
		sh.state.SetStatus("Preset not found: " + name)
		return
	}
	sh.state.SetConfig(sh.svc.Config())
	sh.state.SetActivePreset(name)
	sh.state.SetStatus("Preset applied: " + name)
}

// applyRendererPreference persists the auto/gpu/cpu mapping; gpu maps
// onto the Vulkan preference of the render preference store.
func (sh *shell) applyRendererPreference(choice string) {
	var pref render.Preference
	switch choice {
	case "auto":
		pref = render.PrefAuto
	case "gpu":
		pref = render.PrefVulkan
	case "cpu":
		pref = render.PrefCPU
	default:
		sh.state.SetStatus("Unknown renderer preference: " + choice)
		return
	}
	if err := sh.prefStore.Save(pref); err != nil {
		sh.state.SetStatus("Renderer preference could not be persisted: " + err.Error())
		return
	}
	sh.state.SetStatus("Renderer preference persisted: " + choice +
		" (revalidated on next launch; the running renderer stays active until shutdown)")
}

// startExport validates the export fields and launches the job on a
// goroutine (never blocks the frame thread).
func (sh *shell) startExport() {
	if sh.svc.Track() == nil {
		sh.state.SetStatus("Export: import a track before exporting")
		return
	}
	out := strings.TrimSpace(sh.outPath)
	if out == "" {
		out = defaultExportPath(sh.lay.Exports)
		sh.outPath = out
	}
	if _, err := export.SanitizeOutputPath(out); err != nil {
		sh.state.SetStatus("Export: " + err.Error())
		return
	}
	w, h, ok := export.ResolutionByID(sh.resValue)
	if !ok {
		sh.state.SetStatus("Export: unknown resolution preset " + sh.resValue)
		return
	}
	fps := 30
	fmt.Sscanf(sh.fpsValue, "%d", &fps)
	seconds := exportSeconds(sh.seconds)
	if seconds <= 0 {
		sh.state.SetStatus("Export: seconds must be a positive number")
		return
	}
	codec := export.CodecH264
	if sh.codecValue == "hevc" {
		codec = export.CodecHEVC
	}
	req := export.Request{
		OutputPath:    out,
		Width:         w,
		Height:        h,
		FPS:           fps,
		FPSRate:       export.IntFrameRate(fps),
		VideoCodec:    codec,
		QualityPreset: "balanced",
		Encoder:       "auto",
		AudioBitrate:  "192k",
		PreferGPU:     true,
	}
	svc, st := sh.svc, sh.state
	go func() {
		if err := svc.StartExport(req); err != nil {
			st.SetStatus("Export failed: " + err.Error())
		}
	}()
}

// startMediaToolsInstall runs the pinned tool pack install on a
// goroutine and forces an FFmpeg re-detection afterwards.
func (sh *shell) startMediaToolsInstall() {
	mt, st := sh.mediaTools, sh.state
	go func() {
		st.SetStatus("Media tools install started")
		if err := mt.Install(context.Background()); err != nil {
			st.SetStatus("Media tools install failed: " + err.Error())
			return
		}
		export.SetPathOverride("")
		if _, err := export.DetectEnv(true); err != nil {
			st.SetStatus("Media tools installed; FFmpeg detection still pending: " + err.Error())
			return
		}
		st.SetStatus("Media tools installed and FFmpeg re-detected")
	}()
}

// analyzeSystemPath runs the two-step System Files analysis: step 0
// prepares the authorization and shows the summary; step 1 (after the
// explicit user confirmation) authorizes and runs the bounded walk on
// a goroutine.
func (sh *shell) analyzeSystemPath() {
	root := strings.TrimSpace(sh.sysPath)
	if root == "" {
		sh.state.SetStatus("System Files: enter a file or folder path to analyze")
		return
	}
	step := sh.state.SysFilesStep()
	if step == 0 {
		req, err := sh.fileAccess.PrepareAuthorization(root, "user-selected file/folder analysis",
			"gogui-system-files", "user", systemFilesLimits())
		if err != nil {
			sh.state.SetStatus("System Files: " + err.Error())
			return
		}
		sh.pendingAuthReq = req
		sh.state.SetSysFilesSummary(req.SummaryLines())
		sh.state.SetSysFilesStep(1)
		sh.state.SetStatus("System Files: review the authorization summary, then confirm")
		return
	}
	req := sh.pendingAuthReq
	if req == nil {
		sh.state.SetSysFilesStep(0)
		sh.state.SetStatus("System Files: no pending authorization — start again")
		return
	}
	gr, err := sh.fileAccess.ConfirmAuthorization(req)
	if err != nil {
		sh.state.SetSysFilesStep(0)
		sh.state.SetStatus("System Files: authorization failed: " + err.Error())
		return
	}
	sh.state.SetSysFilesAnalyzing(true)
	sh.state.SetSysFilesStep(0)
	st, fa := sh.state, sh.fileAccess
	go func() {
		defer st.SetSysFilesAnalyzing(false)
		summary, entries, err := runBoundedAnalysis(gr, fa)
		if err != nil {
			st.SetSysFilesText("Analysis failed: " + err.Error())
			st.SetStatus("System Files: analysis failed: " + err.Error())
			return
		}
		st.SetSysFilesText(analysisText(gr.RootPath, gr.RootLabel, gr.ScopeID, summary, entries))
		st.SetStatus("System Files: analysis complete")
	}()
}

// systemFilesLimits returns the fixed, documented bounds for the
// System Files walk (least privilege: read-only, depth-capped,
// byte-capped, time-capped, sensitive paths skipped).
func systemFilesLimits() fileaccess.Limits {
	return fileaccess.Limits{
		MaxDepth:      fileaccess.DefaultMaxDepth,
		MaxFiles:      fileaccess.DefaultMaxFiles,
		MaxTotalByte:  fileaccess.DefaultMaxTotalByte,
		MaxFileByte:   fileaccess.DefaultMaxFileByte,
		TimeBudget:    fileaccess.DefaultTimeBudget,
		SkipSensitive: true,
	}
}

// runBoundedAnalysis walks the granted root and returns the summary
// plus the capped entry list. The retained detail list is capped
// separately from the walk bounds (the audit trail records the rest).
func runBoundedAnalysis(gr *fileaccess.Grant, fa *fileaccess.Manager) (fileaccess.WalkSummary, []fileaccess.FileEntry, error) {
	var entries []fileaccess.FileEntry
	summary, err := gr.Analyze(context.Background(), func(ctx context.Context, e fileaccess.FileEntry) error {
		if len(entries) < analysisEntryCap {
			entries = append(entries, e)
		}
		return nil
	})
	if err != nil {
		return summary, nil, err
	}
	fa.RecordWalk(gr, summary, fileaccess.OutcomeCompleted, "gogui-system-files")
	return summary, entries, nil
}

// analysisText renders the honest walk report.
func analysisText(rootPath, label, scopeID string, s fileaccess.WalkSummary, entries []fileaccess.FileEntry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Scope: %s (label %s, id %s)\n", rootPath, label, scopeID)
	fmt.Fprintf(&b, "Files seen: %d · read: %d · skipped non-regular: %d · skipped by type: %d · skipped sensitive: %d\n",
		s.FilesSeen, s.FilesRead, s.FilesSkippedType, s.FilesSkippedExt, s.FilesSkippedSens)
	fmt.Fprintf(&b, "Bytes read: %d · duration: %s · cancelled: %v · capped: %s\n",
		s.BytesRead, s.Duration.Round(time.Millisecond), s.Cancelled, s.Capped)
	if len(s.Errors) > 0 {
		fmt.Fprintf(&b, "Errors (%d): %s\n", len(s.Errors), strings.Join(s.Errors, "; "))
	}
	for _, e := range entries {
		fmt.Fprintf(&b, "  %10d  %s\n", e.Size, e.RelPath)
	}
	if s.FilesSeen > len(entries) {
		fmt.Fprintf(&b, "… %d further entries seen; details capped at %d (the audit trail carries the counts)\n",
			s.FilesSeen-len(entries), analysisEntryCap)
	}
	b.WriteString("Read-only analysis; nothing was written, uploaded or executed.")
	return b.String()
}

// diagnosticsText renders the diagnostics summary used by the
// clipboard command.
func (sh *shell) diagnosticsText() string {
	var b strings.Builder
	b.WriteString(sh.buildIdentityText())
	b.WriteString("\n")
	b.WriteString(sh.dataLayoutText())
	b.WriteString("\n")
	b.WriteString(sh.rendererText())
	return b.String()
}

// brandAttribution renders the two-party attribution block.
func (sh *shell) brandAttribution() string {
	return strings.Join(sh.attrLines, " · ")
}

// defaultProjectPath builds the default project file path inside the
// projects data directory.
func defaultProjectPath(projectsDir string) string {
	return filepath.Join(projectsDir, "project.sheytan.json")
}

// defaultExportPath builds the default export path inside the exports
// data directory.
func defaultExportPath(exportsDir string) string {
	return filepath.Join(exportsDir, "export.mp4")
}

// exportSeconds parses the seconds field (0 on garbage).
func exportSeconds(s string) float64 {
	var v float64
	fmt.Sscanf(strings.TrimSpace(s), "%g", &v)
	return v
}

// sliderToSensitivity is the pure 0..1 → 0.2..3 mapping.
func sliderToSensitivity(v float64) float64 {
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	return sensitivityMin + v*(sensitivityMax-sensitivityMin)
}

// sensitivityToSlider is the pure 0.2..3 → 0..1 mapping.
func sensitivityToSlider(s float64) float32 {
	if s < sensitivityMin {
		s = sensitivityMin
	}
	if s > sensitivityMax {
		s = sensitivityMax
	}
	return float32((s - sensitivityMin) / (sensitivityMax - sensitivityMin))
}

const (
	sensitivityMin = 0.2
	sensitivityMax = 3.0

	glowMax = 1.5

	// analysisEntryCap bounds the retained entry details of the
	// System Files report (the walk itself is bounded by its Limits).
	analysisEntryCap = 64
)
