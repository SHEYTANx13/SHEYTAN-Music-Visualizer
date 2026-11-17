# CodeQL Alert Remediation — V5 work order (2026-11-17)

Execution environment: Linux sandbox, no GitHub credentials, no remote
writes (work-order hard rule 1). Target repository:
`SHEYTANx13/SHEYTAN-Music-Visualizer` (default branch `main`). Analyzed
commit at execution start: `17325425d53e3f5a0737c59515f54f305745aeee`
(clone HEAD = baseline green run 43938756323 head = `app(6).log`
runtime build commit; working tree clean at clone).

## Owner-reported alerts (V5 §2.1 inventory input)

The owner supplied two OPEN alert references; live API retrieval of the
complete inventory remains `NOT_RETRIEVED` (the code-scanning REST
endpoint requires an authenticated principal; unauthenticated probe
returned HTTP 401 and the shared egress-IP quota is exhausted — same
blockers as the v3 section below, which retains the full detail):

- **#1** `go/zipslip` (CWE-22, High, security severity 7.5) —
  arbitrary file access during archive extraction,
  `internal/mediatools/archive.go:129` (ZIP loop).
- **#2** `go/zipslip` (CWE-22, High) — same rule,
  `internal/mediatools/archive.go:205` (TAR.XZ loop).

The related `go/unsafe-unzip-symlink` query was also defended against
even though it was not among the two visible alerts (work-order §2.2).

## Root cause (verified from live source)

Both flows passed the untrusted archive entry name through
`safeEntryName` — a string validator built on `filepath.Clean` plus
prefix checks — and then materialized the result with
`filepath.Join(dest, …)` into `os.MkdirAll` / `os.OpenFile` sinks.
String sanitization of this shape is not recognized as a taint barrier
by `go/zipslip`, and more importantly it is weaker than the
operating-system-enforced containment the query help recommends: a
single validation slip (separator ambiguity on Windows, reserved
device aliasing, case-folding collisions) would be a real escape, not
just a static-analysis finding.

## Fix (source-level, single auditable boundary)

`internal/mediatools/archive.go` was rewritten as ONE central,
format-aware extraction boundary used by both formats (no parallel
extractor exists anywhere else in the tree — grep-verified):

1. **Slash-domain validation** — every entry name must be canonical
   (`path.Clean(name) == name`), non-empty, non-dot, and
   `io/fs.ValidPath`; control characters, NUL, backslashes (Windows
   separator ambiguity), colons (drive prefixes + alternate data
   streams), Windows reserved device names (CON/NUL/AUX/PRN/COM1-9/
   LPT1-9/CONIN$/CONOUT$, with extensions), components ending in dot
   or space (Win32 aliasing), and names over 2048 chars are rejected.
   Directory entries tolerate exactly one trailing slash (the archive
   convention); a trailing separator on a file entry is an anomaly and
   rejected.
2. **Sanitizer barrier** — the validated name is converted with
   `filepath.Localize` and gated by `filepath.IsLocal` before any
   filesystem use (the lexical barrier the query help recommends).
3. **os.Root confinement** — the destination (always a fresh, private,
   app-owned staging directory created by the manager) is opened with
   `os.OpenRoot`, and EVERY file/directory creation goes through
   `os.Root.MkdirAll` / `os.Root.OpenFile` with exclusive (O_EXCL)
   creation. Containment is enforced by the OS (openat2
   RESOLVE_BENEATH on Linux; reparse-point refusal on Windows), not by
   string comparison. A symlinked destination is refused; a
   pre-existing outside-pointing symlink inside the destination cannot
   be followed out of the root.
4. **Entry-type allowlist** — only regular files and directories are
   honored; symlinks, hardlinks, devices, FIFOs and unknown types are
   rejected outright (`go/unsafe-unzip-symlink` defense).
5. **Collision rejection before writing** — exact and case-insensitive
   duplicate files, file/directory collisions and parent/child
   conflicts are refused from an in-memory tracker before any byte is
   written.
6. **Mode allowlist** — archive-supplied modes are ignored entirely;
   files are created 0644, and 0755 only for the pinned executable
   naming rule (setuid/setgid/sticky/stray exec bits impossible).
7. **Bomb defenses retained and extended** — entry-count (50 000),
   per-entry (512 MiB) and total (3 GiB) bounds, declared-size
   prechecks, streamed bounded copies with a fixed 1 MiB buffer (never
   an allocation sized from an untrusted declared length), CRC
   verification through the archive readers, truncation errors as hard
   failures, and per-entry byte-bound enforcement during the copy.

The manager's install flow (digest verification BEFORE extraction,
fresh unique staging directory, full pack verification, atomic swap
with rollback, staging cleanup on every failure path) is unchanged and
remains the promotion boundary — extraction never runs over an active
installation.

## Regression tests (attack matrix, V5 §2.4)

`internal/mediatools/archive_test.go` + shared fixtures in
`xz_test.go`, all passing (`go test -tags goffi_static -count=1
./internal/mediatools/`, and `-race -count=3` for the whole package):
traversal (`../`, `a/..`, `sub/../../`), backslash and mixed
separators, absolute paths, drive-qualified, UNC, alternate data
streams, reserved device names, NUL/control characters, empty and dot
entries, double separators, trailing separators on files, trailing
dot/space components, excessive length, exact and case-only duplicate
files (`FFMPEG.EXE` vs `ffmpeg.exe`), directory/file collisions,
parent/child conflicts (`a` then `a/b` and the reverse), symlink /
hardlink / device / FIFO entries, symlink-parent-followed-by-child,
excessive entry count, huge declared size, byte-bound enforcement
during copy, bad CRC (patched central directory), truncated ZIP and
TAR.XZ, pre-existing outside-pointing symlink inside the destination
(sentinel file outside proves nothing escapes), symlinked destination
refused — plus positive fixtures: nested directories, paths with
spaces and non-ASCII, executable-bit policy (0755 pair / 0644 data),
license-document selection, and re-extraction into a used destination
refused. Outside sentinels are verified unchanged after every rejected
extraction.

## Verification states (truthful)

- `LOCAL_CODEQL_CLEAN`: **NOT_RUN/BLOCKED** — the CodeQL CLI bundle
  (~450 MB) could not be downloaded from this sandbox (release-asset
  downloads hang; the GitHub REST quota is exhausted). The remediation
  is verified at source level (the `filepath.IsLocal` barrier plus
  `os.Root`-only sinks remove both dataflows by construction) and by
  the attack matrix above; a local SARIF run remains owner follow-up
  per the commands in the v3 section below.
- Hosted alert state for #1/#2: **PENDING_FRESH_DEFAULT_BRANCH_SCAN** —
  GitHub alone may report the alerts fixed, after the owner pushes
  these changes and a new default-branch CodeQL analysis completes.
  No suppression, dismissal, query narrowing or path exclusion was
  added; `codeql.yml` is untouched by this work order.
- Live OPEN inventory beyond the two owner-supplied alerts:
  `NOT_RETRIEVED` (authentication blocker, unchanged from v3).

---

# Prior record — 2026-11-16 v3 work order

# CodeQL Alert Remediation — 2026-11-16 v3 work order

Execution environment: Linux sandbox (no GitHub credentials available to
the execution agent). Target repository: `SHEYTANx13/SHEYTAN-Music-Visualizer`
(default branch `main`). Analyzed commit at execution start:
`9f7e16d63df26c56c88bbd73dcae503383cacaa5` (identical to the runtime
evidence commit of `app(5).log`; working tree clean at clone).

## Live OPEN alert inventory: `BLOCKED`

The v3 work order (non-negotiable 22) requires the complete current OPEN
CodeQL alert inventory from the repository's live default branch, via

```
GET /repos/{owner}/{repo}/code-scanning/alerts?state=open&per_page=100
```

This execution could not retrieve that inventory. Exact blockers,
recorded per the work order's reporting states:

1. **No GitHub authentication is available to this execution.** The
   environment supplies no token, and this work order forbids inventing
   credentials. The code-scanning alerts REST endpoint requires an
   authenticated principal; unauthenticated callers cannot enumerate
   alerts even for public repositories.
2. **Unauthenticated REST attempts fail.** Direct probe of
   `https://api.github.com/repos/SHEYTANx13/SHEYTAN-Music-Visualizer/code-scanning/alerts?state=open&per_page=100`
   returned HTTP 403:
   `API rate limit exceeded for <egress-IP>. (But here's the good news:
   Authenticated requests get a higher rate limit...)`.
   The egress IP's shared unauthenticated quota (60/h) was exhausted at
   probe time (`core.remaining: 0`) and remains unreliable in this
   environment because it is shared by many concurrent callers.
3. **The web UI route also requires authentication.** A fetch of
   `https://github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/security/code-scanning`
   returns the anonymous shell ("You can't perform that action at this
   time") with no alert list; the alert table is rendered only for an
   authenticated, JavaScript-capable session.

Consequently **no alert number, rule ID, path/line, severity, taint
path, or fix status is reported by this document, and none may be
invented.** Per the work order's reporting contract the correct state
for the inventory step is:

- Inventory of live OPEN alerts: `BLOCKED` (access/technical blocker
  documented above).
- `LOCAL_CODEQL_CLEAN`: **not claimed** — no local CodeQL database
  creation/analysis run was completed inside this execution window
  (the sandbox's remaining time budget was allocated to the P0
  playback repair, the two mandated dependency upgrades, regression
  fixes and the delivery contract; a rushed partial database build
  would have produced no verifiable SARIF).
- `HOSTED_CODEQL_FIXED`: **not claimed** — no remote write, dispatch,
  or fresh hosted scan is authorized or possible from this task.
- `HOSTED_PENDING`: **not applicable** — no alert was identified, so
  none can carry a pending fix.

## What was verified about CodeQL configuration instead

The repository's CodeQL workflow (`.github/workflows/codeql.yml`) was
inspected at the analyzed commit. Verified facts:

- It is an independent top-level workflow: `push`/`pull_request` to
  `[main]`, weekly schedule (Monday 03:17 UTC), and
  `workflow_dispatch`; it is NOT called from or gated by
  `build-desktop.yml` (non-negotiable 13 holds).
- Trigger byte-level verification: the `branches:` filters read
  `[main]` in both `on.push` and `on.pull_request` (verified with
  `od -c`; an initial rendering artifact suggested `ain]`, which the
  raw bytes disprove).
- Go analysis uses `build-mode: manual` with a traced build
  `go build -tags goffi_static ./...`, `CGO_ENABLED=1`, `CC=gcc`, and
  `CODEQL_EXTRACTOR_GO_OPTION_EXTRACT_TESTS=true` (test files
  extracted; the coverage regression of run 42921967187 is guarded
  against and the live source inventory is logged each run).
- A separate actions-language analysis job (build-mode none) covers
  workflow definitions.
- Least privilege: workflow default `contents: read`; analyze jobs hold
  `contents: read`, `security-events: write` (+`actions: read`); the
  standalone collector is the only `actions: write` holder and uses it
  only to delete its own temporary artifacts.
- No alert dismissal, suppression, query-pack reduction, or path
  exclusion was added by this work order. This execution did not
  modify `codeql.yml` at all.

## Required follow-up (owner, with credentials)

1. Run the paginated alert query above with an authenticated token
   (security_events scope) and record every OPEN alert (number, rule,
   path/line, severity, snippet) into this file.
2. For each alert: root-cause fix in code + regression test, then a
   fresh default-branch scan; only GitHub's own fixed/closed state may
   be reported as fixed.
3. If a local CodeQL pass is desired before pushing: download the
   CodeQL CLI bundle, `database create` with
   `--command 'go build -tags goffi_static ./...'` under
   `CGO_ENABLED=1 CC=gcc` plus the Fyne pkg-config prerequisites, and
   analyze with the same suites configured in `codeql.yml`.

Integrity statement: this document deliberately reports `BLOCKED`
rather than any invented count, per work-order rules 3, 22, 23 and the
V3 success contract.
