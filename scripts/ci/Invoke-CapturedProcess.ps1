# Invoke-CapturedProcess — robust GUI-subsystem-safe child-process runner.
#
# Root cause this helper eliminates (run 42837709122, gate
# verify-build-metadata): the gate read the automatic variable
# $LASTEXITCODE immediately after `& "bin\sheytan-music-visualizer.exe"`.
# The distributed desktop executable is linked -H=windowsgui; on the
# hosted runner pwsh never surfaced that child's exit code into
# $LASTEXITCODE, Set-StrictMode -Version Latest then threw
#   "The variable '$LASTEXITCODE' cannot be retrieved because it has
#    not been set."
# and the gate died before it verified anything (the separate
# run-version / run-self-check gates passed — the binary was fine; the
# exit-code capture contract was not).
#
# Contract (run-42837709122 repair, required by the gate spec):
#   - System.Diagnostics.Process / ProcessStartInfo with
#     UseShellExecute = $false: a real CreateProcess launch whose
#     exit code Process.ExitCode is authoritative — $LASTEXITCODE is
#     never read.
#   - stdout AND stderr captured independently (redirected pipes,
#     drained concurrently so a full pipe can never deadlock the child).
#   - ArgumentList: every argument is its own element — no path or
#     argument is ever concatenated into a command string, so paths
#     with spaces and arbitrary metadata stay correct.
#   - Bounded wait ($TimeoutSeconds, default 120). On timeout the child
#     is killed (the ENTIRE process tree created by this invocation where
#     the platform supports it — run-42921967187 review) and the
#     observation is "timed out with no exit code".
#   - FAIL CLOSED: a missing/unobservable exit code is returned as an
#     error object (ExitCode = $null + Error set) — it is NEVER
#     converted to zero and never fabricated. The caller decides
#     pass/fail; an incomplete observation must fail the gate.
#   - CreateNoWindow: hidden child, but stdout/stderr stay captured
#     (hidden is not silent).
#   - Observation metadata (run-42921967187 review): StartedUtc, EndedUtc,
#     ElapsedMilliseconds, ProcessId and a Kind discriminator
#     (exited | timeout | start-failed | invalid-argument) so callers and
#     the consolidated report can distinguish timeout, process-start
#     failure, non-zero exit and missing output without string parsing.
#     An unobservable exit is NEVER reported as Kind 'exited' with code 0.
#
# Usage:
#   . ./scripts/ci/Invoke-CapturedProcess.ps1   # (CWD = repository root)
#   $r = Invoke-CapturedProcess -FilePath "bin\sheytan-music-visualizer.exe" `
#            -ArgumentList @("--version")
#   if ($r.Error) { ... fail closed ... }
#   if ($r.ExitCode -ne 0) { ... real exit code ... }
#   $r.StdOut  # string[] of captured stdout lines
#   $r.StdErr  # string[] of captured stderr lines

function Invoke-CapturedProcess {
    [CmdletBinding()]
    [OutputType([pscustomobject])]
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [string[]]$ArgumentList = @(),
        [int]$TimeoutSeconds = 120,
        [string]$WorkingDirectory = ""
    )

    $startedUtc = (Get-Date).ToUniversalTime()
    $result = [pscustomobject]@{
        ExitCode             = $null
        StdOut               = @()
        StdErr               = @()
        TimedOut             = $false
        Error                = ""
        Kind                 = ""        # exited | timeout | start-failed | invalid-argument
        ProcessId            = $null
        StartedUtc           = $startedUtc.ToString("yyyy-MM-ddTHH:mm:ssZ")
        EndedUtc             = $null
        ElapsedMilliseconds  = $null
    }

    if ([string]::IsNullOrWhiteSpace($FilePath)) {
        $result.Error = "Invoke-CapturedProcess: empty FilePath (nothing to run)"
        $result.Kind = "invalid-argument"
        $result.EndedUtc = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
        return $result
    }

    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $FilePath
    if ($null -ne $ArgumentList) {
        foreach ($a in $ArgumentList) { [void]$psi.ArgumentList.Add([string]$a) }
    }
    $psi.UseShellExecute = $false        # real CreateProcess — exit code observable
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $true
    $psi.CreateNoWindow = $true          # hidden child; streams stay captured
    if (-not [string]::IsNullOrWhiteSpace($WorkingDirectory)) {
        $psi.WorkingDirectory = $WorkingDirectory
    }

    $p = New-Object System.Diagnostics.Process
    $p.StartInfo = $psi

    try {
        if (-not $p.Start()) {
            $result.Error = "Invoke-CapturedProcess: Process.Start returned false for '$FilePath'"
            $result.Kind = "start-failed"
            $result.EndedUtc = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
            $p.Dispose()
            return $result
        }
    }
    catch {
        $result.Error = "Invoke-CapturedProcess: process could not be started: $($_.Exception.Message)"
        $result.Kind = "start-failed"
        $result.EndedUtc = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
        $p.Dispose()
        return $result
    }

    $result.ProcessId = $p.Id

    # Drain BOTH pipes concurrently from the first byte. ReadToEndAsync
    # cannot deadlock on a full pipe (a synchronous ReadToEnd of stdout
    # before stderr would deadlock when stderr fills its buffer).
    $soTask = $p.StandardOutput.ReadToEndAsync()
    $seTask = $p.StandardError.ReadToEndAsync()

    $exited = $false
    try {
        $exited = $p.WaitForExit($TimeoutSeconds * 1000)
    }
    catch {
        $result.Error = "Invoke-CapturedProcess: WaitForExit failed: $($_.Exception.Message)"
    }

    if (-not $exited) {
        $result.TimedOut = $true
        $result.Kind = "timeout"
        if (-not $p.HasExited) {
            # Kill ONLY the process tree created by this invocation: the
            # exact child and its descendants. Never a taskkill sweep.
            # .NET Core 3.0+ Process.Kill(bool entireProcessTree) covers
            # the pwsh 7 runtime; a platform without it falls back to the
            # direct child only.
            try { $p.Kill($true) } catch { try { $p.Kill() } catch { } }
        }
        # Give the pipes a final drain so captured output is never lost.
        try { $soTask.Wait(2000) | Out-Null } catch { }
        try { $seTask.Wait(2000) | Out-Null } catch { }
        try { $result.StdOut = @($soTask.Result -split "`r?`n" | Where-Object { $_.Length -gt 0 }) } catch { }
        try { $result.StdErr = @($seTask.Result -split "`r?`n" | Where-Object { $_.Length -gt 0 }) } catch { }
        $result.Error = ("Invoke-CapturedProcess: child did not exit within {0}s (timed out; killed) — no exit code exists, the observation is incomplete" -f $TimeoutSeconds)
        $result.EndedUtc = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
        $result.ElapsedMilliseconds = [int64](((Get-Date).ToUniversalTime() - $startedUtc).TotalMilliseconds)
        $p.Dispose()
        return $result
    }

    # The process has exited: drain the remainder of both pipes, then
    # read the REAL exit code. HasExited is re-checked and the code is
    # only reported when the OS actually exposed it — a missing exit
    # code is an error, never zero.
    try { $result.StdOut = @($soTask.Result -split "`r?`n" | Where-Object { $_.Length -gt 0 }) } catch {
        $result.Error = "Invoke-CapturedProcess: stdout capture failed: $($_.Exception.Message)"
    }
    try { $result.StdErr = @($seTask.Result -split "`r?`n" | Where-Object { $_.Length -gt 0 }) } catch {
        $result.Error = ($result.Error + " stderr capture failed: " + $_.Exception.Message).Trim()
    }

    if ($p.HasExited) {
        try {
            $result.ExitCode = $p.ExitCode
            $result.Kind = "exited"
        }
        catch {
            $result.Error = ($result.Error + " exit code unobservable after exit: " + $_.Exception.Message).Trim()
            $result.Kind = "start-failed"
        }
    }
    else {
        $result.Error = ($result.Error + " process no longer observable (HasExited=false after wait)").Trim()
        $result.Kind = "start-failed"
    }

    $result.EndedUtc = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
    $result.ElapsedMilliseconds = [int64](((Get-Date).ToUniversalTime() - $startedUtc).TotalMilliseconds)
    $p.Dispose()
    return $result
}
