// Package export owns MP4 export: local FFmpeg/ffprobe detection and
// capability probing, the Go-owned export job (deterministic frame
// timeline, streamed raw RGBA frames, canonical-PCM audio), progress,
// cancellation and ffprobe validation.
//
// FFmpeg is strictly a local codec/mux adapter. Job control, media
// timeline, rendering, frame scheduling, progress and validation stay
// in Go. No download, no bundling, no shell interpolation: processes
// run through exec.CommandContext with explicit argument slices.
package export

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/apppaths"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/fflocate"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/procattr"
)

// Resolution presets.
var Resolutions = []struct {
	Name   string
	Width  int
	Height int
}{
	{"720p HD", 1280, 720},
	{"1080p Full HD", 1920, 1080},
	{"1440p QHD", 2560, 1440},
	{"4K UHD", 3840, 2160},
}

// Frame rate choices.
var FrameRates = []int{24, 30, 60}

// Quality presets (encoder-dependent knobs resolved at job time).
var QualityPresets = []string{"high", "balanced", "compat"}

// Environment is the detected local FFmpeg environment.
type Environment struct {
	FFmpegPath       string          `json:"ffmpegPath"`
	FFprobePath      string          `json:"ffprobePath"`
	Source           string          `json:"source,omitempty"` // fflocate rule: override / app-local / path
	Version          string          `json:"version"`
	Encoders         map[string]bool `json:"encoders"`                   // encoder name -> present
	HardwareEncoders []string        `json:"hardwareEncoders,omitempty"` // proof-encoded OK
	Notes            []string        `json:"notes,omitempty"`
}

var (
	envMu     sync.Mutex
	cachedEnv *Environment
)

// DetectEnv probes the local ffmpeg/ffprobe through the single
// fflocate resolution order: explicit user configuration (including
// the SHEYTAN_FFMPEG directory), the app-local tools directory
// (<app root>/tools/ffmpeg/bin), then the system PATH. Results are
// cached per process; force=true re-probes (after the user changes
// the path). The error when nothing is found names every searched
// location and the actionable next step.
func DetectEnv(force bool) (*Environment, error) {
	envMu.Lock()
	defer envMu.Unlock()
	if cachedEnv != nil && !force {
		return cachedEnv, nil
	}
	env := &Environment{Encoders: map[string]bool{}}
	lay := apppaths.Resolve(apppaths.OSEnv())
	loc, lerr := fflocate.Locate(fflocate.OPOpts(lay.ToolsFFmpegBin()))
	if lerr != nil {
		cachedEnv = env
		return env, fmt.Errorf("ffmpeg/ffprobe not available: %w (export needs both; place them in %s or on PATH)",
			lerr, toolsHint(lay))
	}
	env.FFmpegPath = loc.FFmpeg
	env.FFprobePath = loc.FFprobe
	env.Source = loc.Source

	// version
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	vcmd := exec.CommandContext(ctx, env.FFmpegPath, "-version")
	procattr.Configure(vcmd) // never a visible child console on Windows
	out, err := vcmd.Output()
	if err != nil {
		cachedEnv = env
		return env, fmt.Errorf("ffmpeg -version failed for %q: %w", loc.Source, err)
	}
	env.Version = firstLine(string(out))

	// encoders list
	ecmd := exec.CommandContext(ctx, env.FFmpegPath, "-hide_banner", "-encoders")
	procattr.Configure(ecmd) // never a visible child console on Windows
	encOut, err := ecmd.Output()
	if err == nil {
		for _, name := range []string{"libx264", "h264_nvenc", "h264_qsv", "h264_amf", "aac", "libfdk_aac"} {
			if encoderPresent(string(encOut), name) {
				env.Encoders[name] = true
			}
		}
	} else {
		env.Notes = append(env.Notes, "encoder listing failed: "+err.Error())
	}
	cachedEnv = env
	return env, nil
}

// toolsHint renders the app-local tools directory for user messages.
func toolsHint(lay apppaths.Layout) string {
	if d := lay.ToolsFFmpegBin(); d != "" {
		return d
	}
	return "<app root>/tools/ffmpeg/bin"
}

// SetPathOverride stores a user-configured directory containing
// ffmpeg/ffprobe (applied on next DetectEnv(force=true)). It forwards
// to the single fflocate authority.
func SetPathOverride(dir string) { fflocate.SetDirOverride(dir) }

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

var encoderNameRe = regexp.MustCompile(`\s[A-Z0-9._]+\s+([A-Za-z0-9_]+)\s`)

func encoderPresent(encodersOutput, name string) bool {
	for _, line := range strings.Split(encodersOutput, "\n") {
		if m := encoderNameRe.FindStringSubmatch(line); len(m) > 1 && m[1] == name {
			return true
		}
	}
	return false
}

// ProbeHardwareEncoder runs a tiny proof encode with the given
// hardware encoder. Only a successful proof makes it selectable.
func ProbeHardwareEncoder(env *Environment, name string) error {
	if env == nil || env.FFmpegPath == "" {
		return fmt.Errorf("export: ffmpeg not available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	args := []string{
		"-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=red:s=256x256:r=30:d=0.5",
		"-c:v", name, "-frames:v", "8", "-y",
		"-f", "null", "-",
	}
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, env.FFmpegPath, args...)
	procattr.Configure(cmd) // never a visible child console on Windows
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("export: hardware encoder %s proof encode failed: %v: %s",
			name, err, firstLine(stderr.String()))
	}
	return nil
}

// ProofHardwareEncoders validates each listed hardware encoder with a
// small encode and records the ones that pass.
func ProofHardwareEncoders(env *Environment) {
	if env == nil {
		return
	}
	for _, name := range []string{"h264_nvenc", "h264_qsv", "h264_amf"} {
		if !env.Encoders[name] {
			continue
		}
		if err := ProbeHardwareEncoder(env, name); err == nil {
			env.HardwareEncoders = append(env.HardwareEncoders, name)
		}
	}
}

// EncoderChoice is the resolved encoder + args for a job.
type EncoderChoice struct {
	Name string   // libx264 / h264_nvenc / ...
	Args []string // codec-specific args appended before output
}

// ResolveEncoder picks the encoder: explicit choice if proven, else
// the best hardware encoder, else libx264, else any h264 encoder.
func ResolveEncoder(env *Environment, requested string) (EncoderChoice, error) {
	if env == nil || env.FFmpegPath == "" {
		return EncoderChoice{}, fmt.Errorf("export: ffmpeg not available")
	}
	pick := func(name string) (EncoderChoice, error) {
		switch name {
		case "libx264":
			return EncoderChoice{Name: name, Args: []string{"-crf", "18", "-preset", "medium"}}, nil
		case "h264_nvenc", "h264_qsv", "h264_amf":
			return EncoderChoice{Name: name, Args: []string{"-rc", "vbr", "-cq", "19", "-b:v", "0"}}, nil
		default:
			return EncoderChoice{Name: name}, nil
		}
	}
	req := strings.TrimSpace(requested)
	if req == "" || req == "auto" {
		if len(env.HardwareEncoders) > 0 {
			return pick(env.HardwareEncoders[0])
		}
		if env.Encoders["libx264"] {
			return pick("libx264")
		}
		return EncoderChoice{}, fmt.Errorf("export: no usable H.264 encoder found (install a build with libx264)")
	}
	if !env.Encoders[req] {
		return EncoderChoice{}, fmt.Errorf("export: encoder %s not available in the local FFmpeg build", req)
	}
	if req != "libx264" && req != "aac" {
		proven := false
		for _, h := range env.HardwareEncoders {
			if h == req {
				proven = true
				break
			}
		}
		if !proven {
			if err := ProbeHardwareEncoder(env, req); err != nil {
				return EncoderChoice{}, err
			}
		}
	}
	return pick(req)
}

// QualityArgs maps a quality preset to codec args (libx264 CRF).
func QualityArgs(preset string) string {
	switch strings.ToLower(preset) {
	case "balanced":
		return "21"
	case "compat":
		return "23"
	default:
		return "18"
	}
}

// SanitizeOutputPath guards the output location rules: absolute path,
// .mp4 suffix (a .part suffix is appended during the job).
func SanitizeOutputPath(path string) (string, error) {
	p := strings.TrimSpace(path)
	if p == "" {
		return "", fmt.Errorf("export: output path is empty")
	}
	if !strings.HasSuffix(strings.ToLower(p), ".mp4") {
		p += ".mp4"
	}
	return p, nil
}

// ParseProgress extracts the integer value of key= from an ffmpeg
// -progress line (e.g. "frame=42").
func ParseProgressLine(line string) (key string, value int, ok bool) {
	kv := strings.SplitN(strings.TrimSpace(line), "=", 2)
	if len(kv) != 2 {
		return "", 0, false
	}
	k := strings.TrimSpace(kv[0])
	v := strings.TrimSpace(kv[1])
	if n, err := strconv.Atoi(v); err == nil {
		return k, n, true
	}
	return k, 0, false
}

// EncoderList returns the names of detected encoders (hardware only
// when hw is true).
func (e *Environment) EncoderList(hw bool) []string {
	if e == nil {
		return nil
	}
	var out []string
	for _, n := range []string{"libx264", "h264_nvenc", "h264_qsv", "h264_amf", "libfdk_aac"} {
		if e.Encoders[n] {
			if hw && (n == "libx264" || n == "libfdk_aac") {
				continue
			}
			out = append(out, n)
		}
	}
	return out
}
