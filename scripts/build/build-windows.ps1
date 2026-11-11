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
Set-Content -Path $env:CI_LOG_FILE -Value "" -NoNewline # fresh local run

. (Join-Path $repoRoot "scripts/ci/Capture-Step.ps1")

$job = "phase2-windows-build"

# CI/user log capture: self-check and runtime checks write their app
# logs under ci-logs/app-logs (explicit SHEYTAN_LOG_DIR override) and
# resolve all data paths under ci-logs/app-data - never the runner's
# unrelated roaming profile. Local runs can preset their own values.
if (-not $env:SHEYTAN_LOG_DIR)  { $env:SHEYTAN_LOG_DIR  = Join-Path $logDir "app-logs" }
if (-not $env:SHEYTAN_DATA_DIR) { $env:SHEYTAN_DATA_DIR = Join-Path $logDir "app-data" }
# goffi_static: required when Fyne (CGO) and wgpu (goffi FFI) link in one
# binary; harmless on Windows (see ai-notes.md).
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
$script:failures = 0
$script:failedGates = @()
$script:skippedGates = @()

# Missing-prerequisite honesty (agent.md §4 vocabulary): without the Go
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
        "go-build-packages", "build-executable", "run-version",
        "run-self-check", "verify-build-metadata", "stage-portable",
        "verify-package-license", "validate-pe", "create-portable-zip",
        "verify-extracted-package"
    )
    foreach ($g in $blockedGates) {
        Add-Content -Path $env:CI_LOG_FILE -Value ("BLOCKED: gate {0} ({1})" -f $g, $blockedReason)
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

function Write-Note([string]$text) {
    Add-Content -Path $env:CI_LOG_FILE -Value $text
}
function Fail-Gate([string]$gate) {
    $script:failures++
    $script:failedGates += $gate
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
$rc = Capture-Step -Job $job -Step "go-test" -ToolContext "gate:go-test" -GoTags -Command { go test $goTags -skip '^TestPaletteReachesGPUShader$' ./... }
if ($rc -ne 0) { Fail-Gate "go-test" }

$rc = Capture-Step -Job $job -Step "go-vet" -ToolContext "gate:go-vet" -GoTags -Command { go vet $goTags ./... }
if ($rc -ne 0) { Fail-Gate "go-vet" }

$rc = Capture-Step -Job $job -Step "go-build-packages" -ToolContext "gate:go-build" -GoTags -Command { go build $goTags ./... }
if ($rc -ne 0) { Fail-Gate "go-build-packages" }

# ---- desktop executable + runtime checks -----------------------------------
$exeOk = $false
$exePath = Join-Path $repoRoot "bin\sheytan-music-visualizer.exe"
$rc = Capture-Step -Job $job -Step "build-executable" -ToolContext "gate:build-exe" -GoTags -Command {
    $bi = "github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/buildinfo"
    $args_ = @("build", "-trimpath") + $goTags
    $ld = @()
    # GUI SUBSYSTEM (run 42598929646 defect H): the user-facing Windows
    # desktop executable must be linked with -H=windowsgui so launching
    # it never attaches a command window. The token is emitted FIRST
    # and the -X metadata tokens below stay intact (both live in the
    # same -ldflags value); validate-pe verifies the resulting PE
    # Optional Header subsystem is IMAGE_SUBSYSTEM_WINDOWS_GUI / 2.
    $ld += "-H=windowsgui"
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

    # Failure-counter snapshot for the package-status record (run
    # 42939743766 §6): gates BEFORE staging must be green for VERIFIED;
    # later non-package gates do NOT invalidate a verified package — they
    # produce the workflow's TEST-ONLY state instead.
    $script:failuresBeforePackage = $script:failures

    # ---- portable staging + artifact validation -----------------------------
    $rc = Capture-Step -Job $job -Step "stage-portable" -ToolContext "gate:stage-portable" -Command {
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
        Copy-Item "bin\sheytan-music-visualizer.exe" $appDir
        Copy-Item "license.md" $appDir
        Copy-Item "readme.md" $appDir
        Copy-Item "packaging\portable.mode" $appDir
        Copy-Item "packaging\DATA-FOLDERS.txt" (Join-Path $appDir "data")
        Copy-Item "packaging\FFMPEG-TOOLS-LICENSING.md" (Join-Path $appDir "tools\ffmpeg")
        $exeItem = Get-Item (Join-Path $appDir "sheytan-music-visualizer.exe")
        if ($exeItem.Length -le 0) { Write-Output "ERROR: staged executable is empty"; exit 1 }
        Write-Output ("staged executable size: {0} bytes" -f $exeItem.Length)
        Get-ChildItem -Recurse -File $appDir | ForEach-Object { $rel = $_.FullName.Substring($stageRoot.Length + 1); Write-Output ("STAGED: " + ($rel -replace '\\', '/')) }
        exit 0
    }
    if ($rc -ne 0) { Fail-Gate "stage-portable" }

    # BYTE-FOR-BYTE license identity gate (run 42481815519 contract):
    # the staged license.md must equal the canonical tracked file
    # exactly, before archive creation. SHA-256 equality on raw bytes;
    # any mismatch fails the package gate.
    $rc = Capture-Step -Job $job -Step "verify-package-license" -ToolContext "gate:license-identity" -Command {
        $canonical = Get-FileHash -Algorithm SHA256 "license.md"
        $stagedPath = "dist\windows-x64-stage\SHEYTAN-Music-Visualizer-windows-x64\license.md"
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
        if ($subsystem -ne 2) {
            Write-Output "ERROR: the Windows desktop executable is NOT a GUI-subsystem binary (-H=windowsgui missing or overridden)"
            exit 1
        }
        Write-Output "validate-pe PASSED: desktop executable subsystem = Windows GUI (2), verified from the built artifact"
        exit 0
    }
    if ($rc -ne 0) { Fail-Gate "validate-pe" }

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
        $requiredPaths = @(
            "sheytan-music-visualizer.exe",
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
        $canonical = Get-FileHash -Algorithm SHA256 -LiteralPath "license.md"
        $canonicalLen = (Get-Item -LiteralPath "license.md").Length
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
        Write-Output ("extracted EXE PE Optional Header Subsystem: {0} (2 = GUI)" -f $subsystem)
        if ($subsystem -ne 2) {
            Write-Output "ERROR: extracted executable lost the Windows GUI subsystem (must stay -H=windowsgui)"
            & $cleanup; exit 1
        }
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
# The package-status record (run 42939743766 §6) is machine-readable state
# for the workflow upload decision: VERIFIED only when every pre-package
# gate and every package gate passed AND the expected commit (immutable
# github.sha context) exactly matches the embedded build metadata.
$script:packageStatus = "UNAVAILABLE"
$script:commitExpected = $env:SHEYTAN_BUILD_COMMIT
$script:commitObserved = "not-observed"
$script:commitVerified = $false
$exeExists = Test-Path "bin\sheytan-music-visualizer.exe"
$zipPath = Join-Path (Get-Location).Path "dist\SHEYTAN-Music-Visualizer-windows-x64.zip"
$zipExists = (Test-Path -LiteralPath $zipPath) -and ((Get-Item -LiteralPath $zipPath).Length -gt 0)
if ($exeExists -and $zipExists -and $script:failuresBeforePackage -eq 0) {
    $rc = Capture-Step -Job $job -Step "verify-extracted-path-with-spaces" -ToolContext "gate:package-verify (path with spaces)" -RequireRepoRoot -RepoRootHelpers @("scripts/ci/Invoke-CapturedProcess.ps1") -Command {
        . (Join-Path $repoRoot "scripts/ci/Invoke-CapturedProcess.ps1")
        $zip = Join-Path (Get-Location).Path "dist\SHEYTAN-Music-Visualizer-windows-x64.zip"
        $base = Join-Path ([System.IO.Path]::GetTempPath()) ("SHEYTAN Spaces Dir " + [guid]::NewGuid().ToString("N"))
        New-Item -ItemType Directory -Force -Path $base | Out-Null
        $cleanup = { if (Test-Path -LiteralPath $base) { Remove-Item -Recurse -Force $base } }
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
            exit 0
        }
        finally { & $cleanup }
    }
    if ($rc -ne 0) { Fail-Gate "verify-extracted-path-with-spaces" }

    # A -H=windowsgui executable may not deliver stdout through a plain
    # `&` invocation on the runner (run-42837709122 root cause); the
    # robust runner is the only reliable observation path.
    . (Join-Path $repoRoot "scripts/ci/Invoke-CapturedProcess.ps1")
    $verRun = Invoke-CapturedProcess -FilePath "bin\sheytan-music-visualizer.exe" -ArgumentList @("--version") -TimeoutSeconds 120
    if ($verRun.Kind -eq 'exited' -and $verRun.ExitCode -eq 0 -and $verRun.StdOut.Count -gt 0) {
        $verLine = ($verRun.StdOut | Select-Object -First 1)
        if ($verLine -match 'commit ([0-9a-f]{40})') { $script:commitObserved = $Matches[1] }
    }
    if (-not [string]::IsNullOrWhiteSpace($script:commitExpected) -and $script:commitObserved -eq $script:commitExpected.Trim()) {
        $script:commitVerified = $true
    }
    $packageGatesFailed = $script:failures - $script:failuresBeforePackage
    if ($script:commitVerified -and $packageGatesFailed -eq 0) {
        $script:packageStatus = "VERIFIED"
    } else {
        $script:packageStatus = "TEST_ONLY"
    }
}
$pkgRecord = [ordered]@{
    platform         = "windows-x64"
    package_status   = $script:packageStatus
    package_file     = "dist/SHEYTAN-Music-Visualizer-windows-x64.zip"
    app_dir          = "SHEYTAN-Music-Visualizer-windows-x64"
    commit_expected  = $(if ([string]::IsNullOrWhiteSpace($script:commitExpected)) { $null } else { $script:commitExpected })
    commit_observed  = $script:commitObserved
    commit_verified  = $script:commitVerified
    note             = "package_status is VERIFIED only with a non-blank expected commit that exactly matches the embedded metadata; TEST_ONLY packages are valid but the required job stays red"
}
New-Item -ItemType Directory -Force -Path "ci-logs" | Out-Null
$pkgRecord | ConvertTo-Json | Set-Content -Path "ci-logs\package-status-windows.json" -Encoding utf8NoBOM
Get-Content "ci-logs\package-status-windows.json"

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
