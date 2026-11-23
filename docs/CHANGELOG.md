# Changelog

## Runs 45042834040 + 45042834042 repair — build/CodeQL green, diagnostic format upgrade, UI readiness (2026-11-23)

- **Android universal-APK compile repair (`internal/mediatools/archive.go`)**:
  the untyped `3 << 30` archive-bomb bound overflowed a 32-bit `int` at
  both `fmt.Errorf` call sites, breaking every 32-bit-ABI leg of the
  gomobile universal build (run 45042834042, build-android). The bound is
  now typed `int64` (unchanged value; `ExtractResult.Bytes` was already
  `int64`), verified by cross-compiling `android/arm`, `windows/386` and
  `linux/arm64`.
- **Rotated FFmpeg pin repair (all four pin sites)**: the pinned BtbN
  autobuild release `autobuild-2026-11-08-12-35` was removed upstream, so
  both desktop lanes 404'd `media-tools-pinned-install` and
  `verify-extracted-path-with-spaces` (run 45042834042). The pin moved to
  the newest existing autobuild `2026-11-22-12-36`
  (`ffmpeg-n9.0.2-61-gd59078794d`, new SHA-256 digests) in
  `scripts/ci/ensure-pinned-media-tools.sh`, `scripts/ci/MediaTools-Pinned.ps1`,
  `internal/mediatools/manifest.json` and `packaging/FFMPEG-TOOLS-PROVENANCE.md`;
  the observed binary (`ffmpeg version n9.0.2-61-gd59078794d-20261122`,
  GPL buildconf, no nonfree, libx264/x265 present) was verified from the
  archive itself. A 404 now names the rotation cause and the exact re-pin
  procedure in both provisioners.
- **CodeQL collection repair (run 45042834040)**: a zero-byte consolidated
  report from a collector that exited 0 (a clean run with zero
  diagnostics) was misclassified as a missing report, tripping the
  emergency fallback and failing the collection job; the fallback then
  emitted bogus `workflow conclusion: unknown` records for build.yml jobs
  that do not exist in the codeql workflow (profile-blind
  `emergency-report.sh`). The empty report is now valid exactly when the
  collector completed, and the fallback derives job conclusions from the
  declared manifest jobs (profile-correct) plus explicitly passed
  `--result-*` arguments only.
- **CodeQL artifact parity (run-45042834040 user request)**: the CodeQL
  consolidated report is now named `SHEYTAN-Music-Visualizer-all-logs.txt`
  and uploaded with `archive: false` — a plain-text download, identical to
  the build log delivery, no ZIP wrapper.
- **Diagnostic format upgrade (`internal/cilog`)**: the "better output
  format for faster and more accurate updates" pass — records now carry a
  `hint=` field with the known-root-cause next action (overflow, rotated
  pin, missing APK, missing artifact path, CodeQL no-compile), compiler
  positions from GitHub annotation properties and embedded
  `path:line:col` forms are extracted into structured
  `file`/`line`/`column` fields with `source=EXACT`, and finding records
  no longer invent a misleading `exit=0` (exit is omitted when the
  diagnostic carries no process exit code). Enforced by new contract
  tests; the emergency fallback emits the same schema.
- **Stale CI fixture repair (`scripts/ci/tests/test-ci-plumbing.sh`)**:
  four pre-existing red fixtures repaired to assert the implemented
  contracts (the renamed `ios-compile-smoke` job in the collector
  `needs`/manifest assertions, the record-format emergency report, the
  current `NEVER_STARTED` message, the required build-android manifest
  flag): 20 pass / 0 fail.
- **UI/UX readiness (`ui/gogui`, `internal/presentation`)**: the MRU
  import-path list — the documented manual fallback next to the native
  file dialogs — now persists across restarts through the versioned
  workspace-layout store (additive optional v1 field, sanitized on load
  and save: bounded, deduplicated, hostile entries dropped, never
  fatal). Race-tested.
- **Documentation relocation**: all root `.md` files moved into `docs/`
  (the repository root keeps only `readme.md`), with every packaging,
  workflow, test and comment reference updated; the package still stages
  the canonical `license.md` (now sourced from `docs/license.md`).

## Run 44462492262 work order — Go-Gui + GoGPU + Wails shell architecture (2026-11-19/20)

- **Shell architecture**: one toolkit-free Go application core
  (`internal/*`, unchanged ownership rules) with TWO alternative
  presentation shells and ONE renderer subsystem behind an explicit
  contract. No shell owns another shell's window or widget tree; the
  product never starts both shells in one session.
- **`ui/gogui` + `cmd/app-gogui` (new)**: the native Go-Gui shell on
  Gio (`gioui.org v0.10.3`) — one top-level window, top command bar,
  the eleven canonical workspace tabs (Home, Studio, Worlds,
  Templates, Library, Export, Requirements, Downloads, System Files,
  Settings, Diagnostics), responsive breakpoints from the design
  tokens, the CPU-frame preview bridge over the real
  `internal/render` renderer (bounded buffers, stale-session guard,
  honest backend evidence), command dispatch over the shared command
  IDs, `--smoke N` windowed runtime evidence mode, `--self-check`,
  `--version`, `--build-info-json`.
- **`internal/transport/wails` + `cmd/app-wails` + `frontend/wails/dist`
  (new)**: the Wails v3.1.0 web shell as a clearly isolated, buildable
  vertical slice — a validated DTO-only Go service facade, embedded
  token-driven vanilla HTML/CSS/JS frontend, `/api/*` JSON endpoints
  for every facade capability. Built cgo-free in the
  `purego,sheytan_nogpu` profile for linux/windows/darwin; the profile
  renders through the honest CPU renderer (the gogpu fakecgo stack and
  Wails' purego fakecgo cannot link into one binary — documented in
  `internal/render/gpu_unlinked.go`).
- **`design/tokens.json` + `design` package + `internal/presentation`
  (new)**: the canonical framework-independent token document
  (obsidian/crimson/ember/gold identity, Subtle/Ritual/Infernal
  intensity presets, reduced-motion/reduced-glow, breakpoints,
  accessibility contrast pairs) and the shared shell contracts:
  11 navigation destinations, stable command IDs, typed event
  envelopes with bounded coalescing delivery, versioned DTOs, and the
  versioned workspace-layout store with corrupt-state recovery and
  v0→v1 migration.
- **Fyne removed**: `ui/fyne` deleted after the replacement surface
  reached parity; `go.mod`/`go.sum` carry no Fyne reference;
  `cmd/sheytan-music-visualizer` is now the toolkit-free CLI authority
  (version / build-info-json / self-check / export-cli; the desktop
  shells have their own entry points). `scripts/ci/check-no-fyne.sh`
  enforces the removal in preflight.
- **`internal/perfpolicy` (new)**: the persisted performance-mode
  store moved out of the deleted Fyne package into the toolkit-free
  core (same corrupt-document-is-error semantics).
- **`internal/render` build profiles**: `Preference`, `ParsePreference`
  and helper contracts moved to profile-neutral files; the
  `sheytan_nogpu` profile (`gpu_unlinked.go`) provides the honest
  GPU-unlinked fallback (exact reason in `BackendInfo`, CPU selection,
  nothing reported detected/available/executed).
- **CI capture/sanitizer repair (run 44462492262 failures)**:
  `scripts/ci/capture.sh` no longer uses `sed` at all (BSD-safe):
  the canonical sanitizer is the new `cmd/cisanitize` (Go,
  SanitizeAndRedact with credential/token redaction), with a
  byte-exact pure-stdlib Python fallback
  (`scripts/ci/sanitize_bytes.py --self-test`). Windows producers
  write LF-only logs through `scripts/ci/Write-SanitizedChunk.ps1`;
  `Capture-Step.ps1` and `Invoke-CapturedProcess.ps1` never emit CRLF
  into ci-logs and route every chunk through the sanitizer chain;
  `Bootstrap-Logs.ps1` writes UTF-8-no-BOM LF content. Every
  ci-logs producer is routed at capture time; the byte-level
  validator stays strict. Sanitizer portability regression suite
  (`scripts/ci/tests/test-sanitizer-portable.sh`) runs in preflight
  and on the macOS/iOS jobs (BSD-safe).
- **Workflow**: preflight gains the Fyne-removal gate; all five
  platform jobs build the shells they own (Linux: native shell +
  Wails 3-target; Windows: both shells cgo-free; macOS: native shell
  + darwin Wails); Android gains a recorded gomobile APK attempt
  (no emulator claim — UNAVAILABLE is recorded); iOS gains a recorded
  simulator `.app` attempt with the device path recorded
  `BLOCKED_SIGNING_NOT_CONFIGURED`. Windowed smokes are EXPERIMENTAL,
  recorded, never faked.
- **Honesty notes**: the windowed gio smoke could not be exercised in
  the migration sandbox (gio maps no window under Xvfb there — silent
  block, recorded); compile, self-check, unit, vet and full-suite
  evidence is local and green; hosted-run status remains pending.


## Run 44094081531 work order - GitHub Requirements Manager core (2026-11-18)

- **`internal/ghreq` (new)**: the package-acquisition core for the
  GitHub Requirements Manager and Download Center. One resumable
  download engine (typed job states, bounded queue with typed
  saturation, global/per-host concurrency caps, cooperative pause,
  transient-only bounded retries with jitter, `Retry-After` surfaced
  without auto-retry); strict 206/Content-Range/If-Range resume
  validation with atomic sidecar persistence and safe 200-restart;
  EWMA throughput with a minimum-sample rule before any numeric ETA;
  closed validated requirements catalog derived from the mediatools
  provenance manifest; official GitHub REST discovery (latest stable,
  exact tag, bounded pagination, ETag caching, rate-limit state);
  deterministic platform asset selection (no first-pick, no source
  archives); HTTPS-only allowlisted origin policy with redirect
  validation, credential stripping and private-address dial refusal;
  integrity classification (pinned manifest digest / GitHub release
  digest / transport only) with a hard executable trust gate. 30
  tests, race-clean; wired into the platform go-test gates and the
  Linux race gate. No UI route yet (documented PARTIAL); no Git or
  source-control surface anywhere in the package.
- **Docs**: `docs/features/requirements-manager.md` added and indexed.
- **CI**: `./internal/ghreq/...` added to the `go-test-race` gate in
  `scripts/build/build-linux.sh`.
- **CI repairs (both pre-existing defects found by this session's
  local static runs, not caused by the new package)**:
  `scripts/build/build-linux.sh` package-size-budget restored to the
  90 MiB = 94371840-byte contract value the Windows gate, the script
  header comment and the D35 plumbing assertions own (the gate had
  silently tightened to 70 MiB; `test-ci-plumbing.sh` was red at HEAD
  because of it); `scripts/ci/tests/test-codeql-coverage-inventory.sh`
  current-tree expectation re-anchored to the HEAD inventory plus
  documented per-session additions (the run2-relative expectation had
  gone stale across several BETA commits and was silently red at HEAD).

## Run 44010666873 work order — bounded work orchestration, secure selected-file analysis (2026-11-17)

- **`internal/jobs` (new)**: the single app-owned bounded work
  scheduler. Queue classes A–I (real-time audio and UI deliberately
  NOT scheduled); hard lane ceilings (waiting 12/4/2/2/1/4/1, active
  2/2/1/1/1/1/1, total 26 waiting); typed admission outcomes
  (`accepted`, `coalesced`, `rejected_full`, `rejected_shutdown`,
  `rejected_unauthorized`, `rejected_budget`); job records with stable
  IDs, idempotency + newest-wins coalescing, stage-based monotonic
  progress, attempt/retry contracts (transient-only, bounded) and
  correlation IDs; weighted fair sharing (4/2/1/1/1) with borrowable
  shares and 30 s monotonic aging capped below the audio/UI classes;
  shared CPU-heavy budget `max(1, min(3, GOMAXPROCS/3))` adapting DOWN
  under pressure; per-lane memory ceilings and a global temporary-disk
  budget; panic containment at the worker boundary; bounded
  graceful/cancelling shutdown (never blocks forever on a
  non-cooperative job; stall is observable, never claimed as drained);
  a metadata-only JSONL job journal with atomic compaction and crash
  reconciliation (orphaned running jobs are marked `interrupted`,
  never implicitly successful, never resurrected). 24 tests, clean
  under `-race`, including the mixed-workload stress scenario and
  enqueue-storm accounting.
- **`internal/studio/jobs.go` + studio routing**: media import (decode)
  and DSP/Song-DNA analysis run on `audio_analysis`; first-play
  preparation (device open + canonical cache open) on
  `interactive_io`; offline export on `export` (1+1). All prior
  single-flight semantics and events preserved (existing test suite
  unchanged and green); queue-full paths return actionable messages.
  The play-event sink now runs on ONE bounded dispatcher goroutine
  (buffer 256) with inline delivery on overflow — previously one
  goroutine per event with no bound. `JobsOverview()` /
  `CancelJob()` / `SetSchedulerPressure()` expose the Job Center data
  model and pressure governor. `Close()` drains the scheduler within
  documented bounds.
- **`internal/fileaccess` (new)**: user-authorized, read-only, scoped,
  revocable selected-folder analysis. No elevation anywhere (asInvoker;
  no helper — none is needed to read files the user can already read).
  Authorization summary shown before any walk (roots, purpose, depth,
  count/byte/time budgets); 10-minute confirmation window; root
  identity (device+inode) re-checked at confirmation. Traversal is
  anchored to `os.Root` (openat containment): path traversal and
  symlink/junction escapes refused by the OS; symlinks/FIFOs/devices/
  sockets counted, never opened; paged directory reads; depth, file
  count, per-file, aggregate-byte and time caps with cancellation.
  Well-known sensitive locations (credential stores, SSH/GPG keys,
  wallets, browser logins/cookies, OS hives, dumps, `.env`/secrets,
  key containers) skipped by default; a direct selection requires a
  separate confirmation and still cannot bypass OS controls. Redacted
  bounded audit trail (labels + opaque scope IDs, classified errors,
  never full paths). 11 adversarial tests, clean under `-race`.
- **Docs**: `docs/architecture/queue-orchestration.md`,
  `docs/security/file-access-and-privileges.md`,
  `docs/operations/runtime-diagnostics.md` (canonical, indexed in
  `docs/INDEX.md`; docs-consistency gate green).
- Verification on commit `5b306dd` (Linux, Go 1.27.4,
  `goffi_static`): `go build ./...`, `go vet ./...` and the full test
  suite (`go test -skip '^TestPaletteReachesGPUShader$' ./...`) pass;
  `internal/jobs`, `internal/studio`, `internal/fileaccess` also pass
  under `-race`.

## v3 work order — dependencies, playback confirmation contract, xz matrix (2026-11-16)

- Dependencies (work-order item B, exact versions): `astral-sh/setup-uv`
  v8.2.0 → **v10.3.3** pinned to the verified full commit
  `68559e99cc807c1eb7e9238fd43a15bba460eec2` (tag→commit verified via
  `git ls-remote` during execution; comment updated; unrelated inputs
  untouched); `github.com/ulikunitz/xz` v0.5.15 → **v0.5.17** exactly
  (Go proxy-verified tag `6ead826b…`, 2026-09-19; v0.6.0 alphas
  deliberately not taken). `go mod tidy`/`verify`/`tidy -diff` clean;
  no other dependency churn.
- Playback (item C): the transport can no longer report `Playing`
  merely because a Play call was accepted. New states `Priming`
  (device started, confirmation pending) and `Ended` (natural
  end-of-track, previously a stuck "playing" at the total); promotion
  to `Playing` happens only once the processed-sample counter has
  actually advanced beyond the transition epoch; replay-after-end
  restarts from the start; every player spawn increments a session
  (generation) ID exposed via `ClockInfo.Session`. Structured
  lifecycle events (`play.preroll.started/completed`,
  `play.state.changed`, `play.position.advanced`, `play.eos`,
  `play.stop.requested`, `play.resources.released`, `play.error`) flow
  through `Transport.SetEventSink` → `Studio.SetPlayEventSink` into
  the runtime log; the UI also logs `play.requested` /
  `play.pause.requested`. No media bytes/titles/paths logged. Six new
  contract tests (plus all 17 pre-existing deterministic clock tests,
  none weakened) pass under `-race`; pause-during-priming, EOS,
  seek-at-end, session and event-stream behaviors pinned. Backend
  unchanged: Oto v3 (NOT GStreamer; no migration per rule 27).
- `internal/mediatools/xz_test.go` (new, 16 tests): the v3-required xz
  regression matrix against the real v0.5.17 code — round-trips
  (single/multi-block/empty/8 MiB), concatenated streams, truncation
  (documented lenient block-boundary case: never wrong data, never
  silent partial data), corrupt header/body, dictionary-cap bounds,
  writer Close-error propagation (constructor writes 24 bytes —
  measured), early/double Close, tar.xz extraction integration
  (happy/traversal/corrupt/non-xz), CRC64 end-to-end guarantee.
  Stable across `-count=25`.
- Security documentation (item A): `docs/security/CODEQL-ALERT-REMEDIATION.md`
  records the live OPEN alert inventory as `BLOCKED` with the exact
  access evidence (no credentials in the execution environment;
  unauthenticated API 403; web UI requires an authenticated session)
  — no alert count invented, no dismissal/suppression added,
  `codeql.yml` audited and unmodified. New `docs/DEPENDENCIES.md`,
  `docs/AUDIO-PLAYBACK.md`, `docs/RUNTIME-DIAGNOSTICS.md`;
  traceability extended with rows R25–R29 (work-order item V3-D).
- Verified on Linux x64 (Go 1.27.4, `goffi_static`, CGO): full build,
  `go vet`, `gofmt`, `go test ./...` (25 packages) all green; the
  built executable's `--self-check` passes end-to-end. Windows-native
  and real-audio-device verification remain hosted gates (no
  Windows/audio hardware in this sandbox); Cogent Core POC and the 3D
  vertical slice remain STAGED_WITH_GATE (no display/GPU here) — see
  the traceability ledger.

## Run 43767556517 repair + content-integrity milestone (2026-11-16)

Five confirmed CI defects fixed at root cause (no assertion weakening):

- `internal/mediatools` (Gates B/C): `InspectInstalled` resolved executable
  names from `runtime.GOOS` while the installer used the package-level
  platform pin — on Windows CI a verified install inspected as
  `not-installed`. One platform decision now owns the whole package
  (`paths.go`), with a host-agnostic regression test that reproduces the
  exact `state=not-installed` failure on any runner, plus corrupt-manifest,
  update-available, spaces/Unicode data-root and idempotent-remove tests.
- `scripts/ci/MediaTools-Pinned.ps1` (Gate D): the resolver emitted
  `Write-Output` progress strings, so callers received an `object[]` and
  StrictMode threw on `.Url`/`.BinDir`. It now returns ONE schema-validated
  `[pscustomobject]` (`Version, AssetURL, ArchivePath, SHA256, BinaryDir,
  FFmpegPath, FFprobePath, Platform, ArchiveFormat`), progress goes to the
  host stream, every consumer uses the authoritative fields, and the export
  proofs use the resolver's validated `FFmpegPath`/`FFprobePath`.
- `scripts/ci/tests/test-package-gates.sh` P6 (Gate A): the stale awk range
  (`/^    }$/` never matched) swallowed the rest of build-windows.ps1 and
  false-fired on other gates' legitimate `$LASTEXITCODE` reads; `echo | grep -q`
  pipelines produced broken pipes. P6 now extracts exact gate blocks
  (Capture-Step … Fail-Gate), greps files only, and rejects two negative
  mutants so it cannot rot into always-pass.
- `scripts/ci/tests/Test-ResolveRepoRoot.ps1` R8 (Gate E): updated to the
  live SIX-gate RequireRepoRoot contract with exact ordered helper lists
  (verify-build-metadata, create-portable-zip, verify-extracted-package,
  forbid-bundled-media-tools, media-tools-pinned-install,
  verify-extracted-path-with-spaces — the last with its two-helper list).
- New fixture P12 (`scripts/ci/tests/Test-MediaToolsPinnedResolver.ps1`):
  11-case resolver contract matrix (schema inventory, null/array rejection,
  per-field missing-field diagnostics, digest-format strictness, URL/version
  coherence, missing binaries, spaces+Unicode, env-gated dynamic contract).

Content-integrity milestone (work order §3.12, `internal/content`):

- Stable typed IDs (`prj-…`) stored additively in the existing project
  schema; legacy documents get IDs on first write. Format, extension and
  backward compatibility preserved.
- Optimistic revision tokens plus external-modification (bytes-hash)
  detection; stale or overwritten documents are refused, never
  last-write-wins.
- Transaction journal with deterministic crash recovery for move-class
  operations; stale staging cleanup on open; corrupt journals fail safe.
- Trash/restore with tombstones; explicit purge only for trashed entities;
  external source media is out of reach by construction; render guard.
- Bounded revision-scoped undo/redo with exact-state inverses.
- Duplicate-aware import with canonical-content hashing and collision-safe
  names (spaces/Unicode/hostile characters).
- Studio routing: library-rooted saves go through the validated boundary;
  saves outside keep plain atomic save. Scenario tests A–L all PASS
  (Linux x64, `-count=1` and `-race -count=2`).

# CHANGELOG — SHEYTAN Music Visualizer

All notable changes to the application and its engineering machinery.
The canonical licensing document is `license.md`; this changelog is
informational only.

<!-- END HISTORICAL RECORD -->

## 2026-11-14 — run 43541087097 repair, performance and visualizer quality

- **Windows close regression fixed at the contract level.**
  `TestSingleConfirmationSurfaceForDuplicates` failed on Windows
  (`duration not recorded`) because a sub-resolution teardown measured
  as a zero span on the coarse monotonic clock. `internal/appclose`
  now normalizes every completed shutdown to the documented
  `MinCompletedDuration` floor (no sleeps), records a non-zero
  `CompletedAt`, publishes the immutable result BEFORE `Done` closes,
  and takes its timestamps from an injectable clock — the fixed-clock
  test reproduces the exact Windows condition deterministically on
  every platform (100/100 green, race-clean).
- **Confirmation callbacks are generation-validated and once-only.**
  A stale `proceed()` captured by a cancelled confirmation can never
  close the app after the user chose Stay (generation tokens bumped by
  `CancelClose` and by every new confirmation); double-invoking
  `proceed()` runs the teardown exactly once; close-request admission,
  the work snapshot and the confirming/cancelling transition happen
  under one lock hold, so a concurrent no-work close can no longer
  start teardown while another caller's dialog is opening. New
  concurrency tests: proceed-vs-cancel race (50 iterations),
  simultaneous close surfaces, stale-proceed-after-stay, result
  visibility on every completion path.
- **Three critical audio decode defects fixed (all reproduced by new
  regression tests).** (1) Partial sample frames were silently dropped
  at every 64 KiB chunk boundary whenever the frame size did not
  divide 64 KiB — 3/5/6/7-channel sources (e.g. 5.1 M4A) degraded into
  garbage from the first boundary; a frame-aligning reader now carries
  partial frames between reads. (2) The FLAC bit-depth mapping was
  wrong for every depth outside {16,24,32} — 8-bit FLAC decoded as
  pure noise at the wrong duration; the decoder now converts every
  depth to a fixed 24-bit wire. (3) 8-bit WAV decoded with a +1.0 DC
  offset (unsigned samples read as signed); digital silence imported
  as a full-scale DC signal. Also: WAV format tags are validated
  (compressed ADPCM/µ-law files are rejected instead of decoded as
  PCM noise), a hostile 4 GiB `fmt` chunk can no longer trigger a
  giant allocation, NaN/±Inf float32 samples are sanitized at the
  wire, the 2-hour duration limit is enforced DURING the decode, and a
  cancelled FFmpeg decode now unblocks a stuck read (context watcher
  kills the child, which closes the pipe).
- **Honest audio-device fallback restored.** oto v3's asynchronous
  context error is checked after the ready signal, so a machine with
  no audio device gets the documented silent-preview fallback instead
  of a "real" device that plays nothing.
- **Shutdown teardown is complete and dependency-ordered.**
  `studio.Close()` now cancels the running import, the running
  analysis (previously uncancellable), the export job (reaping the
  FFmpeg child) and the transport — idempotently; the appclose hooks
  read the export job from the synchronized studio service instead of
  the dialog-scoped UI field; closing the export dialog no longer
  abandons a running export (the Cancel control stays reachable and
  the terminal events clear the reference).
- **SpectrumTilt actually renders.** The configured perceptual tilt
  (presets, Song DNA defaults, projects) silently never affected the
  picture because the scene only applied it when the input spectrum
  was shorter than 64 bins; it is now applied on the normal path with
  the same formula in preview and export.
- **Waveform mode gets a real trace.** The analysis envelope
  resolution rose from 2 to 60 points/second (8 → 240 points in the
  ±2 s window; AnalysisVersion bumped to 2).
- **GPU/CPU render parity.** The GPU radial path now (a) consumes the
  same attack/release-smoothed scene bins as the CPU path (previously
  raw bins — visibly jittery), (b) composites the title/subtitle/
  watermark overlays the CPU path always drew (GPU exports no longer
  lack the configured overlays), and (c) export renderer selection
  requires a hardware-class adapter exactly like the UI selection
  (WARP can no longer render exports as "GPU").
- **Renderer hardening + hot-path repair.** An unclamped caller config
  (zero Sensitivity) no longer collapses the audio-reactive targets —
  the config is normalized once, in place, before the scene update;
  re-rendering an identical timestamp is bit-stable on both backends
  (a paused preview no longer animates by itself) and a backward seek
  restarts the deterministic evolution. Overlay text faces are cached
  per pixel size (A/B on this repository's benchmark host: 41 → 6
  allocations and 35,784 → 169 bytes per 1080p frame with overlays,
  −99.5% churn) and the preview loop rotates three frame buffers
  instead of allocating ~2 MiB per frame.
- **Export environment honesty.** A failed `DetectEnv` is never cached
  (the second call used to return an empty environment with a nil
  error, silently skipping the "Export requires FFmpeg" dialog);
  callers receive deep copies so encoder proofing cannot race the
  process cache, and proofs accumulate in the cache across dialogs.
- **CI verdict separation.** `collect-logs` now concludes on
  collection health only (a green collector always means the
  consolidated report WAS uploaded); the new `final-verdict` job fails
  closed when any required gate (preflight, build-windows,
  build-linux, codeql, collect-logs) did not succeed. The Linux
  package observer now receives the real `--build-info-json` exit code
  (it was hardcoded to 0, so the "non-zero exit never verified" rule
  was not enforced on Linux).
- **Docs consolidated.** `docs/BUILDING.md` merged into `docs/ci.md`
  (one canonical build/CI reference, internal duplicate artifact table
  removed) and `docs/diagnostics.md` merged into
  `docs/TROUBLESHOOTING.md`; index and cross-links updated; the
  documentation-consistency gate passes.

## 2026-11-13 — run 43090342995 repair

- **Playback clock contract fixed at its source.** The Windows
  `TestSilentTransportClockAndSeek` failure (`position advanced while
  paused: 580ms → 590ms`) was a real state race: the playhead was
  recomputed from live device counters even when paused, and the silent
  pacer could deliver one in-flight tick after `Pause()` returned. The
  transport now owns one epoch triple (`basePos/baseCount/baseBuf`)
  captured atomically at every transition; a paused/stopped transport
  returns the frozen epoch base and ignores every late tick; seeks are
  frame-aligned; the silent pacer runs one goroutine per play session
  and exits on pause (no idle ticker accumulation). A deterministic
  test suite (`transport_clock_test.go`) drives device consumption
  synchronously — pending-tick, pause-before/after-tick, seek-while-
  paused, 60 repeated cycles, buffered arithmetic, zero-duration,
  end-of-track, close idempotence and goroutine-leak checks — with the
  original test text unchanged.
- **One authoritative shutdown path.** New `internal/appclose` state
  machine behind the titlebar close intercept and a File→Quit menu:
  duplicate close events never duplicate dialogs or teardown; active
  playback/export/import get exactly one confirmation; cancel→flush→
  release stages are bounded and evidence-recorded; the window closes
  explicitly once after cleanup.
- **Responsive studio.** Breakpoint recomposition of shared regions
  (phone <480 px single column with touch-sized action grid; compact
  split; desktop workspace; ultrawide content capped at 1680 px and
  centered), long-title truncation, phone-safe dialogs, and a
  23-viewport layout-contract test suite (no overlap/overflow, actions
  reachable, state preserved across breakpoint transitions).
- **First-frame deferral.** The renderer probe and the audio-device
  open no longer block the first frame (truthful "probing…"/not-yet-
  initialized states instead); the idle preview repaints at ~6 fps.
- **Machine-readable build identity.** `--build-info-json` prints one
  stable JSON line; the package-status observers (PowerShell +
  bash/python) parse typed JSON from the built AND the extracted
  executable — the false `commit_observed: not-observed` class is
  structurally impossible now. Package-status semantics enforced:
  commit-identity failure is UNAVAILABLE (never TEST_ONLY), integrity
  gates run even when unrelated tests already failed (valid TEST_ONLY
  packages are no longer withheld).
- **Root-resolver contract.** R8 now pins the real 4-gate
  `-RequireRepoRoot` contract with exact helper call sites; the
  generated-gate diagnostic prints the actual resolved root (the
  literal `$repoRoot` interpolation bug is fixed); new R9 and P9/P10
  fixture blocks.
- **Workflow hygiene.** The ShellCheck SC2129 finding at
  `build-desktop.yml:702` fixed by grouping the step-summary appends
  (order and append semantics preserved); zizmor runs online with the
  workflow's scoped token; the documented actionlint ignores and the
  negative fixture are unchanged.

## 2026-11-11 — run 42939743766 repair

- **GPU: palettes now provably reach the shader.** The fragment color
  target declared no `WriteMask`; in the pinned gputypes the zero value is
  `ColorWriteMaskNone`, so every fragment write was silently dropped and
  all GPU frames stayed at the clear color (identical for every palette).
  Fixed with an explicit `ColorWriteMaskAll` and a formalized Go↔WGSL
  uniform contract: one `packUniforms` encoder, documented offset table,
  16-byte vec4 color lanes (R,G,B,A), a WGSL peak-highlight lane read and
  five contract tests pinning offsets, lane order, packed bytes, WGSL
  member order and the end-to-end palette difference
  (`TestPaletteReachesGPUShader` now passes).
- **GPU test ownership.** `TestPaletteReachesGPUShader` runs exactly once
  per platform in the dedicated `gpu-validation` gate; the default and
  race suites exclude it by exact name only. No other test was removed.
- **CI security tooling.** zizmor upgraded to pinned 1.32.0 executed
  through ONE isolated `uvx` environment (version-verified before the
  audit; the previous `pip`-install vs `/usr/bin/python3` interpreter
  mismatch cannot recur). actionlint negative fixture added; the `$/`
  self-repository reusable-workflow syntax adopted (zizmor requirement);
  a third narrowly-documented actionlint ignore covers actionlint 1.7.12
  not knowing that syntax yet (upstream issue #711).
- **One canonical build per OS.** The recovery-EXE / staged-package
  artifacts are retired. GitHub Actions produces exactly
  `SHEYTAN-Music-Visualizer-windows-x64.zip`,
  `SHEYTAN-Music-Visualizer-linux-x64.tar.gz` and the plain-text
  `SHEYTAN-Music-Visualizer-all-logs.txt`. Package uploads are driven by
  machine-readable package-status records (VERIFIED / TEST_ONLY /
  UNAVAILABLE) with fail-closed commit verification against `github.sha`;
  valid packages of red runs are labeled TEST-ONLY and the workflow stays
  red.
- **Diagnostics.** Per-gate JSON records gained run/attempt/commit/
  platform/elapsed fields; the consolidated report gained a root-cause
  grouping (exact-signature membership, unclassified failures listed
  honestly) and environment-capability limitation statements; a path-with-
  spaces extracted-package runtime check was added on both platforms.
- **Toolchain.** `go.mod` toolchain bumped go1.27.3 → go1.27.4 to clear
  GO-2026-6859 (net/http MaxBytesReader header race, fixed in 1.27.4);
  `govulncheck` now reports "No vulnerabilities found." on the scanned
  packages. The runners provision the version from `go.mod`
  (`actions/setup-go` `go-version-file`), so no workflow change is needed.
- **Docs.** New `docs/BUILDING.md`, `docs/GPU-RENDERING.md`,
  `docs/CI-DIAGNOSTICS.md`, `docs/PERFORMANCE.md`, `docs/TROUBLESHOOTING.md`
  and `docs/REQUIREMENTS-TRACEABILITY.md`; a deterministic docs-consistency
  gate validates artifact names and local links.


<!-- BEGIN HISTORICAL RECORD — run-42921967187 session (superseded artifact/tool
claims are historical; the current contract is the run-42939743766 entry below) -->

## [Unreleased] — run-42921967187 repair session (2026-11-11)

### Fixed

- **CI: shared Windows gate root cause.** The three gates that failed in
  run 42921967187 (`verify-build-metadata`, `create-portable-zip`,
  `verify-extracted-package` — all on `Cannot bind argument to parameter
  'Path' because it is null`) shared one root cause: the generated gate
  scripts referenced `$env:SHEYTAN_REPO_ROOT`, which was never defined
  for the spawned gate process. Fixed at the workflow/gate-generation
  boundary: job-level `SHEYTAN_REPO_ROOT: ${{ github.workspace }}` in
  `build-desktop.yml`, a parent-side assignment in `build-windows.ps1`,
  a new canonical resolver `scripts/ci/Resolve-RepoRoot.ps1`
  (`SHEYTAN_REPO_ROOT` → standard `GITHUB_WORKSPACE` fallback → precise
  gate-named diagnostic; the current working directory is never used),
  and a generated preamble (`Capture-Step -RequireRepoRoot
  -RepoRootHelpers …`) that validates repository identity and every
  required helper before any package output is touched.
- **CI: generated gate scripts** now use `Set-StrictMode -Version 3.0`
  (deterministic; `Latest` changes meaning as PowerShell evolves).
- **CI: Windows artifact availability.** A guaranteed recovery path runs
  `if: always()` after the Windows build step: an independently
  re-validated EXE is uploaded as `windows-x64-recovery-executable`
  (single unzipped file) and the staged app folder as
  `windows-x64-recovery-staged-package` when the final ZIP gate fails —
  so a valid binary can never again be lost to a packaging-only defect
  (run 42921967187's exact user-facing symptom). Eligibility is decided
  from fresh evidence only and recorded in
  `ci-logs/recovery-eligibility.json`; a recovery upload never turns a
  failed required gate green.
- **Diagnostics: machine-readable gate records.** Every executed gate
  now also writes `ci-logs/gate-results/<gate>.result.json` (command
  vector, working directory, UTC window, exit status, verdict, executed
  flag) from both capture helpers; every non-executed gate has no
  record — absence is not a pass.
- **Process helper observations**: `Invoke-CapturedProcess` records
  Started/Ended UTC, elapsed time, PID and an outcome kind
  (`exited`/`timeout`/`start-failed`/`invalid-argument`), and on timeout
  kills only the exact process tree it created.
- **CodeQL coverage**: Go analysis now sets
  `CODEQL_EXTRACTOR_GO_OPTION_EXTRACT_TESTS=true` (run 42921967187
  scanned 58/102 tracked Go files with test extraction disabled); the
  source inventory step additionally captures the exact tracked-file
  list so remaining coverage deltas can be reconciled file-by-file.
- **Application startup logging**: the first runtime-log record now
  carries version, commit, builder, build date, data mode, data root and
  log directory (run-42921967187 logging contract; previously a Windows
  runtime log could be attributed to nothing).

### Added

- Pinned static workflow validation in preflight: `actionlint` v1.7.12
  (checksum-verified) and `zizmor` 1.16.2, failing on findings at low
  severity or above; two exactly-scoped schema-staleness false positives
  are ignored with comments.
- `.github/dependabot.yml` (Go modules + GitHub Actions, weekly),
  `.github/SECURITY.md` (private vulnerability reporting) and
  `.github/CONTRIBUTING.md`.
- `docs/ci.md` (build requirements, gate evidence contract, repo-root
  contract, artifact/recovery distinction, local reproduction),
  `docs/diagnostics.md` (log layout, startup metrics, capability state
  model) and `docs/architecture.md` (package map, startup sequence,
  threading contracts).
- Regression fixtures: `scripts/ci/tests/Test-ResolveRepoRoot.ps1`
  (10 subprocess scenarios) wired as fixture P7 of
  `scripts/ci/tests/test-package-gates.sh`, plus plumbing fixture D34
  for the gate-result records.
- A build-windows job-summary gate table (PASS/FAIL/BLOCKED/NOT RUN per
  gate) on the run page.

### Changed

- **Performance (measurement-led, output-identical):**
  - DSP analysis allocates 98% fewer objects per minute of audio
    (5 649 → 115 allocs/op; 55.71 → 49.32 MB/op) via a chunked display-
    spectrum backing store; wall time unchanged within noise.
  - CPU renderer: glow bounding-box clipping makes 1080p bar frames
    ~18.7% faster, layered bars ~13% faster, 4K waveform ~6% faster;
    the radial mode (centered disc) is unchanged, confirming no
    regression. Details and method: `docs/performance-baseline.md`.
- readme.md feature table and logs section updated to the new
  documentation set and evidence paths.

### Known platform limitations (unchanged, honestly stated)

- Windows: FFmpeg is NOT bundled. MP4 export stays NOT PROVEN on clean
  Windows installs until a real local FFmpeg is provided and an export
  is ffprobe-validated there; the UI and self-check report the absence
  explicitly.
- Physical-GPU acceleration claims require a VERIFIED state (real
  pipeline + readback); software adapters (e.g. Microsoft Basic Render
  Driver) are always labeled software/virtual.
- Hosted GitHub Actions evidence for this source update is pending a
  user-triggered run on the delivered commit (no push was made by the
  repair session).
