package presentation

import "fmt"

// CommandID is a stable, shell-independent identifier for one product
// command. Both shells map their native gestures (menus, keyboard
// shortcuts, buttons, touch targets) onto the same command IDs, so
// behavior parity is auditable by comparing ID tables instead of
// widget trees.
type CommandID string

// Canonical command identifiers. Values are stable; never rename, only
// append.
const (
	CmdImportAudio         CommandID = "import.audio"
	CmdPlayPause           CommandID = "transport.play-pause"
	CmdStop                CommandID = "transport.stop"
	CmdSeekBack            CommandID = "transport.seek-back"
	CmdSeekForward         CommandID = "transport.seek-forward"
	CmdSetVolume           CommandID = "transport.set-volume"
	CmdProjectSave         CommandID = "project.save"
	CmdProjectOpen         CommandID = "project.open"
	CmdExportStart         CommandID = "export.start"
	CmdExportCancel        CommandID = "export.cancel"
	CmdExportPreview       CommandID = "export.preview"
	CmdExportSubtitle      CommandID = "export.subtitle"
	CmdExportSubtitleClear CommandID = "export.subtitle.clear"
	CmdRendererSelect      CommandID = "renderer.select"
	CmdModeSet             CommandID = "visualizer.mode.set"
	CmdPaletteSet          CommandID = "visualizer.palette.set"
	CmdSensitivitySet      CommandID = "visualizer.sensitivity.set"
	CmdBackgroundLayerSet  CommandID = "visualizer.background-layer.set"
	CmdPresetApply         CommandID = "visualizer.preset.apply"
	CmdIntensitySet        CommandID = "theme.intensity.set"
	CmdReducedMotionSet    CommandID = "theme.reduced-motion.set"
	CmdReducedGlowSet      CommandID = "theme.reduced-glow.set"
	CmdNavigate            CommandID = "navigate"
	CmdTabsReorder         CommandID = "workspace.tabs.reorder"
	CmdTabsReset           CommandID = "workspace.tabs.reset"
	CmdSplitOrientation    CommandID = "workspace.split.orientation"
	CmdPanelCollapse       CommandID = "workspace.panel.collapse"
	CmdPanelRestore        CommandID = "workspace.panel.restore"
	CmdLayoutReset         CommandID = "workspace.layout.reset"
	CmdMediaToolsInstall   CommandID = "mediatools.install"
	CmdMediaToolsOpen      CommandID = "mediatools.open"
	CmdAnalyzePath         CommandID = "systemfiles.analyze"
	CmdHelpOpen            CommandID = "help.open"
	CmdLogsReveal          CommandID = "logs.reveal"
	CmdDiagnosticsExport   CommandID = "diagnostics.export"
	CmdCopyToClipboard     CommandID = "clipboard.copy"
	CmdQuit                CommandID = "app.quit"
	CmdAbout               CommandID = "app.about"
)

// Shortcut is a desktop keyboard binding for one command. Platforms
// map "mod" to Ctrl (Windows/Linux) or Cmd (macOS).
type Shortcut struct {
	Command CommandID `json:"command"`
	Keys    string    `json:"keys"` // canonical form: "mod+o", "space", "mod+shift+s"
}

// Shortcuts is the canonical desktop shortcut table. The native shell
// binds these; the web shell mirrors them when it owns the window.
var Shortcuts = []Shortcut{
	{Command: CmdImportAudio, Keys: "mod+i"},
	{Command: CmdPlayPause, Keys: "space"},
	{Command: CmdStop, Keys: "mod+."},
	{Command: CmdSeekBack, Keys: "left"},
	{Command: CmdSeekForward, Keys: "right"},
	{Command: CmdProjectSave, Keys: "mod+s"},
	{Command: CmdProjectOpen, Keys: "mod+o"},
	{Command: CmdExportStart, Keys: "mod+e"},
	{Command: CmdNavigate, Keys: "mod+1..9,0"},
	{Command: CmdLayoutReset, Keys: "mod+alt+0"},
	{Command: CmdQuit, Keys: "mod+q"},
}

// ValidateCommand reports an error when id is not a known command.
func ValidateCommand(id CommandID) error {
	switch id {
	case CmdImportAudio, CmdPlayPause, CmdStop, CmdSeekBack, CmdSeekForward,
		CmdSetVolume, CmdProjectSave, CmdProjectOpen, CmdExportStart,
		CmdExportCancel, CmdExportPreview, CmdExportSubtitle,
		CmdExportSubtitleClear, CmdRendererSelect, CmdModeSet, CmdPaletteSet,
		CmdSensitivitySet, CmdBackgroundLayerSet, CmdPresetApply,
		CmdIntensitySet, CmdReducedMotionSet, CmdReducedGlowSet,
		CmdNavigate, CmdTabsReorder, CmdTabsReset, CmdSplitOrientation,
		CmdPanelCollapse, CmdPanelRestore, CmdLayoutReset,
		CmdMediaToolsInstall, CmdMediaToolsOpen, CmdAnalyzePath,
		CmdHelpOpen, CmdLogsReveal, CmdDiagnosticsExport, CmdCopyToClipboard, CmdQuit, CmdAbout:
		return nil
	}
	return fmt.Errorf("presentation: unknown command %q", id)
}
