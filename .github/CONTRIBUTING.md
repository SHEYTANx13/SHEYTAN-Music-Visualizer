# Contributing to SHEYTAN Music Visualizer

Thanks for looking at this repository. SHEYTAN Music Visualizer is a
local-first audio visualizer for Windows and Linux desktops, authored by
Parsa Tak and licensed by Parsaetak — see the canonical `license.md`
(sole licensing artifact) before contributing code or redistributing
artifacts.

## Ground rules

1. **License first.** All first-party material is proprietary under the
   Parsaetak Proprietary License v1.1. Contributions accepted into this
   repository grant the licensor the same rights as the existing code; you
   keep attribution in your commits. Never add a second license file —
   `license.md` is the only legal authority.
2. **Never rewrite the license.** Packaging and CI compare every
   distributed `license.md` byte-for-byte against the tracked canonical
   file; a changed license fails the build by design.
3. **Preserve the single-window and no-console contracts.** The product UI
   lives in exactly one main Fyne window (`ui/fyne/one_window_test.go`
   enforces this), the Windows executable stays a GUI-subsystem binary
   (`-H=windowsgui`, PE subsystem 2), and child processes stay hidden via
   `internal/procattr`.

## Development workflow

```bash
# toolchain: Go (see go.mod toolchain line) + a C toolchain
# Windows: MSYS2 UCRT64 (never MINGW64); Linux: gcc + X11/GL/Wayland dev packages
go build -tags goffi_static ./...
go test -tags goffi_static ./...
go vet -tags goffi_static ./...
gofmt -l . && go mod tidy -diff && go mod verify
```

`goffi_static` is required when Fyne (CGO) and wgpu (goffi FFI) link into
one binary; see `docs/ci.md` for the exact per-platform requirements and
the full gate list. CI runs the same commands — run them before pushing.

## Commit and PR expectations

- One logical change per PR; keep commits atomic and descriptive.
- Every behavior change needs a test that fails without it.
- CI evidence is honest evidence: a missing capability is recorded
  BLOCKED / NOT PROVEN with its exact reason, never papered over.
- Documentation lives with the code: update `readme.md`, `CHANGELOG.md`
  and the relevant `docs/` page in the same PR.

## Reporting bugs

Run the in-app self-check first (`--self-check`, or Help → Diagnostics in
the main window) and attach its output plus the runtime log from the
documented log folder. Security issues follow `.github/SECURITY.md`
(private reporting only).
