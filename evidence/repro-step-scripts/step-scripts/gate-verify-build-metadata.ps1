Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
$PSNativeCommandUseErrorActionPreference = $false
. (Join-Path $env:SHEYTAN_REPO_ROOT "scripts/ci/Invoke-CapturedProcess.ps1")
    Write-Output "helper loaded (this line was never reached in run 42921967187)"
    exit 0
if (Test-Path Variable:LASTEXITCODE) { exit $LASTEXITCODE } else { exit 0 }

