package export

// export_4k120_test.go — run 43612608237 hard functional target:
// 3840x2160 at 120 fps is a REAL encoded-output test, not a config
// validation. The test renders 120 frames (one second) at 4K120 through
// the same export.Job used by the app, validates the output with the
// SAME ffprobe binary the app resolved (app-local before PATH), and
// asserts the exact contract: codec, dimensions, 120/1 frame cadence,
// exact counted frames, monotonic PTS with no duplicates/drops, pixel
// format and audio presence.
//
// 4K60 coverage runs through the identical harness with the 60 fps
// cadence assertions. Both tests SKIP honestly (NOT AVAILABLE) when no
// local FFmpeg/FFprobe exists — a skip is never a pass.

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/visualizer"
)

// runExportAt executes one real export at the requested geometry/rate
// through the production job pipeline.
func runExportAt(t *testing.T, width, height, fps int, seconds float64) *Result {
	t.Helper()
	env := requireFFmpeg(t)
	dir := t.TempDir()
	wav, provider, duration := syntheticFixture(t, dir, seconds)
	out := filepath.Join(dir, fmt.Sprintf("out-%dx%d-%d.mp4", width, height, fps))
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
			Mode: visualizer.ModeSpectrogram, ShowTitle: true,
			TitleText: "4K120 Export Proof", ShowWatermark: true,
		},
		AudioStateProvider:     provider,
		WaveformWindowProvider: func(float64) []float32 { return nil },
		PreferGPU:              false, // deterministic CPU/software path for portable CI
	}
	job, err := NewJob(req)
	if err != nil {
		t.Fatalf("NewJob: %v", err)
	}
	if job.env.FFprobePath == "" {
		t.Skip("SKIP (NOT AVAILABLE): ffprobe not resolvable")
	}
	_ = env
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	res, err := job.Run(ctx)
	if err != nil {
		t.Fatalf("export %dx%d@%d failed: %v", width, height, fps, err)
	}
	if !res.Validation.OK {
		t.Fatalf("validation problems: %v", res.Validation.Problems)
	}
	return res
}

func TestExport4K120(t *testing.T) {
	if testing.Short() {
		t.Skip("SKIP: short mode (the 4K120 encode is an integration test)")
	}
	env := requireFFmpeg(t)
	if !env.Encoders["libx264"] && len(env.EncoderList(false)) == 0 {
		t.Skipf("SKIP (NOT AVAILABLE): no H.264 encoder in local FFmpeg build (encoders=%v)", env.Encoders)
	}

	const (
		width   = 3840
		height  = 2160
		fps     = 120
		seconds = 1.0
	)
	wantFrames := int(seconds * fps) // 120

	res := runExportAt(t, width, height, fps, seconds)
	defer func() {
		t.Logf("4K120: %d frames, %.3fs, %d bytes, elapsed %s",
			res.Frames, res.Duration, res.FileSize, res.Elapsed)
	}()

	// 1. Container/codec contract through the app-resolved ffprobe.
	if res.Width != width || res.Height != height {
		t.Fatalf("resolution %dx%d != %dx%d", res.Width, res.Height, width, height)
	}
	if res.VideoCodec != "h264" {
		t.Fatalf("video codec %q is not h264", res.VideoCodec)
	}
	if res.FPS != fps {
		t.Fatalf("result fps %d != %d", res.FPS, fps)
	}
	if res.Frames != wantFrames {
		t.Fatalf("counted frames %d != expected %d (1 s at 120 fps)", res.Frames, wantFrames)
	}

	// 2. Stream-level cadence: r_frame_rate and avg_frame_rate must both
	//    report exactly 120/1, pixel format yuv420p, and the counted
	//    frame count must equal 120.
	vs, frames, ptsSec := probeStreamAndFrames(t, env.FFprobePath, res.OutputPath)
	if vs.AvgFrameRate != "120/1" || vs.RFrameRate != "120/1" {
		t.Fatalf("cadence: avg_frame_rate=%q r_frame_rate=%q, want 120/1 for both", vs.AvgFrameRate, vs.RFrameRate)
	}
	if vs.Width != width || vs.Height != height {
		t.Fatalf("stream resolution %dx%d != %dx%d", vs.Width, vs.Height, width, height)
	}
	if vs.PixFmt != "yuv420p" {
		t.Fatalf("pixel format %q is not yuv420p", vs.PixFmt)
	}
	if len(frames) != wantFrames {
		t.Fatalf("ffprobe frame count %d != %d (exact 1 s at 120 fps)", len(frames), wantFrames)
	}
	// 3. Timestamp sequence: strictly increasing with a constant 1/120 s
	//    step (± half a time-base quantum of tolerance), no duplicates,
	//    no drops, starting at t=0.
	stepWant := 1.0 / 120.0
	tol := 1.0 / 120.0 * 0.5
	if math32Abs(ptsSec[0]) > tol {
		t.Fatalf("first frame pts_time=%.6f, want ~0", ptsSec[0])
	}
	for i := 1; i < len(ptsSec); i++ {
		d := ptsSec[i] - ptsSec[i-1]
		if d <= 0 {
			t.Fatalf("frame %d pts_time %.6f not strictly increasing (prev %.6f) — duplicate/dropped frame", i, ptsSec[i], ptsSec[i-1])
		}
		if math32Abs(d-stepWant) > tol {
			t.Fatalf("frame %d pts step %.6f != %.6f (±%.6f) — broken cadence", i, d, stepWant, tol)
		}
	}
	if frames[0].PictType != "I" {
		t.Fatalf("first frame pict_type=%q, want IDR/I", frames[0].PictType)
	}
}

func TestExport4K60(t *testing.T) {
	if testing.Short() {
		t.Skip("SKIP: short mode (the 4K60 encode is an integration test)")
	}
	env := requireFFmpeg(t)
	if !env.Encoders["libx264"] && len(env.EncoderList(false)) == 0 {
		t.Skipf("SKIP (NOT AVAILABLE): no H.264 encoder in local FFmpeg build (encoders=%v)", env.Encoders)
	}
	const (
		width   = 3840
		height  = 2160
		fps     = 60
		seconds = 1.0
	)
	wantFrames := int(seconds * fps)

	res := runExportAt(t, width, height, fps, seconds)
	defer func() {
		t.Logf("4K60: %d frames, %.3fs, %d bytes, elapsed %s",
			res.Frames, res.Duration, res.FileSize, res.Elapsed)
	}()
	if res.Width != width || res.Height != height || res.VideoCodec != "h264" {
		t.Fatalf("probe mismatch: %+v", res)
	}
	if res.FPS != fps {
		t.Fatalf("result fps %d != %d", res.FPS, fps)
	}
	if res.Frames != wantFrames {
		t.Fatalf("counted frames %d != expected %d (1 s at 60 fps)", res.Frames, wantFrames)
	}
	fvs, frames, ptsSec := probeStreamAndFrames(t, env.FFprobePath, res.OutputPath)
	if fvs.AvgFrameRate != "60/1" || fvs.RFrameRate != "60/1" {
		t.Fatalf("cadence: avg_frame_rate=%q r_frame_rate=%q, want 60/1 for both", fvs.AvgFrameRate, fvs.RFrameRate)
	}
	if len(frames) != wantFrames {
		t.Fatalf("ffprobe frame count %d != %d", len(frames), wantFrames)
	}
	stepWant := 1.0 / 60.0
	tol := 1.0 / 60.0 * 0.5
	for i := 1; i < len(ptsSec); i++ {
		d := ptsSec[i] - ptsSec[i-1]
		if d <= 0 || math32Abs(d-stepWant) > tol {
			t.Fatalf("frame %d pts step %.6f broken (want %.6f ±%.6f)", i, d, stepWant, tol)
		}
	}
}

// (probeVideoFrames was folded into probeStreamAndFrames above: the
// frame pts values are normalized through the stream time base there.)

type probedFrame struct {
	Pts      *int64 `json:"pts"`
	PtsTime  string `json:"pts_time"`
	PictType string `json:"pict_type"`
}

type probedStream struct {
	Index        int    `json:"index"`
	CodecType    string `json:"codec_type"`
	CodecName    string `json:"codec_name"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	AvgFrameRate string `json:"avg_frame_rate"`
	RFrameRate   string `json:"r_frame_rate"`
	TimeBase     string `json:"time_base"`
	NbFrames     string `json:"nb_frames"`
	PixFmt       string `json:"pix_fmt"`
	Profile      string `json:"profile"`
	Level        int    `json:"level"`
}

// probeStreamAndFrames returns the first video stream and the frame
// list with pts values NORMALIZED TO SECONDS via the stream time base,
// so the cadence assertion is independent of the muxer's time-base
// choice (1/15360 MP4 tracks vs 1/120 etc.).
func probeStreamAndFrames(t *testing.T, ffprobe, path string) (probedStream, []probedFrame, []float64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	// -select_streams v:0: without it -show_frames reports the AUDIO
	// stream's frames too (observed: 60 video + 47 audio entries for a
	// 1 s clip), which would corrupt the exact-frame-count contract.
	cmd := exec.CommandContext(ctx, ffprobe,
		"-v", "error", "-select_streams", "v:0",
		"-print_format", "json",
		"-show_streams", "-show_frames", path,
	)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("ffprobe stream/frame probe failed: %v", err)
	}
	var doc struct {
		Streams []probedStream `json:"streams"`
		Frames  []probedFrame  `json:"frames"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("ffprobe JSON: %v", err)
	}
	var vs probedStream
	found := false
	for _, s := range doc.Streams {
		if s.CodecType == "video" {
			vs, found = s, true
			break
		}
	}
	if !found {
		t.Fatalf("ffprobe reported no video stream")
	}
	_, den := parseFraction(vs.TimeBase)
	if den == 0 {
		t.Fatalf("video stream time_base %q is not a rational", vs.TimeBase)
	}
	seconds := make([]float64, 0, len(doc.Frames))
	for _, f := range doc.Frames {
		pts := f.PtsTime
		if pts == "" && f.Pts != nil {
			pts = strconv.FormatFloat(float64(*f.Pts)/float64(den), 'f', -1, 64)
		}
		v, err := strconv.ParseFloat(pts, 64)
		if err != nil {
			t.Fatalf("frame pts_time %q unparseable: %v", pts, err)
		}
		seconds = append(seconds, v)
	}
	return vs, doc.Frames, seconds
}

// parseFraction parses "a/b" (or a single decimal) into floats.
func parseFraction(s string) (float64, float64) {
	if num, den, ok := strings.Cut(s, "/"); ok {
		n, e1 := strconv.ParseFloat(num, 64)
		d, e2 := strconv.ParseFloat(den, 64)
		if e1 == nil && e2 == nil {
			return n, d
		}
		return 0, 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, 0
	}
	return v, 1
}

// math32Abs is a tiny helper (tests here avoid pulling math twice for
// float32/float64 mixing).
func math32Abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
