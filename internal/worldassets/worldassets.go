// Package worldassets owns the canonical 3D world asset catalog:
// luciferian sigils, ritual trees and floor variants, expressed as
// deterministic, framework-independent geometry data (unit-space
// outlines, segment skeletons and pattern descriptors).
//
// Design contract (run 45182658992):
//   - Assets are DATA. No goroutines, no signal analysis, no
//     reactivity: renderers and the Blender export (internal/worldexport)
//     consume them at their own discretion.
//   - Every asset is deterministic: the same ID always yields the same
//     geometry (pure functions of authored constants; no rng, no time).
//   - Geometry is unit-space ([-0.5, 0.5] footprint, y-up); the
//     exporter scales it by the plan element's Scale.
//   - Materials are ROLE NAMES resolved against the design tokens by
//     the exporter — never hard-coded hex values here.
package worldassets

import (
	"fmt"
	"math"
	"sort"
)

// Family is one asset family.
type Family string

// Asset families (the three the world builder composes from).
const (
	FamilySigil Family = "sigil"
	FamilyTree  Family = "tree"
	FamilyFloor Family = "floor"
)

// Pattern descriptors for floor assets.
const (
	PatternTiles  = "tiles"  // N x N tile quads
	PatternRings  = "rings"  // concentric ritual rings on a base quad
	PatternCracks = "cracks" // base quad with deterministic crack polylines
)

// Asset is one world object definition.
//
// For sigils and floors, Outline is the closed unit-space loop and
// Segments carries interior line work (star lines, ring inlays,
// cracks). For trees, Segments is the 3D trunk/branch skeleton and
// Outline is empty. Height is the 3D extrusion hint (sigils/trees,
// world units at scale 1); floors are flat (Height 0).
type Asset struct {
	ID          string          `json:"id"`
	Family      Family          `json:"family"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Material    string          `json:"material"` // material role name, resolved by the exporter
	Outline     [][2]float64    `json:"outline,omitempty"`
	Segments    [][2][3]float64 `json:"segments,omitempty"`
	Height      float64         `json:"height,omitempty"`
	Pattern     string          `json:"pattern,omitempty"` // floor assets only
	PatternN    int             `json:"patternN,omitempty"`
}

// Catalog returns the full catalog sorted by ID (stable order).
func Catalog() []Asset {
	out := make([]Asset, len(catalog))
	copy(out, catalog)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Get returns one asset by ID.
func Get(id string) (Asset, bool) {
	for _, a := range catalog {
		if a.ID == id {
			return a, true
		}
	}
	return Asset{}, false
}

// IDsForFamily returns the sorted asset IDs of one family.
func IDsForFamily(f Family) []string {
	var ids []string
	for _, a := range catalog {
		if a.Family == f {
			ids = append(ids, a.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

// ValidateAsset is the catalog invariant check (used by tests and the
// self-check path): IDs are unique and namespaced "<family>/<name>",
// geometry stays in unit space, and floor patterns are known.
func ValidateAsset(a Asset) error {
	if a.ID == "" {
		return fmt.Errorf("asset id empty")
	}
	if a.Family != FamilySigil && a.Family != FamilyTree && a.Family != FamilyFloor {
		return fmt.Errorf("asset %s: unknown family %q", a.ID, a.Family)
	}
	wantPrefix := string(a.Family) + "/"
	if len(a.ID) <= len(wantPrefix) || a.ID[:len(wantPrefix)] != wantPrefix {
		return fmt.Errorf("asset id %q is not namespaced under %q", a.ID, wantPrefix)
	}
	for i, p := range a.Outline {
		if !inUnit(p[0]) || !inUnit(p[1]) {
			return fmt.Errorf("asset %s: outline point %d outside unit space", a.ID, i)
		}
	}
	for i, s := range a.Segments {
		for _, p := range s {
			if !inUnit(p[0]) || !inUnit(p[1]) || !inUnit(p[2]) {
				return fmt.Errorf("asset %s: segment %d endpoint outside unit space", a.ID, i)
			}
		}
	}
	if a.Family == FamilyFloor && a.Pattern != "" {
		switch a.Pattern {
		case PatternTiles, PatternRings, PatternCracks:
		default:
			return fmt.Errorf("asset %s: unknown floor pattern %q", a.ID, a.Pattern)
		}
	}
	return nil
}

func inUnit(v float64) bool { return v >= -0.500001 && v <= 0.500001 }

// --- deterministic geometry helpers (pure math, no rng) ---

// starLines returns the {5/2} pentagram line loop: each vertex of a
// regular pentagon connects to the vertex two steps away. phase is the
// first vertex angle.
func starLines(phase float64) [][2][3]float64 {
	const n, step = 5, 2
	segs := make([][2][3]float64, 0, n)
	pts := make([][2]float64, n)
	for i := 0; i < n; i++ {
		a := phase + float64(i)*2*math.Pi/float64(n)
		pts[i] = [2]float64{0.5 * math.Cos(a), 0.5 * math.Sin(a)}
	}
	for i := 0; i < n; i++ {
		j := (i + step) % n
		segs = append(segs, [2][3]float64{
			{pts[i][0], pts[i][1], 0},
			{pts[j][0], pts[j][1], 0},
		})
	}
	return segs
}

// flipY mirrors unit-space segments across the x axis (inverting a
// glyph so its point faces down — the Luciferian orientation).
func flipY(segs [][2][3]float64) [][2][3]float64 {
	out := make([][2][3]float64, len(segs))
	for i, s := range segs {
		out[i] = [2][3]float64{{s[0][0], -s[0][1], s[0][2]}, {s[1][0], -s[1][1], s[1][2]}}
	}
	return out
}

// arcSegments approximates a circular arc with straight segments.
func arcSegments(cx, cy, r, a0, a1 float64, n int) [][2][3]float64 {
	segs := make([][2][3]float64, 0, n)
	var prev [2]float64
	for i := 0; i <= n; i++ {
		a := a0 + (a1-a0)*float64(i)/float64(n)
		p := [2]float64{cx + r*math.Cos(a), cy + r*math.Sin(a)}
		if i > 0 {
			segs = append(segs, [2][3]float64{{prev[0], prev[1], 0}, {p[0], p[1], 0}})
		}
		prev = p
	}
	return segs
}

// circle is a full unit-space circle inlay.
func circle(cx, cy, r float64) [][2][3]float64 {
	return arcSegments(cx, cy, r, 0, 2*math.Pi, 48)
}

// spiralSegments approximates the serpent coil: a decreasing-radius
// spiral around the origin with the given turns.
func spiralSegments(turns float64, n int) [][2][3]float64 {
	segs := make([][2][3]float64, 0, n)
	var prev [2]float64
	for i := 0; i <= n; i++ {
		t := float64(i) / float64(n)
		a := t * turns * 2 * math.Pi
		r := 0.5 * (1 - 0.72*t)
		p := [2]float64{r * math.Cos(a), r * math.Sin(a)}
		if i > 0 {
			segs = append(segs, [2][3]float64{{prev[0], prev[1], 0}, {p[0], p[1], 0}})
		}
		prev = p
	}
	return segs
}

// treeSkeleton builds a deterministic trunk/branch skeleton: a main
// trunk from the ground to height h, with `levels` branch tiers whose
// branches alternate around the trunk at authored angles. All in unit
// space (y from 0 to 0.5; the exporter scales by the element Scale).
func treeSkeleton(h float64, levels int, lean float64, spreadR float64) [][2][3]float64 {
	segs := make([][2][3]float64, 0, 2+levels*3)
	top := [3]float64{lean * h, h, 0}
	segs = append(segs, [2][3]float64{{0, 0, 0}, top})
	for l := 0; l < levels; l++ {
		f := (float64(l) + 0.62) / float64(levels+1)
		b := [3]float64{top[0] * f, top[1] * f, 0}
		for k := 0; k < 3; k++ {
			a := float64(l)*0.9 + float64(k)*2.0944 + 0.35 // 120-degree spread, offset per tier
			tip := [3]float64{
				b[0] + spreadR*(1-f)*math.Cos(a),
				b[1] + spreadR*0.62*(1-f),
				spreadR * (1 - f) * math.Sin(a) * 0.5,
			}
			segs = append(segs, [2][3]float64{b, tip})
		}
	}
	return segs
}

// squareOutline is the unit-square base quad for floors.
func squareOutline() [][2]float64 {
	return [][2]float64{{-0.5, -0.5}, {0.5, -0.5}, {0.5, 0.5}, {-0.5, 0.5}}
}

// ringSegments returns n concentric circle inlays (ritual floor).
func ringSegments(n int) [][2][3]float64 {
	var segs [][2][3]float64
	for i := 1; i <= n; i++ {
		r := 0.5 * float64(i) / float64(n)
		segs = append(segs, circle(0, 0, r)...)
	}
	return segs
}

// crackSegments returns deterministic crack polylines for the cracked
// earth floor: seeded by fixed constants (no rng; the same asset is
// byte-stable everywhere). Cracks stay inside unit space by
// construction (each fissure is a radial pair from r outward/inward).
func crackSegments(n int) [][2][3]float64 {
	var segs [][2][3]float64
	for i := 0; i < n; i++ {
		// Authored pseudo-random constants (golden-ratio spacing) keep
		// cracks spread without a random source.
		a := float64(i) * 2.399963229728653
		r := 0.18 + 0.24*math.Cos(float64(i)*1.7) // 0.18..0.42
		dir := math.Max((0.5-r)*0.55, 0.02)       // in-bounds fissure half-length
		cx, cy := r*math.Cos(a), r*math.Sin(a)
		dx, dy := dir*math.Cos(a+0.6), dir*math.Sin(a+0.6)
		x0, y0 := cx-dx, cy-dy
		x1, y1 := cx+dx, cy+dy
		// Hard-clip the mirrored branch for exact unit-space safety.
		bx, by := 2*x1-x0, 2*y1-y0
		bx = math.Max(-0.5, math.Min(0.5, bx))
		by = math.Max(-0.5, math.Min(0.5, by))
		segs = append(segs, [2][3]float64{{x0, y0, 0}, {x1, y1, 0}})
		segs = append(segs, [2][3]float64{{x1, y1, 0}, {bx, by, 0}})
	}
	return segs
}
