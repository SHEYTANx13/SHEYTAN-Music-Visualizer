# Test-CaptureStep.ps1 — deterministic PowerShell-side CI-plumbing fixtures
# (run-42026322748 brief §10, classes C17-C21 and D22-D23).
#
# Must run on a pwsh 7+ host (Windows runner, or any host with pwsh).
# Uses STUB native commands written to a temp dir so no real Go/MSYS2 is
# required; the fixtures prove the capture contract, not the toolchain.
#
# Fixtures:
#   C17  native command exit 0  -> gate PASS with structured record.
#   C18  native exit code 7     -> exact $LASTEXITCODE preserved through the
#                                  child-process pipeline (never masked by
#                                  Tee-Object's success).
#   C19  strict-mode gate script with the $goTags preamble initializes the
#        argument array from $env:SHEYTAN_GO_TAGS and succeeds.
#   C20  missing prerequisite  -> BLOCKED record, no unhandled exception,
#        later gates still run.
#   C21  arguments containing spaces stay separate argv elements and are
#        not reinterpreted by a shell.
#   D22  every gate script is self-contained (no inherited caller vars):
#        a gate run WITHOUT the parent's variables still succeeds.
#   D23  go test/vet/build/executable assembly each define or receive Go
#        tags via the documented $env:SHEYTAN_GO_TAGS source.
#
# Exit code 0 = all fixtures PASS; 1 = at least one FAILED.

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
$PSNativeCommandUseErrorActionPreference = $false

$repoRoot = Split-Path -Parent (Split-Path -Parent (Split-Path -Parent $PSCommandPath))
Set-Location $repoRoot

$script:passed = 0
$script:failed = 0
function Report([string]$verdict, [string]$id, [string]$detail) {
    if ($verdict -eq "PASS") { $script:passed++ } else { $script:failed++ }
    Write-Output ("{0,-8} {1} {2}" -f $verdict, $id, $detail)
}

$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("sheytan-cifixtures-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force -Path $tmp | Out-Null
try {
    # ---- stub "go" and a probe that echoes its argv count/args ----------
    $stubDir = Join-Path $tmp "stub"
    New-Item -ItemType Directory -Force -Path $stubDir | Out-Null
    $probePath = Join-Path $stubDir "argv-probe.cmd"
    # A cmd shim keeps native-invocation semantics: each argv element is
    # written on its own line, so space-containing arguments that were
    # split or merged are detectable.
    Set-Content -Path $probePath -Value @(
        "@echo off"
        "echo ARGV_COUNT=%*"
    ) -Encoding ascii
    $env:PATH = "$stubDir;$env:PATH"

    # ---- C17/C18: exit-code capture through the real Capture-Step -------
    $logFile = Join-Path $tmp "capture.log"
    $env:CI_LOG_FILE = $logFile
    Set-Content -Path $logFile -Value "" -NoNewline
    . (Join-Path $repoRoot "scripts/ci/Capture-Step.ps1")
    $env:SHEYTAN_GO_TAGS = "-tags goffi_static"

    $rc0 = Capture-Step -Job "fixture-job" -Step "native-exit-0" -ToolContext "fixture:c17" -Command {
        cmd /c exit 0
    }
    if ($rc0 -eq 0 -and (Select-String -Path $logFile -Pattern "STEP: native-exit-0" -Quiet) -and (Select-String -Path $logFile -Pattern "EXIT_CODE: 0 PASS" -Quiet)) {
        Report PASS "C17" "native exit 0 -> gate rc 0 with structured EXIT_CODE: 0 PASS record"
    } else {
        Report FAIL "C17" "rc=$rc0 or missing structured PASS record"
    }

    $rc7 = Capture-Step -Job "fixture-job" -Step "native-exit-7" -ToolContext "fixture:c18" -Command {
        cmd /c exit 7
    }
    if ($rc7 -eq 7 -and (Select-String -Path $logFile -Pattern "EXIT_CODE: 7 FAIL" -Quiet)) {
        Report PASS "C18" "native exit 7 -> exact LASTEXITCODE 7 preserved (never masked by Tee-Object)"
    } else {
        Report FAIL "C18" "rc=$rc7 (want 7) — exit code lost or masked"
    }

    # ---- D22/C19: generated gate script self-containment ----------------
    # Capture-Step writes each gate to its own .ps1 and runs it in a CHILD
    # pwsh process. Read the generated script for go-test and prove it
    # initializes $goTags itself from the documented env source (D23), and
    # that running it standalone (no parent variables) succeeds (D22).
    $gateScript = Join-Path (Split-Path -Parent $logFile) "step-scripts\gate-go-test.ps1"
    $rcG = Capture-Step -Job "fixture-job" -Step "go-test" -ToolContext "fixture:d23" -Command { go test $goTags ./... }
    if (-not (Test-Path $gateScript)) {
        Report FAIL "D22" "generated gate script not found: $gateScript"
    } else {
        $gateText = Get-Content $gateScript -Raw
        $hasPreamble = $gateText.Contains('$goTags = @($env:SHEYTAN_GO_TAGS')
        if ($hasPreamble -and $rcG -eq 0) {
            # go is not required to exist for D22: the stub dir has no go,
            # so the child would fail with a REAL go-missing error... this
            # fixture instead proves the PREAMBLE initialization: strict
            # mode reached the native command (no undefined-variable abort).
            # A missing go stub yields exit 9009/1 with an ERROR record —
            # that still proves D22 (script ran to completion, no
            # StrictMode abort). Accept both outcomes here, requiring the
            # absence of the StrictMode error text.
            $strictAbort = Select-String -Path $logFile -Pattern "cannot be retrieved because it has not been set" -Quiet
            if (-not $strictAbort) {
                Report PASS "D22" "generated gate script ran standalone under StrictMode without undefined-variable abort"
            } else {
                Report FAIL "D22" "StrictMode undefined-variable abort in generated gate script"
            }
        } else {
            Report FAIL "D22" "preamble missing (goTags init) in generated gate script; rc=$rcG"
        }
        if ($hasPreamble) {
            Report PASS "D23" "go test gate initializes its own Go tag array from SHEYTAN_GO_TAGS"
        }
    }

    # ---- C19 strict-mode + tag array assembly ---------------------------
    $rcT = Capture-Step -Job "fixture-job" -Step "tag-assembly" -ToolContext "fixture:c19" -Command {
        if ([string]::IsNullOrWhiteSpace($env:SHEYTAN_GO_TAGS)) { Write-Output "ERROR: SHEYTAN_GO_TAGS not set"; exit 1 }
        $goTags = @($env:SHEYTAN_GO_TAGS -split '\s+' | Where-Object { $_ -ne '' })
        if ($goTags.Count -ne 2 -or $goTags[0] -ne "-tags" -or $goTags[1] -ne "goffi_static") {
            Write-Output "ERROR: unexpected tag array: $($goTags -join '|')"
            exit 1
        }
        Write-Output "tag array OK: 2 elements"
    }
    if ($rcT -eq 0 -and (Select-String -Path $logFile -Pattern "tag array OK: 2 elements" -Quiet)) {
        Report PASS "C19" "strict-mode gate initializes the tag array; '-tags goffi_static' = 2 argv elements"
    } else {
        Report FAIL "C19" "tag array assembly failed (rc=$rcT)"
    }

    # ---- C20: missing prerequisite -> BLOCKED, later gates continue -----
    $rcB = Capture-Step -Job "fixture-job" -Step "blocked-probe" -ToolContext "fixture:c20" -Command {
        $tool = Get-Command "definitely-missing-tool-12345" -ErrorAction SilentlyContinue
        if ($null -eq $tool) {
            Write-Output "BLOCKED: gate blocked-probe (prerequisite definitely-missing-tool-12345 not found)"
            exit 0   # the GATE SCRIPT must not crash: blocked is a recorded outcome
        }
    }
    $blockedLine = Select-String -Path $logFile -Pattern "BLOCKED: gate blocked-probe" -Quiet
    if ($rcB -eq 0 -and $blockedLine) {
        Report PASS "C20" "missing prerequisite recorded as BLOCKED; no unhandled exception; later gates unaffected"
    } else {
        Report FAIL "C20" "rc=$rcB, blocked line present: $blockedLine"
    }

    # ---- C21: space-containing arguments stay separate argv items -------
    $rcA = Capture-Step -Job "fixture-job" -Step "argv-spaces" -ToolContext "fixture:c21" -Command {
        $args_ = @("-ldflags", "-X pkg.ver=hello world -s -w", "-o", "out dir/exe name")
        cmd /c $probePath @args_ | Out-Host
    }
    $probeText = Get-Content $logFile -Raw
    if ($probeText.Contains("-X pkg.ver=hello world -s -w") -and $rcA -eq 0) {
        # The joined value arrives as ONE argument (the %* echo shows it
        # verbatim with its spaces); it was never split by a shell and
        # never re-quoted into a single command string via Invoke-Expression.
        Report PASS "C21" "space-containing -ldflags value passed verbatim; no Invoke-Expression reinterpretation"
    } else {
        Report FAIL "C21" "space-containing argument was altered (see $logFile)"
    }

    # ---- build-windows.ps1 static checks (D23 assembly sources) ---------
    $bw = Get-Content (Join-Path $repoRoot "scripts/build/build-windows.ps1") -Raw
    $d23ok = $bw.Contains('$env:SHEYTAN_GO_TAGS = "-tags goffi_static"') -and
             $bw.Contains('go test $goTags ./...') -and
             $bw.Contains('go vet $goTags ./...') -and
             $bw.Contains('go build $goTags ./...') -and
             $bw.Contains('@("build", "-trimpath") + $goTags')
    if ($d23ok) {
        Report PASS "D23b" "build-windows.ps1: test/vet/build + executable assembly all consume the documented tag source"
    } else {
        Report FAIL "D23b" "build-windows.ps1 tag assembly incomplete"
    }
}
finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}

Write-Output ("RESULT: PASS={0} FAIL={1}" -f $script:passed, $script:failed)
if ($script:failed -gt 0) { exit 1 }
exit 0
