# Documentation index — SHEYTAN Music Visualizer

Single canonical index of every maintained document. If a document is
not listed here, it is either packaging/legal boilerplate or it should
not exist. Links are verified by `scripts/ci/tests/test-docs-consistency.sh`.

## Product

| Document | Scope |
| --- | --- |
| [readme.md](../readme.md) | Product scope, features, install/run, platform support, honest hardware/encoder notes, quick user guide. |
| [license.md](../license.md) | Canonical licensing document (byte-identical inside every distributed package). |
| [CHANGELOG.md](../CHANGELOG.md) | Notable application and engineering changes per session. |
| [roadmap.md](../roadmap.md) | Full product direction; active/upcoming work only (stale completed tasks are removed). |
| [ai-notes.md](../ai-notes.md) | Current engineering facts, constraints and session history. |

## Engineering reference (`docs/`)

| Document | Scope |
| --- | --- |
| [docs/architecture.md](architecture.md) | Module map, data flow, threading rules (Fyne event loop, `fyne.Do`), the close/shutdown state machine (`internal/appclose`) and the responsive root (`ui/fyne/responsive.go`). |
| [docs/ci.md](ci.md) | Canonical build/CI reference: toolchain per platform, `goffi_static` tag policy, every build script gate, the `--build-info-json` identity authority and the package-status contract (VERIFIED / TEST_ONLY / UNAVAILABLE). |
| [docs/CI-DIAGNOSTICS.md](CI-DIAGNOSTICS.md) | The consolidated `SHEYTAN-Music-Visualizer-all-logs.txt` diagnostics artifact, log-part lifecycle and collector contract. |
| [docs/GPU-RENDERING.md](GPU-RENDERING.md) | Renderer selection, honest device classes, the Go↔WGSL uniform contract and the dedicated GPU palette gate. |
| [docs/PERFORMANCE.md](PERFORMANCE.md) | Performance methodology, current measured baselines and the honesty rules for claims. |
| [docs/performance-baseline.md](performance-baseline.md) | Full measured baseline history with exact commands, host identity and caveats. |
| [docs/TROUBLESHOOTING.md](TROUBLESHOOTING.md) | Runtime log locations, data folders, common failures with actionable fixes. |
| [docs/diagnostics.md](diagnostics.md) | What the app reports about itself (runtime log, self-check, capability states) and how to read it. |
| [docs/BUILDING.md](BUILDING.md) | Hands-on build instructions for Windows (UCRT64) and Linux. |
| [docs/REQUIREMENTS-TRACEABILITY.md](REQUIREMENTS-TRACEABILITY.md) | Every requirement mapped to implementation, tests, gates, user entry point and verification status. |

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
| `scripts/ci/tests/test-package-gates.sh` | P1–P8 packaging/process gates + P7 (repo-root fixtures) + P9/P10 (commit-observer fixtures). |
| `scripts/ci/tests/Test-ResolveRepoRoot.ps1` | R1–R9 repository-root resolver and gate-preamble fixtures (R9: resolved-path diagnostic interpolation). |
| `scripts/ci/tests/Test-PackageStatusObserver.ps1` | Commit-observer matrix, PowerShell side (correct/missing/mismatch/whitespace/LF-CRLF/empty/malformed/non-zero). |
| `scripts/ci/tests/test-package-status.sh` | The same matrix for the bash/python observer (`scripts/ci/observe_commit.py`). |
| `scripts/ci/tests/Test-GuiProcessCapture.ps1` | Windows GUI-subsystem process capture (P8; Windows host required). |
| `scripts/ci/tests/test-docs-consistency.sh` | Artifact names, doc links, no obsolete tool versions. |
| `scripts/ci/tests/test-ci-plumbing.sh` | Capture/collection plumbing contracts. |
| `scripts/ci/tests/test-codeql-coverage-inventory.sh` | CodeQL extraction coverage proof. |
