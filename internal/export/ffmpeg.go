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

// Resolution presets with unique canonical IDs (run 43712071111:
// data-driven list, exactly ONE 4K entry — 4K UHD 3840x2160; no
// duplicate generic "4K" label exists or may be added).
var Resolutions = []struct {
	ID     string
	Name   string
	Width  int
	Height int
}{
	{"720p", "720p HD", 1280, 720},
	{"1080p", "1080p Full HD", 1920, 1080},
	{"1440p", "1440p QHD", 2560, 1440},
	{"4k-uhd", "4K UHD", 3840, 2160},
}

// ResolutionByID returns the preset with the exact canonical ID.
func ResolutionByID(id string) (int, int, bool) {
	for _, r := range Resolutions {
		if r.ID == id {
			return r.Width, r.Height, true
		}
	}
	return 0, 0, false
}

// Frame rate choices. 120 fps is a hard functional target (run
// 43612608237): the encoded output cadence, NOT a live-preview rate —
// offline export schedules frames deterministically at t=frameIndex/fps
// and never needs to render 120 frames per wall-clock second.
var FrameRates = []int{24, 30, 60, 120}

// Quality presets (encoder-dependent knobs resolved at job time).
var QualityPresets = []string{"high", "balanced", "compat"}

// Environment is the detected local FFmpeg environment.
type Environment struct {
	FFmpegPath       string          `json:"ffmpegPath"`
	FFprobePath      string          `json:"ffprobePath"`
	Source           string          `json:"source,omitempty"` // fflocate rule: override / app-local / system / path
	Version          string          `json:"version"`
	Encoders         map[string]bool `json:"encoders"`                   // encoder name -> present
	HardwareEncoders []string        `json:"hardwareEncoders,omitempty"` // proof-encoded OK
	Notes            []string        `json:"notes,omitempty"`

	// filters caches the probed `-filters` table (internal only, never
	// serialized). Owned by the env mutex (see HasVideoFilter).
	filters map[string]bool `json:"-"`
}

var (
	envMu     sync.Mutex
	cachedEnv *Environment
)

// clone deep-copies an Environment so callers can never race the
// process cache (run 43541087097 repair: the cached pointer was handed
// out shared, and ProofHardwareEncoders mutated its slices/maps
// unsynchronized).
func (e *Environment) clone() *Environment {
	if e == nil {
		return nil
	}
	c := &Environment{
		FFmpegPath: e.FFmpegPath, FFprobePath: e.FFprobePath,
		Source: e.Source, Version: e.Version,
		Encoders:         make(map[string]bool, len(e.Encoders)),
		HardwareEncoders: append([]string(nil), e.HardwareEncoders...),
		Notes:            append([]string(nil), e.Notes...),
	}
	for k, v := range e.Encoders {
		c.Encoders[k] = v
	}
	return c
}

// DetectEnv probes the local ffmpeg/ffprobe through the single
// fflocate resolution order: explicit user configuration (including
// the SHEYTAN_FFMPEG directory), the app-local tools directory
// (<app root>/tools/ffmpeg/bin), then the system PATH. Successful
// results are cached per process (a deep copy is returned; force=true
// re-probes after the user changes the path). FAILURES ARE NEVER
// CACHED (run 43541087097 repair: a cached failed detection used to
// surface as an empty environment with a NIL error on the next call,
// silently skipping the "Export requires FFmpeg" dialog) — the next
// call re-probes, so a transient timeout or an FFmpeg installed while
// the app runs recovers without a restart. The error when nothing is
// found names every searched location and the actionable next step.
func DetectEnv(force bool) (*Environment, error) {
	envMu.Lock()
	defer envMu.Unlock()
	if cachedEnv != nil && !force {
		return cachedEnv.clone(), nil
	}
	env := &Environment{Encoders: map[string]bool{}}
	lay := apppaths.Resolve(apppaths.OSEnv())
	loc, lerr := fflocate.Locate(fflocate.OPOptsFull(lay.ToolsFFmpegBin(), lay.MediaToolsBin()))
	if lerr != nil {
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
		return env, fmt.Errorf("ffmpeg -version failed for %q: %w", loc.Source, err)
	}
	env.Version = firstLine(string(out))

	// encoders list
	ecmd := exec.CommandContext(ctx, env.FFmpegPath, "-hide_banner", "-encoders")
	procattr.Configure(ecmd) // never a visible child console on Windows
	encOut, err := ecmd.Output()
	if err == nil {
		for _, name := range []string{"libx264", "libx265", "h264_nvenc", "h264_qsv", "h264_amf",
			"hevc_nvenc", "hevc_qsv", "hevc_amf", "aac", "libfdk_aac"} {
			if encoderPresent(string(encOut), name) {
				env.Encoders[name] = true
			}
		}
	} else {
		env.Notes = append(env.Notes, "encoder listing failed: "+err.Error())
	}
	cachedEnv = env
	return env.clone(), nil
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

// HasVideoFilter reports whether this FFmpeg build supports the named
// video filter (probed once per binary via `-filters`, cached under the
// same env mutex as the process cache). Run 46168717789: the subtitle
// burn-in feature needs libass ("subtitles" filter); a build without it
// is refused with an actionable error BEFORE any frame is rendered —
// never an opaque mid-export failure.
func HasVideoFilter(env *Environment, name string) bool {
	if env == nil || name == "" {
		return false
	}
	envMu.Lock()
	defer envMu.Unlock()
	if cachedEnv != nil && cachedEnv.FFmpegPath == env.FFmpegPath {
		if cachedEnv.filters == nil {
			if err := probeFilters(env); err != nil {
				return false
			}
			cachedEnv.filters = env.filters
		}
		return cachedEnv.filters[name]
	}
	if err := probeFilters(env); err != nil {
		return false
	}
	return env.filters[name]
}

// probeFilters runs `ffmpeg -filters` and records the supported filter
// names on env. Errors leave the map empty (callers report honestly).
func probeFilters(env *Environment) error {
	if env.filters == nil {
		env.filters = map[string]bool{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, env.FFmpegPath,
		"-hide_banner", "-filters")
	procattr.Configure(cmd)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("export: probing ffmpeg filters: %w", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		// `ffmpeg -filters` rows: " T.. name       description".
		if len(fields) >= 3 {
			env.filters[fields[1]] = true
		}
	}
	return nil
}

// SubtitleFilterArg builds the `-vf` value that burns a subtitle file
// into the video. FFmpeg accepts every format its libass bridge reads
// (SRT, ASS/SSA, WebVTT, MicroDVD, SubViewer, ...): the formats are the
// filter's contract, not ours. The path is escaped per the ffmpeg
// filter quoting rules (backslash, colon, quote) so Windows drive
// letters and spaces survive the filter parser; no shell is involved
// (exec argument slice only).
func SubtitleFilterArg(path string) string {
	// Explicit backslash normalization (NOT filepath.ToSlash, which is
	// a no-op on POSIX hosts): the filter parser needs forward slashes
	// on every platform, including Windows drive paths.
	p := strings.ReplaceAll(path, "\\", "/")
	p = strings.ReplaceAll(p, "\\", "\\\\")
	p = strings.ReplaceAll(p, ":", "\\:")
	p = strings.ReplaceAll(p, "'", "\\'")
	return "subtitles='" + p + "'"
}

// ProofHardwareEncoders validates each listed hardware encoder with a
// small encode and records the ones that pass on the caller's copy AND
// (under the cache mutex, keyed by the same binary) in the process
// cache, so proofs accumulate across dialogs without re-encoding.
func ProofHardwareEncoders(env *Environment) {
	if env == nil {
		return
	}
	var proved []string
	for _, name := range []string{"h264_nvenc", "h264_qsv", "h264_amf", "hevc_nvenc", "hevc_qsv", "hevc_amf"} {
		if !env.Encoders[name] {
			continue
		}
		if err := ProbeHardwareEncoder(env, name); err == nil {
			proved = append(proved, name)
		}
	}
	if len(proved) == 0 {
		return
	}
	envMu.Lock()
	env.HardwareEncoders = appendUnique(env.HardwareEncoders, proved)
	if cachedEnv != nil && cachedEnv.FFmpegPath == env.FFmpegPath {
		cachedEnv.HardwareEncoders = appendUnique(cachedEnv.HardwareEncoders, proved)
	}
	envMu.Unlock()
}

// appendUnique appends names that are not already present.
func appendUnique(dst, names []string) []string {
	for _, n := range names {
		found := false
		for _, d := range dst {
			if d == n {
				found = true
				break
			}
		}
		if !found {
			dst = append(dst, n)
		}
	}
	return dst
}

// EncoderChoice is the resolved encoder + args for a job.
type EncoderChoice struct {
	Name string   // libx264 / h264_nvenc / ...
	Args []string // codec-specific args appended before output
}

// VideoCodec values for jobs.
const (
	CodecH264 = "h264"
	CodecHEVC = "hevc"
)

// encodersForCodec lists the candidate encoders for a codec, in
// preference order (software first for determinism unless a proven
// hardware encoder exists).
func encodersForCodec(codec string) (software string, hardware []string, err error) {
	switch codec {
	case "", CodecH264:
		return "libx264", []string{"h264_nvenc", "h264_qsv", "h264_amf"}, nil
	case CodecHEVC:
		return "libx265", []string{"hevc_nvenc", "hevc_qsv", "hevc_amf"}, nil
	default:
		return "", nil, fmt.Errorf("export: unsupported video codec %q (use h264 or hevc)", codec)
	}
}

// ResolveEncoderCodec picks the encoder for a codec: explicit choice
// if present (hardware picks must also be proven), else the first
// PROVEN hardware encoder for that codec, else the software encoder.
func ResolveEncoderCodec(env *Environment, requested, codec string) (EncoderChoice, error) {
	if env == nil || env.FFmpegPath == "" {
		return EncoderChoice{}, fmt.Errorf("export: ffmpeg not available")
	}
	software, hardwareList, err := encodersForCodec(codec)
	if err != nil {
		return EncoderChoice{}, err
	}
	isHardware := func(name string) bool {
		for _, h := range hardwareList {
			if h == name {
				return true
			}
		}
		return false
	}
	pick := func(name string) (EncoderChoice, error) {
		if isHardware(name) {
			return EncoderChoice{Name: name, Args: []string{"-rc", "vbr", "-cq", "19", "-b:v", "0"}}, nil
		}
		// Software CRF is applied by the job from the quality preset.
		return EncoderChoice{Name: name, Args: []string{}}, nil
	}
	req := strings.TrimSpace(requested)
	if req != "" && req != "auto" {
		if !env.Encoders[req] {
			return EncoderChoice{}, fmt.Errorf("export: encoder %s not available in the local FFmpeg build", req)
		}
		if isHardware(req) {
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
	// auto: first proven hardware encoder for the codec, else software.
	for _, h := range env.HardwareEncoders {
		if isHardware(h) {
			return pick(h)
		}
	}
	if env.Encoders[software] {
		return pick(software)
	}
	return EncoderChoice{}, fmt.Errorf("export: no usable %s encoder found (install a build with %s)",
		codecNameForError(codec), software)
}

func codecNameForError(codec string) string {
	if codec == CodecHEVC {
		return "H.265/HEVC"
	}
	return "H.264"
}

// ResolveEncoder keeps the historical H.264-only entry point.
func ResolveEncoder(env *Environment, requested string) (EncoderChoice, error) {
	return ResolveEncoderCodec(env, requested, CodecH264)
}

// CodecEncoderList lists the local encoders available for a codec
// (software + proven hardware), for UI capability lines.
func (e *Environment) CodecEncoderList(codec string) []string {
	if e == nil {
		return nil
	}
	var want []string
	if codec == CodecHEVC {
		want = []string{"libx265", "hevc_nvenc", "hevc_qsv", "hevc_amf"}
	} else {
		want = []string{"libx264", "h264_nvenc", "h264_qsv", "h264_amf"}
	}
	var out []string
	for _, n := range want {
		if e.Encoders[n] {
			out = append(out, n)
		}
	}
	return out
}

// HEVCEncoders lists PROVEN hardware HEVC encoders (proof-encoded).
func (e *Environment) HEVCEncoders(_ *Environment) []string {
	if e == nil {
		return nil
	}
	var out []string
	for _, n := range []string{"hevc_nvenc", "hevc_qsv", "hevc_amf"} {
		for _, h := range e.HardwareEncoders {
			if h == n {
				out = append(out, n)
			}
		}
	}
	return out
}

// H264HardwareEncoders lists PROVEN hardware H.264 encoders.
func (e *Environment) H264HardwareEncoders(_ *Environment) []string {
	if e == nil {
		return nil
	}
	var out []string
	for _, n := range []string{"h264_nvenc", "h264_qsv", "h264_amf"} {
		for _, h := range e.HardwareEncoders {
			if h == n {
				out = append(out, n)
			}
		}
	}
	return out
}

// x264PresetFor picks the x264 speed/quality preset for a job. 4K and
// 120 fps exports use "faster": at 3840x2160@120 the "medium" preset
// cannot reach realistic encode throughput on mid-range CPUs, while the
// CRF rate-control (not the preset) governs the visual quality level.
// Lower resolutions at <=60 fps keep the "medium" tuning.
func x264PresetFor(width, height, fps int) string {
	if width*height >= 2560*1440 || fps >= 120 {
		return "faster"
	}
	return "medium"
}

// QualityArgs maps a quality preset to the libx264 CRF value.
func QualityArgs(preset string) string {
	return qualityCRF(preset, CodecH264)
}

// qualityCRF maps a quality preset to the codec CRF scale. libx264
// and libx265 CRF scales differ (x265 needs ~+3 for a comparable
// bitrate); documented defaults per codec and preset:
//
//	           H.264 (libx264)   H.265 (libx265)
//	high              18              20
//	balanced          21              23
//	compat            23              26
func qualityCRF(preset, codec string) string {
	x265 := codec == CodecHEVC
	switch strings.ToLower(preset) {
	case "balanced":
		if x265 {
			return "23"
		}
		return "21"
	case "compat":
		if x265 {
			return "26"
		}
		return "23"
	default: // high
		if x265 {
			return "20"
		}
		return "18"
	}
}

// encoderPresetFor picks the speed/quality preset for a software
// encoder (libx264/libx265 share the preset names).
func encoderPresetFor(width, height int, fps float64) string {
	if width*height >= 2560*1440 || fps >= 120 {
		return "faster"
	}
	return "medium"
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
	for _, n := range []string{"libx264", "libx265", "h264_nvenc", "h264_qsv", "h264_amf",
		"hevc_nvenc", "hevc_qsv", "hevc_amf", "libfdk_aac"} {
		if e.Encoders[n] {
			if hw && (n == "libx264" || n == "libx265" || n == "libfdk_aac") {
				continue
			}
			out = append(out, n)
		}
	}
	return out
}
