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

## Build identity: `--build-info-json` is the commit authority (run 43090342995)

The desktop binary prints one stable JSON line with `--build-info-json`
(`version`, `commit`, `date`, `built_by`, `go_version`, `os`, `arch`;
`internal/buildinfo.JSON()`). The package-status observers parse THIS
output with typed JSON decoders — never regexes over human `--version`
text:

- `scripts/ci/Get-PackageCommitStatus.ps1` (PowerShell side, used by
  `build-windows.ps1` for the built AND the extracted executable),
- `scripts/ci/observe_commit.py` (bash side, used by `build-linux.sh`).

Verification rules (identical in both, proven by the P9/P10 fixture
matrix): a non-zero exit is never verified; empty/malformed output,
a missing/empty commit field and the literal `unknown` default are never
verified; a non-blank expected commit must equal the observed commit
exactly (case-sensitive); trailing LF/CRLF/whitespace is tolerated by
the decoder. Human `--version` output remains user-facing only.

### Package-status contract

| Status | Meaning | Upload |
|---|---|---|
| `VERIFIED` | every package-integrity gate passed AND the expected commit (immutable `github.sha`) matches the embedded metadata of the built AND extracted executable AND no unrelated gate failed | yes |
| `TEST_ONLY` | package integrity AND commit identity fully passed, but an unrelated quality gate failed — the job/workflow stay red and the summary labels the artifact TEST-ONLY | yes |
| `UNAVAILABLE` | no valid package, unverified embedded commit, or any failed package-integrity gate | never |

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

### Gate ownership (do not duplicate work)

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

## Artifacts: one canonical package per OS (run 42939743766 contract)

The run-42921967187 recovery-artifact path (standalone EXE + staged-folder
package) is RETIRED. There is exactly one Windows and one Linux
application artifact, plus the single consolidated diagnostics report:

| Artifact | Produced by | Condition | Notes |
|---|---|---|---|
| `SHEYTAN-Music-Visualizer-windows-x64.zip` | build-windows | package gates green (VERIFIED) or non-package gates red while package gates green (TEST-ONLY) | one app dir `SHEYTAN-Music-Visualizer-windows-x64/`; PE GUI subsystem 2; license byte-identical; extracted EXE re-run |
| `SHEYTAN-Music-Visualizer-linux-x64.tar.gz` | build-linux | same VERIFIED/TEST_ONLY rule | one app dir `SHEYTAN-Music-Visualizer-linux-x64/`; exec bit preserved |
| `SHEYTAN-Music-Visualizer-all-logs.txt` | collect-logs | always (`if: always()`) | plain text, direct upload (`archive: false`), no ZIP wrapper |

`package_status` (VERIFIED / TEST_ONLY / UNAVAILABLE) is written by the
build scripts to `ci-logs/package-status-<os>.json`; `commit_expected`
comes from the immutable `github.sha` context and must exactly match the
embedded build metadata or the package is never marked VERIFIED. A package
that fails its own checks is never uploaded. Per-job log-part artifacts
are internal collector inputs, deleted only after the consolidated report
upload is verified.

There is no raw EXE, no staged-folder archive and no "recovery" variant
anywhere in the artifact list (the run-42921967187 recovery path was
retired in the run-42939743766 repair). Package upload decisions come from
`ci-logs/package-status-windows.json` / `package-status-linux.json`, never
from file existence alone.

## Job verdicts and the final gate (run 43541087097)

The workflow separates EVIDENCE COLLECTION from the OVERALL VERDICT:

- `collect-logs` concludes on COLLECTION health only: green when the
  consolidated report was produced, validated and uploaded (collector
  exit 0, or exit 3 — report uploaded while an upstream required job
  failed); red when the collection itself is incomplete (exit 1) or the
  collector hard-failed (exit 2) or the emergency fallback/cleanup
  guarantees were not met.
- `final-verdict` (needs preflight, build-windows, build-linux, codeql,
  collect-logs; `always() && !cancelled()`) is the ONE user-facing gate
  that fails closed when any required job did not succeed, naming each
  failed job and pointing at the consolidated report. An upstream test
  failure therefore shows as: the failing build job red, collect-logs
green (the evidence was collected), final-verdict red — never as a
seemingly broken collector.

## Workflow security posture

- Every third-party action pinned to a full 40-character SHA;
  `persist-credentials: false` on checkout.
- Least-privilege job permissions (build jobs: read-only; CodeQL:
  `security-events: write` only; collector: `actions: write` solely for
  temporary-artifact cleanup).
- Preflight runs **pinned** static validators: `actionlint` v1.7.12
  (checksum-verified) and `zizmor` 1.32.0 — installed through the
  SHA-pinned `astral-sh/setup-uv` action and audited in ONE isolated
  `uvx --from zizmor==1.32.0` environment that is verified for version
  before the audit (the run-42939743766 interpreter-mismatch defect is
  structurally impossible now), failing the run on any finding at low
  severity or above. THREE narrowly-scoped actionlint compatibility
  ignores (documented schema staleness for `queue:`, the documented
  `github.run_started_at` context property, and the `$/` self-repository
  reusable-workflow call that zizmor requires but actionlint 1.7.12 does
  not parse yet — upstream issue #711, no newer verified release) are
  ignored by exact message, each with its official reference next to it.
  A negative fixture proves actionlint rejects a deliberately invalid
  workflow, so a broken install can never masquerade as a clean scan.
  Online zizmor audits are disabled (deterministic local audits only) —
  a documented limitation, not a suppressed finding class.
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
