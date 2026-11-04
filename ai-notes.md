# ai-notes.md — AI engineering record and handoff (SHEYTAN Music Visualizer, Phase 1)

This file is the durable AI work log. It records what was actually done,
with command/output evidence for every claim. The concise external handoff
card is `note.md` (delivered beside the Phase 1 source archive); this file
is the detailed repository record.

---

## 1. Session identity and mission

- **Product:** SHEYTAN Music Visualizer (SHEYTAN™), publisher/licensor
  Parsaetak, author attribution Parsa Tak.
- **Phase:** 1 — Foundation only. Phase 2 (studio UI, media, DSP,
  visualizers, GPU, export) is explicitly out of scope for this session.
- **Mission date:** 2026-11-04.
- **Mission contract:** deliver exactly two files to the output directory —
  `SHEYTAN-Music-Visualizer-PHASE1-SOURCE.zip` and `note.md` — with all
  build evidence inside the archive at `evidence/phase1-build.log`.

## 2. Starting state

- Agent sandbox: Linux x86_64 (Debian 13 userspace, kernel
  5.10.134-013.15.kangaroo.al8.x86_64), user `z` (uid 1001, no root/sudo).
- No Go toolchain preinstalled: installed Go 1.27.3 from
  https://go.dev/dl/ (`go1.27.3.linux-amd64.tar.gz`) into the sandbox home.
  `go version` → `go version go1.27.3 linux/amd64`.
- gcc 14.2.0 (Debian), pkg-config 1.8.1, git 2.47.3, zip/unzip present.
- Xvfb present (used later for headless GUI runtime evidence).
- Working directory: `/home/z/my-project`; repository created at
  `/home/z/my-project/SHEYTAN-Music-Visualizer` and initialized as a fresh
  git repository on branch `main` (there was no pre-existing repository,
  no remote, no earlier work to preserve).
- Module path `github.com/SHEYTANx13/SHEYTAN-Music-Visualizer` is
  **provisional**: no git remote exists; nothing was pushed. Recorded per
  the Phase 1 contract §3.

## 3. Research baseline applied (with local verification)

- Go/Fyne baseline from the mission research (checked upstream
  2026-11-03): Go 1.27.3 stable, Fyne v2.8.2 stable.
  - Verified locally: `go get fyne.io/fyne/v2@v2.8.2` resolved and
    downloaded; `go mod tidy` completed cleanly; the module graph contains
    Fyne v2.8.2 as the only direct third-party dependency.
- GitHub Actions pins (full 40-hex SHAs) were verified read-only against
  upstream before use, via `raw.githubusercontent.com/<repo>/<sha>/README.md`
  (existence, HTTP 200) and the upstream release page HTML (short-SHA
  match):
  - `actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1` (v7.0.1) — verified
  - `actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e` (v7.0.0) — verified
  - `actions/upload-artifact@cf430e030ddbb5b0abf93d22962f4752f3646cd9` (v7.0.2) — verified
  - `actions/download-artifact@9000827ccba6bdab643e8b6fd33ac0654aef8333` (v8.0.2) — verified
  - `github/codeql-action@9b3254085494b55a0f7c4c2b3d2d52cc712ce370` (v4.39.1) — verified
  - `msys2/setup-msys2@ec48f7c5447b3140e2b088413ae3a55687bccb6e` — commit
    exists upstream; the GitHub API was not usable unauthenticated (HTTP
    403), so tag correspondence was confirmed via the commit page, which
    identifies the commit as `v2.33.0`. The workflow comments state this.
- License source: the Parsaetak Proprietary License v1.1 full text was
  retrieved from the reference repository
  `https://raw.githubusercontent.com/SHEYTANx13/SHEYTAN-LA/main/LICENSE.md`
  (499 lines, retrieved 2026-11-04) and adapted to this product per the
  mission contract: substantive terms preserved, Software name/scope
  adapted, no Apache-2.0 mixed-model content carried over.
- UCRT64 package names (`mingw-w64-ucrt-x86_64-gcc`,
  `mingw-w64-ucrt-x86_64-pkg-config`) were taken from the reference
  repository's proven `build-desktop.yml` (SHEYTAN-LA, read-only fetch).

## 4. Files created (and why)

- `go.mod`, `go.sum` — module definition; Fyne v2.8.2 the only direct
  dependency; `go`/`toolchain` directives are the single version authority.
- `internal/brand/brand.go` (+ test) — single identity authority: product
  name, trademark, licensor/signer, app identifier, palette tokens, legal
  footer, Phase 1 placeholder text.
- `internal/buildinfo/buildinfo.go` (+ test) — single build-metadata
  authority with linker-injectable variables and normalization; default
  version `0.1.0-dev` (no prior version authority existed; recorded here).
- `internal/logging/logging.go` (+ tests) — structured `log/slog` file
  logging under the app-owned user config dir, bounded rotation
  (`app.log` → `app.log.1`), visible errors, stderr fallback helper,
  content-security rules (no media bytes/credentials).
- `ui/fyne/app.go` — minimal Phase 1 window: exact product title, phase
  placeholder, build label, legal footer; no goroutines, no studio UI.
- `cmd/sheytan-music-visualizer/main.go` — entry point with `--version`,
  `--self-check` (no window) and default window launch; real exit codes.
- `cmd/ci-log-collector/main.go` — stdlib-only CLI around
  `internal/cilogreport` (kept dependency-free on purpose).
- `internal/cilogreport/report.go` + `report_test.go` — deterministic
  consolidated CI report engine: namespaced parts (never flattened),
  ERROR/WARNING indexes with provenance, NEVER_STARTED vs MISSING LOG
  SOURCE distinction, coverage statement, exit codes 0/1/2. Tests cover
  all ten required fixture cases plus manifest validation/strict fields.
- `scripts/ci/capture.sh` — Bash first-byte capture helper (tee,
  structured records, original exit code preserved, optional index).
- `scripts/ci/Capture-Step.ps1` — PowerShell mirror; `$LASTEXITCODE`
  saved immediately; exception paths can never report PASS; UTF-8 no BOM.
- `scripts/build/build-linux.sh` — full Linux gate ladder (gofmt, tidy +
  drift, test, vet, build, native executable, `--version`,
  `--self-check`, collector fixtures) with per-step capture, run summary,
  failure index; log directory parameterized by `CI_JOB_NAME` for CI reuse.
- `scripts/build/build-windows.ps1` — Windows gate ladder with UCRT64
  toolchain verification (rejects deprecated MINGW64) and the same
  capture/summary contract.
- `scripts/ci/collector-fixture-e2e.sh` — CLI-level end-to-end fixture
  evidence (green → exit 0/COMPLETE; red → exit 1/INCOMPLETE with failed +
  missing + skipped distinguished).
- `scripts/ci/workflow-contract-test.sh` — static contract checks for both
  workflows (SHA pins, permissions, persist-credentials, timeouts,
  merge-multiple: false, fail-closed coverage, CodeQL modes, no MINGW64,
  no pull_request_target, no secrets).
- `scripts/ci/assemble-evidence.sh`, `scripts/ci/verify-ladder.sh` —
  evidence transcript assembly and the full Phase 1 verification ladder.
- `.github/workflows/build-desktop.yml` — preflight / build-windows
  (UCRT64) / build-linux / collect-logs (`if: always()`, namespaced
  downloads with `merge-multiple: false`, collector run, report upload
  before fail-closed coverage enforcement, emergency fallback report).
- `.github/workflows/codeql.yml` — Go (build-mode manual with real Fyne
  prerequisites + traced `go build ./...`) and Actions (build-mode none)
  matrix, fail-fast false, weekly schedule, least-privilege permissions.
- `license.md`, `readme.md`, `agent.md`, `roadmap.md`, `ai-notes.md` — the
  five root documents per the content contracts.
- `.gitignore`, `.gitattributes` — repo hygiene (LF for text, CRLF for
  PowerShell, build outputs and logs ignored, evidence log tracked).
- `evidence/phase1-build.log` — assembled verification transcript (see §6).

## 5. Environment-specific build decisions (sandbox constraints, resolved)

This sandbox has **no root access** and no Fyne native dev packages, but
runtime libraries are installed. To make the CGO/Fyne build possible
locally, dev packages were vendored (this is sandbox infrastructure, NOT
part of the product):

1. Debian 13 `.deb` dev packages (libgl-dev, libglx-dev, libegl-dev,
   libopengl-dev, libglvnd-core-dev, libxcursor-dev, libxrandr-dev,
   libxinerama-dev, libxi-dev, libxxf86vm-dev, libxkbcommon-dev,
   libwayland-dev, libxfixes-dev, libxext-dev, libxrender-dev) extracted
   to `/home/z/fyne-deps` by `scripts/setup-fyne-deps.sh` (kept outside
   the archive, in the agent workspace scripts directory).
2. `prefix=/usr` inside the vendored `.pc` files rewritten to the local
   prefix (`--define-prefix` mishandles the multiarch
   `lib/<triplet>/pkgconfig` layout — found and fixed during bring-up).
3. Dev symlinks (e.g. `libwayland-client.so → libwayland-client.so.0`)
   bridged to the system runtime directory where the target existed.
4. Environment exported by the agent-workspace `scripts/go-env.sh`:
   `PKG_CONFIG_PATH`, `PKG_CONFIG`, `CGO_CFLAGS=-I$FYNE_DEPS/usr/include`,
   `CGO_LDFLAGS=-L$FYNE_DEPS/usr/lib/x86_64-linux-gnu`.
5. The shipped `scripts/build/build-linux.sh` supports the generic
   `FYNE_DEPS_PREFIX` hook for such setups; CI hosts using the documented
   apt package set do not need it. CI workflow steps install the
   prerequisites via apt exactly as documented in the workflow.

Failures hit during bring-up (all fixed; full transcripts in
`evidence/phase1-build.log`):

- `GL/glx.h: No such file or directory` — vendored `.pc` prefix remap bug
  (fix: rewrite `prefix=` as above).
- `wayland-client-core.h: No such file or directory` — go-gl/glfw v3.4
  does not use pkg-config on Linux; fix: `CGO_CFLAGS`/`CGO_LDFLAGS` to the
  vendored prefix.
- `cannot find -lwayland-client` / `-lGL` — dangling dev symlinks; fix:
  bridge to system runtime sonames.
- `selfCheck is not a function` — flag/function name collision in
  `main.go`; fix: renamed the flag variable.
- GNU grep 3.11 ERE `\)` quirk in the contract test; fix: `[)]` form.

## 6. Verification evidence (summary; full transcript in
`evidence/phase1-build.log`)

All commands were run with captured stdout/stderr and real exit codes.
Highlights (exact codes are in the transcript):

- `go build ./...` — PASS (includes full Fyne/CGO compile).
- Native executable build — PASS:
  `dist/sheytan-music-visualizer-linux-x64` (~32.8 MB, ELF x86-64).
- `--version` — PASS (exit 0): prints exact product name, `0.1.0-dev`,
  go1.27.3, linux/amd64, and the canonical legal footer.
- `--self-check` — PASS (exit 0): buildinfo, brand identity, brand legal,
  app-log-dir, app-log-write all PASS; wrote a real structured log entry.
- `go test ./...` — PASS: `internal/brand`, `internal/buildinfo`,
  `internal/cilogreport` (10 fixture cases + validation), `internal/logging`.
- `go test -race ./internal/logging ./internal/cilogreport` — PASS.
- `go vet ./...` — PASS. `gofmt -l .` — clean after `gofmt -w`.
- `scripts/build/build-linux.sh` full ladder — PASS (all 10 recorded gates
  exit 0).
- `scripts/ci/collector-fixture-e2e.sh` — PASS (green + red CLI cases).
- `actionlint` v1.7.12 — PASS on both workflows (after fixing an unquoted
  `:` in step names and bare job-header lines found by actionlint).
- YAML parse (PyYAML) — PASS on both workflows.
- `scripts/ci/workflow-contract-test.sh` — PASS (all contract checks).
- **GUI runtime under virtual display:** the Fyne window event loop ran
  for ≥10 s under `Xvfb :99` (1280x720x24), logged `starting` →
  `window closed cleanly` in the app log, and terminated cleanly on
  SIGTERM. Fyne emitted only benign headless warnings (XDG_RUNTIME_DIR,
  dbus theme watching; a `Preferences load error: EOF` on first launch
  while the Fyne preferences store does not exist yet). **A
  physical-display runtime check was not possible on this headless host
  and is NOT claimed.**
- **Full Phase 1 verification ladder** (`scripts/ci/verify-ladder.sh`) —
  PASS, 10/10 recorded gates exit 0: build-gates-linux (the 10
  `build-linux.sh` gates), collector-e2e, actionlint, yaml-parse,
  workflow-contracts, license-consistency, docs-consistency,
  git-diff-check, git-status, fyne-window-runtime-xvfb.
- Defects found by the ladder itself and fixed (records preserved in the
  transcript): the Xvfb probe initially matched its own shell via
  `pgrep -f` (the script text contains the binary name) and TERMed
  itself; a second attempt used an `env` marker that never appears in the
  child's command line. Final approach reads the real PID from `$!` in
  the launch subshell — deterministic and self-match-free.
- Evidence transcript assembled at `evidence/phase1-build.log`
  (649 lines at first full ladder pass; the final assembled version also
  contains the packaging validation records).
- Hosted CI: **NOT RUN** — nothing was pushed, no PR/tag/dispatch was
  created (Phase 1 forbids remote mutation). No run exists to inspect.
- Hosted CodeQL: **NOT RUN** — same reason. Workflow is defined and locally
  validated only.
- **Local security scan (`govulncheck`):** installed and executed
  (`golang.org/x/vuln/cmd/govulncheck@latest`, symbol-level analysis). First
  run found **4 reachable vulnerabilities** from `golang.org/x/image@v0.24.0`
  (GO-2026-5066, GO-2026-5062, GO-2026-5032 + one more; tiff decode paths
  reachable through Fyne). Fixed by upgrading the indirect dependency:
  `golang.org/x/image v0.24.0 → v0.43.0` (and `golang.org/x/text
  v0.22.0 → v0.38.0` pulled by tidy). Full gate set re-run after the bump
  (`gofmt`, `go test ./...`, `go vet ./...`, `go build ./...` — all PASS,
  captured as `vulnfix-*` and `phase1-final` records). Final scan:
  **"Your code is affected by 0 vulnerabilities"** (10 vulnerabilities
  exist in imported packages and 13 in required modules without being
  called by this codebase — reported by the tool, not remediable here
  without upstream work; the Phase 2 dependency audit re-checks them).

## 7. Packaging (recorded during delivery)

- The archive is created from a clean staging directory (not the live
  workspace), with exactly one top-level directory `SHEYTAN-Music-Visualizer/`.
- Excluded: `.git`, `ci-logs/`, `dist/` binaries, caches, temp files.
- Included: all source, tests, scripts, workflows, `go.mod`/`go.sum`, the
  five root documents, and `evidence/phase1-build.log`.
- `.ps1` files are normalized to CRLF in the staging copy, matching what
  `.gitattributes` produces on a Windows checkout.
- Validation: `unzip -t` integrity test, top-level layout check,
  required/forbidden file audit, extraction into a fresh temp directory,
  and `go test ./...` + `go build ./...` from the extracted tree
  (host prerequisites allow this via the vendored prefix).
- The archive is rebuilt once after the final `ai-notes.md`/evidence-log
  updates (the rebuilt archive differs only by those appended records;
  the extracted-tree build/test evidence remains valid for the same
  source content). The final archive's integrity test result is recorded
  in the external `note.md`.
- Git state at delivery: repository committed on branch `main`; the final
  commit hash and clean-worktree status are reported in `note.md`.

## 8. Failures and unresolved items

- Fixed during the session (see §5): GL/wayland header resolution, linker
  symlink bridging, Go flag name collision, YAML step-name quoting, grep
  ERE quirk.
- Unresolved (by design, not defects):
  - No hosted CI/CodeQL run exists (remote mutation forbidden in Phase 1).
  - No physical Windows hardware run and no physical Linux display run —
    reported honestly as NOT RUN; the Windows gate ladder is a hosted-CI
    responsibility.
  - No git remote; module path provisional.
  - `--version`/`--self-check` were run with default (non-linker-stamped)
    build metadata; the CI/staging pipeline defines the real values later.

## 9. Phase 2 starting point (next narrow mission)

1. Studio UI/UX milestone per `roadmap.md` Phase 2 §1 — import panel +
   central preview surface + theme from `internal/brand` tokens, behind
   the existing Fyne window skeleton.
2. In parallel, evaluate the media foundation prerequisite list in
   `roadmap.md` Research questions (decoder candidates first).
3. Constraints for the next agent: keep the log-capture contract intact
   (`capture.sh`/`Capture-Step.ps1`/collector); keep the verification
   ladder green (`scripts/ci/verify-ladder.sh`); update `ai-notes.md` with
   real evidence only; do not push or mutate any remote without explicit
   human authorization.
