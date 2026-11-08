# Test-UCRT64Fixture.ps1 — runtime fixture suite for the UCRT64 identity
# gate (scripts/ci/Verify-UCRT64.ps1), run-42376176493 repair phases 3.7
# and 11.1-11.4.
#
# Each scenario builds a synthetic MSYS2 tree plus stub gcc / pkg-config /
# pacman / go executables whose behavior is driven by UCRTFIX_* environment
# variables, then runs the REAL gate script with -ExpectedBin pointed at
# the synthetic tree. The gate's exit code and recorded evidence lines
# decide PASS/FAIL — the stubs can never make a broken gate pass.
#
# Scenarios:
#   1  valid UCRT64 root + target header + _UCRT + packages + probe -> exit 0
#      (the gcc private include dir has NO _mingw.h — exactly the
#      run-42376176493 false-negative shape must not fail the gate)
#   2  header missing from the target root but present in the private dir
#      -> exit 1 with the target-root path in the message
#   3  header missing everywhere + pacman -Qo fails -> exit 1 with package
#      diagnostics (never a skip)
#   4  foreign gcc first on PATH (stale shadowing) -> exit 1
#   5  wrong -dumpmachine target -> exit 1
#   6  headers without _UCRT -> exit 1
#   7  pkg-config outside the UCRT64 bin -> exit 1
#   8  native command nonzero exit (gcc --version rc 3) -> exit 1
#   9  pacman absent -> exit 1 (package db cannot be verified)
#  10  msvcrt-shaped headers -> exit 1
#
# The stub toolchain uses POSIX shell stubs; the suite runs under pwsh on
# a POSIX host (or any pwsh 7 host able to execute shell scripts). On
# native Windows the suite reports NOT RUN.
#
# Last line is exactly one of:
#   UCRT64-FIXTURES PASS <n>/<n>
#   UCRT64-FIXTURES FAIL <detail>
#   UCRT64-FIXTURES NOT RUN <reason>
# Exit code: 0 when every scenario behaved as specified (PASS or NOT RUN).

[CmdletBinding()]
param()

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
$PSNativeCommandUseErrorActionPreference = $false

# $PSCommandPath = <repo>/scripts/ci/tests/Test-UCRT64Fixture.ps1 — four
# Split-Path levels up (tests -> ci -> scripts -> repo root).
$repoRoot = Split-Path -Parent (Split-Path -Parent (Split-Path -Parent (Split-Path -Parent $PSCommandPath)))
$gate = Join-Path $repoRoot "scripts/ci/Verify-UCRT64.ps1"

if ($IsWindows) {
    Write-Output "UCRT64-FIXTURES NOT RUN reason=POSIX shell stubs required (run under pwsh on Linux/macOS)"
    exit 0
}
if (-not (Test-Path $gate)) {
    Write-Output "UCRT64-FIXTURES FAIL gate script not found: $gate"
    exit 1
}

$passed = 0
$failed = 0
$failures = [System.Collections.Generic.List[string]]::new()

function Write-Stub([string]$path, [string]$body) {
    # POSIX stub: always LF endings regardless of the host/.ps1 line-ending
    # policy (a CRLF shebang makes the kernel fail with env: 'bash\r').
    $dir = Split-Path -Parent $path
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    $lf = $body.Replace("`r`n", "`n")
    [System.IO.File]::WriteAllText($path, $lf, [System.Text.UTF8Encoding]::new($false))
    & /bin/chmod +x $path
}

$gccStubBody = @'
#!/usr/bin/env bash
# UCRT64 gate fixture stub — behavior driven by UCRTFIX_* env vars.
case "$1" in
  --version)
    echo "gcc.exe (GCC) 16.2.0 (fixture stub)"
    echo "Copyright (C) 2025 Free Software Foundation, Inc. (fixture stub)"
    exit "${UCRTFIX_VERSION_RC:-0}" ;;
  -dumpmachine)
    echo "${UCRTFIX_TARGET:-x86_64-w64-mingw32}"
    exit "${UCRTFIX_TARGET_RC:-0}" ;;
  -print-file-name=include)
    echo "${UCRTFIX_PRIVATE_INCLUDE}"
    exit 0 ;;
  -E)
    cat >/dev/null
    cat "${UCRTFIX_DEFINES_FILE}"
    exit "${UCRTFIX_PROBE_RC:-0}" ;;
esac
prev=""
for a in "$@"; do
  if [ "$prev" = "-o" ]; then
    printf 'MZ' > "$a"
    exit "${UCRTFIX_PROBE_RC:-0}"
  fi
  prev="$a"
done
exit "${UCRTFIX_PROBE_RC:-0}"
'@

$pcStubBody = @'
#!/usr/bin/env bash
echo "${UCRTFIX_PC_VERSION:-3.0.7}"
exit "${UCRTFIX_PC_RC:-0}"
'@

$pacStubBody = @'
#!/usr/bin/env bash
if [ "$1" = "-Qo" ]; then
  echo "/ucrt64/include/_mingw.h is owned by mingw-w64-ucrt-x86_64-headers 12.0.0-1 (fixture stub)"
  exit "${UCRTFIX_QO_RC:-0}"
fi
if [ "$1" = "-Q" ] && [ -n "${2:-}" ]; then
  case " ${UCRTFIX_PACMAN_MISSING:-} " in
    *" $2 "*) echo "error: package '$2' was not found (fixture stub)" >&2; exit 1 ;;
  esac
  echo "$2 16.2.0-1 (fixture stub)"
  exit "${UCRTFIX_PACMAN_RC:-0}"
fi
exit "${UCRTFIX_PACMAN_RC:-0}"
'@

$goStubBody = @'
#!/usr/bin/env bash
case "$1" in
  version) echo "go version go1.27.3 linux/amd64 (fixture stub)"; exit 0 ;;
  env) shift; for a in "$@"; do echo "stub-value-for-$a"; done; exit 0 ;;
esac
exit 0
'@

function New-FixtureTree([hashtable]$s) {
    $root = Join-Path ([System.IO.Path]::GetTempPath()) ("ucrtfix-" + [guid]::NewGuid().ToString('N'))
    $bin = Join-Path $root "msys2/ucrt64/bin"
    $privateInc = Join-Path $root "msys2/ucrt64/lib/gcc/x86_64-w64-mingw32/16.2.0/include"
    $targetInc = Join-Path $root "msys2/ucrt64/include"
    New-Item -ItemType Directory -Force -Path $privateInc, $targetInc | Out-Null

    if ($s.ContainsKey("HeaderInPrivateDir") -and $s.HeaderInPrivateDir) {
        Set-Content -Path (Join-Path $privateInc "_mingw.h") -Value "/* fixture private-dir header */" -Encoding ascii
    }
    $targetAbsent = $s.ContainsKey("TargetHeaderAbsent") -and $s.TargetHeaderAbsent
    if (-not $targetAbsent) {
        Set-Content -Path (Join-Path $targetInc "_mingw.h") -Value "/* fixture target-root header */" -Encoding ascii
    }

    Write-Stub (Join-Path $bin "gcc") $gccStubBody
    Copy-Item (Join-Path $bin "gcc") (Join-Path $bin "gcc.exe")
    Write-Stub (Join-Path $bin "pkg-config") $pcStubBody
    Write-Stub (Join-Path $bin "go") $goStubBody

    if (-not ($s.ContainsKey("PacmanAbsent") -and $s.PacmanAbsent)) {
        Write-Stub (Join-Path $root "msys2/usr/bin/pacman.exe") $pacStubBody
        Write-Stub (Join-Path $root "msys2/usr/bin/pacman") $pacStubBody
    }

    if ($s.ContainsKey("ForeignGcc") -and $s.ForeignGcc) {
        Write-Stub (Join-Path $root "foreign/bin/gcc") $gccStubBody
    }
    if ($s.ContainsKey("ForeignPc") -and $s.ForeignPc) {
        Write-Stub (Join-Path $root "foreign/bin/pkg-config") $pcStubBody
    }

    $defines = "#define _UCRT 1`n#define __MSVCRT_VERSION__ 0xE00"
    if ($s.ContainsKey("Defines") -and $s.Defines) { $defines = $s.Defines }
    $definesFile = Join-Path $root "defines.txt"
    Set-Content -Path $definesFile -Value $defines -Encoding ascii

    return @{
        Root        = $root
        Bin         = $bin
        PrivateInc  = $privateInc
        TargetInc   = $targetInc
        DefinesFile = $definesFile
        ForeignBin  = (Join-Path $root "foreign/bin")
    }
}

function Invoke-Scenario([string]$name, [hashtable]$s, [scriptblock]$assert) {
    $fx = New-FixtureTree $s
    $env:UCRTFIX_PRIVATE_INCLUDE = $fx.PrivateInc
    $env:UCRTFIX_DEFINES_FILE = $fx.DefinesFile
    # Export the scenario-controlled stub behaviors.
    $env:UCRTFIX_TARGET = if ($s.ContainsKey("Target")) { $s.Target } else { "" }
    $env:UCRTFIX_VERSION_RC = if ($s.ContainsKey("VersionRc")) { [string]$s.VersionRc } else { "" }
    $env:UCRTFIX_TARGET_RC = if ($s.ContainsKey("TargetRc")) { [string]$s.TargetRc } else { "" }
    $env:UCRTFIX_PROBE_RC = if ($s.ContainsKey("ProbeRc")) { [string]$s.ProbeRc } else { "" }
    $env:UCRTFIX_PC_VERSION = if ($s.ContainsKey("PcVersion")) { $s.PcVersion } else { "" }
    $env:UCRTFIX_PC_RC = if ($s.ContainsKey("PcRc")) { [string]$s.PcRc } else { "" }
    $env:UCRTFIX_QO_RC = if ($s.ContainsKey("QoRc")) { [string]$s.QoRc } else { "" }
    $env:UCRTFIX_PACMAN_RC = if ($s.ContainsKey("PacmanRc")) { [string]$s.PacmanRc } else { "" }
    $env:UCRTFIX_PACMAN_MISSING = if ($s.ContainsKey("PacmanMissing")) { $s.PacmanMissing } else { "" }
    $oldPath = $env:PATH
    $ok = $false
    $detail = ""
    try {
        $env:PATH = $fx.Bin + ":" + $oldPath
        if (($s.ContainsKey("ForeignGcc") -and $s.ForeignGcc) -or ($s.ContainsKey("ForeignPc") -and $s.ForeignPc)) {
            $env:PATH = $fx.ForeignBin + ":" + $env:PATH
        }
        $out = & pwsh -NoProfile -File $gate -ExpectedBin $fx.Bin 2>&1
        $rc = $LASTEXITCODE
        $text = ($out | ForEach-Object { "$_" }) -join "`n"
        $ok = & $assert $rc $text
        if (-not $ok) { $detail = "assertion returned false — gate rc=$rc; output:`n$text" }
    }
    catch {
        $detail = "scenario threw: $($_.Exception.Message)"
        $ok = $false
    }
    finally {
        $env:PATH = $oldPath
        Remove-Item -Recurse -Force $fx.Root -ErrorAction SilentlyContinue
        foreach ($v in @("UCRTFIX_PRIVATE_INCLUDE", "UCRTFIX_DEFINES_FILE", "UCRTFIX_TARGET",
                "UCRTFIX_VERSION_RC", "UCRTFIX_TARGET_RC", "UCRTFIX_PROBE_RC",
                "UCRTFIX_PC_VERSION", "UCRTFIX_PC_RC", "UCRTFIX_QO_RC",
                "UCRTFIX_PACMAN_RC", "UCRTFIX_PACMAN_MISSING")) {
            Remove-Item "Env:$v" -ErrorAction SilentlyContinue
        }
    }
    if ($ok) {
        $script:passed++
        Write-Output ("PASS    {0}" -f $name)
    }
    else {
        $script:failed++
        $failures.Add($name)
        Write-Output ("FAIL    {0}`n{1}" -f $name, $detail)
    }
}

# 1 — the run-42376176493 false-negative catch: valid target header, the
#     private include dir has NO _mingw.h, everything else healthy.
Invoke-Scenario "S1-valid-target-header-private-dir-empty" @{
    HeaderInPrivateDir = $false
} {
    param($rc, $text)
    if ($rc -ne 0) { return $false }
    if ($text.IndexOf("CRT header _mingw.h present at the UCRT64 target root") -lt 0) { return $false }
    if ($text.IndexOf("RESULT: PASS") -lt 0) { return $false }
    if ($text.IndexOf("pacman ownership:") -lt 0) { return $false }
    if ($text.IndexOf("probe PE MZ header confirmed") -lt 0) { return $false }
    return $true
}

# 2 — header absent from the target root but present in the private dir.
Invoke-Scenario "S2-target-header-missing-private-present" @{
    HeaderInPrivateDir = $true
    TargetHeaderAbsent = $true
} {
    param($rc, $text)
    if ($rc -ne 1) { return $false }
    if ($text.IndexOf("CRT header _mingw.h not found at the UCRT64 target root") -lt 0) { return $false }
    if ($text.IndexOf("RESULT: PASS") -ge 0) { return $false }
    return $true
}

# 3 — header missing everywhere AND pacman -Qo fails.
Invoke-Scenario "S3-header-missing-everywhere-pacman-qo-fails" @{
    TargetHeaderAbsent = $true
    QoRc               = 1
} {
    param($rc, $text)
    if ($rc -ne 1) { return $false }
    if ($text.IndexOf("pacman -Qo /ucrt64/include/_mingw.h failed") -lt 0) { return $false }
    return $true
}

# 4 — a foreign shadowing gcc is first on PATH.
Invoke-Scenario "S4-stale-shadowing-compiler" @{
    ForeignGcc = $true
} {
    param($rc, $text)
    if ($rc -ne 1) { return $false }
    if ($text.IndexOf("first gcc on PATH is NOT the UCRT64 toolchain") -lt 0) { return $false }
    return $true
}

# 5 — wrong target triple.
Invoke-Scenario "S5-wrong-target-triple" @{
    Target = "i686-w64-mingw32"
} {
    param($rc, $text)
    if ($rc -ne 1) { return $false }
    if ($text.IndexOf("unexpected gcc -dumpmachine") -lt 0) { return $false }
    return $true
}

# 6 — headers without _UCRT.
Invoke-Scenario "S6-ucrt-macro-missing" @{
    Defines = "#define __MSVCRT_VERSION__ 0x0700"
} {
    param($rc, $text)
    if ($rc -ne 1) { return $false }
    if ($text.IndexOf("CRT headers do not define _UCRT") -lt 0) { return $false }
    return $true
}

# 7 — pkg-config outside the UCRT64 bin.
Invoke-Scenario "S7-foreign-pkg-config" @{
    ForeignPc = $true
} {
    param($rc, $text)
    if ($rc -ne 1) { return $false }
    if ($text.IndexOf("pkg-config is not the UCRT64 pkgconf") -lt 0) { return $false }
    return $true
}

# 8 — native command nonzero exit surfaces with its code.
Invoke-Scenario "S8-gcc-version-nonzero" @{
    VersionRc = 3
} {
    param($rc, $text)
    if ($rc -ne 1) { return $false }
    if ($text.IndexOf("gcc --version failed with exit code 3") -lt 0) { return $false }
    return $true
}

# 9 — pacman absent: the package db cannot be verified (fail-closed).
Invoke-Scenario "S9-pacman-missing" @{
    PacmanAbsent = $true
} {
    param($rc, $text)
    if ($rc -ne 1) { return $false }
    if ($text.IndexOf("package database cannot be verified") -lt 0) { return $false }
    return $true
}

# 10 — msvcrt-shaped headers.
Invoke-Scenario "S10-msvcrt-headers" @{
    Defines = "#define __MINGW64_VERSION_MAJOR 12"
} {
    param($rc, $text)
    if ($rc -ne 1) { return $false }
    if ($text.IndexOf("CRT headers do not define _UCRT") -lt 0) { return $false }
    return $true
}

if ($failed -eq 0) {
    Write-Output "UCRT64-FIXTURES PASS $passed/$($passed + $failed)"
    exit 0
}
Write-Output ("UCRT64-FIXTURES FAIL {0} passed, {1} failed: {2}" -f $passed, $failed, ($failures -join ", "))
exit 1
