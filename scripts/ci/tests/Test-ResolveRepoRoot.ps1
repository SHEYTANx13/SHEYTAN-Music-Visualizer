# Test-ResolveRepoRoot — run-42921967187 regression fixtures for the
# deterministic repository-root contract (scripts/ci/Resolve-RepoRoot.ps1
# and the Capture-Step -RequireRepoRoot preamble).
#
# Runnable on ANY host with pwsh 7. Every scenario runs the resolution in a
# SEPARATE `pwsh -NoProfile -NonInteractive -File` child process (the exact
# surface the generated gates use), never only in this driver process.
#
# Fixtures:
#   R1  SHEYTAN_REPO_ROOT set correctly            -> resolves to it
#   R2  SHEYTAN_REPO_ROOT unset, GITHUB_WORKSPACE  -> falls back
#   R3  both absent                                -> loud precise failure
#   R4  repository root containing spaces          -> resolves
#   R5  SHEYTAN_REPO_ROOT set-but-invalid, GITHUB_WORKSPACE valid -> fallback
#   R6  required helper missing                    -> failure names the path
#   R7  generated gate preamble (Capture-Step -RequireRepoRoot):
#       a) no env vars      -> gate exits 1 BEFORE touching package output
#       b) SHEYTAN_REPO_ROOT -> helper imports, package output touched
#       c) GITHUB_WORKSPACE  -> fallback, helper imports via canonical path
#   R8  static contract: build-windows.ps1 declares -RequireRepoRoot for the
#       three helper-importing gates and no gate body uses
#       $env:SHEYTAN_REPO_ROOT directly.
#
# Exit 0 = all fixtures PASS; nonzero = at least one FAIL.

param(
    [string]$RepoRoot = (Split-Path -Parent (Split-Path -Parent (Split-Path -Parent (Split-Path -Parent $PSCommandPath))))
)

Set-StrictMode -Version 3.0
$ErrorActionPreference = "Stop"
$PSNativeCommandUseErrorActionPreference = $false

$PASSED = 0; $FAILED = 0
function Verdict([string]$id, [bool]$ok, [string]$detail) {
    if ($ok) { $script:PASSED++; Write-Output ("PASS    {0}  {1}" -f $id, $detail) }
    else     { $script:FAILED++; Write-Output ("FAIL    {0}  {1}" -f $id, $detail) }
}
function Reset-RootEnv {
    $env:SHEYTAN_REPO_ROOT = $null
    $env:GITHUB_WORKSPACE = $null
}

$pwsh = (Get-Command pwsh).Source
$sandboxBase = Join-Path ([System.IO.Path]::GetTempPath()) ("sheytan-root-tests-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $sandboxBase | Out-Null

function New-SandboxRepo([string]$base) {
    # Hermetic minimal "repository": the resolver validates identity by
    # scripts/ci/Resolve-RepoRoot.ps1 + go.mod, exactly as in the real repo.
    $repo = Join-Path $base "repo"
    New-Item -ItemType Directory -Force -Path (Join-Path $repo "scripts/ci") | Out-Null
    Copy-Item (Join-Path $RepoRoot "scripts/ci/Resolve-RepoRoot.ps1") (Join-Path $repo "scripts/ci/Resolve-RepoRoot.ps1") -Force
    Set-Content -Path (Join-Path $repo "go.mod") -Value "module sandbox/repo`n" -Encoding utf8NoBOM
    Set-Content -Path (Join-Path $repo "scripts/ci/Fake-Helper.ps1") -Value "param() # fake helper loaded" -Encoding utf8NoBOM
    return $repo
}

function New-CaseScript([string]$path, [string]$body) {
    Set-Content -Path $path -Encoding utf8NoBOM -Value ("Set-StrictMode -Version 3.0`n" + $body)
}

Write-Output "=============================================================="
Write-Output "REPO-ROOT RESOLUTION REGRESSION FIXTURES — $((Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ'))"
Write-Output "pwsh: $($PSVersionTable.PSVersion)"
Write-Output "real repo root: $RepoRoot"
Write-Output "=============================================================="

# --- R1: primary variable set correctly --------------------------------------
$sb = Join-Path $sandboxBase "r1"
$repo = New-SandboxRepo $sb
$case = Join-Path $sb "case.ps1"
New-CaseScript $case (@(
    ". (Join-Path '$($repo -replace "'", "''")' 'scripts/ci/Resolve-RepoRoot.ps1')",
    "`$root = Resolve-SheytanRepoRoot -GateName 'r1' -RequiredRelativePaths @('scripts/ci/Fake-Helper.ps1')",
    "if (`$root -ne '$($repo -replace "'", "''")') { Write-Output (`"MISMATCH: `" + `$root); exit 2 }",
    "exit 0"
) -join "`n")
Reset-RootEnv
$env:SHEYTAN_REPO_ROOT = $repo
& $pwsh -NoProfile -NonInteractive -File $case > (Join-Path $sb "out.txt") 2>&1
Verdict "R1" ($LASTEXITCODE -eq 0) "SHEYTAN_REPO_ROOT set correctly resolves"

# --- R2: fallback to GITHUB_WORKSPACE ----------------------------------------
$sb = Join-Path $sandboxBase "r2"
$repo = New-SandboxRepo $sb
$case = Join-Path $sb "case.ps1"
New-CaseScript $case (@(
    ". (Join-Path '$($repo -replace "'", "''")' 'scripts/ci/Resolve-RepoRoot.ps1')",
    "`$root = Resolve-SheytanRepoRoot -GateName 'r2'",
    "if (`$root -ne '$($repo -replace "'", "''")') { exit 2 }",
    "exit 0"
) -join "`n")
Reset-RootEnv
$env:GITHUB_WORKSPACE = $repo
& $pwsh -NoProfile -NonInteractive -File $case > (Join-Path $sb "out.txt") 2>&1
Verdict "R2" ($LASTEXITCODE -eq 0) "unset SHEYTAN_REPO_ROOT falls back to GITHUB_WORKSPACE"

# --- R3: both absent -> loud precise failure ---------------------------------
$sb = Join-Path $sandboxBase "r3"
$repo = New-SandboxRepo $sb
$case = Join-Path $sb "case.ps1"
New-CaseScript $case (@(
    "try {",
    "  . (Join-Path '$($repo -replace "'", "''")' 'scripts/ci/Resolve-RepoRoot.ps1')",
    "  [void](Resolve-SheytanRepoRoot -GateName 'r3-gate-name')",
    "} catch {",
    "  Write-Output (`"EXPECTED-FAILURE: `" + `$_.Exception.Message)",
    "  exit 0",
    "}",
    "Write-Output 'ERROR: resolver did NOT fail with both roots absent'",
    "exit 1"
) -join "`n")
Reset-RootEnv
& $pwsh -NoProfile -NonInteractive -File $case > (Join-Path $sb "out.txt") 2>&1
$out = Get-Content (Join-Path $sb "out.txt") -Raw
$precise = ($out -match "r3-gate-name") -and ($out -match "SHEYTAN_REPO_ROOT") -and ($out -match "GITHUB_WORKSPACE")
Verdict "R3" ($LASTEXITCODE -eq 0 -and $precise) "both roots absent fails loudly and names gate + candidates"

# --- R4: root containing spaces ----------------------------------------------
$sb = Join-Path $sandboxBase "sp ace dir"
$repo = New-SandboxRepo $sb
$case = Join-Path $sb "case.ps1"
New-CaseScript $case (@(
    ". (Join-Path '$($repo -replace "'", "''")' 'scripts/ci/Resolve-RepoRoot.ps1')",
    "`$root = Resolve-SheytanRepoRoot -GateName 'r4'",
    "if (`$root -ne '$($repo -replace "'", "''")') { exit 2 }",
    "exit 0"
) -join "`n")
Reset-RootEnv
$env:SHEYTAN_REPO_ROOT = $repo
& $pwsh -NoProfile -NonInteractive -File $case > (Join-Path $sb "out.txt") 2>&1
Verdict "R4" ($LASTEXITCODE -eq 0) "repository root containing spaces resolves"

# --- R5: set-but-invalid primary falls back to valid GITHUB_WORKSPACE --------
$sb = Join-Path $sandboxBase "r5"
$repo = New-SandboxRepo $sb
$bogus = Join-Path $sb "not-a-repo"
New-Item -ItemType Directory -Force -Path $bogus | Out-Null
$case = Join-Path $sb "case.ps1"
New-CaseScript $case (@(
    ". (Join-Path '$($repo -replace "'", "''")' 'scripts/ci/Resolve-RepoRoot.ps1')",
    "`$root = Resolve-SheytanRepoRoot -GateName 'r5'",
    "if (`$root -ne '$($repo -replace "'", "''")') { exit 2 }",
    "exit 0"
) -join "`n")
Reset-RootEnv
$env:SHEYTAN_REPO_ROOT = $bogus
$env:GITHUB_WORKSPACE = $repo
& $pwsh -NoProfile -NonInteractive -File $case > (Join-Path $sb "out.txt") 2>&1
Verdict "R5" ($LASTEXITCODE -eq 0) "invalid SHEYTAN_REPO_ROOT falls back to valid GITHUB_WORKSPACE"

# --- R6: missing required helper -> precise failure naming the path ----------
$sb = Join-Path $sandboxBase "r6"
$repo = New-SandboxRepo $sb
$case = Join-Path $sb "case.ps1"
New-CaseScript $case (@(
    "try {",
    "  . (Join-Path '$($repo -replace "'", "''")' 'scripts/ci/Resolve-RepoRoot.ps1')",
    "  [void](Resolve-SheytanRepoRoot -GateName 'r6-gate' -RequiredRelativePaths @('scripts/ci/Invoke-CapturedProcess.ps1'))",
    "} catch {",
    "  Write-Output (`"EXPECTED-FAILURE: `" + `$_.Exception.Message)",
    "  exit 0",
    "}",
    "Write-Output 'ERROR: resolver did NOT fail for a missing required helper'",
    "exit 1"
) -join "`n")
Reset-RootEnv
$env:SHEYTAN_REPO_ROOT = $repo
& $pwsh -NoProfile -NonInteractive -File $case > (Join-Path $sb "out.txt") 2>&1
$out = Get-Content (Join-Path $sb "out.txt") -Raw
Verdict "R6" ($LASTEXITCODE -eq 0 -and $out -match "scripts/ci/Invoke-CapturedProcess.ps1") "missing required helper fails naming the expected relative path"

# --- R7: generated gate preamble (Capture-Step -RequireRepoRoot) -------------
$sb = Join-Path $sandboxBase "r7"
$repo = New-SandboxRepo $sb
$capLog = Join-Path $sb "capture.log"
Set-Content -Path $capLog -Value "" -NoNewline
$env:CI_LOG_FILE = $capLog
. (Join-Path $RepoRoot "scripts/ci/Capture-Step.ps1")
$marker = Join-Path $repo "package-output-marker.txt"
$rc = Capture-Step -Job "regression" -Step "gate-r7" -ToolContext "gate:r7" `
    -RequireRepoRoot -RepoRootHelpers @("scripts/ci/Fake-Helper.ps1") -Command {
    . (Join-Path $repoRoot "scripts/ci/Fake-Helper.ps1")
    Set-Content -Path (Join-Path (Get-Location).Path "package-output-marker.txt") -Value "accessed"
    exit 0
}
# NOTE: the gate body above resolves $repoRoot from the preamble; the marker
# is written relative to the CHILD's working directory, which the driver
# controls below. Generated gate lives under the sandbox step-scripts dir.
$generated = Join-Path $sb "step-scripts/gate-gate-r7.ps1"
if (-not (Test-Path $generated)) { $generated = (Get-ChildItem (Join-Path $sb "step-scripts") -Filter "gate-gate-r7*.ps1" | Select-Object -First 1).FullName }
$preambleOk = [bool]((Get-Content $generated -Raw) -match "Resolve-SheytanRepoRoot -GateName 'gate-r7'")

# (a) no env vars -> exit 1, package output NOT touched
Reset-RootEnv
Push-Location $repo
& $pwsh -NoProfile -NonInteractive -File $generated > (Join-Path $sb "r7a.out") 2>&1
$r7a = $LASTEXITCODE
Pop-Location
$r7aOut = Get-Content (Join-Path $sb "r7a.out") -Raw
$noMarker = -not (Test-Path $marker)
Verdict "R7a" ($r7a -ne 0 -and $noMarker -and $r7aOut -match "gate-r7") "gate without root vars exits 1 BEFORE touching package output"

# (b) SHEYTAN_REPO_ROOT set -> helper imported, marker written
Reset-RootEnv
$env:SHEYTAN_REPO_ROOT = $repo
if (Test-Path $marker) { Remove-Item $marker -Force }
Push-Location $repo
& $pwsh -NoProfile -NonInteractive -File $generated > (Join-Path $sb "r7b.out") 2>&1
$r7b = $LASTEXITCODE
Pop-Location
Verdict "R7b" ($r7b -eq 0 -and (Test-Path $marker) -and $preambleOk) "gate with SHEYTAN_REPO_ROOT imports helper and reaches package output"

# (c) only GITHUB_WORKSPACE -> fallback import via canonical resolver
Reset-RootEnv
$env:GITHUB_WORKSPACE = $repo
if (Test-Path $marker) { Remove-Item $marker -Force }
Push-Location $repo
& $pwsh -NoProfile -NonInteractive -File $generated > (Join-Path $sb "r7c.out") 2>&1
$r7c = $LASTEXITCODE
Pop-Location
$r7cOut = Get-Content (Join-Path $sb "r7c.out") -Raw
Verdict "R7c" ($r7c -eq 0 -and (Test-Path $marker) -and $r7cOut -match "repo root resolved") "gate with only GITHUB_WORKSPACE imports helper through the fallback"

# --- R8: static contract on build-windows.ps1 --------------------------------
$bw = Get-Content (Join-Path $RepoRoot "scripts/build/build-windows.ps1") -Raw
# Count only EXECUTABLE Capture-Step invocations, never comment prose.
$declared = ([regex]::Matches($bw, '(?m)^\s*\$rc = Capture-Step .*-RequireRepoRoot')).Count
# Exactly ONE executable $env:SHEYTAN_REPO_ROOT occurrence is allowed: the
# documented parent-side assignment. Gate bodies must use $repoRoot.
$directUses = ([regex]::Matches($bw, '(?m)^\s*\$env:SHEYTAN_REPO_ROOT = ')).Count
$assignment = [bool]($bw -match [regex]::Escape('$env:SHEYTAN_REPO_ROOT = $repoRoot'))
$threeHelpers = ($bw -match [regex]::Escape('-RepoRootHelpers @("scripts/ci/Invoke-CapturedProcess.ps1")')) -and
                ($bw -match [regex]::Escape('-RepoRootHelpers @("scripts/ci/New-PortableZip.ps1")'))
Verdict "R8" ($declared -eq 3 -and $directUses -eq 1 -and $assignment -and $threeHelpers) `
    "build-windows.ps1: 3 declared RequireRepoRoot gate invocations, single documented env assignment, no direct gate-body use"

# --- cleanup -----------------------------------------------------------------
Reset-RootEnv
Remove-Item -Recurse -Force $sandboxBase -ErrorAction SilentlyContinue

Write-Output "SUMMARY: pass=$PASSED fail=$FAILED"
if ($FAILED -gt 0) { exit 1 }
exit 0
