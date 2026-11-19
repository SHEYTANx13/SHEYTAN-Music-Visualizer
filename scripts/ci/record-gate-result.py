#!/usr/bin/env python3
r"""record-gate-result.py — structured static-workflow-validation gate record.

Run 43612608237 repair: the preflight job previously embedded this JSON
record inside a `python3 -c "..."` string in .github/workflows/
build.yml (renamed from build-desktop.yml in the run 44449832903
repair). The string quoted the actionlint ignore list with
sequences like `\\$\\/\\.github` that, after shell double-quote processing,
reached Python as `\\.` / `\\/` invalid escape sequences and emitted:

    SyntaxWarning: "\." is an invalid escape sequence. Such sequences will
    not work in future Python versions.

The record writer is now a real module: no shell double-quote layer, every
string literal is a plain ASCII literal, and preflight compiles every
scripts/ci/*.py helper with warnings promoted to errors
(`python3 -W error::SyntaxWarning -m py_compile`).

The ignore list below must stay in sync with the `-ignore` flags passed to
actionlint in build.yml. Run 43612608237 removed the third entry
(the `$/` self-repository reusable-workflow-call pattern) because CodeQL is
no longer called as a reusable workflow from build.yml, which also
removed the offending escape sequences at their source.

Usage:
  python3 scripts/ci/record-gate-result.py --out <path.json> \
      --run-id ID --attempt N --commit SHA --started UTC --ended UTC \
      --actionlint-exit N --zizmor-exit N --fixture-failed 0|1

Exit codes: 0 record written; 2 fail-closed (invalid argument).
"""

from __future__ import annotations

import argparse
import json
import os
import re
import sys

ACTIONLINT_VERSION = "1.7.12"
ZIZMOR_VERSION = "1.32.0"
# Keep in exact sync with the actionlint -ignore flags in
# .github/workflows/build.yml (plain-text descriptions of the two
# documented upstream compatibility exceptions; no regex metacharacters).
ACTIONLINT_IGNORES = [
    'unexpected key "queue" for "concurrency" section',
    'property "run_started_at" is not defined',
]

RESULT_TEMPLATE = {
    "gate": "static-workflow-validation",
    "zizmor": {
        "version": ZIZMOR_VERSION,
        "invocation": (
            "GITHUB_TOKEN=<github.token> uvx --from zizmor=="
            + ZIZMOR_VERSION
            + " zizmor --min-severity low --min-confidence low"
            " --format plain .github/workflows/"
        ),
        "online_audits": (
            "enabled (workflow token, contents: read; any unavailable online"
            " check is recorded from zizmor output, never labeled fully"
            " complete then)"
        ),
    },
    "actionlint": {
        "version": ACTIONLINT_VERSION,
        "ignores": ACTIONLINT_IGNORES,
    },
}

SHA_RE = re.compile(r"^[0-9a-f]{40}$|^[0-9a-f]{64}$")
UTC_RE = re.compile(r"^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$")


def _int(value: str, name: str) -> int:
    try:
        return int(value)
    except ValueError as exc:
        raise SystemExit(f"record-gate-result.py: --{name} must be an integer, got {value!r}") from exc


def build_record(args: argparse.Namespace) -> dict:
    commit = args.commit.lower()
    if not SHA_RE.match(commit):
        raise SystemExit("record-gate-result.py: --commit must be a 40/64-char lowercase hex SHA")
    for stamp_name, stamp in (("started", args.started), ("ended", args.ended)):
        if not UTC_RE.match(stamp):
            raise SystemExit(f"record-gate-result.py: --{stamp_name} must be a UTC %Y-%m-%dT%H:%M:%SZ stamp")
    if not args.run_id.isdigit():
        raise SystemExit("record-gate-result.py: --run-id must be numeric")
    if not args.attempt.isdigit():
        raise SystemExit("record-gate-result.py: --attempt must be numeric")

    actionlint_exit = _int(args.actionlint_exit, "actionlint-exit")
    zizmor_exit = _int(args.zizmor_exit, "zizmor-exit")
    fixture_failed = bool(_int(args.fixture_failed, "fixture-failed"))

    record = dict(RESULT_TEMPLATE)
    record.update(
        {
            "run_id": args.run_id,
            "attempt": args.attempt,
            "commit": commit,
            "started_utc": args.started,
            "ended_utc": args.ended,
            "actionlint_exit": actionlint_exit,
            "zizmor_exit": zizmor_exit,
            "negative_fixture_failed": fixture_failed,
            "verdict": "PASS"
            if (actionlint_exit == 0 and zizmor_exit == 0 and not fixture_failed)
            else "FAIL",
        }
    )
    return record


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--out", required=True, help="gate-results JSON output path")
    parser.add_argument("--run-id", required=True)
    parser.add_argument("--attempt", required=True)
    parser.add_argument("--commit", required=True)
    parser.add_argument("--started", required=True, help="started_utc (UTC stamp)")
    parser.add_argument("--ended", required=True, help="ended_utc (UTC stamp)")
    parser.add_argument("--actionlint-exit", required=True)
    parser.add_argument("--zizmor-exit", required=True)
    parser.add_argument("--fixture-failed", required=True, help="0 or 1")
    args = parser.parse_args()

    record = build_record(args)

    out_path = os.path.abspath(args.out)
    out_dir = os.path.dirname(out_path) or "."
    os.makedirs(out_dir, exist_ok=True)
    payload = json.dumps(record, indent=2, ensure_ascii=True) + "\n"
    with open(out_path, "w", encoding="ascii", newline="\n") as handle:
        handle.write(payload)
        handle.flush()
        os.fsync(handle.fileno())

    print(json.dumps(record, sort_keys=True))
    return 0


if __name__ == "__main__":
    sys.exit(main())
