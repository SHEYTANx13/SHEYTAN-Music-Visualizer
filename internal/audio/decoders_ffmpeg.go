package audio

// decoders_ffmpeg.go — OPTIONAL decoder adapter for AAC family inputs
// (M4A/MP4 containers, raw AAC/ADTS) through the user's LOCAL FFmpeg
// installation.
//
// Roadmap context: M4A/AAC input was explicitly listed as NOT
// IMPLEMENTED in the Phase 2 roadmap (a licensed first-party AAC
// decoder is deliberately not bundled; no LGPL codec module was
// added). This adapter closes the gap the honest way:
//
//   - The adapter is OPTIONAL. FFmpeg/ffprobe are never bundled,
//     never downloaded, and never a mandatory runtime requirement:
//     when the binaries are absent the formats are simply not
//     registered and the registry reports exactly the four native
//     decoders (the UI file dialog follows the registry by
//     construction).
//   - The child process is invoked with an explicit argument slice
//     (no shell), bounded output, and context-aware cancellation:
//     cancelling an import kills ffmpeg immediately instead of
//     waiting for the next chunk.
//   - The registry reports NativeDecoder=false for these containers —
//     provenance is truthful in the canonical-cache sidecar.
//
// No third-party Go dependency is introduced; the adapter shells out
// to the same local encoder the export pipeline already requires.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/apppaths"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/fflocate"
)

// ffmpegExecTimeout bounds the short ffprobe/version child processes;
// the decode stream itself is bounded by the import context and the
// shared duration limits instead.
const ffmpegExecTimeout = 30 * time.Second

// ffmpegCloseGrace bounds how long Close waits for a still-running
// decode child before killing it.
const ffmpegCloseGrace = 5 * time.Second

// FFmpegAdapterStatus reports why the optional AAC adapter is (not)
// registered, for diagnostics and honest UI display.
type FFmpegAdapterStatus struct {
	Available   bool   `json:"available"`
	FFmpegPath  string `json:"ffmpegPath,omitempty"`
	FFprobePath string `json:"ffprobePath,omitempty"`
	Version     string `json:"version,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

var ffmpegStatus FFmpegAdapterStatus

// FFmpegAdapter reports the current adapter availability.
func FFmpegAdapter() FFmpegAdapterStatus { return ffmpegStatus }

// ffmpegBinLocater returns the ffmpeg and ffprobe executable paths.
// Tests inject fixed paths through RegisterFFmpegOptional's locator.
type ffmpegBinLocater func() (ffmpeg, ffprobe string, err error)

// defaultFFmpegLocater resolves the binaries through the single
// fflocate authority: the explicit SHEYTAN_FFMPEG directory override
// (user configuration), the app-local tools directory
// (<app root>/tools/ffmpeg/bin), then PATH.
func defaultFFmpegLocater() (string, string, error) {
	loc, err := fflocate.Locate(fflocate.OPOpts(
		apppaths.Resolve(apppaths.OSEnv()).ToolsFFmpegBin()))
	if err != nil {
		return "", "", fmt.Errorf("audio: %w (the optional M4A/AAC adapter stays unregistered)", err)
	}
	return loc.FFmpeg, loc.FFprobe, nil
}

// RegisterFFmpegOptional registers the M4A/AAC decoder family when a
// usable local FFmpeg/ffprobe pair is available. It returns the
// adapter status; callers may ignore it. Registration is idempotent.
func RegisterFFmpegOptional() FFmpegAdapterStatus {
	return registerFFmpegOptionalWith(defaultFFmpegLocater)
}

func registerFFmpegOptionalWith(locater ffmpegBinLocater) FFmpegAdapterStatus {
	ff, fp, err := locater()
	if err != nil {
		ffmpegStatus = FFmpegAdapterStatus{Available: false, Reason: err.Error()}
		return ffmpegStatus
	}
	version := probeFFmpegVersion(ff)
	if version == "" {
		ffmpegStatus = FFmpegAdapterStatus{
			Available: false, FFmpegPath: ff, FFprobePath: fp,
			Reason: "audio: ffmpeg did not answer `-version` (not a usable FFmpeg binary)",
		}
		return ffmpegStatus
	}
	RegisterFormat(SourceFormat{
		Container: "m4a", Extensions: []string{"m4a", "mp4", "m4b"},
		Classifier: "AAC in M4A/MP4 (via local FFmpeg)", Lossy: true, NativeDecoder: false,
	}, func(path string) (Decoder, error) { return newFFmpegDecoder(path, ff, fp) })
	RegisterFormat(SourceFormat{
		Container: "aac", Extensions: []string{"aac", "adts"},
		Classifier: "raw AAC/ADTS (via local FFmpeg)", Lossy: true, NativeDecoder: false,
	}, func(path string) (Decoder, error) { return newFFmpegDecoder(path, ff, fp) })
	// Apple Lossless is decoded through the same local binary.
	RegisterFormat(SourceFormat{
		Container: "alac", Extensions: []string{"alac"},
		Classifier: "ALAC (via local FFmpeg)", NativeDecoder: false,
	}, func(path string) (Decoder, error) { return newFFmpegDecoder(path, ff, fp) })
	// Sidecar provenance for the optional containers.
	DecoderIDs["m4a"] = "local FFmpeg AAC decoder adapter (optional; not bundled)"
	DecoderIDs["aac"] = "local FFmpeg AAC decoder adapter (optional; not bundled)"
	DecoderIDs["alac"] = "local FFmpeg ALAC decoder adapter (optional; not bundled)"
	ffmpegStatus = FFmpegAdapterStatus{Available: true, FFmpegPath: ff, FFprobePath: fp, Version: version}
	return ffmpegStatus
}

func probeFFmpegVersion(ff string) string {
	ctx, cancel := context.WithTimeout(context.Background(), ffmpegExecTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, ff, "-version").Output()
	if err != nil {
		return ""
	}
	line := strings.SplitN(string(out), "\n", 2)[0]
	if !strings.HasPrefix(line, "ffmpeg version") {
		return ""
	}
	return strings.TrimSpace(line)
}

// ffprobeStream is the subset of ffprobe JSON the adapter consumes.
type ffprobeStream struct {
	Index       int            `json:"index"`
	CodecType   string         `json:"codec_type"`
	CodecName   string         `json:"codec_name"`
	SampleRate  string         `json:"sample_rate"`
	Channels    int            `json:"channels"`
	Duration    string         `json:"duration"`
	BitRate     string         `json:"bit_rate"`
	SampleFmt   string         `json:"sample_fmt"`
	Disposition map[string]int `json:"disposition"`
}

type ffprobeFormat struct {
	Duration   string `json:"duration"`
	FormatName string `json:"format_name"`
}

type ffprobeOut struct {
	Streams []ffprobeStream `json:"streams"`
	Format  ffprobeFormat   `json:"format"`
}

// ffmpegDecoder implements Decoder for AAC-family containers via a
// local FFmpeg. Single-goroutine ownership, like every decoder.
type ffmpegDecoder struct {
	path    string
	ffmpeg  string
	ffprobe string
	md      Metadata
	// decode-time state
	mu      sync.Mutex
	cmd     *exec.Cmd
	pipeR   io.ReadCloser
	kill    context.CancelFunc
	waited  bool
	waitErr error
	ctx     context.Context // decode context (optional; SetDecodeContext)
}

// SetDecodeContext lets Import hand its cancellation context to the
// adapter so a cancelled import kills the ffmpeg child immediately
// (the Decoder interface is context-free; this optional extension is
// detected via interface assertion and is a no-op for native
// decoders).
func (d *ffmpegDecoder) SetDecodeContext(ctx context.Context) {
	d.ctx = ctx
}

// NewFFmpegAdapterDecoder is exported for direct tests/diagnostics.
func NewFFmpegAdapterDecoder(path, ffmpeg, ffprobe string) (Decoder, error) {
	return newFFmpegDecoder(path, ffmpeg, ffprobe)
}

func newFFmpegDecoder(path, ffmpeg, ffprobe string) (Decoder, error) {
	if path == "" {
		return nil, fmt.Errorf("audio: path required")
	}
	if err := checkSourceSize(path); err != nil {
		return nil, err
	}
	return &ffmpegDecoder{path: path, ffmpeg: ffmpeg, ffprobe: ffprobe}, nil
}

// Probe validates the stream via ffprobe and validates the shared
// decoder limits before any PCM is produced.
func (d *ffmpegDecoder) Probe() (Metadata, error) {
	if d.md.FileSize != 0 {
		return d.md, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), ffmpegExecTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, d.ffprobe,
		"-v", "error",
		"-print_format", "json",
		"-show_streams", "-show_format",
		"--", d.path,
	).Output()
	if err != nil {
		return d.md, fmt.Errorf("audio: ffprobe failed for %s: %w", filepath.Base(d.path), err)
	}
	var po ffprobeOut
	if jerr := json.Unmarshal(out, &po); jerr != nil {
		return d.md, fmt.Errorf("audio: ffprobe output not parseable: %w", jerr)
	}
	var audio *ffprobeStream
	for i := range po.Streams {
		if po.Streams[i].CodecType == "audio" {
			audio = &po.Streams[i]
			break
		}
	}
	if audio == nil {
		return d.md, fmt.Errorf("%w: %s has no audio stream", ErrUnsupported, filepath.Base(d.path))
	}
	rate := 0
	fmt.Sscanf(strings.TrimSpace(audio.SampleRate), "%d", &rate)
	ch := audio.Channels
	if rate <= 0 || ch <= 0 || ch > MaxChannels {
		return d.md, fmt.Errorf("audio: %s has invalid audio format %d Hz %d ch", filepath.Base(d.path), rate, ch)
	}
	dur := parseFFloat(po.Format.Duration)
	if dur <= 0 {
		dur = parseFFloat(audio.Duration)
	}
	if dur > MaxDurationSeconds {
		return d.md, fmt.Errorf("%w: %s", ErrUnsupported, truncateWarning("duration", MaxDurationSeconds))
	}
	codec := strings.ToUpper(strings.TrimSpace(audio.CodecName))
	if codec == "" {
		codec = "AAC"
	}
	container := "m4a"
	if strings.Contains(po.Format.FormatName, "adts") || strings.Contains(po.Format.FormatName, "aac") {
		container = "aac"
	}
	d.md = Metadata{
		Container:  container,
		Codec:      codec,
		SampleRate: rate,
		Channels:   ch,
		BitDepth:   0, // lossy compressed; the source bit depth is not meaningful
		Duration:   dur,
		FileSize:   fileSize(d.path),
		Classifier: fmt.Sprintf("%s %d Hz %d ch (via local FFmpeg)", codec, rate, ch),
	}
	if d.md.Duration > MaxDurationSeconds {
		return d.md, fmt.Errorf("%w: %s", ErrUnsupported, truncateWarning("duration", MaxDurationSeconds))
	}
	return d.md, nil
}

// SourceFormat reports the decoded wire layout: interleaved float32
// little-endian at the source rate/channels (ffmpeg -f f32le).
func (d *ffmpegDecoder) SourceFormat() (int, int, SampleEncoding) {
	return d.md.SampleRate, d.md.Channels, EncFloat32LE
}

// PCMReader starts ffmpeg and returns its f32le stdout stream. The
// reader surfaces decoder failures as real read errors: on stream end
// the child is waited for and a nonzero exit becomes an error instead
// of a silent EOF.
func (d *ffmpegDecoder) PCMReader() (io.Reader, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cmd != nil {
		return nil, fmt.Errorf("audio: ffmpeg decoder already streaming")
	}
	runCtx, cancel := context.WithCancel(context.Background())
	args := []string{
		"-v", "error", "-nostdin",
		"-i", d.path,
		"-map", "0:a:0", "-vn",
		"-f", "f32le", "-acodec", "pcm_f32le",
		"-",
	}
	cmd := exec.CommandContext(runCtx, d.ffmpeg, args...)
	pipeR, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("audio: ffmpeg stdout pipe: %w", err)
	}
	// NOTE: cmd.Wait() closes StdoutPipe — it must run only AFTER the
	// reader finished (documented os/exec race). The child is reaped
	// in Close(), never in a background goroutine.
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("audio: ffmpeg start: %w", err)
	}
	d.cmd = cmd
	d.pipeR = pipeR
	d.kill = cancel
	return &ffmpegPipeReader{d: d}, nil
}

// Close terminates a running ffmpeg child and reaps it exactly once
// (idempotent, safe to call from Read paths and deferrals).
//
// Exit-status discipline: when the child has already exited (the EOF
// path), Wait's REAL status is captured BEFORE any context
// cancellation — canceling a CommandContext before Wait makes Wait
// report "context canceled" instead of the true native exit code,
// which would turn successful decodes into failures. A still-running
// child gets a short grace period and is then killed.
func (d *ffmpegDecoder) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cmd == nil || d.waited {
		d.pipeR = nil
		return nil
	}
	cmd, cancel := d.cmd, d.kill
	if cancel != nil && d.ctx != nil && d.ctx.Err() != nil {
		// Cancelled decode: kill immediately; Wait reports the
		// cancellation and the reader maps it to ctx.Err().
		cancel()
	}
	waitCh := make(chan struct{})
	go func() {
		d.waitErr = cmd.Wait() // also closes the stdout pipe
		close(waitCh)
	}()
	select {
	case <-waitCh:
		// Natural exit: the real exit status is preserved.
	case <-time.After(ffmpegCloseGrace):
		if cancel != nil {
			cancel()
		}
		<-waitCh
	}
	d.waited = true
	d.pipeR = nil
	return nil
}

// ffmpegPipeReader wraps the ffmpeg stdout pipe with honest error
// reporting and cancellation. A cancelled decode context or a Close()
// kills the child; a nonzero child exit is surfaced as a read error,
// never as a silent EOF.
type ffmpegPipeReader struct {
	d *ffmpegDecoder
}

func (r *ffmpegPipeReader) Read(p []byte) (int, error) {
	d := r.d
	d.mu.Lock()
	pr := d.pipeR
	d.mu.Unlock()
	if pr == nil {
		return 0, io.ErrClosedPipe
	}
	if ctx := d.ctx; ctx != nil {
		if ctx.Err() != nil {
			d.Close()
			return 0, ctx.Err()
		}
	}
	n, err := pr.Read(p)
	if err == io.EOF {
		// Stream ended: reap the child and distinguish a clean finish
		// from a decoder failure.
		d.Close()
		if ctx := d.ctx; ctx != nil && ctx.Err() != nil {
			return n, ctx.Err()
		}
		if d.waitErr != nil {
			return n, fmt.Errorf("audio: ffmpeg decode exited with error: %v", d.waitErr)
		}
		return n, io.EOF
	}
	if err != nil {
		d.Close()
		if ctx := d.ctx; ctx != nil && ctx.Err() != nil {
			return n, ctx.Err()
		}
		return n, err
	}
	return n, nil
}

// contextAwareDecode is an optional Decoder extension for adapters
// that can honour the import context during blocking reads.
type contextAwareDecoder interface {
	SetDecodeContext(ctx context.Context)
}

// attachDecodeContext connects the import context to decoders that
// support it (the FFmpeg adapter). Called by Import after Open.
func attachDecodeContext(dec Decoder, ctx context.Context) {
	if cd, ok := dec.(contextAwareDecoder); ok {
		cd.SetDecodeContext(ctx)
	}
}

func parseFFloat(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "N/A" {
		return 0
	}
	var v float64
	fmt.Sscanf(s, "%g", &v)
	return v
}
