#!/usr/bin/env python3
"""write-package-status.py — the package-status record writer (run 43712071111).

Single documented contract (CONTRACT 4 of the run-43712071111 repair):
``ci-logs/package-status-<os>.json`` is derived from PERSISTED EVIDENCE
ONLY — gate-result JSON files under ``<log-dir>/gate-results/``, the
export-proof result files and an actual archive scan. In-memory gate
variables are never consulted: every capture helper runs a gate in a
CHILD process, so parent-script variables can never observe a gate's
outcome (the exact Windows defect this repair replaces — the removed
staging gate flipped a parent-scoped variable inside a child process
that the parent never saw, so the record claimed "not staged" while
the ZIP actually carried the binaries).

Evidence sources:
  * ``<log-dir>/gate-results/<gate>.result.json``  -> ``verdict`` field
    (PASS/FAIL; no record == gate never executed == not a pass);
  * ``<log-dir>/export-proof-h264.result.txt`` / ``export-proof-h265.result.txt``
    -> PASS/FAIL/NOT RUN (written by the packaged app-managed export
    proof as ``PASS <out>`` / ``FAIL <reason>``; the verdict token is
    the first word of the file);
  * the produced archive itself (tarfile/zipfile member scan for
    bundled media tools and per-file budget violations).

Status semantics (identical to the PowerShell side in
scripts/build/build-windows.ps1):
  VERIFIED    - commit_verified AND zero package-gate failures AND the
                archive is valid and clean AND ffmpeg_binaries_bundled
                is false AND media_tools_downloader_fixture == PASS AND
                app_launch_self_check == PASS AND h264_export_proof ==
                PASS AND h265_software_export_proof == PASS AND no
                quality/media gate failed.
  TEST_ONLY   - soft evidence missing or a quality gate failed while
                package integrity holds (job stays red either way).
  UNAVAILABLE - hard failure: commit unverified, package gate failed,
                archive missing/invalid or bundled media tools detected.
                Never uploaded.

Usage:
  write-package-status.py --platform linux --package <archive> --app-dir <dir> \
      --log-dir ci-logs --out ci-logs/package-status-linux.json \
      --commit-expected <sha> --commit-observed <sha> --commit-verified true|false \
      --commit-source "..." --commit-reason "..."
"""

from __future__ import annotations

import argparse
import json
import os
import sys

MAX_FILE_BYTES = 41943040  # 40 MiB per-file budget (forbid-bundled-media-tools)

# Gate classification. Union across platforms is deliberate: a gate that
# does not exist on this platform simply has no record. ONLY an actual
# FAIL record counts as a failure; a missing record is "never executed".
PACKAGE_GATES = (
    "stage-portable",
    "verify-package-license",
    "validate-pe",
    "create-portable-zip",
    "create-portable-tar",
    "forbid-bundled-media-tools",
    "package-size-budget",
    "verify-extracted-package",
    "verify-extracted-path-with-spaces",
)
QUALITY_GATES = (
    "toolchain-report",
    "desktop-env-check",
    "gofmt-verify",
    "go-mod-tidy-verify",
    "go-mod-verify",
    "go-test",
    "go-vet",
    "go-build-packages",
    "build-executable",
    "run-version",
    "run-self-check",
    "verify-build-metadata",
    "gpu-validation",
    "go-test-race",
    "export-validation",
    "audio-validation",
    "studio-integration",
    "gui-smoke",
    "govulncheck",
)
MEDIA_GATES = (
    "media-tools-downloader-tests",
    "media-tools-pinned-install",
)

MEDIA_TOOL_BASENAMES = {"ffmpeg", "ffmpeg.exe", "ffprobe", "ffprobe.exe"}
NESTED_ARCHIVE_SUFFIXES = (".zip", ".7z", ".tar", ".tar.gz", ".tar.xz")

NOTE = (
    "Derived from persisted evidence only: gate-result JSON files in "
    "ci-logs/gate-results/, export-proof result files and an actual "
    "archive scan. VERIFIED requires commit_verified, zero package-gate "
    "failures, no bundled media tools, a PASSing media-tools downloader "
    "fixture, app_launch_self_check PASS and both h264/h265 app-managed "
    "export proofs PASS; TEST_ONLY keeps the job red; UNAVAILABLE is "
    "never uploaded."
)


def read_gate_verdict(log_dir: str, gate: str) -> str | None:
    """Return 'PASS'/'FAIL' from the persisted gate record, else None."""
    path = os.path.join(log_dir, "gate-results", f"{gate}.result.json")
    try:
        with open(path, encoding="utf-8") as fh:
            doc = json.load(fh)
    except (OSError, ValueError):
        return None
    verdict = doc.get("verdict")
    if verdict in ("PASS", "FAIL"):
        return verdict
    return None


def read_proof_result(log_dir: str, name: str) -> str:
    """Read a proof verdict file.

    The export-proof gates write ``PASS <out-path>`` / ``FAIL <reason>``;
    the verdict token is the FIRST word of the (stripped) file, so a
    detailed reason or path never hides the verdict. Anything else —
    including a missing file — is NOT RUN (never a pass).
    """
    path = os.path.join(log_dir, name)
    try:
        with open(path, encoding="utf-8") as fh:
            text = fh.read().strip()
    except OSError:
        return "NOT RUN"
    first = text.split(None, 1)[0] if text else ""
    if first == "PASS":
        return "PASS"
    if first == "FAIL":
        return "FAIL"
    return "NOT RUN"


def scan_archive(package_path: str) -> tuple[bool, bool, int]:
    """Scan the produced archive.

    Returns (archive_valid, media_tools_bundled, scan_clean):
      archive_valid         - the file exists, is non-empty and opens;
      media_tools_bundled   - any ffmpeg/ffprobe member (case-insensitive);
      scan_clean            - no forbid-bundled-media-tools rule violation.
    """
    if not package_path or not os.path.isfile(package_path):
        return False, False, False
    size = os.path.getsize(package_path)
    if size <= 0:
        return False, False, False

    members: list[tuple[str, int]] = []  # (name, size)
    lower = package_path.lower()
    try:
        if lower.endswith((".zip",)):
            import zipfile

            with zipfile.ZipFile(package_path) as zf:
                for info in zf.infolist():
                    if info.filename.endswith("/"):
                        continue
                    members.append((info.filename, info.file_size))
        else:
            import tarfile

            with tarfile.open(package_path, "r:*") as tf:
                for member in tf.getmembers():
                    if not member.isfile():
                        continue
                    members.append((member.name, member.size))
    except Exception:  # noqa: BLE001 - any unreadable archive is invalid evidence
        return False, False, False

    bundled = False
    clean = True
    for name, msize in members:
        leaf = name.rstrip("/").rsplit("/", 1)[-1].lower()
        rel = name.lower()
        if leaf in MEDIA_TOOL_BASENAMES:
            bundled = True
        if leaf.endswith(".dll") and ("/tools/" in rel or rel.startswith("tools/")):
            clean = False
        if leaf.endswith(NESTED_ARCHIVE_SUFFIXES):
            clean = False
        # The application executable itself is exempt from the
        # media-asset per-file bound (same exemption as
        # forbid-bundled-media-tools: a Go+Fyne+wgpu binary is
        # legitimately ~35-50 MiB; the exact-name match keeps every
        # other file under the bound).
        if leaf in ("sheytan-music-visualizer", "sheytan-music-visualizer.exe",
                    "app-gogui", "app-gogui.exe"):
            # Run 45297707372 ship-the-shell: the product desktop shell
            # is exempt beside the CLI authority (same 35-50 MiB class).
            continue
        if msize > MAX_FILE_BYTES:
            clean = False
    return True, bundled, clean


def derive_matrix_fields(verdicts: dict) -> tuple[str, str]:
    """(ui_layout_matrix, crash_fault_injection) from the owning suites.

    The responsive viewport matrix (ui/fyne/responsive_test.go) runs in
    go-test; the crash/fault-injection tests (GPU device-loss recovery,
    close/flush error paths, export cancel/missing-encoder) run in
    go-test and — on Linux — the race suite. PASS needs the owning
    suites green; PARTIAL means the suite ran red so the subset outcome
    is not isolated; FAIL means the owning suite never executed.
    """
    go_test = verdicts.get("go-test")
    race = verdicts.get("go-test-race")
    if go_test == "PASS":
        ui = "PASS"
        crash = "PASS" if race in ("PASS", None) else "PARTIAL"
    elif go_test == "FAIL":
        ui = "PARTIAL"
        crash = "PARTIAL"
    else:
        ui = "FAIL"
        crash = "FAIL"
    return ui, crash


def build_record(args: argparse.Namespace) -> dict:
    verdicts = {gate: read_gate_verdict(args.log_dir, gate) for gate in set(
        PACKAGE_GATES + QUALITY_GATES + MEDIA_GATES)}

    package_gate_failures = [g for g in PACKAGE_GATES if verdicts.get(g) == "FAIL"]
    quality_gate_failures = [g for g in QUALITY_GATES if verdicts.get(g) == "FAIL"]
    media_gate_failures = [g for g in MEDIA_GATES if verdicts.get(g) == "FAIL"]

    archive_valid, bundled, scan_clean = scan_archive(args.package)
    package_bytes = os.path.getsize(args.package) if archive_valid else 0

    downloader = verdicts.get("media-tools-downloader-tests")
    downloader_fixture = downloader if downloader in ("PASS", "FAIL") else "SKIPPED"
    pinned = verdicts.get("media-tools-pinned-install")
    pinned_integration = pinned if pinned in ("PASS", "FAIL") else "SKIPPED"
    self_check = "PASS" if verdicts.get("run-self-check") == "PASS" else "FAIL"
    h264 = read_proof_result(args.log_dir, "export-proof-h264.result.txt")
    h265 = read_proof_result(args.log_dir, "export-proof-h265.result.txt")
    ui_matrix, crash_matrix = derive_matrix_fields(verdicts)

    commit_verified = args.commit_verified.lower() == "true"
    commit_expected = args.commit_expected.strip() or None
    commit_reason = args.commit_reason.strip()

    hard_failure = (
        (not commit_verified)
        or bool(package_gate_failures)
        or (not archive_valid)
        or (not scan_clean)
        or bundled
    )
    verified_core = (
        commit_verified
        and not package_gate_failures
        and archive_valid
        and scan_clean
        and not bundled
        and downloader_fixture == "PASS"
        and self_check == "PASS"
        and h264 == "PASS"
        and h265 == "PASS"
    )
    soft_failure = bool(quality_gate_failures) or bool(media_gate_failures) or (not verified_core)

    if hard_failure:
        status = "UNAVAILABLE"
        reasons = []
        if not commit_verified:
            reasons.append("commit identity unverified")
        if package_gate_failures:
            reasons.append("failed package gates: " + ", ".join(package_gate_failures))
        if not archive_valid:
            reasons.append("archive missing or unreadable")
        if not scan_clean:
            reasons.append("archive scan violations (dll under tools/, nested archive or oversized file)")
        if bundled:
            reasons.append("bundled media-tool binaries detected in the archive")
        commit_reason = (commit_reason + "; " if commit_reason else "") + "UNAVAILABLE: " + "; ".join(reasons)
    elif verified_core and not soft_failure:
        status = "VERIFIED"
    else:
        status = "TEST_ONLY"

    return {
        "platform": args.platform,
        "package_status": status,
        "package_file": args.package_file,
        "package_bytes": package_bytes,
        "app_dir": args.app_dir,
        "commit_expected": commit_expected,
        "commit_observed": args.commit_observed,
        "commit_verified": commit_verified,
        "commit_source": args.commit_source,
        "commit_reason": commit_reason,
        "ffmpeg_binaries_bundled": bundled,
        "media_tools_download_supported": downloader_fixture == "PASS",
        "media_tools_downloader_fixture": downloader_fixture,
        "media_tools_pinned_install_integration": pinned_integration,
        "app_launch_self_check": self_check,
        "h264_export_proof": h264,
        "h265_software_export_proof": h265,
        "intel_qsv_hevc_proof": "NOT AVAILABLE",
        "ui_layout_matrix": ui_matrix,
        "crash_fault_injection": crash_matrix,
        "note": NOTE,
    }


def main() -> int:
    parser = argparse.ArgumentParser(description="write the package-status JSON record from persisted evidence")
    parser.add_argument("--platform", required=True, help="e.g. linux-x64 / windows-x64")
    parser.add_argument("--package", required=True, help="path to the produced archive (may not exist)")
    parser.add_argument("--package-file", required=True, help="archive identity recorded in the JSON (repo-relative)")
    parser.add_argument("--app-dir", required=True, help="the single top-level app directory inside the archive")
    parser.add_argument("--log-dir", required=True, help="ci-logs directory (gate-results/ + proof files live here)")
    parser.add_argument("--out", required=True, help="output JSON path")
    parser.add_argument("--commit-expected", default="", help="github.sha expected to be embedded")
    parser.add_argument("--commit-observed", default="not-observed", help="commit observed via --build-info-json")
    parser.add_argument("--commit-verified", required=True, choices=["true", "false"])
    parser.add_argument("--commit-source", default="--build-info-json (built exe + extracted package, typed JSON parse)")
    parser.add_argument("--commit-reason", default="")
    args = parser.parse_args()

    record = build_record(args)
    out_dir = os.path.dirname(os.path.abspath(args.out))
    if out_dir:
        os.makedirs(out_dir, exist_ok=True)
    with open(args.out, "w", encoding="utf-8") as fh:
        json.dump(record, fh, indent=2)
        fh.write("\n")
    print(json.dumps(record))
    return 0


if __name__ == "__main__":
    sys.exit(main())
