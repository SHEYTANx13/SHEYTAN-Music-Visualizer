#!/usr/bin/env pwsh
<#
.SYNOPSIS
    build-windows.ps1 — Phase 2 local build/verification entry point
    (Windows x64, MSYS2 UCRT64 toolchain).

.DESCRIPTION
    Runs the Phase 2 verification gates (studio build, export validation, GPU proof) for the Windows desktop build:
    toolchain report, gofmt, tidy, tests, vet, package build, the native
    Windows x64 executable, --version/--self-check runtime checks, and
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
# SINGLE DOCUMENTED TAG SOURCE: $env:SHEYTAN_GO_TAGS. Every gate runs in
# a CHILD pwsh process (Capture-Step serializes each gate to its own
# script); a child process never inherits the caller's PowerShell
# variables — only the environment crosses the boundary. Each generated
# gate script initializes and validates its own local $goTags array
# from this variable (Capture-Step preamble), so no gate can reference
# a parent-only variable and fail under StrictMode.
#
# Inside a gate script the array form is used: PowerShell passes each
# element as its own argument to native commands; a single
# "-tags goffi_static" string would arrive at go as ONE argument.
$env:SHEYTAN_GO_TAGS = "-tags goffi_static"
$script:failures = 0
$script:failedGates = @()
$script:skippedGates = @()

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
        Write-Output "ERROR: gcc not found on PATH (MSYS2 UCRT64 required)"
        exit 1
    }
    Write-Output "gcc resolved to: $($gcc.Source)"
    exit 0
}
if ($rc -ne 0) { Fail-Gate "desktop-env-check" }

# ---- static gates (all independent; always run) ----------------------------
$rc = Capture-Step -Job $job -Step "gofmt-verify" -ToolContext "gate:gofmt" -Command {
    $out = gofmt -l .
    if ($out) {
        $out | ForEach-Object { Write-Output $_ }
        Write-Output "ERROR: gofmt reported unformatted files"
        exit 1
    }
    Write-Output "gofmt clean"
    exit 0
}
if ($rc -ne 0) { Fail-Gate "gofmt-verify" }

# Read-only module hygiene: go mod tidy -diff never mutates the
# checked-out tree (the mutating `go mod tidy` gate was removed).
$rc = Capture-Step -Job $job -Step "go-mod-tidy-verify" -ToolContext "gate:go-mod-tidy" -Command { go mod tidy -diff }
if ($rc -ne 0) { Fail-Gate "go-mod-tidy-verify" }

$rc = Capture-Step -Job $job -Step "go-mod-verify" -ToolContext "gate:go-mod-verify" -Command { go mod verify }
if ($rc -ne 0) { Fail-Gate "go-mod-verify" }

$rc = Capture-Step -Job $job -Step "go-test" -ToolContext "gate:go-test" -Command { go test $goTags ./... }
if ($rc -ne 0) { Fail-Gate "go-test" }

$rc = Capture-Step -Job $job -Step "go-vet" -ToolContext "gate:go-vet" -Command { go vet $goTags ./... }
if ($rc -ne 0) { Fail-Gate "go-vet" }

$rc = Capture-Step -Job $job -Step "go-build-packages" -ToolContext "gate:go-build" -Command { go build $goTags ./... }
if ($rc -ne 0) { Fail-Gate "go-build-packages" }

# ---- desktop executable + runtime checks -----------------------------------
$exeOk = $false
$exePath = Join-Path $repoRoot "bin\sheytan-music-visualizer.exe"
$rc = Capture-Step -Job $job -Step "build-executable" -ToolContext "gate:build-exe" -Command {
    $bi = "github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/buildinfo"
    $args_ = @("build", "-trimpath") + $goTags
    $ld = @()
    if ($env:SHEYTAN_BUILD_VERSION) { $ld += "${bi}.version=$($env:SHEYTAN_BUILD_VERSION)" }
    if ($env:SHEYTAN_BUILD_COMMIT)  { $ld += "${bi}.commit=$($env:SHEYTAN_BUILD_COMMIT)" }
    if ($env:SHEYTAN_BUILD_DATE)    { $ld += "${bi}.date=$($env:SHEYTAN_BUILD_DATE)" }
    if ($env:SHEYTAN_BUILT_BY)      { $ld += "${bi}.builtBy=$($env:SHEYTAN_BUILT_BY)" }
    if ($ld.Count -gt 0) {
        # "-ldflags" and its (space-containing) value are two argv
        # elements; go accepts a single-argument value with spaces.
        $args_ += @("-ldflags", ($ld -join " "))
    }
    $args_ += @("-o", "bin\sheytan-music-visualizer.exe", "./cmd/sheytan-music-visualizer")
    go @args_
}
if ($rc -eq 0) { $exeOk = $true } else { Fail-Gate "build-executable" }

if ($exeOk) {
    $rc = Capture-Step -Job $job -Step "run-version" -ToolContext "runtime:--version" -Command { & "bin\sheytan-music-visualizer.exe" --version }
    if ($rc -ne 0) { Fail-Gate "run-version" }

    $rc = Capture-Step -Job $job -Step "run-self-check" -ToolContext "runtime:--self-check" -Command { & "bin\sheytan-music-visualizer.exe" --self-check }
    if ($rc -ne 0) { Fail-Gate "run-self-check" }

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
