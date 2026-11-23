package presentation

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LayoutStore persists the versioned workspace layout with migration
// and corrupt-state recovery. Layout changes must never restart audio,
// reset a scene or cancel work: the store is written outside any
// transport/render lifecycle and only carries presentation state.
type LayoutStore struct {
	path string
}

// NewLayoutStore builds a store writing to the given JSON path.
func NewLayoutStore(path string) *LayoutStore {
	return &LayoutStore{path: path}
}

// DefaultFilename is the layout file name inside the config directory.
const DefaultFilename = "workspace-layout.json"

// Load reads the layout. Behavior:
//   - missing file  -> default layout (first run), no error
//   - corrupt JSON  -> default layout recovered, ErrRecovered reported
//   - v0/older      -> migrated to v1
//   - v1 valid      -> returned as-is (order validated)
func (s *LayoutStore) Load() (WorkspaceLayoutDTO, error) {
	b, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return DefaultLayout(), nil
		}
		return DefaultLayout(), fmt.Errorf("presentation: read layout: %w", err)
	}
	var raw struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return DefaultLayout(), ErrRecovered
	}
	switch raw.Version {
	case 0:
		return migrateV0(b)
	case 1:
		var lay WorkspaceLayoutDTO
		if err := json.Unmarshal(b, &lay); err != nil {
			return DefaultLayout(), ErrRecovered
		}
		if err := ValidateDestinationOrder(lay.TabOrder); err != nil {
			return DefaultLayout(), ErrRecovered
		}
		lay.Version = 1
		lay.RecentPaths = SanitizeRecentPaths(lay.RecentPaths)
		if lay.ActiveTab == "" {
			lay.ActiveTab = DestHome
		}
		return lay, nil
	default:
		// A newer schema than this build understands: keep defaults
		// rather than guessing at unknown fields.
		return DefaultLayout(), ErrRecovered
	}
}

// ErrRecovered signals that a corrupt or unreadable layout was replaced
// by the default layout. Callers log it; it is not a failure.
var ErrRecovered = errors.New("presentation: layout recovered from corrupt state")

// migrateV0 upgrades a pre-versioned layout (tab order only) to v1.
func migrateV0(b []byte) (WorkspaceLayoutDTO, error) {
	var v0 struct {
		Order []Destination `json:"order"`
	}
	if err := json.Unmarshal(b, &v0); err != nil || len(v0.Order) == 0 {
		return DefaultLayout(), ErrRecovered
	}
	lay := DefaultLayout()
	if err := ValidateDestinationOrder(v0.Order); err != nil {
		return DefaultLayout(), ErrRecovered
	}
	lay.TabOrder = v0.Order
	lay.Version = 1
	return lay, nil
}

// Save atomically persists the layout (write to temp, fsync, rename).
// Save validates before writing and never writes a layout that would
// fail Load.
func (s *LayoutStore) Save(lay WorkspaceLayoutDTO) error {
	if err := ValidateDestinationOrder(lay.TabOrder); err != nil {
		return err
	}
	lay.Version = 1
	lay.RecentPaths = SanitizeRecentPaths(lay.RecentPaths)
	b, err := json.MarshalIndent(lay, "", "  ")
	if err != nil {
		return fmt.Errorf("presentation: encode layout: %w", err)
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("presentation: create layout dir: %w", err)
	}
	tmp := s.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("presentation: write layout temp: %w", err)
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("presentation: write layout temp: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("presentation: sync layout temp: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("presentation: close layout temp: %w", err)
	}
	return os.Rename(tmp, s.path)
}

// RecentPathsMax is the persisted bound of the MRU import-path
// list (mirrors the shell's in-memory bound).
const RecentPathsMax = 5

// recentPathMaxBytes bounds one persisted path entry.
const recentPathMaxBytes = 1024

// SanitizeRecentPaths normalizes a persisted MRU list: non-empty
// entries only, each byte-bounded, deduplicated (first occurrence
// wins), capped at RecentPathsMax. Invalid entries are dropped,
// never fatal: a hostile or corrupt list cannot break layout load.
func SanitizeRecentPaths(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, p := range in {
		p = strings.TrimSpace(p)
		if p == "" || len(p) > recentPathMaxBytes || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
		if len(out) == RecentPathsMax {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
