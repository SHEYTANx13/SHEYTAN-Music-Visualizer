# agent.md — operating manual for AI coding agents

This repository is worked on by AI agents under human direction. This file
is the durable operating manual: it tells a future agent session how to
work here without rediscovering rules, architecture or evidence standards.

## 1. Authority order

1. **The current human prompt has absolute precedence** over this file,
   over `ai-notes.md`, and over anything written in a previous session.
2. `ai-notes.md` — the live record of what was actually done. **Read it
   first, immediately after the current user prompt.** Then read only the
   files relevant to the task; do not reread the whole tree without reason.
3. `roadmap.md` is the phase-status authority. `license.md` is the sole
   licensing authority. `readme.md` is user-facing product truth.
4. Where documents conflict, the authority order above decides, and the
   losing document gets corrected in the same session.

## 2. Phase boundary (do not quietly expand it)

- **Phase 1 (complete):** foundation — identity, logging, buildinfo, minimal
  Fyne window, build scripts, Windows/Linux CI definitions, CodeQL
  definition, log capture/consolidation subsystem, root documentation.
- **Phase 2 (not started):** studio UI/UX, media foundation, playback,
  DSP/song analysis, Song DNA, visualizer engines, GPU backend research and
  proof, export pipeline, presets/projects, hardening.
- Never implement Phase 2 features in a Phase 1 session, and never mark a
  Phase 2 item `DELIVERED`. Status vocabulary is exactly: `PLANNED`,
  `IN PROGRESS`, `DELIVERED`, `BLOCKED`, `DEFERRED`. `DELIVERED` requires
  code + tests/CI evidence.

## 3. Non-negotiable engineering principles

- **Local-first, Go-first.** All first-party application engines are Go.
  No cloud rendering, no remote project state, no mandatory accounts, no
  telemetry, no Python/Node/Electron/Qt/web stack as product runtime.
  Documented OS/driver APIs through maintained Go bindings are allowed.
- **Fyne is the UI shell, never the media renderer.** Phase 2 must keep the
  Fyne UI separate from the deterministic render subsystem so preview and
  export share one render model.
- **GPU acceleration must be proven, not assumed.** Distinguish `detected`,
  `available`, `selected`, `executed`, `verified`. A detected adapter is not
  execution proof. Benchmark against the CPU fallback; never invent
  speedups; never fake GPU telemetry.
- **No fake evidence.** Never state `green`, `fixed`, `verified`, `tested`,
  `complete`, or `working` without matching captured output. Never weaken a
  check to make a run pass; never disable a test to force green.
- **No sleep-based correctness, no blind retries, no retry storms, no
  ignored errors, no silent fallbacks.** Use explicit context cancellation,
  bounded operations, and real exit-code propagation.
- **Preserve user work.** Never run `git reset --hard`, `git clean -fdx`,
  force-push, or destructive cleanup. Modify or add only what the mission
  requires; merge carefully and record decisions.

## 4. Single sources of truth

| Authority | File |
|---|---|
| Product name, trademark, palette, legal footer, app ID | `internal/brand/brand.go` |
| Version / commit / build date | `internal/buildinfo/buildinfo.go` (linker flags; no second version constant) |
| Log directory policy, rotation, redaction rules | `internal/logging/logging.go` |
| Licensing | `license.md` |
| Phase status | `roadmap.md` |
| Agent work record | `ai-notes.md` |
| CI workflow contracts | `.github/workflows/*.yml` |

Docs may display the product name naturally, but code must consume the
brand package — no re-hardcoded identity strings.

## 5. Evidence classes — never inflate one into another

1. **Static inspection** — file exists, YAML parses, actionlint passes.
2. **Local build/test** — commands executed on the actual host with captured
   output and real exit codes.
3. **Hosted CI** — a GitHub Actions run that actually executed.
4. **Physical hardware** — real Windows desktop, real GPU, real display.

A workflow file is not a CI run. A YAML parse is not execution. A local
build is not a physical-hardware result. When hosted CI has not run, report
`HOSTED CI: NOT RUN` (or `NOT OBSERVED` when a run may exist but cannot be
inspected) and move on. Never ask the user for CI results, screenshots, run
IDs, logs, or credentials; never make delivery conditional on hosted CI.

## 6. Required verification ladder

Run every change through the gates in `scripts/build/build-linux.sh`
(Linux/CI) or `scripts/build/build-windows.ps1` (Windows):

1. `gofmt -l .` — no output allowed.
2. `go mod tidy` — no diff in `go.mod`/`go.sum` afterwards.
3. `go test ./...`
4. `go vet ./...`
5. `go build ./...`
6. Build the actual desktop executable for the host (native CGO/Fyne).
7. `--version` and `--self-check` runtime checks.
8. `go test -v ./internal/cilogreport/` (collector fixtures) and
   `./scripts/ci/collector-fixture-e2e.sh` (CLI end-to-end).
9. Workflow validation: `actionlint` + YAML parse +
   `scripts/ci/workflow-contract-test.sh`.
10. `git diff --check`, `git status` — no unrelated changes.
11. Package/zip validation when delivering archives.

**Every command must be captured into a complete log** (stdout AND stderr
from the first byte, real exit code, timestamps, tool versions) using
`scripts/ci/capture.sh` (Bash) or `scripts/ci/Capture-Step.ps1`
(PowerShell). The consolidated evidence transcript lives at
`evidence/phase1-build.log` (later phases: `evidence/phaseN-build.log`).

## 7. Logging hygiene

- Never log secrets, credentials, tokens, audio/media bytes, or unnecessary
  full user paths. Never dump whole environments into logs.
- Application logs go to the app-owned user config directory only — never
  the source tree, never the CWD (enforced by `internal/logging` + tests).
- GitHub secret masking is a last resort, not a design.

## 8. Documentation ownership

The agent owns documentation updates: after real work, update
`ai-notes.md` with what actually happened (commands, exit codes, evidence,
failures, decisions). Do not rewrite completed facts as intentions or vice
versa; do not write filler progress notes; never write a success claim
without an evidence reference. Keep `readme.md` current product truth and
`roadmap.md` the phase authority when scope changes.

## 9. Remote operations are forbidden without explicit authorization

No push, no pull request, no tag, no release, no `workflow_dispatch`, no
remote mutation of any kind unless the human explicitly authorizes that
operation in the current prompt. Read-only inspection of already-existing
hosted runs is allowed when naturally available.

## 10. Handoff contract (end of every session)

`ai-notes.md` must end with, and the next session must expect:

- Environment (OS/toolchain) and repo state (branch, HEAD, dirty/clean).
- What was created/changed and why.
- Exact commands run with exit codes; where the full log lives.
- All failures and whether they were fixed or unresolved.
- Hosted CI / CodeQL status (`NOT RUN` / `NOT OBSERVED` / observed facts).
- Phase 2 starting point: the next narrow mission, unresolved technical
  decisions, and open research gates (see roadmap.md §Research questions).
