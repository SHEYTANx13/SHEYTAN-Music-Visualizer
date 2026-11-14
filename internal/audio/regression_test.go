package audio

// regression_43541087097_test.go — regression coverage for the audio
// decode defects fixed in run 43541087097:
//
//   - C1 chunk-boundary sample drops (frameAlignReader)
//   - C2 FLAC bit-depth mapping (fixed 24-bit conversion wire)
//   - C3 8-bit WAV unsigned DC offset
//   - M4 fmt-chunk allocation bomb (wavfloat)
//   - M5 compressed WAV format tags accepted as PCM
//   - L4 NaN/Inf float32 wire samples

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"

	flacpcm "github.com/tphakala/go-flac/pcm"
)

// oddChunkReader serves the source in chunk sizes that are NOT a
// multiple of the frame size — the condition that used to silently
// drop len(b)%%frame bytes at every boundary.
type oddChunkReader struct {
	data []byte
	off  int
	ch   int
}

func (r *oddChunkReader) Read(p []byte) (int, error) {
	if r.off >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.off:])
	if n > r.ch {
		n = r.ch
	}
	r.off += n
	return n, nil
}

// TestFrameAlignReaderNeverDropsSamples feeds a 3-channel float32
// stream (frame = 12 bytes) through 1000-byte chunks (1000 %% 12 != 0)
// and verifies every Read is frame-aligned and the concatenation is
// byte-identical to the source.
func TestFrameAlignReaderNeverDropsSamples(t *testing.T) {
	const frames = 5000
	const ch = 3
	src := make([]byte, frames*ch*4)
	for i := range src {
		src[i] = byte(i % 251)
	}
	a := &frameAlignReader{r: &oddChunkReader{data: src, ch: 1000}, frame: ch * 4}
	var got []byte
	buf := make([]byte, 4096)
	reads := 0
	for {
		n, err := a.Read(buf)
		if n%a.frame != 0 {
			t.Fatalf("read %d returned %d bytes — not a multiple of frame %d", reads, n, a.frame)
		}
		got = append(got, buf[:n]...)
		reads++
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if reads > frames*2 {
			t.Fatal("frameAlignReader is spinning")
		}
	}
	if len(got) != len(src) {
		t.Fatalf("aligned stream length = %d, want %d (no sample may be dropped)", len(got), len(src))
	}
	if !bytes.Equal(got, src) {
		t.Fatal("aligned stream content diverged from the source")
	}
}

// TestFrameAlignReaderSmallBufferErrors verifies the defensive guard
// for buffers smaller than one frame.
func TestFrameAlignReaderSmallBufferErrors(t *testing.T) {
	a := &frameAlignReader{r: bytes.NewReader([]byte{1, 2, 3}), frame: 4}
	if _, err := a.Read(make([]byte, 3)); err == nil {
		t.Fatal("a buffer smaller than one frame must be an explicit error, never a silent drop")
	}
}

// TestDecodeToCacheSurvivesUnalignedChunkBoundaries reproduces the
// original C1 defect through the real decode path: a 3-channel 48 kHz
// float32 stream read in 64 KiB chunks (65536 %% 12 = 4) used to lose 4
// bytes per boundary and misalign into garbage; the frame count now
// must be exact.
func TestDecodeToCacheSurvivesUnalignedChunkBoundaries(t *testing.T) {
	dir := t.TempDir()
	cache, err := NewCache(filepath.Join(dir, "cache"))
	if err != nil {
		t.Fatal(err)
	}
	const frames = 48000 // 1 s at 48 kHz
	src := make([]byte, frames*3*4)
	for i := 0; i < frames*3; i++ {
		v := float32(0.3 * math.Sin(2*math.Pi*220*float64(i/3)/48000))
		binary.LittleEndian.PutUint32(src[i*4:], math.Float32bits(v))
	}
	// chunks of 64 KiB: not a multiple of the 12-byte frame — wrapped
	// in the same frame-aligning reader audio.Import installs.
	reader := &frameAlignReader{r: &oddChunkReader{data: src, ch: 64 << 10}, frame: 3 * 4}
	md := Metadata{Container: "test", SampleRate: 48000, Channels: 3}
	track, err := decodeToCache(context.Background(), cache, "regtest-unaligned-0001", "unaligned.wav", md, 48000, 3, EncFloat32LE, reader, nil)
	if err != nil {
		t.Fatalf("decodeToCache: %v", err)
	}
	// 3 ch downmixed to stereo 1:1 (mean of 3), no resample: frames must match exactly.
	if track.Frames != frames {
		t.Fatalf("canonical frames = %d, want exactly %d — samples were dropped at chunk boundaries", track.Frames, frames)
	}
}

// TestWAV8BitSilenceHasNoDCOffset pins the C3 repair: 8-bit WAV
// silence (0x80) must decode to ~0, not a +1.0 full-scale DC signal.
func TestWAV8BitSilenceHasNoDCOffset(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "silence8.wav")
	const n = 4800 // 0.1 s at 48 kHz, mono 8-bit
	data := make([]byte, 44+n)
	copy(data[0:4], "RIFF")
	binary.LittleEndian.PutUint32(data[4:8], uint32(36+n))
	copy(data[8:12], "WAVE")
	copy(data[12:16], "fmt ")
	binary.LittleEndian.PutUint32(data[16:20], 16)
	binary.LittleEndian.PutUint16(data[20:22], 1) // PCM
	binary.LittleEndian.PutUint16(data[22:24], 1) // mono
	binary.LittleEndian.PutUint32(data[24:28], 48000)
	binary.LittleEndian.PutUint32(data[28:32], 48000)
	binary.LittleEndian.PutUint16(data[32:34], 1)
	binary.LittleEndian.PutUint16(data[34:36], 8)
	copy(data[36:40], "data")
	binary.LittleEndian.PutUint32(data[40:44], uint32(n))
	for i := 0; i < n; i++ {
		data[44+i] = 0x80 // digital silence
	}
	if err := os.WriteFile(src, data, 0o644); err != nil {
		t.Fatal(err)
	}
	cache, err := NewCache(filepath.Join(dir, "cache"))
	if err != nil {
		t.Fatal(err)
	}
	track, err := Import(context.Background(), src, cache, nil)
	if err != nil {
		t.Fatalf("import 8-bit wav: %v", err)
	}
	f, err := cache.Reader(track.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	raw, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	var sum float64
	samples := len(raw) / 4
	sbuf := make([]float32, samples)
	if m := DecodeFloat32LE(raw, sbuf); m != samples {
		t.Fatalf("decoded %d samples, want %d", m, samples)
	}
	for _, v := range sbuf {
		sum += float64(v)
	}
	mean := sum / float64(samples)
	if math.Abs(mean) > 0.01 {
		t.Fatalf("8-bit silence decoded to mean %.4f — the unsigned DC offset is back", mean)
	}
	for _, v := range sbuf {
		if math.Abs(float64(v)) > 0.01 {
			t.Fatalf("8-bit silence sample %v is not silence", v)
		}
	}
}

// TestWAVCompressedFormatTagRejected pins the M5 repair: a WAV whose
// format tag is a compressed codec (IMA ADPCM here) must be rejected
// with ErrUnsupported instead of decoding integer noise as "PCM".
func TestWAVCompressedFormatTagRejected(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "adpcm.wav")
	data := make([]byte, 44+16)
	copy(data[0:4], "RIFF")
	binary.LittleEndian.PutUint32(data[4:8], 36+16)
	copy(data[8:12], "WAVE")
	copy(data[12:16], "fmt ")
	binary.LittleEndian.PutUint32(data[16:20], 16)
	binary.LittleEndian.PutUint16(data[20:22], 0x0011) // IMA ADPCM
	binary.LittleEndian.PutUint16(data[22:24], 2)
	binary.LittleEndian.PutUint32(data[24:28], 44100)
	binary.LittleEndian.PutUint32(data[28:32], 44100*4)
	binary.LittleEndian.PutUint16(data[32:34], 4)
	binary.LittleEndian.PutUint16(data[34:36], 4)
	copy(data[36:40], "data")
	binary.LittleEndian.PutUint32(data[40:44], 16)
	if err := os.WriteFile(src, data, 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, oerr := Open(src)
	if oerr == nil {
		t.Fatal("compressed WAV must be rejected, not decoded as PCM noise")
	}
}

// TestWAVFloatFmtChunkBombRejected pins the M4 repair: a tiny file
// claiming a 4 GiB fmt chunk must fail fast, never allocate.
func TestWAVFloatFmtChunkBombRejected(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "bomb.wav")
	data := make([]byte, 60)
	copy(data[0:4], "RIFF")
	binary.LittleEndian.PutUint32(data[4:8], 52)
	copy(data[8:12], "WAVE")
	copy(data[12:16], "fmt ")
	binary.LittleEndian.PutUint32(data[16:20], 0xFFFFFFFF) // hostile fmt size
	copy(data[20:24], "\x03\x00\x02\x00")
	if err := os.WriteFile(src, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewWAVFloatDecoder(src); err == nil {
		t.Fatal("a 60-byte file claiming a 4 GiB fmt chunk must be rejected")
	}
}

// TestFloat32WireClampsNaNAndInf pins the L4 repair: non-finite
// float32 wire samples become 0 and out-of-range samples clamp to ±1.
func TestFloat32WireClampsNaNAndInf(t *testing.T) {
	in := make([]byte, 5*4)
	put := func(i int, v float32) { binary.LittleEndian.PutUint32(in[i*4:], math.Float32bits(v)) }
	put(0, float32(math.NaN()))
	put(1, float32(math.Inf(1)))
	put(2, float32(math.Inf(-1)))
	put(3, 2.0)
	put(4, -2.0)
	out := make([]float32, 5)
	if n := bytesLEToFloat(in, out); n != 5 {
		t.Fatalf("decoded %d samples, want 5", n)
	}
	if !math.IsNaN(float64(out[0])) == false { // out[0] must NOT be NaN
		t.Fatalf("NaN must map to 0, got %v", out[0])
	}
	if out[0] != 0 {
		t.Fatalf("NaN must map to 0, got %v", out[0])
	}
	if out[1] != 0 || out[2] != 0 {
		t.Fatalf("±Inf must map to 0, got %v %v", out[1], out[2])
	}
	if out[3] != 1 || out[4] != -1 {
		t.Fatalf("out-of-range samples must clamp to ±1, got %v %v", out[3], out[4])
	}
}

// TestFLAC8BitDecodesThrough24BitWire pins the C2 repair: an 8-bit FLAC
// (legal FLAC; the pcm library emits 1 byte/sample natively) must
// decode through the fixed 24-bit conversion wire with correct
// amplitude — the old mapping read it as int16 and produced pure noise
// at the wrong duration.
func TestFLAC8BitDecodesThrough24BitWire(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "tone8.flac")
	const n = 4800 // 0.1 s at 48 kHz, mono 8-bit
	var buf bytes.Buffer
	enc, err := flacpcm.NewEncoder(&buf, flacpcm.Config{
		SampleRate: 48000, BitDepth: 8, Channels: 1, TotalSamples: n,
	})
	if err != nil {
		t.Fatalf("flac encode init: %v", err)
	}
	pcm := make([]byte, n)
	for i := range pcm {
		if (i/24)%2 == 0 { // slow square wave between ~+1 and -1
			pcm[i] = 0x7F // +127 (FLAC 8-bit is SIGNED two's complement)
		} else {
			pcm[i] = 0x80 // -128
		}
	}
	if _, err := enc.Write(pcm); err != nil {
		t.Fatalf("flac encode write: %v", err)
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("flac encode close: %v", err)
	}
	if err := os.WriteFile(src, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	cache, err := NewCache(filepath.Join(dir, "cache"))
	if err != nil {
		t.Fatal(err)
	}
	track, err := Import(context.Background(), src, cache, nil)
	if err != nil {
		t.Fatalf("import 8-bit flac: %v", err)
	}
	if track.Frames != n {
		t.Fatalf("frames = %d, want exactly %d (8-bit FLAC used to decode at the wrong duration)", track.Frames, n)
	}
	f, err := cache.Reader(track.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	raw, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	samples := len(raw) / 4
	sbuf := make([]float32, samples)
	if m := DecodeFloat32LE(raw, sbuf); m != samples {
		t.Fatalf("decoded %d samples, want %d", m, samples)
	}
	var pos, neg, zero int
	for _, v := range sbuf {
		switch {
		case v > 0.5:
			pos++
		case v < -0.5:
			neg++
		default:
			zero++ // transitions
		}
	}
	if pos < n/4 || neg < n/4 {
		t.Fatalf("square wave lost amplitude: pos=%d neg=%d of %d samples (8-bit FLAC is decoding as noise)", pos, neg, samples)
	}
	// The 24-bit wire must keep full scale: +127/128 ≈ 0.992.
	var peak float32
	for _, v := range sbuf {
		if v > peak {
			peak = v
		}
	}
	if peak < 0.9 {
		t.Fatalf("peak amplitude %.3f — the 8-bit→24-bit conversion lost scale", peak)
	}
}
