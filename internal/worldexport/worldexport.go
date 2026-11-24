// Package worldexport renders a per-track world plan (internal/mvplan)
// plus the asset catalog (internal/worldassets) into Wavefront OBJ +
// MTL files that import cleanly into Blender (y-up, 1 unit = 1 meter,
// one named object per element, floor quads per chapter, material
// roles resolved to brand token colors).
//
// Honesty contract: this is a geometry/metadata export for DCC tools.
// It contains no audio data and no render claims; the exported scene
// is the deterministic plan a music video consumes.
package worldexport

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strings"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/mvplan"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/worldassets"
)

// Material is one exported MTL entry (role name -> token-derived RGBA,
// written as flat RGB diffuse color; MTL has no alpha in Ka/Kd).
type Material struct {
	Role  string
	Color [3]float64 // 0..1 linear RGB
}

// roleColors maps asset material role names onto the brand palette
// (design tokens, resolved here as exporter constants so the OBJ is
// self-contained; the token values come from design/tokens.json).
var roleColors = map[string][3]float64{
	"obsidian":       {0x08 / 255.0, 0x08 / 255.0, 0x08 / 255.0},
	"deepCharcoal":   {0x11 / 255.0, 0x11 / 255.0, 0x11 / 255.0},
	"volcanic":       {0x19 / 255.0, 0x09 / 255.0, 0x09 / 255.0},
	"darkBlood":      {0x9F / 255.0, 0x0B / 255.0, 0x18 / 255.0},
	"infernalRed":    {0xD5 / 255.0, 0x1D / 255.0, 0x2A / 255.0},
	"emberHighlight": {0xFF / 255.0, 0x45 / 255.0, 0x3A / 255.0},
	"hotEmber":       {0xFF / 255.0, 0x70 / 255.0, 0x43 / 255.0},
	"antiqueGold":    {0xB9 / 255.0, 0x8A / 255.0, 0x3E / 255.0},
}

// Result summarizes one export (surfaced on the CLI/stdout).
type Result struct {
	Objects  int
	Vertices int
	Faces    int
	Lines    int
	Material []string
}

// Options tunes the OBJ emission.
type Options struct {
	FloorTiles int // tile subdivision for floor quads (bounded 1..32)
}

// sanitizeName makes an OBJ-safe object name (ASCII letters, digits,
// underscore; OBJ names avoid spaces for Blender import clarity).
func sanitizeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '/', r == '.', r == ' ':
			b.WriteByte('_')
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "object"
	}
	return b.String()
}

// writer accumulates the OBJ text with bounded, deterministic output.
type writer struct {
	sb       strings.Builder
	vertices int
	faces    int
	lines    int
}

func (w *writer) v(x, y, z float64) {
	fmt.Fprintf(&w.sb, "v %.6f %.6f %.6f\n", x, y, z)
	w.vertices++
}

func (w *writer) f(indices ...int) {
	fmt.Fprintf(&w.sb, "f")
	for _, i := range indices {
		fmt.Fprintf(&w.sb, " %d", i)
	}
	fmt.Fprintf(&w.sb, "\n")
	w.faces++
}

func (w *writer) l(a, b int) {
	fmt.Fprintf(&w.sb, "l %d %d\n", a, b)
	w.lines++
}

// Export writes the plan's world as an OBJ document to objW and the
// companion MTL to mtlW. It returns a summary. The output is
// deterministic for the same plan (no timestamps).
func Export(plan *mvplan.Plan, objW, mtlW io.Writer, opts Options) (Result, error) {
	if plan == nil {
		return Result{}, fmt.Errorf("worldexport: nil plan")
	}
	if opts.FloorTiles <= 0 {
		opts.FloorTiles = 12
	}
	if opts.FloorTiles > 32 {
		opts.FloorTiles = 32
	}

	w := &writer{}
	fmt.Fprintf(&w.sb, "# SHEYTAN Music Visualizer world plan export\n")
	fmt.Fprintf(&w.sb, "# track fingerprint: %s\n", plan.TrackFingerprint)
	fmt.Fprintf(&w.sb, "# plan seed: %d chapters: %d elements: %d\n", plan.Seed, len(plan.Chapters), len(plan.Elements))
	fmt.Fprintf(&w.sb, "# y-up, 1 unit = 1 meter; import into Blender with default settings\n")
	fmt.Fprintf(&w.sb, "mtllib sheytan_world.mtl\n")

	// Materials actually referenced (stable sorted order).
	used := map[string]bool{}

	// 1. Chapter floors: tiled quad grids centered at the origin,
	//    stacked at y = -chapter*0.01 so overlapping chapters stay
	//    distinguishable in Blender's outliner.
	chapters := plan.Chapters
	for _, ch := range chapters {
		if ch.Floor == "" {
			continue
		}
		asset, ok := worldassets.Get(ch.Floor)
		if !ok {
			continue
		}
		used[asset.Material] = true
		mat := "mat_" + sanitizeName(asset.Material)
		fmt.Fprintf(&w.sb, "o floor_ch%02d_%s\n", ch.Index, sanitizeName(strings.TrimPrefix(ch.Floor, "floor/")))
		fmt.Fprintf(&w.sb, "usemtl %s\n", mat)
		emitFloorGrid(w, opts.FloorTiles, float64(ch.Index)*-0.01)
		// Inlay line-work (ritual rings, cracks) as OBJ lines.
		for _, seg := range asset.Segments {
			a := w.emitVertexAbs(seg[0][0]*4.0, 0.002+float64(ch.Index)*-0.01, seg[0][1]*4.0)
			b := w.emitVertexAbs(seg[1][0]*4.0, 0.002+float64(ch.Index)*-0.01, seg[1][1]*4.0)
			w.l(a, b)
		}
	}

	// 2. Elements: sigils become extruded prisms, trees become line
	//    skeletons, abstract kinds become bound marker quads sized by
	//    the element scale (the renderers draw the real forms).
	for _, el := range plan.Elements {
		fmt.Fprintf(&w.sb, "o el_%s_%s\n", sanitizeName(el.ID), sanitizeName(string(el.Kind)))
		switch {
		case el.Asset != "":
			asset, ok := worldassets.Get(el.Asset)
			if !ok {
				fmt.Fprintf(&w.sb, "# asset %q missing from catalog; skipped\n", el.Asset)
				continue
			}
			used[asset.Material] = true
			mat := "mat_" + sanitizeName(asset.Material)
			fmt.Fprintf(&w.sb, "usemtl %s\n", mat)
			if asset.Family == worldassets.FamilyTree {
				emitTreeLines(w, asset, el)
			} else {
				emitSigilPrism(w, asset, el)
			}
		default:
			// Abstract geometry family: an axis-aligned marker box at the
			// element position, scaled by the element scale.
			fmt.Fprintf(&w.sb, "usemtl mat_infernalRed\n")
			used["infernalRed"] = true
			emitMarkerBox(w, el)
		}
	}

	// 3. MTL: every referenced role, sorted.
	var roles []string
	for r := range used {
		roles = append(roles, r)
	}
	sort.Strings(roles)
	fmt.Fprintf(mtlW, "# SHEYTAN Music Visualizer world materials (brand tokens)\n")
	for _, r := range roles {
		c := roleColors[r]
		if c == ([3]float64{}) {
			c = roleColors["infernalRed"]
		}
		fmt.Fprintf(mtlW, "newmtl mat_%s\n", sanitizeName(r))
		fmt.Fprintf(mtlW, "Kd %.4f %.4f %.4f\n", c[0], c[1], c[2])
		fmt.Fprintf(mtlW, "Ka 0.0000 0.0000 0.0000\n")
		fmt.Fprintf(mtlW, "Ks 0.0500 0.0500 0.0500\n")
		fmt.Fprintf(mtlW, "Ns 10\n\n")
	}

	_, err := io.WriteString(objW, w.sb.String())
	if err != nil {
		return Result{}, fmt.Errorf("worldexport: writing OBJ: %w", err)
	}
	return Result{
		Objects:  len(plan.Chapters) + len(plan.Elements),
		Vertices: w.vertices,
		Faces:    w.faces,
		Lines:    w.lines,
		Material: roles,
	}, nil
}

// emitVertexAbs appends one absolute vertex and returns its 1-based
// OBJ index.
func (w *writer) emitVertexAbs(x, y, z float64) int {
	w.v(x, y, z)
	return w.vertices
}

// emitFloorGrid writes an N x N quad grid (each tile one quad face)
// centered at the origin on plane y=y0 with 8 m half-span.
func emitFloorGrid(w *writer, n int, y0 float64) {
	const half = 4.0
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			x0 := -half + (2*half)*float64(i)/float64(n)
			x1 := -half + (2*half)*float64(i+1)/float64(n)
			z0 := -half + (2*half)*float64(j)/float64(n)
			z1 := -half + (2*half)*float64(j+1)/float64(n)
			a := w.emitVertexAbs(x0, y0, z0)
			b := w.emitVertexAbs(x1, y0, z0)
			c := w.emitVertexAbs(x1, y0, z1)
			d := w.emitVertexAbs(x0, y0, z1)
			w.f(a, b, c, d)
		}
	}
}

// emitSigilPrism extrudes the sigil outline into a low prism (caps +
// side walls) positioned/scaled by the element.
func emitSigilPrism(w *writer, a worldassets.Asset, el mvplan.Element) {
	if len(a.Outline) == 0 {
		// Line-only glyphs: emit the segments as prism-less lines.
		emitPlanarLines(w, a.Segments, el)
		return
	}
	h := a.Height
	if h <= 0 {
		h = 0.05
	}
	base := w.vertices
	for _, p := range a.Outline {
		x := el.Position[0] + p[0]*el.Scale[0]
		z := el.Position[2] + p[1]*el.Scale[0]
		w.v(x, el.Position[1], z)
	}
	for _, p := range a.Outline {
		x := el.Position[0] + p[0]*el.Scale[0]
		z := el.Position[2] + p[1]*el.Scale[0]
		w.v(x, el.Position[1]+h*math.Min(el.Scale[1], 3), z)
	}
	n := len(a.Outline)
	// Bottom cap (reversed winding for downward normal) and top cap.
	for i := 1; i < n-1; i++ {
		w.f(base+1, base+i+1, base+i+2)
		w.f(base+n+1, base+n+i+2, base+n+i+1)
	}
	// Side walls.
	for i := 0; i < n; i++ {
		j := (i + 1) % n
		w.f(base+i+1, base+j+1, base+n+j+1, base+n+i+1)
	}
	// Interior glyph line-work as elevated lines.
	emitPlanarLines(w, a.Segments, el)
}

// emitPlanarLines writes asset segments as OBJ lines mapped through
// the element position/scale.
func emitPlanarLines(w *writer, segs [][2][3]float64, el mvplan.Element) {
	for _, seg := range segs {
		ax := el.Position[0] + seg[0][0]*el.Scale[0]
		ay := el.Position[1] + seg[0][1]*el.Scale[1]
		az := el.Position[2] + seg[0][2]*el.Scale[0]
		bx := el.Position[0] + seg[1][0]*el.Scale[0]
		by := el.Position[1] + seg[1][1]*el.Scale[1]
		bz := el.Position[2] + seg[1][2]*el.Scale[0]
		w.l(w.emitVertexAbs(ax, ay, az), w.emitVertexAbs(bx, by, bz))
	}
}

// emitTreeLines writes the tree skeleton as OBJ lines (Blender:
// select edges, Convert to Curve, bevel for branches).
func emitTreeLines(w *writer, a worldassets.Asset, el mvplan.Element) {
	for _, seg := range a.Segments {
		ax := el.Position[0] + seg[0][0]*el.Scale[0]
		ay := el.Position[1] + seg[0][1]*el.Scale[1]
		az := el.Position[2] + seg[0][2]*el.Scale[0]
		bx := el.Position[0] + seg[1][0]*el.Scale[0]
		by := el.Position[1] + seg[1][1]*el.Scale[1]
		bz := el.Position[2] + seg[1][2]*el.Scale[0]
		w.l(w.emitVertexAbs(ax, ay, az), w.emitVertexAbs(bx, by, bz))
	}
}

// emitMarkerBox writes an axis-aligned box at the element position.
func emitMarkerBox(w *writer, el mvplan.Element) {
	sx, sy, sz := el.Scale[0]*0.5, el.Scale[1]*0.5, el.Scale[2]*0.5
	cx, cy, cz := el.Position[0], el.Position[1], el.Position[2]
	corners := [8][3]float64{
		{cx - sx, cy - sy, cz - sz}, {cx + sx, cy - sy, cz - sz},
		{cx + sx, cy - sy, cz + sz}, {cx - sx, cy - sy, cz + sz},
		{cx - sx, cy + sy, cz - sz}, {cx + sx, cy + sy, cz - sz},
		{cx + sx, cy + sy, cz + sz}, {cx - sx, cy + sy, cz + sz},
	}
	idx := make([]int, 8)
	for i, c := range corners {
		idx[i] = w.emitVertexAbs(c[0], c[1], c[2])
	}
	w.f(idx[0], idx[1], idx[2], idx[3]) // bottom
	w.f(idx[4], idx[7], idx[6], idx[5]) // top
	w.f(idx[0], idx[3], idx[7], idx[4]) // -x
	w.f(idx[1], idx[5], idx[6], idx[2]) // +x
	w.f(idx[0], idx[4], idx[5], idx[1]) // -z
	w.f(idx[3], idx[2], idx[6], idx[7]) // +z
}
