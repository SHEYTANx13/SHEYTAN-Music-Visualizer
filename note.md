# note.md — run 44094081531 work order execution record

Work order: SHEYTAN MUSIC VISUALIZER BETA — 113-Minute Master
Engineering Workflow (run 44094081531).

Previous session record (run 44010666873, commit 5b306dd) is preserved
in git history and summarized in CHANGELOG.md; the notes below are the
current session's record.

---

## 1. Identity and baseline

- Product identity: **SHEYTAN Music Visualizer BETA** (BETA stage; no
  numbered app version introduced; dependency/toolchain pins kept
  exact and untouched).
- Starting repository: `https://github.com/SHEYTANx13/SHEYTAN-Music-Visualizer`
  (ASCII spelling; the work order's URL contains Unicode lookalike
  characters that do not normalize to a GitHub owner name).
- Starting branch and HEAD: `main` at
  `b4e0e3cb1198114a138ea7fe057585950c841a36` — verified equal to the
  exact commit the target run built.
- Working tree on arrival: clean clone, no staged changes, no
  untracked files. All session changes are local, additive except two
  documented CI-contract repairs. **No remote writes were performed**:
  nothing pushed, no branch created, no PR, no tags, no reruns. Owner
  approval is required before anything is published.

## 2. Live-run evidence (Section 2)

| Item | Value | Source |
| --- | --- | --- |
| Workflow | `build-desktop.yml` | run page HTML |
| Event / branch | `push` on `main` | run page HTML |
| Head SHA | `b4e0e3c` (verified equal to clone HEAD) | run page + local `git rev-parse` |
| Created | 2026-11-17T13:34:30Z | run page HTML |
| Conclusion | **FAILURE** | run page HTML |
| preflight (ubuntu-26.04) | completed, 51s | run page HTML |
| build-windows (windows-latest, x64, UCRT64) | **FAILED**, 3m26s; annotation: "Build and verify Windows x64 desktop app — Process completed with exit code 1" | job page HTML |
| build-linux (ubuntu-26.04, x64) | completed, 3m12s | run page HTML |
| collect-logs | completed, 36s | run page HTML |
| final-verdict | **FAILED**, 4s ("Required gate build-windows did not succeed") | run page HTML |
| Artifacts | `SHEYTAN-Music-Visualizer-all-logs.txt` 762 KB; `SHEYTAN-Music-Visualizer-linux-x64.tar.gz` 23.9 MB; `SHEYTAN-Music-Visualizer-windows-x64.zip` 26.3 MB | run page HTML |

Inference supported by the workflow's own contract (not a substitute
for the log): the Windows ZIP was uploaded, and upload happens only
when `package_status` is VERIFIED or TEST_ONLY — therefore the Windows
build COMPILED and PACKAGED, and the red result is a verification or
test gate, not a compiler failure. The exact failing gate name is
**NOT ACCESSIBLE** in this session: `api.github.com` unauthenticated
quota was exhausted for the entire session (shared egress pool;
`core.remaining` 0 on every attempt), raw job logs and artifact
downloads require credentials that were not available and must never
be solicited. Recorded as BLOCKED, not guessed. No older run's logs
were used as evidence for the current state.

Local reproduction on the exact commit (Linux sandbox, Go 1.27.4,
goffi_static): build, vet, and the FULL non-GUI test suite pass (25
packages, GPU-shader test excluded by the repo's own exact-name
convention). Windows-specific gates are not runnable here (no Windows
OS, no MSYS2): BLOCKED, consistent with the honesty rules. The
Windows go-test gate compiles the new code (GOOS=windows amd64+arm64
build and vet clean for every internal package).

CodeQL: no new alert evidence available (API quota); prior
remediation untouched; the coverage-inventory gate failure discovered
locally was a stale expectation in the test script itself (see 4.4),
not a scan gap — fixed and green.

## 3. Dependency and integration spike (Phase 1)

- Wails: module proxy reports `github.com/wailsapp/wails/v3 v3.1.0`
  as newest stable non-prerelease at run time. Not adopted in this
  session (see 6).
- Go-GUI: `github.com/go-gui-org/go-gui v0.97.0` exists on the proxy
  (newer than the v0.96.0 the work order documented for 2026-11-13).
- GoGPU ecosystem verified on the proxy: `gogpu/gogpu v0.54.1`,
  `gogpu/wgpu v0.35.1` (repo pins v0.34.15), `gogpu/gg v0.52.7`,
  `gogpu/g3d v0.1.12`; `gogpu/ui v0.1.55` confirmed to be a separate
  toolkit from `go-gui-org/go-gui`.
- Integration spike (Wails window + Go-GUI WASM surface + GoGPU
  WebGPU frame): **BLOCKED in this environment** — no GL/EGL/wayland
  or GTK4/WebKitGTK development headers, no root, no Windows/macOS
  hosts. ui/fyne and cmd/sheytan-music-visualizer are environment-
  BLOCKED locally and remain owned by hosted CI. No fabricated window,
  frame, adapter or device evidence was produced. Per the work order,
  bulk UI migration was not started on an unproven path; the delivered
  slice is the UI-independent requirements core (Section 4), which the
  future Go-GUI/Wails surfaces will drive through typed services.

## 4. Implemented changes (all local, on top of b4e0e3c)

### 4.1 `internal/ghreq` (new package) — GitHub Requirements Manager core

- **One download engine** (`ghreq.Downloader`): typed job states
  (queued/resolving/downloading/paused/verifying/completed/failed/
  cancelled/blocked); bounded queue with typed `ErrQueueFull`
  saturation; global (default 2) and per-host (default 2) concurrency
  caps; cooperative chunk-boundary pause with no busy wait and no
  worker slot held while paused; terminal records stay queryable while
  only active jobs consume queue capacity; `Close()` cancels active
  work; transient-only bounded retries (default 3) with exponential
  backoff + jitter; policy failures (unapproved host, downgrade,
  private address, HTML payload, digest mismatch, validator change)
  are permanent and never retried; `Retry-After` surfaced, rate-limit
  responses never auto-retried (no retry storms).
- **Resume correctness**: `<dest>.part` + atomically persisted
  sidecar (temp-file + rename, schema 1); restart only trusts the
  sidecar when URL matches and partial length equals the recorded
  offset; always probes `Range: bytes=<done>-`; accepts `206` only
  with exact `Content-Range` start and matching total (`If-Range`
  carries the stored ETag when known); a `200` answer restarts from
  zero and never appends a full body to a partial file; final digest
  verification before atomic promotion.
- **Honest ETA**: EWMA throughput with a three-sample minimum before
  any numeric ETA; `ETAConfident=false` otherwise; verification time
  excluded from transfer speed.
- **Origin policy**: HTTPS-only allowlist (api.github.com, github.com,
  objects.githubusercontent.com, release-assets.githubusercontent.com,
  codeload.github.com; loopback for tests); 5-hop redirect cap;
  downgrade, userinfo/credentials and token-bearing source URLs
  rejected; Authorization/Cookie stripped on every cross-host
  redirect; dial layer refuses private, loopback (non-test),
  link-local, multicast, TEST-NET and CGNAT addresses.
- **Closed requirements catalog** derived from the repository's
  provenance records (`internal/mediatools` pinned manifest — single
  source of truth; nothing invented): optional GPL FFmpeg media-tool
  packs for windows/amd64 and linux/amd64; other platforms report
  `NOT_AVAILABLE_FOR_THIS_PLATFORM`. Validation rejects duplicate IDs,
  invalid owner/repo (owner: alphanumeric runs with single interior
  hyphens; repo: no doubled/leading/trailing dots), unanchored or
  ambiguous asset selectors, missing licenses, unsafe install paths
  (traversal, drive prefixes, reserved device names), non-canonical
  source URLs, and executables without pinned digest material.
- **Deterministic asset selection**: per-platform anchored include +
  exclude rules; zero matches or ambiguity are errors, never
  first-pick; non-uploaded asset states ignored.
- **Integrity levels**: `PINNED_MANIFEST_DIGEST_VERIFIED` >
  `GITHUB_RELEASE_DIGEST_VERIFIED` (API `digest` field; byte
  consistency with GitHub metadata, NOT publisher identity) >
  `TRANSPORT_ONLY`; mismatches are `FAILED`.
  `EnsureExecutableTrust` hard-blocks executable installation without
  pinned or release-digest evidence; no UI override path exists.
- **GitHub client**: official REST endpoints (releases/latest with a
  defensive draft/prerelease guard, releases/tags/{tag}, bounded
  pagination 5x50 with ID dedup), `Accept: application/vnd.github+json`,
  pinned `X-GitHub-Api-Version`, ETag conditional caching, typed
  rate-limit/not-found/network/malformed errors, `X-RateLimit-*`
  state snapshot. No token requested, accepted or embedded.

### 4.2 CI wiring

- `scripts/build/build-linux.sh`: `./internal/ghreq/...` added to the
  `go-test-race` gate. (Windows ladder has no race gate by design;
  its `go test ./...` covers the package.)

### 4.3 CI contract repairs (both PRE-EXISTING defects found by this
session's local static runs; neither was caused by the new package)

- `scripts/build/build-linux.sh`: package-size-budget restored to the
  90 MiB = 94371840-byte contract value that the Windows gate, the
  script's own header comment and the D35 plumbing assertions own —
  the gate had silently tightened to 70 MiB and
  `test-ci-plumbing.sh` was red at HEAD because of it.
- `scripts/ci/tests/test-codeql-coverage-inventory.sh`: the
  current-tree expectation re-anchored to the HEAD inventory plus
  documented per-session additions; the run2-relative expectation had
  gone stale across several BETA commits and was silently red at HEAD
  (90 production files vs an expected 65).

### 4.4 Docs

- `docs/features/requirements-manager.md` (new; indexed in
  `docs/INDEX.md`): engine contract, resume protocol, origin policy,
  catalog rules, integrity levels, tests and honest BETA limitations.
- `CHANGELOG.md`: run-44094081531 section with the new package, the
  CI wiring and the two pre-existing CI repairs.

### 4.5 Explicitly NOT done (no hollow mocks)

- No Fyne-to-Go-GUI migration in this session: the one-window
  integration spike is environment-blocked (Section 3). Deleting Fyne
  before a proven replacement would violate the work order.
- No Requirements & Downloads UI route: the engine is complete and
  tested; the route is not. Marked PARTIAL honestly; no fake screen.
- No transactional install layer for non-media-tool requirements
  (staging/promotion/rollback beyond the existing hardened mediatools
  install): follow-up milestone, documented.
- No publisher-signature/provenance system beyond pinned digests:
  executables therefore always require pinned material today.
- No Android/iOS artifacts: no mobile toolchains in this environment;
  not fabricated.

## 5. Tests (exact commands and results)

All commands run at the working tree on top of b4e0e3c (host: Linux
x64 sandbox, go1.27.4, gcc 14.2; full output in build-logs.txt):

| Command | Result |
| --- | --- |
| `go build -tags goffi_static ./internal/... ./cmd/ci-log-collector` | PASS |
| `go vet -tags goffi_static ./internal/... ./cmd/ci-log-collector` | PASS (ui/fyne + main cmd are environment-BLOCKED here: no GL/wayland headers, no root) |
| `go test -tags goffi_static -skip '^TestPaletteReachesGPUShader$' -count=1 ./internal/... ./cmd/ci-log-collector` | ALL PASS (25 packages, incl. the new ghreq: 30 tests) |
| `go test -race -count=1 ./internal/jobs/ ./internal/fileaccess/ ./internal/ghreq/` | PASS, race-clean (the hosted race suite additionally covers studio/audio/dsp/export/project/render at this commit — hosted build-linux green) |
| `GOOS=windows GOARCH=amd64 go build ./internal/...` | PASS |
| `GOOS=windows GOARCH=arm64 go build ./internal/ghreq/` + `GOOS=windows go vet` (fileaccess, jobs, studio, mediatools, appclose, ghreq) | PASS |
| `gofmt -l .` | empty (clean) |
| `go mod tidy -diff` | clean |
| `go mod verify` | all modules verified |
| `bash scripts/ci/tests/test-ci-plumbing.sh` | PASS=20 FAIL=0 (3 pwsh fixtures NOT RUN: no pwsh on this host) |
| `bash scripts/ci/tests/test-package-gates.sh` | pass=8 fail=0 notrun=6 (pwsh) |
| `bash scripts/ci/tests/test-package-status.sh` | pass=20 fail=0 |
| `bash scripts/ci/tests/test-verdict-expression.sh` | PASS |
| `bash scripts/ci/tests/test-codeql-coverage-inventory.sh` | PASS |
| `bash scripts/ci/tests/test-docs-consistency.sh` | pass=5 fail=0 |

The studio `-race` build is a local toolchain limitation (goffi dlsym
relocation under -race in this sandbox); the hosted build-linux race
gate at this commit is green evidence for those packages.

## 6. Wails/Go-GUI/GoGPU integration state

- Integration mode: NOT MIGRATED this session. Current production UI
  remains Fyne (ui/fyne) with the GoGPU render stack unchanged; the
  window/lifecycle/render architecture was audited as sound by the
  previous session and left preserved.
- Verified render backend state: unchanged from the repository's own
  honest model (detected/available/selected/executed/verified);
  `TestPaletteReachesGPUShader` owns the GPU proof in CI's dedicated
  gpu-validation gate. This session ran no GPU and no display:
  NO hardware render evidence is claimed.
- Future integration pins (verified on the module proxy this session):
  Wails v3.1.0 stable; go-gui-org/go-gui v0.97.0; gogpu family per
  repo pins (wgpu v0.34.15) or the verified newer compatible line
  after a real spike. The spike checklist (Section 3.4 of the work
  order) stays the hard gate before any bulk migration.

## 7. Audio playback verification state

No audio hardware or output device in this environment. Playback
verification remains owned by the repository's playback/audio test
suites (all pass locally: internal/audio, internal/playback) and by
hosted gates. NO runtime output-device negotiation, sample submission
or position-advance evidence is claimed from this session.

## 8. GitHub Requirements Manager state

- Catalog: derived from the real mediatools provenance manifest; valid
  per `Catalog.Validate()`; tested (`TestEmbeddedCatalogIsValidAndReal`).
- Release discovery: implemented against the official REST contract
  and proven against local HTTP fixtures (latest-stable excluding
  drafts/prereleases, exact-tag resolution, bounded pagination,
  ETag caching, rate-limit handling). NOT exercised against the live
  GitHub API in a download flow this session (the sandbox's api.github.com
  quota was exhausted all session; production paths would hit the real
  API). Recorded honestly as fixture-verified.
- Download/verify: resume, pause, cancel, digest mismatch, HTML
  payload, restart semantics all fixture-tested; no live large-file
  transfer was performed.
- Install: executable trust gate tested; the transactional install
  layer for non-media-tool requirements is the documented follow-up.

## 9. Queue and resource governance results

New download engine: queue saturation visible (typed rejection),
worker caps enforced, pause releases worker slots, bounded retries,
no busy waits, goroutine-safe snapshots; race-clean under `-race`.
The pre-existing `internal/jobs` scheduler (24 tests) is untouched and
green. Full outputs in build-logs.txt.

## 10. Security tests and privilege model

- No app code invokes Git or shells out to source control anywhere in
  the new package (grep-clean; the package has no exec/exec.Command
  usage). No repository-management surface exists in the app.
- Downloaded content is never executed; install paths are validated
  (no traversal, no drive prefixes, no reserved device names); the
  executable trust gate blocks unverified executables; redirect and
  private-address policies are enforced at the dial layer; no
  credentials are requested, stored, logged or forwarded.
- The application privilege model is unchanged: normal-user execution
  (asInvoker); no elevation added anywhere.
- ASCII log policy: build-logs.txt verified byte-for-byte (0 bytes
  outside printable ASCII + CR/LF); source comments ASCII-only.

## 11. Platform build matrix

| Target | Runner | Arch | Package | Signing | Test level |
| --- | --- | --- | --- | --- | --- |
| Linux x64 | hosted (build-linux, run 44094081531) | amd64 | `SHEYTAN-Music-Visualizer-linux-x64.tar.gz` (hosted artifact, 23.9 MB) | n/a | hosted build+tests+package green at b4e0e3c; NOT runtime-tested by this session |
| Windows x64 | hosted (build-windows) | amd64 | `SHEYTAN-Music-Visualizer-windows-x64.zip` (hosted artifact, 26.3 MB) | unsigned | compiled+packaged (TEST_ONLY per workflow contract); verification gate FAILED — exact gate NOT ACCESSIBLE (BLOCKED) |
| Windows x64 (local cross-compile) | this sandbox | amd64/arm64 | none produced | unsigned | build+vet clean for all internal packages; no package produced (would violate the delivery contract) |
| macOS | none available | — | none | — | NOT RUN |
| Android / iOS | none available | — | none | — | NOT RUN |

No local platform test packages are delivered: producing one would
require a passing platform verification gate, which was not available.

## 12. Hosted Actions status

`LOCAL_GREEN_HOSTED_PENDING`. The candidate commit (the local working
tree) was NOT pushed (no remote write authorized), so no hosted run
exists for it. The target run 44094081531 at b4e0e3c is FAILURE
(build-windows; exact failing gate NOT ACCESSIBLE, see Section 2).
`HOSTED_GREEN` is NOT claimed and would be false.

## 13. Known limitations, blockers, unverified claims

- Exact failing Windows gate of run 44094081531: BLOCKED (needs
  `actions:read` credentials or the `SHEYTAN-Music-Visualizer-all-logs.txt`
  artifact to pin; HTML evidence narrows it to a post-package gate).
- Wails/Go-GUI/GoGPU integration spike: BLOCKED locally (no GL/GTK
  dev headers, no root, no macOS/Windows/mobile hosts).
- ui/fyne + cmd/sheytan-music-visualizer local build: BLOCKED
  (environment); owned by hosted CI (green at b4e0e3c).
- Studio `-race` locally: BLOCKED (goffi dlsym relocation under
  -race in this sandbox); hosted race gate green at b4e0e3c.
- Live-API download exercise: NOT RUN (quota); fixture-verified only.
- No hardware GPU/audio/display claims of any kind from this session.
- No mobile claims: Android/iOS NOT RUN.

## 14. Delivered files (download directory)

| File | Size |
| --- | --- |
| `SHEYТAN-Music-Visualizer-BETA-SOURCE.zip` | ~994 KB (266 entries; sha256 recorded at build time in the delivery summary) |
| `note.md` | this file |
| `build-logs.txt` | ~26 KB, ASCII-verified (18 sections, all exit 0) |

The source ZIP contains one top-level directory
(`SHEYTAN-Music-Visualizer-BETA/`), the complete tracked repository
plus this session's new files, excluding `.git`, sandbox artifacts
(`.env`, `download/`), caches and build outputs; verified: single
top-level dir, no traversal entries, no nested archives, required
files present, go.mod readable.

## 15. Suggested next actions for the owner

1. Provide `actions:read` credentials (or the all-logs artifact) to
   pin the exact failing build-windows gate of run 44094081531.
2. Review and commit this session's local changes (git status shows
   exactly: `internal/ghreq/` (new), `docs/features/` (new),
   `CHANGELOG.md`, `docs/INDEX.md`, `scripts/build/build-linux.sh`,
   `scripts/ci/tests/test-codeql-coverage-inventory.sh`), then rerun
   `build-desktop` on GitHub to validate the Windows path end to end.
3. Next milestone: the Wails v3.1.0 + go-gui v0.97.0 one-window spike
   on a real desktop runner (Windows first), then the Requirements &
   Downloads UI route driving `internal/ghreq` through typed services.
