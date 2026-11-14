# CHANGELOG — SHEYTAN Music Visualizer

All notable changes to the application and its engineering machinery.
The canonical licensing document is `license.md`; this changelog is
informational only.

<!-- END HISTORICAL RECORD -->

## 2026-11-13 — run 43090342995 repair

- **Playback clock contract fixed at its source.** The Windows
  `TestSilentTransportClockAndSeek` failure (`position advanced while
  paused: 580ms → 590ms`) was a real state race: the playhead was
  recomputed from live device counters even when paused, and the silent
  pacer could deliver one in-flight tick after `Pause()` returned. The
  transport now owns one epoch triple (`basePos/baseCount/baseBuf`)
  captured atomically at every transition; a paused/stopped transport
  returns the frozen epoch base and ignores every late tick; seeks are
  frame-aligned; the silent pacer runs one goroutine per play session
  and exits on pause (no idle ticker accumulation). A deterministic
  test suite (`transport_clock_test.go`) drives device consumption
  synchronously — pending-tick, pause-before/after-tick, seek-while-
  paused, 60 repeated cycles, buffered arithmetic, zero-duration,
  end-of-track, close idempotence and goroutine-leak checks — with the
  original test text unchanged.
- **One authoritative shutdown path.** New `internal/appclose` state
  machine behind the titlebar close intercept and a File→Quit menu:
  duplicate close events never duplicate dialogs or teardown; active
  playback/export/import get exactly one confirmation; cancel→flush→
  release stages are bounded and evidence-recorded; the window closes
  explicitly once after cleanup.
- **Responsive studio.** Breakpoint recomposition of shared regions
  (phone <480 px single column with touch-sized action grid; compact
  split; desktop workspace; ultrawide content capped at 1680 px and
  centered), long-title truncation, phone-safe dialogs, and a
  23-viewport layout-contract test suite (no overlap/overflow, actions
  reachable, state preserved across breakpoint transitions).
- **First-frame deferral.** The renderer probe and the audio-device
  open no longer block the first frame (truthful "probing…"/not-yet-
  initialized states instead); the idle preview repaints at ~6 fps.
- **Machine-readable build identity.** `--build-info-json` prints one
  stable JSON line; the package-status observers (PowerShell +
  bash/python) parse typed JSON from the built AND the extracted
  executable — the false `commit_observed: not-observed` class is
  structurally impossible now. Package-status semantics enforced:
  commit-identity failure is UNAVAILABLE (never TEST_ONLY), integrity
  gates run even when unrelated tests already failed (valid TEST_ONLY
  packages are no longer withheld).
- **Root-resolver contract.** R8 now pins the real 4-gate
  `-RequireRepoRoot` contract with exact helper call sites; the
  generated-gate diagnostic prints the actual resolved root (the
  literal `$repoRoot` interpolation bug is fixed); new R9 and P9/P10
  fixture blocks.
- **Workflow hygiene.** The ShellCheck SC2129 finding at
  `build-desktop.yml:702` fixed by grouping the step-summary appends
  (order and append semantics preserved); zizmor runs online with the
  workflow's scoped token; the documented actionlint ignores and the
  negative fixture are unchanged.

## 2026-11-11 — run 42939743766 repair

- **GPU: palettes now provably reach the shader.** The fragment color
  target declared no `WriteMask`; in the pinned gputypes the zero value is
  `ColorWriteMaskNone`, so every fragment write was silently dropped and
  all GPU frames stayed at the clear color (identical for every palette).
  Fixed with an explicit `ColorWriteMaskAll` and a formalized Go↔WGSL
  uniform contract: one `packUniforms` encoder, documented offset table,
  16-byte vec4 color lanes (R,G,B,A), a WGSL peak-highlight lane read and
  five contract tests pinning offsets, lane order, packed bytes, WGSL
  member order and the end-to-end palette difference
  (`TestPaletteReachesGPUShader` now passes).
- **GPU test ownership.** `TestPaletteReachesGPUShader` runs exactly once
  per platform in the dedicated `gpu-validation` gate; the default and
  race suites exclude it by exact name only. No other test was removed.
- **CI security tooling.** zizmor upgraded to pinned 1.32.0 executed
  through ONE isolated `uvx` environment (version-verified before the
  audit; the previous `pip`-install vs `/usr/bin/python3` interpreter
  mismatch cannot recur). actionlint negative fixture added; the `$/`
  self-repository reusable-workflow syntax adopted (zizmor requirement);
  a third narrowly-documented actionlint ignore covers actionlint 1.7.12
  not knowing that syntax yet (upstream issue #711).
- **One canonical build per OS.** The recovery-EXE / staged-package
  artifacts are retired. GitHub Actions produces exactly
  `SHEYTAN-Music-Visualizer-windows-x64.zip`,
  `SHEYTAN-Music-Visualizer-linux-x64.tar.gz` and the plain-text
  `SHEYTAN-Music-Visualizer-all-logs.txt`. Package uploads are driven by
  machine-readable package-status records (VERIFIED / TEST_ONLY /
  UNAVAILABLE) with fail-closed commit verification against `github.sha`;
  valid packages of red runs are labeled TEST-ONLY and the workflow stays
  red.
- **Diagnostics.** Per-gate JSON records gained run/attempt/commit/
  platform/elapsed fields; the consolidated report gained a root-cause
  grouping (exact-signature membership, unclassified failures listed
  honestly) and environment-capability limitation statements; a path-with-
  spaces extracted-package runtime check was added on both platforms.
- **Toolchain.** `go.mod` toolchain bumped go1.27.3 → go1.27.4 to clear
  GO-2026-6859 (net/http MaxBytesReader header race, fixed in 1.27.4);
  `govulncheck` now reports "No vulnerabilities found." on the scanned
  packages. The runners provision the version from `go.mod`
  (`actions/setup-go` `go-version-file`), so no workflow change is needed.
- **Docs.** New `docs/BUILDING.md`, `docs/GPU-RENDERING.md`,
  `docs/CI-DIAGNOSTICS.md`, `docs/PERFORMANCE.md`, `docs/TROUBLESHOOTING.md`
  and `docs/REQUIREMENTS-TRACEABILITY.md`; a deterministic docs-consistency
  gate validates artifact names and local links.


<!-- BEGIN HISTORICAL RECORD — run-42921967187 session (superseded artifact/tool
claims are historical; the current contract is the run-42939743766 entry below) -->

## [Unreleased] — run-42921967187 repair session (2026-11-11)

### Fixed

- **CI: shared Windows gate root cause.** The three gates that failed in
  run 42921967187 (`verify-build-metadata`, `create-portable-zip`,
  `verify-extracted-package` — all on `Cannot bind argument to parameter
  'Path' because it is null`) shared one root cause: the generated gate
  scripts referenced `$env:SHEYTAN_REPO_ROOT`, which was never defined
  for the spawned gate process. Fixed at the workflow/gate-generation
  boundary: job-level `SHEYTAN_REPO_ROOT: ${{ github.workspace }}` in
  `build-desktop.yml`, a parent-side assignment in `build-windows.ps1`,
  a new canonical resolver `scripts/ci/Resolve-RepoRoot.ps1`
  (`SHEYTAN_REPO_ROOT` → standard `GITHUB_WORKSPACE` fallback → precise
  gate-named diagnostic; the current working directory is never used),
  and a generated preamble (`Capture-Step -RequireRepoRoot
  -RepoRootHelpers …`) that validates repository identity and every
  required helper before any package output is touched.
- **CI: generated gate scripts** now use `Set-StrictMode -Version 3.0`
  (deterministic; `Latest` changes meaning as PowerShell evolves).
- **CI: Windows artifact availability.** A guaranteed recovery path runs
  `if: always()` after the Windows build step: an independently
  re-validated EXE is uploaded as `windows-x64-recovery-executable`
  (single unzipped file) and the staged app folder as
  `windows-x64-recovery-staged-package` when the final ZIP gate fails —
  so a valid binary can never again be lost to a packaging-only defect
  (run 42921967187's exact user-facing symptom). Eligibility is decided
  from fresh evidence only and recorded in
  `ci-logs/recovery-eligibility.json`; a recovery upload never turns a
  failed required gate green.
- **Diagnostics: machine-readable gate records.** Every executed gate
  now also writes `ci-logs/gate-results/<gate>.result.json` (command
  vector, working directory, UTC window, exit status, verdict, executed
  flag) from both capture helpers; every non-executed gate has no
  record — absence is not a pass.
- **Process helper observations**: `Invoke-CapturedProcess` records
  Started/Ended UTC, elapsed time, PID and an outcome kind
  (`exited`/`timeout`/`start-failed`/`invalid-argument`), and on timeout
  kills only the exact process tree it created.
- **CodeQL coverage**: Go analysis now sets
  `CODEQL_EXTRACTOR_GO_OPTION_EXTRACT_TESTS=true` (run 42921967187
  scanned 58/102 tracked Go files with test extraction disabled); the
  source inventory step additionally captures the exact tracked-file
  list so remaining coverage deltas can be reconciled file-by-file.
- **Application startup logging**: the first runtime-log record now
  carries version, commit, builder, build date, data mode, data root and
  log directory (run-42921967187 logging contract; previously a Windows
  runtime log could be attributed to nothing).

### Added

- Pinned static workflow validation in preflight: `actionlint` v1.7.12
  (checksum-verified) and `zizmor` 1.16.2, failing on findings at low
  severity or above; two exactly-scoped schema-staleness false positives
  are ignored with comments.
- `.github/dependabot.yml` (Go modules + GitHub Actions, weekly),
  `.github/SECURITY.md` (private vulnerability reporting) and
  `.github/CONTRIBUTING.md`.
- `docs/ci.md` (build requirements, gate evidence contract, repo-root
  contract, artifact/recovery distinction, local reproduction),
  `docs/diagnostics.md` (log layout, startup metrics, capability state
  model) and `docs/architecture.md` (package map, startup sequence,
  threading contracts).
- Regression fixtures: `scripts/ci/tests/Test-ResolveRepoRoot.ps1`
  (10 subprocess scenarios) wired as fixture P7 of
  `scripts/ci/tests/test-package-gates.sh`, plus plumbing fixture D34
  for the gate-result records.
- A build-windows job-summary gate table (PASS/FAIL/BLOCKED/NOT RUN per
  gate) on the run page.

### Changed

- **Performance (measurement-led, output-identical):**
  - DSP analysis allocates 98% fewer objects per minute of audio
    (5 649 → 115 allocs/op; 55.71 → 49.32 MB/op) via a chunked display-
    spectrum backing store; wall time unchanged within noise.
  - CPU renderer: glow bounding-box clipping makes 1080p bar frames
    ~18.7% faster, layered bars ~13% faster, 4K waveform ~6% faster;
    the radial mode (centered disc) is unchanged, confirming no
    regression. Details and method: `docs/performance-baseline.md`.
- readme.md feature table and logs section updated to the new
  documentation set and evidence paths.

### Known platform limitations (unchanged, honestly stated)

- Windows: FFmpeg is NOT bundled. MP4 export stays NOT PROVEN on clean
  Windows installs until a real local FFmpeg is provided and an export
  is ffprobe-validated there; the UI and self-check report the absence
  explicitly.
- Physical-GPU acceleration claims require a VERIFIED state (real
  pipeline + readback); software adapters (e.g. Microsoft Basic Render
  Driver) are always labeled software/virtual.
- Hosted GitHub Actions evidence for this source update is pending a
  user-triggered run on the delivered commit (no push was made by the
  repair session).
