// preference_store_test.go — persistence contract tests: roundtrip,
// missing file, corrupt file (an error, never silent auto), invalid
// value rejection, and the explicit-CPU selection behavior.
package render

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/visualizer"
)

// explicitCPUFrame builds a small deterministic frame input.
func explicitCPUFrame() visualizer.FrameInput {
	bins := make([]float64, visualizer.NumVizBins)
	for i := range bins {
		bins[i] = 0.5
	}
	return visualizer.FrameInput{
		FrameIndex: 1, Time: 1.0 / 30, Width: 32, Height: 32,
		Config: visualizer.Config{Mode: visualizer.ModeBars},
		Audio:  visualizer.AudioState{Bass: 0.5, Bins: bins},
	}
}

func TestPreferenceStoreRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "renderer-preference.json")
	s := OpenPreferenceStore(path)

	p, found, err := s.Load()
	if err != nil || found || p != PrefAuto {
		t.Fatalf("missing file → (%v, %v, %v), want auto, not found, nil", p, found, err)
	}

	for _, pref := range []Preference{PrefCPU, PrefVulkan, PrefDX12, PrefSoftware, PrefAuto} {
		if err := s.Save(pref); err != nil {
			t.Fatalf("save %s: %v", pref, err)
		}
		got, found, err := s.Load()
		if err != nil || !found || got != pref {
			t.Fatalf("roundtrip %s → (%v, %v, %v)", pref, got, found, err)
		}
	}
}

func TestPreferenceStoreCorruptIsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "renderer-preference.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := OpenPreferenceStore(path)
	if _, _, err := s.Load(); err == nil {
		t.Fatal("corrupt JSON must be an error, never silently auto")
	}
	if err := os.WriteFile(path, []byte(`{"preference":"bogus"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Load(); err == nil {
		t.Fatal("invalid stored value must be an error, never silently auto")
	}
}

func TestPreferenceStoreRejectsInvalidSave(t *testing.T) {
	s := OpenPreferenceStore(filepath.Join(t.TempDir(), "renderer-preference.json"))
	if err := s.Save(Preference("metal")); err == nil {
		t.Fatal("saving an unknown preference must be an error")
	}
}

// TestSelectRendererExplicitCPU honors the manual CPU override without
// any adapter attempt, with the reason recorded.
func TestSelectRendererExplicitCPU(t *testing.T) {
	r, info := SelectRendererExplicit(PrefCPU)
	defer r.Close()
	if info.Backend != BackendCPU {
		t.Fatalf("explicit CPU preference returned backend %q", info.Backend)
	}
	if info.Detail == "" || !containsFold(info.Detail, "explicit user setting") {
		t.Fatalf("explicit CPU selection must record its reason, got %q", info.Detail)
	}
	img, err := r.RenderFrame(explicitCPUFrame())
	if err != nil {
		t.Fatalf("render frame: %v", err)
	}
	if img == nil || img.Bounds().Empty() {
		t.Fatal("explicit CPU renderer produced an empty frame")
	}
}
