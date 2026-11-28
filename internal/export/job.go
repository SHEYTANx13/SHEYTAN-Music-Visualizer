package export

// job.go — the Go-owned export job. Go renders every frame on a
// fixed schedule (frameIndex / fps is the authoritative media
// timestamp; never wall-clock dt), streams raw RGBA to the local
// FFmpeg adapter's stdin, feeds the canonical WAV as audio, tracks
// real progress from the encoder, honors cancellation and validates
// the output with ffprobe before an atomic rename.

import (
	"bufio"
	"context"
	"fmt"
	"image"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/procattr"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/render"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/songdna"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/visualizer"
)

// Request describes one export job.
type Request struct {
	OutputPath    string
	Width, Height int
	FPS           int       // integer fps (legacy; ignored when FPSRate is set)
	FPSRate       FrameRate // exact rational rate (run 43712071111; preferred)
	VideoCodec    string    // "" / "h264" (compatibility default) / "hevc"
	QualityPreset string
	Encoder       string // auto | libx264 | libx265 | hardware name
	AudioBitrate  string // e.g. "192k" (AAC; empty = 192k default)
	AudioWavPath  string // canonical WAV from the PCM cache
	AudioDuration float64

	// Editing tools (run 46168717789): trim/cut, subtitle burn-in and
	// the bounded preview sample.
	//
	// TrimStart is the cut-in point in seconds (0 = the beginning).
	// TrimDuration is the cut length in seconds (0 = everything from
	// TrimStart to the end). Both are validated against AudioDuration;
	// the render timeline and the audio stream use the SAME window, so
	// the cut is frame-accurate by construction.
	TrimStart    float64
	TrimDuration float64
	// PreviewSeconds bounds the render to the FIRST PreviewSeconds of
	// the chosen window (>0). A preview is a REAL encode with the EXACT
	// chosen resolution/fps/codec/quality/subtitle settings, validated
	// like a full export — "see the live version in your desired format
	// and quality" without pretending to be a real-time player.
	PreviewSeconds float64
	// SubtitlePath burns a subtitle file (SRT/ASS/SSA/WebVTT and every
	// other format the local FFmpeg's libass bridge reads) into the
	// frames. Empty = no subtitle. Validated before rendering starts.
	SubtitlePath string

	// Frame source: the shared visualizer configuration and DNA. The
	// same logic/config drives preview and export.
	Config visualizer.Config
	DNA    *songdna.DNA

	// AudioStateProvider returns the audio snapshot for a media time.
	AudioStateProvider func(t float64) visualizer.AudioState

	// WaveformWindowProvider returns the waveform window for a time.
	WaveformWindowProvider func(t float64) []float32

	// PreferGPU selects the GPU renderer when honestly available.
	PreferGPU bool
}

// Result reports a finished export.
type Result struct {
	OutputPath string             `json:"outputPath"`
	Width      int                `json:"width"`
	Height     int                `json:"height"`
	FPS        int                `json:"fps"`
	Frames     int                `json:"frames"`
	Duration   float64            `json:"duration"`
	FileSize   int64              `json:"fileSize"`
	VideoCodec string             `json:"videoCodec"`
	AudioCodec string             `json:"audioCodec"`
	Validation ValidationResult   `json:"validation"`
	Backend    render.BackendInfo `json:"backend"`
	Elapsed    time.Duration      `json:"elapsed"`
}

// Progress is a live progress snapshot.
type Progress struct {
	FramesRendered int
	FramesTotal    int
	EncoderFrame   int
	Phase          string // rendering | encoding | validating | done
}

// maxPreviewSeconds bounds the preview sample length (run 46168717789:
// a preview is a real encode of the chosen settings; 60 s covers a
// representative choice without pretending to be a full render).
const maxPreviewSeconds = 60.0

// Job runs one export.
type Job struct {
	req      Request
	env      *Environment
	enc      EncoderChoice
	renderer render.Renderer

	mu      sync.Mutex
	cancel  context.CancelFunc
	prog    Progress
	running bool
	err     error
	result  *Result
}

// NewJob validates the request and prepares (but does not start) an
// export job.
func NewJob(req Request) (*Job, error) {
	if req.Width <= 0 || req.Height <= 0 || req.Width > 7680 || req.Height > 4320 {
		return nil, fmt.Errorf("export: invalid resolution %dx%d", req.Width, req.Height)
	}
	rate := req.FPSRate
	if rate.Num == 0 {
		rate = IntFrameRate(req.FPS)
	}
	if !rate.Valid() {
		return nil, fmt.Errorf("export: unsupported frame rate %s (supported: %d-%d fps, e.g. 24/25/29.97/30/50/59.94/60/119.88/120 or 24000/1001)",
			rate.String(), MinFPS, MaxFPS)
	}
	req.FPSRate = rate
	req.FPS = int(rate.Float())
	if req.VideoCodec == "" {
		req.VideoCodec = CodecH264
	}
	if req.AudioBitrate == "" {
		req.AudioBitrate = "192k"
	}
	out, err := SanitizeOutputPath(req.OutputPath)
	if err != nil {
		return nil, err
	}
	req.OutputPath = out
	if req.AudioWavPath == "" {
		return nil, fmt.Errorf("export: audio track (canonical WAV) is required")
	}
	if req.AudioStateProvider == nil {
		return nil, fmt.Errorf("export: audio state provider is required")
	}
	// Editing tools validation (run 46168717789): typed, actionable
	// errors BEFORE any dialog/render work — never a mid-export death.
	if req.TrimStart < 0 || req.TrimDuration < 0 {
		return nil, fmt.Errorf("export: trim start/duration must be >= 0 (got %g/%g)", req.TrimStart, req.TrimDuration)
	}
	if req.AudioDuration > 0 && req.TrimStart >= req.AudioDuration {
		return nil, fmt.Errorf("export: trim start %.2fs is beyond the track duration %.2fs", req.TrimStart, req.AudioDuration)
	}
	if req.PreviewSeconds < 0 || req.PreviewSeconds > maxPreviewSeconds {
		return nil, fmt.Errorf("export: preview length must be in (0, %g] seconds or 0 for a full export, got %g", maxPreviewSeconds, req.PreviewSeconds)
	}
	env, err := DetectEnv(false)
	if err != nil {
		return nil, err
	}
	if req.SubtitlePath != "" {
		fi, serr := os.Stat(req.SubtitlePath)
		if serr != nil || fi.IsDir() {
			return nil, fmt.Errorf("export: subtitle file not found: %s", req.SubtitlePath)
		}
		if !HasVideoFilter(env, "subtitles") {
			return nil, fmt.Errorf("export: the selected FFmpeg build has no subtitles filter (libass missing) — use the in-app Media Tools download, which includes libass, or pick an FFmpeg build with --enable-libass")
		}
	}
	enc, err := ResolveEncoderCodec(env, req.Encoder, req.VideoCodec)
	if err != nil {
		return nil, err
	}
	j := &Job{req: req, env: env, enc: enc}
	return j, nil
}

// Info returns a progress snapshot.
func (j *Job) Info() Progress {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.prog
}

// Err returns the terminal error (nil while running or on success).
func (j *Job) Err() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.err
}

// Result returns the final result after success.
func (j *Job) Result() *Result {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.result
}

// Cancel requests cancellation.
func (j *Job) Cancel() {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.cancel != nil {
		j.cancel()
	}
}

// Running reports whether the job is active.
func (j *Job) Running() bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.running
}

// Run executes the export synchronously (call from a worker
// goroutine, never the UI thread).
func (j *Job) Run(ctx context.Context) (*Result, error) {
	ctx, cancel := context.WithCancel(ctx)
	j.mu.Lock()
	if j.running {
		j.mu.Unlock()
		cancel()
		return nil, fmt.Errorf("export: job already running")
	}
	j.running = true
	j.cancel = cancel
	j.err = nil
	j.result = nil
	j.prog = Progress{Phase: "rendering"}
	j.mu.Unlock()
	defer func() {
		j.mu.Lock()
		j.running = false
		j.mu.Unlock()
		cancel()
	}()

	start := time.Now()
	// Effective edit window (run 46168717789): [effStart, effStart+effDur)
	// within the track. TrimDuration 0 = "to the end"; PreviewSeconds
	// bounds the window further and is the REAL encode sample.
	effStart := j.req.TrimStart
	effDur := j.req.AudioDuration - effStart
	if j.req.TrimDuration > 0 && j.req.TrimDuration < effDur {
		effDur = j.req.TrimDuration
	}
	if j.req.PreviewSeconds > 0 && j.req.PreviewSeconds < effDur {
		effDur = j.req.PreviewSeconds
	}
	if effDur <= 0 {
		return nil, fmt.Errorf("export: effective export window is empty (trim start %.2fs, track %.2fs)", effStart, j.req.AudioDuration)
	}
	totalFrames := int(effDur * j.req.FPSRate.Float())
	if totalFrames <= 0 {
		return nil, fmt.Errorf("export: audio duration is zero")
	}
	if totalFrames > 8*60*60*int(j.req.FPSRate.Float()) { // 8 h safety bound
		return nil, fmt.Errorf("export: requested duration is too long")
	}

	// renderer selection (honest GPU/CPU)
	renderer, binfo := j.selectRenderer()
	defer renderer.Close()
	j.renderer = renderer

	// temporary output next to the destination (atomic rename after
	// validation)
	dest := j.req.OutputPath
	partPath := strings.TrimSuffix(dest, ".mp4") + ".part.mp4"
	_ = os.Remove(partPath)
	defer func() {
		_ = os.Remove(partPath) // no-op after successful rename
	}()

	// ffmpeg command: rawvideo stdin + canonical wav. The HEVC MP4
	// sample entry is tagged for broad player compatibility via the
	// default hvc1 tag ffmpeg writes for hevc in MP4.
	q := qualityCRF(j.req.QualityPreset, j.req.VideoCodec)
	args := []string{
		"-nostdin", "-y", "-hide_banner", "-loglevel", "error", "-progress", "pipe:1",
		"-f", "rawvideo", "-pixel_format", "rgba",
		"-video_size", fmt.Sprintf("%dx%d", j.req.Width, j.req.Height),
		"-framerate", j.req.FPSRate.String(),
		"-i", "pipe:0",
	}
	// The audio input uses the SAME edit window as the render timeline:
	// input seeking on the canonical WAV is sample-accurate, so a cut
	// video and its cut audio cannot drift apart.
	if effStart > 0 {
		args = append(args, "-ss", strconv.FormatFloat(effStart, 'f', 6, 64))
	}
	if effDur < j.req.AudioDuration {
		args = append(args, "-t", strconv.FormatFloat(effDur, 'f', 6, 64))
	}
	args = append(args,
		"-i", j.req.AudioWavPath,
		"-map", "0:v:0", "-map", "1:a:0",
		"-c:v", j.enc.Name,
	)
	args = append(args, j.enc.Args...)
	if j.enc.Name == "libx264" || j.enc.Name == "libx265" {
		args = append(args,
			"-crf", q,
			"-preset", encoderPresetFor(j.req.Width, j.req.Height, j.req.FPSRate.Float()),
		)
	}
	// Subtitle burn-in (run 46168717789): libass renders the subtitles
	// INTO the frames at encode time — every player shows them, no
	// player-side subtitle support required. NewJob already proved the
	// file exists and the filter is supported.
	if j.req.SubtitlePath != "" {
		args = append(args, "-vf", SubtitleFilterArg(j.req.SubtitlePath))
	}
	args = append(args,
		"-pix_fmt", "yuv420p",
		"-c:a", "aac", "-b:a", j.req.AudioBitrate,
		"-shortest",
		"-movflags", "+faststart",
		partPath,
	)
	cmd := exec.CommandContext(ctx, j.env.FFmpegPath, args...)
	procattr.Configure(cmd) // never a visible child console on Windows
	cmd.Args[0] = j.env.FFmpegPath
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("export: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("export: stdout pipe: %w", err)
	}
	var stderrBuf lockedBuffer
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("export: start ffmpeg: %w", err)
	}

	// drain stderr concurrently (prevents pipe deadlock)
	var wg sync.WaitGroup
	wg.Add(2)
	var procErr error
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64*1024), 64*1024)
		for sc.Scan() {
			if k, v, ok := ParseProgressLine(sc.Text()); ok && k == "frame" {
				j.mu.Lock()
				j.prog.EncoderFrame = v
				j.mu.Unlock()
			}
		}
	}()
	go func() {
		defer wg.Done()
		// frame supply: deterministic schedule frameIndex/fps
		for i := 0; i < totalFrames; i++ {
			select {
			case <-ctx.Done():
				procErr = ctx.Err()
				stdin.Close()
				return
			default:
			}
			t := effStart + float64(i)*float64(j.req.FPSRate.Den)/float64(j.req.FPSRate.Num)
			img, rerr := j.renderFrame(i, t)
			if rerr != nil {
				procErr = fmt.Errorf("export: render frame %d: %w", i, rerr)
				stdin.Close()
				return
			}
			if _, werr := stdin.Write(img.Pix); werr != nil {
				// ffmpeg died (encoder error) — surface its stderr below
				procErr = fmt.Errorf("export: write frame %d: %w", i, werr)
				stdin.Close()
				return
			}
			j.mu.Lock()
			j.prog.FramesRendered = i + 1
			j.prog.FramesTotal = totalFrames
			j.mu.Unlock()
		}
		stdin.Close()
	}()

	waitErr := cmd.Wait()
	wg.Wait()

	if ctx.Err() != nil {
		return nil, fmt.Errorf("export: cancelled")
	}
	if procErr != nil && waitErr == nil {
		return nil, procErr
	}
	if waitErr != nil {
		return nil, fmt.Errorf("export: ffmpeg failed: %v: %s", waitErr, firstLine(stderrBuf.String()))
	}

	j.mu.Lock()
	j.prog.Phase = "validating"
	j.mu.Unlock()

	// ffprobe validation
	val, verr := Validate(j.env.FFprobePath, partPath, ValidationExpect{
		Width: j.req.Width, Height: j.req.Height, FPS: j.req.FPSRate.Float(),
		VideoCodec: j.req.VideoCodec, ExpectedDuration: effDur,
	})
	if verr != nil {
		return nil, fmt.Errorf("export: validation error: %w", verr)
	}
	if !val.OK {
		return nil, fmt.Errorf("export: output failed validation: %s", strings.Join(val.Problems, "; "))
	}

	// atomic rename into place
	fi, err := os.Stat(partPath)
	if err != nil {
		return nil, fmt.Errorf("export: stat output: %w", err)
	}
	if err := os.Rename(partPath, dest); err != nil {
		return nil, fmt.Errorf("export: finalize (rename): %w", err)
	}

	res := &Result{
		OutputPath: dest,
		Width:      j.req.Width, Height: j.req.Height, FPS: j.req.FPS,
		Frames:     val.Frames,
		Duration:   val.Duration,
		FileSize:   fi.Size(),
		VideoCodec: val.VideoCodec,
		AudioCodec: val.AudioCodec,
		Validation: *val,
		Backend:    binfo,
		Elapsed:    time.Since(start),
	}
	j.mu.Lock()
	j.result = res
	j.prog.Phase = "done"
	j.mu.Unlock()
	return res, nil
}

// selectRenderer picks the export renderer and reports it honestly.
// The hardware-adapter requirement mirrors the UI selection exactly
// (run 43541087097 parity repair): a software/WARP-class adapter is
// never used as the export "GPU" — before this, export only checked
// the backend API name while the UI refused software adapters, so an
// export could render on WARP while the preview honestly said CPU.
func (j *Job) selectRenderer() (render.Renderer, render.BackendInfo) {
	if j.req.PreferGPU {
		g := render.NewGPURenderer()
		if g != nil {
			info := g.Info()
			if info.GpuState == render.GpuAvailable &&
				render.DeviceClassHardware == info.DeviceClass &&
				info.BackendAPI != "software" && info.BackendAPI != "noop" {
				return g, info
			}
			g.Close()
		}
	}
	c := render.NewCPURenderer()
	return c, c.Info()
}

// renderFrame renders one deterministic frame (reusing the renderer's
// output buffer; pixels are written to the pipe before the next call).
func (j *Job) renderFrame(index int, t float64) (*image.RGBA, error) {
	audio := j.req.AudioStateProvider(t)
	audio.Time = t
	if j.req.WaveformWindowProvider != nil {
		audio.WaveWindow = j.req.WaveformWindowProvider(t)
	}
	in := visualizer.FrameInput{
		FrameIndex: index,
		Time:       t,
		Width:      j.req.Width,
		Height:     j.req.Height,
		Config:     j.req.Config,
		DNA:        j.req.DNA,
		Audio:      audio,
	}
	return j.renderer.RenderFrame(in)
}

// lockedBuffer is a concurrency-safe bytes.Buffer for stderr draining.
type lockedBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
