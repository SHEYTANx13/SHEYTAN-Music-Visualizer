# CHANGELOG — SHEYTAN Music Visualizer

All notable changes to the application and its engineering machinery.
The canonical licensing document is `license.md`; this changelog is
informational only.

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
