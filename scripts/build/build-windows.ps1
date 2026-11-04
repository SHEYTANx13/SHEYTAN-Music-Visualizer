# build-windows.ps1 — repeatable local/CI build gates for SHEYTAN Music
# Visualizer on Windows x64 (initial target platform).
#
# Run this from PowerShell with a UCRT64 MinGW-w64 toolchain available
# (MSYS2 UCRT64 — NOT deprecated MINGW64). On GitHub runners, install the
# toolchain with msys2/setup-msys2 (msystem: UCRT64) and pass the ucrt64\bin
# directory via -Msys2Bin.
#
# Gates (each captured from the first byte, original exit codes preserved):
#   1. gofmt verification
#   2. go mod tidy (+ no drift vs git, when git exists)
#   3. go test ./...
#   4. go vet ./...
#   5. go build ./...            (all packages, CGO/Fyne)
#   6. native app executable     (dist\sheytan-music-visualizer-windows-x64.exe)
#   7. --version runtime check
#   8. --self-check runtime check (no window opened)
#
# Independent gates keep running after a failure; dependent gates are marked
# SKIPPED (never silently passed). The script exits non-zero if any required
# gate failed or was skipped-as-dependent.
#
# Output: per-step logs under ci-logs\windows\ plus index.txt summary.
# No environment dumps (secret hygiene).
[CmdletBinding()]
param(
  # Path of the MSYS2 UCRT64 bin directory (e.g. <msys2-root>\ucrt64\bin).
  [string]$Msys2Bin = ""
)

$ErrorActionPreference = 'Continue'
$utf8NoBom = New-Object System.Text.UTF8Encoding($false)

$Root = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
Set-Location $Root

$LogDir = Join-Path $Root 'ci-logs\windows'
$Index = Join-Path $LogDir 'index.txt'
New-Item -ItemType Directory -Force -Path $LogDir | Out-Null
Set-Content -Path $Index -Value '' -Encoding utf8NoBom
$env:CAPTURE_INDEX = $Index

$Capture = Join-Path $Root 'scripts\ci\Capture-Step.ps1'
$script:Failures = 0
$script:Skipped = 0

function Note([string]$msg) {
  [IO.File]::AppendAllText((Join-Path $LogDir 'summary-notes.log'), $msg + [Environment]::NewLine, $utf8NoBom)
  Write-Host $msg
}

function Run-Gate([string]$step, [string]$logName, [scriptblock]$cmd) {
  $log = Join-Path $LogDir $logName
  try {
    & $Capture -Job 'windows' -Step $step -Log $log -Command $cmd
    return 0
  } catch {
    Write-Host "gate '$step' failed: $($_.Exception.Message)"
    return 1
  }
}

Note "=== SHEYTAN Music Visualizer — Windows build gates ==="
Note "started (UTC): $((Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ'))"
Note "host: $([Environment]::OSVersion.VersionString) / $(@{64='x64';32='x86'}[[Environment]::Is64BitOperatingSystem])"

# --- toolchain ---
if ($Msys2Bin -ne "") {
  if (Test-Path $Msys2Bin) {
    $env:PATH = "$Msys2Bin;$env:PATH"
    Note "MSYS2 UCRT64 bin prepended: $Msys2Bin"
  } else {
    Note "ERROR: -Msys2Bin path not found: $Msys2Bin"
    exit 2
  }
}

$toolchain = @()
$toolchain += "go: $(go version 2>&1)"
$gccCmd = Get-Command gcc -ErrorAction SilentlyContinue
if ($gccCmd) {
  $toolchain += "gcc: $( (& gcc --version)  | Select-Object -First 1 )  [$($gccCmd.Source)]"
} else {
  $toolchain += "gcc: NOT FOUND (Fyne requires CGO with a UCRT64 MinGW-w64 gcc)"
}
$pkgconfCmd = Get-Command pkg-config -ErrorAction SilentlyContinue
if ($pkgconfCmd) { $toolchain += "pkg-config: $( (& pkg-config --version) 2>&1 )  [$($pkgconfCmd.Source)]" }
else { $toolchain += "pkg-config: not on PATH (informational: Fyne on Windows does not require it)" }
$toolchain += "go env: GOOS=$(go env GOOS) GOARCH=$(go env GOARCH) CGO_ENABLED=$(go env CGO_ENABLED) CC=$(go env CC)"
$toolchain | Set-Content -Path (Join-Path $LogDir 'toolchain.log') -Encoding utf8NoBom
$toolchain | ForEach-Object { Write-Host $_ }
if (-not $gccCmd) { Note "FATAL: gcc not available"; exit 2 }
# Verify the compiler is the UCRT64 one, never the deprecated MINGW64.
if ($gccCmd.Source -match 'mingw64' -and $gccCmd.Source -notmatch 'ucrt64') {
  Note "FATAL: selected gcc is from deprecated MINGW64, not UCRT64: $($gccCmd.Source)"
  exit 2
}

# --- 1. gofmt ---
if ((Run-Gate 'gofmt' '01-gofmt.log' { $u = (& gofmt -l .) -join "`n"; if ($u) { Write-Output $u; exit 1 } }) -ne 0) { $script:Failures++ }

# --- 2. tidy (+ drift) ---
if ((Run-Gate 'tidy' '02-tidy.log' { go mod tidy }) -ne 0) { $script:Failures++ }
if (Get-Command git -ErrorAction SilentlyContinue) {
  $insideGit = (& git rev-parse --is-inside-work-tree 2>$null)
  if ("$insideGit".Trim() -eq 'true') {
    if ((Run-Gate 'tidy-drift' '02-tidy-drift.log' { git diff --exit-code -- go.mod go.sum }) -ne 0) { $script:Failures++ }
  } else {
    Note "SKIPPED tidy-drift: no git worktree (extracted archive run)"
  }
}

# --- 3. test / 4. vet / 5. build packages ---
if ((Run-Gate 'tests' '03-test.log' { go test ./... }) -ne 0) { $script:Failures++ }
if ((Run-Gate 'vet' '04-vet.log' { go vet ./... }) -ne 0) { $script:Failures++ }
$pkgBuildOk = $true
if ((Run-Gate 'build-packages' '05-build-all.log' { go build ./... }) -ne 0) { $script:Failures++; $pkgBuildOk = $false }

# --- 6-8. executable + runtime checks ---
$Exe = Join-Path $Root 'dist\sheytan-music-visualizer-windows-x64.exe'
if (-not $pkgBuildOk) {
  Note "SKIPPED native-executable: package build failed; dependent gates cannot run"
  $script:Skipped += 3
  Add-Content -Path $Index -Value "windows|native-executable|SKIPPED|N/A|SKIPPED||" -Encoding utf8NoBom
} else {
  if ((Run-Gate 'native-executable' '06-build-exe.log' { go build -o $Exe ./cmd/sheytan-music-visualizer }) -ne 0) {
    $script:Failures++
    Note "SKIPPED version+self-check: executable build failed"
    $script:Skipped += 2
    Add-Content -Path $Index -Value "windows|version+self-check|SKIPPED|N/A|SKIPPED||" -Encoding utf8NoBom
  } else {
    if ((Run-Gate 'version' '07-version.log' { & $Exe --version }) -ne 0) { $script:Failures++ }
    if ((Run-Gate 'self-check' '08-self-check.log' { & $Exe --self-check }) -ne 0) { $script:Failures++ }
  }
}

# --- summary ---
$ended = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
$summary = @()
$summary += "=== RUN SUMMARY — Windows build gates ==="
$summary += "product: SHEYTAN Music Visualizer (SHEYTAN™)"
$summary += "ended (UTC): $ended"
$summary += ""
$summary += "Per-command records (from index.txt):"
Get-Content $Index | Where-Object { $_ } | ForEach-Object {
  $p = $_ -split '\|'
  $summary += ("  {0,-18} exit={1,-4} {2,-4} log={3}" -f $p[1], $p[3], $p[4], $p[2])
}
if ($script:Skipped -gt 0) {
  $summary += "Skipped-as-dependent gates: $($script:Skipped) (never counted as passing)"
}
$summary += ""
if ($script:Failures -gt 0) {
  $summary += "FAILURE INDEX — $($script:Failures) failed gate(s):"
  Get-Content $Index | Where-Object { $_ -match '\|FAIL\|' } | ForEach-Object {
    $p = $_ -split '\|'
    $summary += ("  {0,-18} exit={1} log={2}" -f $p[1], $p[3], $p[2])
  }
} else {
  $summary += "FAILURE INDEX: (none)"
}
$summaryPath = Join-Path $LogDir 'summary.log'
$summary | Set-Content -Path $summaryPath -Encoding utf8NoBom
$summary | ForEach-Object { Write-Host $_ }

exit $(if ($script:Failures -gt 0) { 1 } else { 0 })
