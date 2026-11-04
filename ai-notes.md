# ai-notes.md — Durable AI Engineering Record

SHEYTAN Music Visualizer · SHEYTAN™ · Licensor: Parsaetak

> This file is the repository's durable, evidence-backed work log and the
> first thing a following agent reads (after the current user prompt).
> Every success claim carries a command/output reference. The concise
> external handoff card for Phase 1 is `note.md` (delivered beside the
> source ZIP); this file stays inside the repository.

---

## Session identity

- **Mission:** Phase 1 — foundation, product documentation, agent handoff,
  Windows/Linux build CI, CodeQL, and complete log capture.
- **Phase boundary:** Phase 1 only. No Phase 2 feature (studio UI, media
  import, playback, DSP, Song DNA, visualizer engines, GPU backend, export
  pipeline, presets) was implemented. See `roadmap.md`.

## Starting state

- **Repository:** new local repository created during this session at
  `/home/z/my-project/work/SHEYTAN-Music-Visualizer` — the agent
  environment exposed no pre-existing checkout, no remote, and no prior
  work. `git init -b main`; provisional module path
  `github.com/SHEYTANx13/SHEYTAN-Music-Visualizer` (provisional: no GitHub
  remote exists and none was created — remote-mutation is forbidden in
  Phase 1).
- **Starting branch/HEAD:** `main`, no prior commit (empty repository
  history); remotes: none. Worktree was empty before scaffolding.
- **Agent environment:** Linux x86_64, Debian GNU/Linux 13 (trixie),
  kernel 5.10.134; gcc 14.2.0 (Debian); git 2.47.3. Headless (no display
  server): GUI launch is possible to compile, not to observe here.
- **Toolchain installed by the agent for this session:** Go 1.27.3
  (downloaded from https://go.dev/dl, verified `go version go1.27.3
  linux/amd64`), Go module proxy reachable. actionlint v1.7.12 built from
  source via `go install github.com/rhysd/actionlint/cmd/actionlint@latest`.
- **Environment constraint discovered:** no passwordless sudo — Fyne
  native prerequisites could not be installed via apt. Workaround: the
  required Debian packages were downloaded with `apt-get download` and
  extracted to a local prefix (`/home/z/tools/x11-root`) with a
  `pkg-config --define-prefix` shim and `CGO_CFLAGS`/`CGO_LDFLAGS`
  pointing at the local include/lib trees. This is a **session-local
  workaround**, not a repository requirement; CI uses the standard apt
  package set.

## Files created (all first-party; nothing pre-existing was modified)

| Path | Why |
|---|---|
| `go.mod`, `go.sum` | module `github.com/SHEYTANx13/SHEYTAN-Music-Visualizer`; `go 1.27`, `toolchain go1.27.3`; single direct dependency `fyne.io/fyne/v2 v2.8.2` |
| `internal/brand/brand.go` (+ test) | single identity authority: names, trademark, licensor/signer, app ID, palette tokens, legal footer |
| `internal/buildinfo/buildinfo.go` (+ test) | single version/build metadata authority with linker-injected vars, sanitization, safe defaults |
| `internal/logging/logging.go` (+ test) | local slog JSON logging in user config dir, bounded rotation, visible errors, `SHEYTAN_LOG_DIR` explicit override |
| `internal/cilog/collect.go` (+ 12 fixture/contract tests) | deterministic consolidated CI report engine: error/warning indexes, NEVER_STARTED vs MISSING LOG SOURCE, coverage statement |
| `cmd/sheytan-music-visualizer/main.go` | desktop entry point: default window, `--version`, `--self-check`, non-zero exit on real errors |
| `cmd/ci-log-collector/main.go` | stdlib-only CLI for the consolidated report (exit 0 COMPLETE / 1 INCOMPLETE / 2 hard error) |
| `ui/fyne/app.go` | minimal Fyne launch surface: exact product title, Phase 1 placeholder, no studio UI |
| `scripts/ci/capture.sh` | Bash first-byte capture helper with structured records and true exit codes |
| `scripts/ci/Capture-Step.ps1` | PowerShell equivalent (immediate `$LASTEXITCODE` capture, exception recording) |
| `scripts/build/build-linux.sh` | Linux verification ladder with `--mode preflight|desktop` |
| `scripts/build/build-windows.ps1` | Windows verification ladder + portable staging/ZIP/PE validation |
| `.github/workflows/build-desktop.yml` | preflight / build-windows (UCRT64) / build-linux / collect-logs with namespaced log parts and fail-closed consolidation |
| `.github/workflows/codeql.yml` | CodeQL go(manual)/actions(none) matrix, least privilege, SHA pins |
| `license.md` | sole licensing authority: Parsaetak Proprietary License v1.1 adapted to this product + verified third-party inventory |
| `readme.md` | user-facing product truth and quickstart |
| `agent.md` | durable agent operating manual |
| `roadmap.md` | full product plan with phase statuses |
| `.gitignore`, `.gitattributes`, `.editorconfig` | repository hygiene |
| `evidence/phase1-build.log` | captured local verification transcript (see Packaging) |

## Research baseline verified at execution time (2026-11-04)

| Reference | Claimed | Verified | Method |
|---|---|---|---|
| Go stable | 1.27.3 | 1.27.3 (2026-10-19) | `https://go.dev/VERSION?m=text` |
| Fyne stable | v2.8.2 | v2.8.2 exists (2026-10-07) | Go module proxy `@v/v2.8.2.info` |
| actions/checkout v7.0.1 | `3d3c42e5…` | matches tag | `git ls-remote` |
| actions/setup-go v7.0.0 | `b7ad1dad…` | matches tag | `git ls-remote` |
| actions/upload-artifact v7.0.2 | `cf430e03…` | matches tag | `git ls-remote` |
| actions/download-artifact v8.0.2 | `9000827c…` | matches tag | `git ls-remote` |
| github/codeql-action v4.39.1 | `9b325408…` | matches peeled commit | `git ls-remote` (tag object `8498dc4c…` → commit `9b325408…`) |
| msys2/setup-msys2 pinned ref | `ec48f7c5…` | genuine upstream commit (v2.33.0, 2026-09-27); branch `main` has since advanced to `a53a959a…` | `git fetch --depth=1 origin <sha>` |
| SHEYTAN-LA LICENSE.md | source of legal terms | retrieved (499 lines) and adapted | raw.githubusercontent.com |

Decision: use the mission's pins as-is (all verified genuine). Fyne kept at
v2.8.2 (latest stable 2.8-line release; no newer stable patch observed).
No beta/pre-release upgrades.

## Architecture / dependency decisions

- **Fyne v2.8.2 is the only direct dependency** (`go mod tidy` produces a
  minimal graph; `go.sum` committed). No GPU/DSP/export dependencies were
  added — that is deliberate (Phase 2 research gates in `roadmap.md`).
- **`cmd/ci-log-collector` and `internal/cilog` are standard-library only**
  so the diagnostic path cannot break with the application graph.
- **Log collector contract**: namespaced artifact directories under
  `ci-parts/<artifact-name>/`, never flattened; deterministic job order =
  manifest order, files lexicographic; `SKIPPED` jobs → `NEVER_STARTED`
  (never a coverage failure); ran/cancelled/failed jobs without their
  expected artifact → `MISSING LOG SOURCE` (coverage INCOMPLETE; report is
  still written and uploaded, then the job fails); an artifact directory
  with zero files counts as missing evidence; empty but present log files
  count as captured and preserved.
- **Concurrency policy intentionally omitted** in `build-desktop.yml`:
  aggressive cancellation could kill `collect-logs` before evidence is
  preserved; artifact names are unambiguous per run. Recorded here as a
  decision.
- **`github.run_started_at` deliberately not used** in the manifest: it is
  a real GitHub context property but actionlint v1.7.12's schema rejects
  it and the validation gate must stay clean; the consolidated report
  carries its own accurate generation timestamp instead.
- **Windows portable artifact** is a staged folder (exe + license.md +
  readme.md) zipped and validated (MZ header + zip entry count) — a CI
  artifact, not a release; no installer/signing in Phase 1.
- **No CI badges** in `readme.md`: no repository URL exists yet.

## Verification record (local, with exit codes)

All commands below were captured into `evidence/phase1-build.log` with
full stdout/stderr, timestamps, working directory, tool context and
`EXIT_CODE:` records. Environment: Linux x86_64, Debian 13, Go 1.27.3,
gcc 14.2.0.

| # | Command | Exit | Result |
|---|---|---|---|
| 1 | `go version` / `go env` / `gcc --version` / `pkg-config --version` (toolchain report) | 0 | go1.27.3 linux/amd64; CGO_ENABLED=1 |
| 2 | `gofmt -l .` (via gate wrapper) | 0 | gofmt clean |
| 3 | `go mod tidy` | 0 | tidy OK |
| 4 | `go mod tidy -diff` | 0 | no diff after final tidy |
| 5 | `go test ./...` | 0 | all packages pass (brand, buildinfo, cilog, logging) |
| 6 | `go vet ./...` | 0 | clean |
| 7 | `go build ./...` | 0 | all packages compile |
| 8 | `go build -trimpath -o bin/sheytan-music-visualizer ./cmd/sheytan-music-visualizer` | 0 | native Linux x64 desktop executable built (CGO + Fyne linked) |
| 9 | `./bin/sheytan-music-visualizer --version` | 0 | prints product, version, commit/date/go version |
| 10 | `./bin/sheytan-music-visualizer --self-check` | 0 | buildinfo + brand + log-directory checks PASS |
| 11 | `go test -race ./internal/...` | 0 | race detector clean (pure-Go packages; see limitations) |
| 12 | `govulncheck ./...` (attempt 1, x/image v0.24.0) | 1 | **4 called vulnerabilities found** in `golang.org/x/image@v0.24.0` (GO-2026-5066, GO-2026-5062, GO-2026-5032, GO-2026-4815; all with example traces). Fix: `go get golang.org/x/image@v0.43.0` (also raised x/text→v0.38.0, x/mod→v0.36.0, x/sync→v0.21.0, x/tools→v0.45.0); `license.md` inventory updated. |
| 12b | `govulncheck ./...` (attempt 2, after fix) | 0 | no vulnerabilities called by the code; informational entries only in required-but-not-called modules |
| 13 | `python3` YAML parse of both workflow files | 0 | both parse; triggers/jobs as designed |
| 14 | `actionlint` v1.7.12 on both workflow files | 0 | clean (shellcheck integration NOT AVAILABLE — not installed) |
| 15 | packaging steps (staging, zip, `unzip -t`, listing audit, extraction, extracted-tree `go test`/`go build`) | see log | recorded in the log's packaging section |

`go test -race` note: applied to `./internal/...` only (the packages with
tests); the Fyne-linked packages have no Phase 1 unit tests and racing a
GUI toolkit on a headless host adds no signal.

### Failures encountered and fixed during the session (honest record)

1. **First `go build ./...` failed at link stage** (`cannot find -lwayland-client`, `-lGL`): the no-root dev-library tree contained the `-dev` symlink stubs but not the runtime `.so.1` files. Root cause: incomplete local extraction set. Fix: downloaded and extracted the corresponding runtime packages (`libgl1`, `libegl1`, `libwayland-client0`, …) into the local prefix. Re-run: PASS. (CI is unaffected — it uses the normal apt set.)
2. **Compile errors in `cmd/sheytan-music-visualizer/main.go`** (import alias collision `fyne` vs `fyneapp`, flag variable name collision with `selfCheck`). Fix: alias `fyneapp`, rename flag var. Re-run: PASS.
3. **`go vet` caught a real bug**: `fmt.Sprintf` format string in `buildinfo.Line()` had 6 verbs / 5 args. Fix: corrected format string; added vet to the permanent gate list (already present).
4. **Three collector test failures** during fixture development (empty-file marker expectation, missing-artifact fixture creating the artifact dir, report provenance substring). Fix: corrected fixture semantics (an artifact declared with files is absent → directory stays absent; empty-dir case tested explicitly) and test expectations. Re-run: PASS.
5. **actionlint rejection of `github.run_started_at`** (property not in its schema). Fix: removed the property; manifest carries the collector's own generation time; decision recorded above. Re-run: PASS.
6. **`sudo` unavailable** in the agent environment (password required): apt install impossible; handled via local `.deb` extraction (documented under Starting state). Recorded as an environment limitation, not a repository defect.
7. **govulncheck found 4 called vulnerabilities** in the Fyne transitive dependency `golang.org/x/image@v0.24.0` (attempt 1, exit 1). Root cause: outdated transitive version. Fix: minimal, contained dependency bump (`golang.org/x/image` → v0.43.0, the highest fixed version covering all four advisories); full ladder re-run clean; `license.md` inventory rows updated. Evidence: both govulncheck records preserved in `evidence/phase1-build.log` (first failure never overwritten).
8. **Packaging driver failures (first archive build)**: ZIP initially contained a stray `stage/` top-level prefix (zip built from the wrong working directory) and the layout audit used a relative path. Both failures were captured in the evidence log; root causes fixed; retries appended as new records (build #2 and later builds audited clean).

No test was deleted, weakened or skipped to achieve green.

## Runtime evidence and its limits (read before claiming anything)

- `--version` and `--self-check` were **executed on Linux** and captured
  (exit 0). A successful self-check is **not** a GUI runtime test.
- **The Fyne window was compiled but never observed on screen** — the
  Phase 1 agent host is headless (no display server). GUI visibility,
  event-loop survival and clean shutdown remain untested locally and are
  expected from the Windows/Linux CI jobs' build steps (which also do not
  open a window) and, ultimately, from a physical-machine run.
- **Windows**: not attempted locally (Linux host). Cross-compiling CGO/Fyne
  to Windows from Linux is unsupported here (requires a Windows C
  toolchain; MinGW cross-build of Fyne is not a Phase 1 gate). The Windows
  build is a hosted-CI gate → **HOSTED CI: NOT RUN**.
- **HOSTED CI: NOT RUN** — Phase 1 forbids push/PR/dispatch; no hosted
  GitHub Actions run exists for this code. **CodeQL: NOT RUN** — workflow
  defined and locally validated only; no analysis was executed and no
  alert result exists (absence of alerts is never claimed).

## Windows compiler and Linux CGO prerequisites (discovered)

- Linux (Ubuntu 24.04 CI): `gcc pkg-config libgl1-mesa-dev libegl1-mesa-dev
  libx11-dev libxcursor-dev libxrandr-dev libxinerama-dev libxi-dev
  libxxf86vm-dev libwayland-dev libxkbcommon-dev xorg-dev`; verified
  locally via `pkg-config --modversion` for all ten modules.
- Windows CI: MSYS2 **UCRT64** (`msys2/setup-msys2`, msystem UCRT64) with
  `mingw-w64-ucrt-x86_64-gcc` + `mingw-w64-ucrt-x86_64-pkgconf`;
  `CGO_ENABLED=1 GOOS=windows GOARCH=amd64`; `path-type: inherit` puts
  UCRT64 tools on PATH for PowerShell steps. Deprecated MINGW64 is not
  used anywhere.

## CodeQL workflow validation status

- Both workflow files pass YAML structural parse (PyYAML) and
  `actionlint` v1.7.12 (exit 0). Static contract review done: triggers,
  least-privilege permissions, full-SHA pins, `persist-credentials:
  false`, explicit timeouts, `fail-fast: false`, go manual build mode with
  Fyne prerequisites, distinct `codeql-logs-part` artifact.
- CodeQL **has not run** — no hosted execution, no SARIF, no alerts
  observed. Any future claim must come from an observed run.

## Packaging record (final, 2026-11-04)

- Output directory: `/home/z/my-project/download/` (the agent
  environment's download/output directory; pre-existing unrelated user
  file `README.md` was present and preserved untouched).
- Deliverables created: `SHEYTAN-Music-Visualizer-PHASE1-SOURCE.zip` and
  `note.md` — exactly two task-generated files; no sidecar log/report.
- ZIP composition: single top-level directory `SHEYTAN-Music-Visualizer/`
  (45 entries) containing source, workflows, scripts, five root documents
  and `evidence/phase1-build.log`. Excluded: `.git`, `bin/`, `ci-logs/`,
  `dist/`, caches, executables, media.
- Verification of the archive: build #1 procedure (integrity test, layout
  audit, extraction, extracted-tree `go test`/`go vet`/`go build`,
  extracted-tree executable + `--version` + `--self-check`) captured in
  the evidence log; the delivered archive received its own `unzip -t`
  (PASS, no errors), layout audit (PASS: one top-level dir, required
  files present, no forbidden paths), and embedded-log audit (PASS:
  ladder summary + `EXIT_CODE` records present). The delivered archive's
  sha256 and byte size are recorded in `note.md` — not in this file,
  because embedding the final archive's hash inside the archive itself is
  self-referential and impossible.
- Evidence log structure: chronological merged transcript of verification
  attempt 1 (govulncheck findings, packaging-driver failures — preserved,
  never overwritten) and attempt 2 (clean ladder after the dependency
  fix), followed by the packaging section and the verification ladder
  summary.
- The `note.md` handoff card carries the final repository HEAD, ZIP size
  and the complete verification matrix.

## Phase 2 starting point (execution order)

1. Windows/Linux hosted CI bring-up **only with explicit user
   authorization to push**; then observe real runs of `build-desktop.yml`
   and `codeql.yml` and record job-level results.
2. Studio UI/UX milestone (roadmap Phase 2 #1) on top of the Fyne shell —
   theme tokens first, then library/import/preview layout.
3. Media foundation (#2) with fixture-driven tests and cancellation.
4. Playback/synchronization (#3) behind narrow interfaces.
5. DSP research gate (#4): evaluate Gonum FFT correctness against
   generated fixtures before any engine work.
6. Keep GPU (#7) and export (#8) strictly behind their proof gates.

Unresolved technical decisions intentionally left open: DSP/FFT library,
decode strategy, playback library, GPU API choice, MP4/H.264/AAC encoding
path — all documented as Phase 2 research gates in `roadmap.md`; none is
prematurely locked.

**The model proposes. The tools execute. The laboratory verifies.**
