# CHANGELOG — SHEYTAN Music Visualizer

All notable changes to the application and its engineering machinery.
The canonical licensing document is `license.md`; this changelog is
informational only.

<!-- END HISTORICAL RECORD -->

## 2026-11-14 — run 43541087097 repair, performance and visualizer quality

- **Windows close regression fixed at the contract level.**
  `TestSingleConfirmationSurfaceForDuplicates` failed on Windows
  (`duration not recorded`) because a sub-resolution teardown measured
  as a zero span on the coarse monotonic clock. `internal/appclose`
  now normalizes every completed shutdown to the documented
  `MinCompletedDuration` floor (no sleeps), records a non-zero
  `CompletedAt`, publishes the immutable result BEFORE `Done` closes,
  and takes its timestamps from an injectable clock — the fixed-clock
  test reproduces the exact Windows condition deterministically on
  every platform (100/100 green, race-clean).
- **Confirmation callbacks are generation-validated and once-only.**
  A stale `proceed()` captured by a cancelled confirmation can never
  close the app after the user chose Stay (generation tokens bumped by
  `CancelClose` and by every new confirmation); double-invoking
  `proceed()` runs the teardown exactly once; close-request admission,
  the work snapshot and the confirming/cancelling transition happen
  under one lock hold, so a concurrent no-work close can no longer
  start teardown while another caller's dialog is opening. New
  concurrency tests: proceed-vs-cancel race (50 iterations),
  simultaneous close surfaces, stale-proceed-after-stay, result
  visibility on every completion path.
- **Three critical audio decode defects fixed (all reproduced by new
  regression tests).** (1) Partial sample frames were silently dropped
  at every 64 KiB chunk boundary whenever the frame size did not
  divide 64 KiB — 3/5/6/7-channel sources (e.g. 5.1 M4A) degraded into
  garbage from the first boundary; a frame-aligning reader now carries
  partial frames between reads. (2) The FLAC bit-depth mapping was
  wrong for every depth outside {16,24,32} — 8-bit FLAC decoded as
  pure noise at the wrong duration; the decoder now converts every
  depth to a fixed 24-bit wire. (3) 8-bit WAV decoded with a +1.0 DC
  offset (unsigned samples read as signed); digital silence imported
  as a full-scale DC signal. Also: WAV format tags are validated
  (compressed ADPCM/µ-law files are rejected instead of decoded as
  PCM noise), a hostile 4 GiB `fmt` chunk can no longer trigger a
  giant allocation, NaN/±Inf float32 samples are sanitized at the
  wire, the 2-hour duration limit is enforced DURING the decode, and a
  cancelled FFmpeg decode now unblocks a stuck read (context watcher
  kills the child, which closes the pipe).
- **Honest audio-device fallback restored.** oto v3's asynchronous
  context error is checked after the ready signal, so a machine with
  no audio device gets the documented silent-preview fallback instead
  of a "real" device that plays nothing.
- **Shutdown teardown is complete and dependency-ordered.**
  `studio.Close()` now cancels the running import, the running
  analysis (previously uncancellable), the export job (reaping the
  FFmpeg child) and the transport — idempotently; the appclose hooks
  read the export job from the synchronized studio service instead of
  the dialog-scoped UI field; closing the export dialog no longer
  abandons a running export (the Cancel control stays reachable and
  the terminal events clear the reference).
- **SpectrumTilt actually renders.** The configured perceptual tilt
  (presets, Song DNA defaults, projects) silently never affected the
  picture because the scene only applied it when the input spectrum
  was shorter than 64 bins; it is now applied on the normal path with
  the same formula in preview and export.
- **Waveform mode gets a real trace.** The analysis envelope
  resolution rose from 2 to 60 points/second (8 → 240 points in the
  ±2 s window; AnalysisVersion bumped to 2).
- **GPU/CPU render parity.** The GPU radial path now (a) consumes the
  same attack/release-smoothed scene bins as the CPU path (previously
  raw bins — visibly jittery), (b) composites the title/subtitle/
  watermark overlays the CPU path always drew (GPU exports no longer
  lack the configured overlays), and (c) export renderer selection
  requires a hardware-class adapter exactly like the UI selection
  (WARP can no longer render exports as "GPU").
- **Renderer hardening + hot-path repair.** An unclamped caller config
  (zero Sensitivity) no longer collapses the audio-reactive targets —
  the config is normalized once, in place, before the scene update;
  re-rendering an identical timestamp is bit-stable on both backends
  (a paused preview no longer animates by itself) and a backward seek
  restarts the deterministic evolution. Overlay text faces are cached
  per pixel size (A/B on this repository's benchmark host: 41 → 6
  allocations and 35,784 → 169 bytes per 1080p frame with overlays,
  −99.5% churn) and the preview loop rotates three frame buffers
  instead of allocating ~2 MiB per frame.
- **Export environment honesty.** A failed `DetectEnv` is never cached
  (the second call used to return an empty environment with a nil
  error, silently skipping the "Export requires FFmpeg" dialog);
  callers receive deep copies so encoder proofing cannot race the
  process cache, and proofs accumulate in the cache across dialogs.
- **CI verdict separation.** `collect-logs` now concludes on
  collection health only (a green collector always means the
  consolidated report WAS uploaded); the new `final-verdict` job fails
  closed when any required gate (preflight, build-windows,
  build-linux, codeql, collect-logs) did not succeed. The Linux
  package observer now receives the real `--build-info-json` exit code
  (it was hardcoded to 0, so the "non-zero exit never verified" rule
  was not enforced on Linux).
- **Docs consolidated.** `docs/BUILDING.md` merged into `docs/ci.md`
  (one canonical build/CI reference, internal duplicate artifact table
  removed) and `docs/diagnostics.md` merged into
  `docs/TROUBLESHOOTING.md`; index and cross-links updated; the
  documentation-consistency gate passes.

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
