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
	"strings"
	"sync"
	"time"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/render"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/songdna"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/visualizer"
)

// Request describes one export job.
type Request struct {
	OutputPath    string
	Width, Height int
	FPS           int
	QualityPreset string
	Encoder       string // auto | libx264 | hardware name
	AudioWavPath  string // canonical WAV from the PCM cache
	AudioDuration float64

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
	validFPS := false
	for _, f := range FrameRates {
		if req.FPS == f {
			validFPS = true
		}
	}
	if !validFPS {
		return nil, fmt.Errorf("export: unsupported frame rate %d (use 24/30/60)", req.FPS)
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
	env, err := DetectEnv(false)
	if err != nil {
		return nil, err
	}
	enc, err := ResolveEncoder(env, req.Encoder)
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
	totalFrames := int(j.req.AudioDuration * float64(j.req.FPS))
	if totalFrames <= 0 {
		return nil, fmt.Errorf("export: audio duration is zero")
	}
	if totalFrames > 8*60*60*j.req.FPS { // 8 h safety bound
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

	// ffmpeg command: rawvideo stdin + canonical wav
	q := QualityArgs(j.req.QualityPreset)
	args := []string{
		"-nostdin", "-y", "-hide_banner", "-loglevel", "error", "-progress", "pipe:1",
		"-f", "rawvideo", "-pixel_format", "rgba",
		"-video_size", fmt.Sprintf("%dx%d", j.req.Width, j.req.Height),
		"-framerate", fmt.Sprint(j.req.FPS),
		"-i", "pipe:0",
		"-i", j.req.AudioWavPath,
		"-map", "0:v:0", "-map", "1:a:0",
		"-c:v", j.enc.Name,
	}
	args = append(args, j.enc.Args...)
	if j.enc.Name == "libx264" {
		args = append(args, "-crf", q)
	}
	args = append(args,
		"-pix_fmt", "yuv420p",
		"-c:a", "aac", "-b:a", "192k",
		"-shortest",
		"-movflags", "+faststart",
		partPath,
	)
	cmd := exec.CommandContext(ctx, j.env.FFmpegPath, args...)
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
			t := float64(i) / float64(j.req.FPS)
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
		Width: j.req.Width, Height: j.req.Height, FPS: j.req.FPS,
		ExpectedDuration: j.req.AudioDuration,
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
func (j *Job) selectRenderer() (render.Renderer, render.BackendInfo) {
	if j.req.PreferGPU {
		g := render.NewGPURenderer()
		if g != nil {
			info := g.Info()
			if info.GpuState == render.GpuAvailable && info.BackendAPI != "software" && info.BackendAPI != "noop" {
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
