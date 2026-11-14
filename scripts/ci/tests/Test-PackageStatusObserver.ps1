# Test-PackageStatusObserver — run-43090342995 item D regression fixtures.
#
# Cross-platform (pwsh 7): exercises scripts/ci/Get-PackageCommitStatus.ps1
# against the FULL verification matrix: correct commit, missing commit
# field, mismatch, trailing whitespace, LF/CRLF line endings, empty
# output, malformed JSON and non-zero process exit. The contract MUST
# stay identical to scripts/ci/observe_commit.py, which carries the same
# matrix in scripts/ci/tests/test-package-status.sh.
#
# Exit 0 = every fixture PASS; nonzero = at least one FAIL.

param(
    [string]$RepoRoot = (Split-Path -Parent (Split-Path -Parent (Split-Path -Parent (Split-Path -Parent $PSCommandPath))))
)

Set-StrictMode -Version 3.0
$ErrorActionPreference = "Stop"
$PSNativeCommandUseErrorActionPreference = $false

. (Join-Path $RepoRoot "scripts/ci/Get-PackageCommitStatus.ps1")

$PASSED = 0; $FAILED = 0
function Verdict([string]$id, [bool]$ok, [string]$detail) {
    if ($ok) { $script:PASSED++; Write-Output ("PASS    {0}  {1}" -f $id, $detail) }
    else     { $script:FAILED++; Write-Output ("FAIL    {0}  {1}" -f $id, $detail) }
}

$sha = "acc64d80d0029410ea6e1671edfd795afbdf06f5"
$sha2 = "0123456789abcdef0123456789abcdef01234567"
function BuildInfo([string]$commit) {
    return '{"version":"1.0.0","commit":"' + $commit + '","date":"2026-11-13T00:00:00Z","built_by":"ci","go_version":"go1.27.4","os":"windows","arch":"amd64"}'
}

Write-Output "=============================================================="
Write-Output "PACKAGE-STATUS COMMIT OBSERVER FIXTURES — $((Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ'))"
Write-Output "pwsh: $($PSVersionTable.PSVersion)"
Write-Output "=============================================================="

# O1 correct commit -> verified
$r = Get-SheytanPackageCommitStatus -ExpectedCommit $sha -BuildInfoStdOut (BuildInfo $sha) -ProcessExitCode 0 -Source "built-exe"
Verdict "O1" ($r.verified -and $r.observed -ceq $sha -and $r.source -eq "built-exe") "correct commit verifies exactly"

# O2 missing commit field -> never verified, observed=unknown
$r = Get-SheytanPackageCommitStatus -ExpectedCommit $sha -BuildInfoStdOut '{"version":"1.0.0","date":"x"}' -ProcessExitCode 0
Verdict "O2" (-not $r.verified -and $r.observed -eq "unknown") "missing commit field is never verified"

# O3 mismatch -> observed truthfully, not verified
$r = Get-SheytanPackageCommitStatus -ExpectedCommit $sha -BuildInfoStdOut (BuildInfo $sha2) -ProcessExitCode 0
Verdict "O3" (-not $r.verified -and $r.observed -ceq $sha2 -and $r.reason -ne "") "commit mismatch reports the real observed value and stays red"

# O4 trailing whitespace + LF and CRLF endings -> still verified
$rLf = Get-SheytanPackageCommitStatus -ExpectedCommit $sha -BuildInfoStdOut ((BuildInfo $sha) + "`n") -ProcessExitCode 0
$rCrLf = Get-SheytanPackageCommitStatus -ExpectedCommit $sha -BuildInfoStdOut ((BuildInfo $sha) + "`r`n") -ProcessExitCode 0
$rPad = Get-SheytanPackageCommitStatus -ExpectedCommit "  $sha  " -BuildInfoStdOut ("`r`n" + (BuildInfo $sha) + "  ") -ProcessExitCode 0
Verdict "O4" ($rLf.verified -and $rCrLf.verified -and $rPad.verified) "LF/CRLF endings and surrounding whitespace never break exact verification"

# O5 empty output -> never verified
$r = Get-SheytanPackageCommitStatus -ExpectedCommit $sha -BuildInfoStdOut "" -ProcessExitCode 0
Verdict "O5" (-not $r.verified -and $r.observed -eq "not-observed") "empty output records not-observed"

# O6 malformed JSON -> never verified
$r = Get-SheytanPackageCommitStatus -ExpectedCommit $sha -BuildInfoStdOut "SHEYTAN Music Visualizer main (commit $sha, ...)" -ProcessExitCode 0
Verdict "O6" (-not $r.verified) "human console text (malformed JSON) is never accepted"

# O7 non-zero process exit -> never verified even with correct JSON
$r = Get-SheytanPackageCommitStatus -ExpectedCommit $sha -BuildInfoStdOut (BuildInfo $sha) -ProcessExitCode 1
Verdict "O7" (-not $r.verified -and $r.observed -eq "not-observed") "non-zero exit code is never verified regardless of output"

# O8 unknown default -> never verified
$r = Get-SheytanPackageCommitStatus -ExpectedCommit $sha -BuildInfoStdOut (BuildInfo "unknown") -ProcessExitCode 0
Verdict "O8" (-not $r.verified -and $r.observed -eq "unknown") "embedded unknown default can never verify"

# O9 blank expected -> cannot verify
$r = Get-SheytanPackageCommitStatus -ExpectedCommit "" -BuildInfoStdOut (BuildInfo $sha) -ProcessExitCode 0
Verdict "O9" (-not $r.verified) "blank expected commit cannot verify (local builds report identity, not CI proof)"

# O10 case difference in the observed commit -> not verified (exact identity)
$upper = $sha.ToUpper()
$r = Get-SheytanPackageCommitStatus -ExpectedCommit $sha -BuildInfoStdOut (BuildInfo $upper) -ProcessExitCode 0
Verdict "O10" (-not $r.verified -and $r.observed -ceq $upper) "commit comparison is exact (case-sensitive)"

Write-Output "SUMMARY: pass=$PASSED fail=$FAILED"
if ($FAILED -gt 0) { exit 1 }
exit 0
