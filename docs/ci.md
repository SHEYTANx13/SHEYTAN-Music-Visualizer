# CI and build documentation

This page is the canonical reference for building the product locally and
for understanding what every workflow gate proves. `readme.md` links here
for the details; it does not duplicate them.

## Build requirements

| Platform | Toolchain | Required build tag | Notes |
| --- | --- | --- | --- |
| Windows x64 | MSYS2 **UCRT64** (`mingw-w64-ucrt-x86_64-gcc`, `mingw-w64-ucrt-x86_64-pkgconf`), PowerShell 7+ | `-tags goffi_static` | `CGO_ENABLED=1 GOOS=windows GOARCH=amd64`. Never MINGW64; the UCRT64 identity gate (`scripts/ci/Verify-UCRT64.ps1`) fails the run on any other toolchain. |
| Linux x64 | Go toolchain from `go.mod`, `gcc`, `pkg-config`, Fyne native libraries (`libgl1-mesa-dev libegl1-mesa-dev libx11-dev libxcursor-dev libxrandr-dev libxinerama-dev libxi-dev libxxf86vm-dev libwayland-dev libxkbcommon-dev`), `xvfb`+`x11-utils` for the GUI smoke | `-tags goffi_static` | Hosted runner label is the explicit `ubuntu-26.04` (registered in `.github/actionlint.yaml`). |
| FFmpeg | `ffmpeg`/`ffprobe` 6+ (optional, external) | — | Required only for MP4 export tests. Never bundled; see `packaging/FFMPEG-TOOLS-LICENSING.md`. |

`goffi_static` is required whenever Fyne (CGO) and wgpu (goffi FFI) link
into one binary; the build scripts set it via the single documented source
`$env:SHEYTAN_GO_TAGS` / its bash counterpart.

## The build scripts and their gates

- `scripts/build/build-windows.ps1` — UCRT64 identity, gofmt,
  `go mod tidy -diff`, `go mod verify`, tests, vet, package build,
  executable build (`-H=windowsgui` + metadata `-X` flags), `--version`,
  `--self-check`, metadata verification, portable staging, staged-license
  byte identity, PE subsystem = 2, portable ZIP, fresh-extraction
  verification. Independent gates keep running after a failure; dependent
  gates are recorded SKIPPED, never passed.
- `scripts/build/build-linux.sh` — the Linux ladder with the same
  capture discipline, plus race tests, FFmpeg export proofs and the Xvfb
  GUI smoke.

Every executed command produces:

1. a structured text record in `ci-logs/phase2-build.log`
   (JOB/STEP/STARTED/WORKDIR/COMMAND/TOOL_CONTEXT/EXIT_CODE/ENDED), and
2. **one machine-readable record**
   `ci-logs/gate-results/<gate>.result.json` (command vector, working
   directory, UTC window, exit status, verdict, `executed` flag), written
   by both capture helpers (`scripts/ci/Capture-Step.ps1` and
   `scripts/ci/capture.sh`).

A gate with no record was never executed — absence is not a pass. On a
failure, inspect the matching `.result.json` first (it survives later
step failures), then the phase log for the captured output.

## Repository-root resolution contract (run-42921967187 repair)

That run failed three Windows gates (verify-build-metadata,
create-portable-zip, verify-extracted-package) on one shared root cause:
generated gate scripts dot-sourced helpers through
`$env:SHEYTAN_REPO_ROOT`, which was never defined for the spawned gate
process (`Cannot bind argument to parameter 'Path' because it is null`).

The contract now has three independent layers:

1. **Workflow job env** — `build-desktop.yml` defines
   `SHEYTAN_REPO_ROOT: ${{ github.workspace }}` at the build-windows job
   scope, so every step and child process inherits it.
2. **Parent script** — `build-windows.ps1` sets
   `$env:SHEYTAN_REPO_ROOT = $repoRoot` (derived from its own script
   location, never the CWD) so local runs behave identically.
3. **Generated-gate preamble** — gates that import helpers declare
   `-RequireRepoRoot -RepoRootHelpers @("scripts/ci/…")`; the generated
   script resolves the root from `SHEYTAN_REPO_ROOT` with the standard
   `GITHUB_WORKSPACE` fallback via the canonical
   `scripts/ci/Resolve-RepoRoot.ps1`, validates repository identity
   markers and every required helper file BEFORE the gate body runs, and
   fails with a precise, gate-named diagnostic if anything is missing.

The current working directory is never consulted as the root (gate
scripts run under `ci-logs/step-scripts/`). Regression fixtures live in
`scripts/ci/tests/Test-ResolveRepoRoot.ps1` (10 subprocess scenarios R1–R8)
and run as fixture P7 of `scripts/ci/tests/test-package-gates.sh`.

## Artifacts: final vs recovery

| Artifact | Name | Condition | Meaning |
| --- | --- | --- | --- |
| Final portable ZIP | `windows-x64-portable-build` | `success()` only — every gate incl. extraction verification passed | The tested, released-quality package (single app-directory root, license byte-identical, extracted EXE re-run). |
| Recovery executable | `windows-x64-recovery-executable` | `always()`, uploaded only when the EXE independently re-proves: non-empty, PE subsystem 2, expected commit, `--self-check` pass | Single unzipped EXE so a packaging-only failure still gives the user a testable binary. Test-only; never a release. |
| Recovery staged package | `windows-x64-recovery-staged-package` | `always()`, when the staged app folder exists | The staged folder as-is. Test-only. |
| Consolidated report | `log.txt` | `always()` after evidence jobs | One plain-text file uploaded unzipped (`archive: false`); validated (non-empty, no ZIP signature) before upload. |

Recovery eligibility is decided from fresh evidence only and recorded in
`ci-logs/recovery-eligibility.json`. A recovery upload never turns a
failed required gate green: the job conclusion comes from the build step.

## Workflow security posture

- Every third-party action pinned to a full 40-character SHA;
  `persist-credentials: false` on checkout.
- Least-privilege job permissions (build jobs: read-only; CodeQL:
  `security-events: write` only; collector: `actions: write` solely for
  temporary-artifact cleanup).
- Preflight runs **pinned** static validators: `actionlint` v1.7.12
  (checksum-verified) and `zizmor` 1.16.2, failing the run on any finding
  at low severity or above. Two narrowly-scoped actionlint false
  positives (documented schema staleness for `queue:` and
  `github.run_started_at`, both proven by a real hosted run) are ignored
  by exact message.
- `govulncheck ./...` remains a required Linux gate.
- `.github/dependabot.yml` tracks Go modules and GitHub Actions weekly.
- CodeQL analyzes Go with
  `CODEQL_EXTRACTOR_GO_OPTION_EXTRACT_TESTS=true` (test files included;
  run 42921967187 scanned 58/102 files with it disabled) plus the Actions
  language, keeping the default query suite.

## Reproducing a failed gate locally

```bash
# Linux full ladder (same gates as CI)
bash scripts/build/build-linux.sh --mode desktop
# Windows full ladder (PowerShell 7+, UCRT64 on PATH)
./scripts/build/build-windows.ps1
# Gate machinery fixtures (no GitHub credentials needed)
bash scripts/ci/tests/test-package-gates.sh    # P1–P7 incl. repo-root fixtures
bash scripts/ci/tests/test-ci-plumbing.sh      # bootstrap/capture/DAG/manifest fixtures
pwsh scripts/ci/tests/Test-ResolveRepoRoot.ps1 # root-resolution scenarios
pwsh scripts/ci/tests/Test-CaptureStep.ps1     # capture fixtures
```

A local pass is local evidence. Hosted GitHub Actions verification on the
delivered commit is a separate, user-triggered step — never claim a
hosted run is green without its run page.
