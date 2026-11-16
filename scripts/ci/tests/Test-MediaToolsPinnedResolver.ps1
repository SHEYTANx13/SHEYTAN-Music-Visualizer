# Test-MediaToolsPinnedResolver — run-43767556517 repair fixtures for
# scripts/ci/MediaTools-Pinned.ps1 (the authoritative media-tools
# resolver contract, §2.2 of the run report).
#
# Runnable on ANY pwsh 7 host (Linux CI evidence; Windows runner
# evidence comes from the platform gates). Covers the required matrix:
#
#   MX1  authoritative schema inventory accepted (valid result object)
#   MX2  null object rejected, diagnostic names the defect
#   MX3  object[] (the Write-Output pollution defect class) rejected
#   MX4  EVERY schema field removed one at a time -> rejected, the
#        diagnostic NAMES the absent field
#   MX5  empty (whitespace) field rejected
#   MX6  invalid SHA256 formats rejected (uppercase, short, non-hex)
#   MX7  AssetURL/Version incoherence rejected
#   MX8  -RequireFiles with a missing binary rejects naming the field
#   MX9  paths with spaces + Unicode accepted (real temp files)
#   MX10 static source contract: the resolver's Get-SheytanPinnedMediaTools
#        body contains NO Write-Output (success-stream pollution class),
#        Test-SheytanToolsResultSchema exists, pinned SHA256 is 64-hex
#        lowercase, pinned AssetURL contains the pinned Version
#   MX11 env-gated dynamic contract: when a verified cache exists
#        (SHEYTAN_MEDIA_TOOLS_CACHE_DIR populated by CI), the resolver's
#        success stream carries EXACTLY ONE schema-valid [pscustomobject].
#        Without the cache the fixture records NOT RUN — never a fake pass.
#
# Exit 0 = all fixtures PASS (NOT RUN allowed); nonzero = at least one FAIL.

param(
    [string]$RepoRoot = (Split-Path -Parent (Split-Path -Parent (Split-Path -Parent (Split-Path -Parent $PSCommandPath))))
)

Set-StrictMode -Version 3.0
$ErrorActionPreference = "Stop"

$PASSED = 0; $FAILED = 0; $NOTRUN = 0
function Verdict([string]$id, [string]$state, [string]$detail) {
    switch ($state) {
        "PASS"   { $script:PASSED++;  Write-Output ("PASS    {0}  {1}" -f $id, $detail) }
        "FAIL"   { $script:FAILED++;  Write-Output ("FAIL    {0}  {1}" -f $id, $detail) }
        "NOTRUN" { $script:NOTRUN++; Write-Output ("NOT RUN {0}  {1}" -f $id, $detail) }
    }
}

Write-Output "=============================================================="
Write-Output "MEDIA-TOOLS PINNED RESOLVER CONTRACT FIXTURES — $((Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ'))"
Write-Output "pwsh: $($PSVersionTable.PSVersion)"
Write-Output "repo root: $RepoRoot"
Write-Output "=============================================================="

$resolverPath = Join-Path $RepoRoot "scripts/ci/MediaTools-Pinned.ps1"
if (-not (Test-Path -LiteralPath $resolverPath -PathType Leaf)) {
    Verdict "MX0" "FAIL" "resolver file missing: $resolverPath"
    Write-Output "SUMMARY: pass=$PASSED fail=$FAILED notrun=$NOTRUN"
    exit 1
}
. $resolverPath

# Valid synthetic result over REAL temp files (spaces + Unicode names).
$schema = $script:SheytanPinnedToolsSchema
$work = Join-Path ([System.IO.Path]::GetTempPath()) ("SHEYTAN resolver fixtures Музыка " + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $work | Out-Null
$archive = Join-Path $work "pinned pack.zip"
$ff = Join-Path $work "bin dir" "ffmpeg.exe"
$fp = Join-Path $work "bin dir" "ffprobe.exe"
New-Item -ItemType Directory -Force -Path (Join-Path $work "bin dir") | Out-Null
foreach ($f in @($archive, $ff, $fp)) { Set-Content -Path $f -Value "fixture-bytes" -Encoding utf8NoBOM }

function New-ValidResult {
    [pscustomobject][ordered]@{
        Version       = "n9.0.2-48-gd4070a14ce"
        AssetURL      = "https://github.com/BtbN/FFmpeg-Builds/releases/download/autobuild-2026-11-08-12-35/ffmpeg-n9.0.2-48-gd4070a14ce-win64-gpl-9.0.zip"
        ArchivePath   = $archive
        SHA256        = ("40608cfdd40b0e7929704059daee4af369f7a9984ea7aa8ee416d78cce2e8aae")
        BinaryDir     = (Join-Path $work "bin dir")
        FFmpegPath    = $ff
        FFprobePath   = $fp
        Platform      = "windows/amd64"
        ArchiveFormat = "zip"
    }
}

# --- MX1: valid result accepted (schema only, then with file checks) ------
$ok = Test-SheytanToolsResultSchema -Result (New-ValidResult)
Verdict "MX1a" ($(if ($ok) { "PASS" } else { "FAIL" })) "valid authoritative result accepted (schema)"
$ok = Test-SheytanToolsResultSchema -Result (New-ValidResult) -RequireFiles
Verdict "MX1b" ($(if ($ok) { "PASS" } else { "FAIL" })) "valid result accepted with on-disk file checks"

# --- MX2: null object rejected --------------------------------------------
try {
    $ok = Test-SheytanToolsResultSchema -Result $null
    Verdict "MX2" ($(if (-not $ok) { "PASS" } else { "FAIL" })) "null object rejected"
} catch {
    Verdict "MX2" "FAIL" "null object check threw instead of returning false: $($_.Exception.Message)"
}

# --- MX3: object[] pollution defect class rejected --------------------------
try {
    $polluted = @("downloading pinned FFmpeg archive: somewhere", (New-ValidResult))
    $ok = Test-SheytanToolsResultSchema -Result $polluted
    Verdict "MX3" ($(if (-not $ok) { "PASS" } else { "FAIL" })) "object[] (Write-Output pollution class) rejected"
} catch {
    Verdict "MX3" "FAIL" "object[] check threw instead of returning false: $($_.Exception.Message)"
}

# --- MX4: every field removed one at a time -> named diagnostic ------------
$mx4 = $true
$namedDiagnostics = 0
foreach ($field in $schema) {
    $src = New-ValidResult
    $src.PSObject.Properties.Remove($field)
    $diag = ""
    try {
        $out = Test-SheytanToolsResultSchema -Result $src -ErrorVariable +mx4Err -ErrorAction SilentlyContinue
        $diag = if ($mx4Err.Count -gt 0) { [string]$mx4Err[0].Exception.Message } else { "" }
        $mx4Err.Clear()
    } catch { $diag = $_.Exception.Message; $out = $false }
    if ($out) { $mx4 = $false; Write-Output ("        MX4: removing '{0}' was NOT rejected" -f $field) }
    elseif (-not $diag.Contains($field)) {
        $mx4 = $false
        Write-Output ("        MX4: diagnostic for missing '{0}' does not name the field: {1}" -f $field, $diag)
    } else { $namedDiagnostics++ }
}
Verdict "MX4" ($(if ($mx4) { "PASS" } else { "FAIL" })) "every missing schema field rejected with a field-naming diagnostic ($namedDiagnostics/$($schema.Count) named)"

# --- MX5: empty field rejected ----------------------------------------------
$src = New-ValidResult
$src.BinaryDir = "   "
$ok = Test-SheytanToolsResultSchema -Result $src
Verdict "MX5" ($(if (-not $ok) { "PASS" } else { "FAIL" })) "whitespace-only field rejected"

# --- MX6: invalid digest formats rejected -----------------------------------
$mx6 = $true
foreach ($b in @(
    [pscustomobject]@{ h = "40608CFDD40B0E7929704059DAEE4AF369F7A9984EA7AA8EE416D78CCE2E8AAE"; why = "uppercase" },
    [pscustomobject]@{ h = "abcd1234"; why = "too short" },
    [pscustomobject]@{ h = ("z" * 64); why = "non-hex" }
)) {
    $src = New-ValidResult
    $src.SHA256 = $b.h
    if (Test-SheytanToolsResultSchema -Result $src) { $mx6 = $false; Write-Output ("        MX6: '{0}' digest was NOT rejected" -f $b.why) }
}
Verdict "MX6" ($(if ($mx6) { "PASS" } else { "FAIL" })) "invalid SHA256 formats rejected (uppercase/short/non-hex)"

# --- MX7: AssetURL/Version incoherence rejected -----------------------------
$src = New-ValidResult
$src.Version = "n9.9.9-unknown"
$ok = Test-SheytanToolsResultSchema -Result $src
Verdict "MX7" ($(if (-not $ok) { "PASS" } else { "FAIL" })) "AssetURL not containing the pinned Version rejected"

# --- MX8: missing binary rejected naming the field --------------------------
$src = New-ValidResult
$src.FFprobePath = Join-Path $work "missing-ffprobe.exe"
$named = $false
$out = $true
try {
    $out = Test-SheytanToolsResultSchema -Result $src -RequireFiles -ErrorVariable +mx8Err -ErrorAction SilentlyContinue
    if ($mx8Err.Count -gt 0) { $named = ([string]$mx8Err[0].Exception.Message).Contains("FFprobePath") }
    $mx8Err.Clear()
} catch { $out = $false }
Verdict "MX8" ($(if (-not $out -and $named) { "PASS" } else { "FAIL" })) "missing binary rejected and the diagnostic names FFprobePath"

# --- MX9: spaces + Unicode paths accepted ------------------------------------
$ok = Test-SheytanToolsResultSchema -Result (New-ValidResult) -RequireFiles
Verdict "MX9" ($(if ($ok) { "PASS" } else { "FAIL" })) "paths with spaces + Unicode accepted end to end"

# --- MX10: static source contract on the resolver ---------------------------
$resolverText = Get-Content -LiteralPath $resolverPath -Raw
$fnStart = $resolverText.IndexOf("function Get-SheytanPinnedMediaTools")
$fnBody = if ($fnStart -ge 0) { $resolverText.Substring($fnStart) } else { "" }
$noWriteOutput = ($fnBody -notmatch "(?m)^\s*Write-Output\b")
$schemaFnPresent = ($resolverText -match "function Test-SheytanToolsResultSchema")
$pinnedHash = "40608cfdd40b0e7929704059daee4af369f7a9984ea7aa8ee416d78cce2e8aae"
$hashPinned = $resolverText.Contains($pinnedHash)
$urlVersionCoherent = $resolverText.Contains("ffmpeg-n9.0.2-48-gd4070a14ce-win64-gpl-9.0.zip") -and $resolverText.Contains('"n9.0.2-48-gd4070a14ce"')
$ver10 = $noWriteOutput -and $schemaFnPresent -and $hashPinned -and $urlVersionCoherent
if (-not $ver10) {
    Write-Output ("        MX10 detail: noWriteOutput={0} schemaFn={1} hashPinned={2} urlCoherent={3}" -f $noWriteOutput, $schemaFnPresent, $hashPinned, $urlVersionCoherent)
}
Verdict "MX10" ($(if ($ver10) { "PASS" } else { "FAIL" })) "resolver source: no Write-Output in Get-SheytanPinnedMediaTools, schema validator present, pinned hash/URL coherent"

# --- MX11: env-gated dynamic contract (CI cache) ------------------------------
$cache = $env:SHEYTAN_MEDIA_TOOLS_CACHE_DIR
$cacheReady = $false
if (-not [string]::IsNullOrWhiteSpace($cache) -and (Test-Path -LiteralPath (Join-Path $cache "ffmpeg-n9.0.2-48-gd4070a14ce-win64-gpl-9.0.zip") -PathType Leaf) -and (Test-Path -LiteralPath (Join-Path $cache "extracted-windows") -PathType Container)) {
    $cacheReady = $true
}
if ($cacheReady) {
    $out = @(& (Get-Command pwsh).Source -NoProfile -NonInteractive -Command {
        . $using:resolverPath
        $tools = Get-SheytanPinnedMediaTools
        @($tools).Count
        if ($tools -is [pscustomobject]) { "TYPE_OK" } else { "TYPE_BAD" }
        $t = $using:schema; foreach ($f in $t) { if ($null -eq $tools.PSObject.Properties[$f]) { "MISSING:$f" } }
    })
    $countOk = ($out[0] -eq "1")
    $typeOk = ($out -contains "TYPE_OK")
    $noMissing = -not ($out | Where-Object { "$_" -like "MISSING:*" })
    Verdict "MX11" ($(if ($countOk -and $typeOk -and $noMissing) { "PASS" } else { "FAIL" })) "cached resolver call returns exactly one schema-complete [pscustomobject] on the success stream"
}
else {
    Verdict "MX11" "NOTRUN" "requires SHEYTAN_MEDIA_TOOLS_CACHE_DIR with the verified pinned archive + extraction (CI stages this; recorded honestly, never faked)"
}

# --- cleanup -----------------------------------------------------------------
Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue

Write-Output "SUMMARY: pass=$PASSED fail=$FAILED notrun=$NOTRUN"
if ($FAILED -gt 0) { exit 1 }
exit 0
