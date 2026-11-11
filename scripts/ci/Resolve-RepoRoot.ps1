# Resolve-RepoRoot — THE single deterministic repository-root resolver.
#
# Root cause this helper eliminates (run 42921967187, gates
# verify-build-metadata / create-portable-zip / verify-extracted-package):
# generated gate scripts dot-sourced repository helpers through
# `Join-Path $env:SHEYTAN_REPO_ROOT ...`, but that process environment
# variable was never defined for the spawned gate process. All three gates
# died on the same line before any check executed:
#   Cannot bind argument to parameter 'Path' because it is null.
# The helpers never loaded, so none of their checks ran. The failure was one
# root cause at the workflow/gate-generation/helper boundary — not three
# application bugs.
#
# Contract (run-42921967187 repair):
#   - Candidates, in deterministic order:
#       1. $env:SHEYTAN_REPO_ROOT  (documented primary; the CI workflow sets
#          it at the build-windows job scope from github.workspace)
#       2. $env:GITHUB_WORKSPACE   (standard runner location — the fallback,
#          so a custom variable can never again be a single point of failure)
#     The current working directory is NEVER consulted: gate scripts run
#     with a CWD such as ci-logs/step-scripts that is not the helper root.
#   - Every candidate is normalized with [System.IO.Path]::GetFullPath and
#     validated as an existing directory that is actually THIS repository
#     (contains scripts/ci/Resolve-RepoRoot.ps1 and go.mod). A set-but-wrong
#     SHEYTAN_REPO_ROOT falls through to the next candidate instead of
#     failing the run.
#   - Every required helper path (Relative to the root) is validated to
#     exist BEFORE the caller dot-sources it, so a missing helper produces a
#     precise diagnostic instead of a null-Path binding error.
#   - Failure is LOUD and PRECISE: the thrown message names the gate, the
#     safe candidate roots, and the expected relative helper path. Vague
#     'Join-Path Path is null' errors are impossible from this contract.
#   - The function never fabricates a root and never returns $null: it
#     either returns a validated full path or throws.
#
# Usage (any script, including generated gate scripts):
#   . (Join-Path $repoRoot 'scripts/ci/Resolve-RepoRoot.ps1')   # once loaded
#   $root = Resolve-SheytanRepoRoot -GateName 'verify-build-metadata' `
#       -RequiredRelativePaths @('scripts/ci/Invoke-CapturedProcess.ps1')
#   . (Join-Path $root 'scripts/ci/Invoke-CapturedProcess.ps1')
#
# Generated gate scripts get this contract through the Capture-Step
# -RequireRepoRoot / -RepoRootHelpers preamble (scripts/ci/Capture-Step.ps1);
# they never hand-roll their own resolution.

function Resolve-SheytanRepoRoot {
    [CmdletBinding()]
    [OutputType([string])]
    param(
        # Gate/step name for precise diagnostics (the generated preamble
        # passes the serialized gate's step name).
        [string]$GateName = "-",
        # Repository-relative helper paths that the caller will dot-source
        # after resolution. Each one is verified to exist before return.
        [string[]]$RequiredRelativePaths = @()
    )

    $candidates = @()
    foreach ($raw in @($env:SHEYTAN_REPO_ROOT, $env:GITHUB_WORKSPACE)) {
        if (-not [string]::IsNullOrWhiteSpace($raw)) { $candidates += $raw.Trim() }
    }

    $problems = New-Object System.Collections.Generic.List[string]
    if ($candidates.Count -eq 0) {
        $problems.Add("no candidate root was present: SHEYTAN_REPO_ROOT and GITHUB_WORKSPACE are both empty/absent")
    }

    foreach ($candidate in $candidates) {
        $full = $null
        try {
            $full = [System.IO.Path]::GetFullPath($candidate)
        }
        catch {
            $problems.Add("candidate '$candidate' could not be normalized: $($_.Exception.Message)")
            continue
        }
        if ([string]::IsNullOrWhiteSpace($full)) {
            $problems.Add("candidate '$candidate' normalized to an empty path")
            continue
        }
        if (-not (Test-Path -LiteralPath $full -PathType Container)) {
            $problems.Add("candidate '$full' is not an existing directory")
            continue
        }
        # Repository identity markers: the resolver itself and the Go module.
        $resolverFile = Join-Path $full "scripts/ci/Resolve-RepoRoot.ps1"
        if (-not (Test-Path -LiteralPath $resolverFile -PathType Leaf)) {
            $problems.Add("candidate '$full' does not contain scripts/ci/Resolve-RepoRoot.ps1 (not this repository)")
            continue
        }
        $goMod = Join-Path $full "go.mod"
        if (-not (Test-Path -LiteralPath $goMod -PathType Leaf)) {
            $problems.Add("candidate '$full' has no go.mod (not this repository)")
            continue
        }
        # Required helpers must exist before the caller imports them.
        $helpersOk = $true
        foreach ($rel in $RequiredRelativePaths) {
            if ([string]::IsNullOrWhiteSpace($rel)) {
                $problems.Add("required helper path is empty (gate '$GateName')")
                $helpersOk = $false
                continue
            }
            $target = Join-Path $full $rel
            if (-not (Test-Path -LiteralPath $target)) {
                $problems.Add("required helper '$rel' does not exist under '$full' (gate '$GateName')")
                $helpersOk = $false
            }
        }
        if (-not $helpersOk) { continue }
        return $full
    }

    $detail = ($problems -join "; ")
    throw (@"
Resolve-SheytanRepoRoot FAILED for gate '$GateName'.
  candidates consulted (order): SHEYTAN_REPO_ROOT, then GITHUB_WORKSPACE
  candidate values (safe paths): $(if ($candidates.Count -gt 0) { $candidates -join '; ' } else { '(none present)' })
  expected relative helper path(s): $(if ($RequiredRelativePaths.Count -gt 0) { $RequiredRelativePaths -join '; ' } else { '(none required)' })
  problems: $detail
  remedy: set SHEYTAN_REPO_ROOT (or the standard GITHUB_WORKSPACE) to the
  repository checkout root for the gate child process. The current working
  directory is never used as the root.
"@)
}
