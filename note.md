# note.md — run 44010666873 work order execution record

Work order: SHEYTAN MUSIC VISUALIZER — stability, bounded work queues,
secure file access, playback, 3D worlds, performance and release
verification (RUN 44010666873).
Base commit audited and modified: `5b306dd96a6637f5101a705830d5e0594ac7e6ca`
(branch `main`, the run's exact head SHA — verified equal to the local
clone HEAD).
No remote writes were performed. Nothing was pushed, merged, tagged,
published or re-run. All changes are local and delivered as a source
ZIP; owner approval is required for any publication.

---

## 1. Live-run forensics (Section 1.1)

### 1.1 Evidence actually retrieved (read-only)

| Item | Value | Source |
| --- | --- | --- |
| Workflow | `build-desktop.yml`, run display "BETA #33" | run page HTML + repo |
| Event / branch | `push` on `main` | run page HTML |
| Head SHA | `5b306dd96a6637f5101a705830d5e0594ac7e6ca` | run page (short `5b306dd`) + local clone `git rev-parse HEAD` (identical) |
| Actor | SHEYTANx13 | run page HTML |
| Started / duration | 2026-11-17 03:30 UTC / ~4m12s | run page HTML |
| Conclusion | **FAILURE** | run page HTML |
| preflight (ubuntu-26.04) | **succeeded**, 42s (job id 133796639098) | job page HTML |
| build-linux (ubuntu-26.04, x64) | **succeeded**, 3m22s (job id 133796639360) | job page HTML |
| build-windows (windows-latest, UCRT64) | **FAILED**, 3m7s (job id 133796638898); annotation text: "Process completed with exit code 1." | job page HTML |
| collect-logs | ran (38s), produced artifact | run page HTML |
| final-verdict | **FAILED**, 3s; annotations: "Required gate build-windows did not succeed"; "result=failure — see the build-windows job log and the consolidated report artifact SHEYTAN-Music-Visualizer-all-logs.txt" | run page HTML |
| Artifacts | `SHEYTAN-Music-Visualizer-all-logs.txt` (755 KB), `SHEYTAN-Music-Visualizer-linux-x64.tar.gz` (23.8 MB), `SHEYTAN-Music-Visualizer-windows-x64.zip` (26.2 MB) | run page HTML |

Inference supported by the workflow's own contract (not a substitute
for the log): the Windows ZIP was uploaded, and upload happens only
when `package_status ∈ {VERIFIED, TEST_ONLY}` and the ZIP exists and
carries no bundled media tools — therefore the Windows build COMPILED
and packaged, and the red result comes from a verification gate, not a
compiler failure. Which gate exactly is recorded only in
`ci-logs/phase2-build.log` inside the artifacts (see blocker).

### 1.2 Evidence blocker (recorded precisely, not worked around)

The `api.github.com` unauthenticated quota (60 req/h, per IP) was
exhausted for the ENTIRE session — from the first call before any
work began until the final attempt — because this execution
environment shares an IP with other tenants that continuously consume
the quota (verified repeatedly via `/rate_limit`: `core.remaining: 0`
across ~90 minutes). Therefore, with no credentials available and
none requested (credentials must never be solicited), the following
could NOT be retrieved: the per-step conclusion matrix of
build-windows, the machine-readable annotation/job API records, and
the `all-logs.txt` artifact (artifact download requires
authentication). The run-page and job-page HTML (not rate-limited)
provided everything listed in §1.1. No older run's logs were used as
evidence. No current-run result was fabricated.

### 1.3 Local reproduction on the exact commit (Linux sandbox)

- `go build -tags goffi_static ./...` — PASS (Go 1.27.4, gcc, CGO on;
  local sysroot for GL/X11/wayland/EGL headers).
- `go vet -tags goffi_static ./...` — PASS.
- `go test -tags goffi_static -skip '^TestPaletteReachesGPUShader$' ./...`
  — ALL packages PASS (matches the green build-linux job; the skipped
  GPU-shader test is the repo's own CI skip).
- Windows-specific gates (MSYS2 UCRT64, PE validation, BtbN pinned
  media-tools proofs, app-managed h264/hevc export proofs on Windows)
  are NOT runnable in this Linux environment: no Windows OS, no MSYS2.
  Reproduction of the failing Windows gate is therefore BLOCKED here,
  consistent with the work order's honesty rules ("a build is not a
  runtime test"; label evidence precisely).

### 1.4 CodeQL status note

No CodeQL alert evidence was supplied for the current state, per the
work order's own note. The CodeQL inventory could not be re-checked
read-only through the API for the same quota reason (§1.2). No alert
was invented, reopened, suppressed or downgraded; the prior Zip-Slip
remediation and its regression tests were left untouched.

---

## 2. Source audit findings (defect matrix)

Architecture map: Fyne UI (ui/fyne, one window) → studio service
(internal/studio) → audio import/analysis (internal/audio, dsp,
songdna), playback (internal/playback, Oto v3), render (internal/render,
CPU + wgpu with the honest GPU state machine), export (internal/export,
FFmpeg child), content/project persistence, mediatools, logging,
appclose. Threading: Fyne main loop; studio mutex; transport player
goroutines; the audio device thread is owned by Oto.

| # | Location | Finding | Class | Action taken |
| --- | --- | --- | --- | --- |
| D1 | `internal/studio/studio.go` (ImportFile/startAnalysis/startPlaybackAsync/StartExport) | Every long operation spawned its own goroutine; no shared admission control, no queue caps, no fairness, no queue-visible metrics; saturation was invisible and unbounded in structure | P0 | All four routed through the new bounded scheduler (lanes audio_analysis / interactive_io / export); single-flight semantics and all events preserved |
| D2 | `internal/studio/studio.go` emitPlayEvent | One goroutine PER play-lifecycle event with no bound on in-flight sink deliveries | P0 | Single bounded dispatcher goroutine (buffer 256) + inline delivery with containment on overflow; events never silently dropped |
| D3 | studio Close() | Cancellation existed but there was no bounded drain of in-flight work at app close | P0 | Close now drains the scheduler with a 3 s deadline, cancels stragglers, stops the dispatcher; bounded and tested |
| D4 | whole app | No resource governor: decode/analysis/export could run simultaneously at full parallelism regardless of CPU count (the "22 workers because 22 CPUs" failure mode) | P0 | CPU-heavy budget `max(1, min(3, GOMAXPROCS/3))` shared across CPU lanes; per-lane memory ceilings; global temp-disk budget; pressure adaptation API wired to the studio |
| D5 | whole app | No user-visible queue state; queue-full situations had no typed outcome or actionable message | P0 | Typed admission outcomes with actionable reasons; `JobsOverview()` Job Center data model + `CancelJob`; UI panel marked partial (see gates) |
| D6 | selected-file analysis | Feature did not exist; risk of naive implementation elevating or over-scanning | P0 | New `internal/fileaccess` implementing the least-privilege, user-authorized, read-only, bounded, revocable contract with a redacted audit trail |
| D7 | `internal/render` | GPU truth model (detected/available/selected/executed/verified, device classes, adapter properties) ALREADY implemented correctly; one production renderer; no duplicate render loop found | — | Preserved unchanged (evidence-based preservation, no speculative replacement) |
| D8 | playback path | Honest device fallback, buffered-aware clock, session/generation IDs, structured events ALREADY implemented | — | Preserved; only the event-delivery bound changed (D2) |
| D9 | build-windows gate failure | Root cause not identifiable without the run's phase2 log (blocker §1.2); local Linux build+tests green at the same SHA | P1 | Recorded as blocked; no blind "fix" fabricated |

---

## 3. Implemented changes (all local, on top of 5b306dd)

### 3.1 `internal/jobs` (new package) — Section 2 vertical slice, complete core

Single scheduler; lanes A–I (A real-time audio and B UI interaction
deliberately NOT worker-pool lanes); hard ceilings (waiting 12/4/2/2/
1/4/1 = 26 total; active 2/2/1/1/1/1/1 = 9 fixed workers + 1
scheduler goroutine, never a goroutine per job); typed enqueue outcomes
(accepted / coalesced / rejected_full / rejected_shutdown /
rejected_unauthorized / rejected_budget); job records with stable IDs,
idempotency keys, newest-wins coalesce keys, priorities, memory/disk
estimates, deadlines, attempts, error categories, safe summaries,
correlation IDs, stage-based monotonic progress; lifecycle with no
terminal-state resurrection; transient-only bounded retries as new
attempts; weighted fair scheduling (IO 4 / analysis 2 / scene 1 /
export 1 / maintenance 1) with borrowable shares; 30 s monotonic aging
capped at PriorityCeiling (never above the audio/UI classes); resource
reservations acquired at admission and released on every terminal path
including panic containment; peek-then-commit admission (a job that
cannot currently be afforded is never lost); pressure governor
(`SetPressure`) that only ever reduces tokens; bounded Shutdown that
never blocks forever and reports stalls honestly; metadata-only JSONL
job journal (schema 1, per-record flush, atomic compaction 4096→1024)
with crash reconciliation marking orphaned live jobs `interrupted`.

Tests (`internal/jobs/jobs_test.go`, 24 tests, clean under `-race`):
capacity + typed rejection; priority ordering with FIFO tiebreak;
aging monotonicity + ceiling; cancellation while waiting/scheduled/
running; deadline timeout; idempotency dedup; newest-wins coalescing;
budget rejections (memory/disk/unknown lane); concurrent
memory-ceiling enforcement; panic containment + scheduler survival;
one-failure-does-not-kill-others; shutdown rejection/cancel/graceful
drain; bounded goroutine count (before/after baseline); enqueue-storm
accounting under 8 concurrent producers; trusted-privileged
authorization (missing/denied/granted); bounded transient retries
(no retry for permanent/cancel/budget); pressure adaptation +
recovery; journal crash reconciliation (orphan marked interrupted,
not resurrected); export non-starvation under continuous analysis
pressure; mixed-workload stress with high-water bound assertions
(≤ caps, ≤ 26 total waiting, zero live at end).

### 3.2 Studio integration (Part 3) — `internal/studio/jobs.go` + edits

Routing: import (decode) → audio_analysis (PriorityHigh,
idempotency `studio:import`); analysis → audio_analysis
(`studio:analysis`, previous cancelled first — at most one live);
first-play preparation → interactive_io (`studio:first-play`,
preparingPlay dedup kept); export → export (`studio:export`, 1+1,
EstDiskMB 2048 reservation). Queue-full paths return/emit actionable
messages. Defensive scheduler-less fallbacks preserve behavior if
journal init ever fails. `Close()` drains bounded (tested ≤ bounds).
New tests (`studio_jobs_test.go`): mixed workload stress (import +
concurrent second-import rejection + play + cancellation pressure;
drains to zero), Job Center overview truthfulness (≥ import+analysis
metered, CPU budget within 1–3), close-during-work boundedness.
The ENTIRE pre-existing studio test suite passes unchanged.

### 3.3 `internal/fileaccess` (new package) — Section 4 vertical slice

asInvoker least privilege (no elevation, no helper — documented why
none is needed); Prepare → user-visible summary (exact root label,
purpose, read-only scope, file types, depth, file/byte/time budgets,
sensitive-skip policy) → 10-minute confirmation window →
root-identity (dev+inode) re-verification → revocable Grant anchoring
an `os.Root`; openat-contained traversal (no string-prefix
containment); symlinks/FIFOs/devices/sockets counted, never opened
(the FIFO-open-blocks bug was caught by the test suite and fixed);
paged directory reads; depth/file-count/per-file/aggregate/time caps
with context cancellation; sensitive-location default-skip inventory
(credential stores, SSH/GPG/AWS/kube/Docker dirs, browser
logins/cookies/key DBs, wallets/seeds, key containers, OS hives,
dumps, .env/secrets); direct sensitive selection requires a separate
confirmation and still cannot bypass OS controls; capped SHA-256
fingerprint as the only derived data (content never executed or
uploaded); redacted bounded audit ring (labels + opaque scope IDs,
classified errors, never full paths) with JSON export.
Tests (11, clean under `-race`): basics + extension filter + audit
redaction; symlink escape never followed; FIFO skipped (adversarial);
sensitive default-skip counts + confirmed direct selection; depth cap;
file-count cap; mid-walk cancellation; revocation blocks walks;
root-identity swap (stale confirmation refused, fresh selection
works); authorization expiry; capped reads; deterministic
fingerprint.

### 3.4 Diagnostics & docs (Sections 5/7)

`studio.JobsOverview()/CancelJob()/SetSchedulerPressure()` (Job Center
data model + pressure feed). Canonical docs added and indexed:
`docs/architecture/queue-orchestration.md`,
`docs/security/file-access-and-privileges.md`,
`docs/operations/runtime-diagnostics.md`; `docs/INDEX.md` updated;
`scripts/ci/tests/test-docs-consistency.sh` PASSES (5/5). CHANGELOG
entry added.

### 3.5 Explicitly NOT done (no hollow mocks)

- No in-app Fyne Job Center / folder-picker PANELS: the underlying
  data model, bounds and cancellation are implemented and tested; the
  UI rendering is marked partial below (a fake panel would violate the
  no-hollow-mock rule).
- No elevated helper: no operation requires elevation; adding one
  "for completeness" is forbidden by the work order.
- No change to the renderer/GPU stack, audio backend, CI workflow
  files, or any security regression test: the audit found them sound;
  evidence did not justify replacement.
- No CodeQL alert handling: none supplied, none invented.

---

## 4. Gate status (honest)

| Gate (work-order section) | Status | Evidence |
| --- | --- | --- |
| §1 run forensics: metadata/jobs/artifacts inventory | PASS (HTML evidence) | §1.1 table |
| §1 run forensics: logs/annotations/artifact download | **BLOCKED** | §1.2 — API quota exhausted all session; no credentials; nothing fabricated |
| §1 reproduce current failures locally (Linux subset) | PASS | build + vet + full tests green at 5b306dd |
| §1 reproduce Windows gate failure | **BLOCKED** | no Windows/MSYS2 in environment |
| §2 bounded queue core + governor + tests | PASS | internal/jobs, 24 race-clean tests |
| §3 route app workloads + isolation | PASS | studio routing + stress/isolation tests; full legacy suite green |
| §3 Job Center UI panel | PARTIAL | data model + cancellation implemented/tested; Fyne panel not rendered |
| §4 secure selected-file access core + adversarial tests | PASS | internal/fileaccess, 11 race-clean tests |
| §4 in-app picker/analysis UI + archive-entry handling | PARTIAL/BLOCKED | core contract done; UI panel and hardened archive integration not in this slice |
| §4 privilege verification on Windows (asInvoker runtime test) | BLOCKED | requires a Windows host; static verification: no manifest/elevation added anywhere, documented |
| §5 runtime diagnostics data model | PASS | JobsOverview + journal + docs |
| §5 watchdog/heartbeat, staged startup milestones | NOT RUN | out of remaining budget; recorded |
| §6 performance benchmarks (P50/P95/P99) | NOT RUN | no comparable baseline hardware session; not simulated (honesty rule) |
| §7 docs | PASS | 3 canonical docs + INDEX + consistency gate |
| Windows x64 test package | **NOT PRODUCED** | gated on the blocked Windows verification; producing an unverified package would violate the delivery contract |
| Linux x64 verification | PASS (local) | build + tests green in this sandbox; NOT a claim about the hosted run |

---

## 5. Traceability to the priority order

- P0 evidence/baseline: §1 (with the recorded blockers), no
  working-tree destruction (clone pristine; only additive edits +
  documented modifications).
- P0 truthful playback + protected audio path: preserved (D8); the
  play-event delivery is now bounded (D2); the audio device thread
  remains scheduler-free (class A).
- P0 bounded/cancellable/visible work: internal/jobs + studio routing
  (D1–D5).
- P0 least-privilege file access: internal/fileaccess (D6).
- P1 single renderer + CPU/GPU coordination: preserved as audited
  (D7); the gpu_upload_render lane provides the bounded submission
  owner for future work; no speculative engine change.
- P1 real 3D worlds / camera / project persistence: untouched and
  functional (regression suite green) — no mock screens introduced.
- P1 Windows package: BLOCKED honestly (see gates).
- P2 startup/diagnostics/docs: diagnostics data model + docs done;
  startup milestones not in this slice.
- P3 roadmap traceability: preserved via this note + CHANGELOG +
  REQUIREMENTS-TRACEABILITY.md (unmodified).

## 6. Suggested next actions for the owner

1. Provide the `SHEYTAN-Music-Visualizer-all-logs.txt` artifact (or a
   token with `actions:read`) to pin the exact failing build-windows
   gate; the HTML evidence already narrows it to a post-package
   verification gate.
2. Review + commit the local changes (git status shows only the files
   listed in §3); rerun `build-desktop` on GitHub to validate the
   Windows path end to end.
3. Decide whether the Job Center / folder-picker Fyne panels should be
   the next milestone (data model is ready and tested).
