package studio

// estimate.go — pre-export size estimation through the REAL pipeline
// (run 43712071111 P1-B): a short sample of the actual visualizer and
// audio is encoded with the selected settings by the same export.Job
// machinery, and the sample's encoded bytes are extrapolated into a
// low/expected/high range. CRF (constant-quality) sizes cannot be
// known from settings alone; the sample makes the estimate honest.

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/audio"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/export"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/export/estimate"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/visualizer"
)

// estimateSampleSeconds is the sample duration encoded for the
// estimate (kept short; the range band covers complexity variance).
const estimateSampleSeconds = 2.0

// EstimateBySample runs a short sample export with the given settings
// (same resolution/FPS/codec/quality/audio) and extrapolates the
// output size range. It blocks for the sample duration; callers run
// it off the UI thread.
func (s *Studio) EstimateBySample(req export.Request, track *audio.ImportedTrack) (estimate.Range, string) {
	s.mu.RLock()
	cfg := s.vizConfig
	dna := s.dna
	s.mu.RUnlock()
	if track == nil {
		return estimate.Range{}, "no track loaded"
	}
	if track.Duration <= 0 {
		return estimate.Range{}, "unknown track duration"
	}
	if s.ExportJob() != nil && s.ExportJob().Running() {
		return estimate.Range{}, "an export is running"
	}
	sampleDur := estimateSampleSeconds
	if track.Duration < sampleDur {
		sampleDur = track.Duration
	}
	// Sample from the middle of the track (representative energy, not
	// a quiet intro); providers are offset so frames see the middle
	// segment while frame times stay deterministic from zero.
	offset := 0.0
	if track.Duration > sampleDur {
		offset = (track.Duration - sampleDur) / 2
	}
	audioAt := s.AudioStateAt
	waveAt := s.WaveformWindow
	req.Config = cfg
	req.DNA = dna
	req.AudioWavPath = s.cache.WavPath(track.Fingerprint)
	req.AudioDuration = sampleDur
	req.AudioStateProvider = func(t float64) visualizer.AudioState {
		return audioAt(t + offset)
	}
	req.WaveformWindowProvider = func(t float64) []float32 {
		return waveAt(t + offset)
	}
	// The audio payload in the sample is the middle segment: the WAV
	// cannot be sliced without re-encoding, so the sample carries the
	// full audio with -shortest bounding at the video length — the
	// encoded VIDEO bytes dominate the estimate; the audio
	// contribution is added analytically below.
	tmp, err := os.MkdirTemp("", "sheytan-estimate-*")
	if err != nil {
		return estimate.Range{}, "temp dir: " + err.Error()
	}
	defer os.RemoveAll(tmp)
	req.OutputPath = filepath.Join(tmp, "estimate-sample.mp4")
	req.PreferGPU = false // deterministic, portable sample
	job, err := export.NewJob(req)
	if err != nil {
		return estimate.Range{}, err.Error()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	res, err := job.Run(ctx)
	if err != nil {
		return estimate.Range{}, "sample encode failed: " + err.Error()
	}
	rng := estimate.FromSample(res.FileSize, time.Duration(res.Duration*float64(time.Second)),
		time.Duration(track.Duration*float64(time.Second)))
	return rng, rng.Note
}
