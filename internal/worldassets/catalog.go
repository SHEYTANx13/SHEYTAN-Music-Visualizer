// catalog.go — the authored asset catalog. Every entry is
// deterministic (pure functions of constants); the Luciferian sigil
// family is drawn as inverted geometric glyph line-work (point-down
// orientation), the tree family as ritual trunk/branch skeletons, the
// floor family as bounded pattern descriptors.
package worldassets

import "math"

// catalog is the single source of truth. Order is presentation order;
// Catalog() sorts by ID for stable programmatic access.
var catalog = []Asset{
	// --- Luciferian sigils (line-work glyphs, extruded on export) ---

	{
		ID:          "sigil/inverted-pentagram",
		Family:      FamilySigil,
		Name:        "Inverted Pentagram",
		Description: "The point-down five-point star inside two binding circles: the opening sigil of the ritual space.",
		Material:    "infernalRed",
		Segments:    joinSegs(flipY(starLines(math.Pi/2)), circle(0, 0, 0.5), circle(0, 0, 0.44)),
		Height:      0.06,
	},
	{
		ID:          "sigil/horned-mark",
		Family:      FamilySigil,
		Name:        "Horned Mark",
		Description: "Twin horn arcs over a foundation circle: the mark placed at chapter gates and arena thresholds.",
		Material:    "antiqueGold",
		Segments: joinSegs(
			arcSegments(-0.22, 0.18, 0.2, 0.6, 2.6, 16),
			arcSegments(0.22, 0.18, 0.2, 0.54, 2.54, 16),
			circle(0, -0.12, 0.26),
		),
		Height: 0.06,
	},
	{
		ID:          "sigil/serpent-coil",
		Family:      FamilySigil,
		Name:        "Serpent Coil",
		Description: "The coil of the old serpent: a three-turn descent from rim to core, ringed at the rim.",
		Material:    "hotEmber",
		Segments:    joinSegs(spiralSegments(3, 96), circle(0, 0, 0.5)),
		Height:      0.04,
	},
	{
		ID:          "sigil/inverted-cross",
		Family:      FamilySigil,
		Name:        "Inverted Cross",
		Description: "The crossed bars hung low on a standing shaft: the sigil of the descent chapters.",
		Material:    "darkBlood",
		Segments: [][2][3]float64{
			{{0, 0.5, 0}, {0, -0.5, 0}},
			{{-0.3, -0.18, 0}, {0.3, -0.18, 0}},
			{{-0.22, -0.38, 0}, {0.22, -0.38, 0}},
		},
		Height: 0.06,
	},
	{
		ID:          "sigil/eye-of-the-abyss",
		Family:      FamilySigil,
		Name:        "Eye of the Abyss",
		Description: "The open eye with a ringed pupil: the watcher placed over void and mirror-water chapters.",
		Material:    "emberHighlight",
		Segments: joinSegs(
			arcSegments(0, 0, 0.45, 0.35, math.Pi-0.35, 20),
			arcSegments(0, 0, 0.45, math.Pi+0.35, 2*math.Pi-0.35, 20),
			circle(0, 0, 0.16),
			circle(0, 0, 0.09),
		),
		Height: 0.04,
	},

	// --- Ritual trees (3D skeletons) ---

	{
		ID:          "tree/dead-conifer",
		Family:      FamilyTree,
		Name:        "Dead Conifer",
		Description: "A tall stripped conifer skeleton: four tiers of climbing branches, straight and barren.",
		Material:    "obsidian",
		Segments:    treeSkeleton(0.5, 4, 0.02, 0.22),
		Height:      1.0,
	},
	{
		ID:          "tree/twisted-oak",
		Family:      FamilyTree,
		Name:        "Twisted Oak",
		Description: "A leaning ritual oak: three heavy tiers on a bowed trunk, the Galleries shade.",
		Material:    "volcanic",
		Segments:    treeSkeleton(0.42, 3, 0.14, 0.3),
		Height:      0.85,
	},
	{
		ID:          "tree/cypress-column",
		Family:      FamilyTree,
		Name:        "Cypress Column",
		Description: "A narrow funeral column: short close tiers, planted along processional paths.",
		Material:    "deepCharcoal",
		Segments:    treeSkeleton(0.5, 5, 0.0, 0.13),
		Height:      1.0,
	},

	// --- Floors (bounded pattern descriptors) ---

	{
		ID:          "floor/obsidian-tile",
		Family:      FamilyFloor,
		Name:        "Obsidian Tile",
		Description: "Black glass tile, 12 by 12: the threshold and hall ground.",
		Material:    "obsidian",
		Outline:     squareOutline(),
		Pattern:     PatternTiles,
		PatternN:    12,
	},
	{
		ID:          "floor/cracked-earth",
		Family:      FamilyFloor,
		Name:        "Cracked Earth",
		Description: "Parched ground with deterministic fissures: the hall and descent ground.",
		Material:    "volcanic",
		Outline:     squareOutline(),
		Segments:    crackSegments(9),
		Pattern:     PatternCracks,
	},
	{
		ID:          "floor/ritual-circle",
		Family:      FamilyFloor,
		Name:        "Ritual Circle",
		Description: "Five concentric binding rings on a stone base: the arena center.",
		Material:    "infernalRed",
		Outline:     squareOutline(),
		Segments:    ringSegments(5),
		Pattern:     PatternRings,
	},
	{
		ID:          "floor/bone-grid",
		Family:      FamilyFloor,
		Name:        "Bone Grid",
		Description: "A pale 16 by 16 ossuary grid: the galleries ground.",
		Material:    "antiqueGold",
		Outline:     squareOutline(),
		Pattern:     PatternTiles,
		PatternN:    16,
	},
	{
		ID:          "floor/mirror-water",
		Family:      FamilyFloor,
		Name:        "Mirror Water",
		Description: "A still reflective plane with a single rim ring: the void ground.",
		Material:    "deepCharcoal",
		Outline:     squareOutline(),
		Segments:    circle(0, 0, 0.46),
		Pattern:     PatternRings,
		PatternN:    1,
	},
}

// joinSegs concatenates segment lists (helper keeping catalog entries
// readable).
func joinSegs(lists ...[][2][3]float64) [][2][3]float64 {
	total := 0
	for _, l := range lists {
		total += len(l)
	}
	out := make([][2][3]float64, 0, total)
	for _, l := range lists {
		out = append(out, l...)
	}
	return out
}
