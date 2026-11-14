#!/usr/bin/env python3
"""observe_commit.py — the bash-side package-status commit observer.

Single documented contract (run 43090342995 item D): the observed commit
comes EXCLUSIVELY from the app's machine-readable `--build-info-json`
output, parsed with a real JSON decoder. Human `--version` console text
is never scraped. Verification rules MUST stay identical to
scripts/ci/Get-PackageCommitStatus.ps1 (the PowerShell-side observer;
both are covered by the same fixture matrix):

  - A non-zero process exit code is never verified, regardless of output.
  - Empty/whitespace output, malformed JSON, a missing/empty commit field
    and the literal "unknown" default are never verified.
  - The observed value is reported EXACTLY as embedded; trailing
    LF/CRLF/whitespace of the whole stdout is tolerated by the decoder.
  - A non-blank expected commit must equal the observed commit exactly.

Usage:
  observe_commit.py <expected-commit> <process-exit-code>   # JSON on stdin
Output: one JSON line {"observed": ..., "verified": bool, "reason": ...}
"""
import json
import sys

NOT_OBSERVED = "not-observed"
UNKNOWN = "unknown"


def observe(expected_commit: str, process_exit_code: int, stdout: str) -> dict:
    observed = NOT_OBSERVED
    verified = False
    reason = ""
    if process_exit_code != 0:
        reason = f"process exited with code {process_exit_code}; output is not trusted"
    elif stdout is None or not stdout.strip():
        reason = "no build-info output captured"
    else:
        try:
            info = json.loads(stdout)
            if not isinstance(info, dict):
                raise ValueError("build-info JSON is not an object")
            commit = info.get("commit")
            if commit is None or not str(commit).strip():
                observed = UNKNOWN
                reason = "commit field missing or empty in build-info JSON"
            elif str(commit) == UNKNOWN:
                observed = UNKNOWN
                reason = "embedded commit is the unknown default (metadata not injected)"
            else:
                observed = str(commit)
                expected = (expected_commit or "").strip()
                if not expected:
                    reason = "expected commit is blank; identity cannot be verified"
                elif observed == expected:
                    verified = True
                else:
                    reason = "observed commit does not match the expected commit"
        except (json.JSONDecodeError, ValueError, AttributeError, TypeError) as exc:
            reason = f"build-info JSON malformed: {exc}"
    return {"observed": observed, "verified": verified, "reason": reason}


def main() -> int:
    if len(sys.argv) < 3:
        print("usage: observe_commit.py <expected-commit> <process-exit-code> < build-info.json", file=sys.stderr)
        return 2
    expected = sys.argv[1]
    try:
        exit_code = int(sys.argv[2])
    except ValueError:
        print(f"invalid exit code argument: {sys.argv[2]!r}", file=sys.stderr)
        return 2
    stdout = sys.stdin.read()
    print(json.dumps(observe(expected, exit_code, stdout)))
    return 0


if __name__ == "__main__":
    sys.exit(main())
