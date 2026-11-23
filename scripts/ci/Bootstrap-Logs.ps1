# Bootstrap-Logs.ps1 — guaranteed CI log-directory bootstrap (PowerShell
# side, for the Windows runner). Mirror of scripts/ci/bootstrap-logs.sh.
#
# Contract (scripts/ci logging contract, docs/agent.md §6):
#   - The build-windows job runs this immediately after checkout and BEFORE
#     any step that writes into ci-logs/ (toolchain identity gate, gate
#     runner, portable packaging logs).
#   - Creates the whole directory layout up front and a NON-EMPTY
#     bootstrap.log, so the job's if:always() upload has a real file even
#     when a later setup step (MSYS2, setup-go) fails before anything ran.
#   - Records only allowlisted, non-secret GitHub/RUNNER context variables.
#   - Idempotent: re-running never truncates existing evidence.
#   - A new PowerShell process does not inherit caller variables, so every
#     value is initialized locally here (run-41915116613 root cause class).
#   - LF-ONLY line endings (run 44462492262 repair): content lines are
#     joined with "`n" and written through [System.IO.File]::WriteAllText
#     with an explicit UTF8-no-BOM encoding. The former
#     Set-Content/Add-Content writes terminated every line with CRLF on
#     Windows (Environment.NewLine) — exactly the 8 stray control bytes
#     (0x0D) the byte-level ASCII gate counted in ci-logs/bootstrap.log on
#     run 44462492262. [System.IO.File]::WriteAllText/AppendAllText with
#     UTF8Encoding($false) works identically on Windows PowerShell 5.1 and
#     pwsh 7 (and replaces the pwsh-only -Encoding utf8NoBOM parameter).
#   - After writing, the file is routed through the canonical sanitizer
#     chain (CI_SANITIZER_EXE, then the python3 bootstrap fallback, then a
#     loud raw passthrough — scripts/ci/Write-SanitizedChunk.ps1). The
#     sanitizer is a fixed-point no-op on this already-contract-compliant
#     content; routing it proves the plumbing and keeps every ci-logs/
#     producer behind one contract. An unavailable sanitizer never blocks
#     the bootstrap and never masks anything: the file is left as written.

$logDir = Join-Path (Get-Location).Path "ci-logs"
New-Item -ItemType Directory -Force -Path $logDir | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $logDir "steps") | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $logDir "app-logs") | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $logDir "manifests") | Out-Null

$bootstrapLog = Join-Path $logDir "bootstrap.log"
$now = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
$utf8NoBom = New-Object System.Text.UTF8Encoding($false)

if (-not (Test-Path $bootstrapLog) -or (Get-Item $bootstrapLog).Length -eq 0) {
    $job = if ($env:GITHUB_JOB) { $env:GITHUB_JOB } else { "unknown" }
    $runId = if ($env:GITHUB_RUN_ID) { $env:GITHUB_RUN_ID } else { "unknown" }
    $attempt = if ($env:GITHUB_RUN_ATTEMPT) { $env:GITHUB_RUN_ATTEMPT } else { "unknown" }
    $repo = if ($env:GITHUB_REPOSITORY) { $env:GITHUB_REPOSITORY } else { "unknown" }
    $runnerOs = if ($env:RUNNER_OS) { $env:RUNNER_OS } else { "unknown" }
    $runnerName = if ($env:RUNNER_NAME) { $env:RUNNER_NAME } else { "unknown" }
    $lines = @(
        "log bootstrap: PASS",
        "job=$job",
        "run_id=$runId",
        "run_attempt=$attempt",
        "repository=$repo",
        "runner_os=$runnerOs",
        "runner_name=$runnerName",
        "bootstrap_utc=$now"
    )
    # LF-only content, UTF-8 without BOM (never CRLF: see header contract).
    [System.IO.File]::WriteAllText($bootstrapLog, (($lines -join "`n") + "`n"), $utf8NoBom)
}
else {
    $rerun = "log bootstrap: re-run (directories ensured; existing evidence preserved) " + $now
    [System.IO.File]::AppendAllText($bootstrapLog, ($rerun + "`n"), $utf8NoBom)
}

# Sanitizer pass (run 44462492262 repair): route the finished bootstrap
# file through the canonical sanitizer chain. The rewrite happens only
# when the sanitizer actually changed the bytes; a sanitizer failure or
# unavailability leaves the evidence untouched (fail visible, never
# masked — the strict validator is the gate, not this helper).
$sanitizerHelper = Join-Path $PSScriptRoot "Write-SanitizedChunk.ps1"
if (Test-Path -LiteralPath $sanitizerHelper -PathType Leaf) {
    . $sanitizerHelper
    $raw = [System.IO.File]::ReadAllText($bootstrapLog)
    $sanitized = Write-SanitizedChunk -Text $raw
    if ($sanitized -ne $raw) {
        [System.IO.File]::WriteAllText($bootstrapLog, $sanitized, $utf8NoBom)
    }
}

Get-Content $bootstrapLog
