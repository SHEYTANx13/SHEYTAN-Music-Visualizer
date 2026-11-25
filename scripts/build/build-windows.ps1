#!/usr/bin/env pwsh
<#
.SYNOPSIS
    build-windows.ps1 — Phase 2 local build/verification entry point
    (Windows x64, MSYS2 UCRT64 toolchain).

.DESCRIPTION
    Runs the Phase 2 verification gates (studio build, export validation, GPU proof) for the Windows desktop build:
    toolchain report, UCRT64 environment identity, gofmt, tidy, tests, vet,
    package build, the native Windows x64 executable, --version/--self-check
    runtime checks with ENFORCED build-metadata verification, and
    portable-folder staging with a ZIP validation.

    COMPACT PACKAGE CONTRACT (run 43712071111): the package ships NO
    media-tool binaries. FFmpeg/ffprobe are app-managed downloads (the
    in-app Media Tools & Codecs manager installs the pinned pair under
    <DataRoot>\media-tools\current\bin). CI enforces this negatively:
    forbid-bundled-media-tools rejects any archive that carries
    ffmpeg/ffprobe, package-size-budget caps the ZIP at 90 MiB, and the
    media-tools gates prove the downloader plus the pinned install
    integration; the extracted-package proof runs REAL h264 + hevc
    exports through the app-managed pair with the system PATH scrubbed
    of FFmpeg. The package-status record is derived from PERSISTED
    evidence only (scripts/ci/write-package-status.py).

    Every command's stdout+stderr is captured from the first byte into
    ci-logs/phase2-build.log with a structured record and the original
    exit code. Independent gates keep running after a failure; dependent
    gates are recorded as SKIPPED, never as passed. The script exits
    non-zero if any required gate failed.

.NOTES
    Requires: Go (from go.mod toolchain), UCRT64 gcc + pkgconfig on PATH
    (MSYS2 UCRT64 — never the deprecated MINGW64), PowerShell 7+.
    CGO_ENABLED=1, GOOS=windows, GOARCH=amd64 must be set for the
    desktop build (the CI workflow sets them; this script verifies them).

    Build metadata: SHEYTAN_BUILD_COMMIT / SHEYTAN_BUILD_DATE /
    SHEYTAN_BUILT_BY / SHEYTAN_BUILD_VERSION are injected into the
    binary via internal/buildinfo when set; a local build without them
    truthfully reports unknown/unknown by local.

    Linker injection contract (run 42231355353 root cause): every -X
    flag must be passed as its own "-X <pkg>.<var>=<value>" token INSIDE
    the single -ldflags value. A joined value without the "-X " prefix
    (e.g. "pkg.version=main pkg.commit=...") is silently ignored by the
    Go tool (exit 0, defaults kept) — the exact defect that made the
    Windows binary report "commit unknown, built unknown by local"
    while the build gate stayed green. The verify-build-metadata gate
    now fails the run when injected metadata does not match the
    environment that requested it.
#>
Set-StrictMode -Version 3.0
$ErrorActionPreference = "Stop"
$PSNativeCommandUseErrorActionPreference = $false

$repoRoot = Split-Path -Parent (Split-Path -Parent (Split-Path -Parent $PSCommandPath))
Set-Location $repoRoot

# REPO-ROOT ENVIRONMENT CONTRACT (run 42921967187 root cause repair):
# generated gate scripts run in CHILD pwsh processes; only ENVIRONMENT
# variables cross that boundary, and the three packaging gates dot-source
# repository helpers through the documented root. The workflow sets
# SHEYTAN_REPO_ROOT at the build-windows job scope (github.workspace); this
# assignment guarantees local runs AND acts as a second belt so every child
# process inherits a defined root. Resolution/fallback/validation itself is
# centralized in scripts/ci/Resolve-RepoRoot.ps1 and the Capture-Step
# -RequireRepoRoot preamble — gates never hand-roll root resolution.
$env:SHEYTAN_REPO_ROOT = $repoRoot

$logDir = Join-Path $repoRoot "ci-logs"
New-Item -ItemType Directory -Force -Path $logDir | Out-Null
$env:CI_LOG_FILE = Join-Path $logDir "phase2-build.log"
[System.IO.File]::WriteAllText($env:CI_LOG_FILE, "", (New-Object System.Text.UTF8Encoding($false))) # fresh local run (LF-safe writer)

# Persisted-evidence reset (run 43712071111): the export-proof result
# files consumed by scripts/ci/write-package-status.py are rewritten by
# the verify-extracted-path-with-spaces gate on every run; leftovers
# from an earlier LOCAL run must never survive as stale PASS evidence.
foreach ($staleProof in @("export-proof-h264.result.txt", "export-proof-h265.result.txt")) {
    $stalePath = Join-Path $logDir $staleProof
    if (Test-Path -LiteralPath $stalePath) { Remove-Item -Force -LiteralPath $stalePath }
}

. (Join-Path $repoRoot "scripts/ci/Capture-Step.ps1")

# Run 44648357437 log-contract repair (work order 2.11): every note is
# sanitized at the moment it enters the log file. The former
# Add-Content writer appended CRLF on Windows (0x0D is a control byte
# the strict validator rejects — the 21 control bytes in
# phase2-build.log) and the raw '=====' banners bypassed the '='-run
# clamp (the 240 forbidden sequences). Write-Note is defined EARLY —
# before the Go-prerequisite BLOCKED branch below — routes through the
# canonical capture-time sanitizer chain (Write-SanitizedChunk, already
# dot-sourced by Capture-Step.ps1) and appends with byte-exact LF via
# [IO.File]::AppendAllText, the writer discipline Capture-Step.ps1
# documents.
function Write-Note([string]$text) {
    $clean = Write-SanitizedChunk -Text $text
    [System.IO.File]::AppendAllText($env:CI_LOG_FILE, $clean + "`n", (New-Object System.Text.UTF8Encoding($false)))
}

$job = "phase2-windows-build"

# CI/user log capture: self-check and runtime checks write their app
# logs under ci-logs/app-logs (explicit SHEYTAN_LOG_DIR override) and
# resolve all data paths under ci-logs/app-data - never the runner's
# unrelated roaming profile. Local runs can preset their own values.
if (-not $env:SHEYTAN_LOG_DIR)  { $env:SHEYTAN_LOG_DIR  = Join-Path $logDir "app-logs" }
if (-not $env:SHEYTAN_DATA_DIR) { $env:SHEYTAN_DATA_DIR = Join-Path $logDir "app-data" }
# goffi_static: required when Fyne (CGO) and wgpu (goffi FFI) link in one
# binary; harmless on Windows (see docs/ai-notes.md).
#
# SINGLE DOCUMENTED TAG SOURCE: $env:SHEYTAN_GO_TAGS. Every Go gate runs in
# a CHILD pwsh process (Capture-Step serializes each gate to its own
# script); a child process never inherits the caller's PowerShell
# variables — only the environment crosses the boundary. Tag-consuming
# gates are generated with the -GoTags switch so each initializes and
# validates its own local $goTags array from this variable (Capture-Step
# preamble). Generic gates (UCRT identity, gofmt, module hygiene, runtime
# checks, staging, PE/ZIP) are generated WITHOUT it: a missing build-tag
# variable must never block a gate that does not consume Go tags
# (run 42231355353 gate-isolation repair).
#
# Inside a gate script the array form is used: PowerShell passes each
# element as its own argument to native commands; a single
# "-tags goffi_static" string would arrive at go as ONE argument.
$env:SHEYTAN_GO_TAGS = "-tags goffi_static"

# PINNED MEDIA-TOOLS PROOF CACHE (run 43712071111): the media-tools CI
# proofs that need REAL ffmpeg/ffprobe binaries (the
# media-tools-pinned-install integration gate and the packaged
# app-managed export proof) download the pinned BtbN win64 GPL asset
# ONCE into this cache — always OUTSIDE the repository, the dist\ tree
# and the package stage tree. The distributed package ships NO
# media-tool binaries; the pinned pair is proof machinery only. Both
# consumer gates resolve the cache through
# scripts/ci/MediaTools-Pinned.ps1 via this environment variable; the
# parent owns cleanup after the last consumer has finished.
$script:mediaToolsCacheOwned = $false
if (-not $env:SHEYTAN_MEDIA_TOOLS_CACHE_DIR) {
    if (-not [string]::IsNullOrWhiteSpace($env:RUNNER_TEMP) -and (Test-Path -LiteralPath $env:RUNNER_TEMP -PathType Container)) {
        $env:SHEYTAN_MEDIA_TOOLS_CACHE_DIR = Join-Path $env:RUNNER_TEMP "media-tools"
    }
    else {
        $env:SHEYTAN_MEDIA_TOOLS_CACHE_DIR = Join-Path ([System.IO.Path]::GetTempPath()) ("sheytan-media-tools-" + [guid]::NewGuid().ToString("N"))
    }
    $script:mediaToolsCacheOwned = $true
}
$script:failures = 0
$script:failedGates = @()
$script:skippedGates = @()

# Missing-prerequisite honesty (docs/agent.md §4 vocabulary): without the Go
# toolchain NOTHING in this script can be verified. Dependent gates are
# recorded BLOCKED (never passed, never failed-with-fake-output) and the
# script exits non-zero so the CI job stays red with truthful evidence.
# The gate records use the same `BLOCKED: gate <name> (<reason>)` shape the
# consolidated report parses into the distinct BLOCKED state.
$goResolved = Get-Command go -ErrorAction SilentlyContinue
if ($null -eq $goResolved) {
    $blockedReason = "go toolchain not found on PATH"
    $blockedGates = @(
        "toolchain-report", "desktop-env-check", "gofmt-verify",
        "go-mod-tidy-verify", "go-mod-verify", "go-test", "go-vet",
        "go-build-packages", "build-executable", "build-shell-resource", "build-gogui-shell", "validate-pe-gui", "run-version",
        "run-self-check", "verify-build-metadata", "stage-portable",
        "verify-package-license", "validate-pe", "create-portable-zip",
        "verify-extracted-package", "forbid-bundled-media-tools",
        "package-size-budget", "media-tools-downloader-tests",
        "media-tools-pinned-install", "verify-extracted-path-with-spaces"
    )
    foreach ($g in $blockedGates) {
        Write-Note ("BLOCKED: gate {0} ({1})" -f $g, $blockedReason)
    }
    Write-Note ""
    $ended = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
    Write-Note "RUN SUMMARY - $job"
    Write-Note "Ended:            $ended"
    Write-Note "Failed gates:     0 (prerequisite missing - this is NOT a passing run)"
    Write-Note "Blocked gate list: $($blockedGates -join ' ')"
    Write-Note "GATES NOT REACHED - the Go toolchain is required for every gate in this script."
    Write-Note "RUN RESULT: FAIL"
    exit 1
}

function Fail-Gate([string]$gate) {
    $script:failures++
    $script:failedGates += $gate
    # RUN 45532045379 repair (no exit 1 without a specific explanation):
    # a failed gate emits an explicit, extractable ERROR line naming the
    # gate, so a hosted failure whose raw tool output matches no
    # diagnostic pattern still produces a specific consolidated-log
    # record (the build-macos.sh fail_gate contract, mirrored here).
    Write-Output ("ERROR: gate {0} FAILED (see the captured gate output above; the gate result JSON in ci-logs/gate-results carries the exit code and timing)" -f $gate)
    Write-Note ("ERROR: gate {0} FAILED (see the captured gate output above; the gate result JSON in ci-logs/gate-results carries the exit code and timing)" -f $gate)
}
function Skip-Gate([string]$gate, [string]$reason) {
    $script:skippedGates += $gate
    Write-Note ("SKIPPED: gate {0} ({1})" -f $gate, $reason)
}

$started = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
Write-Note "=============================================================="
Write-Note "SHEYTAN MUSIC VISUALIZER - PHASE 2 LOCAL VERIFICATION (WINDOWS)"
Write-Note "Product:  SHEYTAN Music Visualizer (SHEYTAN trademark, licensor Parsaetak)"
Write-Note "Host:     $([System.Environment]::OSVersion.VersionString) / $env:PROCESSOR_ARCHITECTURE"
Write-Note "Started:  $started"
Write-Note "=============================================================="

# ---- toolchain + environment report ----------------------------------------
# No -GoTags: this gate only reports identities; it consumes no build tags.
$rc = Capture-Step -Job $job -Step "toolchain-report" -ToolContext "gate:toolchain" -Command {
    go version
    go env GOOS GOARCH CGO_ENABLED GOTOOLCHAIN GOPATH GOMOD
    # $(...) captures the FULL output first; `| Select-Object -First 1`
    # would terminate the pipeline and kill gcc mid-write.
    "gcc: $((gcc --version)[0])"
    "gcc -dumpmachine: $(gcc -dumpmachine)"
    "pkg-config: $(pkg-config --version)"
}
if ($rc -ne 0) { Fail-Gate "toolchain-report" }

$rc = Capture-Step -Job $job -Step "desktop-env-check" -ToolContext "gate:env" -Command {
    $goos = (go env GOOS).Trim()
    $goarch = (go env GOARCH).Trim()
    $cgo = (go env CGO_ENABLED).Trim()
    Write-Output "GOOS=$goos GOARCH=$goarch CGO_ENABLED=$cgo"
    if ($goos -ne "windows" -or $goarch -ne "amd64" -or $cgo -ne "1") {
        Write-Output "ERROR: desktop build requires GOOS=windows GOARCH=amd64 CGO_ENABLED=1"
        exit 1
    }
    $gcc = (Get-Command gcc -ErrorAction SilentlyContinue)
    if ($null -eq $gcc) {
        Write-Output "BLOCKED: gate desktop-env-check (gcc not found on PATH - MSYS2 UCRT64 required)"
        exit 1
    }
    Write-Output "gcc resolved to: $($gcc.Source)"
    # Evidence source 1: the first resolved compiler must live inside the
    # MSYS2 UCRT64 prefix. A stale MINGW64 (msvcrt) or runner-bundled
    # toolchain is rejected with a precise error, never silently used.
    # (Path-shape evidence: the UCRT64 environment always installs the
    # toolchain under a <msys2>\ucrt64\bin prefix; MINGW64 uses mingw64\bin.)
    if ($gcc.Source -notmatch '[\\/]ucrt64[\\/]bin[\\/]') {
        Write-Output "ERROR: first gcc on PATH is not inside a ucrt64\bin prefix: $($gcc.Source)"
        Write-Output "ERROR: MINGW64 (msvcrt runtime) is not an accepted fallback for this pipeline"
        exit 1
    }
    # Target-triple identity (evidence source 2).
    $dumpmachine = (gcc -dumpmachine)
    $dmRc = $LASTEXITCODE
    if ($dmRc -ne 0) { Write-Output "ERROR: gcc -dumpmachine failed with exit code $dmRc"; exit $dmRc }
    Write-Output "gcc -dumpmachine: $dumpmachine"
    if ($dumpmachine -ne "x86_64-w64-mingw32") {
        Write-Output "ERROR: gcc -dumpmachine is '$dumpmachine' (want x86_64-w64-mingw32 - MSYS2 UCRT64 required)"
        exit 1
    }
    # CRT identity (evidence source 3, HEADER-AWARE probe).
    # _UCRT is NOT a GCC built-in macro: mingw-w64 establishes it in the
    # CRT header configuration (_mingw.h) from the configured runtime
    # (__MSVCRT_VERSION__ >= 0xE00/0x1400 selects UCRT). A no-header
    # translation unit therefore cannot establish which CRT the headers
    # select — the run-42231355353 desktop-env-check failure was exactly
    # that defective probe. Include a real CRT umbrella header first;
    # <stdio.h> pulls in _mingw.h through the installed header chain.
    $probeSource = @('#include <stdio.h>', 'int main(void){return 0;}')
    $defines = $probeSource | gcc -E -dM -xc -
    $probeRc = $LASTEXITCODE
    if ($probeRc -ne 0) {
        Write-Output "ERROR: header-aware _UCRT probe failed: gcc -E exited with code $probeRc"
        exit $probeRc
    }
    if (-not ($defines | Select-String -SimpleMatch '_UCRT')) {
        Write-Output "ERROR: CRT headers do not define _UCRT (stale MINGW64/msvcrt toolchain detected)"
        exit 1
    }
    Write-Output "UCRT runtime confirmed via header-aware probe (_UCRT defined after including <stdio.h>)"
    # Header-chain evidence (evidence source 4, run-42376176493 repair):
    # the installed CRT configuration header must exist at the UCRT64
    # TARGET header root <msys2>\ucrt64\include\_mingw.h - installed there
    # by the mingw-w64-ucrt-x86_64-headers package. GCC's private internal
    # include directory (gcc -print-file-name=include) does NOT contain
    # _mingw.h; the run-42376176493 gate turned that fact into a false
    # negative and failed an otherwise-proven UCRT64 installation. The
    # private directory stays a clearly-labeled diagnostic only and is
    # never searched for _mingw.h.
    $incDir = (gcc -print-file-name=include).Trim()
    $incRc = $LASTEXITCODE
    if ($incRc -ne 0 -or [string]::IsNullOrWhiteSpace($incDir)) {
        Write-Output "ERROR: gcc -print-file-name=include failed (rc=$incRc)"
        exit 1
    }
    Write-Output "gcc private internal include dir (diagnostic only - NOT the CRT header location): $incDir"
    if ($incDir -notmatch '[\\/]ucrt64[\\/]') {
        Write-Output "ERROR: compiler private include dir is not inside a ucrt64 prefix: $incDir"
        exit 1
    }
    $ucrtRoot = Split-Path -Parent (Split-Path -Parent $gcc.Source)
    $targetHeader = Join-Path $ucrtRoot "include/_mingw.h"
    if (-not (Test-Path $targetHeader)) {
        Write-Output "ERROR: CRT header _mingw.h not found at the UCRT64 target root: $targetHeader"
        Write-Output "ERROR: expected owner: mingw-w64-ucrt-x86_64-headers (verify with: pacman -Qo /ucrt64/include/_mingw.h)"
        exit 1
    }
    Write-Output "CRT header _mingw.h present at the UCRT64 target root: $targetHeader"
    # pkg-config identity (evidence source 5).
    $pc = Get-Command pkg-config -ErrorAction SilentlyContinue
    if ($null -eq $pc) {
        Write-Output "ERROR: pkg-config not found on PATH (MSYS2 UCRT64 pkgconf required)"
        exit 1
    }
    Write-Output "pkg-config resolved to: $($pc.Source)"
    if ($pc.Source -notmatch '[\\/]ucrt64[\\/]bin[\\/]') {
        Write-Output "ERROR: pkg-config is not the UCRT64 pkgconf (found $($pc.Source))"
        exit 1
    }
    $pcVer = (pkg-config --version)
    $pcRc = $LASTEXITCODE
    if ($pcRc -ne 0) { Write-Output "ERROR: pkg-config --version failed with exit code $pcRc"; exit $pcRc }
    Write-Output "pkg-config version: $pcVer"
    Write-Output "UCRT64 toolchain identity OK: ucrt64\bin prefix, x86_64-w64-mingw32, header-aware _UCRT, _mingw.h present, UCRT64 pkgconf"
    exit 0
}
if ($rc -ne 0) { Fail-Gate "desktop-env-check" }

# ---- static gates (all independent; always run) ----------------------------
# gofmt gate (no -GoTags): the gate MUST honor gofmt's own exit code.
# The previous form could report "gofmt clean" after a crashed gofmt
# (empty output, nonzero exit) because the success path exited 0 without
# checking $LASTEXITCODE.
$rc = Capture-Step -Job $job -Step "gofmt-verify" -ToolContext "gate:gofmt" -Command {
    $gofmt = Get-Command gofmt -ErrorAction SilentlyContinue
    if ($null -eq $gofmt) {
        Write-Output "ERROR: gofmt not found on PATH (go toolchain required)"
        exit 1
    }
    $out = & gofmt -l .
    $gofmtRc = $LASTEXITCODE
    if ($gofmtRc -ne 0) {
        Write-Output "ERROR: gofmt -l . failed with exit code $gofmtRc (never report clean across a failed gofmt)"
        exit $gofmtRc
    }
    if ($out) {
        $out | ForEach-Object { Write-Output $_ }
        Write-Output "ERROR: gofmt reported unformatted files"
        exit 1
    }
    Write-Output "gofmt clean (gofmt exit code 0)"
    exit 0
}
if ($rc -ne 0) { Fail-Gate "gofmt-verify" }

# Read-only module hygiene: go mod tidy -diff never mutates the
# checked-out tree (the mutating `go mod tidy` gate was removed).
$rc = Capture-Step -Job $job -Step "go-mod-tidy-verify" -ToolContext "gate:go-mod-tidy" -Command { go mod tidy -diff }
if ($rc -ne 0) { Fail-Gate "go-mod-tidy-verify" }

$rc = Capture-Step -Job $job -Step "go-mod-verify" -ToolContext "gate:go-mod-verify" -Command { go mod verify }
if ($rc -ne 0) { Fail-Gate "go-mod-verify" }

# ---- Go gates (tag-consuming: generated with the -GoTags preamble) ---------
# Test ownership (run 42939743766 §2.4): the dedicated GPU integration test
# TestPaletteReachesGPUShader is owned EXCLUSIVELY by this job's
# gpu-validation gate; it is excluded here ONLY by that exact name. Every
# other internal/render test stays in the default suite.
# RUN 45532045379 repair (test hermeticity): SHEYTAN_LOG_DIR and
# SHEYTAN_DATA_DIR are set for the RUNTIME captures of this script; the
# Go test/vet/build gates must NOT inherit them - the leaked override
# redirected the Bootstrap-based logging tests away from their own
# layouts and failed the whole go-test gate on hosted Windows
# (reproduced locally, six tests). The Go gates clear both overrides in
# their own child process; runtime gates re-apply the documented values
# explicitly where they need them.
$rc = Capture-Step -Job $job -Step "go-test" -ToolContext "gate:go-test" -GoTags -Command { $env:SHEYTAN_LOG_DIR = $null; $env:SHEYTAN_DATA_DIR = $null; go test $goTags -skip '^TestPaletteReachesGPUShader$' ./... }
if ($rc -ne 0) { Fail-Gate "go-test" }

$rc = Capture-Step -Job $job -Step "go-vet" -ToolContext "gate:go-vet" -GoTags -Command { $env:SHEYTAN_LOG_DIR = $null; $env:SHEYTAN_DATA_DIR = $null; go vet $goTags ./... }
if ($rc -ne 0) { Fail-Gate "go-vet" }

$rc = Capture-Step -Job $job -Step "go-build-packages" -ToolContext "gate:go-build" -GoTags -Command { $env:SHEYTAN_LOG_DIR = $null; $env:SHEYTAN_DATA_DIR = $null; go build $goTags ./... }
if ($rc -ne 0) { Fail-Gate "go-build-packages" }

# ---- desktop executable + runtime checks -----------------------------------
$exeOk = $false
# RUN 45297707372 SHIP-THE-SHELL REPAIR (the reported "Windows build did
# not open and recorded no logs" root cause): the package used to ship
# ONLY bin\sheytan-music-visualizer.exe - the CLI AUTHORITY, whose own
# documentation says "It never opens a window" - linked with
# -H=windowsgui. A user double-clicking that executable saw NOTHING: no
# window (the CLI opens none by design) and no console (the GUI
# subsystem has none), so the product appeared dead. The desktop
# PRODUCT executable is the native Go-Gui shell, cmd/app-gogui:
#   - bin\app-gogui.exe                THE product executable, GUI
#                                      subsystem, shell icon + BETA
#                                      VERSIONINFO resource (windres).
#   - bin\sheytan-music-visualizer.exe the CLI authority, CONSOLE
#                                      subsystem (restored), shipped as
#                                      the scripting/self-check companion.
# validate-pe verifies BOTH subsystems from the built artifacts.
$exePath = Join-Path $repoRoot "bin\sheytan-music-visualizer.exe"
$rc = Capture-Step -Job $job -Step "build-executable" -ToolContext "gate:build-exe" -GoTags -Command {
    $bi = "github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/buildinfo"
    $args_ = @("build", "-trimpath") + $goTags
    $ld = @()
    # CONSOLE SUBSYSTEM RESTORED (run 45297707372): the CLI authority is
    # a console tool by design; its CI gates capture stdout/stderr
    # normally. The -H=windowsgui token moved to the product shell
    # build below (run 42598929646 defect H applies to the GUI exe).
    # EVERY element carries its own "-X " flag token inside the single
    # -ldflags value. Without the -X prefix the Go tool silently ignores
    # the tokens (exit 0, defaults kept) — the run 42231355353 defect
    # that produced "commit unknown, built unknown by local".
    if ($env:SHEYTAN_BUILD_VERSION) { $ld += "-X ${bi}.version=$($env:SHEYTAN_BUILD_VERSION)" }
    if ($env:SHEYTAN_BUILD_COMMIT)  { $ld += "-X ${bi}.commit=$($env:SHEYTAN_BUILD_COMMIT)" }
    if ($env:SHEYTAN_BUILD_DATE)    { $ld += "-X ${bi}.date=$($env:SHEYTAN_BUILD_DATE)" }
    if ($env:SHEYTAN_BUILT_BY)      { $ld += "-X ${bi}.builtBy=$($env:SHEYTAN_BUILT_BY)" }
    if ($ld.Count -gt 0) {
        # "-ldflags" and its (space-containing) value are two argv
        # elements; go accepts a single-argument value with spaces.
        $args_ += @("-ldflags", ($ld -join " "))
    }
    $args_ += @("-o", "bin\sheytan-music-visualizer.exe", "./cmd/sheytan-music-visualizer")
    # Machine-readable diagnostics (safe, non-secret values only):
    # exact final argument vector + which metadata sources were present,
    # so a future regression is diagnosable from the log alone.
    $i = 0
    foreach ($a in $args_) { Write-Output ("GO_ARGV[{0}]={1}" -f $i, $a); $i++ }
    Write-Output ("BUILD_ENV presence: version={0} commit={1} date={2} builtBy={3}" -f
        [bool]($env:SHEYTAN_BUILD_VERSION), [bool]($env:SHEYTAN_BUILD_COMMIT),
        [bool]($env:SHEYTAN_BUILD_DATE), [bool]($env:SHEYTAN_BUILT_BY))
    if ($ld.Count -eq 0) {
        Write-Output "NOTE: no SHEYTAN_BUILD_* metadata was provided; the executable will truthfully report local/dev defaults (verify-build-metadata enforces this contract)."
    }
    go @args_
}
if ($rc -eq 0) { $exeOk = $true } else { Fail-Gate "build-executable" }

# ---- product desktop shell (app-gogui.exe) + Windows icon resource -------
# RUN 45297707372: the EXECUTION FILE the user double-clicks must be the
# native Go-Gui shell AND must carry the SHEYTAN brand icon. windres
# compiles assets/windows/app.rc (1 ICON assets/icon/icon.ico + the BETA
# VERSIONINFO block) into cmd/app-gogui/resource.syso; the Go linker
# embeds every *.syso in the package directory into the exe. The .syso
# is generated fresh every build and is gitignored (a bare-name .syso
# would otherwise be linked into non-Windows targets too).
$shellOk = $false
$rc = Capture-Step -Job $job -Step "build-shell-resource" -ToolContext "gate:shell-resource" -RequireRepoRoot -Command {
    $windres = (Get-Command windres -ErrorAction SilentlyContinue)
    if ($null -eq $windres) { $windres = (Get-Command x86_64-w64-mingw32-windres -ErrorAction SilentlyContinue) }
    if ($null -eq $windres) {
        Write-Output "ERROR: windres not found on PATH (MSYS2 UCRT64 required for the icon resource)"
        exit 1
    }
    Write-Output ("windres: {0}" -f $windres.Source)
    & $windres.Source "-I" $repoRoot "-i" (Join-Path $repoRoot "assets\windows\app.rc") "-O" "coff" "-o" (Join-Path $repoRoot "cmd\app-gogui\resource.syso")
    if ($LASTEXITCODE -ne 0) {
        Write-Output "ERROR: windres failed with exit code $LASTEXITCODE"
        exit $LASTEXITCODE
    }
    $syso = Get-Item (Join-Path $repoRoot "cmd\app-gogui\resource.syso")
    if ($syso.Length -le 0) { Write-Output "ERROR: resource.syso is empty"; exit 1 }
    Write-Output ("resource.syso written: {0} bytes" -f $syso.Length)
    # The tracked ICO must be reachable exactly where app.rc points.
    if (-not (Test-Path (Join-Path $repoRoot "assets\icon\icon.ico"))) {
        Write-Output "ERROR: assets/icon/icon.ico missing (app.rc references it)"
        exit 1
    }
    exit 0
}
if ($rc -eq 0) { Write-Output "shell resource gate: OK" } else { Fail-Gate "build-shell-resource" }

$rc = Capture-Step -Job $job -Step "build-gogui-shell" -ToolContext "gate:build-gogui-shell" -GoTags -Command {
    $bi = "github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/buildinfo"
    $args_ = @("build", "-trimpath") + $goTags
    $ld = @("-H=windowsgui")
    # GUI SUBSYSTEM (run 42598929646 defect H): launching the product
    # shell never attaches a command window; validate-pe-gui below
    # verifies IMAGE_SUBSYSTEM_WINDOWS_GUI / 2 from the built artifact.
    if ($env:SHEYTAN_BUILD_VERSION) { $ld += "-X ${bi}.version=$($env:SHEYTAN_BUILD_VERSION)" }
    if ($env:SHEYTAN_BUILD_COMMIT)  { $ld += "-X ${bi}.commit=$($env:SHEYTAN_BUILD_COMMIT)" }
    if ($env:SHEYTAN_BUILD_DATE)    { $ld += "-X ${bi}.date=$($env:SHEYTAN_BUILD_DATE)" }
    if ($env:SHEYTAN_BUILT_BY)      { $ld += "-X ${bi}.builtBy=$($env:SHEYTAN_BUILT_BY)" }
    $args_ += @("-ldflags", ($ld -join " "))
    $args_ += @("-o", "bin\app-gogui.exe", "./cmd/app-gogui")
    Write-Output ("GO_ARGV: {0}" -f ($args_ -join " "))
    go @args_
}
if ($rc -eq 0) { $shellOk = $true } else { Fail-Gate "build-gogui-shell" }

if ($exeOk) {
    $rc = Capture-Step -Job $job -Step "run-version" -ToolContext "runtime:--version" -Command { & "bin\sheytan-music-visualizer.exe" --version }
    if ($rc -ne 0) { Fail-Gate "run-version" }

    # GUI-subsystem evidence contract (run 42598929646): a
    # -H=windowsgui process may not inherit captured stdout/stderr on
    # every runner, so the structured self-check result is ALSO written
    # to a file that CI reads (main.go --self-check-out). The console
    # capture stays in place; the file is the belt-and-braces evidence.
    $rc = Capture-Step -Job $job -Step "run-self-check" -ToolContext "runtime:--self-check" -Command {
        & "bin\sheytan-music-visualizer.exe" --self-check --self-check-out "ci-logs\windows-self-check.txt"
        if (Test-Path "ci-logs\windows-self-check.txt") {
            Write-Output "self-check result file: ci-logs/windows-self-check.txt"
            Get-Content "ci-logs\windows-self-check.txt"
        }
    }
    if ($rc -ne 0) { Fail-Gate "run-self-check" }

    # ENFORCED metadata acceptance gate (run 42231355353 repair): exit
    # code 0 from --version is NOT evidence that the metadata arrived.
    # When CI metadata was supplied, --version and --self-check MUST
    # carry exactly those values; when none was supplied (local dev
    # build), the legitimate local/dev defaults are asserted instead.
    # Missing, stale, malformed or inconsistent values fail the gate.
    # ROBUST PROCESS-EXIT CONTRACT (run 42837709122 gate A repair): the
    # previous form read the automatic $LASTEXITCODE right after
    # `& "bin\sheytan-music-visualizer.exe"`. The distributed desktop
    # executable is linked -H=windowsgui; on the hosted runner pwsh
    # never surfaced that child's exit code into $LASTEXITCODE, and the
    # gate's Set-StrictMode -Version Latest threw
    #   "The variable '$LASTEXITCODE' cannot be retrieved because it has
    #    not been set."
    # The gate now launches the exe through Invoke-CapturedProcess
    # (System.Diagnostics.Process, UseShellExecute=$false, redirected
    # stdout+stderr, argument array, bounded wait, REAL Process.ExitCode,
    # fail-closed on any unobservable exit). $LASTEXITCODE is never read.
    # The GUI subsystem of the distributed binary is NOT touched.
    $rc = Capture-Step -Job $job -Step "verify-build-metadata" -ToolContext "gate:build-metadata" -RequireRepoRoot -RepoRootHelpers @("scripts/ci/Invoke-CapturedProcess.ps1") -Command {
        . (Join-Path $repoRoot "scripts/ci/Invoke-CapturedProcess.ps1")
        $exe = Join-Path (Get-Location).Path "bin\sheytan-music-visualizer.exe"
        if (-not (Test-Path -LiteralPath $exe)) {
            Write-Output "ERROR: executable not found at $exe (nothing to verify)"
            exit 1
        }
        $expectedCommit = ""
        $expectedVersion = ""
        $expectedBuiltBy = ""
        $expectedDate = ""
        $ciMode = $false
        if (-not [string]::IsNullOrWhiteSpace($env:SHEYTAN_BUILD_COMMIT)) {
            $ciMode = $true
            $expectedCommit = $env:SHEYTAN_BUILD_COMMIT.Trim()
            $expectedVersion = if ($env:SHEYTAN_BUILD_VERSION) { $env:SHEYTAN_BUILD_VERSION.Trim() } else { "" }
            $expectedBuiltBy = if ($env:SHEYTAN_BUILT_BY) { $env:SHEYTAN_BUILT_BY.Trim() } else { "" }
            $expectedDate = if ($env:SHEYTAN_BUILD_DATE) { $env:SHEYTAN_BUILD_DATE.Trim() } else { "" }
        }
        $verRun = Invoke-CapturedProcess -FilePath $exe -ArgumentList @("--version") -TimeoutSeconds 120
        if ($verRun.Error -ne "") {
            Write-Output "ERROR: --version process observation incomplete: $($verRun.Error)"
            exit 1
        }
        Write-Output ("--version exit code observed: {0}" -f $verRun.ExitCode)
        if ($verRun.ExitCode -ne 0) {
            Write-Output "ERROR: --version exited with $($verRun.ExitCode)"
            if ($verRun.StdErr.Count -gt 0) { $verRun.StdErr | ForEach-Object { Write-Output "stderr: $_" } }
            exit $verRun.ExitCode
        }
        $verLine = ($verRun.StdOut | Select-Object -First 1)
        Write-Output "version line observed: $verLine"
        # Machine-readable self-check: the GUI-subsystem process may not
        # have its console streams captured on every runner, so the result
        # is ALSO written to a file (--self-check-out). The file contents
        # are validated here as first-class evidence — never the console
        # output alone.
        $selfOutFile = Join-Path (Get-Location).Path "ci-logs\windows-self-check-metadata.txt"
        if (Test-Path -LiteralPath $selfOutFile) { Remove-Item -LiteralPath $selfOutFile -Force }
        $selfRun = Invoke-CapturedProcess -FilePath $exe -ArgumentList @("--self-check", "--self-check-out", $selfOutFile) -TimeoutSeconds 180
        if ($selfRun.Error -ne "") {
            Write-Output "ERROR: --self-check process observation incomplete: $($selfRun.Error)"
            exit 1
        }
        Write-Output ("--self-check exit code observed: {0}" -f $selfRun.ExitCode)
        if ($selfRun.ExitCode -ne 0) {
            Write-Output "ERROR: --self-check exited with $($selfRun.ExitCode)"
            if ($selfRun.StdErr.Count -gt 0) { $selfRun.StdErr | ForEach-Object { Write-Output "stderr: $_" } }
            exit $selfRun.ExitCode
        }
        if (-not (Test-Path -LiteralPath $selfOutFile)) {
            Write-Output "ERROR: --self-check wrote no machine-readable result file: $selfOutFile"
            exit 1
        }
        $selfFileText = Get-Content -LiteralPath $selfOutFile
        Write-Output "self-check result file contents:"
        $selfFileText | ForEach-Object { Write-Output "  | $_" }
        if (-not ($selfFileText | Where-Object { $_ -match 'self-check: all foundation invariants hold' })) {
            Write-Output "ERROR: self-check result file does not contain the passing verdict"
            exit 1
        }
        $selfInfo = ($selfRun.StdOut + $selfFileText | Where-Object { $_ -match 'version=' } | Select-Object -First 1)
        if ($null -eq $selfInfo) {
            $selfInfo = ($selfFileText | Where-Object { $_ -match 'commit=' } | Select-Object -First 1)
        }
        Write-Output "self-check metadata observed: $selfInfo"

        $failures_ = @()
        if ($ciMode) {
            Write-Output "metadata expected: version=$expectedVersion commit=$expectedCommit builtBy=$expectedBuiltBy date=$expectedDate (CI-injected)"
            if ($verLine -notlike "*commit $expectedCommit,*") { $failures_ += "commit mismatch (want $expectedCommit)" }
            if ($expectedVersion -ne "" -and -not $verLine.StartsWith("SHEYTAN Music Visualizer $expectedVersion ")) {
                $failures_ += "version mismatch (want $expectedVersion)"
            }
            if ($expectedBuiltBy -ne "" -and $verLine -notlike "* by $expectedBuiltBy,*") { $failures_ += "builder mismatch (want $expectedBuiltBy)" }
            if ($expectedDate -ne "" -and $verLine -notlike "* built $expectedDate by*") { $failures_ += "build date mismatch (want $expectedDate)" }
            if ($null -ne $selfInfo -and $selfInfo -notmatch ("commit=" + [regex]::Escape($expectedCommit) + "\b")) {
                $failures_ += "self-check commit mismatch (want $expectedCommit)"
            }
        }
        else {
            Write-Output "metadata expected: local/dev defaults (no SHEYTAN_BUILD_* env supplied)"
            if ($verLine -notmatch 'commit unknown,') { $failures_ += "local build must truthfully report 'commit unknown'" }
            if ($verLine -notmatch ' by local,') { $failures_ += "local build must truthfully report 'by local'" }
        }
        if ($verLine -match 'built unknown by local' -and $ciMode) {
            $failures_ += "CI metadata did not reach the binary (all defaults present)"
        }
        if ($failures_.Count -gt 0) {
            $failures_ | ForEach-Object { Write-Output "ERROR: $_" }
            Write-Output "ERROR: build metadata verification FAILED"
            exit 1
        }
        Write-Output "build metadata verification PASSED (observed values match the expected source)"
        exit 0
    }
    if ($rc -ne 0) { Fail-Gate "verify-build-metadata" }

    # ---- portable staging + artifact validation -----------------------------
    # COMPACT PACKAGE CONTRACT (run 43712071111): the package ships NO
    # media-tool binaries. tools\ffmpeg\ + an EMPTY tools\ffmpeg\bin\
    # stay as the documented app-local override location, and the
    # FFmpeg licensing notice is staged beside them; the binaries
    # themselves are app-managed downloads (the in-app Media Tools &
    # Codecs manager installs the pinned pair under
    # <DataRoot>\media-tools\current\bin, never into the package).
    # forbid-bundled-media-tools and package-size-budget below enforce
    # this negatively on the produced ZIP.
    $rc = Capture-Step -Job $job -Step "stage-portable" -ToolContext "gate:stage-portable" -Command {
        # RUN 45347124889 stage-portable repair: the gate previously died
        # inside $ErrorActionPreference = "Stop" on a thrown Copy-Item
        # without any explanatory "ERROR:" line, so the filtered
        # diagnostic log carried only the downstream missing-entry
        # failures and never the origin. Every source is asserted first
        # with a precise per-file message; a freshly linked executable
        # can transiently hold a sharing lock while Defender real-time
        # scanning reads it (validate-pe read the same CLI exe
        # successfully before AND after the failed copy), so the copy
        # retries ONLY that documented class on a bounded budget,
        # logging every attempt; anything else fails loudly.
        function Copy-StagedFile {
            param([string]$Source, [string]$DestDir)
            if (-not (Test-Path -LiteralPath $Source -PathType Leaf)) {
                Write-Output ("ERROR: stage-portable source missing: {0} (expected under the job working directory)" -f $Source)
                exit 1
            }
            $deadline = (Get-Date).AddSeconds(12)
            $attempt = 0
            while ($true) {
                $attempt++
                try {
                    Copy-Item -LiteralPath $Source -Destination $DestDir -ErrorAction Stop
                    return
                }
                catch {
                    $msg = $_.Exception.Message
                    $transient = ($msg -match "being used by another process") -or ($msg -match "sharing violation")
                    if (-not $transient -or (Get-Date) -ge $deadline) {
                        Write-Output ("ERROR: stage-portable could not copy {0} after {1} attempt(s): {2}" -f $Source, $attempt, $msg)
                        exit 1
                    }
                    Write-Output ("WARN: transient sharing violation copying {0} (attempt {1}); retrying: {2}" -f $Source, $attempt, $msg)
                    Start-Sleep -Seconds 2
                }
            }
        }
        try {
        # CANONICAL ONE-PACKAGE CONTRACT (run 42939743766 §5.1): the GitHub
        # Actions artifact is EXACTLY dist\SHEYTAN-Music-Visualizer-windows-x64.zip
        # containing exactly one top-level app directory
        # SHEYTAN-Music-Visualizer-windows-x64/. The staging root is an
        # INTERNAL directory that is never uploaded and never archived.
        $stageRoot = Join-Path (Get-Location).Path "dist\windows-x64-stage"
        if (Test-Path $stageRoot) { Remove-Item -Recurse -Force $stageRoot }
        $appDir = Join-Path $stageRoot "SHEYTAN-Music-Visualizer-windows-x64"
        New-Item -ItemType Directory -Force -Path $appDir | Out-Null
        New-Item -ItemType Directory -Force -Path (Join-Path $appDir "tools\ffmpeg\bin") | Out-Null
        New-Item -ItemType Directory -Force -Path (Join-Path $appDir "data") | Out-Null
        # RUN 45297707372 SHIP-THE-SHELL: app-gogui.exe is the PRIMARY
        # product executable (the thing users double-click); the CLI
        # stays as the scripting/self-check companion.
        # RUN 45347124889 ROOT CAUSE: this gate body previously read
        # the parent-only $shellOk variable. Capture-Step serializes
        # each gate into a CHILD pwsh process whose only inherited
        # PowerShell surface is the generated script text, so under
        # Set-StrictMode -Version 3.0 the read threw "The variable
        # '\u0024shellOk' cannot be retrieved because it has not been
        # set." - a message with no "error" substring, which the
        # filtered diagnostic log therefore never showed (the same
        # parent-only-variable class as run 41915116613's $goTags).
        # The gate now derives the shell-exe state from the FILE
        # SYSTEM (the artifact the previous gate built), never from
        # parent scope. Gate bodies must reference parent variables
        # never; scripts/ci/tests/test-ci-plumbing.sh asserts the
        # audit below.
        if (Test-Path -LiteralPath "bin\app-gogui.exe" -PathType Leaf) {
            Copy-StagedFile -Source "bin\app-gogui.exe" -DestDir $appDir
        } else {
            Write-Output "ERROR: the product desktop shell (bin/app-gogui.exe) was not found beside the built CLI; refusing to stage a CLI-only package"
            exit 1
        }
        Copy-StagedFile -Source "assets\icon\icon.png" -DestDir $appDir
        Copy-StagedFile -Source "bin\sheytan-music-visualizer.exe" -DestDir $appDir
        Copy-StagedFile -Source "docs/license.md" -DestDir $appDir
        Copy-StagedFile -Source "readme.md" -DestDir $appDir
        Copy-StagedFile -Source "packaging\portable.mode" -DestDir $appDir
        Copy-StagedFile -Source "packaging\DATA-FOLDERS.txt" -DestDir (Join-Path $appDir "data")
        Copy-StagedFile -Source "packaging\FFMPEG-TOOLS-LICENSING.md" -DestDir (Join-Path $appDir "tools\ffmpeg")
        $exeItem = Get-Item (Join-Path $appDir "sheytan-music-visualizer.exe")
        if ($exeItem.Length -le 0) { Write-Output "ERROR: staged executable is empty"; exit 1 }
        Write-Output ("staged executable size: {0} bytes" -f $exeItem.Length)
        Get-ChildItem -Recurse -File $appDir | ForEach-Object { $rel = $_.FullName.Substring($stageRoot.Length + 1); Write-Output ("STAGED: " + ($rel -replace '\\', '/')) }
        }
        catch {
            Write-Output ("ERROR: stage-portable failed: {0}" -f $_.Exception.Message)
            exit 1
        }
        exit 0
    }
    if ($rc -ne 0) { Fail-Gate "stage-portable" }

    # BYTE-FOR-BYTE license identity gate (run 42481815519 contract):
    # the staged license.md must equal the canonical tracked file
    # exactly, before archive creation. SHA-256 equality on raw bytes;
    # any mismatch fails the package gate.
    $rc = Capture-Step -Job $job -Step "verify-package-license" -ToolContext "gate:license-identity" -Command {
        # RUN 45347124889: a missing input previously threw opaquely
        # (Get-FileHash under $ErrorActionPreference = "Stop"); the
        # precondition makes the failure self-describing.
        try {
        $canonicalPath = "docs/license.md"
        $stagedPath = "dist\windows-x64-stage\SHEYTAN-Music-Visualizer-windows-x64\license.md"
        foreach ($licPath in @($canonicalPath, $stagedPath)) {
            if (-not (Test-Path -LiteralPath $licPath -PathType Leaf)) {
                Write-Output ("ERROR: verify-package-license input missing: {0} (a missing staged license means stage-portable failed upstream)" -f $licPath)
                exit 1
            }
        }
        $canonical = Get-FileHash -Algorithm SHA256 $canonicalPath
        $staged = Get-FileHash -Algorithm SHA256 $stagedPath
        Write-Output ("canonical sha256: {0}" -f $canonical.Hash)
        Write-Output ("staged    sha256: {0}" -f $staged.Hash)
        if ($canonical.Hash -ne $staged.Hash) {
            Write-Output "ERROR: staged license.md differs from the canonical tracked license.md (byte comparison FAILED)"
            exit 1
        }
        Write-Output "license identity PASSED (staged copy is byte-identical to the canonical tracked license.md)"
        exit 0
        }
        catch {
            Write-Output ("ERROR: verify-package-license failed: {0}" -f $_.Exception.Message)
            exit 1
        }
    }
    if ($rc -ne 0) { Fail-Gate "verify-package-license" }

    # PE SUBSYSTEM GATE (run 42598929646 defect H / gate V12): verify
    # from the BUILT ARTIFACT that the Optional Header subsystem is
    # Windows GUI (IMAGE_SUBSYSTEM_WINDOWS_GUI = 2). The old gate only
    # checked the MZ signature, which cannot detect a console-subsystem
    # binary. Parsing is pure byte math on the documented PE layout:
    # e_lfanew @0x3C -> "PE\0\0" -> COFF header (20 bytes) -> Optional
    # Header; Subsystem is at OptionalHeader+68 for both PE32 and PE32+.
    $rc = Capture-Step -Job $job -Step "validate-pe" -ToolContext "gate:validate-pe" -Command {
        $exe = "bin\sheytan-music-visualizer.exe"
        $bytes = [System.IO.File]::ReadAllBytes($exe)
        if ($bytes.Length -lt 0x100) { Write-Output "ERROR: file too small for a PE ($($bytes.Length) bytes)"; exit 1 }
        if ($bytes[0] -ne 77 -or $bytes[1] -ne 90) { Write-Output "ERROR: not a PE executable (missing MZ header)"; exit 1 }
        Write-Output "PE MZ header confirmed"
        $peOff = [BitConverter]::ToInt32($bytes, 0x3C)
        if ($peOff -le 0 -or ($peOff + 24 + 68) -gt $bytes.Length) { Write-Output "ERROR: implausible e_lfanew $peOff"; exit 1 }
        if ($bytes[$peOff] -ne 0x50 -or $bytes[$peOff+1] -ne 0x45 -or $bytes[$peOff+2] -ne 0 -or $bytes[$peOff+3] -ne 0) {
            Write-Output "ERROR: PE signature missing at e_lfanew"; exit 1
        }
        $optOff = $peOff + 4 + 20
        $magic = [BitConverter]::ToUInt16($bytes, $optOff)
        if ($magic -ne 0x20B -and $magic -ne 0x10B) { Write-Output "ERROR: unexpected Optional Header magic 0x{0:X4}" -f $magic; exit 1 }
        $subsystem = [BitConverter]::ToUInt16($bytes, $optOff + 68)
        Write-Output ("PE optional-header magic: 0x{0:X4}; Subsystem: {1} (2 = IMAGE_SUBSYSTEM_WINDOWS_GUI, 3 = console)" -f $magic, $subsystem)
        # RUN 45297707372: the CLI authority is a CONSOLE tool (3) by
        # design; the GUI-subsystem assertion moved to validate-pe-gui
        # on the product shell binary.
        if ($subsystem -ne 3) {
            Write-Output "ERROR: the CLI authority must stay a console-subsystem binary (subsystem 3; CI captures its stdout/stderr)"
            exit 1
        }
        Write-Output "validate-pe PASSED: CLI authority subsystem = console (3), verified from the built artifact"
        exit 0
    }
    if ($rc -ne 0) { Fail-Gate "validate-pe" }

    # PRODUCT SHELL PE SUBSYSTEM + ICON RESOURCE GATE (run 45297707372):
    # bin\app-gogui.exe must be IMAGE_SUBSYSTEM_WINDOWS_GUI (2) AND must
    # carry the embedded icon/VERSIONINFO resource (.rsrc section from
    # cmd/app-gogui/resource.syso). This is the gate that would have
    # caught the "package shipped a CLI, nothing opens" defect.
    if ($shellOk) {
        $rc = Capture-Step -Job $job -Step "validate-pe-gui" -ToolContext "gate:validate-pe-gui" -Command {
            $exe = "bin\app-gogui.exe"
            $bytes = [System.IO.File]::ReadAllBytes($exe)
            if ($bytes.Length -lt 0x100) { Write-Output "ERROR: file too small for a PE ($($bytes.Length) bytes)"; exit 1 }
            if ($bytes[0] -ne 77 -or $bytes[1] -ne 90) { Write-Output "ERROR: not a PE executable (missing MZ header)"; exit 1 }
            $peOff = [BitConverter]::ToInt32($bytes, 0x3C)
            if ($peOff -le 0 -or ($peOff + 24 + 68) -gt $bytes.Length) { Write-Output "ERROR: implausible e_lfanew $peOff"; exit 1 }
            if ($bytes[$peOff] -ne 0x50 -or $bytes[$peOff+1] -ne 0x45 -or $bytes[$peOff+2] -ne 0 -or $bytes[$peOff+3] -ne 0) {
                Write-Output "ERROR: PE signature missing at e_lfanew"
                exit 1
            }
            $optOff = $peOff + 4 + 20
            $magic = [BitConverter]::ToUInt16($bytes, $optOff)
            if ($magic -ne 0x20B -and $magic -ne 0x10B) { Write-Output "ERROR: unexpected Optional Header magic 0x{0:X4}" -f $magic; exit 1 }
            $subsystem = [BitConverter]::ToUInt16($bytes, $optOff + 68)
            Write-Output ("PE optional-header magic: 0x{0:X4}; Subsystem: {1} (2 = IMAGE_SUBSYSTEM_WINDOWS_GUI, 3 = console)" -f $magic, $subsystem)
            if ($subsystem -ne 2) {
                Write-Output "ERROR: the product desktop shell is NOT a GUI-subsystem binary (-H=windowsgui missing or overridden)"
                exit 1
            }
            # Resource section presence: the windres-built .syso adds an
            # .rsrc section to the PE. Section names live in the section
            # table directly after the Optional Header.
            $numSections = [BitConverter]::ToUInt16($bytes, $peOff + 6)
            $optSize = [BitConverter]::ToUInt16($bytes, $peOff + 20)
            $secOff = $optOff + $optSize
            $hasResource = $false
            for ($i = 0; $i -lt $numSections; $i++) {
                $nameOff = $secOff + 40 * $i
                $name = [System.Text.Encoding]::ASCII.GetString($bytes, $nameOff, 8).TrimEnd([char]0)
                if ($name -eq ".rsrc") { $hasResource = $true }
            }
            if (-not $hasResource) {
                Write-Output "ERROR: product shell has no .rsrc section (icon/VERSIONINFO resource not embedded; windres step missing?)"
                exit 1
            }
            # The VERSIONINFO string table must contain the product name
            # in UTF-16LE (bounded whole-file byte search).
            $needle = [System.Text.Encoding]::Unicode.GetBytes("SHEYTAN Music Visualizer")
            $found = $false
            for ($i = 0; $i -le $bytes.Length - $needle.Length; $i++) {
                $ok = $true
                for ($j = 0; $j -lt $needle.Length; $j++) {
                    if ($bytes[$i + $j] -ne $needle[$j]) { $ok = $false; break }
                }
                if ($ok) { $found = $true; break }
            }
            if (-not $found) {
                Write-Output "ERROR: VERSIONINFO product name not found in the product shell resource"
                exit 1
            }
            Write-Output "validate-pe-gui PASSED: product shell subsystem = Windows GUI (2), .rsrc present, VERSIONINFO product name present"
            exit 0
        }
        if ($rc -ne 0) { Fail-Gate "validate-pe-gui" }
    } else {
        Write-Note "BLOCKED: gate validate-pe-gui (build-gogui-shell failed; no product shell binary to validate)"
        Skip-Gate "validate-pe-gui" "product shell build failed"
    }

    # PACKAGE ROOT CONTRACT (run 42837709122 gates B+C repair): the
    # archive must carry the APPLICATION directory as its single
    # top-level folder — never the staging wrapper (windows-x64-stage)
    # that CONTAINS it.
    # The previous `Compress-Archive -Path $stageRoot` archived the
    # wrapper itself, so every entry gained one extra outer path level
    # and both the entry manifest and the post-extraction license lookup
    # failed downstream. New-PortableZip writes the wrapper's CHILD app
    # directory as the package root with explicit, '/'-normalized entry
    # names, byte-exact file content and explicit directory entries
    # (empty tools/ffmpeg/bin survives). Linux parity: the TAR gate
    # archives the inner app directory directly (build-linux.sh
    # create-portable-tar), producing the identical single-root layout.
    $rc = Capture-Step -Job $job -Step "create-portable-zip" -ToolContext "gate:create-portable-zip" -RequireRepoRoot -RepoRootHelpers @("scripts/ci/New-PortableZip.ps1") -Command {
        . (Join-Path $repoRoot "scripts/ci/New-PortableZip.ps1")
        $stageRoot = Join-Path (Get-Location).Path "dist\windows-x64-stage"
        $zip = Join-Path (Get-Location).Path "dist\SHEYTAN-Music-Visualizer-windows-x64.zip"
        $mk = New-PortableZip -StageRoot $stageRoot -ZipPath $zip
        if ($mk.Error -ne "") {
            Write-Output "ERROR: portable ZIP creation failed: $($mk.Error)"
            exit 1
        }
        $zipItem = Get-Item -LiteralPath $zip
        if ($zipItem.Length -le 0) { Write-Output "ERROR: created ZIP is empty"; exit 1 }
        Write-Output ("zip entries: {0}; size: {1} bytes" -f $mk.Count, $zipItem.Length)
        Write-Output "--- FULL ENTRY MANIFEST ---"
        foreach ($e in $mk.Entries) { Write-Output "ENTRY: $e" }
        # COMPLETE manifest validation — not only a minimum count:
        #   1. exactly one top-level directory (the app folder),
        #   2. no staging-wrapper directory anywhere,
        #   3. no path-traversal or backslash entries,
        #   4. every required file present as an exact entry,
        #   5. the required directories present (data/, tools/ffmpeg/, tools/ffmpeg/bin/).
        $appRoot = "SHEYTAN-Music-Visualizer-windows-x64"
        $tops = @($mk.Entries | ForEach-Object { ($_ -split '/')[0] } | Sort-Object -Unique)
        if ($tops.Count -ne 1 -or $tops[0] -ne $appRoot) {
            Write-Output "ERROR: package root must be exactly one app directory '$appRoot'; observed top-level: $($tops -join ', ')"
            exit 1
        }
        $wrapper = $mk.Entries | Where-Object { $_ -match '(^|/)windows-x64-stage(/|$)' }
        if ($wrapper) {
            Write-Output "ERROR: staging wrapper directory leaked into the package:"
            $wrapper | ForEach-Object { Write-Output "  $_" }
            exit 1
        }
        $unsafe = $mk.Entries | Where-Object { $_ -match '(^|/)\.\.(/|$)' -or $_ -match '\\' -or $_.StartsWith('/') }
        if ($unsafe) {
            Write-Output "ERROR: unsafe package entries (path traversal/backslash):"; $unsafe | ForEach-Object { Write-Output "  $_" }
            exit 1
        }
        $required = @(
            "SHEYTAN-Music-Visualizer-windows-x64/sheytan-music-visualizer.exe",
            "SHEYTAN-Music-Visualizer-windows-x64/license.md",
            "SHEYTAN-Music-Visualizer-windows-x64/readme.md",
            "SHEYTAN-Music-Visualizer-windows-x64/portable.mode",
            "SHEYTAN-Music-Visualizer-windows-x64/tools/ffmpeg/FFMPEG-TOOLS-LICENSING.md",
            "SHEYTAN-Music-Visualizer-windows-x64/data/DATA-FOLDERS.txt"
        )
        foreach ($req in $required) {
            if ($mk.Entries -notcontains $req) {
                Write-Output "ERROR: zip is missing required package entry: $req"
                exit 1
            }
        }
        $requiredDirs = @(
            "SHEYTAN-Music-Visualizer-windows-x64/data/",
            "SHEYTAN-Music-Visualizer-windows-x64/tools/ffmpeg/",
            "SHEYTAN-Music-Visualizer-windows-x64/tools/ffmpeg/bin/"
        )
        foreach ($reqDir in $requiredDirs) {
            if ($mk.Entries -notcontains $reqDir) {
                Write-Output "ERROR: zip is missing required package directory: $reqDir"
                exit 1
            }
        }
        Write-Output "zip package manifest verified: single app-directory root, wrapper excluded, no traversal, all required files and directories present"
        exit 0
    }
    if ($rc -ne 0) { Fail-Gate "create-portable-zip" }

    # POST-ARCHIVE license + package verification (run 42481815519
    # contract): extract the produced ZIP and compare the distributed
    # license.md bytes against the canonical tracked file. A staged
    # check alone does not prove what a user receives.
    # POST-ARCHIVE PACKAGE VERIFICATION (run 42837709122 gate C repair):
    # the exact produced ZIP is extracted to a FRESH temporary directory
    # and every required path is verified there, the extracted EXE gets
    # the full PE header check (MZ + Optional Header subsystem = 2,
    # IMAGE_SUBSYSTEM_WINDOWS_GUI), the distributed license.md is
    # byte-compared (hash + length) against the canonical tracked file,
    # and the extracted executable must run --version and --self-check
    # through the robust process runner with the machine-readable
    # result file validated. This gate is downstream of the archive
    # root layout: with the run's defect the app files landed one level
    # deeper and the license lookup failed even though the staged copy
    # was byte-identical — the license itself was never wrong.
    $rc = Capture-Step -Job $job -Step "verify-extracted-package" -ToolContext "gate:package-verify" -RequireRepoRoot -RepoRootHelpers @("scripts/ci/Invoke-CapturedProcess.ps1") -Command {
        . (Join-Path $repoRoot "scripts/ci/Invoke-CapturedProcess.ps1")
        $zip = Join-Path (Get-Location).Path "dist\SHEYTAN-Music-Visualizer-windows-x64.zip"
        $extractRoot = Join-Path ([System.IO.Path]::GetTempPath()) ("sheytan-pkg-verify-" + [guid]::NewGuid().ToString("N"))
        New-Item -ItemType Directory -Force -Path $extractRoot | Out-Null
        $cleanup = { if (Test-Path -LiteralPath $extractRoot) { Remove-Item -Recurse -Force $extractRoot } }
        Expand-Archive -Path $zip -DestinationPath $extractRoot
        $appRoot = Join-Path $extractRoot "SHEYTAN-Music-Visualizer-windows-x64"
        # 1. COMPLETE required-path manifest after extraction.
        # RUN 45297707372: the product desktop shell (app-gogui.exe) and
        # the shell icon (icon.png) are REQUIRED package contents - a
        # CLI-only package is the exact defect that shipped a product
        # that "did not open".
        $requiredPaths = @(
            "app-gogui.exe",
            "sheytan-music-visualizer.exe",
            "icon.png",
            "license.md",
            "readme.md",
            "portable.mode",
            "tools\ffmpeg\FFMPEG-TOOLS-LICENSING.md",
            "data\DATA-FOLDERS.txt",
            "tools\ffmpeg\bin"
        )
        $missing = @()
        foreach ($rel in $requiredPaths) {
            $p = Join-Path $appRoot $rel
            if (-not (Test-Path -LiteralPath $p)) { $missing += $rel }
        }
        if ($missing.Count -gt 0) {
            Write-Output "ERROR: extracted package is missing required paths:"
            $missing | ForEach-Object { Write-Output "  MISSING: $_" }
            Write-Output "actual extracted tree:"
            Get-ChildItem -Recurse -Force $extractRoot | ForEach-Object {
                Write-Output ("    {0}" -f $_.FullName.Substring($extractRoot.Length + 1))
            }
            & $cleanup; exit 1
        }
        # No staging wrapper may exist inside the extraction either.
        $wrapperHit = Get-ChildItem -Recurse -Directory $extractRoot |
            Where-Object { $_.Name -eq "windows-x64-stage" }
        if ($wrapperHit) {
            Write-Output "ERROR: staging wrapper directory present inside the extracted package"
            & $cleanup; exit 1
        }
        # 2. Distributed license byte identity (hash AND length).
        $canonical = Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $repoRoot "docs/license.md")
        $canonicalLen = (Get-Item -LiteralPath (Join-Path $repoRoot "docs/license.md")).Length
        $extractedLicense = Join-Path $appRoot "license.md"
        $dist = Get-FileHash -Algorithm SHA256 -LiteralPath $extractedLicense
        $distLen = (Get-Item -LiteralPath $extractedLicense).Length
        Write-Output ("canonical sha256: {0} ({1} bytes)" -f $canonical.Hash, $canonicalLen)
        Write-Output ("extracted sha256: {0} ({1} bytes)" -f $dist.Hash, $distLen)
        if ($canonical.Hash -ne $dist.Hash -or $canonicalLen -ne $distLen) {
            Write-Output "ERROR: DISTRIBUTED license.md differs from the canonical tracked license.md (post-archive byte comparison FAILED)"
            & $cleanup; exit 1
        }
        # 3. Extracted executable: full PE header check, not only MZ.
        $exe = Join-Path $appRoot "sheytan-music-visualizer.exe"
        $bytes = [System.IO.File]::ReadAllBytes($exe)
        if ($bytes.Length -lt 0x100) { Write-Output "ERROR: extracted file too small for a PE ($($bytes.Length) bytes)"; & $cleanup; exit 1 }
        if ($bytes[0] -ne 77 -or $bytes[1] -ne 90) {
            Write-Output "ERROR: extracted executable is not a PE (missing MZ header)"
            & $cleanup; exit 1
        }
        $peOff = [BitConverter]::ToInt32($bytes, 0x3C)
        $optOff = $peOff + 4 + 20
        $subsystem = [BitConverter]::ToUInt16($bytes, $optOff + 68)
        Write-Output ("extracted EXE PE Optional Header Subsystem: {0} (3 = console)" -f $subsystem)
        if ($subsystem -ne 3) {
            Write-Output "ERROR: extracted CLI authority must be a console-subsystem binary (subsystem 3)"
            & $cleanup; exit 1
        }
        # 3b. Extracted PRODUCT SHELL: GUI subsystem + embedded resource
        # (run 45297707372 ship-the-shell gate, on the archived bytes).
        $shellExe = Join-Path $appRoot "app-gogui.exe"
        $shellBytes = [System.IO.File]::ReadAllBytes($shellExe)
        if ($shellBytes.Length -lt 0x100) { Write-Output "ERROR: extracted app-gogui.exe too small for a PE"; & $cleanup; exit 1 }
        $shellPeOff = [BitConverter]::ToInt32($shellBytes, 0x3C)
        $shellOptOff = $shellPeOff + 4 + 20
        $shellSubsystem = [BitConverter]::ToUInt16($shellBytes, $shellOptOff + 68)
        Write-Output ("extracted app-gogui.exe PE Optional Header Subsystem: {0} (2 = GUI)" -f $shellSubsystem)
        if ($shellSubsystem -ne 2) {
            Write-Output "ERROR: extracted product shell lost the Windows GUI subsystem (must stay -H=windowsgui)"
            & $cleanup; exit 1
        }
        $shellNumSections = [BitConverter]::ToUInt16($shellBytes, $shellPeOff + 6)
        $shellOptSize = [BitConverter]::ToUInt16($shellBytes, $shellPeOff + 20)
        $shellSecOff = $shellOptOff + $shellOptSize
        $shellHasResource = $false
        for ($i = 0; $i -lt $shellNumSections; $i++) {
            $nameOff = $shellSecOff + 40 * $i
            $name = [System.Text.Encoding]::ASCII.GetString($shellBytes, $nameOff, 8).TrimEnd([char]0)
            if ($name -eq ".rsrc") { $shellHasResource = $true }
        }
        if (-not $shellHasResource) {
            Write-Output "ERROR: extracted product shell has no .rsrc section (icon/VERSIONINFO lost)"
            & $cleanup; exit 1
        }
        Write-Output "extracted product shell: GUI subsystem + .rsrc resource verified from the archived bytes"
        # 4. Runtime contract on the EXTRACTED binary via the robust
        #    runner: real exit codes, captured streams, machine-readable
        #    self-check file. The package must RUN, not merely exist.
        $verRun = Invoke-CapturedProcess -FilePath $exe -ArgumentList @("--version") -TimeoutSeconds 120
        if ($verRun.Error -ne "") {
            Write-Output "ERROR: extracted EXE --version observation incomplete: $($verRun.Error)"
            & $cleanup; exit 1
        }
        Write-Output ("extracted EXE --version exit code: {0}" -f $verRun.ExitCode)
        if ($verRun.ExitCode -ne 0) {
            Write-Output "ERROR: extracted EXE --version exited with $($verRun.ExitCode)"
            & $cleanup; exit 1
        }
        $verLine = ($verRun.StdOut | Select-Object -First 1)
        Write-Output "extracted EXE version line: $verLine"
        if ($env:SHEYTAN_BUILD_COMMIT -and -not [string]::IsNullOrWhiteSpace($env:SHEYTAN_BUILD_COMMIT)) {
            $want = $env:SHEYTAN_BUILD_COMMIT.Trim()
            if ($verLine -notlike "*commit $want,*") {
                Write-Output "ERROR: extracted EXE commit mismatch (want $want)"
                & $cleanup; exit 1
            }
        }
        $selfOutFile = Join-Path $extractRoot "self-check.txt"
        $selfRun = Invoke-CapturedProcess -FilePath $exe -ArgumentList @("--self-check", "--self-check-out", $selfOutFile) -TimeoutSeconds 180
        if ($selfRun.Error -ne "") {
            Write-Output "ERROR: extracted EXE --self-check observation incomplete: $($selfRun.Error)"
            & $cleanup; exit 1
        }
        Write-Output ("extracted EXE --self-check exit code: {0}" -f $selfRun.ExitCode)
        if ($selfRun.ExitCode -ne 0) {
            Write-Output "ERROR: extracted EXE --self-check exited with $($selfRun.ExitCode)"
            & $cleanup; exit 1
        }
        if (-not (Test-Path -LiteralPath $selfOutFile)) {
            Write-Output "ERROR: extracted EXE --self-check wrote no result file"
            & $cleanup; exit 1
        }
        $selfText = Get-Content -LiteralPath $selfOutFile
        if (-not ($selfText | Where-Object { $_ -match 'self-check: all foundation invariants hold' })) {
            Write-Output "ERROR: extracted EXE self-check result file lacks the passing verdict"
            & $cleanup; exit 1
        }
        & $cleanup
        Write-Output "extracted package verified: complete required-path manifest, no wrapper, license byte-identical, PE GUI subsystem 2, extracted EXE ran --version and self-check with valid result file"
        exit 0
    }
    if ($rc -ne 0) { Fail-Gate "verify-extracted-package" }

    # ---- NEGATIVE PACKAGE GATE: no bundled media tools (run 43712071111) --
    # The distributed archive must contain NO ffmpeg/ffprobe binary, no
    # nested archive, no DLL under tools/ and no oversized single file:
    # media tools are app-managed downloads (the in-app Media Tools &
    # Codecs manager installs the pinned pair under
    # <DataRoot>\media-tools\current\bin, never into the package). The
    # reusable helper extracts the produced ZIP and scans BOTH the raw
    # entry list and the extracted tree, always printing the archive
    # size, packaged file count and top-10 largest files so a failure
    # is diagnosable from the captured log alone.
    $rc = Capture-Step -Job $job -Step "forbid-bundled-media-tools" -ToolContext "gate:forbid-bundled-media-tools (negative: zero bundled media tools)" -RequireRepoRoot -RepoRootHelpers @("scripts/ci/Forbid-BundledMediaTools.ps1") -Command {
        $zip = Join-Path (Get-Location).Path "dist\SHEYTAN-Music-Visualizer-windows-x64.zip"
        if (-not (Test-Path -LiteralPath $zip -PathType Leaf)) {
            Write-Output "ERROR: package ZIP not found: $zip (nothing to scan)"
            exit 1
        }
        & (Join-Path $repoRoot "scripts/ci/Forbid-BundledMediaTools.ps1") -ArchivePath $zip
        $helperRc = $LASTEXITCODE
        if ($helperRc -ne 0) {
            Write-Output "ERROR: forbid-bundled-media-tools FAILED (helper exit code $helperRc): the package must ship zero media-tool binaries"
            exit 1
        }
        exit 0
    }
    if ($rc -ne 0) { Fail-Gate "forbid-bundled-media-tools" }

    # ---- PACKAGE SIZE BUDGET (run 43712071111) ------------------------------
    # The compact package (no bundled media tools) must stay UNDER
    # 90 MiB = 94371840 bytes. Bundling FFmpeg binaries historically
    # pushed the archive far past this budget; the budget now fails the
    # build loudly with the exact byte count instead of shipping a
    # bloated archive silently.
    $rc = Capture-Step -Job $job -Step "package-size-budget" -ToolContext "gate:package-size-budget (90 MiB = 94371840 bytes)" -Command {
        $zip = Join-Path (Get-Location).Path "dist\SHEYTAN-Music-Visualizer-windows-x64.zip"
        if (-not (Test-Path -LiteralPath $zip -PathType Leaf)) {
            Write-Output "ERROR: package ZIP not found: $zip"
            exit 1
        }
        $bytes = (Get-Item -LiteralPath $zip).Length
        $budget = 94371840
        Write-Output ("package:      {0}" -f $zip)
        Write-Output ("package size: {0} bytes" -f $bytes)
        Write-Output ("size budget:  {0} bytes (90 MiB)" -f $budget)
        if ($bytes -ge $budget) {
            Write-Output ("ERROR: package size {0} bytes meets or exceeds the {1}-byte budget (90 MiB)" -f $bytes, $budget)
            exit 1
        }
        Write-Output ("package-size-budget PASSED: {0} bytes < {1} bytes ({2:N2} MiB of the 90 MiB budget)" -f $bytes, $budget, ($bytes / 1MB))
        exit 0
    }
    if ($rc -ne 0) { Fail-Gate "package-size-budget" }

    # ---- media-tools downloader fixture gate (run 43712071111) -------------
    # The in-app Media Tools & Codecs manager (internal/mediatools)
    # downloads and installs the pinned pair at runtime. Its fixture
    # suite proves the downloader, resolution order and layout contracts
    # WITHOUT network access; the env-gated real-download integration
    # test is owned by media-tools-pinned-install below.
    $rc = Capture-Step -Job $job -Step "media-tools-downloader-tests" -ToolContext "gate:media-tools-downloader-tests (internal/mediatools fixtures)" -GoTags -Command {
        go test $goTags -count=1 ./internal/mediatools/
        $tRc = $LASTEXITCODE
        if ($tRc -ne 0) {
            Write-Output "ERROR: internal/mediatools fixture suite failed (exit $tRc) — the app-managed downloader is not proven"
            exit 1
        }
        Write-Output "media-tools downloader fixtures PASSED"
        exit 0
    }
    if ($rc -ne 0) { Fail-Gate "media-tools-downloader-tests" }

    # ---- pinned media-tools install integration gate (run 43712071111) -----
    # Downloads the PINNED BtbN win64 GPL asset ONCE into the runner
    # temp cache (outside repo/dist; never bundled into any artifact),
    # SHA-256-verifies it, then runs the env-gated integration test
    # TestMediaToolsInstallFromPinnedArchive with
    # SHEYTAN_TEST_MEDIA_TOOLS_ARCHIVE/SHEYTAN_TEST_MEDIA_TOOLS_SHA256
    # pointing at the verified archive. A download or hash failure fails
    # the gate; the package can then reach TEST_ONLY at best (it still
    # ships no binaries — that is enforced by the negative gates above).
    $rc = Capture-Step -Job $job -Step "media-tools-pinned-install" -ToolContext "gate:media-tools-pinned-install (pinned BtbN win64 asset + env-gated integration test)" -GoTags -RequireRepoRoot -RepoRootHelpers @("scripts/ci/MediaTools-Pinned.ps1") -Command {
        . (Join-Path $repoRoot "scripts/ci/MediaTools-Pinned.ps1")
        # Run-43767556517 repair: the resolver returns ONE schema-validated
        # [pscustomobject]; every consumer reads the authoritative fields
        # (Version, AssetURL, ArchivePath, SHA256, BinaryDir, FFmpegPath,
        # FFprobePath, Platform, ArchiveFormat) - never ad-hoc names.
        $tools = Get-SheytanPinnedMediaTools
        Write-Output ("pinned asset:   {0}" -f $tools.AssetURL)
        Write-Output ("pinned archive: {0} (SHA-256 verified)" -f $tools.ArchivePath)
        Write-Output ("pinned sha256:  {0}" -f $tools.SHA256)
        Write-Output ("pinned bin dir: {0}" -f $tools.BinaryDir)
        $env:SHEYTAN_TEST_MEDIA_TOOLS_ARCHIVE = $tools.ArchivePath
        $env:SHEYTAN_TEST_MEDIA_TOOLS_SHA256 = $tools.SHA256
        try {
            go test $goTags -count=1 -run 'TestMediaToolsInstallFromPinnedArchive' ./internal/mediatools/ -v
            $tRc = $LASTEXITCODE
        }
        finally {
            Remove-Item Env:SHEYTAN_TEST_MEDIA_TOOLS_ARCHIVE -ErrorAction SilentlyContinue
            Remove-Item Env:SHEYTAN_TEST_MEDIA_TOOLS_SHA256 -ErrorAction SilentlyContinue
        }
        if ($tRc -ne 0) {
            Write-Output "ERROR: TestMediaToolsInstallFromPinnedArchive failed (exit $tRc) — the app-managed install path is not proven"
            exit 1
        }
        Write-Output "media-tools pinned install integration PASSED (pinned archive SHA-256 verified, installer test green)"
        exit 0
    }
    if ($rc -ne 0) { Fail-Gate "media-tools-pinned-install" }
}
else {
    Skip-Gate "run-version" "executable build failed"
    Skip-Gate "run-self-check" "executable build failed"
    Skip-Gate "verify-build-metadata" "executable build failed"
    Skip-Gate "stage-portable" "executable build failed"
    Skip-Gate "verify-package-license" "executable build failed"
    Skip-Gate "validate-pe" "executable build failed"
    Skip-Gate "create-portable-zip" "executable build failed"
    Skip-Gate "verify-extracted-package" "executable build failed"
    Skip-Gate "forbid-bundled-media-tools" "executable build failed"
    Skip-Gate "package-size-budget" "executable build failed"
    Skip-Gate "media-tools-downloader-tests" "executable build failed"
    Skip-Gate "media-tools-pinned-install" "executable build failed"
    Skip-Gate "verify-extracted-path-with-spaces" "executable build failed"
}

# ---- dedicated GPU integration gate (Dedicated owner: TestPaletteReachesGPUShader)
# run 42939743766 §2.4: the palette end-to-end proof runs EXACTLY ONCE per
# platform, here on Windows, with verbose output and its readback
# diagnostics. It is excluded from go-test by its exact name — NOT skipped
# as unreliable. Honest adapter note: hosted runners exercise the
# project's supported software adapter (labelled noop/software, never a
# physical GPU); hardware validation stays a separate, documented activity.
$rc = Capture-Step -Job $job -Step "gpu-validation" -ToolContext "gate:gpu-validation (dedicated palette owner)" -GoTags -Command { go test $goTags -timeout 240s -v -run '^TestPaletteReachesGPUShader$' -count=1 ./internal/render/ }
if ($rc -ne 0) { Fail-Gate "gpu-validation" }

# ---- path-with-spaces runtime check + package-status record ----------------
# The package-status record (run 43712071111 CONTRACT: persisted
# evidence only) is machine-readable state for the workflow upload
# decision, written by scripts/ci/write-package-status.py after the
# gates below have persisted their verdicts. VERIFIED only when every
# package-integrity gate passed AND the expected commit (immutable
# github.sha context) exactly matches the embedded build metadata of
# the built AND the extracted executable, read from --build-info-json
# (typed JSON, never console-text regexes) AND the compact-package
# proofs hold (zero bundled media tools, downloader fixture PASS,
# app-managed h264 + hevc software export proofs PASS). TEST_ONLY
# keeps the job red but preserves a testable package; UNAVAILABLE is
# never uploaded.
$script:commitExpected = $env:SHEYTAN_BUILD_COMMIT
$script:commitObserved = "not-observed"
$script:commitVerified = $false
$script:commitExtractedObserved = "not-observed"
$script:commitReason = "package missing or not built"
$exeExists = Test-Path "bin\sheytan-music-visualizer.exe"
$zipPath = Join-Path (Get-Location).Path "dist\SHEYTAN-Music-Visualizer-windows-x64.zip"
$zipExists = (Test-Path -LiteralPath $zipPath) -and ((Get-Item -LiteralPath $zipPath).Length -gt 0)
# Package-integrity gates run whenever a package exists, INCLUDING runs
# with earlier unrelated failures: a healthy executable must stay
# uploadable as TEST_ONLY instead of being withheld as UNAVAILABLE.
if ($exeExists -and $zipExists) {
    $rc = Capture-Step -Job $job -Step "verify-extracted-path-with-spaces" -ToolContext "gate:package-verify (path with spaces + app-managed media-tools export proofs)" -RequireRepoRoot -RepoRootHelpers @("scripts/ci/Invoke-CapturedProcess.ps1", "scripts/ci/MediaTools-Pinned.ps1") -Command {
        . (Join-Path $repoRoot "scripts/ci/Invoke-CapturedProcess.ps1")
        . (Join-Path $repoRoot "scripts/ci/MediaTools-Pinned.ps1")
        $zip = Join-Path (Get-Location).Path "dist\SHEYTAN-Music-Visualizer-windows-x64.zip"
        $base = Join-Path ([System.IO.Path]::GetTempPath()) ("SHEYTAN Spaces Dir " + [guid]::NewGuid().ToString("N"))
        New-Item -ItemType Directory -Force -Path $base | Out-Null
        $cleanup = { if (Test-Path -LiteralPath $base) { Remove-Item -Recurse -Force $base } }
        # Persisted proof files ("PASS <out>" / "FAIL <reason>") consumed
        # by scripts/ci/write-package-status.py (run 43712071111 schema).
        $proofH264 = "ci-logs\export-proof-h264.result.txt"
        $proofH265 = "ci-logs\export-proof-h265.result.txt"
        foreach ($pf in @($proofH264, $proofH265)) {
            if (Test-Path -LiteralPath $pf) { Remove-Item -Force -LiteralPath $pf }
        }
        try {
            Expand-Archive -Path $zip -DestinationPath $base
            $exe = Join-Path $base "SHEYTAN-Music-Visualizer-windows-x64\sheytan-music-visualizer.exe"
            if (-not (Test-Path -LiteralPath $exe)) { Write-Output "ERROR: EXE missing after spaces-path extraction"; exit 1 }
            $verRun = Invoke-CapturedProcess -FilePath $exe -ArgumentList @("--version") -TimeoutSeconds 120
            if ($verRun.Kind -ne 'exited' -or $verRun.ExitCode -ne 0) {
                Write-Output ("ERROR: extracted EXE --version failed from a path containing spaces (kind={0} code={1})" -f $verRun.Kind, $verRun.ExitCode)
                exit 1
            }
            Write-Output "spaces-path runtime check PASSED"

            # APP-MANAGED MEDIA-TOOLS EXPORT PROOF (run 43712071111): the
            # package ships NO binaries, so the proof installs the PINNED
            # pair into the app-managed location
            # <DataRoot>\media-tools\current\bin\ and runs the EXTRACTED
            # app with SHEYTAN_DATA_DIR pointing at that data root, with
            # PATH scrubbed of every FFmpeg-carrying directory and
            # SHEYTAN_FFMPEG unset — the app MUST resolve its own
            # app-managed pair. Both codecs (h264 default, hevc software)
            # are validated with the app-managed ffprobe.
            $tools = Get-SheytanPinnedMediaTools
            $dataDir = Join-Path $base "SHEYTAN App Data Dir"
            $managedBin = Join-Path $dataDir "media-tools\current\bin"
            New-Item -ItemType Directory -Force -Path $managedBin | Out-Null
            # Run-43767556517 repair: use the resolver's VALIDATED
            # FFmpegPath/FFprobePath (never ambient PATH binaries,
            # never re-derived file names from a stale schema).
            Copy-Item -Force $tools.FFmpegPath (Join-Path $managedBin "ffmpeg.exe")
            Copy-Item -Force $tools.FFprobePath (Join-Path $managedBin "ffprobe.exe")
            Write-Output ("app-managed tools provisioned from the pinned asset: {0}" -f $managedBin)
            $managedFfprobe = Join-Path $managedBin "ffprobe.exe"

            # Keep DLL directories intact: only PATH entries that actually
            # contain an ffmpeg.exe/ffprobe.exe binary are dropped.
            $keptEntries = @($env:PATH -split ';' |
                Where-Object { $_ -ne '' -and
                    -not (Test-Path -LiteralPath (Join-Path $_ "ffmpeg.exe") -PathType Leaf) -and
                    -not (Test-Path -LiteralPath (Join-Path $_ "ffprobe.exe") -PathType Leaf) })
            $scrubbedPath = $keptEntries -join ';'
            Write-Output ("PATH scrubbed of FFmpeg-carrying directories for the export proof ({0} of {1} entries kept)" -f $keptEntries.Count, @($env:PATH -split ';' | Where-Object { $_ -ne '' }).Count)

            foreach ($codec in @("h264", "hevc")) {
                $outMp4 = Join-Path $base ("packaged-export-proof-{0}.mp4" -f $codec)
                $proofFile = if ($codec -eq "h264") { $proofH264 } else { $proofH265 }
                $exArgs = @("--export-cli", "--export-out", $outMp4, "--export-w", "640", "--export-h", "360",
                    "--export-fps", "30", "--export-seconds", "1", "--export-codec", $codec)
                $oldDataDir = $env:SHEYTAN_DATA_DIR
                $oldPath = $env:PATH
                $oldFfmpeg = $env:SHEYTAN_FFMPEG
                try {
                    $env:SHEYTAN_DATA_DIR = $dataDir
                    $env:PATH = $scrubbedPath
                    if (Test-Path Env:SHEYTAN_FFMPEG) { Remove-Item Env:SHEYTAN_FFMPEG }
                    $exRun = Invoke-CapturedProcess -FilePath $exe -ArgumentList $exArgs -TimeoutSeconds 600
                }
                finally {
                    if ($null -ne $oldDataDir) { $env:SHEYTAN_DATA_DIR = $oldDataDir } else { Remove-Item Env:SHEYTAN_DATA_DIR -ErrorAction SilentlyContinue }
                    if ($null -ne $oldPath) { $env:PATH = $oldPath }
                    if ($null -ne $oldFfmpeg) { $env:SHEYTAN_FFMPEG = $oldFfmpeg } else { Remove-Item Env:SHEYTAN_FFMPEG -ErrorAction SilentlyContinue }
                }
                if ($exRun.Error -ne "" -or $exRun.Kind -ne 'exited' -or $exRun.ExitCode -ne 0) {
                    $reason = ("app-managed export --export-codec {0} failed with scrubbed PATH (kind={1} code={2} err={3})" -f $codec, $exRun.Kind, $exRun.ExitCode, $exRun.Error)
                    if ($exRun.StdErr) { $exRun.StdErr | Select-Object -First 5 | ForEach-Object { Write-Output ("  stderr: {0}" -f $_) } }
                    Write-Output "FAIL $reason" | Set-Content -Path $proofFile -Encoding utf8NoBOM
                    Write-Output ("ERROR: {0}" -f $reason)
                    exit 1
                }
                if (-not (Test-Path -LiteralPath $outMp4) -or (Get-Item -LiteralPath $outMp4).Length -le 0) {
                    $reason = "app-managed export --export-codec $codec produced no output file"
                    Write-Output "FAIL $reason" | Set-Content -Path $proofFile -Encoding utf8NoBOM
                    Write-Output ("ERROR: {0}" -f $reason)
                    exit 1
                }
                $outBytes = (Get-Item -LiteralPath $outMp4).Length
                # Validate with the APP-MANAGED ffprobe (the same pair the
                # app resolved): expected codec, ~30 frames, ~1 s, size > 0.
                $probeRun = Invoke-CapturedProcess -FilePath $managedFfprobe -ArgumentList @(
                    "-v", "error", "-count_frames", "-select_streams", "v:0",
                    "-show_entries", "stream=codec_name,width,height,nb_read_frames",
                    "-show_entries", "format=duration", "-of", "json", $outMp4) -TimeoutSeconds 120
                if ($probeRun.Error -ne "" -or $probeRun.ExitCode -ne 0) {
                    $reason = ("app-managed ffprobe validation failed for {0} (code={1} err={2})" -f $codec, $probeRun.ExitCode, $probeRun.Error)
                    Write-Output "FAIL $reason" | Set-Content -Path $proofFile -Encoding utf8NoBOM
                    Write-Output ("ERROR: {0}" -f $reason)
                    exit 1
                }
                $codecName = ""
                $frames = 0
                $duration = 0.0
                try {
                    $probeJson = (($probeRun.StdOut -join "`n") | ConvertFrom-Json)
                    if ($null -ne $probeJson.streams -and $probeJson.streams.Count -gt 0) {
                        $codecName = [string]$probeJson.streams[0].codec_name
                        $frames = [int]$probeJson.streams[0].nb_read_frames
                    }
                    if ($null -ne $probeJson.format) { $duration = [double]$probeJson.format.duration }
                }
                catch {
                    $reason = "app-managed ffprobe output for $codec could not be parsed: $($_.Exception.Message)"
                    Write-Output "FAIL $reason" | Set-Content -Path $proofFile -Encoding utf8NoBOM
                    Write-Output ("ERROR: {0}" -f $reason)
                    exit 1
                }
                Write-Output ("  {0}: codec={1} frames={2} duration={3:N3}s size={4} bytes" -f $codec, $codecName, $frames, $duration, $outBytes)
                $proofFailures = @()
                if ($codecName -ne $codec) { $proofFailures += "codec '$codecName' does not match the requested '$codec'" }
                if ($frames -lt 28 -or $frames -gt 32) { $proofFailures += "frame count $frames is not ~30 (30 fps x 1 s)" }
                if ($duration -lt 0.8 -or $duration -gt 1.25) { $proofFailures += "duration {0:N3}s is not ~1 s" -f $duration }
                if ($outBytes -le 0) { $proofFailures += "output is empty" }
                if ($proofFailures.Count -gt 0) {
                    $reason = "app-managed export --export-codec $codec failed validation: " + ($proofFailures -join "; ")
                    Write-Output "FAIL $reason" | Set-Content -Path $proofFile -Encoding utf8NoBOM
                    Write-Output ("ERROR: {0}" -f $reason)
                    exit 1
                }
                Write-Output "PASS $outMp4" | Set-Content -Path $proofFile -Encoding utf8NoBOM
                Write-Output ("app-managed export proof PASSED: {0} ({1})" -f $codec, $outMp4)
            }
            Write-Output "spaces-path + app-managed media-tools export proofs PASSED (h264 + hevc software; SHEYTAN_DATA_DIR app-managed pair; PATH scrubbed of system FFmpeg; app-managed ffprobe validation)"
            exit 0
        }
        catch {
            # Provisioning/parse failures also leave truthful FAIL records
            # (only for proofs that have not already been written).
            $reason = $_.Exception.Message
            foreach ($pf in @($proofH264, $proofH265)) {
                if (-not (Test-Path -LiteralPath $pf)) { Write-Output "FAIL $reason" | Set-Content -Path $pf -Encoding utf8NoBOM }
            }
            Write-Output ("ERROR: verify-extracted-path-with-spaces failed: {0}" -f $reason)
            exit 1
        }
        finally { & $cleanup }
    }
    if ($rc -ne 0) { Fail-Gate "verify-extracted-path-with-spaces" }

    # COMMIT OBSERVER (run 43090342995 item D repair): the observed commit
    # comes exclusively from the machine-readable --build-info-json output
    # of BOTH the built executable and the extracted-package executable,
    # parsed as typed JSON via scripts/ci/Get-PackageCommitStatus.ps1.
    # Human --version output stays user-facing only; console-capture quirks
    # of a GUI-subsystem process can no longer produce a false
    # not-observed while the embedded metadata is intact.
    . (Join-Path $repoRoot "scripts/ci/Invoke-CapturedProcess.ps1")
    . (Join-Path $repoRoot "scripts/ci/Get-PackageCommitStatus.ps1")
    $biArgs = @("--build-info-json")
    $biRun = Invoke-CapturedProcess -FilePath "bin\sheytan-music-visualizer.exe" -ArgumentList $biArgs -TimeoutSeconds 120
    $biStdOut = if ($biRun.StdOut) { ($biRun.StdOut -join "`n") } else { "" }
    $builtStatus = Get-SheytanPackageCommitStatus -ExpectedCommit $script:commitExpected `
        -BuildInfoStdOut $biStdOut -ProcessExitCode $biRun.ExitCode -Source "built-exe"
    Write-Output ("commit observer (built-exe): observed={0} verified={1} reason={2}" -f $builtStatus.observed, $builtStatus.verified, $builtStatus.reason)

    # The extracted package must carry the identical embedded identity.
    $extractDir = Join-Path ([System.IO.Path]::GetTempPath()) ("SHEYTAN StatusExtract " + [guid]::NewGuid().ToString("N"))
    $extractedStatus = Get-SheytanPackageCommitStatus -ExpectedCommit $script:commitExpected `
        -BuildInfoStdOut "" -ProcessExitCode 1 -Source "extracted-package"
    try {
        New-Item -ItemType Directory -Force -Path $extractDir | Out-Null
        Expand-Archive -Path $zipPath -DestinationPath $extractDir
        $extractedExe = Join-Path $extractDir "SHEYTAN-Music-Visualizer-windows-x64\sheytan-music-visualizer.exe"
        if (Test-Path -LiteralPath $extractedExe) {
            $exRun = Invoke-CapturedProcess -FilePath $extractedExe -ArgumentList $biArgs -TimeoutSeconds 120
            $exStdOut = if ($exRun.StdOut) { ($exRun.StdOut -join "`n") } else { "" }
            $extractedStatus = Get-SheytanPackageCommitStatus -ExpectedCommit $script:commitExpected `
                -BuildInfoStdOut $exStdOut -ProcessExitCode $exRun.ExitCode -Source "extracted-package"
            Write-Output ("commit observer (extracted-package): observed={0} verified={1} reason={2}" -f $extractedStatus.observed, $extractedStatus.verified, $extractedStatus.reason)
        } else {
            $extractedStatus = Get-SheytanPackageCommitStatus -ExpectedCommit $script:commitExpected `
                -BuildInfoStdOut "" -ProcessExitCode 1 -Source "extracted-package"
            Write-Output "ERROR: extracted package executable missing; commit identity of the shipped package is unproven"
        }
    }
    finally { if (Test-Path -LiteralPath $extractDir) { Remove-Item -Recurse -Force $extractDir -ErrorAction SilentlyContinue } }

    $script:commitObserved = $builtStatus.observed
    $script:commitVerified = [bool]($builtStatus.verified -and $extractedStatus.verified)
    $script:commitExtractedObserved = $extractedStatus.observed
    $script:commitReason = if ($builtStatus.verified -and $extractedStatus.verified) { "" } else { $builtStatus.reason + " | extracted: " + $extractedStatus.reason }
}
else {
    Skip-Gate "verify-extracted-path-with-spaces" "package not produced (executable or ZIP missing)"
}

# ---- package-status record from PERSISTED EVIDENCE (run 43712071111) ------
# CONTRACT: ci-logs/package-status-windows.json is derived by the
# canonical writer scripts/ci/write-package-status.py from PERSISTED
# evidence ONLY — the per-gate verdict JSONs in
# ci-logs\gate-results\<gate>.result.json, the export-proof result
# files (export-proof-h264/h265.result.txt) and an actual scan of the
# produced ZIP. In-memory parent variables are NEVER consulted: every
# Capture-Step gate runs in a CHILD pwsh process, so parent-script
# variables can never observe a gate's outcome (the previous in-parent
# status derivation was exactly that defect class — a staging gate
# flipped a parent variable inside a child that the parent never saw,
# so the record claimed "not staged" while the ZIP actually carried
# the binaries). The writer folds commit verification, package-gate
# failures, the bundled-media-tools scan and the media/export proofs
# into VERIFIED / TEST_ONLY / UNAVAILABLE semantics.
$pyWriter = Get-Command python3 -ErrorAction SilentlyContinue
if ($null -eq $pyWriter) { $pyWriter = Get-Command python -ErrorAction SilentlyContinue }
if ($null -eq $pyWriter) {
    Write-Output "ERROR: python3/python not found on PATH — ci-logs/package-status-windows.json cannot be written; failing closed (never fabricate a status record)"
    Fail-Gate "package-status-record"
}
else {
    $statusArgs = @(
        (Join-Path $repoRoot "scripts/ci/write-package-status.py"),
        "--platform", "windows-x64",
        "--package", $zipPath,
        "--package-file", "dist/SHEYTAN-Music-Visualizer-windows-x64.zip",
        "--app-dir", "SHEYTAN-Music-Visualizer-windows-x64",
        "--log-dir", $logDir,
        "--out", (Join-Path $logDir "package-status-windows.json"),
        "--commit-expected", "$script:commitExpected",
        "--commit-observed", "$script:commitObserved",
        "--commit-verified", "$(if ($script:commitVerified) { 'true' } else { 'false' })",
        "--commit-source", "--build-info-json (built exe + extracted package, typed JSON parse)",
        "--commit-reason", "$script:commitReason"
    )
    & $pyWriter.Source @statusArgs
    $writerRc = $LASTEXITCODE
    if ($writerRc -ne 0) {
        Write-Output "ERROR: write-package-status.py failed with exit code $writerRc"
        Fail-Gate "package-status-record"
    }
}
Get-Content (Join-Path $logDir "package-status-windows.json")

# PINNED CACHE CLEANUP (run 43712071111): every consumer of the pinned
# media-tools proof cache (media-tools-pinned-install, the app-managed
# export proof) has finished — remove the cache when this run owns it
# (a caller-preset cache directory is never touched).
if ($script:mediaToolsCacheOwned -and $env:SHEYTAN_MEDIA_TOOLS_CACHE_DIR -and (Test-Path -LiteralPath $env:SHEYTAN_MEDIA_TOOLS_CACHE_DIR)) {
    Remove-Item -Recurse -Force -LiteralPath $env:SHEYTAN_MEDIA_TOOLS_CACHE_DIR -ErrorAction SilentlyContinue
    Write-Output "pinned media-tools proof cache cleaned: $($env:SHEYTAN_MEDIA_TOOLS_CACHE_DIR)"
}

# ---- run summary + failure index -------------------------------------------
$ended = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
Write-Note ""
Write-Note "=============================================================="
Write-Note "RUN SUMMARY - $job"
Write-Note "Ended:            $ended"
Write-Note "Failed gates:     $($script:failures)"
if ($script:failedGates.Count -gt 0) { Write-Note ("Failed gate list: " + ($script:failedGates -join " ")) }
if ($script:skippedGates.Count -gt 0) { Write-Note ("Skipped gate list: " + ($script:skippedGates -join " ")) }
Write-Note ""
Write-Note "FAILURE INDEX (EXIT_CODE records with non-zero codes):"
$select = Select-String -Path $env:CI_LOG_FILE -Pattern 'EXIT_CODE: [1-9][0-9]* FAIL'
if ($select) { $select | ForEach-Object { Write-Note ("{0}: {1}" -f $_.LineNumber, $_.Line) } }
else { Write-Note "(none - all captured commands returned exit code 0)" }
Write-Note ""
Write-Note "SKIPPED GATES:"
$skippedLines = Select-String -Path $env:CI_LOG_FILE -Pattern '^SKIPPED: '
if ($skippedLines) { $skippedLines | ForEach-Object { Write-Note ("{0}: {1}" -f $_.LineNumber, $_.Line) } }
else { Write-Note "(none)" }
$runResult = "FAIL"
if ($script:failures -eq 0) { $runResult = "PASS" }
Write-Note "RUN RESULT: $runResult"
Write-Note "=============================================================="

if ($script:failures -eq 0) { exit 0 } else { exit 1 }
