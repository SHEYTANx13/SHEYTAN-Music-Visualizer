# Write-SanitizedChunk — canonical capture-time sanitizer routing for the
# PowerShell producers (run 44462492262 repair).
#
# Contract (mirrors scripts/ci/capture.sh ci_sanitize):
#   1. $env:CI_SANITIZER_EXE (the built cmd/cisanitize binary) when set
#      and present — the canonical path.
#   2. python3 scripts/ci/sanitize_bytes.py — pure-Python bootstrap
#      fallback, used only when the Go toolchain is not yet available in
#      a bootstrap context. python3 is attempted first, then python:
#      Windows runners do not always expose a python3 launcher name.
#   3. Otherwise: a loud marker line on the error stream and the input
#      is returned UNCHANGED (raw, LF-normalized). The strict byte-level
#      validator will fail that log; this helper never silently pretends
#      to have sanitized anything.
#
# Additional invariants:
#   - The returned text is LF-normalized in EVERY path (the canonical
#     sanitizer already emits LF; the fallback normalizes CRLF/CR to LF),
#     so callers never write CRLF control bytes into ci-logs/.
#   - The helper deals in TEXT ONLY and returns NOTHING else: it never
#     touches $LASTEXITCODE and can never mask or replace any caller's
#     exit-code semantics. The sanitizer process's own exit status is
#     recorded in diagnostics, never forwarded.
#   - PowerShell 5.1 AND pwsh 7 compatible: no ArgumentList (added in
#     .NET Core only, absent in .NET Framework), no ?? operator, no
#     ternary, explicit UTF8Encoding($false) instances.
#
# Dot-source this file, then:
#   $clean = Write-SanitizedChunk -Text $rawLines
#
# The sanitizer itself implements (in order): BOM strip, ANSI escape
# strip, CR/CRLF -> LF, >=0x80 -> '?', C0 controls (except LF/TAB) + DEL
# -> '?', '='/'-' run clamp to two, then credential redaction to
# REDACTED (URL credentials, Bearer headers, GitHub tokens, AWS key IDs,
# credential query parameters, private environment value dumps).

function ConvertTo-LfText {
    # Normalizes CRLF and lone CR to LF (PowerShell-side complement of
    # the sanitize CR rule; used in the no-sanitizer fallback path).
    param([AllowEmptyString()][string]$Text)
    return $Text.Replace("`r`n", "`n").Replace("`r", "`n")
}

function Invoke-SanitizerProcess {
    # Internal: runs one sanitizer process, feeds InputText on stdin
    # (byte-safe UTF-8, no BOM), returns the stdout TEXT, or $null when
    # the process could not start, timed out, or exited non-zero. The
    # caller decides the fallback. NEVER throws on sanitizer failure.
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [string[]]$ArgumentList = @(),
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$InputText
    )
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $FilePath
    # .Arguments (not .ArgumentList): ArgumentList does not exist in
    # .NET Framework, so PowerShell 5.1 would throw. Manual quoting
    # keeps paths with spaces correct on both runtimes.
    if ($ArgumentList.Count -gt 0) {
        $quoted = @($ArgumentList | ForEach-Object { '"' + ($_ -replace '"', '""') + '"' })
        $psi.Arguments = ($quoted -join ' ')
    }
    $psi.UseShellExecute = $false
    $psi.RedirectStandardInput = $true
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $true
    $psi.CreateNoWindow = $true
    $psi.StandardOutputEncoding = New-Object System.Text.UTF8Encoding($false)
    $psi.StandardErrorEncoding = New-Object System.Text.UTF8Encoding($false)

    $p = New-Object System.Diagnostics.Process
    $p.StartInfo = $psi
    try {
        if (-not $p.Start()) { return $null }
    }
    catch {
        try { $p.Dispose() } catch { }
        return $null
    }

    # Start BOTH drains before writing stdin so a full pipe can never
    # deadlock the sanitizer (same discipline as Invoke-CapturedProcess).
    $soTask = $p.StandardOutput.ReadToEndAsync()
    $seTask = $p.StandardError.ReadToEndAsync()

    try {
        $bytes = (New-Object System.Text.UTF8Encoding($false)).GetBytes($InputText)
        $p.StandardInput.BaseStream.Write($bytes, 0, $bytes.Length)
        $p.StandardInput.BaseStream.Flush()
        $p.StandardInput.BaseStream.Dispose()   # EOF for the sanitizer
    }
    catch {
        try { $p.Kill() } catch { }
        try { $p.Dispose() } catch { }
        return $null
    }

    if (-not $p.WaitForExit(30000)) {
        try { $p.Kill() } catch { }
        try { $p.Dispose() } catch { }
        return $null
    }
    if ($p.ExitCode -ne 0) {
        try { $p.Dispose() } catch { }
        return $null
    }
    $out = ""
    try {
        $null = $soTask.Wait(2000)
        $out = $soTask.Result
    }
    catch {
        try { $p.Dispose() } catch { }
        return $null
    }
    try { $p.Dispose() } catch { }
    return $out
}

function Write-SanitizedChunk {
    [CmdletBinding()]
    [OutputType([string])]
    param(
        [Parameter(Mandatory = $true)][AllowEmptyString()][string]$Text,
        # Repository root used only to locate the Python fallback script.
        # Defaults to the parent of this file's directory (scripts/).
        [string]$RepoRoot = ""
    )
    if ($RepoRoot -eq "") {
        $RepoRoot = Split-Path -Parent $PSScriptRoot
    }
    $marker = '[ci_capture] SANITIZER UNAVAILABLE — raw output preserved; the strict validator will fail this log'

    # 1. Canonical Go sanitizer.
    if ($env:CI_SANITIZER_EXE -and (Test-Path -LiteralPath $env:CI_SANITIZER_EXE -PathType Leaf)) {
        $out = Invoke-SanitizerProcess -FilePath $env:CI_SANITIZER_EXE -InputText $Text
        if ($null -ne $out) { return (ConvertTo-LfText $out) }
        [Console]::Error.WriteLine('[ci_capture] CI_SANITIZER_EXE failed — falling back to the python bootstrap sanitizer')
    }

    # 2. Python bootstrap fallback (python3, then python for Windows
    #    runner naming).
    $pyScript = Join-Path $PSScriptRoot 'sanitize_bytes.py'
    $pyExe = $null
    foreach ($candidate in @('python3', 'python')) {
        $cmd = Get-Command $candidate -ErrorAction SilentlyContinue
        if ($null -ne $cmd -and (Test-Path -LiteralPath $pyScript -PathType Leaf)) {
            $pyExe = $cmd.Source
            break
        }
    }
    if ($pyExe) {
        $out = Invoke-SanitizerProcess -FilePath $pyExe -ArgumentList @($pyScript) -InputText $Text
        if ($null -ne $out) { return (ConvertTo-LfText $out) }
    }

    # 3. FAIL CLOSED (priority-A repair of the raw-passthrough bypass):
    #    raw output must never enter a file bound for the strict log
    #    contract, even behind a loud marker. The chunk is replaced by a
    #    strict-contract ERROR record that preserves the failure origin;
    #    the caller's step exit code is untouched (the record is
    #    diagnostic, the exit code decides the gate).
    [Console]::Error.WriteLine($marker)
    $line = 'ERROR|platform=windows-x64|job=build-windows|step=log-capture|gate=desktop-build|command=write-sanitized-chunk|tool=Write-SanitizedChunk|source=NOT_PROVIDED_BY_TOOL|message=sanitizer and python fallback unavailable; captured chunk NOT written raw (fail closed); the producing command exit code still decides the gate'
    return (ConvertTo-LfText $line)
}
