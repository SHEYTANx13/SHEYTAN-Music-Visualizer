package project

import (
	"os"
	"path/filepath"
	"testing"
)

func validProject() *Project {
	p := NewDefault("Test Track", "/music/test.wav", "deadbeef01", "wav", 180.5)
	p.Name = "My Project"
	return p
}

func TestProjectRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proj.json")
	p := validProject()
	p.Visualizer.Mode = "radial"
	p.Visualizer.Glow = 1.2
	p.TimelineMarkers = []float64{12.5, 60}
	if err := Save(p, path); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Name != p.Name || loaded.MediaRef.Fingerprint != p.MediaRef.Fingerprint {
		t.Fatal("round trip mismatch")
	}
	if loaded.Visualizer.Mode != "radial" || loaded.Visualizer.Glow != 1.2 {
		t.Fatalf("visualizer settings mismatch: %+v", loaded.Visualizer)
	}
	if len(loaded.TimelineMarkers) != 2 || loaded.TimelineMarkers[0] != 12.5 {
		t.Fatal("markers mismatch")
	}
}

func TestProjectAtomicSaveLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proj.json")
	if err := Save(validProject(), path); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Fatalf("temp file left behind: %s", e.Name())
		}
	}
}

func TestProjectRejectsUnknownMode(t *testing.T) {
	p := validProject()
	p.Visualizer.Mode = "laser-show"
	if err := Save(p, filepath.Join(t.TempDir(), "p.json")); err == nil {
		t.Fatal("invalid mode must fail validation before save")
	}
}

func TestProjectRejectsNewerVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "future.json")
	blob := []byte(`{"version": 99, "media": {"fingerprint": "x"}, "visualizer": {"mode": "bars"}}`)
	if err := os.WriteFile(path, blob, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("future version must be rejected")
	}
}

func TestProjectToleratesUnknownFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "futurefields.json")
	blob := []byte(`{
		"version": 1,
		"name": "X",
		"media": {"fileName": "a.wav", "fingerprint": "fp", "container": "wav", "duration": 1},
		"visualizer": {"mode": "bars", "holographicStereo": true},
		"aiMode": "disabled",
		"futureField": {"nested": [1,2,3]}
	}`)
	if err := os.WriteFile(path, blob, 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := Load(path)
	if err != nil {
		t.Fatalf("unknown fields must be ignored, got %v", err)
	}
	if p.Name != "X" || p.Visualizer.Mode != "bars" {
		t.Fatal("known fields corrupted by unknown fields")
	}
}

func TestResolveMediaPrefersRelative(t *testing.T) {
	dir := t.TempDir()
	media := filepath.Join(dir, "song.wav")
	if err := os.WriteFile(media, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := validProject()
	p.MediaRef.RelPath = "song.wav"
	p.MediaRef.AbsPath = "/definitely/missing/song.wav"
	got, err := p.ResolveMedia(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != media {
		t.Fatalf("resolved %s, want %s", got, media)
	}
	// missing everywhere → error
	p2 := validProject()
	p2.MediaRef.AbsPath = "/definitely/missing.wav"
	if _, err := p2.ResolveMedia(dir); err != ErrMediaMissing {
		t.Fatalf("want ErrMediaMissing, got %v", err)
	}
}

func TestClampingOnLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wild.json")
	blob := []byte(`{
		"version": 1,
		"media": {"fingerprint": "fp"},
		"visualizer": {"mode": "waveform", "sensitivity": 100, "smoothing": 5, "palette": 99},
		"export": {"width": 999999, "fps": 120}
	}`)
	if err := os.WriteFile(path, blob, 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if p.Visualizer.Sensitivity != 3 || p.Visualizer.Smoothing != 0.9 || p.Visualizer.Palette != 0 {
		t.Fatalf("clamping failed: %+v", p.Visualizer)
	}
	if p.Export.Width != 1920 || p.Export.FPS != 30 {
		t.Fatalf("export defaults not applied: %+v", p.Export)
	}
}
