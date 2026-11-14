# Capture-Step — command-capture helper (PowerShell side).
#
# Dot-source this file, ensure $env:CI_LOG_FILE is set, then call:
#   Capture-Step -Job <job> -Step <step> -Command { <native command> } [-GoTags]
#
# -GoTags is OPT-IN: only gates that run Go commands consuming build
# tags (go test/vet/build and the executable build) may pass it.
#
# Contract:
#   - Capture native stdout AND stderr from the first byte into
#     $env:CI_LOG_FILE (via Tee-Object -Append).
#   - Run the gate in a CHILD pwsh process. This makes `exit` inside
#     the -Command scriptblock mean "exit this gate with this code",
#     NOT "terminate the whole build script". (The in-process form
#     previously unwound build-windows.ps1 at the first `exit 0`.)
#   - Each generated gate script is SELF-CONTAINED: a child pwsh
#     process cannot see the caller's PowerShell variables. Gates
#     called with -GoTags initialize and validate their own local
#     $goTags argument array from the single documented source
#     $env:SHEYTAN_GO_TAGS. (Run 41915116613 root cause 1: four gates
#     referenced the parent-only $goTags and failed under StrictMode
#     with "The variable '$goTags' cannot be retrieved because it has
#     not been set.") A missing or malformed tag source fails such a
#     gate loudly instead of silently building without tags.
#     Tag isolation (run 42231355353 repair): the tag preamble is
#     generated ONLY for -GoTags gates. Gates that never run a
#     tag-consuming Go command (UCRT identity, gofmt, module hygiene,
#     runtime output checks, portable staging, PE/ZIP validation) must
#     not be blocked by a missing build-tag variable.
#   - REPO-ROOT CONTRACT (run 42921967187 repair): gates that dot-source
#     another repository script declare that dependency with
#     -RequireRepoRoot and -RepoRootHelpers. The generated gate then
#     carries a resolution preamble that resolves the repository root
#     from the environment (SHEYTAN_REPO_ROOT, falling back to the
#     standard GITHUB_WORKSPACE; never the current working directory),
#     dot-sources the canonical scripts/ci/Resolve-RepoRoot.ps1, and
#     validates every required helper file BEFORE the gate body runs.
#     Run 42921967187 root cause: three gates referenced
#     $env:SHEYTAN_REPO_ROOT directly and that variable was never
#     defined for the spawned gate process, so all three died on
#     "Cannot bind argument to parameter 'Path' because it is null."
#     before any check executed. With the preamble, a missing root is a
#     loud, precise, gate-named failure — never a null-Path binding
#     error, and helper imports can never depend on one custom variable
#     alone.
#   - The generated script carries an EXIT-CODE EPILOGUE: pwsh -File
#     exits 0 when a script ends normally without `exit`, so a failing
#     native command at script end would otherwise be recorded as a
#     passing gate. (Root cause class 3: lost native exit codes.)
#     Gate bodies must still check $LASTEXITCODE after every native
#     command whose result matters; a gate must never `exit 0` across
#     a failed native command (the gofmt-verify early-exit class).
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
        [string]$ToolContext = "-",
        # Opt-in: generate the SHEYTAN_GO_TAGS initialization preamble.
        # Only tag-consuming Go gates may set this (see header notes).
        [switch]$GoTags,
        # Opt-in: generate the repository-root resolution preamble (run
        # 42921967187 repair). Required for every gate whose body dot-
        # sources another repository script through $repoRoot.
        [switch]$RequireRepoRoot,
        # Repository-relative helper scripts the gate body will import;
        # each is validated to exist before the gate body executes.
        [string[]]$RepoRootHelpers = @()
    )
    if (-not $env:CI_LOG_FILE) {
        throw "Capture-Step: `$env:CI_LOG_FILE must be set before the first call."
    }
    $workdir = (Get-Location).Path
    $startedDate = Get-Date
    $started = $startedDate.ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")

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
    # Deterministic StrictMode version (never 'Latest': its meaning
    # changes as PowerShell evolves, so the same generated gate could
    # start failing on a future runner image with no repository change).
    $strictHeader = @'
Set-StrictMode -Version 3.0
$ErrorActionPreference = "Stop"
$PSNativeCommandUseErrorActionPreference = $false
'@
    # Single-quoted epilogue: it must reach the generated file verbatim.
    $epilogue = 'if (Test-Path Variable:LASTEXITCODE) { exit $LASTEXITCODE } else { exit 0 }'

    # Repository-root preamble (run 42921967187 repair): generated only
    # for gates that declare -RequireRepoRoot. The bootstrap below is the
    # ONLY code allowed to locate scripts/ci/Resolve-RepoRoot.ps1 from
    # nothing; after it loads, the canonical resolver owns the full
    # validation contract (identity markers, required helpers, precise
    # diagnostics). Placeholders: __GATE_NAME__ and __HELPERS__.
    $repoRootPreamble = ""
    if ($RequireRepoRoot -or $RepoRootHelpers.Count -gt 0) {
        $helpersLiteral = "@()"
        if ($RepoRootHelpers.Count -gt 0) {
            $quoted = @($RepoRootHelpers | ForEach-Object { "'" + ($_ -replace "'", "''") + "'" })
            $helpersLiteral = "@(" + ($quoted -join ", ") + ")"
        }
        $gateNameLiteral = $Step -replace "'", "''"
        $repoRootPreamble = @'

# ---- repository-root resolution preamble (run 42921967187 repair) ----
# The gate body below consumes $repoRoot. Resolution happens HERE, before
# any package output is accessed, from the environment only (never the
# current working directory: gate scripts run under ci-logs/step-scripts,
# which is not the helper root).
$__repoRootResolved = $null
foreach ($__candidate in @($env:SHEYTAN_REPO_ROOT, $env:GITHUB_WORKSPACE)) {
    if ([string]::IsNullOrWhiteSpace($__candidate)) { continue }
    try { $__full = [System.IO.Path]::GetFullPath($__candidate.Trim()) } catch { continue }
    $__resolver = Join-Path $__full 'scripts/ci/Resolve-RepoRoot.ps1'
    if (-not (Test-Path -LiteralPath $__resolver -PathType Leaf)) { continue }
    $__repoRootResolved = $__full
    break
}
if ($null -eq $__repoRootResolved) {
    Write-Output "ERROR: gate '__GATE_NAME__' could not bootstrap the repository root."
    Write-Output "  candidates consulted: SHEYTAN_REPO_ROOT, then GITHUB_WORKSPACE (current working directory is never used)"
    Write-Output "  SHEYTAN_REPO_ROOT present: $([bool]($env:SHEYTAN_REPO_ROOT))  GITHUB_WORKSPACE present: $([bool]($env:GITHUB_WORKSPACE))"
    Write-Output "  expected marker under the root: scripts/ci/Resolve-RepoRoot.ps1"
    Write-Output "  remedy: set SHEYTAN_REPO_ROOT (job env: `$env:SHEYTAN_REPO_ROOT = `$env:GITHUB_WORKSPACE) or the standard GITHUB_WORKSPACE to the checkout root"
    exit 1
}
. (Join-Path $__repoRootResolved 'scripts/ci/Resolve-RepoRoot.ps1')
$repoRoot = Resolve-SheytanRepoRoot -GateName '__GATE_NAME__' -RequiredRelativePaths __HELPERS__
Write-Output "repo root resolved: $repoRoot"
'@
        $repoRootPreamble = $repoRootPreamble.Replace("__GATE_NAME__", $gateNameLiteral).Replace("__HELPERS__", $helpersLiteral)
    }
    $assembledBody = $Command.ToString().Trim()
    if ($repoRootPreamble -ne "") {
        $assembledBody = $repoRootPreamble + "`n" + $assembledBody
    }
    if ($GoTags) {
        $goTagsPreamble = @'

# Initialize this gate's OWN local Go tag argument array from the
# documented environment source (a child pwsh process never inherits
# the caller's PowerShell variables). ONLY tag-consuming Go gates are
# generated with this preamble; generic gates must not depend on a
# build-tag variable (run 42231355353 gate-isolation repair).
if ([string]::IsNullOrWhiteSpace($env:SHEYTAN_GO_TAGS)) {
    Write-Output "ERROR: SHEYTAN_GO_TAGS is not set; each Go gate must initialize its own Go tag array from this documented source (the goffi_static build tag is required when Fyne and wgpu link together)."
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
        Set-Content -Path $scriptFile -Value ($strictHeader + "`n" + $goTagsPreamble + "`n" + $assembledBody + "`n" + $epilogue + "`n") -Encoding utf8NoBOM
    }
    else {
        Set-Content -Path $scriptFile -Value ($strictHeader + "`n" + $assembledBody + "`n" + $epilogue + "`n") -Encoding utf8NoBOM
    }

    $rc = $null
    $gateExecuted = $false
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
        $gateExecuted = $true
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

    # Machine-readable per-gate record (run-42921967187 diagnostics
    # contract): every gate invocation writes ONE
    # <log-dir>/gate-results/<gate>.result.json with the command, working
    # directory, UTC window, exit status and verdict — parseable without
    # text scraping, and honest about whether the gate body actually
    # executed. A record-write failure is evidence-only: it can never
    # mask the gate's own exit status.
    try {
        $gateResultsDir = Join-Path (Split-Path -Parent $env:CI_LOG_FILE) "gate-results"
        New-Item -ItemType Directory -Force -Path $gateResultsDir | Out-Null
        $resultFile = Join-Path $gateResultsDir ("{0}.result.json" -f $slug)
        [ordered]@{
            job         = $Job
            gate        = $Step
            tool_context = $ToolContext
            command     = $Command.ToString().Trim()
            workdir     = $workdir
            log_file    = $env:CI_LOG_FILE
            started_utc = $started
            ended_utc   = $ended
            elapsed_ms  = [int64]((Get-Date) - $startedDate).TotalMilliseconds
            run_id      = $(if ($env:GITHUB_RUN_ID) { $env:GITHUB_RUN_ID } else { 'local' })
            attempt     = $(if ($env:GITHUB_RUN_ATTEMPT) { $env:GITHUB_RUN_ATTEMPT } else { '1' })
            commit      = $(if ($env:SHEYTAN_BUILD_COMMIT) { $env:SHEYTAN_BUILD_COMMIT } elseif ($env:GITHUB_SHA) { $env:GITHUB_SHA } else { 'unknown' })
            platform    = $(if ($env:RUNNER_OS) { $env:RUNNER_OS } else { 'windows' })
            exit_code   = $rc
            verdict     = $verdict
            executed    = $gateExecuted
        } | ConvertTo-Json | Set-Content -Path $resultFile -Encoding utf8NoBOM
    }
    catch {
        Add-Content -Path $env:CI_LOG_FILE -Value ("WARN: gate result record write failed for '{0}': {1}" -f $Step, $_.Exception.Message)
    }

    $script:LastCaptureExit = $rc
    return $rc
}
