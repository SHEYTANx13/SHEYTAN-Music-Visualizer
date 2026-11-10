// policy_store.go — the persisted resource-policy mode inside the
// resolved config directory (same document pattern as
// renderer-preference.json and window-state.json: small JSON, atomic
// replace, corrupt document is an ERROR — never a silent reset).
package fyneapp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/apppaths"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/sysprofile"
)

// policyDocument is the persisted JSON shape.
type policyDocument struct {
	Mode string `json:"mode"`
}

// PolicyStore persists the performance mode.
type PolicyStore struct {
	path string
}

// OpenPolicyStore returns a store rooted at path (typically
// <config>/system-policy.json).
func OpenPolicyStore(path string) *PolicyStore {
	return &PolicyStore{path: path}
}

// policyStorePath is the persisted performance-mode document location.
func policyStorePath(lay apppaths.Layout) string {
	return filepath.Join(lay.Config, "system-policy.json")
}

// Load returns (mode, found, error). found=false means no stored
// document (default Auto applies).
func (s *PolicyStore) Load() (sysprofile.PerformanceMode, bool, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return sysprofile.ModeAuto, false, nil
	}
	if err != nil {
		return sysprofile.ModeAuto, false, fmt.Errorf("system policy read failed: %w", err)
	}
	var doc policyDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return sysprofile.ModeAuto, false, fmt.Errorf("system policy is corrupt JSON: %w", err)
	}
	m, err := sysprofile.ParsePerformanceMode(doc.Mode)
	if err != nil {
		return sysprofile.ModeAuto, false, fmt.Errorf("system policy invalid: %w", err)
	}
	return m, true, nil
}

// Save persists the mode atomically (temp file + rename) so a crash
// mid-write cannot leave a corrupt document.
func (s *PolicyStore) Save(m sysprofile.PerformanceMode) error {
	if _, err := sysprofile.ParsePerformanceMode(string(m)); err != nil {
		return err
	}
	data, err := json.MarshalIndent(policyDocument{Mode: string(m)}, "", "  ")
	if err != nil {
		return fmt.Errorf("system policy marshal: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("system policy dir: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("system policy write: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("system policy replace: %w", err)
	}
	return nil
}
