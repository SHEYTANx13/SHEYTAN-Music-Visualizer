# Dependencies — exact upgrade record (2026-11-16 v3 work order)

## Run 45097038674 upgrades (2026-11-23)

- **astral-sh/setup-uv v10.3.3 → v10.4.0** (`.github/workflows/build.yml`,
  the only active `setup-uv` reference): pin moved to
  `uses: astral-sh/setup-uv@1db35dd8eee11c5894872e0b383ae6c406cf738d # v10.4.0`.
  The target SHA equals the remote dependabot branch
  `dependabot/github_actions/astral-sh/setup-uv-10.4.0` diff against main
  (fetched and diffed in this session). Inputs untouched
  (`version: '0.9.9'`, `enable-cache: false`); the comment above the pin
  now names v10.4.0.
- **actions/setup-go v7.0.0 → v7.1.0** (9 sites: 6 in
  `.github/workflows/build.yml`, 2 in `.github/workflows/codeql.yml`):
  pin moved to
  `uses: actions/setup-go@d8cbb8045d7f43706b9177cceb36c1f91a6644d5 # v7.1.0`
  everywhere, matching the remote dependabot branch
  `dependabot/github_actions/actions/setup-go-7.1.0` diff.
- **github.com/gogpu/gputypes v0.8.2 → v0.9.1** (`go.mod`/`go.sum`,
  matching the remote `dependabot/go_modules/go-minor-and-patch-a2dac3352f`
  diff; observed go.sum hashes `h1:b9EfCKhsgx36oeTHXl9lpgDfldGsfTe7ww8rkHOIloc=`
  and `go.mod h1:cnXrDMwTpWTvJLW1Vreop3PcT6a2YP/i3s91rPaOavw=`):
  `go mod tidy` clean afterwards; CI-canonical profile
  (`CGO_ENABLED=1 go build -tags goffi_static ./...`) and
  `CGO_ENABLED=1 go vet -tags goffi_static ./...` green on the upgraded
  graph (API-compatible).
- Verified locally after all three: pinned actionlint 1.7.12 clean on
  both workflows, zizmor 1.32.0 (offline mode) clean at low+ severity.
  A hosted run proving the upgraded actions on GitHub runners remains
  the confirmatory evidence (NOT RUN: no dispatch authorized).

## astral-sh/setup-uv v8.2.0 → v10.3.3

- Changed file: `.github/workflows/build.yml` (the only active
  `setup-uv` reference in the repository):
  - `uses: astral-sh/setup-uv@fac544c07dec837d0ccb6301d7b5580bf5edae39 # v8.2.0`
    → `uses: astral-sh/setup-uv@68559e99cc807c1eb7e9238fd43a15bba460eec2 # v10.3.3`
  - the explanatory comment above it now names v10.3.3.
- Upstream integrity verification performed during execution:
  `git ls-remote https://github.com/astral-sh/setup-uv refs/tags/v10.3.3`
  returned `68559e99cc807c1eb7e9238fd43a15bba460eec2` (lightweight tag
  pointing directly at the release commit). The previous pin
  `fac544c07dec837d0ccb6301d7b5580bf5edae39` was confirmed to be the
  v8.2.0 tag in the same way. The full 40-character immutable SHA is
  pinned; the short form was never used.
- `ai-notes.md` (historical design record describing the preflight)
  was updated to state the new verified commit and to note the upgrade
  provenance; the historical v8.2.0 SHA remains mentioned there only as
  history.
- Inputs not related to the version were untouched (`version: '0.9.9'`,
  `enable-cache: false`).
- A repository-wide search confirmed no other `setup-uv` reference
  exists in workflows, composite actions, docs, fixtures or policy
  files (`grep -rn "setup-uv"` → only build.yml; the
  unmerged upstream `dependabot/github_actions/astral-sh/setup-uv-10.3.3`
  branch exists on the remote but no local file referenced it).
- Not executed in this environment: a hosted Actions run that would
  prove `uv --version` and the zizmor environment through the upgraded
  action (no workflow dispatch is authorized from this task). The YAML
  remains valid — see the local validation note below.

## github.com/ulikunitz/xz v0.5.15 → v0.5.17

- `go.mod`: `github.com/ulikunitz/xz v0.5.15` → `v0.5.17` (direct
  dependency).
- `go.sum`: exactly one pin pair for v0.5.17
  (`h1:flR0y/x1hgM8EGV1AW3Xll6T413G0glV8UfBwR617V4=`,
  `/go.mod h1:H9Rt/W6/Qj27PGauhQc6nfCDy7vHpzsOThBSaYDoEhw=`);
  no stale v0.5.15 entries remain.
- Upstream verification: `proxy.golang.org/.../xz/@v/v0.5.17.info` →
  tag `v0.5.17` at commit `6ead826b4d3c7c9856f2daa905cf06403b9daddc`,
  published 2026-09-19. The v0.6.0 line is alpha
  (`v0.6.0-alpha*`/`v0.6.0-last-dev`) and was deliberately NOT taken;
  the work order pins exactly v0.5.17.
- Use sites: `internal/mediatools/xz.go` (`newXZReader`, the pure-Go
  decoder for the pinned Linux `.tar.xz` pack) — the only importer
  (`go mod why -m` confirms a direct edge; no transitive consumers).
- Commands executed and their results:
  - `go get github.com/ulikunitz/xz@v0.5.17` → upgraded v0.5.15 → v0.5.17
  - `go mod tidy` → clean (exit 0)
  - `go mod verify` → `all modules verified`
  - `go mod tidy -diff` → empty
  - `gofmt -l` → clean
  - `go build -tags goffi_static ./...` → exit 0 (full app incl. CGO
    Fyne/GL and the wgpu backend)
  - `go vet -tags goffi_static ./...` → exit 0
  - `go test -tags goffi_static -count=1 ./...` → all 25 packages ok
  - `go test -count=25 -run 'TestXZ|TestExtractTarXZ|TestManagerRemoveAndCheckForUpdate' ./internal/mediatools/` → ok (stability)
- New regression coverage: `internal/mediatools/xz_test.go`
  (16 tests) pins the required matrix against the real v0.5.17 code:
  single-block round-trip; multiple block sizes (1 KiB / 8 KiB /
  64 KiB); empty and 8 MiB large streams; concatenated streams;
  truncated streams (documented lenient block-boundary boundary case —
  never wrong data, never incomplete data without error); corrupt
  header magic; corrupt mid-stream body (CRC/checksum fail-closed);
  dictionary-cap bound (64 MiB-dict stream readable by default reader,
  small-cap reader must error or decode exactly); writer Close error
  propagation (constructor eagerly writes 24 bytes — measured);
  early/double Close safety; tar.xz extraction integration (happy
  path, traversal rejection, corrupt payload, non-xz payload); CRC64
  end-to-end integrity guarantee.
- Format compatibility: unchanged — the app only READS xz streams
  produced by upstream release archives; `newXZReader` semantics are
  identical across v0.5.15/v0.5.17 for this use (proven by the
  round-trip and extraction suites). No downgrade fallback was added.

## Other dependency constraints honored

- No gratuitous dependency churn: `go.mod`/`go.sum` diff is exactly
  the xz upgrade (non-negotiable 12).
- SHELL MIGRATION (2026-11-19/20, run 44462492262): `fyne.io/fyne/v2`
  was REMOVED from the production module after the replacement shells
  reached parity (`go.mod`/`go.sum` contain no Fyne reference;
  `scripts/ci/check-no-fyne.sh` enforces this in CI). TOOLKIT
  MIGRATION (2026-11-21, run 44648357437 repair): `gioui.org` was
  removed and the native shell now uses the actual Go-Gui library.
  The current presentation dependencies are:
  - `github.com/go-gui-org/go-gui v0.98.0` — the native Go-Gui shell
    toolkit (`cmd/app-gogui`, `ui/gogui`). Immediate-mode Go-first GUI;
    the Linux desktop backend is CGO-FREE (pure-Go X11 via jezek/xgb,
    runtime dlopen of libEGL/libGL/wayland/xkbcommon — no `-dev`
    headers or pkg-config UI modules at build time); Windows desktop is
    cgo-free; macOS uses the host toolchain (Metal backend).
  - `github.com/wailsapp/wails/v3 v3.1.0` — the alternative
    web-technology shell (`cmd/app-wails`,
    `internal/transport/wails`, `frontend/wails/dist`). Built ONLY in
    the cgo-free purego profile with the `sheytan_nogpu` tag, because
    the gogpu fakecgo stack and Wails' purego fakecgo cannot link into
    one binary; that profile renders through the honest CPU renderer.
  - `github.com/gogpu/wgpu v0.34.15` (pinned, unchanged) — the native
    GPU renderer behind `internal/render`; `v0.35.2` exists upstream
    and was deliberately NOT adopted (no blind upgrades; the pinned
    uniform contract is test-locked).
- License: xz is BSD-3-Clause (upstream LICENSE, unchanged by the
  patch upgrade); setup-uv is MIT.
- Rollback: revert the two file changes
  (`.github/workflows/build.yml` pin + `go get
  github.com/ulikunitz/xz@v0.5.15 && go mod tidy`) — no schema or
  API changes are coupled to either upgrade.

## Local workflow validation note

PowerShell is not installed in this execution sandbox, so the
repository's own PowerShell fixture suites (including
`Test-MediaToolsPinnedResolver.ps1` and `test-package-gates.sh`'s
pwsh-driver parts) could not be executed here. The YAML edit was
validated by inspection against the existing actionlint-clean form
(only the action ref and its comment changed; no keys, inputs or
structure altered). actionlint itself was not run locally (binary
download + the repo's pinned checksum gate belong to the hosted
preflight); hosted preflight remains the authoritative static gate.
