# New-PortableZip — deterministic portable-package ZIP builder.
#
# Root cause this helper eliminates (run 42837709122, gates
# create-portable-zip + verify-extracted-package): the old gate ran
# `Compress-Archive -Path $stageRoot` where $stageRoot is the STAGING
# WRAPPER directory (…-windows-x64-portable) that CONTAINS the real
# application directory. Compress-Archive archives the given directory
# itself, so every entry gained the unwanted outer path level:
#   SHEYTAN-Music-Visualizer-windows-x64-portable/SHEYTAN-Music-Visualizer-windows-x64/…
# The required-entry manifest looked for entries at the app-directory
# root and found none, and verify-extracted-package then failed its
# license lookup at the expected (single-level) path. One archive-root
# defect, two failing gates.
#
# Contract:
#   - The package root inside the ZIP is EXACTLY the application
#     directory (SHEYTAN-Music-Visualizer-windows-x64) — the staging
#     wrapper never appears.
#   - Deterministic .NET ZipArchive enumeration with EXPLICIT relative
#     entry names and normalized '/' separators (never compared against
#     platform-dependent backslashes).
#   - Every directory gets an explicit directory entry (with trailing
#     '/'), including EMPTY directories such as tools/ffmpeg/bin when
#     FFmpeg is intentionally not bundled — an empty required tool
#     directory must survive the archive.
#   - Files are stored with their exact bytes (no line-ending or
#     encoding transformation of any kind — license.md stays
#     byte-identical end to end).
#   - Entries are enumerated in sorted order so two builds of the same
#     staging tree produce byte-stable entry lists.
#   - No shell, no wildcards, no path concatenation surprises: names are
#     computed with Path.GetRelativePath and '/'-normalized; '..' or
#     rooted names are rejected.
#
# Usage:
#   . ./scripts/ci/New-PortableZip.ps1     # (CWD = repository root)
#   $r = New-PortableZip -StageRoot <staging wrapper dir> -ZipPath <out.zip>
#   if ($r.Error) { fail }
#   $r.Entries  # normalized entry names actually written

function New-PortableZip {
    [CmdletBinding()]
    [OutputType([pscustomobject])]
    param(
        [Parameter(Mandatory = $true)][string]$StageRoot,
        [Parameter(Mandatory = $true)][string]$ZipPath
    )

    $result = [pscustomobject]@{
        Entries = @()
        Count   = 0
        Error   = ""
    }

    if (-not (Test-Path -LiteralPath $StageRoot -PathType Container)) {
        $result.Error = "New-PortableZip: staging root does not exist or is not a directory: $StageRoot"
        return $result
    }
    $stageFull = (Resolve-Path -LiteralPath $StageRoot).ProviderPath.TrimEnd('\', '/')
    if ([string]::IsNullOrWhiteSpace($stageFull)) {
        $result.Error = "New-PortableZip: staging root resolved empty"
        return $result
    }

    $zipFull = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($ZipPath)
    $zipDir = Split-Path -Parent $zipFull
    if (-not (Test-Path -LiteralPath $zipDir)) {
        New-Item -ItemType Directory -Force -Path $zipDir | Out-Null
    }
    if (Test-Path -LiteralPath $zipFull) { Remove-Item -LiteralPath $zipFull -Force }

    Add-Type -AssemblyName System.IO.Compression
    Add-Type -AssemblyName System.IO.Compression.FileSystem

    # Collect ALL directories (empty ones included) and files first, so
    # directory entries are explicit and nothing disappears silently.
    $dirs = @()
    $files = @()
    try {
        $dirs = @(Get-ChildItem -LiteralPath $stageFull -Recurse -Force -Directory | Sort-Object FullName)
        $files = @(Get-ChildItem -LiteralPath $stageFull -Recurse -Force -File | Sort-Object FullName)
    }
    catch {
        $result.Error = "New-PortableZip: staging tree enumeration failed: $($_.Exception.Message)"
        return $result
    }

    $norm = [predicate[string]] { param($p) $p -replace '\\', '/' }
    $entries = New-Object System.Collections.Generic.List[string]
    $fs = $null
    try {
        $fs = [System.IO.File]::Open($zipFull, [System.IO.FileMode]::CreateNew)
        $archive = New-Object System.IO.Compression.ZipArchive($fs, [System.IO.Compression.ZipArchiveMode]::Create)
        try {
            # 1. The staging-wrapper CHILD directories themselves are NOT
            #    entries — only what is INSIDE the app directory. The app
            #    directory (the only child of the wrapper) becomes the
            #    package root.
            foreach ($d in $dirs) {
                $rel = [System.IO.Path]::GetRelativePath($stageFull, $d.FullName)
                if ([string]::IsNullOrWhiteSpace($rel) -or $rel -eq ".") { continue }
                if ($rel -like "*..*" -or [System.IO.Path]::IsPathRooted($rel)) {
                    $result.Error = "New-PortableZip: refusing unsafe directory path: $rel"
                    return $result
                }
                $name = ($rel -replace '\\', '/') + "/"
                $de = $archive.CreateEntry($name, [System.IO.Compression.CompressionLevel]::Optimal)
                $entries.Add($name) | Out-Null
            }
            # 2. Files at their relative paths, bytes untouched.
            foreach ($f in $files) {
                $rel = [System.IO.Path]::GetRelativePath($stageFull, $f.FullName)
                if ([string]::IsNullOrWhiteSpace($rel) -or $rel -eq ".") { continue }
                if ($rel -like "*..*" -or [System.IO.Path]::IsPathRooted($rel)) {
                    $result.Error = "New-PortableZip: refusing unsafe file path: $rel"
                    return $result
                }
                $name = $rel -replace '\\', '/'
                $fe = $archive.CreateEntry($name, [System.IO.Compression.CompressionLevel]::Optimal)
                $src = [System.IO.File]::OpenRead($f.FullName)
                try {
                    $dst = $fe.Open()
                    try { $src.CopyTo($dst) } finally { $dst.Dispose() }
                }
                finally { $src.Dispose() }
                $entries.Add($name) | Out-Null
            }
            # The application directory itself must exist as the single
            # root entry even if the staging wrapper ever contained it as
            # an empty directory (defensive: an empty app dir would
            # otherwise produce an entry-less ZIP).
            if ($entries.Count -eq 0) {
                $result.Error = "New-PortableZip: staging tree produced zero entries (refusing to write an empty package)"
                return $result
            }
        }
        finally { $archive.Dispose() }
    }
    catch {
        $result.Error = "New-PortableZip: archive write failed: $($_.Exception.Message)"
        if ($fs) { $fs.Dispose() }
        if (Test-Path -LiteralPath $zipFull) { Remove-Item -LiteralPath $zipFull -Force }
        return $result
    }
    finally { if ($fs) { $fs.Dispose() } }

    $sorted = $entries | Sort-Object -Unique
    foreach ($e in $sorted) {
        # Entry names are relative, '/'-separated package paths. Reject
        # anything that could escape the package root or that kept a
        # Windows separator: '..' segments, rooted names, backslashes.
        if ($e -match '(^|/)\.\.(/|$)' -or $e -match '\\' -or $e.StartsWith('/')) {
            $result.Error = "New-PortableZip: unsafe package entry name (path traversal or backslash): $e"
            return $result
        }
    }
    $result.Entries = @($sorted)
    $result.Count = @($sorted).Count
    return $result
}
