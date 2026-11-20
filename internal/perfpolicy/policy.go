// Package perfpolicy persists the resource/performance mode inside the
// resolved config directory (same document pattern as
// renderer-preference.json and workspace-layout.json: small JSON,
// atomic replace, corrupt document is an ERROR — never a silent
// reset).
//
// History: this store was previously package-local to the Fyne UI
// (ui/fyne/policy_store.go). During the Go-Gui/Wails shell migration
// it moved here because the policy belongs to the toolkit-free core:
// both shells (and the CLI) read and write the same document.
package perfpolicy

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/apppaths"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/sysprofile"
)

// DefaultFilename is the persisted performance-mode document name
// inside the resolved config directory.
const DefaultFilename = "system-policy.json"

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

// StorePath returns the canonical document location for a layout.
func StorePath(lay apppaths.Layout) string {
	return filepath.Join(lay.Config, DefaultFilename)
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
// never leaves a half-written policy document behind.
func (s *PolicyStore) Save(m sysprofile.PerformanceMode) error {
	// Validity gate: re-parse the mode so an invalid value is an error,
	// never a persisted corrupt document.
	if _, err := sysprofile.ParsePerformanceMode(string(m)); err != nil {
		return err
	}
	b, err := json.MarshalIndent(policyDocument{Mode: string(m)}, "", "  ")
	if err != nil {
		return fmt.Errorf("system policy encode failed: %w", err)
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("system policy dir: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("system policy write failed: %w", err)
	}
	return os.Rename(tmp, s.path)
}
