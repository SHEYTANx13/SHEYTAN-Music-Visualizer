package dsp

// analysis_bench_test.go — §6.12 benchmarks over representative
// durations. They measure analysis wall time per minute of audio,
// allocations and memory via testing.B; p50/p95 style breakdowns are
// approximated by the per-op numbers (single deterministic run each).

import (
	"context"
	"fmt"
	"testing"
)

func benchTrack(t *testing.B, seconds float64) StereoTrack {
	t.Helper()
	tr := newTrack(nSamples(seconds))
	// Representative musical content: kick grid + hats + bass + pad.
	ClickTrain(&tr, 0, seconds, 120, 0.55, 1.4, 4)
	SustainedBass(&tr, 0, seconds, 0.25)
	for k := 0; k < int(seconds*8); k++ {
		HatLike(&tr, float64(k)*0.125+0.0625, 0.3)
	}
	PadLike(&tr, 0, seconds, 0.15)
	return tr
}

func benchAnalyze(t *testing.B, seconds float64) {
	t.Helper()
	tr := benchTrack(t, seconds)
	inter := tr.Interleaved()
	in := &chunkedInput{data: inter, size: 48000}
	t.ResetTimer()
	a, err := AnalyzeFull(context.Background(), in, "bench", "bench")
	if err != nil {
		t.Fatal(err)
	}
	t.StopTimer()
	if a.FrameCount == 0 {
		t.Fatal("no frames")
	}
}

func BenchmarkAnalyze5Minutes(t *testing.B)  { benchAnalyze(t, 300) }
func BenchmarkAnalyze30Minutes(t *testing.B) { benchAnalyze(t, 1800) }
func BenchmarkAnalyze60Minutes(t *testing.B) { benchAnalyze(t, 3600) }

// BenchmarkAnalyzeMemoryProfile reports the alloc profile of a 5-minute
// analysis for the bounded-memory contract.
func BenchmarkAnalyzeMemoryProfile(t *testing.B) {
	tr := benchTrack(t, 300)
	inter := tr.Interleaved()
	in := &chunkedInput{data: inter, size: 48000}
	t.ReportAllocs()
	t.ResetTimer()
	a, err := AnalyzeFull(context.Background(), in, "bench", "bench")
	if err != nil {
		t.Fatal(err)
	}
	t.StopTimer()
	// Document the retained footprint for the bounded-memory note.
	_ = fmt.Sprintf("frames=%d waveform=%d chroma=%d", a.FrameCount, len(a.Legacy.Waveform), len(a.Harmony.Chroma))
}
