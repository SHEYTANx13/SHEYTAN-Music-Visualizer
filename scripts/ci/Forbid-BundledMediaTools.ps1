#!/usr/bin/env pwsh
<#
.SYNOPSIS
    Forbid-BundledMediaTools.ps1 — NEGATIVE package gate (PowerShell side).

.DESCRIPTION
    Contract (run 43712071111): the distributed Windows application ZIP
    must contain NO bundled media tools. FFmpeg/ffprobe are app-managed
    downloads (the in-app Media Tools & Codecs manager installs the
    pinned pair under <DataRoot>\media-tools\current\bin, never into the
    package). The gate extracts the ZIP to a temporary directory, walks
    the EXTRACTED files and also scans the raw zip entry list, and exits
    1 when any of the following is present:

      1. any file (or entry) whose basename is ffmpeg/ffprobe in any
         case, with or without the .exe suffix;
      2. any *.dll under a tools/ directory;
      3. any nested archive (*.zip, *.tar, *.tar.gz, *.tar.xz, *.7z);
      4. any single file larger than 40 MiB (41943040 bytes).

    The gate ALWAYS prints the archive's total size, the packaged file
    count and the top-10 largest files, so a failure is diagnosable from
    the captured log alone.

    Deliberately a runnable script (not a function library) so the
    packaging-gate fixtures (scripts/ci/tests/test-package-gates.sh,
    fixture P11) can prove BOTH directions directly: a synthetic
    package containing a fake tools/ffmpeg/bin/ffmpeg.exe is REJECTED,
    and a clean compact package is ACCEPTED.

.PARAMETER ArchivePath
    Path to the produced package ZIP.

.PARAMETER MaxFileBytes
    Per-file budget in bytes (default 41943040 = 40 MiB).

.EXAMPLE
    pwsh -NoProfile -File scripts/ci/Forbid-BundledMediaTools.ps1 `
        -ArchivePath dist\SHEYTAN-Music-Visualizer-windows-x64.zip

.EXITCODES
    0 = clean, 1 = violation found, 2 = usage/IO error.
#>
param(
    [Parameter(Mandatory = $true)][string]$ArchivePath,
    [long]$MaxFileBytes = 41943040
)
Set-StrictMode -Version 3.0
$ErrorActionPreference = "Stop"
$PSNativeCommandUseErrorActionPreference = $false

if ($MaxFileBytes -le 0) {
    Write-Output "ERROR: MaxFileBytes must be positive (got $MaxFileBytes)"
    exit 2
}
if (-not (Test-Path -LiteralPath $ArchivePath -PathType Leaf)) {
    Write-Output "ERROR: archive not found: $ArchivePath"
    exit 2
}

$violations = New-Object System.Collections.Generic.List[string]

# ---- zip entry scan (raw names, before any extraction) ---------------------
Add-Type -AssemblyName System.IO.Compression.FileSystem
$zipFull = (Resolve-Path -LiteralPath $ArchivePath).ProviderPath
$zip = $null
try {
    $zip = [System.IO.Compression.ZipFile]::OpenRead($zipFull)
}
catch {
    Write-Output "ERROR: cannot open ZIP '$ArchivePath': $($_.Exception.Message)"
    exit 2
}
try {
    foreach ($e in $zip.Entries) {
        if ($e.FullName.EndsWith('/')) { continue }  # explicit directory entries
        $leaf = ($e.FullName -split '/')[-1]
        # PowerShell -match is case-INSENSITIVE by default: any case passes.
        if ($leaf -match '^(ffmpeg|ffprobe)(\.exe)?$') {
            $violations.Add("bundled media-tool binary (zip entry): $($e.FullName)")
        }
    }
}
finally { $zip.Dispose() }

# ---- extraction + filesystem walk ------------------------------------------
$extractDir = Join-Path ([System.IO.Path]::GetTempPath()) ("sheytan-forbid-media-tools-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $extractDir | Out-Null
try {
    Expand-Archive -Path $ArchivePath -DestinationPath $extractDir
    $files = @(Get-ChildItem -LiteralPath $extractDir -Recurse -File -Force)
    foreach ($f in $files) {
        $rel = ($f.FullName.Substring($extractDir.Length + 1)) -replace '\\', '/'
        $leaf = $f.Name
        if ($leaf -match '^(ffmpeg|ffprobe)(\.exe)?$') {
            $violations.Add("bundled media-tool binary: $rel")
        }
        if ($leaf -match '\.(zip|7z)$' -or $leaf -match '\.tar(\.gz|\.xz|\.bz2)?$') {
            $violations.Add("nested archive inside the package: $rel")
        }
        if ($leaf -match '\.dll$' -and $rel -match '(^|/)tools(/.*)?$') {
            $violations.Add("DLL under tools/: $rel")
        }
        # The application executable itself is exempt from the media-asset
        # per-file bound (a Go+Fyne+wgpu binary is legitimately ~35-50 MiB);
        # the name match is exact and everything else - including any
        # tool/DLL pack file - stays under the bound.
        # Run 46121470729 truth fix: the Wails web shell (app-wails.exe)
        # is the product executable and is exempt beside the CLI
        # authority (the retired app-gogui names must not return).
        $appExe = ($leaf -eq "sheytan-music-visualizer" -or $leaf -eq "sheytan-music-visualizer.exe" -or $leaf -eq "app-wails" -or $leaf -eq "app-wails.exe")
        if (-not $appExe -and $f.Length -gt $MaxFileBytes) {
            $violations.Add(("oversized file ({0} bytes > {1}): {2}" -f $f.Length, $MaxFileBytes, $rel))
        }
    }

    # ---- report ------------------------------------------------------------
    $totalBytes = (Get-Item -LiteralPath $ArchivePath).Length
    Write-Output ("forbid-bundled-media-tools: {0}" -f $ArchivePath)
    Write-Output ("archive total size: {0} bytes" -f $totalBytes)
    Write-Output ("packaged files: {0}" -f $files.Count)
    Write-Output ("per-file budget: {0} bytes" -f $MaxFileBytes)
    Write-Output "top-10 largest files (bytes / package path):"
    $rank = 0
    foreach ($f in ($files | Sort-Object Length -Descending)) {
        $rank++
        if ($rank -gt 10) { break }
        $rel = ($f.FullName.Substring($extractDir.Length + 1)) -replace '\\', '/'
        Write-Output ("  {0,12}  {1}" -f $f.Length, $rel)
    }

    if ($violations.Count -gt 0) {
        Write-Output ("ERROR: {0} forbid-bundled-media-tools violation(s):" -f $violations.Count)
        foreach ($v in $violations) { Write-Output "  $v" }
        exit 1
    }
    Write-Output "forbid-bundled-media-tools PASSED: no bundled media tools, no nested archives, no DLLs under tools/, no oversized files"
    exit 0
}
finally {
    if (Test-Path -LiteralPath $extractDir) {
        Remove-Item -Recurse -Force -LiteralPath $extractDir -ErrorAction SilentlyContinue
    }
}
