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
# This script is INVOKED as a file (never piped as a script block) and
# performs the real checks. Every native command's exit code is
# captured immediately via $LASTEXITCODE; any failure returns non-zero
# after the full evidence set was recorded. The log contains evaluated
# values only — never PowerShell source text.
#
# Checks (multiple independent evidence sources, per the
# run-42231355353 UCRT64 research contract):
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
#   4. the installed CRT configuration header _mingw.h must exist in
#      the compiler's own include directory under the ucrt64 prefix;
#   5. pkg-config identity: must be the UCRT64 pkgconf, version >= 1.8;
#   6. Go toolchain identity.
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
Write-Output "expected_ucrt64_bin: $ExpectedBin"
if (-not (Test-Path (Join-Path $ExpectedBin 'gcc.exe'))) {
    Fail "expected UCRT64 bin directory does not contain gcc.exe: $ExpectedBin"
}

Write-Output "=== gcc identity ==="
$gcc = Get-Command gcc -ErrorAction SilentlyContinue
if ($null -eq $gcc) {
    Fail "gcc not found on PATH"
}
else {
    Write-Output "gcc source: $($gcc.Source)"
    if (-not ($gcc.Source -like "$ExpectedBin*")) {
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
    $incDir = (gcc -print-file-name=include).Trim()
    $incRc = $LASTEXITCODE
    if ($incRc -ne 0 -or [string]::IsNullOrWhiteSpace($incDir)) {
        Fail "gcc -print-file-name=include failed (rc=$incRc)"
    }
    else {
        Write-Output "gcc include dir: $incDir"
        if ($incDir -notmatch '[\\/]ucrt64[\\/]') {
            Fail "compiler include dir is not inside a ucrt64 prefix: $incDir"
        }
        $mingwHdr = Join-Path $incDir "_mingw.h"
        if (-not (Test-Path $mingwHdr)) {
            Fail "CRT header _mingw.h not found at $mingwHdr (incomplete UCRT64 headers install)"
        }
        else {
            Write-Output "CRT header _mingw.h present: $mingwHdr"
        }
    }
}

Write-Output "=== pkg-config identity ==="
$pc = Get-Command pkg-config -ErrorAction SilentlyContinue
if ($null -eq $pc) {
    Fail "pkg-config not found on PATH"
}
else {
    Write-Output "pkg-config source: $($pc.Source)"
    if (-not ($pc.Source -like "$ExpectedBin*")) {
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
