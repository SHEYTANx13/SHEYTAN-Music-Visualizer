// Package wails is the Wails v3 web-technology presentation shell of
// SHEYTAN Music Visualizer BETA: a thin, validated facade between the
// studio core and one webview window.
//
// # Facade boundary rules (non-negotiable)
//
//   - The shell owns exactly ONE top-level window (created in
//     BuildApp, guarded by the purego build tag). It never embeds,
//     drives or imports another shell's widget tree, and it imports
//     no native UI toolkit: each presentation shell stays its own
//     binary with its own window.
//   - Everything crossing the boundary is a versioned DTO from
//     internal/presentation (or an explicitly built JSON map). Domain
//     structs, project records, GPU frames and internal/render types
//     are never exposed to the web layer.
//   - Every mutating method validates its input against the canonical
//     authorities (presentation.ValidateCommand, design tokens,
//     clamped numeric ranges) before touching the studio. Invalid
//     input is a typed error, never a silent correction.
//   - The web layer gets NO filesystem write APIs, NO shell/process
//     execution and NO arbitrary path access. Library import, project
//     save/open and export stay native-shell actions in this slice;
//     the corresponding commands are rejected with an explicit typed
//     error stating the reason.
//   - The web shell does NOT claim to run the visualizer: it reports
//     renderer EVIDENCE (backend/GPU state machine values from
//     internal/render, captured once, verbatim) and honest states.
//     Real-time preview lives in the native shell.
//
// # Build layout
//
// This package is split by build tag on purpose:
//
//   - service.go (and the tests): toolkit-free facade plus the
//     /api/* HTTP mux over the embedded frontend. It imports only the
//     core packages, the frontend dist package and the standard
//     library, so it compiles and runs headless (go test works on a
//     display-less machine).
//   - app.go and the cmd/app-wails entry point are guarded by
//     //go:build purego && sheytan_nogpu: the Wails v3 shell requires
//     the cgo-free purego build profile, and the native GoGPU (gogpu)
//     stack cannot be linked into the same cgo-free binary (both ship
//     their own fakecgo symbols), so the web-shell profile always
//     renders through the honest CPU renderer with the exact reason
//     recorded in BackendInfo.
//     Wails v3 application package, which on linux/darwin requires
//     either cgo or the purego build tag (upstream buildguard). With
//     CGO_ENABLED=0 and no tags, a plain go test of this package must
//     still compile — hence the split.
//
// The JS side talks to Go through the /api/* endpoints of the asset
// handler (fetch-based, verified by httptest). The Wails runtime
// service-binding path (Call.ByID/ByName over /wails/runtime) exists
// and the service is registered as a Wails Service, but this slice
// deliberately keeps the verified, display-less-testable HTTP
// transport as the only frontend dependency.
package wails
