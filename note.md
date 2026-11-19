# Execution note: SHEYTAN Music Visualizer BETA

Session date: 2026-11-19 (UTC). Scope: the run 44449832903 update (single canonical workflow, unified studio direction) executed in an offline Linux engineering sandbox with no repository write authorization to any remote.

## 1. Product identity

`SHEYTAN Music Visualizer BETA` (product-facing label `BETA` everywhere; no numbered app release labels).

## 2. Starting repository state

- Remote: `https://github.com/SHEYTANx13/SHEYTAN-Music-Visualizer` (public; slug verified from the actual clone).
- Starting branch: `main`, HEAD `9df0c59c3f01096319b2c7afbf44aa2669f69f89` (commit message "BETA"). Worktree was clean; no local branches from prior sessions were present in the fresh clone.

## 3. Target run 44449832903: actual state (live-fetched this session)

- URL: `https://github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/actions/runs/44449832903`
- Workflow file at that run: `.github/workflows/build-desktop.yml` (since renamed; see item 9).
- Event: push to `refs/heads/main` by SHEYTANx13; head SHA `9df0c59` (full `9df0c59c3f01096319b2c7afbf44aa2669f69f89`).
- Conclusion: **failure — Invalid workflow file**. The three annotations (lines 1154, 1163, 1172, column 13) read `Unexpected symbol: '${'. Located at position 1 within expression: ${ always() && !cancelled() }`. A workflow-file parse failure is total: **zero jobs ran, no build happened, no artifact exists** for this run.
- Historical baseline (prior report, kept as context only): run 44330231458, SHA `5395d8b9cdd04d50d5d56225be58b7c03cc6a664`, preflight SUCCESS, build-linux SUCCESS, build-windows FAILURE (`TestSameInodeReplacementRequiresReconfirmation`). Its consolidated log violated the ASCII contract (1,383 `===`, 640 `---`, 4,354 ANSI bytes, 3 BOMs, 242 non-ASCII chars). The committed remediation for that run (`internal/asciilog`, `cmd/civalidate`, collector hardening) was present at HEAD but had never been hosted-verified.

## 4. Prior-session note status

The repository's committed `note.md` at start described the run 44330231458 session. It is a historical record of that session, not this one; this file replaces it as the current execution note (previous notes remain in git history).

## 5. Windows `TestSameInodeReplacementRequiresReconfirmation`: root cause and fix state

- Root cause class: a delete+recreate of the granted root can reuse the same MFT record (volume serial + file index), and NTFS name tunneling can additionally restore the original CreationTime inside the ~15 s tunnel window, so a stale confirmation could be accepted as "same identity".
- Fix at HEAD (9df0c59, unverified): creation-time discriminator via `GetFileInformationByHandle` (`identity_windows.go`).
- This session's hardening (commit `2f0a70c`): the identity primitive now also reads the documented **128-bit `FILE_ID_INFO`** via `GetFileInformationByHandleEx(FileIdInfo)`; on NTFS the low quad carries the MFT record sequence number (NTFS increments it on every record reuse — it cannot be restored by tunneling). `RootIdentity.InoHigh` was added; `sameRootIdentity` compares it as a primary component and fails closed on present-vs-absent mismatch; `newScopeID` includes it. Tests extended (`TestWindowsIdentityCarriesCreationTime` pins stability incl. `InoHigh`; `TestWindowsRecreatedDirectoryRejected` rejects the recreated instance).
- Evidence level: `GOOS=windows GOARCH=amd64 go build` + `go vet` pass locally; Linux `go test ./internal/fileaccess/` passes. **Native Windows execution of the identity tests: NOT VERIFIED** (requires a `windows-latest` hosted runner; no push was authorized). The hosted Windows go-test gate remains the proof point.

## 6. Prior regressions: state after this session

Verified locally on Linux (go1.27.4), `-count=1`:

- `internal/mediatools` (archive extraction, Unicode/space paths, xz suite, pinned resolver fixtures): **ok**.
- `internal/studio`: **build failed in this sandbox only** (cgo GL/GLFW headers absent; `goffi` link stubs fail without libGL). Not a source regression; the hosted `build-linux` job installs the Fyne prerequisites and owns this gate. Treat as NOT RUN locally, hosted-pending.
- `internal/jobs` (bounded queue governance), `internal/playback`, `internal/audio`, `internal/export` (deterministic export proofs), `internal/dsp`, `internal/content`, `internal/project`, `internal/appclose`, `internal/apppaths`, `internal/ghreq` (200/302/rate-limit/resume/digest fixtures), `internal/logging`, `internal/buildinfo`, `internal/brand`, `internal/render`, `internal/visualizer`, `internal/fflocate`, `internal/perf`, `internal/procattr`, `internal/songdna`, `internal/sysprofile`, `internal/cilog`, `internal/asciilog`: **all ok**.
- Windows-specific archive-mode and studio PCM-handle gates: unchanged code paths; hosted Windows runner owns them.

## 7. Platform package states (this session)

No platform package is delivered from this session:

- `SHEYTAN-Music-Visualizer-BETA-Windows-x64-TEST.zip`: **UNAVAILABLE** (hosted build not run; the run 44330231458 artifact was TEST_ONLY and is not re-offered).
- `SHEYTAN-Music-Visualizer-BETA-Linux-x64-TEST.tar.gz`: **UNAVAILABLE** (hosted build not run; the run 44330231458 VERIFIED artifact predates this source state and is not re-offered).
- macOS / Android / iOS packages: **UNAVAILABLE** (macOS needs `macos-26`; Android/iOS compile smoke needs Android SDK/NDK and macOS+Xcode; none exist in this sandbox).

## 8. Candidate commit

- Branch: `fix/44449832903-canonical-build-workflow` (LOCAL ONLY).
- Repair commit (all changes of item 9): `2f0a70c9e2e72661138644d548e1bb5c5eb85077` (parent `9df0c59c3f01096319b2c7afbf44aa2669f69f89`).
- This note.md is committed immediately after it as the branch-tip docs commit; the exact tip SHA is stated in the handoff summary and is verifiable with `git log` in a checkout of the branch.
- **No push, no PR, no release, no remote write of any kind was performed** (user authorization required). The hosted run for the candidate SHA therefore does not exist yet.

## 9. Implemented changes (commit 2f0a70c, 25 files, +531/-122)

Workflow / CI:

1. `build-desktop.yml` renamed to **`.github/workflows/build.yml`** (the single canonical application build workflow; the old file is retired). `name: build`, concurrency group `build-`.
2. Run 44449832903 expression repair: the three malformed conditions `if: ${ always() && !cancelled() }` (former lines 1154/1163/1172) are now `if: always()`, each annotated in-source with the historical defect reference.
3. Central collector contract (latest-update override A): `collect-logs` job-level condition is exactly `if: always()` (success, failure, skip AND cancellation all still collect); its download/manifest/build/run/validate/fallback steps also run under plain `always()`. The only retained `!cancelled()` conditions are (a) the in-job temporary-artifact cleanup step and (b) the `final-verdict` job, each with an in-source justification comment; both use the exact `${{ }}` syntax.
4. ASCII log contract (override F / spec 10): every `===` / `---` producer teed into `ci-logs/` was removed from the workflow (section markers now `[section] ...`, verbatim-capture marker is contract-clean). Per-job `civalidate` byte gates now cannot be tripped by the workflow's own markers (they previously would have failed `build-linux`'s gate on its own `=== OS image ===` headers — a latent red gate).
5. New regression gate `scripts/ci/tests/test-expression-syntax.sh` + `scripts/ci/tests/expression_syntax_check.py`, wired into preflight: reparses every workflow with PyYAML; rejects any `if:` value containing a `${` that is not `${{`; rejects unbalanced `${{`; asserts the collector's job-level condition is exactly `always()`; negative-controls the three historical annotation strings plus two unbalanced samples (4/4 detected).
6. Fixed `scripts/ci/tests/test-verdict-expression.sh` path rot: `../../` resolved to a nonexistent directory so the loop silently scanned **zero** workflow files; now `../../../` with an existence guard and a scanned-files count. Also extended its gate-name regex to underscore names (`analyze_go`) so `codeql.yml` is actually checked (it now passes).
7. Updated all current-state `build-desktop` references to `build.yml` across `.github/actionlint.yaml`, `codeql.yml` comments, `scripts/ci/{write-manifest,record-gate-result,gen_audit_ledger}.py`, `scripts/ci/tests/{test-ci-plumbing,test-docs-consistency}.sh`, collector comments, and docs (`ci.md`, `CI-DIAGNOSTICS.md`, `DEPENDENCIES.md`, `ai-notes.md` current-state table, `roadmap.md`, `agent.md`). Historical run records in `CHANGELOG.md`, `evidence/`, `docs/REQUIREMENTS-TRACEABILITY.md` and `docs/security/` were deliberately left verbatim (they describe their own sessions).
8. Latent-defect repairs surfaced by the now-actually-scanning fixtures (all three pre-existed at HEAD): `scripts/ci/capture.sh` printed an **empty COMMAND line** (piped into a function that reads a parameter, not stdin); fixture B11 expected a stale three-job collector `needs` list; fixture H30 predated the five-platform manifest writer's fail-closed env contract. All fixed; `test-ci-plumbing.sh` now reports `PASS=20 FAIL=0 NOT_RUN=3` (NOT RUN = pwsh-only fixtures; this host has no pwsh).
9. gofmt: four files carried pre-existing doc-comment drift under go1.27.4 (`cmd/ci-log-collector/main.go`, `internal/cilog/{collect.go,classification_test.go,githubapi_test.go}`); reformatted. `gofmt -l .` is now clean repo-wide (the preflight gate would have gone red on HEAD).
10. Docs truthfulness: stale collector comments claiming the artifact name `log.txt` corrected to `SHEYTAN-Music-Visualizer-all-logs.txt`.

File-access security: item 5 above (FILE_ID_INFO root identity).

BETA branding: already in place and re-verified — `internal/buildinfo` defaults to `BETA` (unit-tested), workflow build jobs pass `SHEYTAN_BUILD_VERSION: BETA`.

## 10. Dependency pins

- Go: module `go 1.27`, toolchain `go1.27.4` (CI resolves via `go-version-file: go.mod`).
- Wails: **`v3.1.0` is the newest non-prerelease stable** on the Go module proxy at session time (v3.1.0-nightly.202610xx and v3.2.0-nightly.* exist but are prereleases). **Not yet added to go.mod** — the Wails migration did not start (see item 12).
- Go-GUI: **`github.com/go-gui-org/go-gui v0.97.0`** is the newest tagged release on the module proxy. **Not yet added to go.mod.**
- GoGPU: **`github.com/gogpu/gogpu v0.54.1`** is the newest tagged release; the repository currently pins the coherent graph `gogpu/wgpu v0.34.15`, `gogpu/gputypes v0.8.2` (+ `gpucontext v0.32.0`, `naga v0.22.0` in the module graph). go-gpu/goffi v0.6.8 is in the graph. No floating upgrades were made (`go mod tidy -diff` clean, `go mod verify` all verified).

## 11. Architecture state

The architecture decision record is unchanged in intent: Wails v3 (lifecycle/windowing) + Go-GUI (all product UI) + GoGPU (render viewport), one top-level window, bounded channels, honest GPU states. What exists in code at the candidate SHA is still the Fyne production UI plus the GoGPU/wgpu module graph used by `internal/render`/`internal/visualizer`.

## 12. UI migration status (truthful)

- Fyne (`ui/fyne`) **remains the production UI**. The full Go-GUI workspace migration (unified tab workspace, resizable/dockable panels, red hellish theme tokens, Requirements & Downloads route, Diagnostics) **was NOT implemented in this session**.
- Blocking reasons: (a) this sandbox has no display, no WebView, no GPU and cannot link the existing Fyne/GL stack, so no UI behavior could be verified here; (b) the spec requires real Wails-hosted render/input proof per platform, which only hosted/native runners can produce; (c) adding go-gui/wails pins to go.mod without any ability to run or test them would have risked breaking the currently-green build graph. Recorded as **BLOCKED** with the concrete integration plan intact (override C/E; sections 4, 7 of the work order). No mock, placeholder, or fake-completeness UI was added.

## 13. Tests executed (exact commands, Linux x64 sandbox, go1.27.4)

- `go test -count=1 ./internal/asciilog/ ./internal/cilog/ ./internal/fileaccess/ ./internal/buildinfo/ ./internal/brand/ ./internal/ghreq/ ./internal/logging/` — ok.
- `go test -count=1 ./internal/mediatools/ ./internal/jobs/ ./internal/dsp/ ./internal/content/ ./internal/project/ ./internal/appclose/ ./internal/apppaths/ ./internal/songdna/ ./internal/sysprofile/ ./internal/perf/ ./internal/procattr/ ./internal/visualizer/ ./internal/render/ ./internal/audio/ ./internal/playback/ ./internal/export/ ./internal/fflocate/ ./internal/fileaccess/` — all ok (export suite ~40 s).
- `go test -count=1 ./internal/cilog/` after the doc-comment reformat — ok.
- `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build` + `go vet` over `internal/fileaccess`, `internal/cilog`, `internal/asciilog`, `internal/buildinfo`, `internal/brand`, `internal/ghreq`, `internal/logging`, `cmd/civalidate`, `cmd/ci-log-collector` — ok.
- `gofmt -l .` — clean. `go vet` on the listed packages — clean. `go mod tidy -diff` — clean. `go mod verify` — all modules verified.
- `bash scripts/ci/tests/test-expression-syntax.sh` — PASS. `bash scripts/ci/tests/test-verdict-expression.sh` — PASS (2 workflow files scanned). `bash scripts/ci/tests/test-ci-plumbing.sh` — PASS=20 FAIL=0 NOT_RUN=3 (pwsh fixtures need a pwsh host). `bash scripts/ci/tests/test-docs-consistency.sh` — PASS 5/0.
- actionlint 1.7.12 (official tarball, sha256-verified against the pinned checksum) over `build.yml` + `codeql.yml` with the two documented ignores — exit 0; negative fixture (deliberately invalid workflow) correctly rejected.
- Limitations: `zizmor` and `shellcheck` are not installable here (no uv/pwsh); those preflight sub-gates are hosted-pending. `internal/studio` and `ui/fyne` tests are not buildable in this sandbox (missing GL/GLFW system headers; cgo). Race tests (`-race`) were not run in this sandbox; hosted Linux owns `go-test-race`.

## 14. Runtime vs compile evidence

Everything in this session is **compile/unit-level evidence on Linux** plus **compile-level evidence for windows/amd64**. No application was launched on any desktop or mobile OS; no GUI, audio-device, GPU, WebView, emulator or simulator evidence exists for this candidate. The prior run's Linux Xvfb GUI-smoke evidence belongs to SHA 5395d8b, not to this candidate.

## 15. Audio playback verification state

Unchanged by this session. `internal/audio`, `internal/playback` unit suites pass locally (deterministic PCM paths); real output-device negotiation and position advancement require the hosted GUI-smoke environment (Xvfb + null-PCM classification on `build-linux`), which did not run for this candidate. Status: **PARTIAL (unit-level only)**.

## 16. GitHub Requirements Manager status

`internal/ghreq` (discovery, stable-release filtering, redirect policy, rate limiting, resume validators, digest verification) passes its full local fixture suite. The Go-GUI Requirements & Downloads route on top of it **does not exist yet** (BLOCKED with the UI migration; see item 12). No catalog, download or install behavior was changed.

## 17. Queue and resource governance status

`internal/jobs` (bounded typed queues, cancellation, fairness) passes its local suite. No changes were made to scheduler, cache-handle or import-coalescing behavior in this session.

## 18. File identity and selected-scope security

Covered in item 5: volume serial + 128-bit file ID (MFT sequence) + creation FILETIME from the opened handle; fail-closed comparator; scope ID includes every identity component; all handles closed via `defer h.Close()` on the opened root. Linux regression suite green; Windows tests hosted-pending (NOT VERIFIED locally). Archive extraction protections unchanged and green (`internal/mediatools`).

## 19. ASCII log verification (canonical deliverable `SHEYTAN-Music-Visualizer-all-logs.txt`)

Produced by the real pipeline (`write-manifest.py` -> `cmd/ci-log-collector` -> `cmd/civalidate`) over this session's local evidence; parts sanitized at capture time with the repository's canonical `ci_sanitize`:

- bytes: 14,417; lines: 235; non-ASCII characters/bytes: **0**; ANSI escape bytes: **0**; UTF-8 BOM: **0**; control bytes: **0**; exact `===` sequences: **0**; exact `---` sequences: **0**; secrets/tokens: none present (collector redaction active; inputs were this session's gate outputs only).
- Content: run metadata (event `local-verification`, ref `refs/heads/fix/44449832903-canonical-build-workflow`, commit `2f0a70c...`), JOB STATUS TABLE (preflight success; the five platform jobs recorded `skipped` / `NEVER_STARTED` because no hosted job ran), and the merged preflight evidence part.
- Collector classification: coverage COMPLETE, **overall=FAIL (rc=3)** — the honest verdict: no hosted platform job has run for this candidate. This artifact is a local verification log, not a hosted-run log.

## 20. Five-platform matrix (candidate SHA 2f0a70c)

| Job | Runner | State | Evidence level |
| --- | --- | --- | --- |
| build-windows | windows-latest (definition) | **UNAVAILABLE** (not run) | local: cross-compile + vet of CI-critical packages incl. the hardened identity primitive |
| build-linux | ubuntu-26.04 (definition) | **UNAVAILABLE** (not run) | local: unit suites for 20+ internal packages; studio/ui gates blocked by sandbox GL headers |
| build-macos | macos-26 (definition) | **UNAVAILABLE** (not run) | none |
| build-android | ubuntu-26.04 + SDK/NDK (definition, EXPERIMENTAL compile smoke) | **UNAVAILABLE** (not run) | none |
| build-ios | macos-26 + Xcode (definition, EXPERIMENTAL compile smoke) | **UNAVAILABLE** (not run) | none |

Mobile jobs remain `EXPERIMENTAL_TEST` by definition; no APK/IPA/simulator artifact exists.

## 21. CodeQL status

**UNKNOWN / NOT RETRIEVED.** `.github/workflows/codeql.yml` is present and actionlint-clean; no SARIF or alert data was fetched for the candidate SHA. No zero-alert claim is made.

## 22. Hosted Actions status for the candidate

**LOCAL_GREEN_HOSTED_PENDING.** Every gate this sandbox could execute is green (items 13, 19); no hosted Actions run exists for `2f0a70c9e2e72661138644d548e1bb5c5eb85077` because pushing was not authorized. The first hosted run of `build.yml` on this SHA is also the availability proof point for the `ubuntu-26.04` runner label (see `.github/actionlint.yaml` note).

## 23. Known limitations, blockers, next required owner actions

Limitations / blockers:

- No remote write was authorized: the candidate exists only on the local branch named above.
- Native Windows, macOS, Android, iOS verification: BLOCKED on hosted runners.
- Go-GUI/Wails/GoGPU UI migration: BLOCKED as described in item 12 (this is the largest open work item; the 22+18-minute UI slices of the work order were not consumed).
- sandbox-only gaps: shellcheck, zizmor, Xvfb, audio device, GPU, pwsh fixtures.

Next required owner actions:

1. Push the local branch (user-authorized remote write) and dispatch `build.yml` on `2f0a70c...`; the preflight expression gate, the five platform jobs, the collector and the final verdict then produce the hosted evidence.
2. Verify the Windows go-test gate (identity fix proof) on the hosted windows-latest runner.
3. Then schedule the Go-GUI/Wails/GoGPU integration slices with the pins from item 10.

## 24. Deliverables (exact names, this download set)

- `SHEYTAN-Music-Visualizer-BETA-SOURCE.zip` — full candidate source tree (top-level directory `SHEYTAN-Music-Visualizer-BETA-SOURCE/`), committed state = branch tip (repair commit `2f0a70c...` + this note commit), excludes `.git`, caches, built binaries, and the unsanitized historical `evidence/*.log` files (kept in git history; excluded per the source-ZIP log rule). Verified to open with the expected top-level directory and manifest.
- `note.md` — this file (also present at the repository root inside the ZIP).
- `SHEYTAN-Music-Visualizer-all-logs.txt` — sanitized local verification consolidated log (byte stats in item 19; validated by `cmd/civalidate`, 0 violations).
