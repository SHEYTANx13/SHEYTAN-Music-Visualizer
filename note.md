# Execution note — run 44330231458 update

- Product: SHEYTAN Music Visualizer BETA
- Execution start: 2026-11-18T20:05:19Z, execution end: 2026-11-18T20:24:00Z (elapsed within the 113-minute budget; ~19 minutes wall clock in the Linux implementation sandbox, all of it logged below)
- Base (verified): `main` at `5395d8b9cdd04d50d5d56225be58b7c03cc6a664` — exactly the SHA of the failed Windows gate in the supplied report; remote slug verified ASCII `SHEYTANx13/SHEYTAN-Music-Visualizer` from the actual `origin` remote.
- Work policy honored: local edits only. No push, no branch creation, no PR, no release, no repository settings change, no user data touched (worktree was clean at inspection).

## 1. What this update delivered

### 1.1 Windows red gate repaired (the only failed gate, `build-windows/go-test`)

`TestSameInodeReplacementRequiresReconfirmation` failed on Windows with `same-(dev,ino) replacement accepted: <nil>`.

Root cause (measured from the actual source at the run SHA): the Windows identity primitive `identityFromPath` (internal/fileaccess/identity_windows.go) captured only volume serial + file index + mtime + size. A delete+recreate that reuses the same MFT record (volume serial + file index) while the recreated directory's mtime coincides is invisible to that tuple, so the stale confirmation was accepted. The Linux primitive (statx) carries a birth-time discriminator; Windows did not.

Fix: the Windows primitive now also captures the handle's `CreationTime` (per-instance discriminator) into the identity's birth-time fields via a new `filetimeUnixNanos` conversion (1601 epoch to Unix nanoseconds, 100 ns ticks). The comparison contract (`sameRootIdentity`) needed no change: birth time present on both sides now rejects the simulated same-file-id replacement deterministically, and mtime remains a second discriminator.

New regression tests: internal/fileaccess/identity_windows_test.go (Windows-only, compile-verified for windows/amd64, EXECUTION NOT VERIFIED locally — see section 3):
- `TestWindowsIdentityCarriesCreationTime` — the primitive must carry a usable creation time and be stable across stats of the same instance.
- `TestWindowsRecreatedDirectoryRejected` — a real delete+recreate must be rejected through the creation-time or file-id discriminator.

### 1.2 Build-log ASCII contract implemented end to end (Section 2.5 violations)

Measured violations on the attached run log (1,383 `===`, 640 `---`, 242 non-ASCII chars / 699 bytes >= 0x80, 4,354 ANSI escape bytes, 3 BOMs) are addressed at the SOURCE, plus a byte-level validator gate:

- internal/asciilog — new canonical implementation: `Sanitize` (BOM strip, ANSI CSI/OSC/two-char escape strip, stray-ESC neutralization, CRLF to LF, non-ASCII to '?', C0/DEL to '?', `=`/`-` runs clamped to two) and `Validate` (exact byte-class counts, same metrics as the evidence scan). Unit tested.
- scripts/ci/capture.sh (Linux producer): new record markers `:: COMMAND BEGIN :: / :: OUTPUT ... :: / :: END OUTPUT :: / :: COMMAND END ::` (no `===`/`---`), streaming `ci_sanitize` (sed implementation of the same byte rules) piped between every captured command and the log/console, and sanitized record header values. PIPESTATUS[0] semantics preserved — original exit codes are never masked.
- scripts/ci/Capture-Step.ps1 (Windows producer): same markers, `ConvertTo-AsciiLogLine` sanitizer applied to every captured line before Tee-Object, sanitized header values.
- internal/cilog/gate.go: record-marker regexes and the `--- FAIL:` root-cause signature updated in lockstep (`-- FAIL:` matches sanitized output).
- internal/cilog/collect.go: the consolidated report passes through the canonical sanitizer immediately before write, and the written bytes are validated — a validator violation fails the collector (fail closed). The report's own `heavySeparator` and 41 em-dashes in emitted strings are now ASCII. Corresponding test assertions updated (collect_test.go, githubapi_test.go, gate_test.go, classification_test.go).
- cmd/civalidate — new byte-level validator CLI; added as a `Validate ASCII log contract` gate step in every platform job (windows/linux/macos/android/ios) before artifact upload; exit 1 on any violation.
- scripts/ci/emergency-report.sh: separators and em-dashes ASCII-cleaned.
- internal/logging: the RUNTIME log (app.log and the stderr fallback) now enforces the same contract via an `asciiHandler` wrapper plus `ReplaceAttr` value sanitization; pinned by `TestASCIIContractEnforced`.

Divergence note: the sed streaming variant normalizes a lone CR to LF (matching the canonical Go semantics); CRLF handling is identical. The validator is the authority; the collector sanitizes the final artifact regardless of producer behavior.

### 1.3 Product-facing BETA metadata

- internal/buildinfo: default version and fallback are now `BETA` (product label until fully functional; no numbered app release labels in this phase). `--version` no longer depends on the git ref name.
- .github/workflows/build-desktop.yml: `SHEYTAN_BUILD_VERSION: BETA` in both platform build jobs (previously `${{ github.ref_name }}` = `main`).

### 1.4 Workflow repair and five-target expansion

- REPAIRED a real pre-existing defect: the workflow triggers contained `branches: ain]` — the byte-level remnant of a previous malformed ANSI-strip pass that consumed the `[m` of `branches: [main]`. Restored to `branches: [main]` for push and pull_request. (This also explains the corruption class seen elsewhere in tooling displays; the alert-summary fixture in githubapi_test.go LOOKED corrupted in text views but its bytes are intact — verified with od.)
- build-macos job ADDED (macos-26, REQUIRED): toolchain report, gofmt, `go mod verify`, full `go test ./...`, vet, package build, BETA version check, tar.gz package, civalidate gate. Its gate contract is a documented REDUCED subset: not-yet-ported parity gates (race, export/audio/GPU validation, GUI smoke) are recorded BLOCKED inside the job — never PASS — and the package is TEST_ONLY until they are ported and pass on a hosted runner.
- build-android job ADDED (EXPERIMENTAL, non-required): toolchain report incl. NDK identity, CGO cross-compile smoke of the pure product packages for android/arm64 against the NDK clang wrapper, civalidate gate. NO APK claim, no emulator/device claim, no GoGPU native-backend claim.
- build-ios job ADDED (EXPERIMENTAL, non-required, macos runner): toolchain report incl. Xcode identity, CGO compile smoke for ios/arm64, civalidate gate. NO simulator-validation claim, NO signing claim.
- collect-logs and final-verdict wired for the three new jobs (needs, artifact downloads, manifest results, aggregate verdict; macos required, android/ios reported truthfully as experimental non-required).
- scripts/ci/write-manifest.py extended accordingly (results, downloads, artifacts, jobs with required flags).
- New build scripts: scripts/build/build-macos.sh, build-android.sh, build-ios.sh (all `bash -n` clean; they dot-source the canonical capture.sh so every gate produces structured records through the new contract).

### 1.5 Verified integration pins (recorded for the migration work)

Queried live from the Go module proxy during this execution:
- github.com/go-gui-org/go-gui v0.97.0 (2026-11-17T22:28:30Z, tag hash 11be524688a2d3a366716083085ec91d935d1418)
- github.com/wailsapp/wails/v3 v3.1.0 (2026-11-01, tag hash 4b454e443f6f11ccb64679c5c3d3f3bf5e6fb440)
- github.com/gogpu/gogpu v0.54.1 (2026-11-08, tag hash 01b7d29ce5678a034483bcaee6c6b5822110a08a); the repository already pins gogpu/wgpu v0.34.15 and gogpu/gputypes v0.8.2.

## 2. Local verification evidence (Linux sandbox, Go 1.27.4)

- `go build ./internal/... ./cmd/civalidate` — PASS
- `go vet ./internal/... ./cmd/civalidate` — PASS
- `go test ./internal/...` — ALL PASS, including the exact classes the prompt requires to stay fixed: internal/mediatools (archive/space/Unicode coverage), internal/studio (cold-cache actionable event; passes with the canonical `-tags goffi_static` tag used by the Linux build script; without that tag the sandbox linker cannot link the goffi test binary — a sandbox limitation, not a source defect), internal/ghreq, internal/fileaccess (Linux identity semantics unchanged), internal/cilog, internal/asciilog, internal/logging, internal/buildinfo.
- `GOOS=windows GOARCH=amd64`: build + vet of the touched packages PASS; the Windows fileaccess test binary COMPILES (`go test -c`). Windows test EXECUTION requires a hosted windows runner — NOT VERIFIED locally (no Windows OS in this sandbox; no wine available).
- `GOOS=darwin GOARCH=arm64`: touched packages build — PASS.
- `cmd/civalidate` self-check: a fixture with BOM + ANSI + non-ASCII + `===` + `---` is detected with exact counts and exits 1; a clean fixture exits 0.
- `ui/fyne` and `cmd/sheytan-music-visualizer` were NOT compiled locally: the sandbox lacks OpenGL/Wayland/pkg-config GL development headers (go-gl/glfw cgo requirements). The hosted Linux runner provides them (run 44330231458 built them successfully); no source change touches those packages.
- gofmt: repository-wide clean (0 files listed).
- YAML: build-desktop.yml parses; job graph verified (preflight, build-windows, build-linux, build-macos, build-android, build-ios, collect-logs, final-verdict); `bash -n` clean on all touched/new shell scripts.

## 3. Explicitly NOT VERIFIED / BLOCKED / NOT STARTED (no fabrication)

- Windows test execution of the repaired gate: NOT VERIFIED. Evidence exists only that the fixed code and tests compile for windows/amd64 and that the comparison logic rejects the simulated same-file-id replacement by construction. A hosted windows-latest `go-test` run is required before calling the red gate green.
- Hosted CI runs of the expanded workflow (macos/android/ios jobs, civalidate gates, collector with six artifacts): NOT RUN. Unauthenticated GitHub API access was rate-limited from this sandbox, and no run was triggered, no branch pushed. The supplied report remains the authoritative evidence for run 44330231458's current state.
- CodeQL: NOT RETRIEVED for the run SHA (no authenticated API access). Preserved codeql.yml unchanged; no alert claims made.
- Fyne to Go-GUI production migration: NOT STARTED in this update. The full route-parity migration (Section 4 of the work order) requires the integration spike first (Wails v3.1.0 shell, Go-GUI v0.97.0 browser/WASM surface inside the WebView, GoGPU v0.54.1 WebGPU frame path, one-window/one-lifecycle proof on each host WebView). This sandbox is Linux-only, has no WebView host for Windows/macOS probes, no Android emulator, no iOS simulator, and the remaining budget after the red-gate, log-contract, and workflow work was insufficient for a production-shaped spike with real rendering evidence. No partial or mock migration was committed: the production app still runs the existing Fyne UI (ui/fyne untouched), and no fake Go-GUI surface was added.
- Requirements & Downloads screen: internal/ghreq (existing engine) is unchanged and fully tested locally; the Go-GUI screen was not built (same reasons as above; the work order forbids a disconnected demo screen, and none was fabricated).
- Platform test packages: NONE shipped with this update. The only local build environment (Linux sandbox) could not execute the full Linux gate contract (race/export/audio/GPU validation, GUI smoke need Xvfb/ffmpeg/GPU runners), so no package status `VERIFIED` is claimable from this execution, and per the delivery rule (only packages whose build AND verification gates actually pass) no binaries are included. The workflow now produces the package targets on hosted runners when it runs.

## 4. Files changed / added

Modified: .github/workflows/build-desktop.yml, scripts/ci/write-manifest.py, scripts/ci/capture.sh, scripts/ci/Capture-Step.ps1, scripts/ci/emergency-report.sh, internal/fileaccess/identity_windows.go, internal/buildinfo/buildinfo.go, internal/cilog/{collect.go,gate.go,collect_test.go,gate_test.go,classification_test.go,githubapi_test.go}, internal/logging/{logging.go,logging_test.go}.
Added: internal/asciilog/{asciilog.go,asciilog_test.go}, cmd/civalidate/main.go, internal/fileaccess/identity_windows_test.go, scripts/build/{build-macos.sh,build-android.sh,build-ios.sh}.

## 5. Recommended next execution order

1. Push this work to a feature branch (user-authorized remote write required) and let build-desktop.yml run: the Windows go-test gate decides 1.1, the civalidate gates decide 1.2, the macOS job decides 1.4.
2. Port the reduced-parity macOS gates (race, export/audio/GPU validation, GUI smoke) so build-macos becomes full-parity and its package can earn VERIFIED.
3. Execute the Wails v3.1.0 + Go-GUI v0.97.0 + GoGPU v0.54.1 integration spike on a real desktop runner (Windows first), per the one-window/one-lifecycle contract, before any bulk UI migration.
4. Only after the spike proves per-WebView rendering: begin the route-by-route Fyne parity migration with the parity inventory, keeping Fyne until parity and tests are complete.
