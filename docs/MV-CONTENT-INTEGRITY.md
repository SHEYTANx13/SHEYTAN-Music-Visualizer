# MV-CONTENT-INTEGRITY — project content CRUD, transactions and recovery

Canonical contract for persisted project content (run-43767556517
milestone §3.12). Implementation: `internal/content` (the validated,
UI-independent service boundary) + `internal/studio` wiring + the
additive schema fields in `internal/project`. Tests:
`internal/content/content_test.go` (scenarios A–L),
`internal/studio/studio_content_test.go`, `internal/project/project_test.go`.

## What content exists today (entity inventory)

| Entity | Persisted where | Identity | Created by | Edited by | Removed by |
|---|---|---|---|---|---|
| Project document (`.sheytan.json`, schema v1) | the content library root (`apppaths.Layout.Projects`) or any user-chosen path | `contentId` (typed `prj-…`) stored INSIDE the document | Import-audio flow → `Studio.SaveProject` → `content.Library.Create/Update` | `Studio.SaveProject` → `Library.Update` (revision-checked) | `Library.SoftDelete` (Trash) → explicit `Library.Purge` |
| Source media reference (`MediaRef`) | inside the project document | part of the owning project | import | relink only | never (external file is never owned) |
| Built-in presets | compiled into `internal/project` | constant names | n/a | n/a (read-only) | n/a |

Future entity types (scenes, timelines, mesh jobs, AI presets) do NOT
exist yet; they are staged in `docs/REQUIREMENTS-TRACEABILITY.md`
(R01–R24 ledger) and get NO fake UI until their domain lands.

## Stable identity

- Every library entity carries `contentId` = `prj-` + 26 lowercase
  crockford-base32 characters (`content.ValidID`), minted from the
  crypto random source. It is immutable across rename, move, edit,
  duplicate display names and trash/restore.
- Legacy documents (pre-milestone) have no id; `content.Open` assigns
  one on first observation (migration-on-write; bytes otherwise
  preserved; old builds ignore the additive JSON fields).
- IDs are NEVER derived from list indexes, display names, paths or
  widget state. UI selection MUST carry the ID (contract for the UI
  layer; enforced by the service API shape).

## Single mutation boundary

All persisted mutations of library-managed documents go through
`content.Library`:

- `Create(CreateRequest)`, `Get(id)`, `List()`, `ListTrashed()`,
- `Update(id, expectedRev, mut)`, `Rename(id, expectedRev, newName)`,
- `SoftDelete(id, expectedRev, reason)`, `Restore(id)`,
- `Purge(id)` (explicit, trashed entities only),
- `ImportFile(path)` (duplicate-aware, new identity),
- `Undo()/Redo()` (bounded, revision-scoped command inverses).

The studio routes a `SaveProject` call through the service exactly when
the target directory is the library root; saves outside the root keep
the plain atomic-save behavior (`internal/studio/studio_content_test.go`
proves both paths). Nothing else writes managed documents.

## Revision and external-modification safety

- `revision` (uint64, inside the document) bumps by exactly one on
  every successful mutation. A command carrying a stale expected
  revision fails with `ErrRevisionConflict` — never last-write-wins.
- The library also records the SHA-256 of the document bytes it last
  observed and verifies them BEFORE accepting a mutation
  (`ErrExternalModification`). A file rewritten behind the library's
  back is refused, not silently adopted.
- Identity and revision tokens are tampering-guarded: a mutator that
  changes `contentId` or `revision` is refused.

## Transactions, durability and recovery

- Single-document mutations use `project.Save` (temp file → fsync →
  atomic rename in the same directory). A failure leaves the previous
  committed document intact and removes only its own staging file.
- Move-class transactions (rename, trash, restore, undo/redo moves)
  write a `.journal-<op>-<n>.json` record BEFORE the rename and remove
  it after. `content.Open` replays left-behind journals
  deterministically: destination present ⇒ committed; only source
  present ⇒ never executed (rollback is a no-op); both ⇒ the source is
  preserved under a "(recovered)" name, no bytes destroyed; neither ⇒
  no-op. A corrupt journal fails safe with
  "manual inspection required" — nothing is changed.
- Stale `project.Save` staging files are cleaned by `Open`.
- "Saved" is reported (event `EventProjectSaved`) only after the
  durable commit returns success; every failure emits
  `EventProjectFailed` with the actionable reason and keeps the
  in-memory draft.

## Delete / restore / purge policy

- `Remove from library` is SOFT: the document moves to
  `<root>/.trash/` with a tombstone (`.meta.json`: id, UTC time,
  reason). Identity and revision history are preserved.
- Deleting a project NEVER touches the external source media — the
  trash move is confined to the managed root by construction
  (proven by `TestDeleteSafetyAndPurgePolicy`).
- An active render can block removal via `Library.RenderGuard`
  (host-registered check); renders also operate on their own snapshot.
- `Purge` is a separate explicit command for already-trashed entities;
  it refuses live entities (`ErrNotTrashed`) and removes only the
  trashed document + tombstone inside `.trash/`.
- Undo records survive through trash/restore so "undo of a delete"
  restores the exact entity (id, name, revision).

## Undo / redo

- Bounded stack (`maxUndoRecords`, oldest dropped); new commands clear
  the redo branch.
- Every record binds the exact inverse operation with the entity id,
  exact paths and exact serialized snapshots; a record whose entity was
  purged is refused with an explanation and dropped — no guessing, no
  corruption.
- Re-applied snapshots bump the revision (monotonic), so concurrent
  editors keep getting honest conflicts.

## Import safety

- Malformed/non-project files are refused (`project.Validate`).
- Byte-identical or semantically identical documents (canonical hash
  with id/revision stripped) are detected: live duplicate ⇒
  `ErrDuplicateContent` naming the existing id; identical trashed
  entity ⇒ same error pointing at restore.
- Distinct documents import as NEW entities with collision-safe names
  ("Name (2)…"), preserving display name and metadata; the source file
  is never modified.
- Names with spaces, Unicode and filesystem-hostile characters map to
  stable managed files (deterministic character mapping + "(n)" suffix
  on collision; trailing dots/spaces trimmed for Windows).

## Verification matrix (scenario lettering of the work order)

| Scenario | Proof |
|---|---|
| A create | `TestCreateAssignsUniqueTypedIDsAndPersists`, `TestCreateRejectsInvalidNames` |
| B read/list | `TestListIsDeterministicAndIdentityStable` |
| C update | `TestUpdateSemantics`, `TestExternalModificationDetected` |
| D delete | `TestDeleteSafetyAndPurgePolicy`, `TestRenderGuardBlocksDelete` |
| E restore | `TestRestorePreservesIdentityAndHistory` |
| F undo/redo | `TestUndoRedoSequence`, `TestUndoStackBoundedAndRedoCleared` |
| G persistence | `TestPersistenceAcrossReopen` |
| H recovery | `TestJournalRecovery` (committed/rolled-back/stale-staging/corrupt/duplicate-id) |
| I import | `TestImportSafety`, `TestNameHandling` |
| J concurrency | `TestConcurrencyRefusals` |
| K cross-platform | pure Go (`os/filepath` only); runs unchanged on Windows and Linux runners; local evidence Linux x64 |
| L UI targeting | service-level: stable-ID API shape; `TestSelectionTargetsStableIDs`; studio wiring `TestStudioSaveRoutesThroughContentService`, `TestStudioSaveOutsideLibraryStaysLegacy` |

All rows PASS locally (Linux x64, `-count=1` and `-race -count=2`;
commands and exit codes in `ci-logs/`). Windows rerun is owned by the
hosted `build-windows` job (same pure-Go tests).

## Known staged gaps (explicit, not hidden)

- Library browser UI (list/trash/restore screens) does not exist yet —
  the service and studio wiring are ready; the visible CRUD surface is
  the next UI milestone (traceability R24/STAGED_WITH_GATE rows).
- Autosave/crash recovery snapshots, schema migrations beyond the
  additive id/revision fields, and ZIP project archives are staged.
- Concurrent multi-process library access: single-writer per process is
  guaranteed; two app instances on one root are protected by the
  external-modification detection (hash mismatch ⇒ refusal), not by
  file locks yet.
