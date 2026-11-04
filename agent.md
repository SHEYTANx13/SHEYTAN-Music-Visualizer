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
- **Phase 2 (not started)**: studio UI/UX, media import, playback sync,
  DSP analysis, Song DNA, visualizer engines, GPU backend, export
  pipeline, presets/projects, quality/security hardening (see
  `roadmap.md`).
- Never quietly expand Phase 1 scope into Phase 2, and never mark a
  Phase 2 feature `DELIVERED` in Phase 1 documents.

## 3. Engineering invariants

- **Go-first, local-first.** First-party application logic, engines and
  orchestration are Go, running locally. No cloud rendering, no hosted
  project state, no mandatory account, no telemetry. Do not introduce
  Python, Node.js, Electron, React, web UIs, Qt/QML or C++ product
  engines.
- **Fyne owns the desktop UI only** — it must never become the export
  renderer or media pipeline. UI thread ownership: never mutate Fyne
  widgets from goroutines; use Fyne's documented UI-thread mechanism.
- **GPU acceleration is a Phase 2 requirement with a proof gate.** Never
  claim acceleration, never fake GPU telemetry, never label CPU output as
  GPU. Detection ≠ execution.
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
go test ./...                                # all packages pass
go vet ./...                                 # clean
go build ./...                               # packages compile
go build -trimpath -o bin/sheytan-music-visualizer ./cmd/sheytan-music-visualizer
./bin/sheytan-music-visualizer --version     # prints product + build metadata
./bin/sheytan-music-visualizer --self-check  # foundation invariants, exit 0
go test ./internal/cilog/ -run . -count=1    # collector fixtures (included in ./...)
```

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
- Independent gates keep running after a failure; dependent gates are
  recorded `SKIPPED`, never as passed.
- CI jobs upload log parts with `if: always()`; `collect-logs` downloads
  them namespaced (never flattened), builds one deterministic
  consolidated report via `cmd/ci-log-collector`, uploads it even when
  coverage is INCOMPLETE, and only then fails the job (`MISSING LOG
  SOURCE` ≠ `SKIPPED`/`NEVER_STARTED`).

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
