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
#   - Each generated gate script is SELF-CONTAINED: a child pwsh
#     process cannot see the caller's PowerShell variables, so every
#     gate initializes and validates its own local $goTags argument
#     array from the single documented source $env:SHEYTAN_GO_TAGS.
#     (Run 41915116613 root cause 1: four gates referenced the
#     parent-only $goTags and failed under StrictMode with "The
#     variable '$goTags' cannot be retrieved because it has not been
#     set.") A missing or malformed tag source fails the gate loudly
#     instead of silently building without tags.
#   - The generated script carries an EXIT-CODE EPILOGUE: pwsh -File
#     exits 0 when a script ends normally without `exit`, so a failing
#     native command at script end would otherwise be recorded as a
#     passing gate. (Root cause class 3: lost native exit codes.)
#   - Return the original exit code AND NOTHING ELSE on the output
#     stream. The child's console output is forwarded through
#     Write-Host (information stream), never through the success
#     stream: otherwise `$rc = Capture-Step ...` captures the whole
#     transcript plus the code, and the callers' `if ($rc -ne 0)`
#     becomes an array filter that is truthy for ANY passing gate with
#     output. (Run 41915116613 root cause 2: five PASS gates were
#     counted failed -> the false nine-gate summary.)
#   - The original exit code of the gate is $LASTEXITCODE of the child
#     process, captured immediately after it exits. A child crash is
#     recorded as an error with exit code 1; an exit code is never
#     fabricated.
#   - Append a structured record: JOB, STEP, STARTED, WORKDIR, COMMAND,
#     TOOL_CONTEXT, output, EXIT_CODE with PASS|FAIL verdict, ENDED.
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

# Initialize this gate's OWN local Go tag argument array from the
# documented environment source (a child pwsh process never inherits
# the caller's PowerShell variables).
if ([string]::IsNullOrWhiteSpace($env:SHEYTAN_GO_TAGS)) {
    Write-Output "ERROR: SHEYTAN_GO_TAGS is not set; each gate must initialize its own Go tag array from this documented source (the goffi_static build tag is required when Fyne and wgpu link together)."
    exit 1
}
$goTags = @($env:SHEYTAN_GO_TAGS -split '\s+' | Where-Object { $_ -ne '' })
if ($goTags.Count -eq 0) {
    Write-Output "ERROR: SHEYTAN_GO_TAGS parsed to an empty argument array."
    exit 1
}
foreach ($tagToken in $goTags) {
    if ($tagToken -notmatch '^[A-Za-z0-9_.,=/+%-]+$') {
        Write-Output "ERROR: SHEYTAN_GO_TAGS contains an invalid argument token: '$tagToken'."
        exit 1
    }
}
'@
    # Single-quoted epilogue: it must reach the generated file verbatim.
    $epilogue = 'if (Test-Path Variable:LASTEXITCODE) { exit $LASTEXITCODE } else { exit 0 }'
    Set-Content -Path $scriptFile -Value ($preamble + "`n" + $Command.ToString().Trim() + "`n" + $epilogue + "`n") -Encoding utf8NoBOM

    $rc = $null
    $pwsh = (Get-Command pwsh -ErrorAction SilentlyContinue)
    if ($null -eq $pwsh) {
        Add-Content -Path $env:CI_LOG_FILE -Value "ERROR: pwsh executable not found on PATH; cannot run the gate in a child process."
        $rc = 1
    }
    else {
        # Stream discipline: the child's console output must be visible
        # in the job log AND must never become this function's return
        # value. Tee-Object passes output through the SUCCESS stream; a
        # bare call would make `$rc = Capture-Step ...` capture the
        # whole transcript plus the exit code, and `if ($rc -ne 0)`
        # would become an array filter that is truthy for ANY passing
        # gate with output.
        & $pwsh.Source -NoProfile -NonInteractive -File $scriptFile 2>&1 | Tee-Object -FilePath $env:CI_LOG_FILE -Append | ForEach-Object { Write-Host $_ }
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
