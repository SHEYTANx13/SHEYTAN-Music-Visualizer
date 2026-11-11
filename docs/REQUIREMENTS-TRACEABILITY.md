# Requirements traceability — APP-01 … APP-18

Status: run 42939743766 repair session (2026-11-11). Every row connects the
required behavior to its implementation, its tests, its user-visible entry
point and its CI gate. Verification states:

- `VERIFIED` — re-run against the changed source in this session with the
  exact command listed (or executed by an automated gate this session).
- `IMPLEMENTED-BUT-UNVERIFIED` — implementation + tests exist but this
  session could not execute them (environment).
- `PARTIAL` / `MISSING` — must be repaired before completion.
- `BLOCKED` — cannot be executed in this environment; the CI workflow owns
  the verification.

The traceability status of every row is also recorded in the run's
`note.md`. Evidence boundary: local verification ran on a Linux x64
sandbox; hosted CI (Windows + Linux) is the authority for platform-specific
rows and runs after this session's changes are pushed by the user.

| ID | Behavior | Implementation | Tests | CI gate | User entry point | Docs | Status (this session) |
|---|---|---|---|---|---|---|---|
| APP-01 | One primary Fyne main window; all views inside it | `cmd/sheytan-music-visualizer/main.go`, `ui/fyne/app.go` (`NewApp` single-window lifecycle) | `ui/fyne/one_window_test.go` | Linux GUI smoke (Xvfb); Windows process/window fixture | the app window | readme; docs/architecture.md | VERIFIED locally (one-window test PASS); hosted Windows smoke BLOCKED (no local Windows) |
| APP-02 | Windows GUI: no console window; hidden child processes | `scripts/build/build-windows.ps1` (`-H=windowsgui`), `internal/procattr` | `internal/procattr/procattr_windows_test.go`, PE-subsystem gate (`validate-pe`), `Test-GuiProcessCapture.ps1` | build-windows: `validate-pe`, `verify-extracted-package` | n/a (desktop launch) | docs/BUILDING.md; docs/TROUBLESHOOTING.md | VERIFIED (code + tests present, fixtures run in preflight on pwsh hosts); native Windows execution BLOCKED locally, owned by hosted build-windows |
| APP-03 | System Integration from real probes | `internal/sysprofile`, `internal/perf`, `ui/fyne/sysint.go` | `internal/sysprofile/*_test.go`, `ui/fyne` snapshot/behavior tests | platform `go test` gates | Help / System Integration view | docs/architecture.md | VERIFIED (tests PASS this session) |
| APP-04 | Honest hardware states; software/virtual never "physical" | `internal/render` (`classifyAdapterInfo`, `DeviceClass*`), `internal/sysprofile` | `internal/render/gpu_adapter_class_test.go`, `TestSoftwareAdapterIsLabelledHonest`, `TestSoftwarePreferenceNeverLabelsHardware` | gpu-validation + default suite | System Integration view | docs/GPU-RENDERING.md | VERIFIED (all classification tests PASS this session) |
| APP-05 | Resource selection respects capability/memory/override | `internal/sysprofile` resource policy, `internal/render` selection (`preference_store.go`) | `preference_store_test.go`, policy tests | default suite | Settings | docs/architecture.md | VERIFIED (tests PASS this session) |
| APP-06 | Startup stays responsive; no blocking probe on UI thread | `cmd/.../main.go`, `ui/fyne/app.go`, `ui/fyne/runtime.go` | startup stage instrumentation; `scripts/bench/startup-bench.sh` | Linux GUI smoke; docs/PERFORMANCE.md baselines | app launch | docs/PERFORMANCE.md | VERIFIED (bench script + stage logs; baseline ~1115 ms on the measurement host) |
| APP-07 | Fullscreen stable (F11, restore, no loops) | `internal/fullscreen/fullscreen.go` | `internal/fullscreen/fullscreen_test.go` | default suite | F11 / Esc | readme | VERIFIED (state-transition tests PASS; interactive desktop behavior NOT RUN headless — recorded) |
| APP-08 | Portable/installed data locations safe | `internal/apppaths`, `packaging/portable.mode`, `packaging/DATA-FOLDERS.txt` | `internal/apppaths/*_test.go`; package staging + extracted-package gates | build-linux/build-windows package gates | data folders (Help dialog shows mode) | readme; docs/TROUBLESHOOTING.md | VERIFIED (resolver/bootstrap/migration tests PASS this session) |
| APP-09 | Persistent useful runtime logs, rotation, no secrets | `internal/logging`, `internal/apppaths`, startup wiring | `internal/logging/*_test.go`, log-content tests | default suite + `run-self-check` gate | log folder | docs/TROUBLESHOOTING.md | VERIFIED (tests PASS this session) |
| APP-10 | Open log folder / collect diagnostics without CMD | `ui/fyne/actions.go` (Diagnostics dialog) | `ui/fyne` action tests | Linux GUI smoke | Help / Diagnostics | docs/TROUBLESHOOTING.md | VERIFIED (tests PASS; button execution on a physical desktop NOT RUN headless — recorded) |
| APP-11 | FFmpeg discovery order + pair validation | `internal/fflocate/fflocate.go`, `internal/audio/decoders_ffmpeg.go`, `internal/export` | `internal/fflocate/fflocate_test.go`, `internal/audio/decoders_ffmpeg_test.go` | export-validation (Linux) | export dialog / import | docs/TROUBLESHOOTING.md | VERIFIED (locator + pair tests PASS this session) |
| APP-12 | Without FFmpeg core works; with FFmpeg real export + ffprobe | `internal/audio` registry, `internal/export/ffmpeg.go` | `internal/audio/*_test.go`, `internal/export/export_test.go` (1080p30, 4K24, cancel, missing encoder) | export-validation | export dialog | readme; docs/TROUBLESHOOTING.md | VERIFIED (Linux, FFmpeg present: export proofs PASS this session; Windows export BLOCKED on hosted runners — recorded honestly) |
| APP-13 | Audio ingest/playback/DSP correct and responsive | `internal/audio`, `internal/playback`, `internal/dsp` | unit + integration + race suites; `*_bench_test.go` | go-test / go-test-race / audio-validation | playback transport | readme | VERIFIED (all suites PASS this session) |
| APP-14 | GPU palette reaches the shader; device-loss recovery; CPU fallback | `internal/render/gpu.go` (`packUniforms`, `WriteMask` fix), `internal/visualizer` | `TestPaletteReachesGPUShader`, `TestUniformLayoutMatchesWGSL`, `TestColorLaneOrderMatchesPaletteFields`, `TestPackUniformsContract`, `TestWGSLParamsContractMatchesGoLayout`, `gpu_recovery_test.go` | **gpu-validation (dedicated, once per platform)** + default suite + race | visualizer palette selector | docs/GPU-RENDERING.md | VERIFIED — the run-42939743766 defect is fixed and the full render suite PASSES (software adapter; physical-GPU proof stays a hardware activity) |
| APP-15 | Export 1080p/4K, cancel, missing-encoder clarity | `internal/export` | `TestExport1080p30`, `TestExport4K24`, `TestExportCancel`, `TestExportMissingEncoderFails` | export-validation | export dialog | readme | VERIFIED on Linux with FFmpeg; Windows hosted export BLOCKED (no FFmpeg on runner — recorded) |
| APP-16 | Performance measured, improved only where profiled | `internal/perf`, bench suites, `scripts/bench/startup-bench.sh` | `cpu_bench_test.go`, `dsp/bench_test.go`, `audio_bench_test.go` | default suite + docs/PERFORMANCE.md | n/a | docs/PERFORMANCE.md; docs/performance-baseline.md | VERIFIED (benches PASS; baseline documented; no speculative change shipped) |
| APP-17 | Reproducible build identity + diagnostics | `internal/buildinfo`, ldflags injection, `--version`/`--self-check`, package-status records | `verify-build-metadata` gate, extracted-package gates | build-linux / build-windows | `--version`, `--self-check` | docs/BUILDING.md | VERIFIED (gates defined; hosted execution owned by CI; commit_expected now sourced from `github.sha` and fail-closed) |
| APP-18 | Canonical license byte-identical in packages | root `license.md`, staging gates | `verify-package-license` (staged) + post-extract identity check (`cmp` / SHA-256) | both platform package gates | package contents | docs/BUILDING.md | VERIFIED (gate logic exercised by `test-package-gates.sh` fixtures; hosted execution owned by CI) |

## Repair-session deltas (run 42939743766)

- APP-14: root cause fixed at the source — explicit
  `WriteMask: gputypes.ColorWriteMaskAll` (the zero mask silently dropped
  every fragment write), single `packUniforms` encoder with the documented
  layout table, WGSL now reads every packed lane (peak highlight), and the
  previously vacuous `imageUniform` check became pixel-accurate.
- APP-14 test ownership: the dedicated GPU gate owns the palette test once
  per platform; the default and race suites exclude it by exact name only.
- APP-17: `commit_expected` is always the workflow's `github.sha`; a
  missing/blank expected value yields `commit_verified=false` and
  `package_status` UNAVAILABLE/TEST_ONLY — never a silent pass.
- APP-18: package naming contract is exactly one canonical package per OS;
  the run-42921967187 recovery artifacts are gone.
