# Building and verifying SHEYTAN Music Visualizer

Status: current as of run 42939743766 repair (2026-11-11). The Go toolchain
version authority is `go.mod` (go 1.27 line); CI resolves it with
`actions/setup-go` `go-version-file: go.mod`.

## Build tag (non-negotiable)

Every build that links Fyne (CGO) and wgpu (goffi FFI) together MUST use
`-tags goffi_static` — goffi's fakecgo/dlopen stubs cannot be placed by the
Go internal linker into a CGO binary otherwise (evidence in
`ai-notes.md`). The build scripts and CI apply it automatically; local
manual builds must too.

## Linux x64 dependencies

```bash
sudo apt-get install -y --no-install-recommends \
  gcc pkg-config \
  libgl1-mesa-dev libegl1-mesa-dev libx11-dev libxcursor-dev \
  libxrandr-dev libxinerama-dev libxi-dev libxxf86vm-dev \
  libwayland-dev libxkbcommon-dev
```

Optional, for the export-validation gates only (never bundled into the
product): `ffmpeg`, plus `xvfb x11-utils` for the virtual-display GUI smoke.

## Windows x64 dependencies

MSYS2 **UCRT64** (never the deprecated MINGW64):
`mingw-w64-ucrt-x86_64-gcc`, `mingw-w64-ucrt-x86_64-pkgconf`, with
`CGO_ENABLED=1 GOOS=windows GOARCH=amd64`. The desktop EXE is linked with
`-H=windowsgui` (PE subsystem 2); child processes stay hidden via
`internal/procattr`.

## Commands

```bash
# Linux (Bash)
scripts/build/build-linux.sh --mode preflight   # static gates only
scripts/build/build-linux.sh --mode desktop     # full verification + package

# Windows (PowerShell 7+, UCRT64 tools on PATH)
./scripts/build/build-windows.ps1
```

Both scripts capture every command's stdout+stderr from the first byte into
`ci-logs/`, record a structured `EXIT_CODE` record per gate, and write a
machine-readable `ci-logs/gate-results/<gate>.result.json`. Independent
gates keep running after a failure; dependent gates are recorded SKIPPED,
never passed.

## Gate ownership (do not duplicate work)

| Gate | Command (Linux form) | Owner |
|---|---|---|
| gofmt / mod tidy -diff / mod verify | `gofmt -l .` / `go mod tidy -diff` / `go mod verify` | both platform jobs + preflight |
| default unit suite | `go test -tags goffi_static -skip '^TestPaletteReachesGPUShader$' ./...` | both platform jobs |
| vet / package build | `go vet -tags goffi_static ./...` / `go build -tags goffi_static ./...` | both platform jobs |
| desktop EXE + `--version` / `--self-check` | built with injected metadata; commit must match `github.sha` | both platform jobs |
| package staging / license / archive / extract verification | see scripts | both platform jobs (exactly once) |
| **dedicated GPU gate** | `go test -tags goffi_static -v -run '^TestPaletteReachesGPUShader$' -count=1 ./internal/render/` | **only** `gpu-validation`, once per platform |
| race suite | `go test -race ... -skip '^TestPaletteReachesGPUShader$'` | platform jobs |
| export / audio / studio validation | real FFmpeg fixtures | Linux job (Windows: FFmpeg unavailable on hosted runners — recorded honestly) |

`TestPaletteReachesGPUShader` is excluded from the default and race suites
ONLY by its exact name because the dedicated GPU gate owns it — not because
it is unreliable. Do not remove the dedicated gate; do not reintroduce
duplicate runs.

## CI artifact contract (exactly three user-visible artifacts)

| Artifact | Produced by | Notes |
|---|---|---|
| `SHEYTAN-Music-Visualizer-windows-x64.zip` | build-windows | one app dir `SHEYTAN-Music-Visualizer-windows-x64/`, PE GUI subsystem 2 |
| `SHEYTAN-Music-Visualizer-linux-x64.tar.gz` | build-linux | one app dir `SHEYTAN-Music-Visualizer-linux-x64/`, exec bit preserved |
| `SHEYTAN-Music-Visualizer-all-logs.txt` | collect-logs | plain text, direct upload, no ZIP |

Packages upload as `VERIFIED` (all required gates green, expected commit
from `github.sha` matches the embedded metadata) or `TEST_ONLY` (package
gates green, some other gate red; the workflow stays red and the job
summary says **TEST-ONLY — required gates failed**). A package that fails
its own checks is never uploaded (`package_status: UNAVAILABLE`).

Temporary per-job log-part artifacts are collector inputs and are deleted
after the consolidated report upload is verified. Nothing else is uploaded:
no raw EXE, no staged folder, no duplicate variant, no source ZIP (the
source ZIP is a local agent deliverable, never a CI artifact).

## Diagnosing a red run

See `docs/CI-DIAGNOSTICS.md`. Short version: download
`SHEYTAN-Music-Visualizer-all-logs.txt`, read the gate table and the
root-cause grouping, and check the recorded environment limitations before
calling something a defect.

## Documentation consistency gate

`scripts/ci/tests/test-docs-consistency.sh` (run in preflight) verifies
that the canonical artifact names documented in the docs match the workflow
upload paths and that every local doc link in `readme.md`/`docs/*.md`
resolves to an existing file.
