package wails

// service_dna.go — the Song DNA and export-status surfaces of the web
// shell facade.
//
// Song DNA (internal/songdna) is the deterministic per-track visual
// identity derived from MEASURED DSP analysis: brightness, warmth,
// punchiness, rhythmic drive and loudness become the artistic defaults
// (mode, palette, motion, pulse response, smoothing, particle density,
// glow, spectrum tilt) that the studio applies to the live visualizer
// configuration when the user has not customized it. This file exposes
// that real state to the web layer verbatim - never synthesized, never
// faked: a track without a finished analysis is reported as pending,
// not as invented numbers.
//
// Every value crosses the boundary as an explicitly built JSON map
// (boundary rule: domain structs are never exposed directly).

import (
	"math"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/export"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/songdna"
)

// GetSongDNA serves GET /api/songdna: the real Song DNA of the loaded
// track plus the measured features it was derived from, and whether
// the derived defaults are currently applied to the visualizer config.
// States are honest:
//   - "no-track":   nothing imported yet
//   - "pending":    track loaded, analysis still running (or failed;
//     diagnostics pane names the failure)
//   - "ready":      DNA derived from the measured analysis
func (s *SheytanService) GetSongDNA() map[string]any {
	track := s.studio.Track()
	if track == nil {
		return map[string]any{
			"state": "no-track",
			"note":  "import a track (Import button in Studio) to derive its Song DNA",
		}
	}

	analysis := s.studio.Analysis()
	dna := s.studio.DNA()
	if analysis == nil || dna == nil {
		return map[string]any{
			"state": "pending",
			"note":  "analysis in progress; the Song DNA is derived from the measured result when it finishes",
			"track": map[string]any{
				"name":     track.SourceName,
				"duration": track.Duration,
			},
		}
	}

	return map[string]any{
		"state": "ready",
		"track": map[string]any{
			"name":        track.SourceName,
			"duration":    track.Duration,
			"fingerprint": track.Fingerprint,
		},
		"dna": dnaMap(dna),
		"measured": map[string]any{
			"meanCentroidHz":  analysis.MeanCentroid,
			"meanRolloffHz":   analysis.MeanRolloff,
			"meanRMS":         analysis.MeanRMS,
			"peakRMS":         analysis.PeakRMS,
			"bassEnergy":      analysis.BassEnergy,
			"midEnergy":       analysis.MidEnergy,
			"trebleEnergy":    analysis.TrebleEnergy,
			"onsetCount":      analysis.OnsetCount,
			"bpm":             analysis.BPM,
			"tempoConfidence": analysis.TempoConfidence,
			"tempoReliable":   analysis.TempoReliable,
			"tempoNote":       analysis.TempoNote,
		},
		"applied":     !s.studio.UserCustomized(),
		"appliedNote": appliedNote(s.studio.UserCustomized()),
	}
}

// dnaMap is the verbatim DNA projection (no invention, no renaming of
// meanings; JSON keys follow the songdna package's own JSON tags).
func dnaMap(dna *songdna.DNA) map[string]any {
	return map[string]any{
		"version":         dna.Version,
		"fingerprint":     dna.Fingerprint,
		"seed":            dna.Seed,
		"userSeed":        dna.UserSeed,
		"brightness":      dna.Brightness,
		"punchiness":      dna.Punchiness,
		"warmth":          dna.Warmth,
		"rhythmicDrive":   dna.RhythmicDrive,
		"loudness":        dna.Loudness,
		"defaultMode":     dna.DefaultMode,
		"paletteIndex":    dna.PaletteIndex,
		"motionStyle":     dna.MotionStyle,
		"pulseResponse":   dna.PulseResponse,
		"smoothing":       dna.Smoothing,
		"particleDensity": dna.ParticleDensity,
		"glowIntensity":   dna.GlowIntensity,
		"spectrumTilt":    dna.SpectrumTilt,
		"descriptor":      dna.Descriptor,
	}
}

func appliedNote(customized bool) string {
	if customized {
		return "your manual visualizer customizations take precedence; the DNA defaults are recorded but NOT applied until you reset them"
	}
	return "the DNA artistic defaults are applied to the live visualizer configuration"
}

// GetExportStatus serves GET /api/export: the real export job state
// from the studio (idle/running/done/failed), with the live progress
// snapshot and the finished-result metadata when an export completed.
// Never synthesized progress: absent job means idle.
func (s *SheytanService) GetExportStatus() map[string]any {
	job := s.studio.ExportJob()
	if job == nil {
		return map[string]any{
			"state": "idle",
			"note":  "no export job in this session yet",
		}
	}

	info := job.Info()
	percent := 0.0
	if info.FramesTotal > 0 {
		percent = math.Min(100, 100*float64(info.FramesRendered)/float64(info.FramesTotal))
	}

	status := map[string]any{
		"phase":          info.Phase,
		"framesRendered": info.FramesRendered,
		"framesTotal":    info.FramesTotal,
		"encoderFrame":   info.EncoderFrame,
		"percent":        math.Round(percent*10) / 10,
		"running":        job.Running(),
	}

	if err := job.Err(); err != nil {
		status["state"] = "failed"
		status["error"] = err.Error()
		return status
	}
	if res := job.Result(); res != nil {
		status["state"] = "done"
		status["result"] = exportResultMap(res)
		return status
	}
	if job.Running() {
		status["state"] = "running"
	} else {
		status["state"] = "idle"
	}
	return status
}

// exportResultMap projects the finished export result for the web UI.
func exportResultMap(res *export.Result) map[string]any {
	return map[string]any{
		"outputPath": res.OutputPath,
		"width":      res.Width,
		"height":     res.Height,
		"fps":        res.FPS,
		"frames":     res.Frames,
		"duration":   res.Duration,
		"fileSize":   res.FileSize,
		"videoCodec": res.VideoCodec,
		"audioCodec": res.AudioCodec,
	}
}
