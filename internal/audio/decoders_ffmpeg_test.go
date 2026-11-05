package audio

// decoders_ffmpeg_test.go — fixtures for the OPTIONAL M4A/AAC decoder
// adapter. The tests generate real AAC fixtures with the local ffmpeg
// when one is available; on hosts without FFmpeg every fixture skips
// with an explicit NOT AVAILABLE reason (never a fake PASS). The
// optional-adapter registration path is exercised without FFmpeg too,
// so the "formats stay unregistered" guarantee is verified everywhere.

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// requireFFmpeg skips the test honestly when no usable ffmpeg exists.
func requireFFmpeg(t *testing.T) string {
	t.Helper()
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("SKIP (NOT AVAILABLE): no ffmpeg binary on PATH; the optional M4A/AAC adapter cannot be exercised on this host")
	}
	return ff
}

// makeAACFixture generates a 1 s 440 Hz sine in an .m4a (AAC) file.
func makeAACFixture(t *testing.T, dir string) string {
	t.Helper()
	out := filepath.Join(dir, "sine-440.m4a")
	cmd := exec.Command("ffmpeg", "-y", "-loglevel", "error",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=1:sample_rate=48000",
		"-c:a", "aac", "-b:a", "128k", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("SKIP (NOT AVAILABLE): ffmpeg could not generate the AAC fixture: %v: %s", err, b)
	}
	fi, err := os.Stat(out)
	if err != nil || fi.Size() == 0 {
		t.Fatalf("AAC fixture missing/empty: %v", err)
	}
	return out
}

// Fixture: probe reports the AAC container truthfully through the
// adapter.
func TestFFmpegAdapterProbeM4A(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	src := makeAACFixture(t, dir)

	d, err := NewFFmpegAdapterDecoder(src, "ffmpeg", "ffprobe")
	if err != nil {
		t.Fatalf("adapter open: %v", err)
	}
	defer d.Close()
	md, err := d.Probe()
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if md.Container != "m4a" {
		t.Fatalf("container = %q, want m4a", md.Container)
	}
	if !strings.Contains(strings.ToLower(md.Codec), "aac") {
		t.Fatalf("codec = %q, want AAC family", md.Codec)
	}
	if md.SampleRate <= 0 || md.Channels <= 0 || md.Channels > MaxChannels {
		t.Fatalf("format = %d Hz %d ch", md.SampleRate, md.Channels)
	}
	if md.Duration < 0.8 || md.Duration > 1.3 {
		t.Fatalf("duration = %.2f s, want ~1 s", md.Duration)
	}
	if md.Classifier == "" || !strings.Contains(md.Classifier, "FFmpeg") {
		t.Fatalf("classifier %q must state the local-FFmpeg provenance", md.Classifier)
	}
}

// Fixture: a full import of an AAC file through the REAL pipeline
// (probe → PCM stream → canonical 48 kHz stereo cache). This is the
// user-visible behavior: importing .m4a works end to end.
func TestFFmpegAdapterImportPipeline(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	src := makeAACFixture(t, dir)

	cacheDir := filepath.Join(dir, "pcm-cache")
	t.Setenv("SHEYTAN_DATA_DIR", cacheDir)
	cache, err := NewCache(cacheDir)
	if err != nil {
		t.Fatalf("cache: %v", err)
	}
	RegisterFFmpegOptional()
	if !FFmpegAdapter().Available {
		t.Fatal("adapter must be available after registration (ffmpeg present)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	tr, err := Import(ctx, src, cache, nil)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if tr.Metadata.Container != "m4a" {
		t.Fatalf("container = %q, want m4a", tr.Metadata.Container)
	}
	// 1 s at 48 kHz: AAC decodes in 1024-sample units, so the raw PCM
	// stream carries ceil(48000/1024)=47 packets = 48128 frames (the
	// container edit list trims playback, not the raw decode).
	if tr.Frames < 47800 || tr.Frames > 48500 {
		t.Fatalf("frames = %d, want ~48128 (47 AAC packets of 1024)", tr.Frames)
	}
	// The decoded signal must be non-silent: read a slice of the raw
	// canonical cache and check RMS in a musical range.
	raw, err := os.ReadFile(cache.RawPath(tr.Fingerprint))
	if err != nil {
		t.Fatalf("raw cache: %v", err)
	}
	n := len(raw) / 4
	if n < 1000 {
		t.Fatalf("canonical PCM too small: %d samples", n)
	}
	if n > 48000 {
		n = 48000
	}
	var sum float64
	samples := make([]byte, 4)
	for i := 0; i < n; i++ {
		copy(samples, raw[i*4:(i+1)*4])
		v := float64(math.Float32frombits(binary.LittleEndian.Uint32(samples)))
		sum += v * v
	}
	rms := math.Sqrt(sum / float64(n))
	if rms < 0.01 || rms > 1.0 {
		t.Fatalf("RMS = %.4f, want a bounded non-silent signal", rms)
	}
	// Sidecar provenance must name the adapter, not a native decoder.
	sc, err := cache.Lookup(tr.Fingerprint)
	if err != nil {
		t.Fatalf("sidecar: %v", err)
	}
	if !strings.Contains(sc.Decoder, "FFmpeg") {
		t.Fatalf("sidecar decoder = %q, want the FFmpeg adapter provenance", sc.Decoder)
	}
	if sc.Container != "m4a" {
		t.Fatalf("sidecar container = %q, want m4a", sc.Container)
	}
}

// Fixture: cache round trip — importing the same AAC file twice hits
// the canonical cache (identical fingerprint), not a second decode.
func TestFFmpegAdapterCacheHit(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	src := makeAACFixture(t, dir)
	cacheDir := filepath.Join(dir, "pcm-cache")
	t.Setenv("SHEYTAN_DATA_DIR", cacheDir)
	cache, err := NewCache(cacheDir)
	if err != nil {
		t.Fatalf("cache: %v", err)
	}
	RegisterFFmpegOptional()
	ctx := context.Background()
	tr1, err := Import(ctx, src, cache, nil)
	if err != nil {
		t.Fatalf("import 1: %v", err)
	}
	tr2, err := Import(ctx, src, cache, nil)
	if err != nil {
		t.Fatalf("import 2: %v", err)
	}
	if tr1.Fingerprint != tr2.Fingerprint || tr1.Frames != tr2.Frames {
		t.Fatalf("cache hit mismatch: %+v vs %+v", tr1, tr2)
	}
}

// Fixture: decode-failure honesty — an invalid "AAC" file surfaces a
// probe error instead of being silently accepted.
func TestFFmpegAdapterRejectsGarbage(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	bad := filepath.Join(dir, "garbage.m4a")
	if err := os.WriteFile(bad, bytes.Repeat([]byte{0xFF}, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := NewFFmpegAdapterDecoder(bad, "ffmpeg", "ffprobe")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()
	if _, err := d.Probe(); err == nil {
		t.Fatal("probe of garbage input must fail")
	}
}

// Fixture: without FFmpeg the optional formats are NOT registered and
// the registry still reports exactly the four native decoders.
func TestFFmpegAdapterOptionalWhenAbsent(t *testing.T) {
	// Start from a clean registry: earlier tests in this binary may
	// have registered the optional formats (global registry state).
	resetForTest()
	RegisterAll()
	// Locator that simulates a host without ffmpeg/ffprobe.
	status := registerFFmpegOptionalWith(func() (string, string, error) {
		return "", "", fmt.Errorf("audio: ffmpeg not found on PATH (the optional M4A/AAC adapter stays unregistered)")
	})
	if status.Available {
		t.Fatal("adapter must not be available without ffmpeg")
	}
	if status.Reason == "" {
		t.Fatal("the status must carry the exact reason")
	}
	// The native four are unaffected.
	got := map[string]bool{}
	for _, f := range SupportedFormats() {
		got[f.Container] = true
	}
	for _, want := range []string{"wav", "mp3", "flac", "ogg"} {
		if !got[want] {
			t.Fatalf("native decoder %s missing after optional registration attempt", want)
		}
	}
	if got["m4a"] {
		t.Fatal("m4a must stay unregistered when ffmpeg is absent")
	}
	// Opening an m4a on such a host fails with ErrUnsupported BEFORE
	// any file access.
	if _, _, err := Open(filepath.Join(t.TempDir(), "song.m4a")); err == nil {
		t.Fatal("Open of an unregistered extension must fail")
	}
}

// Fixture: registration with a locator that returns stale/non-ffmpeg
// binaries is refused (ffmpeg must answer -version).
func TestFFmpegAdapterRejectsBrokenBinary(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 7\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	status := registerFFmpegOptionalWith(func() (string, string, error) {
		return fake, filepath.Join(dir, "ffprobe"), nil
	})
	if status.Available {
		t.Fatal("a non-answering ffmpeg must not register the adapter")
	}
}

// Fixture: the SourceFormat contract of the adapter is the f32le wire
// format at the probed rate/channels.
func TestFFmpegAdapterSourceFormat(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	src := makeAACFixture(t, dir)
	d, err := NewFFmpegAdapterDecoder(src, "ffmpeg", "ffprobe")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()
	if _, err := d.Probe(); err != nil {
		t.Fatalf("probe: %v", err)
	}
	rate, ch, enc := d.SourceFormat()
	md, err := d.Probe()
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if enc != EncFloat32LE {
		t.Fatalf("encoding = %v, want EncFloat32LE (ffmpeg -f f32le wire)", enc)
	}
	if rate != md.SampleRate || ch != md.Channels {
		t.Fatalf("source format %d/%d disagrees with the probe %d/%d", rate, ch, md.SampleRate, md.Channels)
	}
}
