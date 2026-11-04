package export

// validate.go — ffprobe-based output validation: container, codecs,
// resolution, fps, duration sync and size. The result is honest: any
// failed check lands in Problems and OK=false.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// ValidationExpect describes what the output must satisfy.
type ValidationExpect struct {
	Width, Height    int
	FPS              int
	ExpectedDuration float64 // audio duration (video within tolerance)
}

// ValidationResult is the ffprobe verdict.
type ValidationResult struct {
	OK         bool     `json:"ok"`
	Format     string   `json:"format"`
	VideoCodec string   `json:"videoCodec"`
	AudioCodec string   `json:"audioCodec"`
	Width      int      `json:"width"`
	Height     int      `json:"height"`
	FPS        float64  `json:"fps"`
	Duration   float64  `json:"duration"`
	Frames     int      `json:"frames"`
	Size       int64    `json:"size"`
	Problems   []string `json:"problems,omitempty"`
}

// durationTolerance is the audio/video sync tolerance: one frame at
// the lowest rate plus muxing slack.
const durationTolerance = 1.0/24 + 0.5

// Validate runs ffprobe on path and checks it against expect.
func Validate(ffprobePath string, path string, expect ValidationExpect) (*ValidationResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, ffprobePath,
		"-v", "error", "-print_format", "json",
		"-show_format", "-show_streams", path,
	).Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe: %w", err)
	}
	var probe struct {
		Format struct {
			FormatName string `json:"format_name"`
			Duration   string `json:"duration"`
			Size       string `json:"size"`
		} `json:"format"`
		Streams []struct {
			CodecType    string `json:"codec_type"`
			CodecName    string `json:"codec_name"`
			Width        int    `json:"width"`
			Height       int    `json:"height"`
			AvgFrameRate string `json:"avg_frame_rate"`
			NbFrames     string `json:"nb_frames"`
			Duration     string `json:"duration"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &probe); err != nil {
		return nil, fmt.Errorf("ffprobe output: %w", err)
	}
	res := &ValidationResult{
		Format: probe.Format.FormatName,
	}
	if probe.Format.Duration != "" {
		res.Duration, _ = strconv.ParseFloat(probe.Format.Duration, 64)
	}
	if probe.Format.Size != "" {
		res.Size, _ = strconv.ParseInt(probe.Format.Size, 10, 64)
	}
	for _, s := range probe.Streams {
		switch s.CodecType {
		case "video":
			res.VideoCodec = s.CodecName
			res.Width, res.Height = s.Width, s.Height
			res.FPS = parseFrameRate(s.AvgFrameRate)
			res.Frames, _ = strconv.Atoi(s.NbFrames)
			if res.Duration == 0 && s.Duration != "" {
				res.Duration, _ = strconv.ParseFloat(s.Duration, 64)
			}
		case "audio":
			res.AudioCodec = s.CodecName
		}
	}
	// checks
	problems := make([]string, 0, 4)
	if !strings.Contains(strings.ToLower(res.Format), "mp4") {
		problems = append(problems, fmt.Sprintf("container %q is not MP4", res.Format))
	}
	if res.VideoCodec != "h264" {
		problems = append(problems, fmt.Sprintf("video codec %q is not H.264", res.VideoCodec))
	}
	if res.AudioCodec != "aac" {
		problems = append(problems, fmt.Sprintf("audio codec %q is not AAC", res.AudioCodec))
	}
	if res.Width != expect.Width || res.Height != expect.Height {
		problems = append(problems, fmt.Sprintf("resolution %dx%d != requested %dx%d",
			res.Width, res.Height, expect.Width, expect.Height))
	}
	if expect.FPS > 0 && math.Abs(res.FPS-float64(expect.FPS)) > 0.05 {
		problems = append(problems, fmt.Sprintf("fps %.3f != requested %d", res.FPS, expect.FPS))
	}
	if res.Duration <= 0 {
		problems = append(problems, "no duration reported")
	} else if expect.ExpectedDuration > 0 &&
		math.Abs(res.Duration-expect.ExpectedDuration) > durationTolerance {
		problems = append(problems, fmt.Sprintf("duration %.3fs differs from audio %.3fs by more than %.3fs",
			res.Duration, expect.ExpectedDuration, durationTolerance))
	}
	if res.Size <= 0 {
		problems = append(problems, "file size is zero")
	}
	res.Problems = problems
	res.OK = len(problems) == 0
	return res, nil
}

// parseFrameRate parses "30000/1001" or "30" style frame rates.
func parseFrameRate(s string) float64 {
	if s == "" {
		return 0
	}
	if num, den, ok := strings.Cut(s, "/"); ok {
		n, err1 := strconv.ParseFloat(num, 64)
		d, err2 := strconv.ParseFloat(den, 64)
		if err1 == nil && err2 == nil && d != 0 {
			return n / d
		}
		return 0
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}
