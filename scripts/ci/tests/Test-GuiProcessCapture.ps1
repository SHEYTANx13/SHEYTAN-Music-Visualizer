# Test-GuiProcessCapture — Windows integration fixture for the
# captured-process runner against the REAL GUI-subsystem executable.
#
# Mission contract (run-42921967187 §7 regression tests): launch the
# GUI-subsystem --version and --self-check modes through
# Invoke-CapturedProcess and verify the REAL exit code and the
# machine-readable self-check result file.
#
# Execution policy (honest evidence):
#   - Requires the built Windows EXE (bin/sheytan-music-visualizer.exe)
#     and a Windows host. Anything else records NOT RUN with the exact
#     reason and exits 0 — a missing prerequisite is never a pass.
#   - On Windows with the EXE present, every assertion runs for real and
#     a failure exits nonzero.

param(
    [string]$RepoRoot = (Split-Path -Parent (Split-Path -Parent (Split-Path -Parent (Split-Path -Parent $PSCommandPath))))
)

Set-StrictMode -Version 3.0
$ErrorActionPreference = "Stop"
$PSNativeCommandUseErrorActionPreference = $false

Write-Output "=============================================================="
Write-Output "GUI-PROCESS CAPTURE FIXTURES — $((Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ'))"
Write-Output ("pwsh: {0}" -f $PSVersionTable.PSVersion)
Write-Output ("is-windows: {0}" -f $IsWindows)
Write-Output "=============================================================="

if (-not $IsWindows) {
    Write-Output "NOT RUN: this fixture requires a Windows host (GUI-subsystem PE behavior is platform-defined)."
    Write-Output "SUMMARY: pass=0 fail=0 notrun=1"
    exit 0
}

$exe = Join-Path $RepoRoot "bin/sheytan-music-visualizer.exe"
if (-not (Test-Path -LiteralPath $exe)) {
    Write-Output "NOT RUN: built executable not found at $exe (build it first with scripts/build/build-windows.ps1)."
    Write-Output "SUMMARY: pass=0 fail=0 notrun=1"
    exit 0
}

. (Join-Path $RepoRoot "scripts/ci/Invoke-CapturedProcess.ps1")

$PASSED = 0; $FAILED = 0
function Verdict([string]$id, [bool]$ok, [string]$detail) {
    if ($ok) { $script:PASSED++; Write-Output ("PASS    {0}  {1}" -f $id, $detail) }
    else     { $script:FAILED++; Write-Output ("FAIL    {0}  {1}" -f $id, $detail) }
}

# W1: PE Optional Header subsystem is 2 (Windows GUI) on the BUILT artifact.
$bytes = [System.IO.File]::ReadAllBytes($exe)
$peOff = [BitConverter]::ToInt32($bytes, 0x3C)
$subsystem = [BitConverter]::ToUInt16($bytes, $peOff + 4 + 20 + 68)
Verdict "W1" ($subsystem -eq 2) "PE Optional Header Subsystem = $subsystem (2 = GUI; -H=windowsgui preserved)"

# W2: --version through the captured runner: real exit code 0, first line
#     carries the product name and a commit field (never fabricated).
$ver = Invoke-CapturedProcess -FilePath $exe -ArgumentList @("--version") -TimeoutSeconds 120
$verOk = ($ver.Kind -eq "exited") -and ($ver.ExitCode -eq 0) -and ($ver.StdOut.Count -gt 0) -and
    ($ver.StdOut[0] -like "SHEYTAN Music Visualizer*")
Verdict "W2" $verOk "captured --version: kind=$($ver.Kind) exit=$($ver.ExitCode) line='$($ver.StdOut | Select-Object -First 1)'"

# W3: --self-check through the captured runner with the machine-readable
#     result file; the file must contain the passing verdict.
$selfOut = Join-Path ([System.IO.Path]::GetTempPath()) ("gui-capture-self-check-" + [guid]::NewGuid().ToString("N") + ".txt")
try {
    $self = Invoke-CapturedProcess -FilePath $exe -ArgumentList @("--self-check", "--self-check-out", $selfOut) -TimeoutSeconds 180
    $selfRcOk = ($self.Kind -eq "exited") -and ($self.ExitCode -eq 0)
    $fileOk = $false
    if (Test-Path -LiteralPath $selfOut) {
        $text = Get-Content -LiteralPath $selfOut
        $fileOk = [bool]($text | Where-Object { $_ -match "self-check: all foundation invariants hold" })
    }
    Verdict "W3" ($selfRcOk -and $fileOk) "captured --self-check: exit=$($self.ExitCode); result file verdict present: $fileOk"
}
finally {
    if (Test-Path -LiteralPath $selfOut) { Remove-Item -LiteralPath $selfOut -Force }
}

# W4: observation metadata is real (PID, UTC window, elapsed >= 0).
$metaOk = ($null -ne $ver.ProcessId) -and ($null -ne $ver.StartedUtc) -and ($null -ne $ver.EndedUtc) -and
    ($ver.ElapsedMilliseconds -ge 0) -and ($ver.Kind -eq "exited")
Verdict "W4" $metaOk "observation metadata: pid=$($ver.ProcessId) elapsed_ms=$($ver.ElapsedMilliseconds) started=$($ver.StartedUtc) ended=$($ver.EndedUtc)"

Write-Output "SUMMARY: pass=$PASSED fail=$FAILED"
if ($FAILED -gt 0) { exit 1 }
exit 0
