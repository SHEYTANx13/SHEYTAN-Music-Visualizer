# Verify-UCRT64.ps1 — EXECUTED UCRT64 toolchain identity gate.
#
# Run 42231355353 defect this file repairs: the workflow step piped a
# bare `{ ... }` script-block literal into Tee-Object. PowerShell
# treats that as a VALUE, not an invocation: the block's SOURCE TEXT
# was written to ci-logs/windows-compiler.log, no assertion ran, the
# $fail flag stayed $false and the step printed PASSED. The log
# therefore contained script text instead of compiler evidence — a
# false-green verification.
#
# Run 42376176493 defect this file ALSO repairs (header-location false
# negative): the CRT header check looked for `_mingw.h` inside
# `gcc -print-file-name=include` — GCC's PRIVATE internal include
# directory (`<msys2>\ucrt64\lib\gcc\x86_64-w64-mingw32\<version>\include`).
# The mingw-w64 UCRT64 `headers` package installs `_mingw.h` at the
# TARGET header root `<msys2>\ucrt64\include\_mingw.h` (GCC finds it via
# its own target search path, not via `-print-file-name=include`). The
# run's windows-compiler.log proved a working UCRT64 GCC (correct
# executable path, target triple, `_UCRT` after including <stdio.h>,
# UCRT64 pkgconf) yet failed the gate purely on that wrong path. This
# file now resolves the UCRT64 target root from the compiler location,
# checks `_mingw.h` THERE, records GCC's private include dir as a
# clearly-labeled diagnostic, and verifies package ownership through
# the MSYS2 pacman database.
#
# This script is INVOKED as a file (never piped as a script block) and
# performs the real checks. Every native command's exit code is
# captured immediately via $LASTEXITCODE; any failure returns non-zero
# after the full evidence set was recorded. The log contains evaluated
# values only — never PowerShell source text.
#
# Checks (multiple independent evidence sources):
#   1. gcc identity: first resolved gcc.exe must live inside the
#      expected MSYS2 <msys2>\ucrt64\bin directory;
#   2. gcc --version and -dumpmachine (x86_64-w64-mingw32);
#   3. HEADER-AWARE CRT probe: _UCRT is established by the mingw-w64
#      CRT header configuration (_mingw.h, from __MSVCRT_VERSION__),
#      NOT by GCC built-ins, so the probe includes <stdio.h> before
#      scanning the preprocessor macro list. A no-header translation
#      unit cannot establish which CRT the headers select (the
#      run-42231355353 desktop-env-check failure was that defective
#      probe form);
#   4. the installed CRT configuration header _mingw.h must exist at
#      the UCRT64 TARGET header root <msys2>\ucrt64\include\_mingw.h
#      (NOT in GCC's private include directory — run 42376176493);
#   5. package-database verification (pacman -Q): the gcc, headers,
#      crt and pkgconf UCRT64 packages are installed, and pacman -Qo
#      attributes /ucrt64/include/_mingw.h to the headers package;
#   6. compile/link probe with the selected compiler and the UCRT64
#      headers (separate from the product build): exit code, PE MZ
#      header of the probe binary, and on Windows hosts the probe's
#      own execution output;
#   7. pkg-config identity: must be the UCRT64 pkgconf, version >= 1.8;
#   8. Go toolchain identity.
#
# Usage:
#   pwsh ./scripts/ci/Verify-UCRT64.ps1 -ExpectedBin <msys2>\ucrt64\bin
# The caller captures the emitted evidence lines (all evaluated values)
# and tees them into ci-logs/windows-compiler.log — the script itself
# only writes the success stream, and the caller preserves the exit code.
# Exit codes: 0 all checks passed; 1 at least one check failed
# (recorded with the exact evidence).

[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$ExpectedBin
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
$PSNativeCommandUseErrorActionPreference = $false

$fail = $false
function Fail([string]$message) {
    # Record the failure and keep collecting the remaining evidence:
    # independent checks must all run before the script exits non-zero.
    Write-Output "ERROR: $message"
    $script:fail = $true
}

$started = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
Write-Output "=== UCRT64 toolchain identity verification (executed) ==="
Write-Output "started_utc: $started"

# --- Root resolution ---------------------------------------------------------
# ExpectedBin is the workflow contract: <msys2>\ucrt64\bin. The UCRT64
# TARGET root (where the mingw-w64 headers package installs _mingw.h) is
# its parent directory: <msys2>\ucrt64. Trailing separators are trimmed so
# Split-Path yields real parents on both Windows and POSIX hosts.
$ExpectedBinNorm = $ExpectedBin.Trim().TrimEnd('\', '/')
if (-not ($ExpectedBinNorm -match '[\\/](ucrt64)[\\/](bin)$')) {
    Fail "expected UCRT64 bin directory must end in ucrt64/bin (got: $ExpectedBin)"
}
$UcrtRoot = Split-Path -Parent $ExpectedBinNorm   # <msys2>\ucrt64
$MsysRoot = Split-Path -Parent $UcrtRoot          # <msys2>
Write-Output "expected_ucrt64_bin: $ExpectedBin"
Write-Output "ucrt64_target_root: $UcrtRoot"
Write-Output "msys2_root: $MsysRoot"
if (-not (Test-Path (Join-Path $ExpectedBinNorm 'gcc.exe')) -and
    -not (Test-Path (Join-Path $ExpectedBinNorm 'gcc'))) {
    Fail "expected UCRT64 bin directory does not contain gcc: $ExpectedBin"
}

Write-Output "=== gcc identity ==="
$gcc = Get-Command gcc -ErrorAction SilentlyContinue
if ($null -eq $gcc) {
    Fail "gcc not found on PATH"
}
else {
    Write-Output "gcc source: $($gcc.Source)"
    if (-not ($gcc.Source -like "$ExpectedBinNorm*")) {
        Fail "first gcc on PATH is NOT the UCRT64 toolchain (stale shadowing toolchain detected): $($gcc.Source)"
    }
    # $(...) captures the FULL output first; piping a native tool into
    # `Select-Object -First 1` can terminate the producer early.
    $gccVersionOut = (gcc --version)
    $gccVersionRc = $LASTEXITCODE
    if ($gccVersionRc -ne 0) {
        Fail "gcc --version failed with exit code $gccVersionRc"
    }
    else {
        Write-Output "gcc: $($gccVersionOut[0])"
    }
    $dumpmachine = (gcc -dumpmachine)
    $dmRc = $LASTEXITCODE
    if ($dmRc -ne 0) {
        Fail "gcc -dumpmachine failed with exit code $dmRc"
    }
    else {
        Write-Output "gcc -dumpmachine: $dumpmachine"
        if ($dumpmachine -ne 'x86_64-w64-mingw32') {
            Fail "unexpected gcc -dumpmachine (want x86_64-w64-mingw32)"
        }
    }

    Write-Output "=== CRT identity (header-aware _UCRT probe) ==="
    $probeSource = @('#include <stdio.h>', 'int main(void){return 0;}')
    $defines = $probeSource | gcc -E -dM -xc -
    $probeRc = $LASTEXITCODE
    if ($probeRc -ne 0) {
        Fail "header-aware _UCRT probe failed: gcc -E exited with code $probeRc"
    }
    elseif (-not ($defines | Select-String -SimpleMatch '_UCRT')) {
        Fail "CRT headers do not define _UCRT (stale MINGW64/msvcrt toolchain detected)"
    }
    else {
        Write-Output "UCRT runtime confirmed (_UCRT defined after including <stdio.h>)"
    }

    Write-Output "=== CRT header location (run-42376176493 false-negative repair) ==="
    # GCC's PRIVATE internal include directory is a diagnostic only. It is
    # NEVER the mingw-w64 CRT header location and is never searched for
    # _mingw.h; the authoritative location is the UCRT64 target root.
    # Null-safe capture: a failing/empty -print-file-name must produce a
    # structured FAIL record, never a PowerShell crash on a null method call.
    $incDir = ("$(gcc -print-file-name=include)").Trim()
    $incRc = $LASTEXITCODE
    if ($incRc -ne 0 -or [string]::IsNullOrWhiteSpace($incDir)) {
        Fail "gcc -print-file-name=include failed (rc=$incRc)"
    }
    else {
        Write-Output "gcc private internal include dir (diagnostic only — NOT the CRT header location): $incDir"
        if ($incDir -notmatch '[\\/]ucrt64[\\/]') {
            Fail "compiler private include dir is not inside a ucrt64 prefix: $incDir"
        }
        if (Test-Path (Join-Path $incDir "_mingw.h")) {
            Write-Output "note: _mingw.h is also present in the gcc private include dir (atypical install); the target-root check below stays authoritative"
        }
    }
    # Authoritative CRT header check: the UCRT64 TARGET header root.
    $targetHeader = Join-Path $UcrtRoot 'include/_mingw.h'
    if (-not (Test-Path $targetHeader)) {
        Fail "CRT header _mingw.h not found at the UCRT64 target root: $targetHeader (expected owner: mingw-w64-ucrt-x86_64-headers; verify with pacman -Qo /ucrt64/include/_mingw.h)"
    }
    else {
        Write-Output "CRT header _mingw.h present at the UCRT64 target root: $targetHeader"
    }
}

Write-Output "=== UCRT64 package database (pacman) ==="
$pacman = Join-Path $MsysRoot 'usr/bin/pacman.exe'
if (-not (Test-Path $pacman)) {
    Fail "MSYS2 pacman not found at $pacman — the UCRT64 package database cannot be verified"
}
else {
    foreach ($pkg in @(
            'mingw-w64-ucrt-x86_64-gcc',
            'mingw-w64-ucrt-x86_64-headers',
            'mingw-w64-ucrt-x86_64-crt',
            'mingw-w64-ucrt-x86_64-pkgconf'
        )) {
        $pkgOut = (& $pacman -Q $pkg 2>&1) -join '; '
        $pkgRc = $LASTEXITCODE
        if ($pkgRc -ne 0) {
            Fail "pacman -Q $pkg failed (rc=$pkgRc): $pkgOut"
        }
        else {
            Write-Output "pacman: $pkgOut"
        }
    }
    $ownOut = (& $pacman -Qo '/ucrt64/include/_mingw.h' 2>&1) -join '; '
    $ownRc = $LASTEXITCODE
    if ($ownRc -ne 0) {
        Fail "pacman -Qo /ucrt64/include/_mingw.h failed (rc=$ownRc): $ownOut"
    }
    else {
        Write-Output "pacman ownership: $ownOut"
    }
}

Write-Output "=== UCRT64 compile/link probe (separate from the product build) ==="
if ($null -eq $gcc) {
    Fail "compile/link probe skipped: gcc not found"
}
else {
    $probeDir = Join-Path ([System.IO.Path]::GetTempPath()) ("ucrt64-probe-" + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Force -Path $probeDir | Out-Null
    try {
        $probeSrc = Join-Path $probeDir 'probe.c'
        Set-Content -Path $probeSrc -Value "#include <stdio.h>`r`nint main(void){puts(`"ucrt64-probe-ok`"); return 0;}`r`n" -Encoding ascii
        $probeExe = Join-Path $probeDir 'probe.exe'
        $probeCompilerOut = (& gcc -Wall -O0 -o $probeExe $probeSrc 2>&1)
        $probeRc2 = $LASTEXITCODE
        if ($probeRc2 -ne 0) {
            $joined = ($probeCompilerOut | ForEach-Object { "$_" }) -join '; '
            Fail "compile/link probe failed (rc=$probeRc2): $joined"
        }
        else {
            Write-Output "compile/link probe: rc=0"
            if ($probeCompilerOut) {
                $probeCompilerOut | ForEach-Object { Write-Output "probe compiler note: $_" }
            }
            $probeBytes = [System.IO.File]::ReadAllBytes($probeExe)
            if ($probeBytes.Length -ge 2 -and $probeBytes[0] -eq 77 -and $probeBytes[1] -eq 90) {
                Write-Output "probe PE MZ header confirmed ($($probeBytes.Length) bytes)"
            }
            else {
                Fail "probe output is not a PE executable (missing MZ header)"
            }
            # Executing the probe requires a Windows host (the runner's
            # UCRT64 bin is on PATH, so the UCRT runtime resolves). On a
            # non-Windows validation host the compile + PE-header evidence
            # is recorded and the execution step is explicitly skipped —
            # never reported as executed.
            if ($IsWindows) {
                $probeRunOut = (& $probeExe 2>&1)
                $probeRunRc = $LASTEXITCODE
                if ($probeRunRc -ne 0) {
                    $joined = ($probeRunOut | ForEach-Object { "$_" }) -join '; '
                    Fail "probe executable failed to run (rc=$probeRunRc): $joined"
                }
                elseif (-not (($probeRunOut | ForEach-Object { "$_" }) -join ' ').Contains('ucrt64-probe-ok')) {
                    Fail "probe executable printed unexpected output: $(($probeRunOut | ForEach-Object { "$_" }) -join '; ')"
                }
                else {
                    Write-Output "probe executable ran on the UCRT64 runtime and printed the expected marker"
                }
            }
            else {
                Write-Output "probe execution skipped (non-Windows validation host); compile + PE header evidence recorded above"
            }
        }
    }
    finally {
        Remove-Item -Recurse -Force $probeDir -ErrorAction SilentlyContinue
    }
}

Write-Output "=== pkg-config identity ==="
$pc = Get-Command pkg-config -ErrorAction SilentlyContinue
if ($null -eq $pc) {
    Fail "pkg-config not found on PATH"
}
else {
    Write-Output "pkg-config source: $($pc.Source)"
    if (-not ($pc.Source -like "$ExpectedBinNorm*")) {
        Fail "pkg-config is not the UCRT64 pkgconf (found $($pc.Source))"
    }
    $ver = (pkg-config --version)
    $pcRc = $LASTEXITCODE
    if ($pcRc -ne 0) {
        Fail "pkg-config --version failed with exit code $pcRc"
    }
    else {
        Write-Output "pkg-config version: $ver"
        $majors = $ver.Split('.')
        if ([int]$majors[0] -lt 1 -or ([int]$majors[0] -eq 1 -and [int]$majors[1] -lt 8)) {
            Fail "pkg-config too old (want >= 1.8 from UCRT64 pkgconf)"
        }
    }
}

Write-Output "=== go identity ==="
$goCmd = Get-Command go -ErrorAction SilentlyContinue
if ($null -eq $goCmd) {
    Fail "go not found on PATH"
}
else {
    go version
    $goRc = $LASTEXITCODE
    if ($goRc -ne 0) {
        Fail "go version failed with exit code $goRc"
    }
    go env GOOS GOARCH CGO_ENABLED GOTOOLCHAIN
    $goEnvRc = $LASTEXITCODE
    if ($goEnvRc -ne 0) {
        Fail "go env failed with exit code $goEnvRc"
    }
}

$ended = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
if ($fail) {
    Write-Output "ended_utc: $ended"
    Write-Output "RESULT: FAIL (see the ERROR lines above)"
    exit 1
}
Write-Output "ended_utc: $ended"
Write-Output "RESULT: PASS"
exit 0
