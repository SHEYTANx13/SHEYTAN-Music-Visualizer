#!/usr/bin/env python3
"""audit-gate-bodies.py — child-safety audit for build-windows.ps1 gates.

Capture-Step serializes each Capture-Step -Command scriptblock's TEXT into
a CHILD pwsh process (scripts/ci/Capture-Step.ps1). The child sees only
its own strict header, the body text and the exit epilogue; PowerShell
variables from the parent build script NEVER cross that boundary. Under
Set-StrictMode -Version 3.0 a body that references a parent-only variable
dies with

  The variable '$X' cannot be retrieved because it has not been set.

- a message without any "error" substring, so the filtered diagnostic log
never shows the origin (run 45347124889, gate stage-portable reading the
parent-only $shellOk; the same class as run 41915116613's parent-only
$goTags).

The audit extracts every -Command scriptblock by brace matching, strips
comment prose (a comment may legitimately mention a retired variable by
name), and flags $var references that are none of:
  - assigned inside the body (assignment, foreach target, ++/--),
  - automatic ($null / $false / $true / $env: prefix),
  - $goTags with the gate declaring -GoTags (generated preamble defines it),
  - $repoRoot with the gate declaring -RequireRepoRoot (preamble defines it).

Exit 0 = clean; exit 1 = at least one parent-only reference (with the
exact gate and variable named).
"""
import re
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parents[3]
TARGET = REPO / "scripts" / "build" / "build-windows.ps1"

src = TARGET.read_bytes().decode("utf-8")

AUTOMATIC = {"_", "false", "null", "true", "LASTEXITCODE"}

blocks = []
for m in re.finditer(r'-Step "([A-Za-z0-9_.-]+)".*?-Command \{', src):
    start = m.end() - 1
    depth = 0
    i = start
    while i < len(src):
        c = src[i]
        if c == "{":
            depth += 1
        elif c == "}":
            depth -= 1
            if depth == 0:
                break
        i += 1
    body = src[start + 1:i]
    # Comment lines are prose, not code.
    body = "\n".join(ln for ln in body.split("\n") if not ln.lstrip().startswith("#"))
    step_start = src.rfind('-Step "' + m.group(1) + '"')
    step_ctx = src[step_start:step_start + 500]
    blocks.append((m.group(1), body, step_ctx))

problems = []
for name, body, ctx in blocks:
    assigned = set(re.findall(r"\$(\w+)\s*=(?!=)", body))
    assigned |= set(re.findall(r"foreach\s+\(\$(\w+)\s+in", body))
    assigned |= set(re.findall(r"\$(\w+)\s*(?:\+\+|--)", body))
    assigned |= set(re.findall(r"param\(([^)]*)\)", body) and set(re.findall(r"\$(\w+)", " ".join(re.findall(r"param\(([^)]*)\)", body)))) or set())
    for v in sorted(set(re.findall(r"\$(\w+)", body))):
        if v == "env":
            continue  # "$env:NAME" — \w+ stops at the colon
        if v in AUTOMATIC or v in assigned:
            continue
        if v == "goTags" and "-GoTags" in ctx:
            continue  # defined by the generated -GoTags preamble
        if v == "repoRoot" and "-RequireRepoRoot" in ctx:
            continue  # defined by the generated -RequireRepoRoot preamble
        problems.append((name, v))

if problems:
    print("ERROR: build-windows.ps1 gate bodies reference parent-only variables")
    print("       (Capture-Step runs each gate in a CHILD pwsh process; parent")
    print("        variables never cross the boundary and Set-StrictMode makes")
    print("        the read a silent death with no 'error' substring):")
    seen = set()
    for gate, v in problems:
        if (gate, v) in seen:
            continue
        seen.add((gate, v))
        print(f"         gate '{gate}': ${v}")
    print("       fix: derive the state in-body (filesystem checks, env:) or pass")
    print("       it through the documented env sources (SHEYTAN_GO_TAGS,")
    print("       SHEYTAN_REPO_ROOT).")
    sys.exit(1)
print(f"OK: {len(blocks)} gate bodies in {TARGET.name} are child-safe (no parent-only variable references)")
