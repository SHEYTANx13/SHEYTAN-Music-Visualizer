package audio

// decoders_integration_test.go — real-file decode proofs for the MP3,
// FLAC and Ogg decoders. Fixtures are tiny synthesized files generated
// by the local FFmpeg binary at test time (nothing copyrighted, nothing
// committed). When FFmpeg is absent the test skips with an explicit
// NOT AVAILABLE message — it never fakes a PASS.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func ffmpegAvailable() bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
}

// genFixture renders `seconds` of a 440 Hz sine in the given container.
func genFixture(t *testing.T, dir, container string, seconds float64) string {
	t.Helper()
	out := filepath.Join(dir, "tone."+container)
	args := []string{
		"-y", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2",
	}
	switch container {
	case "mp3":
		args = append(args, "-c:a", "libmp3lame", "-b:a", "128k")
	case "flac":
		args = append(args, "-c:a", "flac")
	case "ogg":
		args = append(args, "-c:a", "libvorbis")
	default:
		t.Fatalf("unknown container %s", container)
	}
	args = append(args, out)
	cmd := exec.Command("ffmpeg", args...)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("SKIP (NOT AVAILABLE): ffmpeg could not generate %s fixture: %v: %s",
			container, err, string(b[:min(len(b), 200)]))
	}
	return out
}

func TestRealFileDecoders(t *testing.T) {
	if !ffmpegAvailable() {
		t.Skip("SKIP (NOT AVAILABLE): ffmpeg not installed; MP3/FLAC/Ogg fixtures cannot be generated")
	}
	RegisterAll()
	cases := []struct {
		container string
		wantRate  int
	}{
		{"mp3", 44100},
		{"flac", 44100},
		{"ogg", 44100},
	}
	for _, c := range cases {
		t.Run(c.container, func(t *testing.T) {
			dir := t.TempDir()
			src := genFixture(t, dir, c.container, 2.0)
			cache, err := NewCache(filepath.Join(dir, "cache"))
			if err != nil {
				t.Fatal(err)
			}
			track, err := Import(context.Background(), src, cache, nil)
			if err != nil {
				t.Fatalf("import %s: %v", c.container, err)
			}
			if track.Frames == 0 {
				t.Fatalf("%s decoded to zero frames", c.container)
			}
			// 2 s ± decoder/duration-reporting slack
			if track.Duration < 1.8 || track.Duration > 2.2 {
				t.Fatalf("%s duration %.2f, want ~2.0", c.container, track.Duration)
			}
			// canonical cache size matches frames exactly
			fi, err := os.Stat(cache.RawPath(track.Fingerprint))
			if err != nil {
				t.Fatal(err)
			}
			if fi.Size() != int64(track.Frames)*8 {
				t.Fatalf("%s cache size %d != frames*8 (%d)", c.container, fi.Size(), track.Frames*8)
			}
			// sidecar provenance records the decoder
			sc, err := cache.Lookup(track.Fingerprint)
			if err != nil {
				t.Fatalf("%s sidecar: %v", c.container, err)
			}
			if sc.Container != c.container || sc.Decoder == "" {
				t.Fatalf("%s sidecar incomplete: %+v", c.container, sc)
			}
		})
	}
}
