package export

// regression_43541087097_test.go — regression coverage for the export
// environment defects fixed in run 43541087097:
//
//   - H-1: DetectEnv cached FAILED detections; the next call returned
//     an empty environment with a NIL error, silently skipping the
//     "Export requires FFmpeg" dialog and failing late inside NewJob.
//   - M-1: the cached *Environment pointer was shared, so concurrent
//     proofing/resolution raced its map and slices.

import (
	"sync"
	"testing"
)

// TestDetectEnvFailureIsNeverCached points the fflocate override at a
// directory without ffmpeg: every DetectEnv call must keep reporting
// the honest error (never a fake empty success), and once the override
// is cleared the cache must still be empty so detection re-probes.
func TestDetectEnvFailureIsNeverCached(t *testing.T) {
	SetPathOverride(t.TempDir()) // exists but has no ffmpeg/ffprobe
	envMu.Lock()
	cachedEnv = nil
	envMu.Unlock()
	t.Cleanup(func() { SetPathOverride("") })

	for i := 0; i < 3; i++ {
		env, err := DetectEnv(false)
		if err == nil {
			t.Fatalf("call %d: DetectEnv must keep failing honestly while FFmpeg is absent (got env %+v)", i, env)
		}
		if env == nil {
			t.Fatalf("call %d: a diagnostic (possibly empty) environment must accompany the error", i)
		}
	}
	envMu.Lock()
	cached := cachedEnv
	envMu.Unlock()
	if cached != nil {
		t.Fatal("a failed detection must never populate the process cache")
	}
}

// TestEnvironmentCloneIsolatesCallers verifies the deep copy: mutating
// a returned Environment (proofing appends, encoder map writes) can
// never corrupt the process cache or another caller's copy.
func TestEnvironmentCloneIsolatesCallers(t *testing.T) {
	base := &Environment{
		FFmpegPath:       "/x/ffmpeg",
		FFprobePath:      "/x/ffprobe",
		Version:          "test",
		Encoders:         map[string]bool{"libx264": true, "aac": true},
		HardwareEncoders: []string{"h264_nvenc"},
	}
	a := base.clone()
	b := base.clone()
	a.Encoders["libx264"] = false
	a.Encoders["bogus"] = true
	a.HardwareEncoders = append(a.HardwareEncoders, "h264_qsv")
	if b.Encoders["libx264"] != true || b.Encoders["bogus"] != false {
		t.Fatal("encoder-map mutation leaked into another copy")
	}
	if len(b.HardwareEncoders) != 1 || b.HardwareEncoders[0] != "h264_nvenc" {
		t.Fatalf("hardware-encoder mutation leaked into another copy: %v", b.HardwareEncoders)
	}
	if base.Encoders["libx264"] != true || len(base.HardwareEncoders) != 1 {
		t.Fatal("mutation leaked into the source environment")
	}
}

// TestDetectEnvConcurrentCallersNeverRace exercises the shared-cache
// discipline under concurrency (run with -race in CI).
func TestDetectEnvConcurrentCallersNeverRace(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			env, err := DetectEnv(false)
			if err == nil && env == nil {
				t.Error("success must carry an environment")
			}
			if env != nil {
				ProofHardwareEncoders(env) // mutates only its own copy
				if _, rerr := ResolveEncoder(env, "auto"); rerr == nil && env.FFmpegPath == "" {
					t.Error("resolved an encoder without ffmpeg")
				}
			}
		}()
	}
	wg.Wait()
}
