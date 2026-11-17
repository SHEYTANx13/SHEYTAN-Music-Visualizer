# File access and privileges — selected-file analysis

Canonical reference for `internal/fileaccess`, introduced for run
44010666873. Companion doc: `docs/security/CODEQL-ALERT-REMEDIATION.md`
(historical alert context).

## Privilege model: least privilege, no elevation

- The application runs **asInvoker** on Windows. Nothing in the build
  or packaging adds `requireAdministrator`, a UAC manifest, a service
  or a scheduled task. Microsoft's guidance is explicit that desktop
  apps should run with standard user privileges and elevate only
  narrowly for operations that genuinely require protected locations;
  nothing in this product requires such an operation.
- **No privileged helper exists**, and none was added "for
  completeness": selected-file analysis reads files the user can
  already read with standard permissions. If a future feature truly
  needs elevation, it must be a separate, minimal, versioned helper
  with a typed IPC contract, an allowlisted operation set and an
  explicit UAC consent screen — added only with evidence.
- Denied access is a normal typed outcome. The app never takes
  ownership, changes ACLs, enables privileges or retries as
  administrator behind the user's back.

## Authorization model

Access exists ONLY for roots the user explicitly selected through a
picker. The flow is:

1. `PrepareAuthorization(root, purpose, project, actor, limits)`
   validates the root and produces an `AuthorizationRequest` whose
   `SummaryLines()` the UI must show: exact root label, purpose,
   read-only scope, file types, recursion depth, maximum file count,
   total/per-file read volume, time budget, and the sensitive-skip
   policy.
2. The user confirms.
3. `ConfirmAuthorization(req)` creates the revocable `Grant` — only
   within a 10-minute confirmation window, and only if the root's
   filesystem identity (device + inode on Unix) still matches the one
   the user saw. A swapped directory requires fresh confirmation.
4. `Grant.Analyze(ctx, visit)` performs the bounded, read-only walk.
5. `Revoke` / `RevokeAll` (project close, user revocation, app exit)
   close the anchored root handle; later walks fail with
   `ErrNotAuthorized`.

Grants never extend to sibling or parent directories.

## Safe traversal

- Every file open is anchored to an `os.Root` handle opened at
  authorization time (openat containment): path traversal and
  symlink/junction escapes are refused by the OS, not by string
  prefix checks, and TOCTOU swaps land on a different inode.
- Symlinks are never followed, in either direction. FIFOs, devices,
  sockets and other non-regular entries are counted and never opened
  (opening a FIFO read-only would block until a writer appears).
- Bounds are enforced by the walker: recursion depth, file count,
  per-file read cap, aggregate byte cap, wall-clock budget and context
  cancellation. Directory reads are paged so a huge directory cannot
  allocate without bound.
- The only derived data are size, timestamps and a capped SHA-256
  fingerprint of the first bytes. Selected content is untrusted data:
  never executed, never parsed as instructions, never uploaded.

## Sensitive-data exclusions (default skip)

Well-known secret-bearing locations are skipped during folder walks
even when the user selected the parent folder: SSH/GPG/AWS/Azure/
gcloud/kube/Docker credential directories, browser profile stores
(Login Data, cookies, key4.db...), password-manager databases, wallet
files and seeds, private-key containers (.pem, .key, .p12, .pfx,
.kdbx, .ppk, .jks), OS hives and memory dumps, `.env`/secrets files.
A deliberate DIRECT selection of a sensitive file requires a separate
explicit confirmation (`AnalyzeFile(..., confirmedSensitive=true)`)
and still cannot bypass OS access controls — the default action is
always skip. The inventory lives in `internal/fileaccess/sensitive.go`.

## Audit trail

`Manager.AuditRecords/AuditJSON` exposes a bounded ring of redacted
records: actor, project, opaque scope ID, root LABEL (never the full
path), operation, outcome, aggregate counts, duration, correlation ID.
Walk error summaries are classified ("permission denied"), never echoed
with raw filesystem paths. No content, no secret-bearing paths, no
telemetry, no network transfer — analysis is local-only.

## Status and verification

Implemented and tested in this milestone: authorization summary +
confirmation window, root-identity reconfirmation, openat-anchored
read-only traversal, symlink/FIFO/device refusal, depth/count/bytes/
time caps, cancellation, sensitive default-skip with direct-selection
confirmation path, revocation, audit redaction (adversarial tests in
`internal/fileaccess/fileaccess_test.go`, clean under `-race`).

Future work (explicitly not claimed): the in-app folder picker and
analysis UI panel; archive-entry handling through the hardened
extraction rules; a sandboxed parser helper for crash-prone formats.
