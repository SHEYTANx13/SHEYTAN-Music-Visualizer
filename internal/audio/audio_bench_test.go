package audio

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// benchWavFixture writes a deterministic 16-bit stereo 48 kHz WAV of
// n seconds (a counted ramp, no math import needed) and returns its
// path. Small on purpose: the benchmark measures the import path
// (decode + convert + resample), not disk throughput.
func benchWavFixture(b *testing.B, seconds int) string {
	b.Helper()
	const rate = 48000
	frames := seconds * rate
	pcm := make([]byte, frames*2*2) // stereo, 16-bit
	v := int16(0)
	for i := 0; i < frames; i++ {
		v = int16((i*37)%3276 - 1638) // deterministic non-silent ramp
		binary.LittleEndian.PutUint16(pcm[i*4:], uint16(v))
		binary.LittleEndian.PutUint16(pcm[i*4+2:], uint16(-v))
	}
	var buf bytes.Buffer
	// canonical 44-byte WAV header
	buf.WriteString("RIFF")
	chunk := uint32(36 + len(pcm))
	binary.Write(&buf, binary.LittleEndian, chunk)
	buf.WriteString("WAVEfmt ")
	binary.Write(&buf, binary.LittleEndian, uint32(16))
	binary.Write(&buf, binary.LittleEndian, uint16(1))      // PCM
	binary.Write(&buf, binary.LittleEndian, uint16(2))      // stereo
	binary.Write(&buf, binary.LittleEndian, uint32(rate))   // sample rate
	binary.Write(&buf, binary.LittleEndian, uint32(rate*4)) // byte rate
	binary.Write(&buf, binary.LittleEndian, uint16(4))      // block align
	binary.Write(&buf, binary.LittleEndian, uint16(16))     // bits
	buf.WriteString("data")
	binary.Write(&buf, binary.LittleEndian, uint32(len(pcm)))
	buf.Write(pcm)

	dir := b.TempDir()
	p := filepath.Join(dir, "bench-fixture.wav")
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		b.Fatalf("fixture write: %v", err)
	}
	return p
}

// BenchmarkAudioImportWav5s measures the full import path (fingerprint,
// decode, conversion, canonical cache write) on a deterministic 5 s
// stereo 48 kHz WAV — the per-track cost users wait on during import.
func BenchmarkAudioImportWav5s(b *testing.B) {
	RegisterAll()
	p := benchWavFixture(b, 5)
	dir := b.TempDir()
	cache, err := NewCache(dir)
	if err != nil {
		b.Fatalf("cache: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		tr, err := Import(ctx, p, cache, Progress(func(done, total uint64) {}))
		cancel()
		if err != nil {
			b.Fatalf("import: %v", err)
		}
		if tr.Duration <= 0 {
			b.Fatalf("imported track has no duration")
		}
	}
}

// BenchmarkResample1s48to44 measures the resampler on 1 s of stereo
// 48 kHz audio downsampled to 44.1 kHz — the per-second cost during
// import of mismatched-rate material.
func BenchmarkResample1s48to44(b *testing.B) {
	const inFrames = 48000
	in := make([]float32, inFrames*2)
	for i := range in {
		in[i] = float32(i%480) / 480.0
	}
	rs, err := NewResampler(48000, 44100, 2)
	if err != nil {
		b.Fatalf("resampler: %v", err)
	}
	out := make([]float32, rs.EstimatedOutputFrames(inFrames)*2)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rs.Reset()
		got := rs.Process(in, out)
		if got <= 0 {
			b.Fatalf("empty resample output")
		}
	}
}

var _ = io.Discard
