# GitHub Requirements Manager and Download Center engine

Status: **Implemented (engine core), Experimental (UI integration)**.
This document covers the `internal/ghreq` package delivered by the
run-44094081531 work order: the single download engine, the closed
requirements catalog, GitHub release discovery, deterministic asset
selection, origin policy, integrity classification and the resumable
transfer state machine.

## What this subsystem is

A package-acquisition core for runtime requirements that the product
actually consumes. It is **not** a source-control client: it never
invokes Git, never clones, never builds downloaded source trees, and
exposes no repository-management operations. The coding agent's own
repository tooling is unrelated to the shipped application.

## The one download engine

`internal/ghreq.Downloader` is the only transfer implementation.
Every download in the product (requirements, media tools, optional
assets) must use it; nothing else implements HTTP transfers.

Engine contract:

- Typed job states: `queued`, `resolving`, `downloading`, `paused`,
  `verifying`, `completed`, `failed`, `cancelled`, `blocked`.
- Bounded queue (`QueueCapacity`, default 32). Saturation is visible
  as a typed `ErrQueueFull` rejection, never unbounded growth.
- Concurrency caps: `GlobalMaxActive` (default 2) and
  `PerHostMaxActive` (default 2).
- Cooperative pause at chunk boundaries; no busy waits; no worker
  slot held while paused; no whole-process kills.
- Transient-only bounded retries (default 3 attempts, exponential
  backoff with jitter). Policy failures (unapproved redirect host,
  insecure scheme, private address, HTML payload, digest mismatch,
  validator change) are permanent and never retried. Rate-limit
  responses surface `Retry-After` to the caller and are never
  auto-retried (no retry storms).
- Terminal records stay queryable; only active jobs count against
  queue capacity. `Close()` cancels active work and stops intake.

## Resume correctness

- Partial state lives in `<dest>.part` plus an atomically persisted
  sidecar `<dest>.part.meta.json` (schema 1: URL, ETag,
  Last-Modified, totals, bytes done, digest). The sidecar is written
  through temp-file + rename, never in place.
- On restart, the sidecar is only trusted when the URL matches and
  the partial file length equals the recorded offset.
- The engine always probes `Range: bytes=<done>-` on resume. A `206`
  is accepted only when `Content-Range` starts exactly at the stored
  offset and the total matches; a mismatch is a hard
  `ErrValidatorChanged` failure, never an append. `If-Range` carries
  the stored ETag when one is known.
- A `200` answer to a range probe means the server ignored the
  range: the engine restarts from zero (truncate), never appends a
  full body to a partial file.
- Final verification happens before promotion; only a verified file
  is renamed to its destination. Failed digests quarantine nothing
  at the destination and remove the partial bytes.

## ETA honesty

Throughput uses an exponentially weighted moving average with a
minimum of three samples before any numeric ETA is exposed. Until
then `ETAConfident` is false and the UI must show `Calculating`.
Verification and stall time are not counted as transfer speed.

## Origin policy

Only HTTPS on allowlisted hosts: `api.github.com`, `github.com`,
`objects.githubusercontent.com`, `release-assets.githubusercontent.com`
and `codeload.github.com` (plus loopback for deterministic tests).
Redirects are capped at 5 hops; downward scheme changes, unapproved
hosts, userinfo/credentials, and token-bearing source URLs are
rejected. `Authorization` and `Cookie` headers are stripped on every
cross-host redirect. The dial layer refuses private, loopback
(non-test), link-local, multicast, TEST-NET and carrier-grade NAT
addresses, so a redirect can never pivot into the local network.

## Requirements catalog

The catalog is closed and repository-owned; end users cannot add
sources. `Catalog.Validate()` enforces per requirement:

- Unique stable IDs; exact GitHub owner/repo names (owner:
  alphanumeric runs with single interior hyphens; repo: no doubled or
  leading/trailing dots).
- One bounded release policy: `pinned_tag`, `stable_latest` or
  `user_selected_stable`. Prereleases are opt-in only.
- Deterministic per-platform asset selectors: one anchored literal
  include pattern plus optional anchored exclusions. Ambiguity and
  zero matches are errors, never first-pick.
- A license identifier for every entry.
- Executable entries require pinned digest material (global or
  per-platform SHA-256) from repository provenance records.
- Safe install paths: forward-slash relative, no traversal, no drive
  prefixes, no reserved device names.
- Positive size bounds and canonical HTTPS source URLs.

`Embedded()` derives the catalog from `internal/mediatools`' pinned
manifest (single source of truth): the optional GPL FFmpeg media-tool
packs for windows/amd64 and linux/amd64. Other platforms report
`NOT_AVAILABLE_FOR_THIS_PLATFORM` honestly. The catalog invents no
requirements; new entries arrive only through reviewed repository
updates.

## Integrity levels

`ClassifyIntegrity` reports exactly which evidence held, in order:
`PINNED_MANIFEST_DIGEST_VERIFIED` (repository provenance pin),
`GITHUB_RELEASE_DIGEST_VERIFIED` (the API's release-asset `digest`
field over TLS - byte consistency with GitHub metadata, not publisher
identity), or `TRANSPORT_ONLY` (no independent material). A mismatch
is `FAILED`. `EnsureExecutableTrust` blocks installation of
executables unless pinned or release-digest evidence succeeded; a UI
override cannot bypass this gate.

## GitHub client

`ghreq.Client` uses the official REST endpoints
(`/releases/latest`, `/releases/tags/{tag}`, bounded `/releases`
pagination, 5 pages of 50, dedup by release ID), sends
`Accept: application/vnd.github+json` and the pinned
`X-GitHub-Api-Version`, caches metadata with ETag conditional
requests, records `X-RateLimit-*` state, and types rate-limit,
not-found, network and malformed-response failures. No token is
requested, accepted or embedded.

## Tests and gates

30 tests in `internal/ghreq` cover the acceptance criteria: catalog
rejections, embedded-catalog realness, deterministic asset selection,
platform normalization, origin/redirect policy (downgrade, private
address, credential stripping, depth), integrity classification and
the executable trust gate, resume with strict 206 validation,
200-restart, validator-change restart, digest mismatch blocking, HTML
payload rejection, pause/resume with persisted sidecar, cancel,
queue saturation, disk-short blocking, honest ETA behavior, retry
boundedness without storms, and bounded subscriber snapshots.

CI wiring: the platform `go-test` gates cover the package via
`go test ./...`; the Linux `go-test-race` gate lists
`./internal/ghreq/...` explicitly.

## Known limitations (BETA)

- No UI route yet: the Requirements & Downloads screen that exposes
  selection, ordering and progress is the next milestone. The engine
  contract above is complete and tested; the route is not, and no
  mock screen exists for it.
- Free-disk-space measurement is a host-supplied hook
  (`DownloaderConfig.FreeSpace`); without it the engine reports
  "unknown" instead of a fabricated number.
- Signature/provenance verification beyond pinned digests (publisher
  key policies) is planned, not implemented; executables therefore
  always require pinned material today.
- Archive extraction for arbitrary requirements reuses the hardened
  `internal/mediatools` extract session at install time; the typed
  install-transaction layer (staging, promotion, rollback for
  non-media-tool requirements) is the follow-up milestone.
