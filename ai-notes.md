# ai-notes.md — Durable AI Engineering Record

SHEYTAN Music Visualizer · SHEYTAN™ · Licensor: Parsaetak

> This file is the repository's durable, evidence-backed work log and the
> first thing a following agent reads (after the current user prompt).
> Every success claim carries a command/output reference. The concise
> external handoff card for Phase 1 is `note.md` (delivered beside the
> source ZIP); this file stays inside the repository.

---

## Session record — 2026-11-13 (run 43090342995 repair, delivered as source ZIP + note.md)

Concise facts for the next agent; full details in `CHANGELOG.md`,
`docs/REQUIREMENTS-TRACEABILITY.md` (APP-19…APP-30) and the session's
external `note.md`.

- Playback transport: one epoch triple (`basePos/baseCount/baseBuf`)
  captured under the transport mutex at every transition is the playhead
  authority; paused/stopped positions are frozen by state (late device
  ticks are structurally irrelevant); seeks are frame-aligned; the
  silent pacer is one goroutine per play session. Deterministic tests
  drive consumption synchronously — no sleeps, no tolerance windows.
- Close lifecycle: `internal/appclose` is the ONLY close path. The UI
  wires `SetCloseIntercept` + File→Quit into it; teardown stages run off
  the event loop with per-stage bounded deadlines; `fyne.Do(win.Close)`
  is the single finalization.
- Responsive root: `ui/fyne/responsive.go` recomposes shared regions at
  breakpoints (480/1024/1680). Fyne facts learned the hard way: wrap+
  truncation on one RichText panics; `Container.Add` does NOT re-parent
  (drop the old composition from the paint tree instead); a container
  with a nil layout never sizes children (MaxLayout wraps the swap
  target); BaseWidget routes container layouts to the RENDERER, and
  SimpleRenderer only Resizes — re-parented children keep stale
  positions without an explicit Move in a custom renderer.
- Startup: renderer probe + audio-device open are off the first-frame
  path; measured medians unchanged on the measurement host because
  ~950 ms of Go/library load + ~90 ms of GLFW/GL creation dominate — the
  evidence table is in `docs/performance-baseline.md`.
- Commit identity: `--build-info-json` (typed JSON) is the only machine
  authority; the pwsh and bash observers share one contract proven by
  P9/P10 fixture matrices; commit-identity failure is UNAVAILABLE.
- Resolver: R8 asserts the real 4-call-site `-RequireRepoRoot` contract;
  the preamble diagnostic must interpolate `$repoRoot` (R9 pins it).
- Workflow: SC2129 fixed by grouping appends; trigger branches verified
  byte-exact as `[main]`; zizmor online mode requires the hosted token.


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

---

# Phase 2 session record (2026-11-04)

## Session identity

- **Mission:** Phase 2 — turn the Phase 1 foundation into a working studio
  slice: media import, playback, DSP, Song DNA, visualizers, CPU/GPU
  renderers, MP4 export, projects/presets, and local verification.
- **Repository:** the live remote existed this time. Cloned
  `https://github.com/SHEYTANx13/SHEYTAN-Music-Visualizer` (origin verified
  against the mission URL; `go.mod` module path
  `github.com/SHEYTANx13/SHEYTAN-Music-Visualizer` confirmed). Default
  branch `main` (verified via `git ls-remote --symref`), starting HEAD
  `b77c8ee` ("beta"), clean worktree, in sync with origin/main. The Phase 1
  provisional-module-path note in `brand.go` is now historical — the
  module path is real; the comment was left intact on purpose (identity
  constants are frozen without a verified defect).
- **Agent environment:** Linux x86_64, Debian 13 (trixie), gcc 14.2.0,
  git 2.47.3, ffmpeg/ffprobe 7.1.5-0+deb13u1, headless (no display, no
  audio device). Go 1.27.3 installed from go.dev. No passwordless sudo —
  the Phase 1 X11 work-around (apt-get download → local prefix +
  pkg-config define-prefix via CGO_CFLAGS/CGO_LDFLAGS) was re-applied for
  the Fyne CGO build.

## Starting state verification

Phase 1 files read in full before editing: `license.md`, `readme.md`,
`agent.md`, `roadmap.md`, `ai-notes.md`, `go.mod`/`go.sum`, every Go file
under `cmd/`, `internal/`, `ui/`, both build scripts, both CI workflows,
`scripts/ci/*`. Baseline `go build ./...` + `go test ./...` PASS after
rebuilding the local X11 prefix. No user changes were discarded; nothing
was reset, rebased or force-checked-out. No push/PR/tag/release was made.

## Dependency lock (verified against the module proxy at run time)

| Concern | Module (pinned) | Note vs mission pins |
|---|---|---|
| UI | fyne.io/fyne/v2 v2.8.2 | as pinned |
| FFT | gonum.org/v1/gonum v0.17.0 | as pinned |
| Playback | github.com/ebitengine/oto/v3 v3.5.1 | as pinned |
| WAV | github.com/go-audio/wav v1.1.0 | as pinned |
| MP3 | github.com/llehouerou/go-mp3 v1.5.0 | as pinned |
| FLAC | github.com/tphakala/go-flac v1.4.0 | latest tag (mission said "resolve at implementation time") |
| Ogg/Vorbis | github.com/jfreymuth/oggvorbis v1.0.5 | as pinned |
| GPU | github.com/gogpu/wgpu v0.34.7 | v0.34.5 exists but v0.34.7 is the current tag (mission allowed verifying the current release) |
| FFmpeg | local 7.1.5 executable, never bundled | adapter only |

Archived `hajimehoshi/go-mp3` was NOT used; no LGPL codec modules were
added; no Rust FFI/C++ renderer code was introduced.

## Critical linker discovery (documented for every future session)

Linking Fyne (CGO) and wgpu (goffi FFI) into one binary fails with the Go
internal linker on Linux: `unhandled relocation for goffi_errno_location
(SDYNIMPORT)`. Fix: `-tags goffi_static` (goffi's static dlopen path).
Verified working for `go build` and `go test`. All build scripts, CI
delegation and docs apply the tag; Windows theoretically does not need it
(its goffi path differs) but gets it harmlessly.

## Files created / changed (Phase 2, first-party only)

| Path | Purpose |
|---|---|
| `internal/audio/` (audio, registry, decoders, decoders_wavfloat, convert, resample, fingerprint, cache, import + tests) | decoder registry, 4 native decoders, conversion, Kaiser-sinc resampler, canonical cache, import pipeline |
| `internal/dsp/` (dsp, tempo + tests) | streaming FFT analysis, bands, centroid, flux/onsets, BPM confidence, waveform envelope |
| `internal/songdna/` (+ test) | deterministic per-track DNA from measured features |
| `internal/visualizer/` (config, frame) | modes, config clamping, presets, palettes, frame contract |
| `internal/render/` (renderer, scene, cpu, gpu, text + tests) | Renderer contract, scene state/particles, deterministic CPU renderer (4 modes), wgpu GPU renderer with WGSL radial shader + verified readback, sfnt/opentype text overlays |
| `internal/playback/` (output, transport + tests) | Oto context, silent fallback, transport with buffering-aware clock |
| `internal/export/` (ffmpeg, job, validate + tests) | FFmpeg detection/proof, export job (streamed frames, progress, cancel), ffprobe validation |
| `internal/project/` (+ test) | versioned JSON project, atomic save, validation, relink |
| `internal/studio/` (studio + test) | application service layer (events, workers, ownership) |
| `ui/fyne/` (studio, actions, theme; app.go doc updated) | the studio shell + hellish theme (replaces the Phase 1 placeholder) |
| `cmd/sheytan-music-visualizer/main.go` | extended `--self-check` (decoders, DSP fixture, visualizer config, renderer, ffmpeg probe) |
| `scripts/build/build-linux.sh`, `build-windows.ps1` | Phase 2 ladders (race, export/GPU/audio validations, govulncheck, goffi_static) |
| `readme.md`, `roadmap.md`, `agent.md`, `license.md` | truth updates (this file: session record) |
| `evidence/phase2-build.log` | packaged verification transcript |

## Verification record (local, exit codes in evidence/phase2-build.log)

| Gate | Result |
|---|---|
| gofmt clean | PASS |
| go mod tidy / tidy -diff | PASS |
| go mod verify | PASS |
| go test -tags goffi_static ./... | PASS (audio, dsp, songdna, visualizer-internal, render, playback, export, project, studio, brand, buildinfo, cilog, logging) |
| go vet -tags goffi_static ./... | PASS |
| go build -tags goffi_static ./... | PASS |
| Linux x64 desktop executable + `--version` + `--self-check` (8 checks) | PASS |
| go test -race (audio, playback, dsp, export, studio, project, render) | PASS |
| Export validation: real FFmpeg, streamed frames | PASS — 1080p30 (90 frames, 3.00 s, h264+aac validated, ~2.6 s) and 4K24 (72 frames at 3840×2160, 3.00 s, validated, ~8.8 s); cancel path leaves no partial file |
| GPU validation (wgpu pipeline: adapter→device→WGSL→draw→submit→readback→deterministic compare) | PASS on the software adapter; hardware GPU: NOT AVAILABLE on this host |
| Studio end-to-end integration (import→analysis→DNA→deterministic preview→project round trip→service export validated) | PASS |
| govulncheck ./... | PASS — 0 called vulnerabilities (10 imported / 12 module-level informational, not called) |
| GUI runtime observation | NOT RUN — headless host, no display server; the window compiles and self-check passes, which is not GUI proof |
| Audio device playback (Oto with real device) | NOT AVAILABLE — no audio device on this host; transport verified via the silent output with identical clock logic |
| Windows x64 native build/runtime | NOT RUN — Linux host; script + CI definition statically validated only |

### Defects found and fixed during the session (honest record)

1. DSP `emitFrame` indexed the spectrum with `len(coeff)/2` although Gonum
   returns N/2+1 coefficients → panic on real input. Fixed (half = N/2).
2. `Resampler.Process` returned frames where every caller expected
   interleaved samples → half of the audio was written to the cache. Fixed
   (contract: samples).
3. The resampler feed loop re-appended the same input each iteration →
   unbounded queue, disk-full hang. Fixed (append once, drain with nil).
4. Canonical WAV cache entries were written without a header and never
   finalized (temp files aborted) → corrupt/missing wrapper. Fixed (header
   at open, atomic rename on finish).
5. `Cache.StoreSidecar` re-locked the mutex the import worker already held
   → deadlock. Fixed (locked internal variant).
6. Mono WAV downmix wrote half the samples (frame/sample confusion).
   Fixed.
7. Tempo estimator: subdivision bonus biased metronome material to the
   half-time reading; steady tones got spurious "reliable" tempo. Fixed
   (main-correlation scoring + explicit half/double handling + RMS-
   variation gate), tests updated.
8. wgpu renderer: instance released too early (deferred in constructor)
   and texture descriptors lacked Dimension/MipLevelCount → pipeline
   proof failures. Fixed; readback+verify now passes deterministically.
9. UI: Fyne slider API differences and a button self-reference compile
   error; `fyne.Do` bridging for all service events. Fixed.

No test was deleted, weakened or skipped to achieve green. Export/GPU
tests skip with explicit "SKIP (NOT AVAILABLE)" messages when FFmpeg or
an adapter is absent — the skip reason is recorded, never a fake PASS.

## Remaining gaps (next narrow mission order)

1. Windows native build + runtime proof on a real Windows 11 host
   (UCRT64), including Oto WASAPI playback and the FFmpeg export path.
2. Hardware-GPU proof (Vulkan/DX12) on a real GPU host; record
   adapter/backend/driver + executed workload + readback.
3. GUI observation (Xvfb or real desktop) — window/interaction survival.
4. M4A/AAC input via the optional FFmpeg decoder adapter (registry slot
   exists; deliberately unimplemented).
5. Keyboard navigation polish, manual tempo/offset correction, spectral
   rolloff, layered visual compositions — all explicitly NOT DONE.

---

## Repair & hardening session (Phase 2 follow-up, 2026-11-05)

Scope: repository repair pass against run 41889384784 (both build jobs
failed) — no feature expansion, no push/PR/tag/release.

Defect map established from the live run page (job conclusions and
durations), the shipped consolidated evidence, and source inspection:

1. **build-linux failed after a full PASS run (4m31s).** The workflow
   step copied `ci-logs/phase1-build.log` — a Phase 1 filename. The
   Phase 2 scripts write `ci-logs/phase2-build.log`. Under GitHub's
   default `bash -e`, `cp` of the missing file failed the step after
   every gate had already passed. The preflight variant survived only
   because it carried a `|| true` guard. Fix: removed the stale copy
   step (the script's own structured log is the artifact source).
2. **build-windows failed in 1m13s without a captured build.**
   `Capture-Step` ran gate scriptblocks in-process; `exit` inside a
   scriptblock terminates the enclosing script, so
   `build-windows.ps1` unwound at the `desktop-env-check` gate's
   `exit 0` — matching the reported log cutoff after environment
   detection in BOTH runs 41889384784 and 41837000543. Fix:
   `Capture-Step` serializes each gate to a script and runs it in a
   child pwsh process, making `exit N` mean "this gate's exit code".
   Additionally the runner's preinstalled `C:\mingw64\bin\gcc.exe`
   and Strawberry `pkg-config 0.26` shadowed MSYS2 UCRT64
   (setup-msys2 scopes PATH changes to `shell: msys2` steps only).
   Fix: the workflow prepends the action's `msys2-location` output
   (`ucrt64\bin`) to the runner PATH and a hard identity gate fails
   fast unless the first `gcc`/`pkg-config` on PATH are the UCRT64
   toolchain (`-dumpmachine`, `_UCRT` define, version >= 1.8).
3. **Diagnostics consolidation** redesigned: collector now fetches the
   authoritative GitHub job/step conclusions and complete raw job logs
   for the same run attempt (stdlib-only REST client, best effort,
   limitations recorded verbatim), writes ONE file named `log.txt`
   with the nine required sections, redacts secrets (exact workflow
   token + generic token/key/PEM patterns), derives the failure index
   from job/step conclusions AND text records, and exits non-zero
   (1=incomplete coverage, 3=required job failure) after the report is
   uploaded. Temporary part artifacts are deleted by the collector job
   (`actions: write`, scoped, documented).
4. **CodeQL** became a reusable workflow called by build-desktop
   (push/PR coverage through the caller; schedule/dispatch standalone
   with its own single log.txt). The traced manual build now matches
   production: `go build -tags goffi_static ./...`.
5. **Brand/legal**: Parsaetak's official link corrected to
   https://linktr.ee/Parsaetak everywhere, SHEYTAN project home
   https://github.com/SHEYTANx13 added alongside; two-party roles made
   explicit in brand.go (LicensorURL/ProjectURL), CLI --version, About
   dialog, license.md and readme.md. Original proprietary license text
   preserved unchanged.
6. **GPU path**: real uniform-layout bug fixed (WGSL vec4 alignment
   puts the bin array at offset 48 and colors at 304; the Go side
   wrote 32/288, misaligning the spectrum and shifting the palette by
   16 bytes). Adapter ownership fixed (the adapter handle was dropped
   out of scope while the comment claimed it was retained). Explicit
   preference model Auto/Vulkan/DX12/Software (SHEYTAN_GPU_PREFERENCE)
   mapped onto the pinned wgpu v0.34.7 instance backend mask; device
   loss classified via wgpu.ErrDeviceLost; uniform staging buffer
   reused across frames; device type/vendor/device IDs surfaced.
7. **Build hygiene**: `go mod tidy` (mutating) replaced by
   `go mod tidy -diff`; preflight slimmed to gofmt + tidy -diff + mod
   verify (tests/vet/builds run once in the platform jobs); buildinfo
   injected via ldflags (commit/date/builtBy); govulncheck pinned to
   v1.9.0 (v1.1.4 cannot build SSA under go1.27); setup-go cache
   enabled; least-privilege workflow permissions; queued (not
   cancelled) concurrency.

Verification statuses of this session are recorded in
`evidence/log.txt` (generated by the collector itself) and note.md.

---

# Repair + roadmap-completion session (2026-11-05)

Scope: run 41915116613 repair (Windows `$goTags` failure, collector
summary mismatch, Ubuntu 26.04 migration) plus completion of the
remaining Phase 2 roadmap items. No push/PR/tag/release was made; all
verification is local on Linux x86_64 (Debian 13 trixie, gcc 14.2.0,
Go 1.27.3, ffmpeg 7.1.5, Xvfb). Hosted CI: NOT RUN this session
(no push); the hosted-availability of the `ubuntu-26.04` label is
therefore NOT OBSERVED (actionlint v1.7.12's label DB predates it —
registered via `.github/actionlint.yaml` with an explicit honesty note).

## Root causes established for run 41915116613 (all three reproduced
locally with pwsh 7.5.4 child processes)

1. **`$goTags` parent-scope reference (the four real failures).**
   `Capture-Step` serializes each gate scriptblock into a standalone
   script under `ci-logs/step-scripts/` and runs it in a NEW pwsh
   process with `Set-StrictMode -Version Latest`. A child process
   never inherits the caller's PowerShell variables. The four gates
   `go-test`, `go-vet`, `go-build-packages`, `build-executable`
   referenced the parent-only `$goTags` and failed with the exact
   reported error: `The variable '$goTags' cannot be retrieved because
   it has not been set.` Fix: single documented tag source
   `$env:SHEYTAN_GO_TAGS = "-tags goffi_static"` set by
   `build-windows.ps1`; every generated gate script initializes and
   validates its OWN local `$goTags` array from it in the
   `Capture-Step` preamble (missing/malformed source fails the gate
   loudly; StrictMode kept).
2. **Capture-Step output-stream pollution (the false nine-gate
   summary).** `Capture-Step` returned its exit code THROUGH the
   success output stream together with the Tee-Object passthrough, so
   `$rc = Capture-Step ...` captured the whole transcript plus the
   code; the callers' `if ($rc -ne 0)` became an array filter that is
   truthy for ANY passing gate with output. The five passing gates
   (toolchain-report, desktop-env-check, gofmt-verify,
   go-mod-tidy-verify, go-mod-verify) were therefore counted as
   failures: 4 real + 5 false = the inconsistent nine-gate summary
   against the four-record failure index. Fix: child console output is
   forwarded via `Write-Host` (information stream); the function
   returns ONLY the exit code.
3. **Lost native exit codes at child-script end.** `pwsh -File`
   exits 0 when a script ends without `exit`, so a failing native
   command at gate end would be recorded PASS. Fix: every generated
   gate script carries the guarded epilogue
   `if (Test-Path Variable:LASTEXITCODE) { exit $LASTEXITCODE } else { exit 0 }`.
   Verified: a stub command exiting 3 yields `EXIT_CODE: 3 FAIL`.

Workflow hardening in the same pass: the UCRT64 identity gate no
longer pipes gcc into `Select-Object -First 1` (full-output capture
first); collect-logs and the standalone CodeQL collect use
`if: ${{ always() && !cancelled() }}`; CodeQL log parts are named per
matrix dimension (`codeql-logs-part-go/-actions`, the actions leg now
writes an evidence marker) and both cleanup filters match the new
names.

## Collector repair (nine-versus-four class)

`internal/cilog/gate.go` adds the AUTHORITATIVE gate-status table:
gate status derives ONLY from each structured command record's own
`EXIT_CODE` line inside a `===== COMMAND BEGIN/END =====` block; a
gate with `EXIT_CODE: 0 PASS` can never be listed failed;
`SKIPPED: gate ...` markers are dependency skips, not command
failures; worst exit code wins across copies; provenance is kept per
gate; text-derived records are deduplicated by identical text with
all provenances preserved and stay explicitly supplemental. The
report gained section 5.B (gate table + counts that must agree) and
`##[error]`/`##[warning]` annotation forms are now indexed. Twelve new
fixtures in `internal/cilog/gate_test.go` encode the required
classes, including the exact regression: five passing gates + four
`$goTags` failures + five skips + summary/index copies must yield
`FailedGateCount == 4` and nothing else.

## Ubuntu 26.04 migration

Every Linux job (preflight, build-linux, collect-logs, CodeQL
analyze + collect) now pins `runs-on: ubuntu-26.04`; windows-latest is
unchanged. Cache policy: exactly ONE Linux job (build-linux) saves the
Go build cache; preflight, CodeQL and collect-logs run with
`cache: false` so no two jobs can fight over one cache key
(`another job may be creating this cache` class). Package
availability on the 26.04 image could NOT be verified from this
session (no hosted runner): the apt set is unchanged and the
migration evidence step prints `/etc/os-release` into the job log.
Status: IMPLEMENTED + actionlint/YAML-clean; hosted-run verification
NOT RUN.

## Roadmap capabilities completed this session (evidence: local gates)

- **M4A/AAC/ALAC input** — optional local-FFmpeg adapter
  (`internal/audio/decoders_ffmpeg.go`): ffprobe JSON probe with the
  shared limits, `ffmpeg -f f32le` stdout stream with honest failure
  surfacing, context-aware cancellation (import cancel kills the
  child; `Close` preserves the real exit status), idempotent optional
  registration with status/reason, sidecar provenance
  (`NativeDecoder: false`). Seven fixtures; real decode through the
  full import pipeline verified against generated AAC fixtures.
- **Layered compositions** — `BackgroundLayer`/`BackgroundIntensity`
  (0–0.6) in `visualizer.Config` + project mirror fields + CPU
  renderer compositing via a dimmed palette + inspector Select/slider.
  Deterministic; layer-free output byte-identical to the historical
  path; GPU shader scope unchanged and documented.
- **Manual tempo/offset correction** — `TempoOverrideBPM` (30–300,
  bypasses the confidence gate) and `SyncOffsetSeconds` (±5 s,
  features read at t−offset) on the studio and in versioned projects;
  `AudioStateAt` applies both so preview and export share them; UI
  entries in the inspector; six fixtures including a project round
  trip.
- **Spectral rolloff** — 85%-energy rolloff per DSP frame +
  `MeanRolloff` (`internal/dsp`); fixtures: pure tone ≈ tone
  frequency, broadband clicks high, silence zero.
- **Keyboard transport** — Space = play/pause, ←/→ = ∓5 s seek
  (`ui/fyne/studio.go`, `installTransportKeys`), dispatched through
  the same service actions as the buttons.
- **Performance benchmarks** — `internal/dsp/bench_test.go`
  (60 s of audio analyzed in ≈0.40 s on the 2-core verification host)
  and `internal/render/cpu_bench_test.go` (1080p bars ≈50 ms/frame,
  radial ≈17.5 ms/frame; 4K waveform benchmark included). Numbers are
  host-relative; the benchmark code is the durable artifact.
- **GPU device-loss recovery drills** — injectable `renderGPUFn` seam;
  injected `wgpu.ErrDeviceLost` → FALLBACK state with explicit
  "device lost:" detail and a valid CPU frame; a generic GPU failure
  stays distinguishable (`internal/render/gpu_recovery_test.go`). Real
  hardware drills remain open.
- **GUI launch observed** — the studio window was launched under
  Xvfb: the X window tree shows the mapped "SHEYTAN Music Visualizer"
  window (1440×860), the process survived the event loop and exited
  cleanly on SIGTERM (rc=0); screenshot at
  `evidence/gui-smoke-xvfb.png`. A `gui-smoke` gate now runs in
  `build-linux.sh` desktop mode when Xvfb exists (SKIPPED honestly
  otherwise). Physical-desktop observation remains open.

## Verification record (this session, local)

| Gate | Result |
|---|---|
| gofmt -l . | PASS (empty) — including pre-existing non-gofmt-clean `internal/cilog/collect.go`, now formatted |
| go mod tidy -diff / go mod verify | PASS (after the dependency bump) |
| go test -tags goffi_static ./... | PASS — full ladder gate 17/17 |
| go vet -tags goffi_static ./... | PASS |
| go build -tags goffi_static ./... + native exe + --version + --self-check | PASS — `decoders=7 (aac, alac, flac, m4a, mp3, ogg, wav)`; honest `backend=cpu gpu=none api=software` |
| go test -race (audio, playback, dsp, export, studio, project, render) | PASS (ladder gate) |
| Export proofs | PASS — 1080p30: 90 frames/3.00 s/2.6 s wall; 4K24: 72 frames at 3840×2160/3.00 s/8.0 s wall; cancel + missing-encoder paths PASS |
| GUI smoke (Xvfb) | PASS — mapped window, 8 s survival, clean SIGTERM exit |
| govulncheck v1.9.0 | PASS — "No vulnerabilities found" after bumping x/net v0.62.0, x/text v0.43.0, x/image v0.45.0 (10 imported + 12 module-level informational findings resolved; the 0-called baseline was preserved throughout) |
| actionlint v1.7.12 (+ label config) + PyYAML parse | PASS — both workflows |
| ci-log-collector on this session's ladder log | PASS — gates=17, failed-gates=0, coverage=COMPLETE, overall=PASS, exit 0 → `evidence/log.txt` |

Not run / blocked (exact reasons):
- **Windows native build/runtime**: NOT RUN — this session had no
  Windows host; the repaired PowerShell path was verified by running
  the REAL `Capture-Step` + gate scripts under pwsh 7.5.4 with stub Go
  commands (child-process scope, exit codes, argv assembly all
  proven). WINDOWS-TARGET-BUILD remains a hosted/Windows-host task.
- **Hosted CI / ubuntu-26.04 image / CodeQL alerts**: NOT RUN /
  BLOCKED — pushing is forbidden; the GitHub API rate-limited
  unauthenticated inspection of run 41915116613, so its artifacts were
  not re-fetched; the source-level diagnosis above is reproducible
  from the prompt's log description plus the code.
- **Hardware GPU execution**: NOT AVAILABLE — no GPU on this host;
  software/noop adapters keep being refused as hardware, as designed.

No test was deleted, weakened or skipped to achieve green. The
baseline suite at the starting commit 50ffe8b passed before any edit
(17 gates), and the final ladder passes after them.

---

## Session 2026-11-06 — CI repair for run 42026322748 (no push made)

- **Mission:** repair every demonstrated workflow/logging failure of GitHub
  Actions run 42026322748 (build-desktop.yml @ fcc81a0d1581817f3263cb8f161291caf147739c),
  restore complete Windows/Linux/CodeQL/log-collector evidence per run,
  deliver the updated source ZIP + note.md. Repository constraint honored:
  NO push, PR, tag, release or settings change was performed.
- **Baseline:** live HEAD == the run's commit (branch had not diverged);
  worktree clean; `$goTags`/Capture-Step preamble repair from run
  41915116613 already present. Reproduction of the primary failure is in
  `evidence/run-42026322748-forensic-analysis.log`.

### Confirmed root causes and fixes

| # | Root cause (run evidence) | Fix |
|---|---|---|
| RC-1 | preflight teed `ci-logs/runner-image.log` BEFORE any mkdir; the mkdir lived in the NEXT step, which was skipped after the failure | `scripts/ci/bootstrap-logs.sh` + `Bootstrap-Logs.ps1` run immediately after checkout in EVERY evidence-producing job |
| RC-2 | upload `ci-logs/**` then found no files (`if-no-files-found: error` cascade) | bootstrap guarantees `bootstrap.log`; uploads now use the directory form `path: ci-logs` |
| RC-3 | `build-windows`/`build-linux` had success-only `needs: preflight` -> both SKIPPED on preflight failure | dependency removed; collector remains the only aggregator (`always() && !cancelled()`); fail-closed step preserved |
| RC-4 | preflight gates skipped after the runner-image step failed | gates step condition `!cancelled()`; runner-image capture hardened (PIPESTATUS, guarded lookups, toolchain identity) |
| RC-5 | no guaranteed bootstrap evidence in build-windows/build-linux/codeql/collect-logs | bootstrap step in all five jobs + `if: failure()` BLOCKED-marker evidence steps |
| RC-6 | artifact names carried no run attempt | attempt-suffixed names everywhere; manifest + cleanup jq pattern updated together |
| RC-7 | one job could be listed twice as failed (manifest + API rows); CodeQL manifest rows didn't match API matrix-leg names | `deriveFailures` deduplicated per stable job id (one entry, BOTH sources; API authoritative; aggregate rows proven successful are RECONCILED, never failures); manifest rows renamed `analyze (go)`/`analyze (actions)` |
| RC-8 | raw-log 404 classes conflated | per-job classification: skipped -> NEVER_STARTED (never fetched), in-progress -> IN_PROGRESS_AT_SNAPSHOT, completed 404 -> API_LOG_UNAVAILABLE, artifact missing + API log -> ARTIFACT_MISSING + RECOVERED_FROM_API |
| RC-9 | collector-binary build failure left the run without log.txt | `scripts/ci/emergency-report.sh` fallback writes/uploads a truthful report; fail-closed still fires (rc forced >= 1) |
| RC-10 | CodeQL vet/build used raw tee (no structured record for a silent build) | both steps wrapped in `scripts/ci/capture.sh` (EXIT_CODE: 0 PASS even with empty stdout) |
| RC-11 | "0 failed / 0 skipped / 0 passed" ambiguity for a failed job that reached no gate | collector reports GATES NOT REACHED explicitly; BLOCKED gate state added (`BLOCKED: gate` lines parse into their own state) |
| RC-12 | codeql caller held `actions: write` it never uses when called | tightened to `actions: read` (the reusable collect job needing write is skipped in that path) |

Also fixed during validation: the prereq install steps' failure counter was
modified inside a pipeline subshell and silently lost (actionlint
SC2030/SC2031) — replaced with a status-file mechanism in both workflows;
`build-windows.ps1` now asserts the UCRT64 target triple + `_UCRT` define
itself (previously only the workflow identity gate did); `build-linux.sh`
records `BLOCKED: gate` for missing prerequisites and runs under
`set -Eeuo pipefail`; `github.run_started_at` feeds the manifest
(`start_time_utc`) — actionlint v1.7.12's expression DB predates it, noted
in `.github/actionlint.yaml` (same DB-vintage class as the ubuntu-26.04
label note).

### Verification record (all local, real commands)

| Gate | Result |
|---|---|
| `scripts/build/build-linux.sh --mode preflight` (set -Eeuo pipefail) | PASS |
| `--mode desktop` full ladder (tests, vet, build, exe, --version/--self-check, race, export proofs, GPU/software-honesty, studio, GUI smoke Xvfb, govulncheck) | PASS — `evidence/phase-build-linux.log` |
| gofmt -l . / go mod tidy -diff / go mod verify | PASS |
| `go test -tags goffi_static ./...` (incl. 9 new cilog classification fixtures) | PASS |
| `go test -race` concurrent cores | PASS |
| `govulncheck ./...` (pinned v1.9.0) | No vulnerabilities found |
| `scripts/ci/tests/test-ci-plumbing.sh` (A1-A6, C14-C16, B7-B13, D22-25 static, E43/E44, F49) | 11 PASS / 0 FAIL / 2 NOT RUN (pwsh absent) |
| `scripts/ci/tests/test-codeql-coverage-inventory.sh` | PASS — run-commit inventory 65 = 40 + 25 (`evidence/codeql-coverage-inventory.txt`) |
| `scripts/ci/tests/Test-CaptureStep.ps1` | NOT RUN — pwsh not installed on this host |
| actionlint v1.7.12 + PyYAML parse + semantic spot checks | PASS (2 known DB-vintage `run_started_at` findings, documented) |
| shellcheck 0.10.0 (all bash CI scripts) | CLEAN |
| Collector e2e on real local logs (skip/failure/determinism injections) | PASS with truthful exit codes 3/0/3 — `evidence/phase-collector.log` |
| actionlint prereq-counter regression (SC2030/SC2031) | found and FIXED in both workflows |

### Not run / blocked (exact reasons)

- **Hosted CI re-run (all phases on GitHub runners)**: NOT RUN — pushing is
  forbidden by the mission; the repaired workflow's first hosted run is the
  proof point for ubuntu-26.04 image availability, Windows UCRT64 build,
  CodeQL scan and the consolidated artifact lifecycle.
- **Windows-target build/runtime, PE validation, portable ZIP**: NOT RUN —
  no Windows host (static UCRT64 regressions verified; see
  `evidence/phase-build-windows.log`).
- **CodeQL diagnostics + open alerts for the analyzed commit**: BLOCKED —
  GitHub REST API rate-limited for this host (unauthenticated 60/hr
  exhausted) and the Actions page is JS-rendered; not invented. The hosted
  collector now retrieves open alerts per run into log.txt section 7.
- **PowerShell runtime fixtures**: NOT RUN — no pwsh on this host; shipped
  for the CI Windows runner.
- **`go list` compiled-file proof inside the inventory fixture**: NOT
  COMPLETED on this host (CGO/pkg-config prerequisites for full package
  resolution are session-local); the git ls-tree inventory is the
  authoritative proof and the limitation is recorded in the fixture output.

### Next handoff

1. Push nothing until the user reviews; the deliverable is the source ZIP +
   note.md beside it.
2. On the next hosted run: expect every independent job to execute even if
   preflight fails; check `log.txt` section 2 (job table with classified
   log sources), 5.B (gate table incl. BLOCKED), 7 (CodeQL status + open
   alerts for the exact commit) and 8 (missing/recovered sources).
3. If `analyze (go)` still scans fewer files than production .go files in a
   future tree, re-run `scripts/ci/tests/test-codeql-coverage-inventory.sh`
   and re-derive the claim before touching CodeQL config.
4. Run `scripts/ci/tests/Test-CaptureStep.ps1` on any pwsh 7 host to close
   the PowerShell fixture gap recorded above.

## Session 2026-11-07 — CI repair for run 42231355353 (no push made)

Evidence run 42231355353 (commit dbedaa455dd3e5704452dc64bd1f56b02fc8e42c,
branch main, push). Live HEAD at session start equaled the evidence commit;
work stayed local. Confirmed root causes, all reproduced locally:

1. **Windows build metadata (commit unknown, built unknown by local)** —
   `build-windows.ps1` built the single `-ldflags` value WITHOUT the `-X `
   flag tokens (`pkg.version=main pkg.commit=...`). The Go tool silently
   ignores such tokens (exit 0), so the binary kept its defaults while the
   build gate stayed green. Reproduced both directions with a minimal
   module through the real Capture-Step chain: no `-X` tokens → defaults
   with exit 0; `-X` per element → full metadata. Linux was unaffected
   because `build-linux.sh` always included `-X` per element.
2. **False-green "Verify UCRT64 toolchain" step** — the workflow piped a
   bare `{ ... }` script-block literal into Tee-Object: PowerShell wrote
   the block's SOURCE TEXT to windows-compiler.log, ran nothing, and the
   step printed PASSED. Reproduced live with pwsh 7.4.6.
3. **Defective `_UCRT` probe** — a no-header `int main(void){return 0;}`
   translation unit only exposes GCC built-ins; mingw-w64 establishes
   `_UCRT` in the CRT header configuration (`_mingw.h`,
   `__MSVCRT_VERSION__ >= 0xE00/0x1400`), so the run-42231355353
   desktop-env-check failure was the probe, not proof of MINGW64.
4. **Gate tag coupling** — Capture-Step injected the `SHEYTAN_GO_TAGS`
   preamble into EVERY generated gate; a missing tag variable blocked
   unrelated gates (reproduced: run-version-style gate blocked).
5. **gofmt gate early exit** — the success path `exit 0` never checked
   gofmt's own `$LASTEXITCODE`; a crashed gofmt (empty output) reported
   "clean".
6. **Collector report reconciliation** — "Declared jobs" mislabeled
   manifest evidence records; the CodeQL note referenced an "API
   ENRICHMENT LIMITATION" section that the writer only emits when an API
   error exists (dangling reference when no client is configured); the
   nested CodeQL rows carried only the caller aggregate; API job names
   never matched the declared "analyze (go)/(actions)" rows.
7. **GPU evidence** — a dx12-reported "Microsoft Basic Render Driver"
   (WARP) adapter with `devtype=integrated-gpu` was accepted as GPU.
   Classification is now property-based (PCI identity 0x1414/0x008c,
   device type, backend) with classes hardware/software/virtual/unknown;
   SelectRenderer refuses everything not proven hardware.

Repairs (files): `scripts/build/build-windows.ps1` (-X contract, enforced
`verify-build-metadata` gate, header-aware probe, gofmt exit handling,
Go-argv + env-presence diagnostics), `scripts/ci/Verify-UCRT64.ps1` (new,
executed helper), `scripts/ci/Capture-Step.ps1` (opt-in `-GoTags`),
`.github/workflows/build-desktop.yml` (helper invocation, manifest kind/
result_basis, Cyrillic homoglyph in the manifest product string fixed,
xvfb+x11-utils prereqs), `internal/cilog/collect.go` (job model labels,
conditional API notes, token-subsequence matching for nested reusable
jobs), `internal/render/{gpu,renderer}.go` + new
`gpu_adapter_class_test.go`, `cmd/sheytan-music-visualizer/main.go`
(self-check prints device class + explicit export-capability semantics),
`scripts/build/build-linux.sh` (GUI smoke: bounded readiness polling,
window-mapping proof via xwininfo, 3 s stay-mapped observation, panic
scan, bounded graceful SIGTERM close; SIGKILL only as recorded cleanup),
`scripts/ci/tests/{Test-CaptureStep.ps1,test-ci-plumbing.sh}` (fixtures
updated + new C22/D24/D25/D26; stale assertions replaced with current
contracts), `internal/cilog/collect_reconcile_test.go` (new), plus
`roadmap.md`/`readme.md` inventory numbers.

Verification matrix (this host: Linux sandbox, go1.27.3, pwsh 7.4.6):

| Check | Result |
|---|---|
| gofmt -l . | PASS (clean) |
| go vet -tags goffi_static ./internal/... | PASS |
| go test -tags goffi_static ./... | 13 packages ok; cmd + ui/fyne BLOCKED (host lacks root: Fyne GL/wayland dev headers cannot be installed) |
| scripts/ci/tests/test-ci-plumbing.sh | PASS (17 fixture groups incl. new D26) |
| scripts/ci/tests/Test-CaptureStep.ps1 | PASS 11/11 (C17-C22, D22-D25, D23b) |
| scripts/ci/tests/test-codeql-coverage-inventory.sh | PASS |
| Metadata E2E through the REAL verify-build-metadata body | PASS both directions (repaired passes; defective rejected with 6 precise errors) |
| Failure injections 1-12 | ALL OK |
| Hosted GitHub Actions re-run | NOT RUN (no push permitted) |
| GitHub API run status/artifacts for 42231355353 | BLOCKED (unauthenticated REST 403 rate-limited on this host) |
| CodeQL raw diagnostics retrieval | BLOCKED (no API access; never invented) |

## Session 2026-11-08 — CI repair for run 42376176493 (no push made)

Evidence run 42376176493 (commit bf50c8e5f6b1866c64a274f610fc3f7a6359d289,
branch main, push, 2026-11-07 13:16-13:19 UTC). Live HEAD at session start
equaled the evidence commit with a clean tree; work stayed local. The
run's user report was provided as the runbook text only (the raw
log42376176493.txt was not attached); run API metadata, raw job logs and
artifacts were NOT RETRIEVED (unauthenticated api.github.com 403
rate-limited on this host). Confirmed root causes, all reproduced locally
from the checked-out source (scripts/repro-manifest-failure.sh output:
ci-logs/repro-run-42376176493/):

1. **Manifest producer failure (primary collector rc=2)** — the manifest
   steps built JSON exclusively with `jq -n ... > ci-parts/manifest.json`.
   The `>` redirect creates the file before jq runs, so with jq
   unavailable the step failed and left a ZERO-BYTE manifest, while
   ci-parts/download-diagnostics.txt was already written (five successful
   downloads recorded — exactly the run's evidence). The collector guard
   `[ -s ci-parts/manifest.json ]` correctly refused (rc=2) and the
   emergency fallback published environment-derived results with coverage
   UNVERIFIED. Repair: scripts/ci/write-manifest.py (stdlib json,
   env-only inputs, temp file + fsync + schema validation + atomic
   rename, diagnostics with allowlisted summary); jq is no longer
   load-bearing in either workflow; a missing python3 fails closed
   instead of leaving an empty manifest.
2. **UCRT64 header-location false negative** — the gates searched
   `_mingw.h` in `gcc -print-file-name=include` (GCC's private internal
   include dir); the mingw-w64-ucrt-x86_64-headers package installs it at
   the UCRT64 TARGET header root <msys2>\ucrt64\include\_mingw.h. A
   healthy UCRT64 installation therefore failed the gate. Repair:
   target-root check derived from the compiler location, private dir
   demoted to a labeled diagnostic, pacman -Q/-Qo package-ownership
   verification, separate compile/link probe with PE MZ validation
   (Verify-UCRT64.ps1 + build-windows.ps1 desktop-env-check).
3. **GUI smoke false positive** — WINDOW_MAPPED=1 was set because the
   title appeared in `xwininfo -root -tree` and "stayed mapped" only
   re-queried the XID, while window-stats.txt said Map State: IsUnMapped.
   Repair: scripts/ci/x11-window-mapstate.sh requires IsViewable
   (case-insensitive: xwininfo prints "IsUnMapped", Xlib says IsUnmapped
   — the run's exact spelling), map state re-queried every poll and
   captured at readiness/after observation/before shutdown, plausible
   geometry required, Xvfb identity from dpkg-query (never the
   unsupported -version option), test-only ALSA null-PCM config scoped to
   the app process, ALSA messages recorded as a structured environment
   limitation (never suppressed; panic/fatal still fails).
4. **Collector observability** — collector stdout/stderr now captured
   verbatim into ci-logs/collector-run.log with the original rc preserved
   (no pipeline masking); the Go binary emits distinct HARD_ERROR
   manifest-missing / manifest-empty / manifest-invalid causes; the
   numeric 0/1/2/3 exit contract is unchanged.

Files: scripts/ci/write-manifest.py (new), scripts/ci/x11-window-mapstate.sh
(new), scripts/ci/tests/Test-UCRT64Fixture.ps1 (new), scripts/ci/Verify-UCRT64.ps1,
scripts/build/build-windows.ps1, scripts/build/build-linux.sh,
scripts/ci/emergency-report.sh, cmd/ci-log-collector/main.go,
.github/workflows/build-desktop.yml, .github/workflows/codeql.yml,
scripts/ci/tests/test-ci-plumbing.sh (D27/G28/G29/H30/H31/H32/D33),
evidence/local-verification-run-42376176493-repair.log.

Verification (this host: Linux sandbox, go1.27.3, pwsh 7.6.9 — Fyne CGO
compiled via a user-space dpkg -x sysroot with the full GL/EGL/X11/
wayland header set; real x11-utils extracted the same way):

| Check | Result |
|---|---|
| gofmt / tidy -diff / mod verify / vet | PASS (clean) |
| go test -tags goffi_static ./... | 13 packages ok (export = real FFmpeg) |
| go test -race (7 core trees) | PASS |
| build-desktop ladder (--mode desktop) | RUN RESULT: PASS incl. corrected GUI smoke on Xvfb: window 0x400020 "SHEYTAN Music Visualizer" Map State: IsViewable at readiness/after observation/before shutdown (1440x860), graceful SIGTERM close rc=0, audio classification clean under the null-PCM config |
| govulncheck v1.9.0 ./... | "No vulnerabilities found." |
| test-ci-plumbing.sh | PASS 20/20 fixtures |
| Test-UCRT64Fixture.ps1 | PASS 10/10 (incl. the run's exact false-negative shape) |
| Test-CaptureStep.ps1 | PASS 11/11 |
| Deterministic manifest/collector repro | PASS (old writer + jq-less PATH -> rc=127 + 0-byte manifest + diagnostics present; guard -> 2; repaired writer -> collector runs, coverage INCOMPLETE named, rc=1 fail-closed) |
| actionlint 1.7.12 | 3 findings, all pre-existing hosted-runner features this workflow already used (concurrency `queue: max`, github.run_started_at x2); linter schema older than the runner features; repaired regions clean |
| YAML duplicate-key parse / shellcheck / pwsh parser | PASS / PASS / PASS; PSScriptAnalyzer NOT AVAILABLE |
| Hosted re-run | NOT RUN (no push permitted) |
| CodeQL open-alert count | NOT RETRIEVED (API 403; collector NOT RETRIEVED semantics preserved) |

# Run-42921967187 repair session (2026-11-11)

## Session identity

Mission: resolve every failure of hosted run 42921967187 (attempt 1, commit
c5161282ee1c91c6fc8346a9acf0e731959496c3), prevent recurrence, harden
diagnostics, keep the Linux build green, keep the Windows GUI contract,
improve measured startup/runtime performance, and re-synchronize docs.
No push/PR/release was made (mission rule 1): hosted verification on the
delivered commit remains a user-triggered step.

<!-- BEGIN HISTORICAL RECORD — run 42921967187 session (superseded artifact/tool claims are historical, see the run-42939743766 section below) -->

## Starting state

Checkout verified at the evidence commit, clean tree, branch main up to
date. Evidence reports 43/46 gates passed; the 3 failures were
build-windows / verify-build-metadata, create-portable-zip,
verify-extracted-package — all on `Join-Path $env:SHEYTAN_REPO_ROOT …`
with a null Path argument, one shared root cause (the variable was never
defined for the spawned gate process; the workflow defined neither it nor
a fallback).

## Root cause reproduction (before any fix)

`evidence/repro-run42921967187-repo-root.{log,json}`: a gate generated by
the REAL `Capture-Step.ps1` with the exact failing import line, executed
by `pwsh -NoProfile -NonInteractive -File` in a clean child without the
variables, reproduced the run's error VERBATIM; the control run with the
variable set imported the helper and exited 0. One root cause, three
gates — confirmed, machine-readable record committed.

## Implemented repair (see note.md / CHANGELOG.md for the full list)

- `scripts/ci/Resolve-RepoRoot.ps1` (new canonical resolver: candidate
  order SHEYTAN_REPO_ROOT → GITHUB_WORKSPACE, GetFullPath normalization,
  repository-identity markers, required-helper validation, precise
  gate-named diagnostics; CWD never consulted).
- `Capture-Step -RequireRepoRoot -RepoRootHelpers` preamble generation;
  generated gates now `Set-StrictMode -Version 3.0`.
- `build-windows.ps1`: parent sets `$env:SHEYTAN_REPO_ROOT = $repoRoot`;
  the three gates declare their helpers and import through `$repoRoot`.
- `build-desktop.yml`: job-level env, recovery-artifact path (independent
  re-validation + `recovery-eligibility.json`), gate-table job summary,
  pinned actionlint/zizmor preflight step.
- `Invoke-CapturedProcess`: Kind/PID/UTC window/elapsed fields; timeout
  kills only its own process tree.
- Per-gate machine-readable `ci-logs/gate-results/<gate>.result.json`
  from BOTH capture helpers (PowerShell + bash).
- codeql.yml: `CODEQL_EXTRACTOR_GO_OPTION_EXTRACT_TESTS='true'` + tracked
  file inventory capture (58/102 scanned previously with tests disabled).
- Dependabot, SECURITY.md, CONTRIBUTING.md; docs/{ci,diagnostics,
  architecture}.md + performance-baseline pass record; CHANGELOG.md;
  readme/roadmap/ai-notes synchronization.
- Performance (measurement-led, output-identical): DSP chunked bins
  backing store (5649→115 allocs/op, 55.71→49.32 MB/op, wall time within
  noise) and glowCircle bounding-box clip (1080p bars −18.7%, layered
  bars −13.0%, 4K waveform −6.0%, radial unchanged). A per-bin energy
  scratch was measured, REGRESSED wall time slightly, and was reverted.

## Verification record (this host: Debian 13 sandbox, go1.27.3, pwsh 7.4.6,
gcc 14.2; Fyne CGO compiled via a user-space dpkg -x GL/EGL/X11/wayland
sysroot — the same approach as the 2026-11-06 session; Xvfb NOT AVAILABLE
without root, so GUI smoke/startup-stage measurement are NOT RUN here)

| Check | Result |
|---|---|
| gofmt / go mod tidy -diff / go mod verify / go vet | PASS (clean) |
| go build -tags goffi_static ./... | PASS |
| go test -tags goffi_static ./... | 21 packages ok (export = real FFmpeg 1080p30/4K24 proofs + cancel + missing-encoder) |
| go test -race (9 trees: audio/playback/dsp/export/studio/project/render/visualizer/ui) | PASS |
| test-package-gates.sh (P1–P7 incl. R1–R8 root-resolution) | PASS 6/6 fixtures, 10/10 sub-fixtures |
| test-ci-plumbing.sh (incl. new D34 gate-record fixture, C17-21, D33) | PASS 21/21 |
| Test-ResolveRepoRoot.ps1 | PASS 10/10 |
| actionlint 1.7.12 | 3 findings = the two documented schema-staleness false positives (queue:, run_started_at ×2) — narrowly ignored in the preflight step with comments |
| zizmor 1.16.2 | 0 low / 0 medium / 0 high (4 informational low-confidence template notes on step outputs) |
| Workflow YAML parse | PASS (both workflows) |
| actionlint/zizmor as executed by the new preflight step | PASS (exact commands verified locally) |
| govulncheck | NOT RUN this session (no network to install pinned v1.9.0 in the sandbox; it remains a required hosted/Linux-ladder gate) |
| Windows UCRT64 build + portable ZIP + recovery upload path execution | BLOCKED locally (no Windows toolchain in this Linux sandbox; definition validated by fixtures) |
| Hosted re-run | NOT RUN (no push permitted) |


<!-- END HISTORICAL RECORD -->

# Session record — run 42939743766 repair (2026-11-11)

## The GPU palette defect: what actually failed and why

`TestPaletteReachesGPUShader` failed identically on hosted Windows and
Linux ("palettes 0 and 1 produced identical GPU frames"). Reproduction on
this host reproduced it byte-for-byte, and empirical instrumentation
proved:

1. The Go packer wrote CORRECT, palette-dependent bytes (bins@48,
   colors@304, size 384 — matching the WGSL struct layout).
2. The rendered frame was EXACTLY the load-op clear color (0.03,0.03,0.03,1)
   for every pixel, for every palette.
3. The wgpu software HAL stores `colorTarget.mask = state.WriteMask` raw;
   the pipeline declared `gputypes.ColorTargetState{Format: ...}` with the
   zero WriteMask — and in the pinned gputypes the zero value IS
   `ColorWriteMaskNone`. Every fragment write was masked out.
4. The general readback test passed VACUOUSLY because `imageUniform`
   compared bytes, and a uniform RGBA clear color has alpha != RGB.

Fixes: explicit `WriteMask: gputypes.ColorWriteMaskAll` in
`internal/render/gpu.go` buildPipeline; pixel-granular `imageUniform`;
the packer formalized into ONE `packUniforms` with the documented layout
table and offset constants; the WGSL shader now reads every packed lane
(peak highlight mirrors the CPU renderer's peak markers); contract tests
pin offsets, lane order, packed bytes and the static WGSL member order.

## Go↔WGSL data contract (the fixed, tested one)

See `docs/GPU-RENDERING.md` — the authoritative table, plus
`TestUniformLayoutMatchesWGSL`, `TestColorLaneOrderMatchesPaletteFields`,
`TestPackUniformsContract`, `TestWGSLParamsContractMatchesGoLayout` and
`TestPaletteReachesGPUShader`. Any change to the WGSL Params struct or the
packer must update all of them together.

## How to reproduce the GPU test

```bash
go test -tags goffi_static -v -run '^TestPaletteReachesGPUShader$' -count=1 ./internal/render/
```

It runs on the project's supported software adapter (ForceFallbackAdapter;
labelled "Software Renderer", never reported as hardware). CI owns it in
the dedicated `gpu-validation` gate, once per platform; the default and
race suites exclude it by exact name (`-skip '^TestPaletteReachesGPUShader$'`).

## zizmor repair

The run-42921967187 preflight installed the then-pinned zizmor release
with `pip` but
audited via `/usr/bin/python3 -m zizmor` — a different environment
("No module named zizmor"), so the audit never executed. The preflight now
installs uv through the SHA-pinned `astral-sh/setup-uv` action (verified
commit fac544c07dec837d0ccb6301d7b5580bf5edae39 = v8.2.0) and runs BOTH
the version check and the audit through ONE isolated environment:
`uvx --from zizmor==1.32.0 zizmor ...`. Verified locally this session:
`zizmor 1.32.0` (PyPI, released 2026-11-02), audit clean at
`--min-severity low --min-confidence low` after adopting GitHub's `$/`
self-repository syntax for the CodeQL reusable call (GA 2026-07;
actionlint 1.7.12 does not parse it yet — upstream issue #711, no newer
verified release — covered by a third exact, documented ignore).
actionlint negative fixture: an invalid fixture workflow is REJECTED with
exit 1 (proven locally).

## One-canonical-package contract

The run-42921967187 recovery artifacts (standalone EXE + staged package)
are retired. Exactly two application artifacts exist (Windows ZIP, Linux
TAR.GZ) plus the plain-text report
`SHEYTAN-Music-Visualizer-all-logs.txt`. Upload decisions come from the
build scripts' `ci-logs/package-status-<os>.json` (VERIFIED / TEST_ONLY /
UNAVAILABLE; commit_expected from `github.sha`, fail-closed). Local
verification this session: `scripts/ci/tests/test-ci-plumbing.sh`
PASS=19 FAIL=0; package-gate bash fixtures PASS; the P/C/D pwsh fixtures
are NOT RUN here (no pwsh) and stay owned by the preflight job.

## Environment boundary of this session

Linux x64 sandbox, go1.27.3. All non-GUI packages verified; `ui/fyne` and
`cmd/sheytan-music-visualizer` are environment-BLOCKED locally (no root to
install GL/wayland dev headers) and are owned by hosted CI. No physical
GPU/audio/display hardware exists here: every hardware claim stays
software-adapter evidence.

## Run 43541087097 session record (2026-11-14)

Scope: full repair, performance, visualizer-quality and UX pass per the
113-minute implementation contract. Environment: Linux x64 sandbox,
go1.27.4, locally provisioned GL/EGL/wayland dev libraries (user-prefix
pkg-config), FFmpeg 7.1.5 present, Xvfb available; 2 vCPU, no physical
GPU/audio/display.

Key engineering facts established this session (full detail in
`CHANGELOG.md` and the delivered `note.md`):

- The Windows `TestSingleConfirmationSurfaceForDuplicates` failure is a
  timing-contract portability defect: a sub-resolution monotonic
  teardown measured as a zero span. Fixed by the documented
  `appclose.MinCompletedDuration` floor + an injectable clock; the
  fixed-clock test reproduces the Windows condition deterministically on
  Linux. The BASELINE binary also logged `duration_ms=0` on a Linux
  shutdown — the defect was not Windows-only.
- appclose now uses generation-validated, once-only proceed callbacks
  and single-lock-hold admission (RequestClose → snapshot → confirming/
  cancelling), eliminating the stale-proceed-after-Stay and
  no-work-vs-confirming teardown races.
- Three critical audio decode defects fixed and regression-proven:
  chunk-boundary sample drops (frameAlignReader), the FLAC bit-depth
  mapping (fixed 24-bit conversion wire; FLAC 8-bit is SIGNED, unlike
  unsigned WAV 8-bit), and the 8-bit WAV DC offset. Plus WAV format-tag
  validation, a bounded fmt-chunk read, NaN/Inf wire sanitization, the
  duration limit enforced during the decode, an FFmpeg decode-cancel
  watcher, and the oto async-error honesty fix.
- studio.Close() is now a complete dependency-ordered teardown (import
  → analysis → export → transport, idempotent); analysis gained the
  cancellation path it never had.
- Render parity: SpectrumTilt applied on the normal path; the GPU
  radial path consumes scene-smoothed bins and composites overlays (GPU
  exports previously lost the title/watermark); export GPU selection
  requires a hardware adapter class; identical-timestamp re-render is
  bit-stable on both backends; an unclamped config can no longer zero
  audio reactivity.
- Hot-path A/B (this host, 5 runs, medians): 1080p overlay frame 41 → 6
  allocs/op and 35,784 → 169 B/op (−99.5% churn); the preview loop
  rotates three frame buffers instead of allocating ~2 MiB per frame.
  Startup first-window-usable medians: baseline 887/881 ms (cold/warm)
  vs 883/875 ms after — parity within noise on this host.
- AnalysisVersion bumped to 2 (waveform envelope 2 → 60 pts/s).
- CI: collect-logs concludes on collection health only; a new
  final-verdict job fails closed on any required gate; the Linux
  --build-info-json observer receives the real exit code; the stale
  CodeQL-inventory production count corrected (run2 + 9).
- startup-bench.sh accepts absolute bin paths (an absolute path
  previously passed the -x check but launched a nonexistent file, so
  every sample FAILed).
- Docs: BUILDING.md merged into ci.md; diagnostics.md merged into
  TROUBLESHOOTING.md; the docs-consistency gate is green.
- The tracked `.env` and `download/README.md` are initial-commit
  sandbox artifacts, not product files; they are excluded from the
  delivered source ZIP and left untouched in the tree.
