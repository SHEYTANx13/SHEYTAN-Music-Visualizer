// Package brand is the single source of truth for SHEYTAN Music Visualizer
// product identity: exact product name, trademark, licensor/signer
// attribution, application identifier, the black/red hellish palette tokens,
// and the canonical legal footer text.
//
// No other file in the product may hardcode these values. Documentation may
// naturally display the full product name, but identity constants used by
// code must come from this package.
package brand

// Product identity — fixed requirements (Phase 1 mission contract).
const (
	// ProductName is the exact product name. Do not reword it.
	ProductName = "SHEYTAN Music Visualizer"

	// BrandTag is the trademark tag used beside the product name.
	BrandTag = "SHEYTAN™"

	// Publisher is the licensor/copyright holder attribution.
	Publisher = "Parsaetak"

	// Author is the author/signer attribution where required.
	Author = "Parsa Tak"

	// AppIdentifier is the Fyne application identifier. It is provisional
	// until the final distribution channel is decided in Phase 2; the
	// decision is recorded in ai-notes.md.
	AppIdentifier = "com.parsaetak.sheytan-music-visualizer"

	// AppDirName is the app-owned directory name used under the user
	// config/data directory for local application state and logs.
	AppDirName = "SHEYTAN-Music-Visualizer"

	// CopyrightRange is the canonical copyright range statement.
	CopyrightRange = "© 2024–2026 Parsaetak. All rights reserved."

	// LicenseDocument is the canonical licensing authority file.
	LicenseDocument = "license.md"

	// LicenseName is the license governing all first-party material.
	LicenseName = "Parsaetak Proprietary License v1.1"

	// ContactURL is the licensor contact/source URL.
	ContactURL = "https://github.com/Parsaetak"

	// TrademarkStatement is the exact trademark position for this product.
	// It must not imply the software is open source or that third parties
	// are licensed to reuse SHEYTAN branding.
	TrademarkStatement = "SHEYTAN™ and the SHEYTAN logo are trademarks of Parsaetak. " +
		"They identify " + ProductName + " and its distribution. No license grants any right " +
		"to use the SHEYTAN name, logo or branding beyond what trademark law allows, and " +
		"nothing implies endorsement of derived works."

	// LegalFooter is the canonical one-line legal footer.
	LegalFooter = CopyrightRange + " " + Publisher + ". " + TrademarkStatement

	// PhaseCurrent identifies the current delivery phase.
	PhaseCurrent = "Phase 1 — Foundation"

	// PhasePlaceholder is the exact placeholder text shown in the minimal
	// Phase 1 window. Phase 2 replaces this surface with the studio UI.
	PhasePlaceholder = "Phase 1 foundation build. The studio (library, preview, visualizers, export) arrives in Phase 2 — see roadmap.md."
)

// Palette holds the black/red hellish identity tokens. These are identity
// tokens for documentation and future UI work; Phase 1 does not build the
// visual theme. Black dominates; red is the accent — premium, cinematic,
// restrained.
type Palette struct {
	Obsidian        string // app background
	DeepCharcoal    string // surface
	VolcanicSurface string // volcanic surface
	DarkBlood       string // primary crimson
	InfernalRed     string // primary action
	EmberHighlight  string // ember highlight
	HotEmberAccent  string // hot ember accent, used sparingly
	PrimaryText     string // primary text
	SecondaryText   string // secondary text
	Divider         string // dividers / subtle border
}

// Theme is the single palette authority. Phase 2 UI code must consume these
// tokens instead of introducing new ad-hoc colors.
var Theme = Palette{
	Obsidian:        "#080808",
	DeepCharcoal:    "#111111",
	VolcanicSurface: "#190909",
	DarkBlood:       "#9F0B18",
	InfernalRed:     "#D51D2A",
	EmberHighlight:  "#FF453A",
	HotEmberAccent:  "#FF7043",
	PrimaryText:     "#F2EEEE",
	SecondaryText:   "#A9A0A0",
	Divider:         "#342020",
}
