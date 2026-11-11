#!/usr/bin/env bash
# test-docs-consistency.sh — deterministic documentation consistency gate
# (run 42939743766 §7). No network, no crawler: pure local checks.
#
# Checks:
#   1. The canonical CI artifact names documented in the docs match the
#      paths the workflows actually upload.
#   2. Every relative doc link inside readme.md and docs/*.md resolves to
#      an existing tracked file.
#   3. Obsolete artifact names / tool versions no longer appear anywhere
#      in the tracked docs.
set -uo pipefail
pass=0; fail=0
ok()  { pass=$((pass+1)); echo "PASS    $1"; }
bad() { fail=$((fail+1)); echo "FAIL    $1"; }

repo_root="$(cd "$(dirname "$0")/../../.." && pwd)"
cd "$repo_root"

# 1. canonical artifact names agree between docs and workflow
for name in \
  'SHEYTAN-Music-Visualizer-windows-x64.zip' \
  'SHEYTAN-Music-Visualizer-linux-x64.tar.gz' \
  'SHEYTAN-Music-Visualizer-all-logs.txt'; do
  if grep -q "$name" .github/workflows/build-desktop.yml && grep -rq "$name" docs/ readme.md; then
    ok "artifact name documented and wired: $name"
  else
    bad "artifact name missing in workflow or docs: $name"
  fi
done

# 2. relative markdown links resolve (readme.md + docs/*.md)
link_re='\]\(([^)#]+)(#[^)]*)?\)'
check_links_in() {
  local f="$1" dir link target
  dir="$(dirname "$f")"
  while IFS= read -r link; do
    case "$link" in
      http://*|https://*|mailto:*|\/*) continue ;;
    esac
    target="$dir/$link"
    if [ ! -e "$target" ]; then
      bad "broken doc link in $f: $link"
      return 0
    fi
  done < <(sed -nE "s/.*$link_re.*/\1/p" "$f")
}
for f in readme.md agent.md roadmap.md ai-notes.md CHANGELOG.md docs/*.md; do
  [ -f "$f" ] || continue
  check_links_in "$f"
done
ok "relative doc links checked (any failure listed above)"

# 3. obsolete names/versions must not appear in tracked docs
obsolete=(
  'windows-x64-recovery-executable'
  'windows-x64-recovery-staged-package'
  'linux-x64-portable-build'
  'windows-x64-portable-build'
  'SHEYTAN-Music-Visualizer-windows-x64-portable.zip'
  'SHEYTAN-Music-Visualizer-linux-x64-portable.tar.gz'
  'zizmor 1.16.2'
  'zizmor==1.16.2'
)
found_obsolete=0
python3 - "${obsolete[@]}" <<'PYOBS'
import os, sys
patterns = sys.argv[1:]
skip_keys = ("retired", "superseded", "replaced the run", "are gone", "previous", "then-pinned")
bad = 0
for root, dirs, files in os.walk("."):
    if "./.git" in root:
        continue
    for fn in files:
        if not fn.endswith(".md"):
            continue
        path = os.path.join(root, fn)
        in_hist = False
        for lineno, line in enumerate(open(path, encoding="utf-8", errors="replace"), 1):
            if "BEGIN HISTORICAL RECORD" in line:
                in_hist = True
                continue
            if "END HISTORICAL RECORD" in line:
                in_hist = False
                continue
            if in_hist:
                continue
            if any(k in line.lower() for k in skip_keys):
                continue
            for pat in patterns:
                if pat in line:
                    print(f"BAD {path}:{lineno}: {pat}")
                    bad += 1
sys.exit(0 if bad == 0 else 1)
PYOBS
if [ $? -ne 0 ]; then
  bad "obsolete names/versions present in current doc text (see BAD lines above)"
else
  ok "no obsolete artifact names or tool versions in current doc text"
fi

echo "SUMMARY: pass=$pass fail=$fail"
if [ "$fail" -gt 0 ]; then exit 1; fi
exit 0
