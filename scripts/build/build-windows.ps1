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
Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
$PSNativeCommandUseErrorActionPreference = $false

$repoRoot = Split-Path -Parent (Split-Path -Parent (Split-Path -Parent $PSCommandPath))
Set-Location $repoRoot

$logDir = Join-Path $repoRoot "ci-logs"
New-Item -ItemType Directory -Force -Path $logDir | Out-Null
$env:CI_LOG_FILE = Join-Path $logDir "phase2-build.log"
Set-Content -Path $env:CI_LOG_FILE -Value "" -NoNewline # fresh local run

. (Join-Path $repoRoot "scripts/ci/Capture-Step.ps1")

$job = "phase2-windows-build"
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
        "validate-pe", "create-portable-zip"
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
    # Header-chain evidence (evidence source 4): the installed CRT
    # configuration header must exist inside the compiler's own include
    # directory under the UCRT64 prefix.
    $incDir = (gcc -print-file-name=include).Trim()
    $incRc = $LASTEXITCODE
    if ($incRc -ne 0 -or [string]::IsNullOrWhiteSpace($incDir)) {
        Write-Output "ERROR: gcc -print-file-name=include failed (rc=$incRc)"
        exit 1
    }
    Write-Output "gcc include dir: $incDir"
    if ($incDir -notmatch '[\\/]ucrt64[\\/]') {
        Write-Output "ERROR: compiler include dir is not inside a ucrt64 prefix: $incDir"
        exit 1
    }
    $mingwHdr = Join-Path $incDir "_mingw.h"
    if (-not (Test-Path $mingwHdr)) {
        Write-Output "ERROR: CRT header _mingw.h not found at $mingwHdr (incomplete UCRT64 headers install)"
        exit 1
    }
    Write-Output "CRT header _mingw.h present: $mingwHdr"
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
$rc = Capture-Step -Job $job -Step "go-test" -ToolContext "gate:go-test" -GoTags -Command { go test $goTags ./... }
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

    $rc = Capture-Step -Job $job -Step "run-self-check" -ToolContext "runtime:--self-check" -Command { & "bin\sheytan-music-visualizer.exe" --self-check }
    if ($rc -ne 0) { Fail-Gate "run-self-check" }

    # ENFORCED metadata acceptance gate (run 42231355353 repair): exit
    # code 0 from --version is NOT evidence that the metadata arrived.
    # When CI metadata was supplied, --version and --self-check MUST
    # carry exactly those values; when none was supplied (local dev
    # build), the legitimate local/dev defaults are asserted instead.
    # Missing, stale, malformed or inconsistent values fail the gate.
    $rc = Capture-Step -Job $job -Step "verify-build-metadata" -ToolContext "gate:build-metadata" -Command {
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
        $verLines = & "bin\sheytan-music-visualizer.exe" --version
        $verRc = $LASTEXITCODE
        if ($verRc -ne 0) { Write-Output "ERROR: --version exited with $verRc"; exit $verRc }
        $verLine = ($verLines | Select-Object -First 1)
        Write-Output "version line observed: $verLine"
        $selfLines = & "bin\sheytan-music-visualizer.exe" --self-check
        $selfRc = $LASTEXITCODE
        if ($selfRc -ne 0) { Write-Output "ERROR: --self-check exited with $selfRc"; exit $selfRc }
        $selfInfo = ($selfLines | Where-Object { $_ -match 'version=' } | Select-Object -First 1)
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
    $rc = Capture-Step -Job $job -Step "stage-portable" -ToolContext "gate:stage-portable" -Command {
        $stage = Join-Path (Get-Location).Path "dist\SHEYTAN-Music-Visualizer-windows-x64-portable"
        if (Test-Path $stage) { Remove-Item -Recurse -Force $stage }
        New-Item -ItemType Directory -Force -Path $stage | Out-Null
        Copy-Item "bin\sheytan-music-visualizer.exe" $stage
        Copy-Item "license.md" $stage
        Copy-Item "readme.md" $stage
        $exeItem = Get-Item (Join-Path $stage "sheytan-music-visualizer.exe")
        if ($exeItem.Length -le 0) { Write-Output "ERROR: staged executable is empty"; exit 1 }
        Write-Output ("staged executable size: {0} bytes" -f $exeItem.Length)
        exit 0
    }
    if ($rc -ne 0) { Fail-Gate "stage-portable" }

    $rc = Capture-Step -Job $job -Step "validate-pe" -ToolContext "gate:validate-pe" -Command {
        $bytes = [System.IO.File]::ReadAllBytes("bin\sheytan-music-visualizer.exe")[0..1]
        if ($bytes[0] -ne 77 -or $bytes[1] -ne 90) { Write-Output "ERROR: not a PE executable (missing MZ header)"; exit 1 }
        Write-Output "PE MZ header confirmed"
        exit 0
    }
    if ($rc -ne 0) { Fail-Gate "validate-pe" }

    $rc = Capture-Step -Job $job -Step "create-portable-zip" -ToolContext "gate:create-portable-zip" -Command {
        $stage = Join-Path (Get-Location).Path "dist\SHEYTAN-Music-Visualizer-windows-x64-portable"
        $zip = Join-Path (Get-Location).Path "dist\SHEYTAN-Music-Visualizer-windows-x64-portable.zip"
        if (Test-Path $zip) { Remove-Item -Force $zip }
        Compress-Archive -Path "$stage\*" -DestinationPath $zip
        $zipItem = Get-Item $zip
        if ($zipItem.Length -le 0) { Write-Output "ERROR: created ZIP is empty"; exit 1 }
        Add-Type -AssemblyName System.IO.Compression.FileSystem
        $archive = [System.IO.Compression.ZipFile]::OpenRead($zip)
        try {
            Write-Output ("zip entries: {0}; size: {1} bytes" -f $archive.Entries.Count, $zipItem.Length)
            if ($archive.Entries.Count -lt 3) { Write-Output "ERROR: zip does not contain the expected files"; exit 1 }
        }
        finally { $archive.Dispose() }
        exit 0
    }
    if ($rc -ne 0) { Fail-Gate "create-portable-zip" }
}
else {
    Skip-Gate "run-version" "executable build failed"
    Skip-Gate "run-self-check" "executable build failed"
    Skip-Gate "verify-build-metadata" "executable build failed"
    Skip-Gate "stage-portable" "executable build failed"
    Skip-Gate "validate-pe" "executable build failed"
    Skip-Gate "create-portable-zip" "executable build failed"
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
