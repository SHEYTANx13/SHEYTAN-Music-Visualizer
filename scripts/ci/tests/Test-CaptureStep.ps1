# Test-CaptureStep.ps1 — deterministic PowerShell-side CI-plumbing fixtures
# (run-42026322748 brief §10 classes C17-C21 / D22-D23, plus the
# run-42231355353 repairs: gate isolation D24/D25, script-block invocation
# regression C22, enforced argv fidelity C21b).
#
# Must run on a pwsh 7+ host (Windows runner, or any host with pwsh).
# Uses STUB native commands written to a temp dir so no real Go/MSYS2 is
# required; the fixtures prove the capture contract, not the toolchain.
# Native commands are selected per-host ($IsWindows): cmd.exe on Windows,
# /bin/sh elsewhere — the exit-code and argv contracts are identical.
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
#        not reinterpreted by a shell; the per-argument probe proves the
#        space-containing value arrived as ONE argv element (the old %*
#        echo could not distinguish split from joined).
#   C22  script-block invocation regression (run 42231355353): a bare
#        script block piped into a cmdlet logs SOURCE TEXT and never
#        executes; the repaired pattern (invoke the file/call operator)
#        logs EVALUATED values. The false-green class is documented and
#        guarded: the repository workflow must invoke helpers as files.
#   D22  every Go gate script is self-contained (no inherited caller vars):
#        a gate run WITHOUT the parent's variables still succeeds.
#   D23  go test/vet/build/executable assembly each define or receive Go
#        tags via the documented $env:SHEYTAN_GO_TAGS source (-GoTags).
#   D24  gate isolation: a generic gate generated WITHOUT -GoTags contains
#        no SHEYTAN_GO_TAGS dependency and succeeds while the tag variable
#        is absent (run 42231355353: generic gates must not be blocked by
#        a build-tag variable).
#   D25  a Go gate generated WITH -GoTags fails loudly when the tag source
#        is missing (never silently builds without tags).
#
# Exit code 0 = all fixtures PASS; 1 = at least one FAILED.

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
$PSNativeCommandUseErrorActionPreference = $false

# Resolve paths against an ABSOLUTE script path: a relative -File
# invocation previously broke the three Split-Path hops (the fixtures
# then dot-sourced scripts/scripts/ci/Capture-Step.ps1 and crashed).
$testScriptPath = (Resolve-Path -LiteralPath $PSCommandPath).Path
$repoRoot = Split-Path -Parent (Split-Path -Parent (Split-Path -Parent (Split-Path -Parent $testScriptPath)))
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
    # ---- per-host native exit helpers -------------------------------------
    # Each exit-N snippet is a REAL native command on this host, so the
    # fixtures exercise the true $LASTEXITCODE path on Windows AND Linux.
    $exit0 = if ($IsWindows) { { cmd /c exit 0 } } else { { sh -c 'exit 0' } }
    $exit7 = if ($IsWindows) { { cmd /c exit 7 } } else { { sh -c 'exit 7' } }

    # ---- per-argument argv probe (unambiguous one-element proof) ----------
    $stubDir = Join-Path $tmp "stub"
    New-Item -ItemType Directory -Force -Path $stubDir | Out-Null
    if ($IsWindows) {
        $probePath = Join-Path $stubDir "argv-probe.cmd"
        # cmd shim printing EACH argument bracketed on its own line: a
        # space-containing argument that was split by any layer becomes
        # multiple ARG=[...] lines; one element stays one line.
        Set-Content -Path $probePath -Value @(
            "@echo off"
            ":loop"
            'if "%~1"=="" goto done'
            "echo ARG=[%~1]"
            "shift"
            "goto loop"
            ":done"
        ) -Encoding ascii
    }
    else {
        $probePath = Join-Path $stubDir "argv-probe.sh"
        Set-Content -Path $probePath -Value @(
            "#!/bin/sh"
            'for a in "$@"; do'
            'echo "ARG=[$a]"'
            "done"
        ) -Encoding utf8NoBOM
    }
    if (-not $IsWindows) { chmod +x $probePath | Out-Null }

    # Deterministic stub "go" for gate fixtures whose subject is the
    # generated-script CONTRACT, not a real build (the real toolchain's
    # dependency state must not influence these fixtures).
    $goStub = Join-Path $stubDir "go"
    if ($IsWindows) {
        Set-Content -Path "$goStub.cmd" -Value @("@echo off", "exit 0") -Encoding ascii
    } else {
        Set-Content -Path $goStub -Value ("#!/bin/sh", "exit 0") -Encoding utf8NoBOM
        chmod +x $goStub | Out-Null
    }

    # ---- C17/C18: exit-code capture through the real Capture-Step -------
    $logFile = Join-Path $tmp "capture.log"
    $env:CI_LOG_FILE = $logFile
    Set-Content -Path $logFile -Value "" -NoNewline
    . (Join-Path $repoRoot "scripts/ci/Capture-Step.ps1")
    $env:SHEYTAN_GO_TAGS = "-tags goffi_static"

    $rc0 = Capture-Step -Job "fixture-job" -Step "native-exit-0" -ToolContext "fixture:c17" -Command $exit0
    if ($rc0 -eq 0 -and (Select-String -Path $logFile -Pattern "STEP: native-exit-0" -Quiet) -and (Select-String -Path $logFile -Pattern "EXIT_CODE: 0 PASS" -Quiet)) {
        Report PASS "C17" "native exit 0 -> gate rc 0 with structured EXIT_CODE: 0 PASS record"
    } else {
        Report FAIL "C17" "rc=$rc0 or missing structured PASS record"
    }

    $rc7 = Capture-Step -Job "fixture-job" -Step "native-exit-7" -ToolContext "fixture:c18" -Command $exit7
    if ($rc7 -eq 7 -and (Select-String -Path $logFile -Pattern "EXIT_CODE: 7 FAIL" -Quiet)) {
        Report PASS "C18" "native exit 7 -> exact LASTEXITCODE 7 preserved (never masked by Tee-Object)"
    } else {
        Report FAIL "C18" "rc=$rc7 (want 7) — exit code lost or masked"
    }

    # ---- C22: script-block invocation regression (false-green class) ----
    # Reproduce the run-42231355353 defect shape in a child pwsh: a bare
    # script block piped into a cmdlet writes the SOURCE TEXT, the body
    # never runs, and the caller's failure flag stays false. The repaired
    # pattern (call operator) executes and records evaluated values.
    # Command-STRING form: exactly the pipeline shape the workflow had.
    $c22Script = @'
$marker = "C22-EVALUATED-MARKER-8271"
$old = { Write-Output $args[0]; $script:fail = $true } 2>&1 | Out-String
$oldExecuted = $old.Contains($marker)
$fail = $false
$new = & { param([string]$m) Write-Output $m; $script:fail = $true } $marker
$newExecuted = (($new | Out-String)).Contains($marker)
"OLDEXEC=$oldExecuted"
"NEWEXEC=$newExecuted"
'@
    $c22 = & pwsh -NoProfile -Command $c22Script
    $c22Text = ($c22 | ForEach-Object { "$_" }) -join "`n"
    if ($c22Text -match "OLDEXEC=False" -and $c22Text -match "NEWEXEC=True") {
        Report PASS "C22" "uninvoked script block never executes (source text only); call-operator invocation executes — false-green class documented and guarded"
    } else {
        Report FAIL "C22" "script-block invocation semantics changed (see output): $c22Text"
    }

    # ---- D22/D23: generated Go gate script self-containment -------------
    # Capture-Step writes each gate to its own .ps1 and runs it in a CHILD
    # pwsh process. Read the generated script for go-test and prove it
    # initializes $goTags itself from the documented env source (D23), and
    # that running it standalone (no parent variables) succeeds (D22).
    # The stub go keeps the fixture deterministic: the assertion target is
    # the generated-script contract, never the host's dependency state.
    $gateScript = Join-Path (Split-Path -Parent $logFile) "step-scripts/gate-go-test.ps1"
    $savedPath = $env:PATH
    $env:PATH = "$stubDir$([System.IO.Path]::PathSeparator)$savedPath"
    $rcG = Capture-Step -Job "fixture-job" -Step "go-test" -ToolContext "fixture:d23" -GoTags -Command { go test $goTags ./... }
    $env:PATH = $savedPath
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
            Report PASS "D23" "go test gate (-GoTags) initializes its own Go tag array from SHEYTAN_GO_TAGS"
        }
    }

    # ---- D24: gate isolation — generic gate has no tag dependency -------
    $genericScript = Join-Path (Split-Path -Parent $logFile) "step-scripts/gate-generic-runtime.ps1"
    Remove-Item Env:SHEYTAN_GO_TAGS -ErrorAction SilentlyContinue
    $rcGen = Capture-Step -Job "fixture-job" -Step "generic-runtime" -ToolContext "fixture:d24" -Command $exit0
    if (Test-Path $genericScript) {
        $genericText = Get-Content $genericScript -Raw
        if ($rcGen -eq 0 -and -not $genericText.Contains('SHEYTAN_GO_TAGS')) {
            Report PASS "D24" "generic gate (no -GoTags) contains no tag dependency and succeeds with SHEYTAN_GO_TAGS absent"
        } else {
            Report FAIL "D24" "generic gate still depends on the tag variable (rc=$rcGen, preamble present: $($genericText.Contains('SHEYTAN_GO_TAGS')))"
        }
    } else {
        Report FAIL "D24" "generated generic gate script not found: $genericScript"
    }

    # ---- D25: Go gate fails loudly without the tag source ---------------
    # (Tag source absent: the preamble must fail the gate BEFORE any
    # native command runs — the stub go must never be reached.)
    $rcMissing = Capture-Step -Job "fixture-job" -Step "go-tag-missing" -ToolContext "fixture:d25" -GoTags -Command { go test $goTags ./... }
    $missingLogged = Select-String -Path $logFile -Pattern "ERROR: SHEYTAN_GO_TAGS is not set" -Quiet
    if ($rcMissing -ne 0 -and $missingLogged) {
        Report PASS "D25" "Go gate (-GoTags) fails loudly when SHEYTAN_GO_TAGS is missing (never silently builds without tags)"
    } else {
        Report FAIL "D25" "missing tag source did not fail the Go gate loudly (rc=$rcMissing, error logged: $missingLogged)"
    }
    $env:SHEYTAN_GO_TAGS = "-tags goffi_static"

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

    # ---- C21: space-containing arguments stay ONE argv element ----------
    # The probe path crosses the child-process boundary through the
    # environment (a child pwsh cannot see caller variables).
    $env:FIXTURE_ARGV_PROBE = $probePath
    $rcA = Capture-Step -Job "fixture-job" -Step "argv-spaces" -ToolContext "fixture:c21" -Command {
        $probe = $env:FIXTURE_ARGV_PROBE
        $args_ = @("-ldflags", "-X pkg.ver=hello world -s -w", "-o", "out dir/exe name")
        & $probe @args_
    }
    $probeText = Get-Content $logFile -Raw
    if ($probeText.Contains("ARG=[-X pkg.ver=hello world -s -w]") -and $rcA -eq 0) {
        # The joined value arrived as ONE argument (its own bracketed
        # probe line); it was never split across arguments and never
        # reinterpreted by a shell.
        Report PASS "C21" "space-containing -ldflags value passed as ONE argv element (per-argument probe), no shell reinterpretation"
    } else {
        Report FAIL "C21" "space-containing argument was split or altered (rc=$rcA; see $logFile)"
    }

    # ---- build-windows.ps1 static checks (D23 assembly sources) ---------
    $bw = Get-Content (Join-Path $repoRoot "scripts/build/build-windows.ps1") -Raw
    $d23ok = $bw.Contains('$env:SHEYTAN_GO_TAGS = "-tags goffi_static"') -and
             $bw.Contains('go test $goTags ./...') -and
             $bw.Contains('go vet $goTags ./...') -and
             $bw.Contains('go build $goTags ./...') -and
             $bw.Contains('@("build", "-trimpath") + $goTags') -and
             # run-42231355353 metadata contract: every -X flag carries its
             # own "-X " token inside the single -ldflags value, and an
             # enforced metadata acceptance gate exists.
             $bw.Contains('"-X ${bi}.version=') -and
             $bw.Contains('"-X ${bi}.commit=') -and
             $bw.Contains('verify-build-metadata')
    if ($d23ok) {
        Report PASS "D23b" "build-windows.ps1: tag assembly + -X ldflags contract + enforced metadata gate all present"
    } else {
        Report FAIL "D23b" "build-windows.ps1 tag assembly / metadata contract incomplete"
    }
}
finally {
    Remove-Item Env:FIXTURE_ARGV_PROBE -ErrorAction SilentlyContinue
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}

Write-Output ("RESULT: PASS={0} FAIL={1}" -f $script:passed, $script:failed)
if ($script:failed -gt 0) { exit 1 }
exit 0
