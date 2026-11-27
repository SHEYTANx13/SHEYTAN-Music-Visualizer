#!/usr/bin/env python3
"""write-manifest.py — deterministic, atomic, self-validating run-manifest writer.

Run 42376176493 root cause this file repairs: the manifest steps built JSON
exclusively with `jq -n`. When jq was unavailable on the runner image, the
`> ci-parts/manifest.json` shell redirect still created the file, jq failed,
and the step left a ZERO-BYTE manifest behind. The collector's
`[ -s ci-parts/manifest.json ]` guard correctly refused to run (exit 2 =
hard error class "invalid/missing manifest") and the emergency fallback
reported "MANIFEST MISSING OR EMPTY" with environment-derived job results.

Contract (run-42376176493 repair phase 5; conclusion vocabulary extended
for run 45678113841):
  * All context arrives via environment variables — never via shell string
    interpolation into JSON, never via heredocs.
  * Hosted run 45678113841 repair: GitHub can emit job conclusions outside
    the historical four-value whitelist — that run's build-macos concluded
    "abandoned" (runner lost mid-job), the writer failed closed, the run
    manifest never existed, the collector refused (rc=2) and the emergency
    fallback published UNVERIFIED coverage (the run-42376176493 signature
    class returned through a different door). The writer now records the
    complete GitHub conclusion vocabulary verbatim and maps any future
    unrecognized value to "failure" + result_raw/result_note instead of
    destroying the manifest: the collector always runs, the consolidated
    report is always produced, and a required job that did not succeed is
    always red. Fail-closed remains for genuine input defects (missing env,
    non-numeric ids, malformed SHA) — those are repository bugs, not
    GitHub vocabulary evolution.
  * JSON is produced with the Python standard library `json` module only.
  * The manifest is written to a temporary file in the SAME directory,
    fsynced, re-read, parsed, schema-checked (required keys/types, result
    vocabulary, numeric attempt, SHA shape) and only then atomically
    renamed to the final path. A failure never leaves a plausible-looking
    empty or partial manifest at the final path.
  * A diagnostics block (cwd, absolute output path, byte length, parser
    version, validation result, allowlisted summary) is printed to stdout
    and written next to the manifest — tokens/secrets are never read.
  * Exit codes: 0 manifest written and validated; 2 fail-closed
    (missing/invalid environment input or validation failure), with a
    specific stderr message. The calling step must fail.

Usage:
  python3 scripts/ci/write-manifest.py --profile caller|codeql \
      --out ci-parts/manifest.json [--diagnostics ci-parts/manifest-diagnostics.txt]
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sys
import tempfile

# Run 45678113841 repair: the complete GitHub Actions job-conclusion
# vocabulary. "abandoned" was OBSERVED hosted (needs.build-macos.result on
# run 45678113841: the macos-26 runner was lost mid-job); the rest are the
# documented workflow-syntax conclusions. Step outcomes (download steps)
# can additionally be "success"/"failure"/"cancelled"/"skipped" only, so
# the outcome tuple stays conservative.
VALID_RESULTS = (
    "success",
    "failure",
    "cancelled",
    "skipped",
    "neutral",
    "timed_out",
    "action_required",
    "startup_failure",
    "abandoned",
)
VALID_OUTCOMES = ("success", "failure", "cancelled", "skipped")
SHA_RE = re.compile(r"^[0-9a-f]{40}$|^[0-9a-f]{64}$")

# Run 45455106994 repair: REQUIRED_CALLER_RESULTS / REQUIRED_CALLER_DOWNLOADS
# must list EXACTLY the env names the "Write run manifest" step of
# build.yml passes. The run-45347124889 update removed the iOS job and its
# env rows from build.yml but left RESULT_IOS / DOWNLOAD_IOS required here;
# on every hosted run the writer then failed closed with "required
# environment variable RESULT_IOS is empty or unset" BEFORE writing the
# manifest, so the collector refused (rc=2), the emergency fallback ran and
# collect-logs went red with "run manifest MISSING: the manifest step did
# not create it" (the run-42376176493 signature). iOS is not a live target;
# these tuples now match the five live platforms. The env-parity contract is
# enforced structurally by the fixture in scripts/ci/tests/test-ci-plumbing.sh
# (it parses build.yml's manifest-step env block and asserts BOTH directions:
# every name build.yml passes is required here, and every required name is
# passed there), so a future job addition/removal can never desync again.
REQUIRED_CALLER_RESULTS = (
    "RESULT_PREFLIGHT",
    "RESULT_WINDOWS",
    "RESULT_LINUX",
    "RESULT_MACOS",
    "RESULT_ANDROID",
)
REQUIRED_CALLER_DOWNLOADS = (
    "DOWNLOAD_PREFLIGHT",
    "DOWNLOAD_WINDOWS",
    "DOWNLOAD_LINUX",
    "DOWNLOAD_MACOS",
    "DOWNLOAD_ANDROID",
)


class ManifestError(Exception):
    """Fail-closed writer error (exit 2, specific message)."""


def env(name: str, required: bool = True) -> str:
    value = os.environ.get(name)
    if value is None or value == "":
        if required:
            raise ManifestError(
                f"required environment variable {name} is empty or unset; "
                "the manifest step must pass every context value via env"
            )
        return ""
    return value


def checked_result(name: str) -> tuple[str, str, str]:
    """Return (result, result_raw, result_note).

    Known conclusions are recorded verbatim. An unrecognized value is
    NEVER fail-closed (run 45678113841 class): it is recorded as
    result="failure" (a required job that did not succeed is red) with
    the raw value and a note kept verbatim, so the evidence chain
    (manifest -> collector -> consolidated report) always survives GitHub
    conclusion-vocabulary evolution. An EMPTY value is still fail-closed
    (env() raises): an absent conclusion means the workflow wiring broke.
    """
    value = env(name)
    if value in VALID_RESULTS:
        return value, "", ""
    return (
        "failure",
        value,
        "unrecognized workflow conclusion recorded verbatim and classified "
        "as failure (run 45678113841 conclusion-vocabulary contract)",
    )


def checked_outcome(name: str) -> str:
    value = env(name)
    if value not in VALID_OUTCOMES:
        raise ManifestError(
            f"{name}={value!r} is not a step outcome "
            f"(want one of {', '.join(VALID_OUTCOMES)})"
        )
    return value


def _raw_fields(raws: dict, notes: dict, name: str) -> dict:
    """Emit result_raw/result_note ONLY for unrecognized conclusions, so
    the manifest stays byte-stable for the known vocabulary (the summary
    and the collector never see empty noise fields)."""
    if not raws.get(name):
        return {}
    return {"result_raw": raws[name], "result_note": notes[name]}


def build_caller_manifest() -> dict:
    attempt = env("RUN_ATTEMPT")
    if not attempt.isdigit():
        raise ManifestError(f"RUN_ATTEMPT={attempt!r} is not numeric")
    sha = env("COMMIT_SHA").lower()
    if not SHA_RE.match(sha):
        raise ManifestError(
            f"COMMIT_SHA={sha!r} is not a 40/64-char lowercase hex commit SHA"
        )
    run_id = env("RUN_ID")
    if not run_id.isdigit():
        raise ManifestError(f"RUN_ID={run_id!r} is not numeric")

    checked = {name: checked_result(name) for name in REQUIRED_CALLER_RESULTS}
    results = {name: value[0] for name, value in checked.items()}
    raws = {name: value[1] for name, value in checked.items()}
    notes = {name: value[2] for name, value in checked.items()}
    downloads = {name: checked_outcome(name) for name in REQUIRED_CALLER_DOWNLOADS}

    artifacts = {
        "preflight": f"preflight-logs-part-attempt{attempt}",
        "windows": f"windows-logs-part-attempt{attempt}",
        "linux": f"linux-logs-part-attempt{attempt}",
        "macos": f"macos-logs-part-attempt{attempt}",
        "android": f"android-logs-part-attempt{attempt}",
    }

    return {
        "product": "SHEYTAN Music Visualizer",
        "repository": env("REPO"),
        "workflow": "build.yml",
        "run_id": run_id,
        "attempt": attempt,
        "commit_sha": sha,
        "ref": env("REF"),
        "event": env("EVENT"),
        "runner_os": env("RUNNER_OS_LABEL"),
        "start_time_utc": env("STARTED_AT", required=False),
        # CodeQL is an independent workflow (run 43612608237); the caller
        # manifest records ONLY this workflow's own jobs and evidence.
        "codeql": {
            "workflow": "codeql.yml",
            "job": "independent-workflow",
            "result": "NOT_APPLICABLE",
            "note": (
                "CodeQL is no longer invoked by build.yml; it is "
                "a standalone workflow (push/pull_request/schedule/dispatch) "
                "whose results are consolidated in that workflow's own "
                "log.txt and never gate this build."
            ),
        },
        "downloads": [
            {"artifact": artifacts["preflight"], "outcome": downloads["DOWNLOAD_PREFLIGHT"]},
            {"artifact": artifacts["windows"], "outcome": downloads["DOWNLOAD_WINDOWS"]},
            {"artifact": artifacts["linux"], "outcome": downloads["DOWNLOAD_LINUX"]},
            {"artifact": artifacts["macos"], "outcome": downloads["DOWNLOAD_MACOS"]},
            {"artifact": artifacts["android"], "outcome": downloads["DOWNLOAD_ANDROID"]},
        ],
        "jobs": [
            {
                "name": "preflight",
                "kind": "workflow-job",
                "result": results["RESULT_PREFLIGHT"],
                "result_basis": "needs.preflight.result (workflow conclusion)",
                "artifact": artifacts["preflight"],
                "required": True,
                **_raw_fields(raws, notes, "RESULT_PREFLIGHT"),
            },
            {
                "name": "build-windows",
                "kind": "workflow-job",
                "result": results["RESULT_WINDOWS"],
                "result_basis": "needs.build-windows.result (workflow conclusion)",
                "artifact": artifacts["windows"],
                "required": True,
                **_raw_fields(raws, notes, "RESULT_WINDOWS"),
            },
            {
                "name": "build-linux",
                "kind": "workflow-job",
                "result": results["RESULT_LINUX"],
                "result_basis": "needs.build-linux.result (workflow conclusion)",
                "artifact": artifacts["linux"],
                "required": True,
                **_raw_fields(raws, notes, "RESULT_LINUX"),
            },
            {
                "name": "build-macos",
                "kind": "workflow-job",
                "result": results["RESULT_MACOS"],
                "result_basis": "needs.build-macos.result (workflow conclusion)",
                "artifact": artifacts["macos"],
                **_raw_fields(raws, notes, "RESULT_MACOS"),
                # Required: macOS is a desktop parity target. The job's
                # gate contract is a documented reduced subset of the
                # Linux contract; not-yet-ported parity gates are
                # recorded BLOCKED inside the job, never passed.
                "required": True,
            },
            {
                "name": "build-android",
                "kind": "workflow-job",
                "result": results["RESULT_ANDROID"],
                "result_basis": "needs.build-android.result (workflow conclusion)",
                "artifact": artifacts["android"],
                **_raw_fields(raws, notes, "RESULT_ANDROID"),
                # REQUIRED product target: a real arm64 APK is built,
                # validated and published through the workflow-wide
                # publisher. Emulator runtime evidence remains honestly
                # UNAVAILABLE on hosted runners (no nested
                # virtualization) and is never claimed.
                "required": True,
            },
            # (The iOS compile smoke job was removed by the
            # run-45347124889 update: iOS is not a supported target at
            # this stage; the manifest records only live jobs.)
        ],
    }


def build_codeql_manifest() -> dict:
    attempt = env("RUN_ATTEMPT")
    if not attempt.isdigit():
        raise ManifestError(f"RUN_ATTEMPT={attempt!r} is not numeric")
    sha = env("COMMIT_SHA").lower()
    if not SHA_RE.match(sha):
        raise ManifestError(
            f"COMMIT_SHA={sha!r} is not a 40/64-char lowercase hex commit SHA"
        )
    run_id = env("RUN_ID")
    if not run_id.isdigit():
        raise ManifestError(f"RUN_ID={run_id!r} is not numeric")

    result_go = checked_result("RESULT_ANALYZE_GO")
    result_actions = checked_result("RESULT_ANALYZE_ACTIONS")
    result_codeql = "success" if (result_go == "success" and result_actions == "success") else "failure"

    return {
        "product": "SHEYTAN Music Visualizer",
        "repository": env("REPO"),
        "workflow": "codeql.yml",
        "run_id": run_id,
        "attempt": attempt,
        "commit_sha": sha,
        "ref": env("REF"),
        "event": env("EVENT"),
        "runner_os": env("RUNNER_OS_LABEL"),
        "start_time_utc": env("STARTED_AT", required=False),
        "codeql": {
            "workflow": "codeql.yml",
            "job": "aggregate",
            "result": result_codeql,
        },
        "jobs": [
            {
                "name": "analyze (go)",
                "result": result_go,
                "artifact": f"codeql-logs-part-go-attempt{attempt}",
                "required": True,
            },
            {
                "name": "analyze (actions)",
                "result": result_actions,
                "artifact": f"codeql-logs-part-actions-attempt{attempt}",
                "required": True,
            },
        ],
    }


def validate_document(doc: dict, profile: str) -> None:
    """Schema check applied to the SERIALIZED-THEN-REPARSED document, so
    validation exercises exactly the bytes that land on disk."""
    for key in ("product", "repository", "workflow", "run_id", "attempt",
                "commit_sha", "ref", "event", "jobs"):
        if key not in doc:
            raise ManifestError(f"serialized manifest is missing required key {key!r}")
    if not isinstance(doc["jobs"], list) or not doc["jobs"]:
        raise ManifestError("serialized manifest 'jobs' must be a non-empty array")
    for job in doc["jobs"]:
        if not isinstance(job.get("name"), str) or not job["name"]:
            raise ManifestError(f"job row without a name: {job!r}")
        if job.get("result") not in VALID_RESULTS:
            raise ManifestError(f"job {job['name']!r} has invalid result {job.get('result')!r}")
        if profile == "caller" and not isinstance(job.get("kind"), str):
            raise ManifestError(f"job {job['name']!r} is missing its 'kind' label")
    for dl in doc.get("downloads", []):
        if dl.get("outcome") not in VALID_OUTCOMES:
            raise ManifestError(f"download row has invalid outcome: {dl!r}")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--profile", choices=("caller", "codeql"), required=True)
    parser.add_argument("--out", required=True, help="final manifest path (written atomically)")
    parser.add_argument(
        "--diagnostics",
        default=None,
        help="diagnostics text path (default: <out dir>/manifest-diagnostics.txt)",
    )
    args = parser.parse_args()

    out_path = os.path.abspath(args.out)
    out_dir = os.path.dirname(out_path) or "."
    os.makedirs(out_dir, exist_ok=True)

    try:
        doc = build_caller_manifest() if args.profile == "caller" else build_codeql_manifest()
    except ManifestError as exc:
        print(f"write-manifest.py: FAIL-CLOSED: {exc}", file=sys.stderr)
        return 2

    payload = json.dumps(doc, indent=2, ensure_ascii=True) + "\n"

    # Atomic write: temp file in the SAME directory, fsync, validate the
    # reparsed bytes, then rename. A crash or validation failure can never
    # leave an empty/partial file at the final path.
    tmp_fd, tmp_path = tempfile.mkstemp(prefix=".manifest-", suffix=".tmp", dir=out_dir)
    try:
        with os.fdopen(tmp_fd, "w", encoding="ascii", newline="\n") as handle:
            handle.write(payload)
            handle.flush()
            os.fsync(handle.fileno())
        with open(tmp_path, "rb") as handle:
            reparsed = json.loads(handle.read().decode("ascii"))
        validate_document(reparsed, args.profile)
        if os.path.exists(out_path):
            os.unlink(out_path)  # remove a stale/empty artifact before the rename
        os.replace(tmp_path, out_path)
        tmp_path = None  # renamed away
    except ManifestError:
        print("write-manifest.py: FAIL-CLOSED: serialized manifest failed schema validation", file=sys.stderr)
        return 2
    except json.JSONDecodeError as exc:
        print(f"write-manifest.py: FAIL-CLOSED: serialized manifest does not parse: {exc}", file=sys.stderr)
        return 2
    finally:
        if tmp_path is not None and os.path.exists(tmp_path):
            os.unlink(tmp_path)

    size = os.path.getsize(out_path)
    if size <= 0:
        # Unreachable in practice (os.replace of an fsynced temp file), but
        # the byte-size invariant is part of the contract.
        print("write-manifest.py: FAIL-CLOSED: final manifest byte size is zero", file=sys.stderr)
        return 2

    summary = {
        "workflow": doc["workflow"],
        "run_id": doc["run_id"],
        "attempt": doc["attempt"],
        "commit_sha": doc["commit_sha"],
        "ref": doc["ref"],
        "event": doc["event"],
        "jobs": [{"name": j["name"], "result": j["result"]} for j in doc["jobs"]],
        "downloads": [{"artifact": d["artifact"], "outcome": d["outcome"]} for d in doc.get("downloads", [])],
    }
    diag_lines = [
        "=== run-manifest writer diagnostics (allowlisted fields only; no env dump) ===",
        f"python: {sys.version.split()[0]} (stdlib json)",
        f"profile: {args.profile}",
        f"cwd: {os.getcwd()}",
        f"manifest_path_absolute: {out_path}",
        f"manifest_bytes: {size}",
        "validation: OK (reparsed bytes, schema checked)",
        "atomic_write: OK (temp file in same directory, fsync, os.replace)",
        "summary: " + json.dumps(summary, sort_keys=True),
    ]
    diag_text = "\n".join(diag_lines) + "\n"
    diag_path = args.diagnostics or os.path.join(out_dir, "manifest-diagnostics.txt")
    with open(diag_path, "w", encoding="utf-8", newline="\n") as handle:
        handle.write(diag_text)
    print(diag_text, end="")
    print(f"write-manifest.py: manifest written and validated: {out_path} ({size} bytes)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
