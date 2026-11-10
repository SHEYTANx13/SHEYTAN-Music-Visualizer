// export_display.go — the Export and Display sections of the system
// profile (run 42837709122 spec §4.1: export capability and display
// facts belong in the typed snapshot with explicit provenance).
//
// Export facts are collected through the EXISTING single discovery
// authority (internal/fflocate) — never a second search — and report
// honest states: a located candidate binary is Detected, a probed
// version/encoder set is Supported, and MP4 export capability stays
// "not proven" unless the executed export tests proved it (the UI
// shows the exact summary; no fake success, no silent audio omission).
//
// Display facts come from the running window the CALLER passes in
// (Fyne owns the real values); this package never guesses a
// resolution, scale or refresh rate — anything not supplied stays
// Unknown with the exact reason.
package sysprofile

import (
	"fmt"
	"path/filepath"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/fflocate"
)

// ExportInfo reports the optional FFmpeg capability and the built-in
// decoder coverage.
type ExportInfo struct {
	// FfmpegPath / FfprobePath: located candidate binaries (Detected)
	// or Unknown with the search outcome.
	FfmpegPath  Fact `json:"ffmpegPath"`
	FfprobePath Fact `json:"ffprobePath"`
	// SourceDir: which documented location provided the pair
	// (configured override / app-local tools / PATH).
	SourceDir Fact `json:"sourceDir"`
	// Probed: true ONLY when a real version/encoder probe ran (UI
	// rescan). Version/Encoders are Unknown with that reason otherwise.
	Probed   bool `json:"probed"`
	Version  Fact `json:"version"`
	Encoders Fact `json:"encoders"`
	// BuiltInDecoders: the always-present Go decoder registry coverage.
	// Unknown when the caller did not supply the registry state.
	BuiltInDecoders Fact `json:"builtInDecoders"`
	// Summary: the user-facing one-liner (e.g. "MP4 export unavailable —
	// no local FFmpeg/ffprobe pair found; WAV/MP3/FLAC/OGG unaffected").
	Summary string `json:"summary"`
}

// DisplayInfo reports the display facts the running window actually
// exposes. Refresh rate is Unknown everywhere in the pinned Fyne
// version (the framework does not expose it) — stated, never guessed.
type DisplayInfo struct {
	// Resolution: the live content size of the main window (Detected)
	// or Unknown with the reason.
	Resolution Fact `json:"resolution"`
	// Scale: the UI scale factor reported by the window/settings.
	Scale Fact `json:"scale"`
	// Refresh: Unknown with the framework reason (never fabricated).
	Refresh Fact `json:"refresh"`
}

// emptyExportInfo is the unavailable-export default.
func emptyExportInfo(reason string) ExportInfo {
	return ExportInfo{
		FfmpegPath:  unknownFact(reason),
		FfprobePath: unknownFact(reason),
		SourceDir:   unknownFact("no FFmpeg source location resolved"),
		Version:     unknownFact("not probed"),
		Encoders:    unknownFact("not probed"),
		BuiltInDecoders: unknownFact(
			"decoder registry coverage not supplied by the caller"),
		Summary: "MP4 export availability unknown — " + reason,
	}
}

// CollectExportFast resolves the FFmpeg/ffprobe candidate pair through
// fflocate (path existence only — no subprocess, safe at any time).
// appLocalBin is the layout's ToolsFFmpegBin ("" = skip that rule).
// It never claims capability beyond what was actually checked.
func CollectExportFast(appLocalBin string) ExportInfo {
	loc, err := fflocate.Locate(fflocate.OPOpts(appLocalBin))
	if err != nil {
		e := emptyExportInfo(err.Error())
		e.Summary = "MP4 export unavailable — " + err.Error() +
			"; built-in WAV/MP3/FLAC/OGG decoding and all other features are unaffected"
		return e
	}
	src := "system PATH"
	summarySrc := "system PATH"
	switch loc.Source {
	case "override":
		src = "configured override directory"
		summarySrc = "configured FFmpeg directory"
	case "app-local":
		src = "app-local tools/ffmpeg/bin"
		summarySrc = "app-local tools/ffmpeg/bin"
	}
	return ExportInfo{
		FfmpegPath:  Fact{Value: loc.FFmpeg, State: StateDetected, Source: src},
		FfprobePath: Fact{Value: loc.FFprobe, State: StateDetected, Source: src},
		SourceDir:   Fact{Value: filepath.Dir(loc.FFmpeg), State: StateDetected, Source: src},
		Version:     unknownFact("version not probed (fast profile path; rescan probes the real binaries)"),
		Encoders:    unknownFact("encoder set not probed (fast profile path; rescan probes the real binaries)"),
		BuiltInDecoders: unknownFact(
			"decoder registry coverage not supplied by the caller"),
		Summary: fmt.Sprintf("FFmpeg pair located via %s — version/encoder capability probed only on rescan; MP4 export proven only by executed export tests", summarySrc),
	}
}

// CollectDisplay builds the display section from the live window the
// caller observed. Zero/unknown inputs stay Unknown with reasons.
func CollectDisplay(widthPx, heightPx int, scale float64) DisplayInfo {
	d := DisplayInfo{
		Resolution: unknownFact("window size not supplied"),
		Scale:      unknownFact("scale factor not supplied"),
		Refresh: unknownFact(
			"the pinned Fyne v2.8.2 framework does not expose the display refresh rate"),
	}
	if widthPx > 0 && heightPx > 0 {
		d.Resolution = Fact{
			Value:  fmt.Sprintf("%d x %d px (main window content)", widthPx, heightPx),
			State:  StateDetected,
			Source: "running Fyne window content size",
		}
	}
	if scale > 0 {
		d.Scale = Fact{
			Value:  fmt.Sprintf("%.2fx", scale),
			State:  StateDetected,
			Source: "Fyne window/settings scale",
		}
	}
	return d
}
