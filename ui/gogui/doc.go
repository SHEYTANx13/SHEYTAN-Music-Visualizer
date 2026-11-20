// Package gogui is the native Go-Gui presentation shell of
// SHEYTAN Music Visualizer BETA, built on github.com/go-gui-org/go-gui
// v0.98.0 (the "gui" package plus its backend and soft renderer).
//
// # Ownership rules
//
//   - This package owns exactly ONE top-level window per process. There
//     is no second window, no hidden fallback UI and no auxiliary
//     surface; every feature renders inside that single window. The
//     construction goes through the newShellWindow seam so the
//     one-window contract is enforceable from source.
//   - The core (internal/*) is UI-toolkit-free. This shell consumes the
//     core through the shared contracts in internal/presentation and
//     the design tokens in design. go-gui types NEVER leak into
//     internal/* or cmd/app-gogui.
//   - The go-gui frame thread owns ALL widget state. Background
//     services (studio events, play events, media-tools snapshots,
//     FFmpeg detection) communicate through the mutex-protected
//     snapshot in state.go; the bridge asks for the next frame with
//     QueueCommand(func(*gui.Window){ w.InvalidateLayout() }) — the
//     documented thread-safe path, so no service goroutine ever
//     touches the view or widget state directly.
//   - Layout/tab changes never restart audio or recreate the renderer:
//     studio.Studio and render.Renderer lifetimes live in shell.Run,
//     outside any layout or destination state.
//
// # The renderer bridge contract (GoGPU-adjacent boundary)
//
// render.SelectRenderer() is called exactly once at startup. The
// renderer (CPU or GPU behind the render.Renderer interface) is closed
// ONLY on shutdown, never on resize, re-layout or destination change.
// The preview frame pacer (preview.go) is a producer goroutine: it
// renders studio.PreviewFrame through the shell renderer at the honest
// cadence — full rate while playing (30 ms floor) and a slow idle
// cadence otherwise (driven by studio.IsPlayingLike, additionally
// slowed by the reduced-motion accessibility switch) — and publishes
// frames through a gui.PixelSurface (gui.Image(Surface)). Each publish
// converts the renderer's premultiplied RGBA frame into the
// straight-alpha RGBA8 layout PixelSurface.Update copies; Update
// copies synchronously under its own lock before returning, so the
// bounded ring of at most three reusable conversion buffers (latest
// frame wins) is never recycled while a copy is in flight. The
// stale-frame guard keyed on the playback session is preserved. The
// PixelSurface wake covers the preview image redraw; the pacer also
// schedules a layout refresh at the same bounded cadence while
// playing, so the transport clock and status strip stay live on every
// destination.
//
// # Vertical-slice honesty notes
//
//   - Import, project open/save and export output use the go-gui
//     native file dialogs (w.NativeOpenDialog / w.NativeSaveDialog).
//     Where a platform has no native picker, go-gui falls back to its
//     in-window file browser — the dialogs never fake success. The
//     in-app path entry fields plus a recent-paths list remain as the
//     manual fallback, and their unavailability is stated in the UI.
//     File authorization checks stay in the Go core (fileaccess);
//     dialog results are only ever user-mediated path selections.
//   - The reveal-in-folder action (CmdLogsReveal) is the only action
//     that may launch the platform file-manager portal (xdg-open
//     style), and it reports failure honestly instead of pretending
//     success.
//   - Worlds and Downloads are honest empty states exactly as the
//     presentation catalog marks them: unavailable in this build, no
//     fake content.
//
// # Contracts consumed
//
//   - internal/presentation: destinations, command IDs, DTOs, EventBus
//     vocabulary and the versioned LayoutStore.
//   - design: MustLoad() token document (colors, intensity presets,
//     breakpoints, accessibility switches).
//   - internal/brand: window title and attribution.
//   - internal/studio, internal/render, internal/export,
//     internal/mediatools, internal/fileaccess, internal/apppaths,
//     internal/buildinfo: the service layer.
//   - github.com/go-gui-org/go-gui v0.98.0: gui (views, theme, window),
//     gui/backend (Run) and gui/backend/soft (headless smoke evidence).
package gogui
