# Capture-Step — Phase 1 command-capture helper (PowerShell side).
#
# Dot-source this file, ensure $env:CI_LOG_FILE is set, then call:
#   Capture-Step -Job <job> -Step <step> -Command { <native command> }
#
# Contract:
#   - Capture native stdout AND stderr from the first byte into
#     $env:CI_LOG_FILE (via Tee-Object -Append).
#   - Save $LASTEXITCODE IMMEDIATELY after the native process exits,
#     before formatting or another command can overwrite it.
#   - Script-level PowerShell exceptions are recorded as
#     "ERROR: PowerShell exception: ..." with exit code 1; an exception
#     is never reported as PASS just because $LASTEXITCODE was unset.
#   - Append a structured record: JOB, STEP, STARTED, WORKDIR, COMMAND,
#     TOOL_CONTEXT, output, EXIT_CODE with PASS|FAIL verdict, ENDED.
#   - Return the original exit code; never mask a non-zero status.
#
# Note: -Command MUST invoke native executables (go, git, gcc, ...).
# For cmdlet-only blocks $LASTEXITCODE is not authoritative and the
# caller should not use this helper.
#
# Encoding: UTF-8 without BOM is used consistently for the log
# (PowerShell 7 default for Add-Content / Tee-Object).

function Capture-Step {
    [CmdletBinding()]
    param(
        [Parameter(Mandatory = $true)][string]$Job,
        [Parameter(Mandatory = $true)][string]$Step,
        [Parameter(Mandatory = $true)][scriptblock]$Command,
        [string]$ToolContext = "-"
    )
    if (-not $env:CI_LOG_FILE) {
        throw "Capture-Step: `$env:CI_LOG_FILE must be set before the first call."
    }
    $workdir = (Get-Location).Path
    $started = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")

    Add-Content -Path $env:CI_LOG_FILE -Value @(
        ""
        "===== COMMAND BEGIN ====="
        "JOB: $Job"
        "STEP: $Step"
        "STARTED: $started"
        "WORKDIR: $workdir"
        "COMMAND: $Command"
        "TOOL_CONTEXT: $ToolContext"
        "--- OUTPUT (stdout+stderr from first byte) ---"
    )

    $rc = $null
    $exception = $null
    try {
        & $Command 2>&1 | Tee-Object -FilePath $env:CI_LOG_FILE -Append
    }
    catch {
        $exception = $_
    }

    # Save the native exit code immediately, before anything else can
    # overwrite $LASTEXITCODE.
    if ($null -ne $exception) {
        Add-Content -Path $env:CI_LOG_FILE -Value ("ERROR: PowerShell exception: " + $exception.ToString())
        $rc = 1
    }
    elseif ($null -ne $LASTEXITCODE) {
        $rc = $LASTEXITCODE
    }
    else {
        # No native process ran and no exception was thrown.
        $rc = 0
    }

    $ended = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
    $verdict = if ($rc -eq 0) { "PASS" } else { "FAIL" }
    Add-Content -Path $env:CI_LOG_FILE -Value @(
        "--- END OUTPUT ---"
        "EXIT_CODE: $rc $verdict"
        "ENDED: $ended"
        "===== COMMAND END ====="
    )

    $script:LastCaptureExit = $rc
    return $rc
}
