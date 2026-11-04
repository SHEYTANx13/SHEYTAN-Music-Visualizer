package audio

import (
	"context"
	"encoding/binary"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// writeSineWavInt16 writes a canonical test WAV (int16 PCM).
func writeSineWavInt16(t *testing.T, path string, rate, seconds int, freq float64) {
	t.Helper()
	n := rate * seconds
	data := make([]byte, 44+n*4)
	copy(data[0:4], "RIFF")
	binary.LittleEndian.PutUint32(data[4:8], uint32(36+n*4))
	copy(data[8:12], "WAVE")
	copy(data[12:16], "fmt ")
	binary.LittleEndian.PutUint32(data[16:20], 16)
	binary.LittleEndian.PutUint16(data[20:22], 1) // PCM
	binary.LittleEndian.PutUint16(data[22:24], 2)
	binary.LittleEndian.PutUint32(data[24:28], uint32(rate))
	binary.LittleEndian.PutUint32(data[28:32], uint32(rate*4))
	binary.LittleEndian.PutUint16(data[32:34], 4)
	binary.LittleEndian.PutUint16(data[34:36], 16)
	copy(data[36:40], "data")
	binary.LittleEndian.PutUint32(data[40:44], uint32(n*4))
	for i := 0; i < n; i++ {
		v := int16(12000 * math.Sin(2*math.Pi*freq*float64(i)/float64(rate)))
		binary.LittleEndian.PutUint16(data[44+i*4:], uint16(v))
		binary.LittleEndian.PutUint16(data[44+i*4+2:], uint16(v))
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeSineWavFloat32 writes an IEEE-float32 WAVE file.
func writeSineWavFloat32(t *testing.T, path string, rate, seconds int, freq float64) {
	t.Helper()
	n := rate * seconds
	data := make([]byte, 44+n*8)
	copy(data[0:4], "RIFF")
	binary.LittleEndian.PutUint32(data[4:8], uint32(36+n*8))
	copy(data[8:12], "WAVE")
	copy(data[12:16], "fmt ")
	binary.LittleEndian.PutUint32(data[16:20], 16)
	binary.LittleEndian.PutUint16(data[20:22], 3) // IEEE float
	binary.LittleEndian.PutUint16(data[22:24], 2)
	binary.LittleEndian.PutUint32(data[24:28], uint32(rate))
	binary.LittleEndian.PutUint32(data[28:32], uint32(rate*8))
	binary.LittleEndian.PutUint16(data[32:34], 8)
	binary.LittleEndian.PutUint16(data[34:36], 32)
	copy(data[36:40], "data")
	binary.LittleEndian.PutUint32(data[40:44], uint32(n*8))
	for i := 0; i < n; i++ {
		v := float32(0.4 * math.Sin(2*math.Pi*freq*float64(i)/float64(rate)))
		bits := mathFloat32bits(v)
		binary.LittleEndian.PutUint32(data[44+i*8:], bits)
		binary.LittleEndian.PutUint32(data[44+i*8+4:], bits)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func mathFloat32bits(f float32) uint32 { return math.Float32bits(f) }

func TestMain(m *testing.M) {
	RegisterAll()
	os.Exit(m.Run())
}

func TestImportWAVInt16ToCanonical(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "sine44.wav")
	writeSineWavInt16(t, src, 44100, 2, 220) // needs resampling
	cache, err := NewCache(filepath.Join(dir, "cache"))
	if err != nil {
		t.Fatal(err)
	}
	track, err := Import(context.Background(), src, cache, nil)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if track.Frames == 0 {
		t.Fatal("zero frames")
	}
	want := uint64(2) * CanonicalSampleRate
	if track.Frames != want {
		t.Fatalf("frames = %d, want %d (exact integer resample math)", track.Frames, want)
	}
	if track.Duration != 2.0 {
		t.Fatalf("duration = %f, want 2.0", track.Duration)
	}
	// cache hit second time
	track2, err := Import(context.Background(), src, cache, nil)
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if track2.Fingerprint != track.Fingerprint || track2.Frames != track.Frames {
		t.Fatal("cache hit mismatch")
	}
	// WAV wrapper validates
	f, err := cache.OpenWAV(track.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	hdr := make([]byte, 44)
	if _, err := io.ReadFull(f, hdr); err != nil {
		t.Fatalf("read wav header: %v", err)
	}
	t.Logf("header: %q", string(hdr[0:44]))
	if string(hdr[0:4]) != "RIFF" || string(hdr[8:12]) != "WAVE" {
		t.Fatalf("canonical WAV wrapper invalid: %q", string(hdr[0:12]))
	}
}

func TestImportWAVFloat32(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "sine48f.wav")
	writeSineWavFloat32(t, src, 48000, 1, 440)
	cache, _ := NewCache(filepath.Join(dir, "cache"))
	track, err := Import(context.Background(), src, cache, nil)
	if err != nil {
		t.Fatalf("import float32 wav: %v", err)
	}
	if track.Frames != uint64(CanonicalSampleRate) {
		t.Fatalf("frames = %d, want %d", track.Frames, uint64(CanonicalSampleRate))
	}
	if track.Metadata.Codec != "PCM float32" {
		t.Fatalf("codec = %s", track.Metadata.Codec)
	}
}

func TestResamplerRoundTrip(t *testing.T) {
	// 44.1k → 48k: sine frequency must be preserved (SNR-ish check)
	r, err := NewResampler(44100, 48000, 2)
	if err != nil {
		t.Fatal(err)
	}
	inRate := 44100
	inFrames := inRate // 1 s
	in := make([]float32, inFrames*2)
	for i := 0; i < inFrames; i++ {
		v := float32(0.5 * math.Sin(2*math.Pi*1000*float64(i)/float64(inRate)))
		in[2*i], in[2*i+1] = v, v
	}
	outCap := r.EstimatedOutputFrames(inFrames) * 2
	out := make([]float32, outCap+48000)
	written := r.Process(in, out)
	written += r.Process(r.FlushFeed(), out[written:])
	if written == 0 {
		t.Fatal("resampler produced no output")
	}
	// dominant frequency of output must stay 1 kHz (zero-crossing count)
	zc := 0
	prev := out[0]
	for i := 1; i < written/2; i++ {
		cur := out[2*i]
		if (prev < 0 && cur >= 0) || (prev >= 0 && cur < 0) {
			zc++
		}
		prev = cur
	}
	// zero crossings ≈ 2 * cycles ≈ 2*1000*duration; duration≈1s → ~2000
	if zc < 1800 || zc > 2200 {
		t.Fatalf("output zero crossings %d, want ~2000 (1 kHz preserved)", zc)
	}
}

func TestDecodeCorruptFileFailsCleanly(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.wav")
	if err := os.WriteFile(bad, []byte("this is not a wav file at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := newWAVAny(bad); err == nil {
		t.Fatal("corrupt WAV should fail")
	}
	empty := filepath.Join(dir, "empty.wav")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := newWAVAny(empty); err == nil {
		t.Fatal("empty WAV should fail")
	}
}

func TestUnknownExtensionRejected(t *testing.T) {
	RegisterAll()
	if _, _, err := Open("/tmp/track.xyz"); err == nil {
		t.Fatal("unknown extension must be rejected with ErrUnsupported")
	}
}

func TestFingerprintStableAndContentSensitive(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.wav")
	b := filepath.Join(dir, "b.wav")
	writeSineWavInt16(t, a, 48000, 1, 220)
	writeSineWavInt16(t, b, 48000, 1, 440) // different content
	fa, err := fingerprintFile(a)
	if err != nil {
		t.Fatal(err)
	}
	fb, _ := fingerprintFile(b)
	fa2, _ := fingerprintFile(a)
	if fa != fa2 {
		t.Fatal("fingerprint not stable")
	}
	if fa == fb {
		t.Fatal("fingerprint not content-sensitive")
	}
}

func TestConversionHelpers(t *testing.T) {
	// int16 → float32 full scale
	in := []byte{0x00, 0x80, 0xFF, 0x7F} // -32768, 32767
	out := make([]float32, 2)
	int16ToFloat32(in, out)
	if out[0] != -1.0 {
		t.Fatalf("int16 -32768 → %f, want -1.0", out[0])
	}
	if out[1] < 0.999 || out[1] > 1.0 {
		t.Fatalf("int16 32767 → %f, want ~1.0", out[1])
	}
	// mono → stereo duplicates
	st := make([]float32, 4)
	n := downmix([]float32{0.5, -0.5}, 1, 2, st)
	if n != 4 || st[0] != 0.5 || st[1] != 0.5 || st[2] != -0.5 {
		t.Fatalf("mono→stereo downmix wrong: n=%d st=%v", n, st)
	}
}
