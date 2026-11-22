#!/usr/bin/env python3
"""validate-apk.py — APK package validation (priority-B Android gate).

Checks, fail-closed:
  - the file exists, is non-empty and is a structurally valid ZIP;
  - AndroidManifest.xml is present;
  - classes*.dex entries are present;
  - the arm64-v8a ABI has native libraries;
  - the package is large enough to be a real application package.

Exit codes: 0 valid; 1 invalid (reason printed to stdout, sanitized
upstream by the capture pipeline).
"""
import os
import sys
import zipfile


def main(argv):
    if len(argv) != 2:
        print("usage: validate-apk.py <apk>")
        return 2
    path = argv[1]
    if not os.path.isfile(path) or os.path.getsize(path) == 0:
        print("ERROR: APK missing or empty")
        return 1
    size = os.path.getsize(path)
    print(f"apk_size_bytes={size}")
    if size < 1048576:
        print("ERROR: implausibly small APK (< 1 MiB)")
        return 1
    try:
        z = zipfile.ZipFile(path)
    except zipfile.BadZipFile as e:
        print(f"ERROR: not a valid ZIP/APK: {e}")
        return 1
    bad = z.testzip()
    if bad is not None:
        print(f"ERROR: corrupt APK entry {bad}")
        return 1
    names = z.namelist()
    for prefix in ("lib/arm64-v8a/", "classes", "AndroidManifest.xml"):
        if not any(n.startswith(prefix) for n in names):
            print(f"ERROR: APK missing {prefix}*")
            return 1
    abi = [n for n in names if n.startswith("lib/arm64-v8a/")]
    print(f"apk entries: {len(names)}")
    print(f"arm64 ABI native libraries: {len(abi)} (lib/arm64-v8a/*)")
    print("AndroidManifest.xml: present; classes*: present")
    print("APK_VALIDATION_OK")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
