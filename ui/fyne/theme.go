package fyneapp

// theme.go — the SHEYTAN hellish black/red Fyne theme built from the
// brand identity tokens (internal/brand). Black dominates; red is the
// accent for primary actions, focus and meaningful emphasis only.

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/brand"
)

// hellTheme implements fyne.Theme with the brand palette.
type hellTheme struct {
	fyne.Theme
}

// NewHellTheme builds the brand theme.
func NewHellTheme() fyne.Theme {
	return &hellTheme{Theme: theme.DefaultTheme()}
}

func hexColor(c string) color.Color {
	v := uint32(0)
	for _, ch := range c[1:] {
		v <<= 4
		switch {
		case ch >= '0' && ch <= '9':
			v |= uint32(ch - '0')
		case ch >= 'a' && ch <= 'f':
			v |= uint32(ch-'a') + 10
		case ch >= 'A' && ch <= 'F':
			v |= uint32(ch-'A') + 10
		}
	}
	return color.NRGBA{
		R: uint8((v >> 16) & 0xFF),
		G: uint8((v >> 8) & 0xFF),
		B: uint8(v & 0xFF),
		A: 255,
	}
}

// brandRGBA converts a brand token (with alpha for disabled states).
func brandRGBA(c string, a uint8) color.NRGBA {
	base := hexColor(c).(color.NRGBA)
	base.A = a
	return base
}

var (
	colBackground = hexColor(brand.ColorObsidian)
	colSurface    = hexColor(brand.ColorDeepCharcoal)
	colVolcanic   = hexColor(brand.ColorVolcanicSurface)
	colInput      = hexColor(brand.ColorDeepCharcoal)
	colDivider    = hexColor(brand.ColorDivider)
	colPrimary    = hexColor(brand.ColorInfernalRed)
	colEmber      = hexColor(brand.ColorEmberHighlight)
	colText       = hexColor(brand.ColorPrimaryText)
	colTextDim    = hexColor(brand.ColorSecondaryText)
	colDisabled   = brandRGBA(brand.ColorSecondaryText, 0x55)
	colFocus      = hexColor(brand.ColorHotEmberAccent)
	colShadow     = color.NRGBA{R: 0, G: 0, B: 0, A: 0x66}
	colMenuBg     = hexColor(brand.ColorVolcanicSurface)
	colOverlay    = hexColor(brand.ColorVolcanicSurface)
)

func (t *hellTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNameBackground:
		return colBackground
	case theme.ColorNameForeground:
		return colText
	case theme.ColorNamePrimary:
		return colPrimary
	case theme.ColorNameFocus:
		return colFocus
	case theme.ColorNameDisabled:
		return colDisabled
	case theme.ColorNameDisabledButton:
		return colVolcanic
	case theme.ColorNameButton:
		return colVolcanic
	case theme.ColorNameInputBackground:
		return colInput
	case theme.ColorNameInputBorder:
		return colDivider
	case theme.ColorNameMenuBackground:
		return colMenuBg
	case theme.ColorNameOverlayBackground:
		return colOverlay
	case theme.ColorNameSeparator:
		return colDivider
	case theme.ColorNameScrollBar:
		return brandRGBA(brand.ColorDivider, 0xA0)
	case theme.ColorNameSelection:
		return brandRGBA(brand.ColorDarkBlood, 0x88)
	case theme.ColorNameShadow:
		return colShadow
	case theme.ColorNameHover:
		return brandRGBA(brand.ColorDarkBlood, 0x66)
	case theme.ColorNamePlaceHolder:
		return colTextDim
	default:
		return t.Theme.Color(name, variant)
	}
}

func (t *hellTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNameSeparatorThickness:
		return 1
	case theme.SizeNamePadding:
		return 6
	case theme.SizeNameInnerPadding:
		return 9
	default:
		return t.Theme.Size(name)
	}
}
