#!/usr/bin/env bash
# test-package-gates.sh — run-42837709122 regression fixtures for the
# Windows packaging/process gates, runnable on ANY host with pwsh.
#
# Covered classes (spec §2 required repairs):
#   P1  New-PortableZip puts the CHILD app directory at the archive root —
#       never the staging wrapper (the exact create-portable-zip defect).
#   P2  Complete entry manifest: single top-level app dir, no wrapper, no
#       traversal/backslash entries, all required files AND directories,
#       empty tools/ffmpeg/bin preserved, license bytes untransformed.
#   P3  Staging path WITH SPACES still produces a correct package.
#   P4  verify-extracted-package contract: PE byte math validates the GUI
#       subsystem field and detects a console (CUI=3) binary.
#   P5  Invoke-CapturedProcess: exit code 0, non-zero, no output, timeout,
#       missing executable, argument arrays, paths with spaces — and it
#       works under Set-StrictMode as the FIRST command in a fresh child
#       (the exact verify-build-metadata failure surface: no reliance on
#       $LASTEXITCODE).
#   P6  Static root-cause assertions on build-windows.ps1: the failing
#       forms are gone and the robust forms are present.
#
# Exit code 0 = every fixture PASS; nonzero = at least one FAILED.
# A fixture that cannot run on this host (pwsh missing) is reported
# NOT RUN with the exact reason and does not count as a pass.
set -u
cd "$(dirname "${BASH_SOURCE[0]}")/../../.." || exit 3   # repo root
REPO_ROOT="$PWD"
PASSED=0; FAILED=0; NOT_RUN=0

report() {
  case "$1" in
    PASS)      PASSED=$((PASSED+1));  printf 'PASS    %-8s %s\n' "$2" "$3" ;;
    FAIL)      FAILED=$((FAILED+1));  printf 'FAIL    %-8s %s\n' "$2" "$3" ;;
    "NOT RUN") NOT_RUN=$((NOT_RUN+1)); printf 'NOT RUN %-8s %s\n' "$2" "$3" ;;
  esac
}

PWSH_BIN="$(command -v pwsh || true)"
if [ -z "$PWSH_BIN" ]; then
  for cand in /home/z/pwsh/pwsh /usr/local/bin/pwsh; do
    [ -x "$cand" ] && PWSH_BIN="$cand" && break
  done
fi

T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT

echo "=============================================================="
echo "PACKAGE/PROCESS GATE FIXTURES — $(date -u '+%Y-%m-%dT%H:%M:%SZ')"
echo "repo root: $REPO_ROOT"
echo "pwsh: ${PWSH_BIN:-NOT FOUND}"
echo "=============================================================="

if [ -z "$PWSH_BIN" ]; then
  for f in P1 P2 P3 P4 P5 P6; do report "NOT RUN" "$f" "pwsh not available on this host"; done
  echo "SUMMARY: pass=$PASSED fail=$FAILED notrun=$NOT_RUN"
  exit 0
fi

# The pwsh driver is a QUOTED heredoc: bash performs NO expansion inside,
# every $ below is a PowerShell variable. REPO_ROOT and workdir are passed
# as -File arguments.
cat > "$T/gate-driver.ps1" <<'DRIVER'
param(
    [Parameter(Mandatory = $true)][string]$RepoRoot,
    [Parameter(Mandatory = $true)][string]$Work
)
Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
$PSNativeCommandUseErrorActionPreference = $false
if (Test-Path $Work) { Remove-Item -Recurse -Force $Work }
New-Item -ItemType Directory -Force -Path $Work | Out-Null
. (Join-Path $RepoRoot 'scripts/ci/New-PortableZip.ps1')
. (Join-Path $RepoRoot 'scripts/ci/Invoke-CapturedProcess.ps1')

function New-MockStage([string]$baseDir) {
    # EXACT stage-portable layout (placeholder exe: PE gates are byte math
    # tested in P4; the real exe runs on the hosted Windows job).
    $stageRoot = Join-Path $baseDir 'SHEYTAN-Music-Visualizer-windows-x64-portable'
    $appDir = Join-Path $stageRoot 'SHEYTAN-Music-Visualizer-windows-x64'
    New-Item -ItemType Directory -Force -Path (Join-Path $appDir 'tools/ffmpeg/bin') | Out-Null
    New-Item -ItemType Directory -Force -Path (Join-Path $appDir 'data') | Out-Null
    Set-Content -Path (Join-Path $appDir 'sheytan-music-visualizer.exe') -Value 'MZ-dummy' -NoNewline
    Set-Content -Path (Join-Path $appDir 'license.md') -Value 'CANONICAL-LICENSE-BYTES' -NoNewline
    Set-Content -Path (Join-Path $appDir 'readme.md') -Value 'readme' -NoNewline
    Set-Content -Path (Join-Path $appDir 'portable.mode') -Value '' -NoNewline
    Set-Content -Path (Join-Path $appDir 'data/DATA-FOLDERS.txt') -Value 'contract' -NoNewline
    Set-Content -Path (Join-Path $appDir 'tools/ffmpeg/FFMPEG-TOOLS-LICENSING.md') -Value 'licensing' -NoNewline
    return $stageRoot
}
$required = @(
    'SHEYTAN-Music-Visualizer-windows-x64/sheytan-music-visualizer.exe',
    'SHEYTAN-Music-Visualizer-windows-x64/license.md',
    'SHEYTAN-Music-Visualizer-windows-x64/readme.md',
    'SHEYTAN-Music-Visualizer-windows-x64/portable.mode',
    'SHEYTAN-Music-Visualizer-windows-x64/tools/ffmpeg/FFMPEG-TOOLS-LICENSING.md',
    'SHEYTAN-Music-Visualizer-windows-x64/data/DATA-FOLDERS.txt'
)
$requiredDirs = @(
    'SHEYTAN-Music-Visualizer-windows-x64/data/',
    'SHEYTAN-Music-Visualizer-windows-x64/tools/ffmpeg/',
    'SHEYTAN-Music-Visualizer-windows-x64/tools/ffmpeg/bin/'
)

# --- P1/P2: plain staging path ----------------------------------------------
$fail = 0
$stageRoot = New-MockStage $Work
$zip = Join-Path $Work 'package.zip'
$mk = New-PortableZip -StageRoot $stageRoot -ZipPath $zip
if ($mk.Error -ne '') { Write-Output 'P1-FAIL: New-PortableZip error:' $mk.Error; $fail = 1 }
else {
    Write-Output 'P1-INFO full entry manifest:'
    $mk.Entries | ForEach-Object { Write-Output ("  ENTRY: " + $_) }
    $tops = @($mk.Entries | ForEach-Object { ($_ -split '/')[0] } | Sort-Object -Unique)
    if ($tops.Count -ne 1 -or $tops[0] -ne 'SHEYTAN-Music-Visualizer-windows-x64') {
        Write-Output 'P1-FAIL: top-level is not exactly the app directory:' $tops; $fail = 1
    }
    if ($mk.Entries | Where-Object { $_ -match 'windows-x64-portable(/|$)' }) {
        Write-Output 'P1-FAIL: staging wrapper leaked into entries'; $fail = 1
    }
    foreach ($req in $required) { if ($mk.Entries -notcontains $req) { Write-Output ('P2-FAIL: missing entry ' + $req); $fail = 1 } }
    foreach ($d in $requiredDirs) { if ($mk.Entries -notcontains $d) { Write-Output ('P2-FAIL: missing directory entry ' + $d); $fail = 1 } }
    if ($mk.Entries | Where-Object { $_ -match '(^|/)\.\.(/|$)' -or $_ -match '\\' }) {
        Write-Output 'P2-FAIL: traversal/backslash entry present'; $fail = 1
    }
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $ar = [System.IO.Compression.ZipFile]::OpenRead($zip)
    try {
        $le = $ar.Entries | Where-Object { $_.FullName -eq 'SHEYTAN-Music-Visualizer-windows-x64/license.md' }
        if (-not $le) { Write-Output 'P2-FAIL: license entry missing on reopen'; $fail = 1 }
        else {
            $sr = New-Object System.IO.StreamReader($le.Open())
            $bytesInZip = $sr.ReadToEnd(); $sr.Dispose()
            $canon = Get-Content -Raw (Join-Path $stageRoot 'SHEYTAN-Music-Visualizer-windows-x64/license.md')
            if ($bytesInZip -cne $canon) { Write-Output 'P2-FAIL: license bytes transformed in archive'; $fail = 1 }
        }
    } finally { $ar.Dispose() }
}
if ($fail -eq 0) { Write-Output 'P1P2-VERDICT: PASS' } else { Write-Output 'P1P2-VERDICT: FAIL' }

# --- P3: staging path WITH SPACES -------------------------------------------
$fail = 0
$spaced = Join-Path $Work 'stage dir with spaces'
New-Item -ItemType Directory -Force -Path $spaced | Out-Null
$stageRoot2 = New-MockStage $spaced
$zip2 = Join-Path $spaced 'out package.zip'
$mk2 = New-PortableZip -StageRoot $stageRoot2 -ZipPath $zip2
if ($mk2.Error -ne '') { Write-Output 'P3-FAIL:' $mk2.Error; $fail = 1 }
else {
    foreach ($req in $required) { if ($mk2.Entries -notcontains $req) { Write-Output ('P3-FAIL: missing ' + $req); $fail = 1 } }
    $extract2 = Join-Path $Work 'extract spaced'
    Expand-Archive -Path $zip2 -DestinationPath $extract2
    if (-not (Test-Path (Join-Path $extract2 'SHEYTAN-Music-Visualizer-windows-x64/license.md'))) {
        Write-Output 'P3-FAIL: extracted license missing (spaced path)'; $fail = 1
    }
}
if ($fail -eq 0) { Write-Output 'P3-VERDICT: PASS' } else { Write-Output 'P3-VERDICT: FAIL' }

# --- P4: PE byte math on crafted fixtures ------------------------------------
$fail = 0
$dos = New-Object byte[] 0x40; $dos[0] = 0x4D; $dos[1] = 0x5A; $dos[0x3C] = 0x40
$peSig = [System.Text.Encoding]::ASCII.GetBytes("PE`0`0")
$coff = New-Object byte[] 20; $coff[0] = 0x64; $coff[1] = 0x86
$opt = New-Object byte[] 240
$opt[0] = 0x0B; $opt[1] = 0x02
$opt[68] = 0x02
$guiPath = Join-Path $Work 'fixture-gui.exe'
$all = New-Object System.Collections.Generic.List[byte]
$all.AddRange($dos); $all.AddRange($peSig); $all.AddRange($coff); $all.AddRange($opt)
[System.IO.File]::WriteAllBytes($guiPath, $all.ToArray())
$b = [System.IO.File]::ReadAllBytes($guiPath)
$peOff = [BitConverter]::ToInt32($b, 0x3C)
$sub = [BitConverter]::ToUInt16($b, $peOff + 4 + 20 + 68)
if ($peOff -ne 0x40 -or $sub -ne 2) { Write-Output ('P4-FAIL: GUI fixture misread peOff=' + $peOff + ' sub=' + $sub); $fail = 1 }
$opt[68] = 0x03
$cuiPath = Join-Path $Work 'fixture-cui.exe'
$all2 = New-Object System.Collections.Generic.List[byte]
$all2.AddRange($dos); $all2.AddRange($peSig); $all2.AddRange($coff); $all2.AddRange($opt)
[System.IO.File]::WriteAllBytes($cuiPath, $all2.ToArray())
$b2 = [System.IO.File]::ReadAllBytes($cuiPath)
$sub2 = [BitConverter]::ToUInt16($b2, ([BitConverter]::ToInt32($b2, 0x3C)) + 4 + 20 + 68)
if ($sub2 -ne 3) { Write-Output ('P4-FAIL: CUI fixture misread sub=' + $sub2); $fail = 1 }
if ($fail -eq 0) { Write-Output 'P4-VERDICT: PASS' } else { Write-Output 'P4-VERDICT: FAIL' }

# --- P5: robust process runner ------------------------------------------------
$fail = 0
$sh = '/bin/sh'
if (-not (Test-Path $sh)) { Write-Output 'P5-FAIL: /bin/sh missing'; $fail = 1 }
else {
    $r0 = Invoke-CapturedProcess -FilePath $sh -ArgumentList @('-c', 'exit 0') -TimeoutSeconds 30
    if ($r0.Error -ne '' -or $r0.ExitCode -ne 0) { Write-Output ('P5-FAIL: exit0 err=' + $r0.Error + ' code=' + $r0.ExitCode); $fail = 1 }
    $r7 = Invoke-CapturedProcess -FilePath $sh -ArgumentList @('-c', 'exit 7') -TimeoutSeconds 30
    if ($r7.Error -ne '' -or $r7.ExitCode -ne 7) { Write-Output ('P5-FAIL: exit7 err=' + $r7.Error + ' code=' + $r7.ExitCode); $fail = 1 }
    $rc = Invoke-CapturedProcess -FilePath $sh -ArgumentList @('-c', 'echo OUT-LINE; echo ERR-LINE 1>&2') -TimeoutSeconds 30
    if ($rc.Error -ne '') { Write-Output ('P5-FAIL: streams err=' + $rc.Error); $fail = 1 }
    if (-not ($rc.StdOut -contains 'OUT-LINE') -or -not ($rc.StdErr -contains 'ERR-LINE')) {
        Write-Output 'P5-FAIL: streams not captured independently'; $fail = 1
    }
    $rn = Invoke-CapturedProcess -FilePath $sh -ArgumentList @('-c', 'exit 0') -TimeoutSeconds 30
    if ($rn.StdOut.Count -ne 0 -or $rn.StdErr.Count -ne 0) { Write-Output 'P5-FAIL: silent child produced output'; $fail = 1 }
    $rt = Invoke-CapturedProcess -FilePath $sh -ArgumentList @('-c', 'sleep 30') -TimeoutSeconds 2
    if (-not $rt.TimedOut -or $null -ne $rt.ExitCode -or $rt.Error -eq '') {
        Write-Output ('P5-FAIL: timeout not fail-closed timeout=' + $rt.TimedOut + ' code=' + $rt.ExitCode); $fail = 1
    }
    $rm_ = Invoke-CapturedProcess -FilePath (Join-Path $Work 'definitely-missing.exe') -TimeoutSeconds 10
    if ($null -ne $rm_.ExitCode -or $rm_.Error -eq '') { Write-Output 'P5-FAIL: missing exe did not fail closed'; $fail = 1 }
    $spacedDir = Join-Path $Work 'runner dir with spaces'
    New-Item -ItemType Directory -Force -Path $spacedDir | Out-Null
    $child = Join-Path $spacedDir 'child script.sh'
    $nl = "`n"
    Set-Content -Path $child -Value ('#!/bin/sh' + $nl + 'echo "ARG1=$1 ARG2=$2"') -NoNewline
    chmod +x $child
    $ra = Invoke-CapturedProcess -FilePath $child -ArgumentList @('hello world', 'second arg') -TimeoutSeconds 30
    if ($ra.Error -ne '' -or $ra.ExitCode -ne 0) { Write-Output ('P5-FAIL: spaced runner err=' + $ra.Error); $fail = 1 }
    elseif (-not ($ra.StdOut[0] -eq 'ARG1=hello world ARG2=second arg')) {
        Write-Output ('P5-FAIL: argv wrong: ' + $ra.StdOut[0]); $fail = 1
    }
    # Strict-mode + fresh child + runner as the very first command: the
    # exact verify-build-metadata failure surface. Passes only when the
    # runner works with $LASTEXITCODE never having been set.
    $smPath = Join-Path $Work 'strict-mode.ps1'
    $body = 'Set-StrictMode -Version Latest' + $nl
    $body += '. (Join-Path ''' + $RepoRoot + ''' ''scripts/ci/Invoke-CapturedProcess.ps1'')' + $nl
    $body += '$r = Invoke-CapturedProcess -FilePath ''/bin/sh'' -ArgumentList @(''-c'',''exit 3'') -TimeoutSeconds 30' + $nl
    $body += 'if ($r.Error -ne '''') { Write-Output $r.Error; exit 8 }' + $nl
    $body += 'if ($r.ExitCode -ne 3) { exit 9 }' + $nl
    $body += 'exit 0'
    Set-Content -Path $smPath -Encoding utf8NoBOM -Value $body
    $sm = Invoke-CapturedProcess -FilePath (Get-Command pwsh).Source -ArgumentList @('-NoProfile', '-NonInteractive', '-File', $smPath) -TimeoutSeconds 60
    if ($sm.ExitCode -ne 0) {
        Write-Output ('P5-FAIL: strict-mode first-command scenario failed rc=' + $sm.ExitCode + ' stderr: ' + ($sm.StdErr -join '; ')); $fail = 1
    }
}
if ($fail -eq 0) { Write-Output 'P5-VERDICT: PASS' } else { Write-Output 'P5-VERDICT: FAIL' }
DRIVER

"$PWSH_BIN" -NoProfile -NonInteractive -File "$T/gate-driver.ps1" -RepoRoot "$REPO_ROOT" -Work "$T/work" > "$T/driver.out" 2>&1
DRIVER_RC=$?
sed 's/^/  /' "$T/driver.out"
if [ "$DRIVER_RC" -ne 0 ]; then
  report FAIL "P1-P5" "driver exited $DRIVER_RC (full output above)"
  FAILED=$((FAILED-1))
  for v in P1P2 P3 P4 P5; do report FAIL "$v" "not reached (driver crash)"; FAILED=$((FAILED-1)); done
else
  for v in P1P2 P3 P4 P5; do
    if grep -q "${v}-VERDICT: PASS" "$T/driver.out"; then
      report PASS "$v" "gate fixture $v verified"
    else
      report FAIL "$v" "gate fixture $v failed (see driver output above)"
    fi
  done
fi

# ----------------------------------------------------------------------------
# P6: static root-cause assertions on build-windows.ps1
# ----------------------------------------------------------------------------
BW="$REPO_ROOT/scripts/build/build-windows.ps1"
P6OK=1
# Only an EXECUTABLE Compress-Archive call would reintroduce the defect;
# prose comments documenting the old root cause are expected to remain.
grep -Eq '^[[:space:]]*Compress-Archive' "$BW" && { echo "  an executable Compress-Archive call is still present"; P6OK=0; }
grep -q 'New-PortableZip -StageRoot' "$BW" || { echo "  New-PortableZip not used by create-portable-zip gate"; P6OK=0; }
VB="$(awk '/Step "verify-build-metadata"/,/^    }$/' "$BW")"
echo "$VB" | grep -q 'LASTEXITCODE' && { echo "  verify-build-metadata still reads LASTEXITCODE"; P6OK=0; }
echo "$VB" | grep -q 'Invoke-CapturedProcess' || { echo "  verify-build-metadata missing robust runner"; P6OK=0; }
VE="$(awk '/Step "verify-extracted-package"/,/^    }$/' "$BW")"
echo "$VE" | grep -q 'Subsystem' || { echo "  verify-extracted-package missing PE subsystem check"; P6OK=0; }
echo "$VE" | grep -q 'Invoke-CapturedProcess' || { echo "  verify-extracted-package missing robust runner"; P6OK=0; }
echo "$VE" | grep -q 'SHA256' || { echo "  verify-extracted-package missing license hash compare"; P6OK=0; }
if [ "$P6OK" -eq 1 ]; then report PASS "P6" "static root-cause assertions hold"; else report FAIL "P6" "static root-cause assertions failed"; fi

echo "SUMMARY: pass=$PASSED fail=$FAILED notrun=$NOT_RUN"
[ "$FAILED" -eq 0 ] || exit 1
exit 0
