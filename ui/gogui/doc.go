// Package gogui is the native Go-Gui (Gio) presentation shell of
// SHEYTAN Music Visualizer BETA.
//
// # Ownership rules
//
//   - This package owns exactly ONE top-level window per process. There
//     is no second window, no hidden fallback UI and no auxiliary
//     surface; every feature renders inside that single window.
//   - The core (internal/*) is UI-toolkit-free. This shell consumes the
//     core through the shared contracts in internal/presentation and
//     the design tokens in design. Gio types NEVER leak into internal/*
//     or cmd/app-gogui.
//   - The Gio event-loop goroutine owns ALL widget state. Background
//     services (studio events, play events, media-tools snapshots,
//     FFmpeg detection) communicate through the mutex-protected
//     snapshot in state.go; the bridge calls win.Invalidate() to ask
//     for the next frame. No widget is ever touched from another
//     goroutine.
//   - Layout/tab changes never restart audio or recreate the renderer:
//     studio.Studio and render.Renderer lifetimes live in shell.Run,
//     outside any layout or destination state.
//
// # The renderer bridge contract (GoGPU-adjacent boundary)
//
// render.SelectRenderer() is called exactly once at startup. The
// renderer (CPU or GPU behind the render.Renderer interface) is closed
// ONLY on shutdown, never on resize, re-layout or destination change.
// Each frame the shell may call studio.PreviewFrame with the CURRENT
// pane size; the result is blitted through a bounded ring of at most
// three reusable image buffers (preview.go) with a stale-frame guard
// keyed on the playback session. The frame pacer runs the preview at
// full rate while playing and throttles to a slow cadence when idle
// (via studio.IsPlayingLike) so an untouched window costs almost
// nothing.
//
// # Vertical-slice honesty notes
//
//   - Gio provides no file picker. Import, project open/save and export
//     output therefore use in-app path entry fields plus a recent-paths
//     list, NOT a native dialog. The reveal-in-folder action
//     (CmdLogsReveal) is the only action that may launch the platform
//     file-manager portal (xdg-open style), and it reports failure
//     honestly instead of pretending success.
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
package gogui
