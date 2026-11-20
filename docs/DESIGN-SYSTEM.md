# SHEYTAN Design System — canonical tokens and shell sharing

This document describes the framework-independent design system that
keeps the native Go-Gui (Gio) shell and the Wails v3 web shell visually
and behaviorally identical.

## Source of truth

`design/tokens.json` is the single canonical token document
(schemaVersion 1, identity `BETA`). It is embedded by the `design`
Go package (`design/design.go`, `//go:embed`) and validated on load:

- structural checks (schema version, default preset existence, every
  preset inside the decoration contract ranges, every color `#RRGGBB`);
- the identity colors cross-checked against `internal/brand` constants
  (a drift between `design/tokens.json` and `internal/brand/brand.go`
  fails `go test ./design/`).

`design.Raw()` returns the exact JSON bytes so the Wails shell serves
the identical document to the web layer (`frontend/wails/dist/tokens.json`
is byte-compared against `design.Raw()` by a test — copies cannot drift).

## Token groups

| Group | Purpose |
|---|---|
| `color.surface` | obsidian `#080808`, deep-charcoal `#111111`, volcanic `#190909` |
| `color.accent` | dark blood `#9F0B18`, infernal red `#D51D2A`, ember `#FF453A`, hot ember `#FF7043` |
| `color.secondary` | antique gold `#B98A3E` (restrained secondary accent) |
| `color.text` | primary `#F2EEEE`, secondary `#A9A0A0` |
| `color.status` | accessible success/warning/error/info colors |
| `typography` | family stack + display/title/heading/body/caption scale |
| `spacing`, `radius`, `elevation` | 4 dp unit ramp, radii, elevation levels with optional crimson glow tint |
| `motion` | fast/base/slow durations, standard/emphasized easing, `reducedMotion`, `reducedGlow` |
| `decoration` | magic-circle rings, orbit nodes, rune marks, radial light, opacity/glow ceilings, embers |
| `intensityPresets` | `subtle` (0.05 / 0.15, no embers), `ritual` (0.10 / 0.35, embers), `infernal` (0.16 / 0.60, embers) |
| `breakpoints` | phone <600 dp stacked, tablet 600–959 stacked-wide, desktop ≥960 split, wide ≥1680 split-capped |
| `accessibility` | focus ring, WCAG AA contrast pairs, screen-reader announcement rules |

Decoration contract: original concentric magic circles, radial geometry,
orbit nodes and fictional rune-like marks only — never protected logos or
insignia. Decoration is always a low-opacity backdrop and must never
obscure timelines, waveforms, text or controls.

## Consumption rules

1. The native shell builds its Gio theme from `design.MustLoad()`
   (`ui/gogui/theme.go`); intensity presets and the reduced-motion /
   reduced-glow switches map onto the same values.
2. The web shell fetches `tokens.json` (canonical bytes) and injects
   CSS custom properties (`frontend/wails/dist/sheytan.css`).
3. Both shells implement framework-native controls separately; they
   share tokens, navigation destinations, command IDs, DTOs and events
   (`internal/presentation`), never widget instances.
4. Every persisted user choice (tab order, active destination,
   intensity, renderer preference, performance mode) lives in
   toolkit-free stores (`internal/presentation`, `internal/render`,
   `internal/perfpolicy`) so both shells read and write identical state.
