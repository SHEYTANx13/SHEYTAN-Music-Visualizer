package wails

// service_import_export.go — the import.audio / export.start /
// export.cancel dispatch of the web shell facade.
//
// Repair/run-46033044814 product direction: the Wails shell is THE
// desktop shell, so making a music video must work end to end from
// the web surface:
//
//   1. the user presses Import; the facade asks the NATIVE dialog host
//      (host.go, wired from app.go over the Wails v3 dialog runtime) for
//      a user-selected audio file and hands the path to studio.ImportFile;
//      the studio then runs the real DSP analysis and derives the Song
//      DNA that drives the music video (see service_dna.go / /api/songdna);
//   2. the user presses Export; the facade asks the native save dialog
//      for the destination and builds a validated export.Request with the
//      resolution/frame-rate/codec/quality the web layer chose;
//   3. the user can cancel the running export; cancellation goes through
//      the export job's own Cancel (bounded lanes, honest states).
//
// The web layer NEVER receives the chosen path: it is consumed inside
// Go. Cancel and validation failures surface as typed FacadeErrors.

import (
	"fmt"
	"strings"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/export"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/presentation"
)

// export facade bounds mirror export.NewJob's authority; validating
// here gives the web layer an immediate typed error, while NewJob
// remains the single source of truth.
const (
	maxFacadeWidth  = 7680
	maxFacadeHeight = 4320
)

// validFacadeCodecs are the video codecs export.NewJob accepts.
var validFacadeCodecs = map[string]bool{"": true, "h264": true, "hevc": true}

// validFacadeQualities are the CRF quality presets of qualityCRF
// (empty = the encoder default "high").
var validFacadeQualities = map[string]bool{"": true, "high": true, "balanced": true, "compat": true}

// dialogs returns the registered native dialog host (nil-safe read).
func (s *SheytanService) dialogsHost() hostDialogs {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dialogs
}

// runImportAudio dispatches import.audio through the native dialog
// host. A user cancel is success-with-no-change (the UI reports the
// cancelled state from the library snapshot, never a fake error).
func (s *SheytanService) runImportAudio(cmd presentation.CommandID) error {
	host := s.dialogsHost()
	if host == nil {
		return &FacadeError{Command: cmd, Kind: KindRejected,
			Reason: "no native dialog host registered (headless or test context cannot open OS dialogs)"}
	}
	path, err := host.OpenAudioPath()
	if err != nil {
		return &FacadeError{Command: cmd, Kind: KindRejected,
			Reason: fmt.Sprintf("import dialog failed: %v", err)}
	}
	if strings.TrimSpace(path) == "" {
		return nil // user cancelled the dialog; no state change
	}
	if err := s.studio.ImportFile(path); err != nil {
		return reject(cmd, err)
	}
	return nil
}

// buildExportRequest validates the shared format/editing arguments and
// returns the bounded values (run 46168717789: one authority for
// export.start AND export.preview so a preview is EXACTLY the settings
// the full export will use).
func (s *SheytanService) buildExportRequest(cmd presentation.CommandID, args commandArgs, preview bool) (width, height, fps int, trimStart, trimDur, previewSecs float64, videoCodec, quality string, err error) {
	width, height, fps = 1920, 1080, 30
	if args.Width != nil {
		width = *args.Width
	}
	if args.Height != nil {
		height = *args.Height
	}
	if args.FPS != nil {
		fps = *args.FPS
	}
	if width <= 0 || height <= 0 || width > maxFacadeWidth || height > maxFacadeHeight {
		return 0, 0, 0, 0, 0, 0, "", "", argsError(cmd, "resolution must be within 1x1..%dx%d, got %dx%d",
			maxFacadeWidth, maxFacadeHeight, width, height)
	}
	if fps < export.MinFPS || fps > export.MaxFPS {
		return 0, 0, 0, 0, 0, 0, "", "", argsError(cmd, "fps must be in [%d,%d], got %d", export.MinFPS, export.MaxFPS, fps)
	}
	if !validFacadeCodecs[args.VideoCodec] {
		return 0, 0, 0, 0, 0, 0, "", "", argsError(cmd, "videoCodec must be one of h264|hevc (empty = h264), got %q", args.VideoCodec)
	}
	if !validFacadeQualities[args.QualityPreset] {
		return 0, 0, 0, 0, 0, 0, "", "", argsError(cmd, "qualityPreset must be one of high|balanced|compat (empty = high), got %q", args.QualityPreset)
	}
	if args.TrimStart != nil {
		trimStart = *args.TrimStart
	}
	if args.TrimDuration != nil {
		trimDur = *args.TrimDuration
	}
	if trimStart < 0 || trimDur < 0 {
		return 0, 0, 0, 0, 0, 0, "", "", argsError(cmd, "trimStart/trimDuration must be >= 0, got %g/%g", trimStart, trimDur)
	}
	if args.Preview != nil {
		previewSecs = *args.Preview
	}
	if previewSecs < 0 {
		return 0, 0, 0, 0, 0, 0, "", "", argsError(cmd, "preview must be >= 0 (0 = no preview bound), got %g", previewSecs)
	}
	videoCodec, quality = args.VideoCodec, args.QualityPreset
	return width, height, fps, trimStart, trimDur, previewSecs, videoCodec, quality, nil
}

// runExportStart dispatches export.start: native save dialog for the
// destination, then a fully validated export.Request. Argument
// validation runs BEFORE the dialog host check, so bad input is a
// typed error even in headless contexts and the dialog is never
// opened for garbage.
func (s *SheytanService) runExportStart(cmd presentation.CommandID, args commandArgs) error {
	width, height, fps, trimStart, trimDur, _, videoCodec, quality, aerr := s.buildExportRequest(cmd, args, false)
	if aerr != nil {
		return aerr
	}

	host := s.dialogsHost()
	if host == nil {
		return &FacadeError{Command: cmd, Kind: KindRejected,
			Reason: "no native dialog host registered (headless or test context cannot open OS dialogs)"}
	}

	track := s.studio.Track()
	if track == nil {
		return reject(cmd, fmt.Errorf("no track loaded: import music before exporting"))
	}

	defaultName := exportDefaultName(track.SourceName)
	path, err := host.SaveExportPath(defaultName)
	if err != nil {
		return &FacadeError{Command: cmd, Kind: KindRejected,
			Reason: fmt.Sprintf("export dialog failed: %v", err)}
	}
	if strings.TrimSpace(path) == "" {
		return nil // user cancelled the dialog; no state change
	}

	req := export.Request{
		OutputPath:    path,
		Width:         width,
		Height:        height,
		FPS:           fps,
		VideoCodec:    videoCodec,
		QualityPreset: quality,
		TrimStart:     trimStart,
		TrimDuration:  trimDur,
		SubtitlePath:  s.exportSubtitlePath(),
	}
	if err := s.studio.StartExport(req); err != nil {
		return reject(cmd, err)
	}
	return nil
}

// exportSubtitlePath returns the attached subtitle file ("" when none).
func (s *SheytanService) exportSubtitlePath() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.subtitlePath
}

// setExportSubtitlePath stores the dialog-chosen subtitle path.
func (s *SheytanService) setExportSubtitlePath(p string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.subtitlePath = p
}

// runExportPreview dispatches export.preview (run 46168717789 editing
// tools): the user picks a destination in the native save dialog and
// the job encodes a REAL, ffprobe-validated sample with the EXACT
// chosen resolution/fps/codec/quality/trim/subtitle settings, bounded
// to a representative length. The user then plays the sample and
// decides with evidence, not guesses.
func (s *SheytanService) runExportPreview(cmd presentation.CommandID, args commandArgs) error {
	width, height, fps, trimStart, trimDur, previewSecs, videoCodec, quality, aerr := s.buildExportRequest(cmd, args, true)
	if aerr != nil {
		return aerr
	}
	if previewSecs <= 0 {
		return argsError(cmd, "preview length must be > 0 seconds (e.g. 8), got %g", previewSecs)
	}

	host := s.dialogsHost()
	if host == nil {
		return &FacadeError{Command: cmd, Kind: KindRejected,
			Reason: "no native dialog host registered (headless or test context cannot open OS dialogs)"}
	}

	track := s.studio.Track()
	if track == nil {
		return reject(cmd, fmt.Errorf("no track loaded: import music before previewing"))
	}

	base := exportDefaultName(track.SourceName)
	defaultName := strings.TrimSuffix(base, ".mp4") + "-preview.mp4"
	path, err := host.SaveExportPath(defaultName)
	if err != nil {
		return &FacadeError{Command: cmd, Kind: KindRejected,
			Reason: fmt.Sprintf("preview dialog failed: %v", err)}
	}
	if strings.TrimSpace(path) == "" {
		return nil // user cancelled the dialog; no state change
	}

	req := export.Request{
		OutputPath:     path,
		Width:          width,
		Height:         height,
		FPS:            fps,
		VideoCodec:     videoCodec,
		QualityPreset:  quality,
		TrimStart:      trimStart,
		TrimDuration:   trimDur,
		PreviewSeconds: previewSecs,
		SubtitlePath:   s.exportSubtitlePath(),
	}
	if err := s.studio.StartExport(req); err != nil {
		return reject(cmd, err)
	}
	return nil
}

// runExportSubtitleAttach opens the native subtitle picker. Every
// format the local FFmpeg libass bridge accepts is offered (SRT,
// ASS/SSA, WebVTT, MicroDVD, SubViewer and more); the format support
// belongs to the filter bridge, not to this app's allowlist. The path
// stays inside Go; the web layer only sees the attach state.
func (s *SheytanService) runExportSubtitleAttach(cmd presentation.CommandID) error {
	host := s.dialogsHost()
	if host == nil {
		return &FacadeError{Command: cmd, Kind: KindRejected,
			Reason: "no native dialog host registered (headless or test context cannot open OS dialogs)"}
	}
	path, err := host.OpenSubtitlePath()
	if err != nil {
		return &FacadeError{Command: cmd, Kind: KindRejected,
			Reason: fmt.Sprintf("subtitle dialog failed: %v", err)}
	}
	if strings.TrimSpace(path) == "" {
		return nil // user cancelled; keep the previous attachment
	}
	s.setExportSubtitlePath(path)
	return nil
}

// runExportCancel dispatches export.cancel against the current job.
// Cancelling when nothing runs is a typed, honest no-op refusal.
func (s *SheytanService) runExportCancel(cmd presentation.CommandID) error {
	job := s.studio.ExportJob()
	if job == nil || !job.Running() {
		return &FacadeError{Command: cmd, Kind: KindRejected,
			Reason: "no running export to cancel"}
	}
	job.Cancel()
	return nil
}

// exportDefaultName derives a safe suggested file name from the track
// name: the source extension is stripped, the stem is sanitized to a
// conservative ASCII file stem (spaces/dashes collapse, path
// separators become dashes, other characters drop), and ".mp4" is
// appended. It is a SUGGESTION for the native dialog only; the real
// destination comes from the user.
func exportDefaultName(trackName string) string {
	name := strings.TrimSpace(trackName)
	if name == "" {
		name = "sheytan-music-video"
	}
	// Strip the source extension (last dot component), if any.
	if i := strings.LastIndexAny(name, "./\\"); i >= 0 && name[i] == '.' {
		name = name[:i]
	}
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '.':
			b.WriteRune(r)
			lastDash = false
		case r == ' ', r == '-', r == '/', r == '\\':
			if !lastDash {
				b.WriteRune('-')
				lastDash = true
			}
		default:
			// drop everything else (unicode, punctuation, control)
		}
	}
	clean := strings.Trim(b.String(), "-.")
	if clean == "" {
		clean = "sheytan-music-video"
	}
	if len(clean) > 80 {
		clean = clean[:80]
	}
	clean += ".mp4"
	return clean
}
