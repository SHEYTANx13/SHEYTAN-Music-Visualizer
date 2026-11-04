# Capture-Step — command-capture helper (PowerShell side).
#
# Dot-source this file, ensure $env:CI_LOG_FILE is set, then call:
#   Capture-Step -Job <job> -Step <step> -Command { <native command> }
#
# Contract:
#   - Capture native stdout AND stderr from the first byte into
#     $env:CI_LOG_FILE (via Tee-Object -Append).
#   - Run the gate in a CHILD pwsh process. This makes `exit` inside
#     the -Command scriptblock mean "exit this gate with this code",
#     NOT "terminate the whole build script". (The in-process form
#     previously unwound build-windows.ps1 at the first `exit 0`.)
#   - The original exit code of the gate is $LASTEXITCODE of the child
#     process, captured immediately after it exits. A child crash is
#     recorded as an error with exit code 1; an exit code is never
#     fabricated.
#   - Append a structured record: JOB, STEP, STARTED, WORKDIR, COMMAND,
#     TOOL_CONTEXT, output, EXIT_CODE with PASS|FAIL verdict, ENDED.
#   - Return the original exit code; never mask a non-zero status.
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
        "COMMAND: $($Command.ToString().Trim())"
        "TOOL_CONTEXT: $ToolContext"
        "--- OUTPUT (stdout+stderr from first byte) ---"
    )

    # Serialize the gate into a standalone script so it runs in a child
    # pwsh process: `exit N` inside the block then means exactly
    # "this gate finished with exit code N".
    $slug = ($Step -replace '[^A-Za-z0-9_.-]', '_')
    $scriptDir = Join-Path (Split-Path -Parent $env:CI_LOG_FILE) "step-scripts"
    New-Item -ItemType Directory -Force -Path $scriptDir | Out-Null
    $scriptFile = Join-Path $scriptDir ("gate-{0}.ps1" -f $slug)
    $preamble = @'
Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
$PSNativeCommandUseErrorActionPreference = $false
'@
    Set-Content -Path $scriptFile -Value ($preamble + "`n" + $Command.ToString().Trim() + "`n") -Encoding utf8NoBOM

    $rc = $null
    $pwsh = (Get-Command pwsh -ErrorAction SilentlyContinue)
    if ($null -eq $pwsh) {
        Add-Content -Path $env:CI_LOG_FILE -Value "ERROR: pwsh executable not found on PATH; cannot run the gate in a child process."
        $rc = 1
    }
    else {
        & $pwsh.Source -NoProfile -NonInteractive -File $scriptFile 2>&1 | Tee-Object -FilePath $env:CI_LOG_FILE -Append
        # Save the child's exit code immediately, before anything else
        # can overwrite $LASTEXITCODE.
        if ($null -ne $LASTEXITCODE) {
            $rc = $LASTEXITCODE
        }
        else {
            # No child process actually ran.
            Add-Content -Path $env:CI_LOG_FILE -Value "ERROR: child pwsh produced no exit code (process did not start)."
            $rc = 1
        }
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
