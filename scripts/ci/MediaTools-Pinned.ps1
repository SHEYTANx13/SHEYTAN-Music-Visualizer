# MediaTools-Pinned.ps1 — pinned FFmpeg asset provider (PowerShell side).
#
# Contract (run 43712071111; run-43767556517 repair): CI proofs that need
# REAL ffmpeg/ffprobe binaries (the media-tools-pinned-install integration
# gate and the packaged app-managed export proof) download the PINNED BtbN
# win64 GPL asset ONCE into a runner-temp cache directory (always OUTSIDE
# the repository, the dist\ tree and the package stage tree; never bundled
# into any artifact — the package ships no media-tool binaries).
#
# Pinned asset (packaging/FFMPEG-TOOLS-PROVENANCE.md is the documented
# record; internal/mediatools/manifest.json is the machine-readable
# source of truth consumed by the in-app Media Tools & Codecs manager):
#   URL:    https://github.com/BtbN/FFmpeg-Builds/releases/download/autobuild-2026-11-22-12-36/ffmpeg-n9.0.2-61-gd59078794d-win64-gpl-9.0.zip
#   SHA256: 438a2555f30ab84368e888b654c7e30fa8797d02ecb8da67d67f3afefc4ce64e
#   inner:  ffmpeg-n9.0.2-61-gd59078794d-win64-gpl-9.0/bin/ (ffmpeg.exe, ffprobe.exe)
#
# Run-45042834042 repair: the previously pinned autobuild-2026-11-08-12-35
# release was rotated away upstream, so Invoke-WebRequest failed with 404
# and both media-tools gates went red. The download failure now THROWS a
# diagnostic that names the rotation cause and the exact re-pin procedure.
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
# RETURN CONTRACT (run-43767556517 repair, authoritative schema):
# Get-SheytanPinnedMediaTools returns exactly ONE [pscustomobject] with
# the properties below — nothing else ever reaches the success stream.
# All human-readable progress goes through Write-Host (information
# stream). The run-43767556517 defect class was Write-Output progress
# strings inside the resolver: PowerShell merges ALL function output,
# so callers assigning `$tools = Get-SheytanPinnedMediaTools` received
# an object[] array, and under StrictMode reading `$tools.Url` threw
# "The property 'Url' cannot be found on this object." — the exact
# media-tools-pinned-install / verify-extracted-path-with-spaces
# failure of that run. Rules:
#   1. The resolver NEVER uses Write-Output; the contract fixture
#      (scripts/ci/tests/Test-MediaToolsPinnedResolver.ps1) enforces
#      this statically and dynamically.
#   2. Every consumer reads ONLY the schema properties below; a
#      property inventory is validated by Test-SheytanToolsResultSchema
#      before the object leaves this file, and a missing/invalid field
#      fails the gate with a diagnostic that NAMES the absent field.
#   3. The expected digest is pinned here in source (never accepted
#      from a server response) and is verified before extraction.
#
# Authoritative schema ([pscustomobject]):
#   Version       e.g. "n9.0.2-48-gd4070a14ce" (matches the Go manifest)
#   AssetURL      pinned download URL (contains the pinned version)
#   ArchivePath   SHA-256-verified archive on disk
#   SHA256        pinned lowercase hex digest (trust anchor, 64 hex)
#   BinaryDir     extracted inner bin directory (ffmpeg.exe/ffprobe.exe)
#   FFmpegPath    validated path to ffmpeg.exe inside BinaryDir
#   FFprobePath   validated path to ffprobe.exe inside BinaryDir
#   Platform      "windows/amd64" (the pinned pack identity)
#   ArchiveFormat "zip"
#
# Usage (dot-sourced inside a generated gate script, which runs in a
# CHILD pwsh process and therefore needs its own file-scoped provider):
#   . (Join-Path $repoRoot "scripts/ci/MediaTools-Pinned.ps1")
#   $tools = Get-SheytanPinnedMediaTools
#   $tools.AssetURL  $tools.ArchivePath  $tools.SHA256
#   $tools.BinaryDir $tools.FFmpegPath   $tools.FFprobePath
#
# Any failure (download, SHA-256 mismatch, unexpected archive layout,
# schema violation) THROWS — under the gates' Set-StrictMode/
# ErrorActionPreference=Stop that fails the gate with the exact reason.
# Never a partial result.

# The authoritative property inventory. Order is part of the contract:
# the fixture matrix asserts it so silent renames cannot pass.
$script:SheytanPinnedToolsSchema = @(
    'Version', 'AssetURL', 'ArchivePath', 'SHA256', 'BinaryDir',
    'FFmpegPath', 'FFprobePath', 'Platform', 'ArchiveFormat'
)

# Test-SheytanToolsResultSchema validates an object against the
# authoritative resolver schema. Returns $true/$false; on failure each
# violated field is named on the error stream (never a generic crash).
# -RequireFiles additionally proves ArchivePath/FFmpegPath/FFprobePath
# exist on disk (leaf files) — used by the resolver and the fixture
# matrix's "missing binaries" case.
function Test-SheytanToolsResultSchema {
    [CmdletBinding()]
    [OutputType([bool])]
    param(
        [Parameter(Mandatory = $true)][AllowNull()]$Result,
        [switch]$RequireFiles
    )
    if ($null -eq $Result) {
        Write-Error -ErrorAction Continue "media tools result schema violation: object is null (expected a [pscustomobject] with: $($script:SheytanPinnedToolsSchema -join ', '))"
        return $false
    }
    if ($Result -isnot [pscustomobject]) {
        Write-Error -ErrorAction Continue ("media tools result schema violation: expected [pscustomobject], got '{0}' (the run-43767556517 defect class: Write-Output pollution turns the result into an object[] array)" -f $Result.GetType().FullName)
        return $false
    }
    foreach ($field in $script:SheytanPinnedToolsSchema) {
        $value = $Result.PSObject.Properties[$field]
        if ($null -eq $value) {
            Write-Error -ErrorAction Continue ("media tools result schema violation: absent field '{0}' (authoritative schema: {1})" -f $field, ($script:SheytanPinnedToolsSchema -join ', '))
            return $false
        }
        if ([string]::IsNullOrWhiteSpace([string]$value.Value)) {
            Write-Error -ErrorAction Continue ("media tools result schema violation: empty field '{0}'" -f $field)
            return $false
        }
    }
    # Case-SENSITIVE comparison (-cnotmatch): the trust anchor is a
    # lowercase 64-hex digest; -notmatch would silently accept an
    # uppercase variant of the pinned digest.
    if ($Result.SHA256 -cnotmatch '^[0-9a-f]{64}$') {
        Write-Error -ErrorAction Continue ("media tools result schema violation: SHA256 '{0}' is not a lowercase 64-hex digest" -f $Result.SHA256)
        return $false
    }
    if ($Result.AssetURL -notlike "*$($Result.Version)*") {
        Write-Error -ErrorAction Continue ("media tools result schema violation: AssetURL does not correspond to the pinned Version '{0}'" -f $Result.Version)
        return $false
    }
    if ($RequireFiles) {
        foreach ($pair in @(
            @{ name = 'ArchivePath';  path = $Result.ArchivePath },
            @{ name = 'FFmpegPath';   path = $Result.FFmpegPath },
            @{ name = 'FFprobePath';  path = $Result.FFprobePath }
        )) {
            if (-not (Test-Path -LiteralPath $pair.path -PathType Leaf)) {
                Write-Error -ErrorAction Continue ("media tools result schema violation: {0} does not exist on disk: '{1}'" -f $pair.name, $pair.path)
                return $false
            }
        }
    }
    return $true
}

# Get-SheytanPinnedMediaTools resolves the pinned win64 asset: cache or
# download, pinned-digest verification BEFORE extraction, layout
# validation, schema-validated single-object result.
function Get-SheytanPinnedMediaTools {
    [CmdletBinding()]
    [OutputType([pscustomobject])]
    param()

    # Pinned trust anchors (source-pinned; never taken from a response).
    $url = "https://github.com/BtbN/FFmpeg-Builds/releases/download/autobuild-2026-11-22-12-36/ffmpeg-n9.0.2-61-gd59078794d-win64-gpl-9.0.zip"
    $sha256 = "438a2555f30ab84368e888b654c7e30fa8797d02ecb8da67d67f3afefc4ce64e"
    $version = "n9.0.2-61-gd59078794d"
    $innerBin = "ffmpeg-n9.0.2-61-gd59078794d-win64-gpl-9.0/bin"
    $platform = "windows/amd64"
    $archiveFormat = "zip"
    if ($url -notlike "*$version*") {
        throw "pinned asset configuration incoherent: URL does not contain the pinned version '$version'"
    }

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

    $archive = Join-Path $base "ffmpeg-n9.0.2-61-gd59078794d-win64-gpl-9.0.zip"
    $needDownload = $true
    if ((Test-Path -LiteralPath $archive -PathType Leaf) -and ((Get-Item -LiteralPath $archive).Length -gt 0)) {
        $cachedHash = (Get-FileHash -Algorithm SHA256 -LiteralPath $archive).Hash.ToLowerInvariant()
        if ($cachedHash -eq $sha256) {
            $needDownload = $false
            Write-Host "pinned FFmpeg archive already cached and SHA-256-verified: $archive"
        }
        else {
            Write-Host "WARN: cached pinned archive SHA-256 mismatch - redownloading: $archive"
            Remove-Item -Force -LiteralPath $archive
        }
    }
    if ($needDownload) {
        Write-Host "downloading pinned FFmpeg archive: $url"
        try {
            Invoke-WebRequest -Uri $url -OutFile $archive -UseBasicParsing
        }
        catch {
            # Run-45042834042 repair: name the rotation cause and the exact
            # re-pin procedure instead of a bare HTTP status exception.
            throw ("pinned FFmpeg download failed: {0}
Cause: the pinned BtbN autobuild release was rotated away upstream (dated autobuild releases are removed after a rolling window); HTTP failure: {1}
Fix: pick the newest existing tag from https://github.com/BtbN/FFmpeg-Builds/releases, then update the pinned URL/SHA256/version/innerBin here, in scripts/ci/ensure-pinned-media-tools.sh, internal/mediatools/manifest.json and packaging/FFMPEG-TOOLS-PROVENANCE.md (they must stay identical)." -f $url, $_.Exception.Message)
        }
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

    # Single result object; validated against the authoritative schema
    # (with on-disk file checks) BEFORE it leaves this function. A
    # violation throws naming the exact absent/invalid field.
    $result = [pscustomobject][ordered]@{
        Version       = $version
        AssetURL      = $url
        ArchivePath   = $archive
        SHA256        = $sha256
        BinaryDir     = $binDir
        FFmpegPath    = $ffExe
        FFprobePath   = $fpExe
        Platform      = $platform
        ArchiveFormat = $archiveFormat
    }
    if (-not (Test-SheytanToolsResultSchema -Result $result -RequireFiles)) {
        # Write-Error diagnostics were already emitted naming the field;
        # fail the gate loudly instead of returning a broken contract.
        throw "pinned media tools resolver produced an invalid result object (see schema diagnostics above)"
    }
    return $result
}
