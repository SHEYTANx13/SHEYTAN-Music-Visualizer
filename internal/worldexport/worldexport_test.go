package worldexport

import (
	"bytes"
	"strings"
	"testing"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/dsp"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/mvplan"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/songdna"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/worldassets"
)

func testDNA(seed uint64) *songdna.DNA {
	return &songdna.DNA{
		Version:         2,
		Fingerprint:     "fp-base",
		Seed:            seed,
		Brightness:      0.42,
		Punchiness:      0.71,
		Warmth:          0.55,
		RhythmicDrive:   0.63,
		Loudness:        0.5,
		DefaultMode:     "spectrum-ritual",
		PaletteIndex:    1,
		MotionStyle:     2,
		PulseResponse:   1.1,
		Smoothing:       0.3,
		ParticleDensity: 0.5,
		GlowIntensity:   1.0,
		SpectrumTilt:    0,
		Descriptor:      "driving mid-bright groove",
	}
}

func testAnalysis(fp string, duration float64) *dsp.Analysis {
	return &dsp.Analysis{
		SchemaVersion:     3,
		SourceFingerprint: fp,
		Duration:          duration,
		SampleRate:        48000,
		Channels:          2,
		AnalyzerID:        dsp.AnalyzerIDV3,
		HopSeconds:        0.01,
		Structure: []dsp.SectionEvent{
			{Start: 0, End: 18, Label: "intro", Confidence: 0.7},
			{Start: 18, End: 58, Label: "verse", Confidence: 0.8},
			{Start: 58, End: 96, Label: "chorus", Confidence: 0.9},
			{Start: 96, End: 152, Label: "outro", Confidence: 0.5},
		},
		Rhythm: &dsp.Rhythm{BPM: 128, TempoConfidence: 0.82, TempoReliable: true},
	}
}

// TestPlanBindingsRealizeCatalog proves the plan's asset bindings
// always reference real catalog entries of the matching family, and
// every chapter carries a floor asset.
func TestPlanBindingsRealizeCatalog(t *testing.T) {
	plan, err := mvplan.Build(testDNA(77), testAnalysis("track-one", 152))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(plan.Elements) == 0 {
		t.Fatalf("plan has no elements")
	}
	bound := 0
	for _, el := range plan.Elements {
		if el.Asset == "" {
			continue
		}
		bound++
		a, ok := worldassets.Get(el.Asset)
		if !ok {
			t.Fatalf("element %s binds unknown asset %q", el.ID, el.Asset)
		}
		if el.Kind != mvplan.KindSigil && el.Kind != mvplan.KindTree {
			t.Fatalf("element %s (kind %s) must not bind an asset", el.ID, el.Kind)
		}
		want := worldassets.FamilySigil
		if el.Kind == mvplan.KindTree {
			want = worldassets.FamilyTree
		}
		if a.Family != want {
			t.Fatalf("element %s kind %s binds asset of family %s", el.ID, el.Kind, a.Family)
		}
	}
	if bound == 0 {
		t.Fatalf("no element ever binds an asset (selection broken)")
	}
	for _, ch := range plan.Chapters {
		if ch.Floor == "" {
			t.Fatalf("chapter %d has no floor asset", ch.Index)
		}
		if a, ok := worldassets.Get(ch.Floor); !ok || a.Family != worldassets.FamilyFloor {
			t.Fatalf("chapter %d floor %q is not a floor asset", ch.Index, ch.Floor)
		}
	}
}

// TestExportProducesValidOBJ: the OBJ document is structurally valid
// (indices in range, one mtllib, usemtl only for declared materials)
// and deterministic across runs.
func TestExportProducesValidOBJ(t *testing.T) {
	plan, err := mvplan.Build(testDNA(7), testAnalysis("track-two", 152))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var obj1, mtl1, obj2, mtl2 bytes.Buffer
	r1, err := Export(plan, &obj1, &mtl1, Options{})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if _, err := Export(plan, &obj2, &mtl2, Options{}); err != nil {
		t.Fatalf("Export (2): %v", err)
	}
	if obj1.String() != obj2.String() || mtl1.String() != mtl2.String() {
		t.Fatalf("export is not deterministic")
	}
	if r1.Objects == 0 || r1.Vertices == 0 {
		t.Fatalf("empty export: %+v", r1)
	}

	// Structural validation.
	mtl := mtl1.String()
	obj := obj1.String()
	if !strings.HasPrefix(obj, "# SHEYTAN Music Visualizer world plan export") {
		t.Fatalf("missing export header")
	}
	if !strings.Contains(obj, "mtllib sheytan_world.mtl") {
		t.Fatalf("missing mtllib reference")
	}
	declared := map[string]bool{}
	for _, line := range strings.Split(mtl, "\n") {
		if strings.HasPrefix(line, "newmtl ") {
			declared[strings.TrimSpace(line[7:])] = true
		}
	}
	if len(declared) == 0 {
		t.Fatalf("no materials declared")
	}
	maxVerts := 0
	usesNonDeclared := false
	for _, line := range strings.Split(obj, "\n") {
		switch {
		case strings.HasPrefix(line, "v "):
			maxVerts++
		case strings.HasPrefix(line, "f "), strings.HasPrefix(line, "l "):
			for _, tok := range strings.Fields(line)[1:] {
				n := 0
				for _, c := range tok {
					if c < '0' || c > '9' {
						t.Fatalf("non-numeric index token %q", tok)
					}
					n = n*10 + int(c-'0')
				}
				if n < 1 || n > maxVerts {
					t.Fatalf("index %d out of range (verts so far %d) in %q", n, maxVerts, line)
				}
			}
		case strings.HasPrefix(line, "usemtl "):
			if !declared[strings.TrimSpace(line[7:])] {
				usesNonDeclared = true
			}
		}
	}
	if usesNonDeclared {
		t.Fatalf("usemtl references an undeclared material")
	}
	// Blender-friendliness: no spaces in object names.
	for _, line := range strings.Split(obj, "\n") {
		if strings.HasPrefix(line, "o ") && strings.Contains(strings.TrimSpace(line[2:]), " ") {
			t.Fatalf("object name contains a space: %q", line)
		}
	}
}

// TestExportNilPlan fails closed.
func TestExportNilPlan(t *testing.T) {
	var obj, mtl bytes.Buffer
	if _, err := Export(nil, &obj, &mtl, Options{}); err == nil {
		t.Fatalf("nil plan must fail")
	}
}

// TestSanitizeName keeps OBJ names Blender-safe.
func TestSanitizeName(t *testing.T) {
	for in, want := range map[string]string{
		"sigil/inverted-pentagram": "sigil_inverted_pentagram",
		"tree/dead-conifer":        "tree_dead_conifer",
		"":                         "object",
	} {
		if got := sanitizeName(in); got != want {
			t.Fatalf("sanitizeName(%q) = %q, want %q", in, got, want)
		}
	}
}
