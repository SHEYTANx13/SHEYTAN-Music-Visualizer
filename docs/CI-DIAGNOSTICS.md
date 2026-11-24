# CI diagnostics — reading a red (or green) run

Status: current as of run 42939743766 repair (2026-11-11).

## The one consolidated report

Every `build` run (the canonical workflow build.yml, renamed from
build-desktop.yml in the run 44449832903 repair) and every independent
`codeql` run uploads exactly ONE user-facing diagnostics artifact, under
the SAME name and the SAME direct-delivery mode (run-45042834040 parity
repair: the CodeQL report used to be a ZIP-wrapped `log.txt` artifact;
both workflows now deliver a plain-text download):

- **`SHEYTAN-Music-Visualizer-all-logs.txt`** — a single plain-text file
  containing ONLY `WARNING|`/`ERROR|` records in the stable schema
  (platform/job/step/gate/command/exit/tool/file/line/column/source/
  hint/message; `hint=` carries the known-root-cause next action when a
  record matches a signature in `internal/cilog/hints.go` — the
  run-45042834042 "faster and more accurate updates" format upgrade),
  strict printable-ASCII + LF byte contract (no tabs, no CR, no BOM, no
  ANSI, no `===`/`---` runs), an EMPTY file on a clean run (zero
  diagnostics is a valid report; the collector exits 0 and the
  collection job stays green — the run-45042834040 empty-report repair),
  and a 256 KiB ceiling enforced fail-closed (records are never
  truncated; an overflow fails the collector). Compiler positions are
  extracted into the structured `file`/`line`/`column` fields with
  `source=EXACT` wherever the raw diagnostic carries them (GitHub
  annotation properties or embedded `path:line:col` positions), so a
  red run names the exact source location instead of prose. Metadata,
  job/step/gate tables and success summaries live in the GitHub Actions
  job summary (written via `ci-log-collector --summary`), never in this
  file; raw job logs never enter it either (the API-recovered logs
  contribute their diagnostic LINES only). The artifact is uploaded
  with `archive: false`: download it and open it in any editor; there
  is no ZIP wrapper.

Temporary per-job log-part artifacts (`*-logs-part-attempt*`) are internal
collector inputs. The collector deletes them only after the consolidated
report upload has succeeded and its artifact identity has been recorded;
if that upload fails, the log parts are preserved and the job fails.

## Application packages (never diagnostics)

Exactly two application artifacts exist, produced only by their platform
jobs:

- `SHEYTAN-Music-Visualizer-windows-x64.zip`
- `SHEYTAN-Music-Visualizer-linux-x64.tar.gz`

Each archive contains exactly one top-level application directory. When a
non-package quality gate failed but the package itself passed its
package-integrity, metadata, runtime and license checks, the single
canonical package is still uploaded and labeled **TEST-ONLY — required
gates failed** in the job summary; the required job and the workflow stay
red. If the package itself fails its checks, no unusable package is
uploaded. There is no raw EXE, no staged-folder archive and no "recovery"
variant — this contract replaced the run-42921967187 recovery path.

## Gate statuses and what they mean

| Status | Meaning | Is it a failure? |
|---|---|---|
| PASS | The gate's command record has `EXIT_CODE: 0 PASS` | no |
| FAIL | The gate's command record has a nonzero exit code | yes |
| SKIPPED | The gate was intentionally not run (`SKIPPED: gate <name> (<reason>)`) — e.g. its input was never produced | not a command failure; the run summary must explain it |
| BLOCKED | A prerequisite is missing (`BLOCKED: gate <name> (<reason>)`) — e.g. no Go toolchain | not a command failure; the run stays red |

Rules the collector enforces (run 42026322748 / 41915116613 contract):

- A gate's status comes ONLY from its own structured `EXIT_CODE:` record.
  Output presence, a created file or a later gate never implies success.
- `SKIPPED` and `BLOCKED` are never converted into `PASS`.
- A required job that ends without ANY gate record is reported as
  **GATES NOT REACHED** — never as a pass.

## Root-cause grouping

Failed gates keep their raw failure in the gate table; the report's
**ROOT-CAUSE GROUPING** section adds the shared causes. Membership requires
the exact failure signature inside the gate's own captured output:

- `GPU palette uniform not reaching shader` — signature
  `--- FAIL: TestPaletteReachesGPUShader` (one defect can fail several
  gates: Windows go-test, Linux go-test, Linux go-test-race,
  Linux gpu-validation).
- `zizmor invocation uses interpreter without installed module` — signature
  `No module named zizmor`.

Failures that match no known signature are listed as UNCLASSIFIED and are
never force-fitted into a group. The report also separates environment
capability limitations (software adapter, no physical audio, no Windows
FFmpeg on hosted runners) from failures.

## Per-gate JSON records

Every executed gate writes `ci-logs/gate-results/<gate>.result.json` with
the exact argv vector, working directory, UTC window, elapsed milliseconds,
exit code, verdict, run/attempt/commit/platform identity and the raw log
path. These records are the machine-readable source the collector
reconciles against the GitHub job conclusions. The preflight
static-workflow gate additionally writes
`gate-results/static-workflow-validation.result.json` with the pinned tool
versions, the documented ignore list and the negative-fixture outcome.

## Secret redaction

The collector redacts the GitHub token (exact-value) plus generic secret
patterns from everything it writes. It never prints runner credential
files or full environment dumps. Gate records carry only allowlisted
environment metadata.

## actionlint MUST run WITH shellcheck (hosted parity)

actionlint integrates shellcheck when a shellcheck binary is on PATH.
Hosted ubuntu runners preinstall shellcheck; most local environments do
not. A local "clean" actionlint run WITHOUT shellcheck is therefore
NOT hosted parity: run 45297707372's preflight failed on a shellcheck
finding (SC2034, an unused variable in codeql.yml's emergency-fallback
step) that no local no-shellcheck run could ever see. Before claiming a
local actionlint pass, install shellcheck (for example the pinned
v0.10.0 release tarball) and rerun:

```bash
actionlint -ignore 'unexpected key "queue" for "concurrency" section'   -ignore 'property "run_started_at" is not defined'
```

with `shellcheck` on PATH, and confirm `shellcheck --version` prints
first. A comment line whose first word after `#` is `shellcheck` is
parsed as a shellcheck DIRECTIVE — never write prose lines in `run:`
blocks that start that way.

The same parity class applies to pwsh-gated fixtures:
`test-ci-plumbing.sh`'s C17-21 group (and `Test-ResolveRepoRoot.ps1`'s
R8 fixture inside `test-package-gates.sh`) execute only when a `pwsh`
binary is on PATH; every session before 2026-11-24 recorded them
`NOT RUN` and the stale assertions inside them (D23b's pre-run-44648357437
`go test $goTags ./...` line, H30's pre-iOS-removal download count)
stayed invisible until a pwsh-capable host ran the suite. Install pwsh 7
before claiming a local plumbing/fixture pass.

Gate-body child safety (run 45347124889): a Capture-Step gate body that
references a parent-only build-script variable dies under
`Set-StrictMode -Version 3.0` with a message carrying no "error"
substring, so the filtered diagnostic log never shows the origin. The
P13 fixture (`scripts/ci/tests/audit-gate-bodies.py`, invoked by
`test-package-gates.sh`) audits every `build-windows.ps1` gate body for
parent-only variable references; extend it whenever a gate gains a new
documented preamble variable.

## Diagnosing a red run, in order

1. Open the run page; check which required job(s) are red.
2. Download `SHEYTAN-Music-Visualizer-all-logs.txt`.
3. Read section A (job/step conclusions) and section B (gate table).
4. Read the ROOT-CAUSE GROUPING; follow the listed occurrences into the
   raw logs (section 4) by the file:line provenance in each gate row.
5. Check ENVIRONMENT CAPABILITY LIMITATIONS before treating a software
   adapter or missing Windows FFmpeg as a defect.
6. If a package is marked TEST-ONLY, the package itself passed all
   package-level checks; only non-package gates failed.
