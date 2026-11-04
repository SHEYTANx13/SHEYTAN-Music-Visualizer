package playback

import (
	"encoding/binary"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeSine writes canonical float32 stereo PCM (48 kHz).
func writeSine(t *testing.T, path string, seconds float64) int64 {
	frames := int(SampleRate * seconds)
	data := make([]byte, frames*8)
	for i := 0; i < frames; i++ {
		v := float32(0.5 * math.Sin(2*math.Pi*440*float64(i)/SampleRate))
		bits := math.Float32bits(v)
		binary.LittleEndian.PutUint32(data[i*8:], bits)
		binary.LittleEndian.PutUint32(data[i*8+4:], bits)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return int64(len(data))
}

func TestSilentTransportClockAndSeek(t *testing.T) {
	dir := t.TempDir()
	pcm := filepath.Join(dir, "track.f32")
	writeSine(t, pcm, 2.0) // 2 s

	tr, err := NewTransport(NewSilentOutput(), pcm)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	if tr.TotalDuration() != 2*time.Second {
		t.Fatalf("total %v, want 2s", tr.TotalDuration())
	}
	clock := tr.Clock()
	if clock.DeviceReal {
		t.Fatal("silent output must not claim a real device")
	}
	if err := tr.Play(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	pos := tr.Position()
	if pos <= 100*time.Millisecond || pos > 1500*time.Millisecond {
		t.Fatalf("position after 300 ms = %v (must advance on the media clock)", pos)
	}
	// pause holds position
	if err := tr.Pause(); err != nil {
		t.Fatal(err)
	}
	p1 := tr.Position()
	time.Sleep(150 * time.Millisecond)
	p2 := tr.Position()
	if p1 != p2 {
		t.Fatalf("position advanced while paused: %v → %v", p1, p2)
	}
	// seek works
	if err := tr.Seek(1500 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if diff := tr.Position() - 1500*time.Millisecond; diff > 200*time.Millisecond || diff < -200*time.Millisecond {
		t.Fatalf("seek landed at %v, want ~1.5s", tr.Position())
	}
	// stop resets
	if err := tr.Stop(); err != nil {
		t.Fatal(err)
	}
	if tr.Position() != 0 {
		t.Fatalf("position after stop = %v, want 0", tr.Position())
	}
}

func TestTransportVolumeBounds(t *testing.T) {
	dir := t.TempDir()
	pcm := filepath.Join(dir, "t.f32")
	writeSine(t, pcm, 0.2)
	tr, err := NewTransport(NewSilentOutput(), pcm)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	tr.SetVolume(5)
	if tr.Volume() != 1 {
		t.Fatalf("volume %v, want clamped to 1", tr.Volume())
	}
	tr.SetVolume(-1)
	if tr.Volume() != 0 {
		t.Fatalf("volume %v, want clamped to 0", tr.Volume())
	}
}

func TestTransportMissingFileFails(t *testing.T) {
	if _, err := NewTransport(NewSilentOutput(), "/no/such/file.f32"); err == nil {
		t.Fatal("missing PCM must fail")
	}
}

func TestOtoOutputUnavailableIsHonest(t *testing.T) {
	// On a host without an audio device NewOtoOutput fails; the app
	// must fall back to the silent output (this test only asserts the
	// silent path works everywhere).
	out := NewSilentOutput()
	if out == nil {
		t.Fatal("silent output must always construct")
	}
	_ = out.Close()
	var _ io.Reader = nil
}
