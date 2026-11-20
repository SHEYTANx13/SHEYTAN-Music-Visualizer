// Package dist embeds the Wails web shell frontend for SHEYTAN Music
// Visualizer BETA.
//
// The embedded set is deliberately tiny and framework-free: one HTML
// document, one vanilla JavaScript module and one stylesheet, plus a
// byte-identical copy of the canonical design tokens
// (design/tokens.json). The copy is guarded by a test in
// internal/transport/wails that compares the embedded bytes against
// design.Raw(), so the two files can never silently drift apart.
//
// Nothing in this package imports a UI toolkit; the assets are plain
// static files served through the asset-server handler built by the
// shell (internal/transport/wails).
package dist

import (
	"embed"
	"io/fs"
)

// assets holds the four frontend files at the package root so request
// paths map 1:1 to file names ("/" serves index.html, "/main.js"
// serves main.js, ...).
//
//go:embed index.html main.js sheytan.css tokens.json
var assets embed.FS

// FS returns the embedded frontend assets as a read-only filesystem.
// The returned FS is safe for concurrent use.
func FS() fs.FS {
	return assets
}

// HandledAssets lists the asset names this package guarantees to
// serve. Tests use the list to prove the embedded set is complete and
// non-empty; the served set is exactly these four files.
func HandledAssets() []string {
	return []string{"index.html", "main.js", "sheytan.css", "tokens.json"}
}
