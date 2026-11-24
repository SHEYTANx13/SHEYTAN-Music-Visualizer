# Documentation index — SHEYTAN Music Visualizer

Single canonical index of every maintained document. If a document is
not listed here, it is either packaging/legal boilerplate or it should
not exist. Links are verified by `scripts/ci/tests/test-docs-consistency.sh`.

## Product

| Document | Scope |
| --- | --- |
| [readme.md](../readme.md) | Product scope, features, install/run, platform support, honest hardware/encoder notes, quick user guide. |
| [license.md](license.md) | Canonical licensing document (byte-identical inside every distributed package). |
| [CHANGELOG.md](CHANGELOG.md) | Notable application and engineering changes per session. |
| [roadmap.md](roadmap.md) | Full product direction; active/upcoming work only (stale completed tasks are removed). |
| [ai-notes.md](ai-notes.md) | Historical engineering archive: durable facts, constraints and per-run session records (append-only). |

## Engineering reference (`docs/`)

| Document | Scope |
| --- | --- |
| [docs/architecture.md](architecture.md) | Module map, data flow, frame-thread rules (the go-gui shell: frame-thread-owned control values, uiState/preview-pacer bridges) and the responsive token classes (`ui/gogui/layout.go`). |
| [docs/architecture/queue-orchestration.md](architecture/queue-orchestration.md) | The bounded work scheduler (`internal/jobs`): queue classes A–I, hard lane ceilings, typed admission outcomes, job lifecycle/records, weighted fairness + aging, cancellation/containment/shutdown, job journal and crash reconciliation, studio routing map. |
| [docs/features/requirements-manager.md](features/requirements-manager.md) | GitHub Requirements Manager + Download Center engine (`internal/ghreq`): closed validated requirements catalog, official release discovery, deterministic asset matching, strict origin policy, integrity levels, resumable transfer state machine. |
| [docs/security/file-access-and-privileges.md](security/file-access-and-privileges.md) | Selected-file/folder analysis (`internal/fileaccess`): asInvoker least-privilege model (no elevation, no helper), authorization summary + revocable grants, openat-anchored read-only bounded traversal, sensitive-location default-skip inventory, redacted audit trail. |
| [docs/ci.md](ci.md) | Canonical build/CI reference: toolchain per platform, `goffi_static` tag policy, hands-on build instructions, gate ownership, the `--build-info-json` identity authority, the package-status contract (VERIFIED / TEST_ONLY / UNAVAILABLE) and the final-verdict gate. |
| [docs/CI-DIAGNOSTICS.md](CI-DIAGNOSTICS.md) | The consolidated `SHEYTAN-Music-Visualizer-all-logs.txt` diagnostics artifact, log-part lifecycle and collector contract. |
| [docs/GPU-RENDERING.md](GPU-RENDERING.md) | Renderer selection, honest device classes, the Go↔WGSL uniform contract and the dedicated GPU palette gate. |
| [docs/DESIGN-SYSTEM.md](DESIGN-SYSTEM.md) | Canonical design tokens (`design/tokens.json`), intensity presets, breakpoints and the token-sharing contract between the native and web shells. |
| [docs/PERFORMANCE.md](PERFORMANCE.md) | Performance methodology, current measured baselines and the honesty rules for claims. |
| [docs/performance-baseline.md](performance-baseline.md) | Full measured baseline history with exact commands, host identity and caveats. |
| [docs/TROUBLESHOOTING.md](TROUBLESHOOTING.md) | Canonical user-facing diagnostics + troubleshooting: runtime log, capability states, startup metrics, common failures with actionable fixes. |
| [docs/operations/runtime-diagnostics.md](operations/runtime-diagnostics.md) | Runtime operations evidence (`internal/jobs` + studio Job Center data model): queue/job metrics, pressure adaptation, job journal + crash reconciliation, truthfulness rules. |
| [docs/REQUIREMENTS-TRACEABILITY.md](REQUIREMENTS-TRACEABILITY.md) | Every requirement mapped to implementation, tests, gates, user entry point and verification status. Includes the run-43767556517 R01–R24 zero-omission ledger (the canonical MV-REQUIREMENTS-TRACEABILITY mapping). |
| [docs/MV-CONTENT-INTEGRITY.md](MV-CONTENT-INTEGRITY.md) | Project-content CRUD contract: stable typed IDs, revision/external-modification safety, transaction journal + recovery, Trash/restore/purge, undo/redo, import safety, scenario test matrix (`internal/content`). |

## Repository guides (`.github/`, `packaging/`)

| Document | Scope |
| --- | --- |
| [.github/CONTRIBUTING.md](../.github/CONTRIBUTING.md) | Contribution and evidence rules. |
| [.github/SECURITY.md](../.github/SECURITY.md) | Security policy and reporting. |
| [packaging/FFMPEG-TOOLS-LICENSING.md](../packaging/FFMPEG-TOOLS-LICENSING.md) | Licensing notice for the optional, external FFmpeg tools folder. |
| [packaging/DATA-FOLDERS.txt](../packaging/DATA-FOLDERS.txt) | Data-folder bootstrap marker shipped in packages. |

## CI fixture inventory (test ownership map)

| Fixture file | Fixes |
| --- | --- |
| `scripts/ci/tests/test-package-gates.sh` | P1–P8 packaging/process gates (P6: exact gate-block static contract with negative controls, pipe-safe) + P7 (repo-root fixtures) + P9/P10 (commit-observer fixtures) + P12 (media-tools resolver contract matrix). |
| `scripts/ci/tests/Test-ResolveRepoRoot.ps1` | R1–R9 repository-root resolver and gate-preamble fixtures (R8: SIX-gate exact ordered helper-list contract; R9: resolved-path diagnostic interpolation). |
| `scripts/ci/tests/Test-MediaToolsPinnedResolver.ps1` | Media-tools pinned resolver contract matrix (MX1–MX11): authoritative `[pscustomobject]` schema, Write-Output pollution class, missing fields/invalid digest/missing binaries/spaces+Unicode, env-gated dynamic contract. |
| `scripts/ci/tests/Test-PackageStatusObserver.ps1` | Commit-observer matrix, PowerShell side (correct/missing/mismatch/whitespace/LF-CRLF/empty/malformed/non-zero). |
| `scripts/ci/tests/test-package-status.sh` | The same matrix for the bash/python observer (`scripts/ci/observe_commit.py`). |
| `scripts/ci/tests/Test-GuiProcessCapture.ps1` | Windows GUI-subsystem process capture (P8; Windows host required). |
| `scripts/ci/tests/test-docs-consistency.sh` | Artifact names, doc links, no obsolete tool versions. |
| `scripts/ci/tests/test-ci-plumbing.sh` | Capture/collection plumbing contracts. |
| `scripts/ci/tests/test-codeql-coverage-inventory.sh` | CodeQL extraction coverage proof. |
