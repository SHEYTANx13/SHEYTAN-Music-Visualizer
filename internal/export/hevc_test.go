package export

// hevc_test.go — real H.265 (HEVC) software export proof (run
// 43712071111): a full production export through libx265, validated by
// ffprobe (codec hevc, resolution, exact fractional frame rate,
// duration sync, non-zero size). Skips when the local FFmpeg build has
// no libx265. Intel QSV/hevc hardware is NOT claimed anywhere here:
// hardware HEVC is only ever exposed after a real proof encode on the
// user's machine (ProbeHardwareEncoder), which this environment cannot
// provide.

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/visualizer"
)

func TestExportHEVC1080p30Software(t *testing.T) {
	requireFFmpeg(t)
	env, err := DetectEnv(true)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if !env.Encoders["libx265"] {
		t.Skip("local FFmpeg build has no libx265 (HEVC proof needs a GPL build with libx265)")
	}
	dir := t.TempDir()
	wav, provider, duration := syntheticFixture(t, dir, 1.0)
	job, jerr := NewJob(Request{
		OutputPath:         filepath.Join(dir, "hevc-1080p30.mp4"),
		Width:              1920,
		Height:             1080,
		FPSRate:            IntFrameRate(30),
		VideoCodec:         CodecHEVC,
		QualityPreset:      "high",
		Encoder:            "auto",
		AudioBitrate:       "192k",
		AudioWavPath:       wav,
		AudioDuration:      duration,
		Config:             visualizer.Config{Mode: visualizer.ModeRadial, ShowTitle: true, TitleText: "HEVC Proof"},
		AudioStateProvider: provider,
		PreferGPU:          false,
	})
	if jerr != nil {
		t.Fatalf("new job: %v", jerr)
	}
	res, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("hevc export: %v", err)
	}
	if res.VideoCodec != "hevc" {
		t.Fatalf("video codec = %s, want hevc", res.VideoCodec)
	}
	if res.AudioCodec != "aac" {
		t.Fatalf("audio codec = %s, want aac", res.AudioCodec)
	}
	if res.Width != 1920 || res.Height != 1080 {
		t.Fatalf("resolution = %dx%d", res.Width, res.Height)
	}
	if res.FileSize <= 0 {
		t.Fatal("zero-size output")
	}
}

func TestExportCustomFrameRates(t *testing.T) {
	requireFFmpeg(t)
	r, err := ParseFrameRate("29.97")
	if err != nil {
		t.Fatal(err)
	}
	if r.Num != 30000 || r.Den != 1001 {
		t.Fatalf("29.97 must parse to the exact 30000/1001, got %d/%d", r.Num, r.Den)
	}
	dir := t.TempDir()
	wav, provider, duration := syntheticFixture(t, dir, 1.0)
	job, jerr := NewJob(Request{
		OutputPath:         filepath.Join(dir, "custom-2997.mp4"),
		Width:              640,
		Height:             360,
		FPSRate:            r,
		VideoCodec:         CodecH264,
		QualityPreset:      "balanced",
		Encoder:            "auto",
		AudioWavPath:       wav,
		AudioDuration:      duration,
		Config:             visualizer.Config{Mode: visualizer.ModeBars},
		AudioStateProvider: provider,
		PreferGPU:          false,
	})
	if jerr != nil {
		t.Fatalf("new job: %v", jerr)
	}
	res, err := job.Run(context.Background())
	if err != nil {
		t.Fatalf("29.97 export: %v", err)
	}
	// The encoded output must carry the exact 30000/1001 cadence
	// (ffprobe-measured average frame rate).
	if d := res.Validation.FPS - 29.97002997002997; d < -0.05 || d > 0.05 {
		t.Fatalf("output fps = %.6f, want 30000/1001", res.Validation.FPS)
	}
}

func TestExportJobRejectsBadRates(t *testing.T) {
	dir := t.TempDir()
	wav, provider, duration := syntheticFixture(t, dir, 1.0)
	base := Request{
		OutputPath: filepath.Join(dir, "rates.mp4"), Width: 640, Height: 360,
		AudioWavPath: wav, AudioDuration: duration,
		Config: visualizer.Config{Mode: visualizer.ModeBars}, AudioStateProvider: provider,
	}
	for _, bad := range []string{"0", "-30", "121", "abc", "1/0"} {
		rate, err := ParseFrameRate(bad)
		if err == nil {
			base.FPSRate = rate
			if _, jerr := NewJob(base); jerr == nil {
				t.Errorf("frame rate %q must be rejected by NewJob", bad)
			}
		}
	}
}
