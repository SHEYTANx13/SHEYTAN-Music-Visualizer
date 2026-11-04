// Package brand is the single source of truth for the SHEYTAN Music
// Visualizer product identity: exact names, trademark position, licensor
// attribution, application identifier, theme tokens and the legal footer.
//
// Product and marketing strings must not be re-declared elsewhere in
// first-party code; import this package instead. Documentation files may
// naturally display the full product name.
package brand

// Identity constants. Do not edit values without updating license.md,
// readme.md and ai-notes.md together.
const (
	// ProductName is the exact product name.
	ProductName = "SHEYTAN Music Visualizer"

	// BrandTag is the brand mark used next to the product name.
	BrandTag = "SHEYTAN™"

	// Publisher is the licensor / copyright holder attribution.
	Publisher = "Parsaetak"

	// Author is the signer attribution used where a personal name is
	// required (e.g. build signatures, legal notices).
	Author = "Parsa Tak"

	// AppID is the application identifier for desktop integration.
	AppID = "com.parsaetak.sheytan-music-visualizer"

	// ModulePath is the Go module path. It is provisional: no GitHub
	// remote exists for this repository as of Phase 1. See ai-notes.md.
	ModulePath = "github.com/SHEYTANx13/SHEYTAN-Music-Visualizer"

	// Copyright is the canonical copyright line.
	Copyright = "© 2024–2026 Parsaetak. All rights reserved."

	// TrademarkStatement must not imply that the software is open source
	// or that third parties are licensed to reuse SHEYTAN branding.
	TrademarkStatement = "SHEYTAN™, SHEYTAN Music Visualizer and the SHEYTAN logo are trademarks of Parsaetak. " +
		"They identify the Software and its distribution; no license grants any right to use the SHEYTAN name, " +
		"logo or branding beyond what trademark law allows, and nothing here implies that the Software is " +
		"open source or that third parties are licensed to reuse SHEYTAN branding."

	// LicenseFile is the sole licensing authority document.
	LicenseFile = "license.md"

	// SourceURL is the canonical contact / source URL.
	SourceURL = "https://github.com/Parsaetak"

	// LogDirName is the directory name used inside the user config
	// directory for application-owned data.
	LogDirName = "SHEYTAN Music Visualizer"
)

// WindowTitle returns the exact Fyne window title for the product.
func WindowTitle() string {
	return ProductName
}

// LegalFooter returns the compact legal footer shown in the Phase 1
// placeholder window and used in CLI outputs.
func LegalFooter() string {
	return Copyright + " " + TrademarkStatement
}

// Theme direction: black and red — hellish / infernal. Black dominates;
// red is the accent. The direction is premium, cinematic, restrained,
// dark, powerful, precise and professional. Avoid excessive flames,
// skulls, glow, gradients or saturated red surfaces.
//
// These are identity tokens for documentation and future Phase 2 UI
// work. Phase 1 does not build the visual design.
const (
	// ColorObsidian is the app background.
	ColorObsidian = "#080808"

	// ColorDeepCharcoal is the deep charcoal surface.
	ColorDeepCharcoal = "#111111"

	// ColorVolcanicSurface is the volcanic surface.
	ColorVolcanicSurface = "#190909"

	// ColorDarkBlood is the primary crimson.
	ColorDarkBlood = "#9F0B18"

	// ColorInfernalRed is the primary action red.
	ColorInfernalRed = "#D51D2A"

	// ColorEmberHighlight is the ember highlight.
	ColorEmberHighlight = "#FF453A"

	// ColorHotEmberAccent is used sparingly.
	ColorHotEmberAccent = "#FF7043"

	// ColorPrimaryText is the primary text color.
	ColorPrimaryText = "#F2EEEE"

	// ColorSecondaryText is the secondary text color.
	ColorSecondaryText = "#A9A0A0"

	// ColorDivider is the divider / subtle border color.
	ColorDivider = "#342020"
)

// Palette returns the identity theme tokens in a stable order for
// diagnostics and tests.
func Palette() map[string]string {
	return map[string]string{
		"obsidian.background":   ColorObsidian,
		"surface.deepCharcoal":  ColorDeepCharcoal,
		"surface.volcanic":      ColorVolcanicSurface,
		"accent.darkBlood":      ColorDarkBlood,
		"accent.infernalRed":    ColorInfernalRed,
		"accent.emberHighlight": ColorEmberHighlight,
		"accent.hotEmber":       ColorHotEmberAccent,
		"text.primary":          ColorPrimaryText,
		"text.secondary":        ColorSecondaryText,
		"border.divider":        ColorDivider,
	}
}
