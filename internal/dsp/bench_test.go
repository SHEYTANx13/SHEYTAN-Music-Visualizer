package dsp

// bench_test.go — performance benchmarks for the analysis core
// (roadmap Phase 2 #10: "Performance benchmarks: NOT DONE"). Run with:
//
//	go test -tags goffi_static -bench BenchmarkDSP -run ^$ ./internal/dsp/
//
// The reference machine for the packaged evidence is the Phase 2
// verification host (Debian 13, gcc 14.2, Go 1.27); numbers scale with
// hardware and mean nothing without the host record.

import (
	"math"
	"testing"
)

// benchStereo builds interleaved stereo white-noise-ish content.
func benchStereo(seconds float64) []float32 {
	n := int(SampleRate * seconds)
	out := make([]float32, n*2)
	for i := 0; i < n; i++ {
		v := float32(0.4 * math.Sin(2*math.Pi*220*float64(i)/SampleRate))
		out[2*i], out[2*i+1] = v, v
	}
	return out
}

// BenchmarkDSPAnalyzeOneMinute reports the cost of analyzing one
// minute of canonical stereo audio, split into natural stream chunks.
func BenchmarkDSPAnalyzeOneMinute(b *testing.B) {
	chunk := benchStereo(1.0) // 1 s chunks, like the streaming importer
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		a := NewAnalyzer()
		b.StartTimer()
		for s := 0; s < 60; s++ {
			a.Process(chunk)
		}
		res := a.Finish()
		if len(res.Frames) < 5000 {
			b.Fatalf("unexpected frame count %d", len(res.Frames))
		}
	}
}

// BenchmarkDSPFrameCost isolates the per-hop STFT+features cost.
func BenchmarkDSPFrameCost(b *testing.B) {
	a := NewAnalyzer()
	stereo := []float32{0.3, 0.3}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// 512 interleaved samples = one hop of frames.
		for k := 0; k < HopSize; k++ {
			a.Process(stereo)
		}
	}
}
