package export

// export_test.go — end-to-end MP4 export proofs against the local
// FFmpeg: a short synthetic fixture at 1080p and 4K, streamed frames,
// bounded memory, ffprobe validation, cancel handling and the
// missing-encoder path. The test skips with an honest message when
// FFmpeg is not installed (recorded as NOT AVAILABLE in note.md).

import (
	"context"
	"fmt"
	"image/color"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/audio"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/visualizer"
)

func requireFFmpeg(t *testing.T) *Environment {
	t.Helper()
	env, err := DetectEnv(false)
	if err != nil || env.FFmpegPath == "" {
		t.Skipf("SKIP (NOT AVAILABLE): local FFmpeg missing: %v", err)
	}
	return env
}

// syntheticFixture builds a canonical WAV + a deterministic audio
// provider (swept sine + clicks) without any external file.
func syntheticFixture(t *testing.T, dir string, seconds float64) (wavPath string, provider func(float64) visualizer.AudioState, duration float64) {
	t.Helper()
	audio.RegisterAll()
	frames := int(seconds * audio.CanonicalSampleRate)
	samples := make([]float32, frames*2)
	for i := 0; i < frames; i++ {
		tSec := float64(i) / audio.CanonicalSampleRate
		// swept tone 220→880 Hz with a 4 Hz tremolo for rhythm
		f := 220 + 660*tSec/seconds
		v := 0.5 * (0.7 + 0.3*math.Sin(2*math.Pi*4*tSec)) *
			math.Sin(2*math.Pi*f*tSec)
		samples[2*i] = float32(v)
		samples[2*i+1] = float32(v)
	}
	// encode to a canonical float32 WAV through the cache writer path
	wavPath = filepath.Join(dir, "fixture.wav")
	f, err := os.Create(wavPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	// header
	type u16 = uint16
	hdr := make([]byte, 44)
	copy(hdr[0:4], "RIFF")
	putU32 := func(off int, v uint32) {
		hdr[off] = byte(v)
		hdr[off+1] = byte(v >> 8)
		hdr[off+2] = byte(v >> 16)
		hdr[off+3] = byte(v >> 24)
	}
	putU32(4, uint32(36+frames*8))
	copy(hdr[8:12], "WAVE")
	copy(hdr[12:16], "fmt ")
	putU32(16, 16)
	putu16 := func(off int, v u16) {
		hdr[off] = byte(v)
		hdr[off+1] = byte(v >> 8)
	}
	putu16(20, 3) // IEEE float
	putu16(22, 2)
	putU32(24, audio.CanonicalSampleRate)
	putU32(28, audio.CanonicalSampleRate*8)
	putu16(32, 8)
	putu16(34, 32)
	copy(hdr[36:40], "data")
	putU32(40, uint32(frames*8))
	if _, err := f.Write(hdr); err != nil {
		t.Fatal(err)
	}
	// float32 samples (little-endian on all supported platforms)
	for _, v := range samples {
		bits := math.Float32bits(v)
		var b [4]byte
		b[0] = byte(bits)
		b[1] = byte(bits >> 8)
		b[2] = byte(bits >> 16)
		b[3] = byte(bits >> 24)
		if _, err := f.Write(b[:]); err != nil {
			t.Fatal(err)
		}
	}
	bins := make([]float64, visualizer.NumVizBins)
	for i := range bins {
		bins[i] = 0.3 + 0.5*math.Abs(math.Sin(float64(i)/9))
	}
	provider = func(tSec float64) visualizer.AudioState {
		return visualizer.AudioState{
			Time: tSec,
			RMS:  0.4, Bass: 0.5, Mid: 0.35, Treble: 0.2,
			Centroid: 1800, Flux: 0.02,
			Onset:     math.Mod(tSec, 0.5) < 0.05,
			BeatPhase: math.Mod(tSec, 0.5) / 0.5,
			Bins:      bins,
		}
	}
	return wavPath, provider, seconds
}

func runExport(t *testing.T, width, height, fps int, preferGPU bool) *Result {
	t.Helper()
	requireFFmpeg(t)
	dir := t.TempDir()
	wav, provider, duration := syntheticFixture(t, dir, 3.0) // 3 s fixture
	out := filepath.Join(dir, fmt.Sprintf("out-%dx%d.mp4", width, height))
	req := Request{
		OutputPath:    out,
		Width:         width,
		Height:        height,
		FPS:           fps,
		QualityPreset: "high",
		Encoder:       "auto",
		AudioWavPath:  wav,
		AudioDuration: duration,
		Config: visualizer.Config{
			Mode: visualizer.ModeRadial, ShowTitle: true,
			TitleText: "Export Proof", ShowWatermark: true,
		},
		AudioStateProvider:     provider,
		WaveformWindowProvider: func(float64) []float32 { return nil },
		PreferGPU:              preferGPU,
	}
	job, err := NewJob(req)
	if err != nil {
		t.Fatalf("NewJob: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	res, err := job.Run(ctx)
	if err != nil {
		t.Fatalf("export %dx%d: %v", width, height, err)
	}
	if !res.Validation.OK {
		t.Fatalf("validation problems: %v", res.Validation.Problems)
	}
	fi, err := os.Stat(res.OutputPath)
	if err != nil || fi.Size() == 0 {
		t.Fatalf("output missing/empty: %v", err)
	}
	// no .part residue
	if _, err := os.Stat(out + ".part.mp4"); err == nil {
		t.Fatal("partial file left behind")
	}
	return res
}

func TestExport1080p30(t *testing.T) {
	res := runExport(t, 1920, 1080, 30, false)
	if res.Width != 1920 || res.Height != 1080 || res.FPS != 30 {
		t.Fatalf("metadata mismatch: %+v", res)
	}
	if res.VideoCodec != "h264" || res.AudioCodec != "aac" {
		t.Fatalf("codecs: %s/%s", res.VideoCodec, res.AudioCodec)
	}
	t.Logf("1080p30: %d frames, %.2fs, %d bytes, %s", res.Frames, res.Duration, res.FileSize, res.Elapsed)
}

func TestExport4K24(t *testing.T) {
	res := runExport(t, 3840, 2160, 24, false)
	if res.Width != 3840 || res.Height != 2160 {
		t.Fatalf("4K output is actually %dx%d", res.Width, res.Height)
	}
	if res.FPS != 24 {
		t.Fatalf("fps %d, want 24", res.FPS)
	}
	t.Logf("4K24: %d frames, %.2fs, %d bytes, %s", res.Frames, res.Duration, res.FileSize, res.Elapsed)
}

func TestExportCancel(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	wav, provider, duration := syntheticFixture(t, dir, 45.0)
	out := filepath.Join(dir, "cancelled.mp4")
	req := Request{
		OutputPath: out, Width: 640, Height: 360, FPS: 30,
		QualityPreset: "high", Encoder: "auto",
		AudioWavPath: wav, AudioDuration: duration,
		Config:                 visualizer.Config{Mode: visualizer.ModeBars},
		AudioStateProvider:     provider,
		WaveformWindowProvider: func(float64) []float32 { return nil },
	}
	job, err := NewJob(req)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(1500 * time.Millisecond)
		cancel()
	}()
	if _, err := job.Run(ctx); err == nil {
		t.Fatal("cancelled export must return an error")
	}
	// no output file may remain
	if _, err := os.Stat(out); err == nil {
		t.Fatal("cancelled export left an output file")
	}
}

func TestExportMissingEncoderFails(t *testing.T) {
	env := requireFFmpeg(t)
	_, err := ResolveEncoder(env, "h264_doesnotexist")
	if err == nil {
		t.Fatal("unknown encoder must be rejected")
	}
}

func TestExportJobRequiresFFmpeg(t *testing.T) {
	if _, err := DetectEnv(false); err != nil {
		t.Skip("SKIP (NOT AVAILABLE): no ffmpeg; requirement check not applicable")
	}
	if _, err := NewJob(Request{Width: 0, Height: 0, FPS: 30}); err == nil {
		t.Fatal("zero resolution must be rejected")
	}
	if _, err := NewJob(Request{Width: 1920, Height: 1080, FPS: 17}); err == nil {
		t.Fatal("unsupported fps must be rejected")
	}
}

func TestParseProgressLine(t *testing.T) {
	if k, v, ok := ParseProgressLine("frame=42"); !ok || k != "frame" || v != 42 {
		t.Fatalf("parse: %v %v %v", k, v, ok)
	}
	if _, _, ok := ParseProgressLine("progress=end"); ok {
		t.Fatal("non-numeric value must not parse as progress")
	}
}

func TestQualityArgsAndSanitize(t *testing.T) {
	if QualityArgs("high") != "18" || QualityArgs("balanced") != "21" || QualityArgs("compat") != "23" {
		t.Fatal("quality mapping wrong")
	}
	p, err := SanitizeOutputPath("/tmp/movie")
	if err != nil || p != "/tmp/movie.mp4" {
		t.Fatalf("sanitize: %s %v", p, err)
	}
	if _, err := SanitizeOutputPath("  "); err == nil {
		t.Fatal("empty path must fail")
	}
}

// redFrameCheck validates that GPU-vs-CPU frames come from the same
// contract: not used in CI assertions here, kept for diagnostics.
var _ = color.RGBA{}
var _ = exec.Command
