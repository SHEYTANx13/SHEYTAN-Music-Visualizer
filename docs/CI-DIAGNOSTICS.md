# CI diagnostics — reading a red (or green) run

Status: current as of run 42939743766 repair (2026-11-11).

## The one consolidated report

Every `build-desktop` run uploads exactly ONE user-facing diagnostics
artifact:

- **`SHEYTAN-Music-Visualizer-all-logs.txt`** — a single plain-text file,
  uploaded directly (artifact mode `archive: false`). Download it and open
  it in any editor; there is no ZIP wrapper. It contains run/attempt/commit
  metadata, the authoritative job/step conclusions, the per-gate status
  table, the root-cause grouping, the CodeQL evidence and the full raw
  per-job logs with secrets redacted.

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
