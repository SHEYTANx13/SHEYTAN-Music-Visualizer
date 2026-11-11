# Security Policy — SHEYTAN Music Visualizer

Maintained by Parsaetak (licensor and rights holder, see `license.md`).
Last aligned with the repository state: 2026-11 (run-42921967187 repair).

## Reporting a vulnerability

**Do not open a public GitHub issue for a security report.**

Report vulnerabilities privately using one of these channels:

1. **GitHub private vulnerability reporting** (preferred): use the
   repository's *Security → Report a vulnerability* flow. This keeps the
   report, discussion and fix coordination off the public tracker until a
   fix is available.
2. If that flow is unavailable, use the licensor contact points listed in
   `license.md` (§ Governance and contact) and mark the message
   `SECURITY`.

Please include: affected component/paths, a minimal reproduction, the
commit you tested against, and your assessment of impact. You will receive
an acknowledgment and, for accepted reports, a coordinated disclosure
timeline. We credit reporters by name in the changelog unless they ask to
remain anonymous.

## Scope

In scope: the Go desktop application (`cmd/`, `internal/`, `ui/`), the
build/CI machinery (`scripts/`, `.github/`), and the packaging contracts
(`packaging/`). The Windows portable package is a test artifact, not a
release channel (see `docs/ci.md`).

Out of scope: vulnerabilities in third-party dependencies should be
reported upstream; this repository consumes Go modules and pinned GitHub
Actions and tracks advisories through `govulncheck` (a required CI gate)
and Dependabot (`.github/dependabot.yml`).

## Security posture of the CI pipeline

- Every third-party action is pinned to a full 40-character commit SHA;
  mutable tags and branches are never used.
- Jobs run least-privilege: build/preflight jobs hold read-only
  `contents`/`metadata` tokens; only CodeQL analysis holds
  `security-events: write`; checkout uses `persist-credentials: false`.
- Workflow files are linted by `actionlint` and `zizmor` (pinned versions,
  checksum-verified) in the preflight job; low-severity zizmor findings
  fail the run.
- `govulncheck ./...` is a required build gate on Linux.
- Consolidated diagnostics (`log.txt`) are redacted before upload: exact
  token values and generic secret patterns never reach the report.

## Runtime posture of the application

- Local-first: no cloud account, no telemetry upload, no device-inventory
  upload. System profiling and audio/render/export all stay on the machine.
- On Windows, the distributed executable is a GUI-subsystem binary
  (PE subsystem 2) and every child process (FFmpeg/ffprobe) is started
  hidden (`HideWindow` + `CREATE_NO_WINDOW` via `internal/procattr`).
- Logs live in the app-data directory documented in `readme.md` and are
  redacted of secrets and personal media paths by design.
