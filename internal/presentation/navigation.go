// Package presentation defines the shell-independent presentation
// contracts for SHEYTAN Music Visualizer: navigation destinations,
// command identifiers, typed event envelopes with bounded delivery,
// and versioned DTOs.
//
// Both presentation shells — the native Go-Gui (Gio) shell and the
// Wails v3 web shell — consume these contracts. Shells share
// contracts, tokens and data, never widget instances: each shell owns
// its own top-level window and its whole widget tree, and exactly one
// shell owns any given window.
//
// The package imports no UI toolkit and no shell package. It is part
// of the UI-toolkit-free core boundary set.
package presentation

import "fmt"

// Destination identifies one workspace tab of the studio shell.
// The order is the canonical tab order; shells render destinations in
// this order and persist user reordering as ID sequences, never as
// hardcoded indexes.
type Destination string

// The eleven canonical workspace destinations.
const (
	DestHome         Destination = "home"
	DestStudio       Destination = "studio"
	DestWorlds       Destination = "worlds"
	DestTemplates    Destination = "templates"
	DestLibrary      Destination = "library"
	DestExport       Destination = "export"
	DestRequirements Destination = "requirements"
	DestDownloads    Destination = "downloads"
	DestSystemFiles  Destination = "system-files"
	DestSettings     Destination = "settings"
	DestDiagnostics  Destination = "diagnostics"
)

// Destinations is the canonical ordered destination set.
var Destinations = []Destination{
	DestHome, DestStudio, DestWorlds, DestTemplates, DestLibrary,
	DestExport, DestRequirements, DestDownloads, DestSystemFiles,
	DestSettings, DestDiagnostics,
}

// DestinationInfo describes one destination for shell navigation bars.
type DestinationInfo struct {
	ID          Destination `json:"id"`
	Title       string      `json:"title"`
	Icon        string      `json:"icon"`
	Order       int         `json:"order"`
	Available   bool        `json:"available"`
	EmptyState  string      `json:"emptyState,omitempty"`
	Description string      `json:"description"`
}

// DestinationCatalog is the shell-facing catalog with display metadata.
// Available=false destinations are honest placeholders backed by real
// empty states; they never render fake content.
func DestinationCatalog() []DestinationInfo {
	return []DestinationInfo{
		{ID: DestHome, Title: "Home", Icon: "home", Order: 1, Available: true,
			Description: "Product overview, quick actions and session status."},
		{ID: DestStudio, Title: "Studio", Icon: "studio", Order: 2, Available: true,
			Description: "Library, live preview, inspector and transport."},
		{ID: DestWorlds, Title: "Worlds", Icon: "worlds", Order: 3, Available: false,
			EmptyState:  "Worlds scenes are not implemented in this build.",
			Description: "Saved multi-scene worlds (planned)."},
		{ID: DestTemplates, Title: "Templates", Icon: "templates", Order: 4, Available: true,
			Description: "Built-in visualizer presets as starting points."},
		{ID: DestLibrary, Title: "Library", Icon: "library", Order: 5, Available: true,
			Description: "Imported songs and project files."},
		{ID: DestExport, Title: "Export", Icon: "export", Order: 6, Available: true,
			Description: "Deterministic offline MP4 export and validation."},
		{ID: DestRequirements, Title: "Requirements", Icon: "requirements", Order: 7, Available: true,
			Description: "Local media-tool requirements status (FFmpeg/FFprobe)."},
		{ID: DestDownloads, Title: "Downloads", Icon: "downloads", Order: 8, Available: false,
			EmptyState:  "Download Center is not implemented in this build.",
			Description: "Trusted-origin download transactions (planned)."},
		{ID: DestSystemFiles, Title: "System Files", Icon: "system-files", Order: 9, Available: true,
			Description: "Selected-scope file/folder analysis with least privilege."},
		{ID: DestSettings, Title: "Settings", Icon: "settings", Order: 10, Available: true,
			Description: "Theme intensity, renderer preference, performance mode."},
		{ID: DestDiagnostics, Title: "Diagnostics", Icon: "diagnostics", Order: 11, Available: true,
			Description: "Build identity, data layout, log path, renderer evidence."},
	}
}

// ValidateDestination reports an error when id is not canonical.
func ValidateDestination(id Destination) error {
	for _, d := range Destinations {
		if d == id {
			return nil
		}
	}
	return fmt.Errorf("presentation: unknown destination %q", id)
}

// ValidateDestinationOrder validates a persisted user tab order: it
// must be a permutation of the canonical set.
func ValidateDestinationOrder(order []Destination) error {
	if len(order) != len(Destinations) {
		return fmt.Errorf("presentation: order has %d entries, want %d", len(order), len(Destinations))
	}
	seen := map[Destination]bool{}
	for _, id := range order {
		if err := ValidateDestination(id); err != nil {
			return err
		}
		if seen[id] {
			return fmt.Errorf("presentation: duplicate destination %q in order", id)
		}
		seen[id] = true
	}
	return nil
}
