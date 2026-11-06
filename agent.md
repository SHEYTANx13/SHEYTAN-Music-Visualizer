# agent.md — Operating Manual for Coding Agents

SHEYTAN Music Visualizer · SHEYTAN™ · Licensor: Parsaetak

This is the durable operating manual for any coding agent (human or AI)
working in this repository. It complements — and never overrides — the
current user prompt.

## 1. Authority and reading order

1. **The current human prompt has absolute precedence** over this document,
   `ai-notes.md`, and any prior note. Repo docs describe standing rules;
   the prompt defines the current mission.
2. After reading the prompt, read **`ai-notes.md` first** — it records the
   actual state of the work, the last session's evidence, and unresolved
   decisions. Then read only the files relevant to the task; avoid
   rereading the whole tree without reason.
3. Authority map (one source of truth each):
   - Licensing/legal: `license.md` (sole authority)
   - User-facing product truth + quickstart: `readme.md`
   - Product plan and phase statuses: `roadmap.md`
   - Durable AI work record: `ai-notes.md`
   - Product name / trademark / palette tokens: `internal/brand/brand.go`
   - Version/build metadata: `internal/buildinfo/buildinfo.go`
   - App log location and policy: `internal/logging/logging.go`
   - CI log capture/consolidation contract: `scripts/ci/` +
     `internal/cilog` + `.github/workflows/build-desktop.yml`
   Never duplicate these authorities; import the Go packages or link the
   documents instead.

## 2. Phase boundary

- **Phase 1 (done)**: foundation — buildable Go + Fyne skeleton with a
  placeholder window, identity/buildinfo/logging authorities, build
  scripts, CI workflows, log capture/consolidation, root documents.
- **Phase 2 (done, 2026-11-04)**: studio slice — studio UI, media import
  (WAV/MP3/FLAC/Ogg), canonical PCM cache, Oto playback with a
  buffering-aware playhead, DSP analysis with tempo confidence, Song DNA,
  four visualizer modes, CPU renderer + wgpu GPU renderer behind one
  contract, MP4 export through 4K via local FFmpeg, projects/presets.
  Evidence in `evidence/phase2-build.log`; honest gaps (hardware GPU,
  Windows runtime, M4A input) in `roadmap.md` and `ai-notes.md`.
- Never mark a capability `DELIVERED` without code, tests and captured
  evidence. Hardware-dependent claims need hardware evidence.

## 3. Engineering invariants

- **Go-first, local-first.** First-party application logic, engines and
  orchestration are Go, running locally. No cloud rendering, no hosted
  project state, no mandatory account, no telemetry. Do not introduce
  Python, Node.js, Electron, React, web UIs, Qt/QML or C++ product
  engines.
- **Fyne owns the desktop UI only** — it must never become the export
  renderer or media pipeline. UI thread ownership: never mutate Fyne
  widgets from goroutines; use Fyne's documented UI-thread mechanism.
- **GPU acceleration has a proof gate.** Never claim acceleration, never
  fake GPU telemetry, never label CPU output as GPU. Detection ≠
  execution ≠ verification. The wgpu renderer reports
  detected/available/selected/executed/verified honestly; software and
  noop adapters are never reported as hardware GPUs.
- **No silent fallbacks.** Errors are surfaced, not swallowed. Bounded
  operations, explicit context cancellation, real exit-code propagation.
  No sleep-based correctness, no blind retry storms, no disabled tests to
  force green, no weakened checks to make a run pass.
- **Preserve user work.** Never run `git reset --hard`, `git clean -fdx`,
  force-push or destructive cleanup. Modify only what the mission
  requires; merge carefully and record decisions.
- **Secrets and media**: never log secrets, credentials, tokens, media
  bytes or audio contents; never put secrets in workflow variables or
  command arguments. GitHub secret masking is not a substitute.

## 4. Evidence classes — never inflate

There are four distinct evidence classes. One never substitutes for
another:

| Class | What it proves | Example |
|---|---|---|
| Static inspection | structure/syntax only | YAML parse, reading code |
| Local compile/test | the code builds and unit tests pass on this host | `go test ./...` |
| Hosted CI | the workflows ran on GitHub runners | an observed Actions run |
| Physical hardware | real user-device behavior | Windows laptop, real GPU |

Status vocabulary: `PLANNED`, `IN PROGRESS`, `DELIVERED`, `BLOCKED`,
`DEFERRED` — and for checks: `PASS`, `FAIL`, `NOT RUN`, `NOT AVAILABLE`,
`BLOCKED`, `NOT OBSERVED`. Never write `green`, `fixed`, `verified`,
`tested`, `complete` or `working` without matching evidence. Never claim
zero security alerts without an observed complete analysis result.

## 5. Required verification ladder

Run the shortest honest ladder that covers changed code, and capture
every command (see §6). On Linux:

```bash
gofmt -l .                                   # no files listed
go mod tidy && go mod tidy -diff             # no diff after final tidy
go mod verify                                # module integrity
go test -tags goffi_static ./...             # all packages pass
go vet -tags goffi_static ./...              # clean
go build -tags goffi_static ./...            # packages compile
go build -trimpath -tags goffi_static -o bin/sheytan-music-visualizer ./cmd/sheytan-music-visualizer
./bin/sheytan-music-visualizer --version     # prints product + build metadata
./bin/sheytan-music-visualizer --self-check  # invariants incl. decoders/DSP/renderer/ffmpeg, exit 0
go test -race -tags goffi_static ./internal/...  # race detector on concurrent cores
```

The `-tags goffi_static` build tag is REQUIRED whenever Fyne (CGO) and the
wgpu renderer (goffi FFI stubs) link into one binary — the Go internal
linker rejects the mixed relocations (`goffi_dlopen` SDYNIMPORT vs CGO);
`goffi_static` selects goffi's static dlopen path. The packaged build
scripts apply it on both Linux and Windows.

Or run the packaged script that does all of the above with full capture:

```bash
scripts/build/build-linux.sh --mode desktop     # or --mode preflight
./scripts/build/build-windows.ps1               # on Windows (PowerShell 7, UCRT64)
```

Workflow validation: `actionlint` (if installed; record NOT AVAILABLE
otherwise) plus a YAML structural parse; a YAML parse is not workflow
execution.

If a hard gate fails: diagnose the first failing command, fix the root
cause, re-run the smallest affected gate, then the complete ladder. Do not
delete the failing test or turn the error into a warning.

## 6. Log capture rules (every build command)

- Every build/verification command must run under the capture helpers
  (`scripts/ci/capture.sh` on Bash, `scripts/ci/Capture-Step.ps1` on
  PowerShell) or an equivalent that:
  - captures stdout AND stderr from the first byte;
  - preserves the command's ORIGINAL exit code through `tee`/`Tee-Object`
    (`PIPESTATUS[0]` / immediate `$LASTEXITCODE`);
  - appends a structured record (JOB, STEP, STARTED, WORKDIR, COMMAND,
    TOOL_CONTEXT, output, `EXIT_CODE: <n> PASS|FAIL`, ENDED);
  - returns the original non-zero exit code (no `|| true`).
  A silent successful command is still evidence: an empty stdout with an
  explicit `EXIT_CODE: 0 PASS` record is a PASS, never missing evidence
  (this is exactly how the CodeQL traced build is recorded).
- Independent gates keep running after a failure; dependent gates are
  recorded `SKIPPED` (intentional not-run) or `BLOCKED: gate <name>
  (<reason>)` (missing prerequisite) — two distinct states, both distinct
  from FAIL, and none of them counts as passed.
- BOOTSTRAP FIRST (run-42026322748 root cause): every evidence-producing CI
  job runs `scripts/ci/bootstrap-logs.sh` (bash) or
  `scripts/ci/Bootstrap-Logs.ps1` (Windows) IMMEDIATELY AFTER CHECKOUT,
  before any step that writes into `ci-logs/`. Never redirect into
  `ci-logs/` before the bootstrap; never assume a previous step created the
  directory. The bootstrap guarantees a non-empty `bootstrap.log`, so each
  job's `if: always()` upload always has real evidence even when a later
  setup step fails (a failed setup additionally writes an explicit BLOCKED
  marker via the `if: failure()` evidence step).
- DAG contract: preflight, build-windows, build-linux and the CodeQL call
  are INDEPENDENT evidence-producing jobs — none declares a success-only
  `needs:` on another, so one failed phase can never suppress the others.
  Only `collect-logs` depends on all of them (`if: always() &&
  !cancelled()`), and a final fail-closed step keeps the run red after the
  report upload. Concurrency is queued, never cancelled
  (`cancel-in-progress: false`), so a newer push cannot destroy an
  in-progress evidence run.
- Artifact lifecycle: per-job log-part artifact names carry the run attempt
  (`*-logs-part-attempt${{ github.run_attempt }}`); `collect-logs` downloads
  them namespaced (never flattened), builds one deterministic consolidated
  report via `cmd/ci-log-collector`, uploads it even when coverage is
  INCOMPLETE, and only then fails the job (`MISSING LOG SOURCE` ≠
  `SKIPPED`/`NEVER_STARTED`; a missing artifact whose raw log the API
  recovered is `RECOVERED_FROM_API`; a still-running job's 404 class is
  `IN_PROGRESS_AT_SNAPSHOT`; a completed job's 404 class is
  `API_LOG_UNAVAILABLE`). Temporary parts are deleted ONLY after the final
  upload succeeded. If the collector binary cannot be built,
  `scripts/ci/emergency-report.sh` still writes and uploads a truthful
  `log.txt`, and the job then fails closed.
- Reading `log.txt`: section 2 is the per-job status table (one row per
  job, workflow + API conclusions, classified log source); section 5.B is
  the authoritative gate table — a gate's status comes ONLY from its own
  `EXIT_CODE` record; `GATES NOT REACHED` for a failed job means its gates
  never executed (never read zero failed gates as a pass); section 8 lists
  never-started jobs, missing/recovered/unavailable log sources and
  reconciled rows; the overall result stays FAIL if any required job did
  not succeed, no matter how complete the logs are.

## 7. Remote-mutation prohibition

No `git push`, pull request, tag, release, workflow dispatch, or any other
remote mutation unless the current human prompt explicitly authorizes it.
Creating and locally validating workflow files is the deliverable; hosted
runs are not required for handoff. When no hosted run exists, report
`HOSTED CI: NOT RUN` (use `NOT OBSERVED` only when a run may exist but
cannot be inspected). Never ask the user for CI results, screenshots, run
IDs, logs or credentials; never make delivery conditional on hosted CI.

## 8. Documentation ownership

- The agent owns keeping the five root documents truthful and consistent
  with the code after each change.
- `ai-notes.md` records **actual work only** — with command/output
  evidence for every claim. Do not rewrite completed facts as intentions
  or vice versa. No filler progress notes.
- Dates use exact ISO format (`2026-11-04`). No invented version numbers,
  release history, or badges (badges only when a real repository URL
  exists).

## 9. Handoff contract (end of every mission)

The next session must be able to continue without rediscovering anything.
Leave behind:

1. What is done, with evidence references (log paths, commands, exit
   codes).
2. What is NOT done — explicitly, including any Phase 2 temptation you
   resisted.
3. Known risks, unresolved architecture choices, and the exact reasons.
4. Tests run, failures and their evidence (first failing command, log
   path, fixed or unresolved).
5. The next narrow mission, in execution order.
