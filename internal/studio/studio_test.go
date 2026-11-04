package studio

// studio_test.go — integration: import a generated WAV → canonical
// cache → analysis → Song DNA → preview frame → project save/load
// round trip → deterministic preview across reloads. Uses a temp data
// dir via SHEYTAN_DATA_DIR so the user config dir is never touched.

import (
	"encoding/binary"
	"image"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/export"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/render"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/visualizer"
)

func init() {
	os.Setenv("SHEYTAN_DATA_DIR", tDir())
}

func tDir() string {
	d, _ := os.MkdirTemp("", "sheytan-studio-test-*")
	return d
}

func writeTestWav(t *testing.T, path string, seconds float64) {
	t.Helper()
	rate := 48000
	frames := int(seconds * float64(rate))
	data := make([]byte, 44+frames*4)
	copy(data[0:4], "RIFF")
	binary.LittleEndian.PutUint32(data[4:8], uint32(36+frames*4))
	copy(data[8:12], "WAVE")
	copy(data[12:16], "fmt ")
	binary.LittleEndian.PutUint32(data[16:20], 16)
	binary.LittleEndian.PutUint16(data[20:22], 1)
	binary.LittleEndian.PutUint16(data[22:24], 1) // mono
	binary.LittleEndian.PutUint32(data[24:28], uint32(rate))
	binary.LittleEndian.PutUint32(data[28:32], uint32(rate*2))
	binary.LittleEndian.PutUint16(data[32:34], 2)
	binary.LittleEndian.PutUint16(data[34:36], 16)
	copy(data[36:40], "data")
	binary.LittleEndian.PutUint32(data[40:44], uint32(frames*2))
	for i := 0; i < frames; i++ {
		// musical-ish: 120 BPM kick pattern + tone
		tSec := float64(i) / float64(rate)
		phase := math.Mod(tSec, 0.5)
		amp := math.Max(0, 1-phase*8)
		v := int16(9000 * amp * math.Sin(2*math.Pi*220*tSec))
		binary.LittleEndian.PutUint16(data[44+i*2:], uint16(v))
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, s *Studio, timeout time.Duration, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

// drainEvents empties the event channel (test logging).
func drainEvents(events chan Event) {
	for {
		select {
		case ev := <-events:
			_ = ev
		default:
			return
		}
	}
}

func TestStudioEndToEnd(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "track.wav")
	writeTestWav(t, src, 6.0)

	s, err := New()
	if err != nil {
		t.Fatalf("studio: %v", err)
	}
	defer s.Close()

	events := make(chan Event, 4096)
	done := make(chan struct{})
	s.Subscribe(func(ev Event) {
		select {
		case events <- ev:
		case <-done:
		default: // never block the producer on slow test consumers
		}
	})

	if err := s.ImportFile(src); err != nil {
		t.Fatalf("import: %v", err)
	}
	waitFor(t, s, 60*time.Second, func() bool { return s.Track() != nil }, "import")
	drainEvents(events)
	waitFor(t, s, 60*time.Second, func() bool {
		return s.Analysis() != nil && s.DNA() != nil
	}, "analysis+DNA")
	drainEvents(events)

	track := s.Track()
	if track.Duration < 5.9 || track.Duration > 6.1 {
		t.Fatalf("duration %.2f, want ~6.0", track.Duration)
	}
	if track.Frames != uint64(6*48000) {
		t.Fatalf("frames %d (mono→stereo pipeline changed?)", track.Frames)
	}
	an := s.Analysis()
	if an.BassEnergy < 0.2 {
		// (the pure-band mapping is verified precisely in dsp tests
		// with single-tone fixtures; this integration fixture mixes a
		// 220 Hz tone with a kick envelope)
		t.Fatalf("mixed fixture bass energy %.2f too low", an.BassEnergy)
	}
	dna := s.DNA()
	if dna.Fingerprint != track.Fingerprint {
		t.Fatal("DNA fingerprint mismatch")
	}

	// deterministic preview: same position twice → same image
	r := render.NewCPURenderer()
	defer r.Close()
	if err := s.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := s.Seek(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	f1, err := s.PreviewFrame(160, 90, r)
	if err != nil || f1 == nil {
		t.Fatalf("preview: %v", err)
	}
	f2, err := s.PreviewFrame(160, 90, r)
	if err != nil || f2 == nil {
		t.Fatalf("preview2: %v", err)
	}
	if !samePixels(f1.Image, f2.Image) {
		t.Fatal("preview not deterministic at the same position")
	}

	// project round trip
	proj := filepath.Join(dir, "test.sheytan.json")
	if err := s.SaveProject(proj); err != nil {
		t.Fatalf("save project: %v", err)
	}
	cfgBefore := s.Config()
	s.SetConfig(visualizer.Config{Mode: visualizer.ModeParticles, Glow: 1.5, Palette: 3})
	if err := s.LoadProject(proj); err != nil {
		t.Fatalf("load project: %v", err)
	}
	waitFor(t, s, 60*time.Second, func() bool {
		return s.Analysis() != nil && s.DNA() != nil
	}, "reload analysis")
	cfgAfter := s.Config()
	if cfgAfter.Mode != cfgBefore.Mode {
		t.Fatalf("project round trip: mode %s != %s", cfgAfter.Mode, cfgBefore.Mode)
	}
	if cfgAfter.Glow != cfgBefore.Glow {
		t.Fatalf("glow %.2f != %.2f", cfgAfter.Glow, cfgBefore.Glow)
	}

	// export through the service (short 480p to keep CI cheap)
	out := filepath.Join(dir, "out.mp4")
	if err := s.StartExport(exportRequestFor(out)); err != nil {
		t.Logf("export through service unavailable (ffmpeg missing?): %v", err)
	} else {
		waitFor(t, s, 180*time.Second, func() bool {
			j := s.ExportJob()
			return j != nil && (j.Result() != nil || j.Err() != nil)
		}, "export job")
		j := s.ExportJob()
		if j == nil {
			t.Fatal("no export job")
		}
		if j.Err() != nil {
			t.Fatalf("service export failed: %v", j.Err())
		}
		res := j.Result()
		if res == nil {
			t.Fatalf("no result; job err=%v", j.Err())
		}
		if !res.Validation.OK {
			t.Fatalf("validation problems: %v", res.Validation.Problems)
		}
		t.Logf("service export: %s validated, %d bytes", res.OutputPath, res.FileSize)
	}
	close(done)
}

func exportRequestFor(out string) export.Request {
	return export.Request{
		OutputPath: out, Width: 854, Height: 480, FPS: 30,
		QualityPreset: "high", Encoder: "auto", PreferGPU: false,
	}
}

func samePixels(a, b *image.RGBA) bool {
	if a.Bounds() != b.Bounds() {
		return false
	}
	for i := range a.Pix {
		if a.Pix[i] != b.Pix[i] {
			return false
		}
	}
	return true
}
