#!/usr/bin/env python3
"""validate-candidates.py — publisher-side re-validation of the four
candidate packages (priority-B one-final-publisher contract).

Fail-closed: a missing, corrupt, wrong-content or implausibly small
candidate refuses publication (a red run publishes ONLY the diagnostics
artifact). Exit 0 = all four candidates valid.
"""
import os
import sys
import tarfile
import zipfile

MIN_BYTES = 1024 * 1024


def check_zip(path, want_primary, label, markers):
    if not os.path.isfile(path) or os.path.getsize(path) == 0:
        print(f"ERROR: {label} candidate missing or empty")
        return False
    try:
        z = zipfile.ZipFile(path)
        if z.testzip() is not None:
            print(f"ERROR: corrupt {label} zip entry")
            return False
        data = b"".join(z.read(n) for n in z.namelist() if z.getinfo(n).file_size < 64 * 1024 * 1024)
        for marker in markers:
            if marker not in data.lower():
                print(f"ERROR: {label} package lacks {marker.decode()!r} content marker")
                return False
        print(f"{label} zip ok: {len(z.namelist())} entries")
        return True
    except Exception as e:  # noqa: BLE001 — fail-closed validator
        print(f"ERROR: {label}: {e}")
        return False


def main(argv):
    wz, lt, mt, at = argv[1:5]
    ok = True

    ok &= check_zip(wz, True, "windows", [b"sheytan-music-visualizer"])
    ok &= check_zip(at, True, "android apk", [b"androidmanifest.xml", b"arm64-v8a"])

    if not os.path.isfile(lt) or os.path.getsize(lt) == 0:
        print("ERROR: linux candidate missing or empty")
        ok = False
    else:
        try:
            tf = tarfile.open(lt, "r:gz")
            names = tf.getnames()
            if not any("sheytan-music-visualizer" in n for n in names):
                print("ERROR: Linux package lacks the primary executable")
                ok = False
            else:
                print(f"linux tar.gz ok: {len(names)} members")
        except Exception as e:  # noqa: BLE001
            print(f"ERROR: linux tar.gz: {e}")
            ok = False

    if not os.path.isfile(mt) or os.path.getsize(mt) == 0:
        print("ERROR: macOS candidate missing or empty")
        ok = False
    else:
        try:
            z = zipfile.ZipFile(mt)
            if z.testzip() is not None:
                print("ERROR: corrupt macOS zip entry")
                ok = False
            else:
                names = z.namelist()
                if not any(".app/Contents/Info.plist" in n for n in names):
                    print("ERROR: macOS package lacks a real .app bundle (Info.plist)")
                    ok = False
                elif not any(".app/Contents/MacOS/" in n for n in names):
                    print("ERROR: macOS package lacks the app executable")
                    ok = False
                else:
                    print(f"macos app zip ok: {len(names)} entries")
        except Exception as e:  # noqa: BLE001
            print(f"ERROR: macos app zip: {e}")
            ok = False

    for f in (wz, lt, mt, at):
        size = os.path.getsize(f) if os.path.exists(f) else 0
        if size < MIN_BYTES:
            print(f"ERROR: implausibly small package {f} ({size} bytes)")
            ok = False

    if not ok:
        print("PUBLICATION REFUSED: one or more candidates are missing or invalid; a failed run publishes ONLY the diagnostics artifact.")
        return 1
    print("all four candidates validated; staging final canonical names")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
