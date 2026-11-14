package render

// cpu_bench_test.go — CPU renderer frame-cost benchmarks (roadmap
// Phase 2 #10). Run with:
//
//      go test -tags goffi_static -bench BenchmarkCPURender -run ^$ ./internal/render/
//
// Resolution tiers mirror the export presets: 1080p30 is the default
// export target; 4K is the ceiling.

import (
	"testing"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/visualizer"
)

func benchInput(w, h int, mode visualizer.Mode) visualizer.FrameInput {
	return visualizer.FrameInput{
		FrameIndex: 30,
		Time:       1.0,
		Width:      w,
		Height:     h,
		Config: visualizer.Config{
			Mode: mode, Sensitivity: 1.2, Smoothing: 0.2,
			Glow: 0.7, Palette: 0, ParticleDensity: 0.6,
			PulseResponse: 1.2, ShowTitle: false, ShowWatermark: false,
		},
		Audio: visualizer.AudioState{
			Bass: 0.6, Mid: 0.4, Treble: 0.2,
			Bins:       testBins(),
			WaveWindow: []float32{0.1, 0.5, 0.2, 0.8, 0.3, 0.6, 0.4, 0.9},
		},
	}
}

func benchmarkCPUFrame(b *testing.B, w, h int, mode visualizer.Mode) {
	r := NewCPURenderer()
	defer r.Close()
	in := benchInput(w, h, mode)
	// warm-up (buffer allocation outside the timer)
	if _, err := r.RenderFrame(in); err != nil {
		b.Fatalf("render: %v", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		in.FrameIndex++
		in.Time += 1.0 / 30.0
		if _, err := r.RenderFrame(in); err != nil {
			b.Fatalf("render: %v", err)
		}
	}
}

func BenchmarkCPURender1080pBars(b *testing.B) {
	benchmarkCPUFrame(b, 1920, 1080, visualizer.ModeBars)
}

func BenchmarkCPURender1080pRadial(b *testing.B) {
	benchmarkCPUFrame(b, 1920, 1080, visualizer.ModeRadial)
}

func BenchmarkCPURender4KWaveform(b *testing.B) {
	benchmarkCPUFrame(b, 3840, 2160, visualizer.ModeWaveform)
}

func BenchmarkCPURender1080pBarsWithLayer(b *testing.B) {
	r := NewCPURenderer()
	defer r.Close()
	in := benchInput(1920, 1080, visualizer.ModeBars)
	in.Config.BackgroundLayer = visualizer.ModeWaveform
	in.Config.BackgroundIntensity = 0.35
	if _, err := r.RenderFrame(in); err != nil {
		b.Fatalf("render: %v", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		in.FrameIndex++
		in.Time += 1.0 / 30.0
		if _, err := r.RenderFrame(in); err != nil {
			b.Fatalf("render: %v", err)
		}
	}
}

// BenchmarkCPURender1080pBarsWithOverlays measures the frame cost with
// the full overlay text path active (title + subtitle + watermark) —
// the preview default. Run-43541087097 A/B evidence for the face cache:
// the baseline created and closed three opentype faces per frame.
func BenchmarkCPURender1080pBarsWithOverlays(b *testing.B) {
	r := NewCPURenderer()
	defer r.Close()
	in := benchInput(1920, 1080, visualizer.ModeBars)
	in.Config.ShowTitle = true
	in.Config.TitleText = "SHEYTAN overlay benchmark title"
	in.Config.SubtitleText = "subtitle line"
	in.Config.ShowWatermark = true
	if _, err := r.RenderFrame(in); err != nil {
		b.Fatalf("render: %v", err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		in.FrameIndex++
		in.Time += 1.0 / 30.0
		if _, err := r.RenderFrame(in); err != nil {
			b.Fatalf("render: %v", err)
		}
	}
}
