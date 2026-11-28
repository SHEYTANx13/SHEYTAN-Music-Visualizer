# SHEYTAN Design System — canonical tokens and shell sharing

This document describes the framework-independent design system that
keeps the product's presentation surfaces visually and behaviorally
identical. The product desktop shell is the Wails v3 web shell
(`cmd/app-wails` + embedded `frontend/wails/dist`); the retired Go-Gui
surface is recorded under "Historical shells" in `docs/architecture.md`.

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

1. The Go core loads the canonical document through
   `design.MustLoad()` (embed + validation); `design.Raw()` is the
   byte authority the shell serves to the web layer.
2. The web frontend injects CSS custom properties from the served
   tokens (canonical bytes fetched at boot: `frontend/wails/dist/js/tokens.js`,
   applied across the per-element stylesheets in `frontend/wails/dist/css/`;
   the former single `sheytan.css` file was split into those per-element
   stylesheets by the run-46033044814 frontend redesign).
3. Framework-native controls are implemented once, in the web shell;
   the shell shares tokens, navigation destinations, command IDs, DTOs
   and events (`internal/presentation`) with the toolkit-free core —
   never widget instances.
4. Every persisted user choice (tab order, active destination,
   intensity, renderer preference, performance mode) lives in
   toolkit-free stores (`internal/presentation`, `internal/render`,
   `internal/perfpolicy`) so the shell and the core read and write
   identical state.
