// preference_store.go — the persisted renderer preference and the
// explicit (non-env) selection entry point.
//
// Contract (run 42598929646 System Integration):
//   - Auto is the default. The user may persist Auto / CPU / Vulkan /
//     DX12 / Software; the choice survives restarts but is REVALIDATED
//     on every launch (hardware and drivers change; a stored GPU
//     preference that no longer resolves falls back to the CPU renderer
//     with the exact reason — never a silent lie).
//   - The store follows the fullscreen window-state pattern: a small
//     JSON document in the app config directory. A corrupt document is
//     an error, never a silent reset.
package render

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// preferenceDocument is the persisted JSON shape.
type preferenceDocument struct {
	Preference string `json:"preference"`
}

// PreferenceStore persists the renderer preference in the app config
// directory. It is safe for sequential use from the UI goroutine.
type PreferenceStore struct {
	path string
}

// OpenPreferenceStore returns a store rooted at path (typically
// <config>/renderer-preference.json).
func OpenPreferenceStore(path string) *PreferenceStore {
	return &PreferenceStore{path: path}
}

// Load returns (preference, found, error). found=false means no stored
// document (default Auto applies). A corrupt or invalid document is an
// ERROR — surfaced, never silently treated as Auto.
func (s *PreferenceStore) Load() (Preference, bool, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return PrefAuto, false, nil
	}
	if err != nil {
		return PrefAuto, false, fmt.Errorf("render preference read failed: %w", err)
	}
	var doc preferenceDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return PrefAuto, false, fmt.Errorf("render preference is corrupt JSON: %w", err)
	}
	p, err := ParsePreference(doc.Preference)
	if err != nil {
		return PrefAuto, false, fmt.Errorf("render preference invalid: %w", err)
	}
	return p, true, nil
}

// Save persists the preference atomically (temp file + rename) so a
// crash mid-write cannot leave a corrupt document.
func (s *PreferenceStore) Save(p Preference) error {
	if _, err := ParsePreference(string(p)); err != nil {
		return err
	}
	data, err := json.MarshalIndent(preferenceDocument{Preference: string(p)}, "", "  ")
	if err != nil {
		return fmt.Errorf("render preference marshal: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("render preference dir: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("render preference write: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("render preference replace: %w", err)
	}
	return nil
}

// SelectRendererExplicit selects with an explicit persisted preference
// (no environment variable involved). A manual CPU choice is honored
// directly; a manual GPU choice that cannot resolve falls back to the
// CPU renderer WITH the exact reason (never a silent lie), mirroring
// SelectRenderer's honesty contract.
func SelectRendererExplicit(pref Preference) (Renderer, BackendInfo) {
	if pref == PrefCPU {
		c := NewCPURenderer()
		ci := c.Info()
		ci.Detail = "renderer preference: CPU (explicit user setting)"
		return c, ci
	}
	return selectRendererWith(pref, false)
}
