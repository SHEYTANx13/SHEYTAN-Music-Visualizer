# Bootstrap-Logs.ps1 — guaranteed CI log-directory bootstrap (PowerShell
# side, for the Windows runner). Mirror of scripts/ci/bootstrap-logs.sh.
#
# Contract (scripts/ci logging contract, agent.md §6):
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

$logDir = Join-Path (Get-Location).Path "ci-logs"
New-Item -ItemType Directory -Force -Path $logDir | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $logDir "steps") | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $logDir "app-logs") | Out-Null
New-Item -ItemType Directory -Force -Path (Join-Path $logDir "manifests") | Out-Null

$bootstrapLog = Join-Path $logDir "bootstrap.log"
$now = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")

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
    Set-Content -Path $bootstrapLog -Value $lines -Encoding utf8NoBOM
}
else {
    Add-Content -Path $bootstrapLog -Value ("log bootstrap: re-run (directories ensured; existing evidence preserved) " + $now) -Encoding utf8NoBOM
}

Get-Content $bootstrapLog
