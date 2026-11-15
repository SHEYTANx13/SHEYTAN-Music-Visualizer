package export

// climode.go — headless CLI export diagnostic (run 43612608237).
//
// RunExportCLI drives the REAL export pipeline (export.Job: same
// fflocate app-local resolution, same deterministic frame schedule, same
// ffprobe validation) from the command line with a deterministic
// synthetic demo signal. Its purpose is package verification: hosted CI
// and the portable-package gate prove "the extracted package can
// actually export a validated MP4 with its OWN tools" without a GUI
// session. It never fabricates success — any pipeline/validation failure
// returns a non-zero exit and prints the exact error.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/visualizer"
)

// CLIOptions carries the --export-cli flag values.
type CLIOptions struct {
	Out     string  // output .mp4 path (required)
	Width   int     // frame width (default 1920)
	Height  int     // frame height (default 1080)
	FPS     int     // 24/30/60/120
	Seconds float64 // duration (default 1.0)
	Codec   string  // "h264" (default) or "hevc" (run 43712071111)
	FPSRate string  // optional exact rational rate, e.g. "24000/1001"
}

// RunCLI executes one headless export and returns the process exit
// code. The demo signal is a deterministic layered synthetic (bass
// sweep + mid chord + treble sparkle with a beat envelope) so CI output
// is reproducible; the visualizer mode defaults to bars (the stable
// core mode) with a spectrogram option through the config below.
func RunCLI(opts CLIOptions, out, errW io.Writer) int {
	if opts.Out == "" {
		fmt.Fprintln(errW, "export-cli: --export-out is required")
		return 2
	}
	if opts.Width <= 0 {
		opts.Width = 1920
	}
	if opts.Height <= 0 {
		opts.Height = 1080
	}
	if opts.FPS == 0 {
		opts.FPS = 30
	}
	if opts.Seconds <= 0 {
		opts.Seconds = 1.0
	}
	if opts.Codec == "" {
		opts.Codec = "h264"
	}

	dir, err := os.MkdirTemp("", "sheytan-export-cli-")
	if err != nil {
		fmt.Fprintf(errW, "export-cli: temp dir: %v\n", err)
		return 1
	}
	defer os.RemoveAll(dir)

	wav, provider, duration, err := writeDemoFixture(dir, opts.Seconds)
	if err != nil {
		fmt.Fprintf(errW, "export-cli: demo fixture: %v\n", err)
		return 1
	}

	req := Request{
		OutputPath:    opts.Out,
		Width:         opts.Width,
		Height:        opts.Height,
		FPS:           opts.FPS,
		VideoCodec:    opts.Codec,
		QualityPreset: "high",
		Encoder:       "auto",
		AudioWavPath:  wav,
		AudioDuration: duration,
		Config: visualizer.Config{
			Mode: visualizer.ModeBars, ShowTitle: true,
			TitleText: "SHEYTAN Export Diagnostic", ShowWatermark: true,
		},
		AudioStateProvider:     provider,
		WaveformWindowProvider: func(float64) []float32 { return nil },
		PreferGPU:              false,
	}
	job, err := NewJob(req)
	if err != nil {
		fmt.Fprintf(errW, "export-cli: job setup failed: %v\n", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	res, err := job.Run(ctx)
	if err != nil {
		fmt.Fprintf(errW, "export-cli: export failed: %v\n", err)
		return 1
	}
	if !res.Validation.OK {
		fmt.Fprintf(errW, "export-cli: output failed ffprobe validation: %s\n",
			joinProblems(res.Validation.Problems))
		return 1
	}
	enc, _ := json.Marshal(res)
	fmt.Fprintf(out, "%s\n", enc)
	fmt.Fprintf(out, "export-cli: OK — %dx%d @ %d fps, %d frames, %.3fs, %d bytes, validated by %s\n",
		res.Width, res.Height, res.FPS, res.Frames, res.Duration, res.FileSize, res.Validation.VideoCodec)
	return 0
}

func joinProblems(p []string) string {
	s := ""
	for i, v := range p {
		if i > 0 {
			s += "; "
		}
		s += v
	}
	return s
}

// writeDemoFixture synthesizes the deterministic demo signal into a
// canonical float32 WAV (the same canonical PCM format the audio cache
// writes) and returns the audio state provider for the render timeline.
func writeDemoFixture(dir string, seconds float64) (wavPath string, provider func(float64) visualizer.AudioState, duration float64, err error) {
	const sampleRate = 48000
	frames := int(seconds * float64(sampleRate))
	n := frames * 2 // stereo interleaved
	samples := make([]float32, n)
	for i := 0; i < frames; i++ {
		t := float64(i) / float64(sampleRate)
		// layered deterministic mix: bass sweep 55→110 Hz, mid chord,
		// treble sparkle, and a 2 Hz beat envelope
		bass := 0.42 * math.Sin(2*math.Pi*(55+55*t/seconds)*t)
		mid := 0.20 * (math.Sin(2*math.Pi*330*t) + 0.8*math.Sin(2*math.Pi*440*t))
		treble := 0.08 * math.Sin(2*math.Pi*1760*t) * (0.5 + 0.5*math.Sin(2*math.Pi*7*t))
		beat := 0.6 + 0.4*math.Pow(1-math.Mod(t*2, 1), 3)
		v := (bass + mid + treble) * beat
		if v > 1 {
			v = 1
		} else if v < -1 {
			v = -1
		}
		samples[2*i] = float32(v)
		samples[2*i+1] = float32(v)
	}

	wavPath = filepath.Join(dir, "demo.wav")
	if err := writeFloat32Wav(wavPath, samples, sampleRate); err != nil {
		return "", nil, 0, err
	}

	provider = func(t float64) visualizer.AudioState {
		return demoAudioState(t, seconds)
	}
	return wavPath, provider, seconds, nil
}

// writeFloat32Wav writes a canonical IEEE-float stereo WAV.
func writeFloat32Wav(path string, samples []float32, rate int) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	frames := len(samples) / 2
	put32 := func(dst []byte, v uint32) {
		dst[0], dst[1], dst[2], dst[3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
	}
	put16 := func(dst []byte, v uint16) {
		dst[0], dst[1] = byte(v), byte(v>>8)
	}
	hdr := make([]byte, 44)
	copy(hdr[0:4], "RIFF")
	put32(hdr[4:8], uint32(36+frames*8))
	copy(hdr[8:12], "WAVE")
	copy(hdr[12:16], "fmt ")
	put32(hdr[16:20], 16)
	put16(hdr[20:22], 3) // IEEE float
	put16(hdr[22:24], 2) // stereo
	put32(hdr[24:28], uint32(rate))
	put32(hdr[28:32], uint32(rate*8))
	put16(hdr[32:34], 8)  // block align
	put16(hdr[34:36], 32) // bits per sample
	copy(hdr[36:40], "data")
	put32(hdr[40:44], uint32(frames*8))
	if _, err := f.Write(hdr); err != nil {
		return err
	}
	buf := make([]byte, 4)
	for _, v := range samples {
		put32(buf, math.Float32bits(v))
		if _, err := f.Write(buf); err != nil {
			return err
		}
	}
	return f.Close()
}

// demoAudioState derives the deterministic audio feature snapshot for
// the demo timeline (pure function of t — no randomness, no wall clock).
func demoAudioState(t, seconds float64) visualizer.AudioState {
	bass := 0.55 + 0.35*math.Sin(2*math.Pi*1.5*t)
	mid := 0.45 + 0.25*math.Sin(2*math.Pi*3*t+1.1)
	treble := 0.30 + 0.25*math.Sin(2*math.Pi*6*t+2.3)
	bins := make([]float64, visualizer.NumVizBins)
	for i := range bins {
		u := float64(i) / float64(visualizer.NumVizBins-1)
		shape := math.Exp(-3*u) * (0.55 + 0.45*math.Sin(2*math.Pi*(2*u+t*0.6)))
		bins[i] = clampBin(shape*(0.5*bass+0.3*mid+0.2*treble) + 0.05)
	}
	beatPhase := math.Mod(t*2, 1) // 120 BPM
	return visualizer.AudioState{
		Time:       t,
		RMS:        0.5 + 0.2*math.Sin(2*math.Pi*1.5*t),
		Bass:       bass,
		Mid:        mid,
		Treble:     treble,
		Flux:       math.Max(0, math.Sin(2*math.Pi*t*2)) * 0.5,
		Onset:      beatPhase < 0.06,
		BeatPhase:  beatPhase,
		Bins:       bins,
		WaveWindow: demoWaveWindow(t),
	}
}

func clampBin(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// demoWaveWindow returns the deterministic waveform envelope window.
func demoWaveWindow(t float64) []float32 {
	w := make([]float32, 128)
	for i := range w {
		ph := float64(i) / float64(len(w))
		v := math.Sin(2*math.Pi*(2*t+ph)) * (0.4 + 0.6*math.Sin(2*math.Pi*ph*0.5+t))
		w[i] = float32(v * 0.6)
	}
	return w
}
