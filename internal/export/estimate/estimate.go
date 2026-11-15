// Package estimate provides honest pre-export output-size estimation
// (run 43712071111 P1-B).
//
//   - Bitrate mode (hardware encoders): bytes are computed from the
//     duration x bitrate formula plus the audio bitrate and a small
//     documented container/mux overhead. Exact by construction for
//     ABR/VBR-with-target encoders.
//
//   - Constant-quality mode (CRF — libx264/libx265): the size cannot
//     be known from settings alone, so a SHORT representative sample
//     of the real content (the actual visualizer + audio, same
//     resolution/FPS/codec/quality settings) is encoded with the real
//     pipeline and extrapolated into a low/expected/high RANGE. The
//     label always says "estimate"; content complexity changes the
//     result.
package estimate

import (
	"fmt"
	"time"
)

// containerOverheadFraction is the documented MP4 mux + faststart
// overhead added on top of the payload streams.
const containerOverheadFraction = 0.015

// Bitrate returns the estimated output size for a target-bitrate
// encode: (video bitrate + audio bitrate) * duration + mux overhead.
// Bitrates are bits per second; the result is bytes.
func Bitrate(duration time.Duration, videoBitsPerSec, audioBitsPerSec int64) int64 {
	if duration <= 0 || videoBitsPerSec <= 0 {
		return 0
	}
	audio := audioBitsPerSec
	if audio < 0 {
		audio = 0
	}
	payload := float64(videoBitsPerSec+audio) * duration.Seconds() / 8
	return int64(payload*(1+containerOverheadFraction) + 0.5)
}

// Range is a low/expected/high size estimate in bytes with the method
// label and an honest note.
type Range struct {
	Low, Expected, High int64
	Method              string
	Note                string
}

// FromSample extrapolates a sample encode into a range. The sample
// must use the SAME resolution/FPS/codec/quality settings as the full
// export; only the duration differs. The ±30% band expresses the
// honest variability of constant-quality encoding over content
// complexity.
func FromSample(sampleBytes int64, sampleDuration, totalDuration time.Duration) Range {
	if sampleDuration <= 0 || totalDuration <= 0 || sampleBytes <= 0 {
		return Range{Method: "sample", Note: "not enough sample data for an estimate"}
	}
	ratio := totalDuration.Seconds() / sampleDuration.Seconds()
	expected := float64(sampleBytes) * ratio
	return Range{
		Low:      int64(expected * 0.7),
		Expected: int64(expected),
		High:     int64(expected * 1.3),
		Method:   "sample-encode extrapolation (±30%)",
		Note: fmt.Sprintf("extrapolated from a %.1f s sample encode of the actual visualizer and audio at the selected settings; content complexity changes the result",
			sampleDuration.Seconds()),
	}
}

// HumanBytes renders a byte count for the UI.
func HumanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}
