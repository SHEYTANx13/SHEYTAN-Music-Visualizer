package studio

// studio_sync_test.go — manual tempo override and sync offset
// (roadmap Phase 2 #3). Both corrections must reach the deterministic
// frame model (AudioStateAt) that the preview and the export consume.

import (
	"math"
	"path/filepath"
	"testing"
	"time"
)

// analyzeFixture imports + analyzes a fixed musical fixture and
// returns the studio ready for assertions.
func analyzeFixture(t *testing.T) *Studio {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "track.wav")
	writeTestWav(t, src, 6.0)
	s, err := New()
	if err != nil {
		t.Fatalf("studio: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.ImportFile(src); err != nil {
		t.Fatalf("import: %v", err)
	}
	waitFor(t, s, 60*time.Second, func() bool { return s.Track() != nil }, "import")
	waitFor(t, s, 60*time.Second, func() bool {
		return s.Analysis() != nil && s.DNA() != nil
	}, "analysis+DNA")
	return s
}

// The tempo override replaces the measured BPM for the beat phase and
// bypasses the confidence gate — an explicit user decision.
func TestTempoOverrideChangesBeatPhase(t *testing.T) {
	s := analyzeFixture(t)

	s.SetTempoOverride(100) // 600 ms beat
	ph := s.AudioStateAt(0.3).BeatPhase
	want := math.Mod(0.3, 0.6) / 0.6 // exactly 0.5
	if math.Abs(ph-want) > 1e-9 {
		t.Fatalf("override phase = %v, want %v (100 BPM at t=0.3)", ph, want)
	}
	if s.TempoOverride() != 100 {
		t.Fatalf("override getter = %v, want 100", s.TempoOverride())
	}

	// 0 restores the measured path.
	s.SetTempoOverride(0)
	if s.TempoOverride() != 0 {
		t.Fatalf("override reset = %v, want 0", s.TempoOverride())
	}
}

func TestTempoOverrideClamps(t *testing.T) {
	s := analyzeFixture(t)
	if got := s.SetTempoOverride(9999); got != 300 {
		t.Fatalf("override %v, want clamped 300", got)
	}
	if got := s.SetTempoOverride(1); got != 30 {
		t.Fatalf("override %v, want clamped 30", got)
	}
	s.SetTempoOverride(0)
}

// The sync offset shifts the analysis timeline: features at media time
// t are read at (t - offset). The AudioState.Time stays the media time.
func TestSyncOffsetShiftsFeatures(t *testing.T) {
	s := analyzeFixture(t)

	s.SetSyncOffset(0)
	before := s.AudioStateAt(1.0)

	s.SetSyncOffset(0.5)
	after := s.AudioStateAt(1.0)

	if after.Time != 1.0 {
		t.Fatalf("state time = %v, want the media time 1.0", after.Time)
	}
	// Features must equal the ones previously served at t-offset:
	// state@1.0 with +0.5 offset == state@0.5 without offset.
	s.SetSyncOffset(0)
	equivalent := s.AudioStateAt(0.5)
	if math.Abs(after.RMS-equivalent.RMS) > 1e-12 {
		t.Fatalf("offset RMS %.6f != shifted reference %.6f", after.RMS, equivalent.RMS)
	}
	for i := range after.Bins {
		if math.Abs(after.Bins[i]-equivalent.Bins[i]) > 1e-12 {
			t.Fatalf("offset bins[%d] %.6f != shifted reference %.6f", i, after.Bins[i], equivalent.Bins[i])
		}
	}
	// The shift must be visible on this dynamic (kick-pattern) fixture:
	// state@1.0 with offset differs from state@1.0 without.
	if math.Abs(after.RMS-before.RMS) < 1e-12 {
		for i := range after.Bins {
			if math.Abs(after.Bins[i]-before.Bins[i]) > 1e-12 {
				return // a visible feature shift exists
			}
		}
		t.Fatal("offset produced no visible feature shift on a dynamic fixture")
	}
}

func TestSyncOffsetClamps(t *testing.T) {
	s := analyzeFixture(t)
	s.SetSyncOffset(50)
	if got := s.SyncOffset(); got != 5 {
		t.Fatalf("offset %v, want clamped 5", got)
	}
	s.SetSyncOffset(-50)
	if got := s.SyncOffset(); got != -5 {
		t.Fatalf("offset %v, want clamped -5", got)
	}
	s.SetSyncOffset(0)
}

// Project round trip carries the corrections.
func TestTempoOffsetPersistedInProject(t *testing.T) {
	s := analyzeFixture(t)
	if s.Track() == nil {
		t.Fatal("no track")
	}
	s.SetTempoOverride(105)
	s.SetSyncOffset(0.25)

	path := filepath.Join(t.TempDir(), "p.sheytan.json")
	if err := s.SaveProject(path); err != nil {
		t.Fatalf("save: %v", err)
	}
	s.SetTempoOverride(0)
	s.SetSyncOffset(0)
	if err := s.LoadProject(path); err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := s.TempoOverride(); got != 105 {
		t.Fatalf("restored tempo override = %v, want 105", got)
	}
	if got := s.SyncOffset(); got != 0.25 {
		t.Fatalf("restored sync offset = %v, want 0.25", got)
	}
}
