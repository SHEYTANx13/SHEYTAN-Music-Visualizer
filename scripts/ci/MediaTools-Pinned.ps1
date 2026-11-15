# MediaTools-Pinned.ps1 — pinned FFmpeg asset provider (PowerShell side).
#
# Contract (run 43712071111): CI proofs that need REAL ffmpeg/ffprobe
# binaries (the media-tools-pinned-install integration gate and the
# packaged app-managed export proof) download the PINNED BtbN win64 GPL
# asset ONCE into a runner-temp cache directory (always OUTSIDE the
# repository, the dist\ tree and the package stage tree; never bundled
# into any artifact — the package ships no media-tool binaries).
#
# Pinned asset (packaging/FFMPEG-TOOLS-PROVENANCE.md is the documented
# record; internal/mediatools/manifest.json is the machine-readable
# source of truth consumed by the in-app Media Tools & Codecs manager):
#   URL:    https://github.com/BtbN/FFmpeg-Builds/releases/download/autobuild-2026-11-08-12-35/ffmpeg-n9.0.2-48-gd4070a14ce-win64-gpl-9.0.zip
#   SHA256: 40608cfdd40b0e7929704059daee4af369f7a9984ea7aa8ee416d78cce2e8aae
#   inner:  ffmpeg-n9.0.2-48-gd4070a14ce-win64-gpl-9.0/bin/ (ffmpeg.exe, ffprobe.exe)
#
# The download is CACHED: a second call reuses the verified archive and
# extraction instead of downloading twice. The caller owns cleanup —
# build-windows.ps1 removes the cache directory after the last consumer
# gate has finished.
#
# Cache location: $env:SHEYTAN_MEDIA_TOOLS_CACHE_DIR when set by the
# caller, else $env:RUNNER_TEMP\media-tools, else the user temp dir
# \sheytan-media-tools.
#
# Usage (dot-sourced inside a generated gate script, which runs in a
# CHILD pwsh process and therefore needs its own file-scoped provider):
#   . (Join-Path $repoRoot "scripts/ci/MediaTools-Pinned.ps1")
#   $tools = Get-SheytanPinnedMediaTools
#   $tools.Archive  $tools.BinDir  $tools.Sha256  $tools.Url
#
# Any failure (download, SHA-256 mismatch, unexpected archive layout)
# THROWS — under the gates' Set-StrictMode/ErrorActionPreference=Stop
# that fails the gate with the exact reason. Never a partial result.

function Get-SheytanPinnedMediaTools {
    [CmdletBinding()]
    [OutputType([hashtable])]
    param()

    $url = "https://github.com/BtbN/FFmpeg-Builds/releases/download/autobuild-2026-11-08-12-35/ffmpeg-n9.0.2-48-gd4070a14ce-win64-gpl-9.0.zip"
    $sha256 = "40608cfdd40b0e7929704059daee4af369f7a9984ea7aa8ee416d78cce2e8aae"
    $innerBin = "ffmpeg-n9.0.2-48-gd4070a14ce-win64-gpl-9.0/bin"

    $base = $null
    if (-not [string]::IsNullOrWhiteSpace($env:SHEYTAN_MEDIA_TOOLS_CACHE_DIR)) {
        $base = $env:SHEYTAN_MEDIA_TOOLS_CACHE_DIR
    }
    elseif (-not [string]::IsNullOrWhiteSpace($env:RUNNER_TEMP) -and (Test-Path -LiteralPath $env:RUNNER_TEMP -PathType Container)) {
        $base = Join-Path $env:RUNNER_TEMP "media-tools"
    }
    else {
        $base = Join-Path ([System.IO.Path]::GetTempPath()) "sheytan-media-tools"
    }
    New-Item -ItemType Directory -Force -Path $base | Out-Null

    $archive = Join-Path $base "ffmpeg-n9.0.2-48-gd4070a14ce-win64-gpl-9.0.zip"
    $needDownload = $true
    if ((Test-Path -LiteralPath $archive -PathType Leaf) -and ((Get-Item -LiteralPath $archive).Length -gt 0)) {
        $cachedHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $archive).Hash.ToLowerInvariant()
        if ($cachedHash -eq $sha256) {
            $needDownload = $false
            Write-Output "pinned FFmpeg archive already cached and SHA-256-verified: $archive"
        }
        else {
            Write-Output "WARN: cached pinned archive SHA-256 mismatch - redownloading: $archive"
            Remove-Item -Force -LiteralPath $archive
        }
    }
    if ($needDownload) {
        Write-Output "downloading pinned FFmpeg archive: $url"
        Invoke-WebRequest -Uri $url -OutFile $archive -UseBasicParsing
        $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath $archive).Hash.ToLowerInvariant()
        if ($hash -ne $sha256) {
            Remove-Item -Force -LiteralPath $archive -ErrorAction SilentlyContinue
            throw "pinned FFmpeg download SHA-256 mismatch: got $hash, want $sha256"
        }
    }

    $extractDir = Join-Path $base "extracted-windows"
    $binDir = Join-Path $extractDir ($innerBin -replace '/', '\')
    $ffExe = Join-Path $binDir "ffmpeg.exe"
    $fpExe = Join-Path $binDir "ffprobe.exe"
    if (-not ((Test-Path -LiteralPath $ffExe -PathType Leaf) -and (Test-Path -LiteralPath $fpExe -PathType Leaf))) {
        if (Test-Path -LiteralPath $extractDir) { Remove-Item -Recurse -Force -LiteralPath $extractDir }
        New-Item -ItemType Directory -Force -Path $extractDir | Out-Null
        Expand-Archive -Path $archive -DestinationPath $extractDir
        if (-not ((Test-Path -LiteralPath $ffExe -PathType Leaf) -and (Test-Path -LiteralPath $fpExe -PathType Leaf))) {
            throw "pinned archive layout unexpected: ffmpeg.exe/ffprobe.exe not found under $binDir"
        }
    }

    return @{
        Url     = $url
        Sha256  = $sha256
        Archive = $archive
        BinDir  = $binDir
    }
}
