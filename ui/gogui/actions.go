// actions.go — command dispatch for the Gio shell: every
// presentation.CommandID used by this shell is mapped to a real handler
// or explicitly unsupported with its honest reason, and the canonical
// desktop keyboard shortcuts (from presentation.Shortcuts) are bound
// here through a pure, testable mapping.
//
// UI-thread rules: handlers run on the event loop; anything that may
// block (import, export, project I/O, media-tools install, FFmpeg
// detection, file analysis) is launched on a goroutine and reports back
// through the state snapshot.

package gogui

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/system"
	"gioui.org/layout"

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
		svc.SetVolume(float64(sh.volume.Value))
		st.SetStatus(fmt.Sprintf("Volume %.0f%%", sh.volume.Value*100))
	}

	// Import / project.
	h[presentation.CmdImportAudio] = func() { sh.startImport() }
	h[presentation.CmdProjectSave] = func() { sh.saveProject() }
	h[presentation.CmdProjectOpen] = func() { sh.openProject() }

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
	h[presentation.CmdModeSet] = func() { sh.applyMode(visualizer.Mode(sh.modeEnum.Value)) }
	h[presentation.CmdPaletteSet] = func() { sh.applyPalette(sh.paletteEnum.Value) }
	h[presentation.CmdSensitivitySet] = func() { sh.applySensitivity() }
	h[presentation.CmdBackgroundLayerSet] = func() { sh.applyBackgroundLayer(visualizer.Mode(sh.bgEnum.Value)) }
	h[presentation.CmdPresetApply] = func() { sh.applySelectedPreset() }

	// Renderer preference: persisted and revalidated on next launch.
	// The renderer instance is closed ONLY at shutdown, so the swap can
	// never race a mid-flight frame.
	h[presentation.CmdRendererSelect] = func() { sh.applyRendererPreference(sh.rendererPrefEnum.Value) }

	// Theme.
	h[presentation.CmdIntensitySet] = func() {
		if sh.th.SetIntensity(sh.intensityEnum.Value) {
			st.SetStatus("Theme intensity: " + sh.intensityEnum.Value)
		} else {
			st.SetStatus("Unknown intensity preset: " + sh.intensityEnum.Value)
		}
	}
	h[presentation.CmdReducedMotionSet] = func() { sh.th.SetReducedMotion(sh.reducedMotionSw.Value) }
	h[presentation.CmdReducedGlowSet] = func() { sh.th.SetReducedGlow(sh.reducedGlowSw.Value) }

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

	// Quit: close the ONE window; teardown runs on DestroyEvent.
	h[presentation.CmdQuit] = func() {
		sh.win.Perform(system.ActionClose)
	}

	// Explicitly unsupported commands — honest reasons, never silent no-ops.
	d.unsupported[presentation.CmdTabsReorder] =
		"drag-and-drop tab reordering is not implemented in this vertical slice; " +
			"the persisted store round-trips a user order written by any shell and Reset restores the canonical order"
	d.unsupported[presentation.CmdSplitOrientation] =
		"the split orientation follows the responsive breakpoint from the design tokens instead of a manual toggle"
	d.unsupported[presentation.CmdPanelCollapse] =
		"panel collapse is not implemented in this vertical slice; every destination renders its full surface"
	d.unsupported[presentation.CmdPanelRestore] =
		"panel collapse is not implemented in this vertical slice; there is nothing collapsed to restore"
}

// shortcutFor maps a keyboard event to a command and, for navigation,
// to the 1-based tab index (mod+1..9, mod+0 → 10). The bool reports
// whether the key combination is bound. This is the pure binding of
// presentation.Shortcuts for this shell:
//
//	mod+i import · space play/pause · mod+. stop · left/right seek
//	mod+s save · mod+o open · mod+e export · mod+1..9,0 navigate
//	mod+alt+0 layout reset · mod+q quit
func shortcutFor(name key.Name, mods key.Modifiers) (presentation.CommandID, int, bool) {
	mod := mods.Contain(key.ModShortcut)
	alt := mods.Contain(key.ModAlt)
	switch name {
	case key.NameSpace:
		if mods == 0 {
			return presentation.CmdPlayPause, 0, true
		}
	case key.NameLeftArrow:
		return presentation.CmdSeekBack, 0, true
	case key.NameRightArrow:
		return presentation.CmdSeekForward, 0, true
	case "I":
		if mod {
			return presentation.CmdImportAudio, 0, true
		}
	case "S":
		if mod {
			return presentation.CmdProjectSave, 0, true
		}
	case "O":
		if mod {
			return presentation.CmdProjectOpen, 0, true
		}
	case "E":
		if mod {
			return presentation.CmdExportStart, 0, true
		}
	case "Q":
		if mod {
			return presentation.CmdQuit, 0, true
		}
	case ".":
		if mod {
			return presentation.CmdStop, 0, true
		}
	case "0":
		if mod && alt {
			return presentation.CmdLayoutReset, 0, true
		}
		if mod {
			return presentation.CmdNavigate, 10, true
		}
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		if mod {
			return presentation.CmdNavigate, int(name[0] - '0'), true
		}
	}
	return "", 0, false
}

// handleKeys processes the window-level key events of one frame and
// dispatches the mapped commands.
func (sh *shell) handleKeys(gtx layout.Context) {
	filters := []event.Filter{
		key.Filter{Required: key.ModShortcut, Optional: key.ModShortcutAlt, Name: "I"},
		key.Filter{Required: key.ModShortcut, Optional: key.ModShortcutAlt, Name: "S"},
		key.Filter{Required: key.ModShortcut, Optional: key.ModShortcutAlt, Name: "O"},
		key.Filter{Required: key.ModShortcut, Optional: key.ModShortcutAlt, Name: "E"},
		key.Filter{Required: key.ModShortcut, Optional: key.ModShortcutAlt, Name: "Q"},
		key.Filter{Required: key.ModShortcut, Optional: key.ModShortcutAlt, Name: "."},
		key.Filter{Required: key.ModShortcut, Optional: key.ModShortcutAlt, Name: "0"},
		key.Filter{Required: key.ModShortcut, Optional: key.ModShortcutAlt, Name: "1"},
		key.Filter{Required: key.ModShortcut, Optional: key.ModShortcutAlt, Name: "2"},
		key.Filter{Required: key.ModShortcut, Optional: key.ModShortcutAlt, Name: "3"},
		key.Filter{Required: key.ModShortcut, Optional: key.ModShortcutAlt, Name: "4"},
		key.Filter{Required: key.ModShortcut, Optional: key.ModShortcutAlt, Name: "5"},
		key.Filter{Required: key.ModShortcut, Optional: key.ModShortcutAlt, Name: "6"},
		key.Filter{Required: key.ModShortcut, Optional: key.ModShortcutAlt, Name: "7"},
		key.Filter{Required: key.ModShortcut, Optional: key.ModShortcutAlt, Name: "8"},
		key.Filter{Required: key.ModShortcut, Optional: key.ModShortcutAlt, Name: "9"},
		key.Filter{Name: key.NameSpace},
		key.Filter{Name: key.NameLeftArrow},
		key.Filter{Name: key.NameRightArrow},
	}
	for {
		ev, ok := gtx.Event(filters...)
		if !ok {
			return
		}
		ke, isKey := ev.(key.Event)
		if !isKey || ke.State != key.Press {
			continue
		}
		cmd, navIndex, bound := shortcutFor(ke.Name, ke.Modifiers)
		if !bound {
			continue
		}
		if cmd == presentation.CmdNavigate {
			if dest, ok := TabForShortcut(sh.tabs.Layout.TabOrder, navIndex); ok {
				sh.tabs.SetActive(dest)
				sh.syncTabs()
				sh.state.SetStatus("Destination: " + string(dest))
			}
			continue
		}
		sh.disp.Run(cmd)
	}
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

// startImport runs the import from the path entry on a goroutine.
func (sh *shell) startImport() {
	path := strings.TrimSpace(sh.importPathEd.Text())
	if path == "" {
		sh.state.SetStatus("Import: enter an audio file path first (no native file picker in this slice)")
		return
	}
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

// saveProject writes the project to the entered path (goroutine). An
// empty path falls back to the default inside the projects data dir.
func (sh *shell) saveProject() {
	if sh.svc.Track() == nil {
		sh.state.SetStatus("Save project: nothing to save — import a track first")
		return
	}
	path := strings.TrimSpace(sh.projectPathEd.Text())
	if path == "" {
		path = defaultProjectPath(sh.lay.Projects)
		sh.projectPathEd.SetText(path)
	}
	svc, st := sh.svc, sh.state
	go func() {
		if err := svc.SaveProject(path); err != nil {
			st.SetStatus("Save project failed: " + err.Error())
		}
	}()
}

// openProject loads the project at the entered path (goroutine).
func (sh *shell) openProject() {
	path := strings.TrimSpace(sh.projectPathEd.Text())
	if path == "" {
		sh.state.SetStatus("Open project: enter a project .json path first")
		return
	}
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

// applyPalette pushes the palette index change (enum value "0".."3").
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
	cfg.Sensitivity = sliderToSensitivity(float64(sh.sens.Value))
	cfg.Clamp()
	sh.svc.SetConfig(cfg)
}

// applyGlow pushes the glow slider change (0..1 widget value mapped
// onto the 0..1.5 contract range).
func (sh *shell) applyGlow() {
	cfg := sh.svc.Config()
	cfg.Glow = float64(sh.glow.Value) * glowMax
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
// goroutine (never blocks the event loop).
func (sh *shell) startExport() {
	if sh.svc.Track() == nil {
		sh.state.SetStatus("Export: import a track before exporting")
		return
	}
	out := strings.TrimSpace(sh.outPathEd.Text())
	if out == "" {
		out = defaultExportPath(sh.lay.Exports)
		sh.outPathEd.SetText(out)
	}
	if _, err := export.SanitizeOutputPath(out); err != nil {
		sh.state.SetStatus("Export: " + err.Error())
		return
	}
	w, h, ok := export.ResolutionByID(sh.resEnum.Value)
	if !ok {
		sh.state.SetStatus("Export: unknown resolution preset " + sh.resEnum.Value)
		return
	}
	fps := 30
	fmt.Sscanf(sh.fpsEnum.Value, "%d", &fps)
	seconds := exportSeconds(sh.secondsEd.Text())
	if seconds <= 0 {
		sh.state.SetStatus("Export: seconds must be a positive number")
		return
	}
	codec := export.CodecH264
	if sh.codecEnum.Value == "hevc" {
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
	root := strings.TrimSpace(sh.sysPathEd.Text())
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
