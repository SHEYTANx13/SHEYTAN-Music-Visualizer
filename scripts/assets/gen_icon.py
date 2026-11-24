#!/usr/bin/env python3
"""gen_icon.py - deterministic brand icon generator for SHEYTAN Music Visualizer.

Draws the canonical application icon from the design token palette
(design/tokens.json) and writes:
  assets/icon/icon.png   512x512 RGBA (window icon, macOS iconset source,
                         gomobile assets/icon.png source)
  assets/icon/icon.ico   multi-size ICO (16/24/32/48/64/128/256) for the
                         Windows executable resource (.syso via windres)
  assets/icon/iconset/*.png  macOS iconset for iconutil (icns), EXACTLY
                         the documented Apple iconset table: every
                         canonical name present and every file's pixel
                         dimensions equal to the size its name declares
                         (@2x entries are 2x the base size). iconutil
                         refuses a set whose file dimensions contradict
                         the names (run 45347124889: icon_512x512@2x.png
                         was written at 512x512, the set was missing the
                         16/32/128/256 @2x entries and the 512 base, and
                         it carried a non-standard icon_64x64.png - the
                         hosted package-macos gate failed inside
                         iconutil and no .app artifact was produced).

The mark: a black obsidian rounded square, a red INVERTED pentagram
(the Luciferian brand mark, matching internal/worldassets sigil
geometry) and an ember ring. Deterministic: same inputs, same bytes on
every platform and run (no timestamps, no randomness, stable PNG/ICO
encoding).

Usage: python3 scripts/assets/gen_icon.py  (from the repository root)
Requires Pillow; a PIL-unavailable environment re-runs the committed
assets unchanged (the assets are tracked source, not build products).
"""
import math
import os
import struct
import sys

TOKENS = os.path.join(os.path.dirname(__file__), "..", "..", "design", "tokens.json")


def load_palette():
    import json
    with open(TOKENS, "r", encoding="utf-8") as f:
        doc = json.load(f)
    c = doc["color"]
    def hx(s):
        s = s.lstrip("#")
        return (int(s[0:2], 16), int(s[2:4], 16), int(s[4:6], 16), 255)
    return {
        "obsidian": hx(c["surface"]["obsidian"]),
        "volcanic": hx(c["surface"]["volcanic"]),
        "infernalRed": hx(c["accent"]["infernalRed"]),
        "darkBlood": hx(c["accent"]["darkBlood"]),
        "ember": hx(c["accent"]["emberHighlight"]),
        "gold": (0xC9, 0xA2, 0x27, 255),  # antiqueGold tone from the token scale
    }


def draw_icon(size):
    """Render the brand mark at `size` px into an RGBA PIL image."""
    from PIL import Image, ImageDraw
    pal = load_palette()
    img = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)

    # Obsidian rounded-square plate with a dark-blood inner border.
    m = max(1, size // 32)
    radius = size // 5
    d.rounded_rectangle([m, m, size - 1 - m, size - 1 - m], radius=radius,
                        fill=pal["obsidian"])
    inset = size // 14
    d.rounded_rectangle([inset, inset, size - 1 - inset, size - 1 - inset],
                        radius=radius * 3 // 4, outline=pal["darkBlood"],
                        width=max(1, size // 64))

    # Inverted pentagram: two points up, one down (the Luciferian mark,
    # matching internal/worldassets SigilPentagramInverted).
    cx, cy = size / 2.0, size / 2.0
    r = size * 0.34
    pts = []
    for i in range(5):
        # 306deg start places two vertices up and one down on a
        # y-down canvas (the inverted orientation).
        a = math.radians(306 + i * 72)
        pts.append((cx + r * math.cos(a), cy + r * math.sin(a)))
    line_w = max(2, size // 42)
    order = [0, 2, 4, 1, 3, 0]  # pentagram stroke order
    stroke = [pts[i] for i in order]
    # Dark blood under-stroke for depth, infernal red core on top.
    d.line(stroke, fill=pal["darkBlood"], width=line_w * 2, joint="curve")
    d.line(stroke, fill=pal["infernalRed"], width=line_w, joint="curve")

    # Ember ring around the sigil.
    ring_r = r * 1.22
    d.ellipse([cx - ring_r, cy - ring_r, cx + ring_r, cy + ring_r],
              outline=pal["ember"], width=max(1, size // 96))

    # Gold caps on the two upturned horns (vertices 0 and 4).
    for idx in (0, 4):
        x, y = pts[idx]
        rr = max(1, size // 60)
        d.ellipse([x - rr, y - rr, x + rr, y + rr], fill=pal["gold"])

    return img


def png_bytes(img):
    import io
    b = io.BytesIO()
    img.save(b, format="PNG", optimize=False)
    return b.getvalue()


def make_ico(sizes, render):
    """Build a multi-size ICO whose entries are PNG-encoded images
    (valid per the Vista+ ICO specification; modern Windows Explorer,
    shortcuts and the taskbar render PNG entries directly)."""
    entries = []
    for s in sizes:
        entries.append((s, png_bytes(render(s))))
    header = struct.pack("<HHH", 0, 1, len(entries))
    offset = 6 + 16 * len(entries)
    blobs = []
    for s, data in entries:
        w = 0 if s >= 256 else s
        h = 0 if s >= 256 else s
        header += struct.pack("<BBBBHHII", w, h, 0, 0, 1, 32,
                              len(data), offset)
        blobs.append(data)
        offset += len(data)
    return header + b"".join(blobs)


def main():
    try:
        from PIL import Image  # noqa: F401
    except ImportError:
        print("Pillow not available; the committed icon assets are "
              "authoritative. Install Pillow to regenerate.", file=sys.stderr)
        return 0
    root = os.path.join(os.path.dirname(__file__), "..", "..")
    out_dir = os.path.join(root, "assets", "icon")
    iconset_dir = os.path.join(out_dir, "iconset")
    os.makedirs(out_dir, exist_ok=True)

    img512 = draw_icon(512)
    with open(os.path.join(out_dir, "icon.png"), "wb") as f:
        f.write(png_bytes(img512))

    sizes = [16, 24, 32, 48, 64, 128, 256]
    with open(os.path.join(out_dir, "icon.ico"), "wb") as f:
        f.write(make_ico(sizes, draw_icon))

    os.makedirs(iconset_dir, exist_ok=True)
    # The documented Apple iconset table (man iconutil, Apple TN): name ->
    # required pixel dimensions. iconutil validates both directions - a
    # missing canonical name and a file whose dimensions contradict its
    # name both fail the icns conversion.
    iconset = [
        ("icon_16x16.png", 16),
        ("icon_16x16@2x.png", 32),
        ("icon_32x32.png", 32),
        ("icon_32x32@2x.png", 64),
        ("icon_128x128.png", 128),
        ("icon_128x128@2x.png", 256),
        ("icon_256x256.png", 256),
        ("icon_256x256@2x.png", 512),
        ("icon_512x512.png", 512),
        ("icon_512x512@2x.png", 1024),
    ]
    canonical = set(name for name, _ in iconset)
    # Remove any stale non-canonical entry (e.g. the retired
    # icon_64x64.png) so the tracked set stays exactly the table.
    for stale in os.listdir(iconset_dir):
        if stale.endswith(".png") and stale not in canonical:
            os.remove(os.path.join(iconset_dir, stale))
    for name, px in iconset:
        with open(os.path.join(iconset_dir, name), "wb") as f:
            f.write(png_bytes(draw_icon(px)))
    print("icon assets regenerated:", out_dir)
    return 0


if __name__ == "__main__":
    sys.exit(main())
