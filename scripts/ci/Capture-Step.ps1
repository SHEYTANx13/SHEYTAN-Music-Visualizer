# Capture-Step.ps1 — first-byte command capture for SHEYTAN Music Visualizer
# build and CI logs on Windows. Mirrors scripts/ci/capture.sh. Adapted from
# the proven SHEYTAN-LA contract with two hardening changes:
#   - a script-level exception can never be reported as PASS even when
#     $LASTEXITCODE is 0 or unset;
#   - output is written UTF-8 without BOM (comparisons/concatenation safe).
#
# Usage:
#   ./scripts/ci/Capture-Step.ps1 -Job "windows" -Step "build" `
#       -Log "ci-logs/windows/build.log" -Command { go build ./... }
#
# Optional environment: CAPTURE_INDEX — one line is appended per captured
# command: JOB|STEP|log|EXIT|VERDICT|STARTED|ENDED
#
# Contract:
#   - stdout + stderr captured from the first byte into the log.
#   - $LASTEXITCODE saved IMMEDIATELY after the native command exits; it is
#     not overwritten by Tee-Object, formatting or another command.
#   - Structured STARTED / EXIT_CODE / ENDED records with the command text.
#   - The script throws on FAIL so the step terminates; callers that must
#     continue (independent diagnostic gates) catch, record, and return a
#     non-zero aggregate themselves. Never swallow with `|| $true` semantics.
param(
  [Parameter(Mandatory = $true)][string]$Job,
  [Parameter(Mandatory = $true)][string]$Step,
  [Parameter(Mandatory = $true)][string]$Log,
  [Parameter(Mandatory = $true)][scriptblock]$Command
)

$ErrorActionPreference = 'Continue'
$utf8NoBom = New-Object System.Text.UTF8Encoding($false)

$dir = Split-Path -Parent $Log
if ($dir) { New-Item -ItemType Directory -Force -Path $dir | Out-Null }

$startUtc = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
$cmdText = $Command.ToString().Trim()

@(
  '=== COMMAND RECORD ==='
  "JOB: $Job"
  "STEP: $Step"
  "STARTED: $startUtc"
  "WORKDIR: $PWD"
  "COMMAND: $cmdText"
) | ForEach-Object { [IO.File]::AppendAllText($Log, $_ + [Environment]::NewLine, $utf8NoBom) }

$threw = $false
try {
  & $Command 2>&1 | ForEach-Object { "$_" } | ForEach-Object {
    [IO.File]::AppendAllText($Log, $_ + [Environment]::NewLine, $utf8NoBom)
  }
} catch {
  $threw = $true
  @(
    "ERROR: $($_.Exception.Message)"
    "ERRORTYPE: $($_.Exception.GetType().FullName)"
    "TARGET: $($_.TargetObject)"
  ) | ForEach-Object { [IO.File]::AppendAllText($Log, $_ + [Environment]::NewLine, $utf8NoBom) }
}

# Save the exit status of the last native command IMMEDIATELY.
$code = 0
if (Get-Variable -Name LASTEXITCODE -ErrorAction SilentlyContinue -ValueOnly) {
  $code = $LASTEXITCODE
}
if ($threw -and $code -eq 0) { $code = 1 }  # an exception path is never PASS

$endUtc = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
$verdict = 'FAIL'
if (($code -eq 0) -and (-not $threw)) { $verdict = 'PASS' }

@(
  "EXIT_CODE: $code $verdict"
  "ENDED: $endUtc"
  '=== END COMMAND RECORD ==='
) | ForEach-Object { [IO.File]::AppendAllText($Log, $_ + [Environment]::NewLine, $utf8NoBom) }

if ($env:CAPTURE_INDEX) {
  [IO.File]::AppendAllText($env:CAPTURE_INDEX, "$Job|$Step|$Log|$code|$verdict|$startUtc|$endUtc" + [Environment]::NewLine, $utf8NoBom)
}

if ($code -ne 0) {
  throw "step '$Step' failed with exit code $code (see $Log)"
}
exit $code
