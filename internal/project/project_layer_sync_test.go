package project

// project_layer_sync_test.go — round trips for the layered-composition
// mirror fields and the manual tempo/offset correction fields
// (roadmap Phase 2 #3 and #6).

import (
	"path/filepath"
	"testing"
)

func TestLayerAndSyncFieldsRoundTrip(t *testing.T) {
	p := NewDefault("song", "/media/song.wav", "fp-123", "wav", 120)
	p.Visualizer.Mode = "radial"
	p.Visualizer.BackgroundLayer = "waveform"
	p.Visualizer.BackgroundIntensity = 0.45
	p.TempoOverrideBPM = 96.5
	p.SyncOffsetSeconds = -0.35

	path := filepath.Join(t.TempDir(), "roundtrip.sheytan.json")
	if err := Save(p, path); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Visualizer.BackgroundLayer != "waveform" {
		t.Fatalf("background layer = %q, want waveform", got.Visualizer.BackgroundLayer)
	}
	if got.Visualizer.BackgroundIntensity < 0.449 || got.Visualizer.BackgroundIntensity > 0.451 {
		t.Fatalf("background intensity = %v, want 0.45", got.Visualizer.BackgroundIntensity)
	}
	if got.TempoOverrideBPM != 96.5 {
		t.Fatalf("tempo override = %v, want 96.5", got.TempoOverrideBPM)
	}
	if got.SyncOffsetSeconds != -0.35 {
		t.Fatalf("sync offset = %v, want -0.35", got.SyncOffsetSeconds)
	}
}

func TestLayerAndSyncFieldClamps(t *testing.T) {
	p := NewDefault("song", "/media/song.wav", "fp-123", "wav", 120)
	p.Visualizer.BackgroundLayer = "strobe-party" // unknown → none
	p.Visualizer.BackgroundIntensity = 9
	p.TempoOverrideBPM = 9999 // out of range → clamped into 30..300
	p.SyncOffsetSeconds = 60  // out of range → clamped to ±5
	if err := p.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if p.Visualizer.BackgroundLayer != "none" {
		t.Fatalf("unknown layer survived: %q", p.Visualizer.BackgroundLayer)
	}
	if p.Visualizer.BackgroundIntensity != 0 {
		t.Fatalf("intensity with layer=none must reset, got %v", p.Visualizer.BackgroundIntensity)
	}
	if p.TempoOverrideBPM != 300 {
		t.Fatalf("tempo override = %v, want clamped 300", p.TempoOverrideBPM)
	}
	if p.SyncOffsetSeconds != 5 {
		t.Fatalf("sync offset = %v, want clamped 5", p.SyncOffsetSeconds)
	}
}

func TestTempoOverrideZeroMeansMeasured(t *testing.T) {
	p := NewDefault("song", "/media/song.wav", "fp-123", "wav", 120)
	p.TempoOverrideBPM = 0
	if err := p.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if p.TempoOverrideBPM != 0 {
		t.Fatalf("0 override must stay 0 (measured), got %v", p.TempoOverrideBPM)
	}
}
