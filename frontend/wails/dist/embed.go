// Package dist embeds the Wails web shell frontend for SHEYTAN Music
// Visualizer BETA.
//
// The embedded set is deliberately tiny and framework-free: one HTML
// document, vanilla JavaScript modules (one file per element under
// js/), one stylesheet per element (under css/) and a byte-identical
// copy of the canonical design tokens (design/tokens.json). The copy
// is guarded by a test in internal/transport/wails that compares the
// embedded bytes against design.Raw(), so the two files can never
// silently drift apart.
//
// Nothing in this package imports a UI toolkit; the assets are plain
// static files served through the asset-server handler built by the
// shell (internal/transport/wails).
package dist

import (
	"embed"
	"io/fs"
)

// assets holds the frontend tree so request paths map 1:1 to file
// names ("/" serves index.html, "/main.js" serves main.js, "/css/x.css"
// serves css/x.css, ...). gofmt-safe: the patterns cover the module
// entry, every js/ module, every css/ file and the token copy.
//
//go:embed index.html main.js tokens.json js css
var assets embed.FS

// FS returns the embedded frontend assets as a read-only filesystem.
// The returned FS is safe for concurrent use.
func FS() fs.FS {
	return assets
}

// HandledAssets lists the asset names this package guarantees to
// serve. Tests use the list to prove the embedded set is complete and
// non-empty; the served set is exactly these files.
func HandledAssets() []string {
	return []string{
		"index.html",
		"main.js",
		"tokens.json",
		"js/api.js",
		"js/boot.js",
		"js/commands.js",
		"js/dom.js",
		"js/drag.js",
		"js/exportpane.js",
		"js/loaders.js",
		"js/mediatools.js",
		"js/songdna.js",
		"js/state.js",
		"js/system.js",
		"js/tabs.js",
		"js/tokens.js",
		"css/adaptive.css",
		"css/base.css",
		"css/controls.css",
		"css/dna.css",
		"css/export.css",
		"css/responsive.css",
		"css/statusbar.css",
		"css/tabstrip.css",
		"css/topbar.css",
		"css/transport.css",
		"css/workspace.css",
	}
}
