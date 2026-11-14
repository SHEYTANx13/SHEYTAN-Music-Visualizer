# Get-PackageCommitStatus — the package-status commit observer.
#
# Single documented contract (run 43090342995 item D): the observed
# commit comes EXCLUSIVELY from the app's machine-readable
# `--build-info-json` output, parsed with ConvertFrom-Json into typed
# fields. Human `--version` console text is never scraped with regexes;
# a GUI-subsystem executable's stdout capture quirks can no longer
# forge `commit_observed: not-observed` while the metadata is intact.
#
# Verification rules (must stay identical to scripts/ci/observe_commit.py
# — the bash-side observer; both are covered by the fixture matrix):
#   - A non-zero process exit code is never verified, regardless of output.
#   - Empty/whitespace output, malformed JSON, a missing/empty commit
#     field and the literal "unknown" default are never verified.
#   - The observed value is reported EXACTLY as embedded (no trimming of
#     the field itself); trailing LF/CRLF of the whole stdout is fine
#     because the JSON decoder tolerates it.
#   - A non-blank expected commit must equal the observed commit with
#     exact case-sensitive string comparison.
#
# Dot-source this file, then:
#   $r = Get-SheytanPackageCommitStatus -ExpectedCommit $sha `
#            -BuildInfoStdOut $rawStdOut -ProcessExitCode 0 -Source "built-exe"
#   $r.observed / $r.verified / $r.reason / $r.source

function Get-SheytanPackageCommitStatus {
    [CmdletBinding()]
    param(
        [AllowEmptyString()][string]$ExpectedCommit,
        [AllowEmptyString()][string]$BuildInfoStdOut,
        [Parameter(Mandatory = $true)][int]$ProcessExitCode,
        [string]$Source = "-"
    )
    $observed = "not-observed"
    $verified = $false
    $reason = ""
    if ($ProcessExitCode -ne 0) {
        $reason = "process exited with code $ProcessExitCode; output is not trusted"
    }
    elseif ([string]::IsNullOrWhiteSpace($BuildInfoStdOut)) {
        $reason = "no build-info output captured"
    }
    else {
        try {
            $info = $BuildInfoStdOut | ConvertFrom-Json
            # StrictMode-safe property access: a missing commit property
            # must behave EXACTLY like an empty one (the bash observer in
            # scripts/ci/observe_commit.py treats both as "unknown").
            $commit = $null
            if ($info.PSObject.Properties['commit']) { $commit = $info.commit }
            if ($null -eq $commit -or [string]::IsNullOrWhiteSpace($commit)) {
                $observed = "unknown"
                $reason = "commit field missing or empty in build-info JSON"
            }
            elseif ($commit -eq "unknown") {
                $observed = "unknown"
                $reason = "embedded commit is the unknown default (metadata not injected)"
            }
            else {
                $observed = "$commit"
                $expected = if ([string]::IsNullOrWhiteSpace($ExpectedCommit)) { "" } else { $ExpectedCommit.Trim() }
                if ($expected -eq "") {
                    $reason = "expected commit is blank; identity cannot be verified"
                }
                elseif ($observed -ceq $expected) {
                    $verified = $true
                }
                else {
                    $reason = "observed commit does not match the expected commit"
                }
            }
        }
        catch {
            $reason = "build-info JSON malformed: $($_.Exception.Message)"
        }
    }
    return [pscustomobject]@{
        source   = $Source
        observed = $observed
        verified = $verified
        reason   = $reason
    }
}
