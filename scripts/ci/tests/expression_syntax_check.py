#!/usr/bin/env python3
"""expression_syntax_check.py - GitHub Actions `if:` expression syntax checker.

Implements the gate contract of scripts/ci/tests/test-expression-syntax.sh
(run 44449832903 repair). GitHub Actions accepts an `if:` condition either
as a bare expression (``if: always()``) or in the exact braced form
(``if: ${{ expression }}``). The malformed single-brace form
(``if: ${ always() && !cancelled() }``, the run 44449832903 defect on
former build-desktop.yml lines 1154/1163/1172) parses as YAML but fails
the whole workflow file at expression-evaluation time, so no job runs.

Modes:
  --scan FILE        Parse the workflow with PyYAML, walk every `if:` key
                     (job level, step level, anywhere), report violations,
                     exit 1 on any violation, exit 0 otherwise.
  --check-value      Read condition values from stdin (one per line); exit 0
                     when every line is a VALID condition, exit 1 when any
                     line is malformed. Used by the negative controls.
  --job-if FILE JOB  Print the job-level `if:` value of JOB (empty if none).

Validation rules for a condition value (string only):
  R1. No `${` that is not immediately followed by `{` (malformed
      single-brace form).
  R2. Every `${{` opening has a matching `}}` closing in the same value
      (balanced braces).
Comments and non-`if:` keys are never inspected: values come from the
parsed YAML document, so YAML comments cannot trigger findings.
"""

from __future__ import annotations

import argparse
import re
import sys

try:
    import yaml
except ImportError:  # pragma: no cover - PyYAML is a CI runner base image package
    sys.stderr.write("expression_syntax_check.py: PyYAML is required\n")
    sys.exit(3)

_OPEN = "${{"
_CLOSE = "}}"
_MALFORMED = re.compile(r"\$\{(?!{)")


def check_value(value: str) -> str | None:
    """Return a violation description for a condition value, or None."""
    if not isinstance(value, str):
        return None
    if _MALFORMED.search(value):
        return f"malformed single-brace expression (found `${{` not `${{{{`): {value!r}"
    opens = value.count(_OPEN)
    closes = value.count(_CLOSE)
    if opens != closes:
        return f"unbalanced expression braces (open_count={opens}, close_count={closes}): {value!r}"
    return None


def _walk_if_values(node, path):
    if isinstance(node, dict):
        for key, val in node.items():
            here = f"{path}.{key}" if path else str(key)
            if key == "if":
                yield here, val
            yield from _walk_if_values(val, here)
    elif isinstance(node, list):
        for idx, item in enumerate(node):
            yield from _walk_if_values(item, f"{path}[{idx}]")


def scan(path: str) -> int:
    violations = 0
    try:
        with open(path, encoding="utf-8") as fh:
            doc = yaml.safe_load(fh)
    except (OSError, yaml.YAMLError) as exc:
        print(f"PARSE-ERROR {path}: {exc}")
        return 1
    if not isinstance(doc, dict):
        print(f"PARSE-ERROR {path}: top-level document is not a mapping")
        return 1
    jobs = doc.get("jobs")
    if not isinstance(jobs, dict) or not jobs:
        print(f"PARSE-ERROR {path}: no jobs mapping")
        return 1
    for where, value in _walk_if_values(doc, ""):
        problem = check_value(value) if isinstance(value, str) else None
        if problem:
            print(f"VIOLATION {path} at {where}: {problem}")
            violations += 1
    return 1 if violations else 0


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    group = ap.add_mutually_exclusive_group(required=True)
    group.add_argument("--scan", metavar="FILE")
    group.add_argument("--check-value", action="store_true")
    group.add_argument("--job-if", nargs=2, metavar=("FILE", "JOB"))
    args = ap.parse_args()

    if args.scan:
        return scan(args.scan)

    if args.check_value:
        bad = 0
        for line in sys.stdin.read().splitlines():
            problem = check_value(line)
            if problem:
                sys.stderr.write(f"INVALID: {problem}\n")
                bad += 1
        return 1 if bad else 0

    path, job = args.job_if
    try:
        with open(path, encoding="utf-8") as fh:
            doc = yaml.safe_load(fh)
    except (OSError, yaml.YAMLError) as exc:
        sys.stderr.write(f"PARSE-ERROR {path}: {exc}\n")
        return 3
    jobs = doc.get("jobs") if isinstance(doc, dict) else None
    if not isinstance(jobs, dict) or job not in jobs:
        sys.stderr.write(f"job {job!r} not found in {path}\n")
        return 3
    value = jobs[job].get("if", "")
    print(value if isinstance(value, str) else "")
    return 0


if __name__ == "__main__":
    sys.exit(main())
