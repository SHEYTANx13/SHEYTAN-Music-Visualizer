package mvplan

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/dsp"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/songdna"
)

// testDNA returns a DNA with deterministic, in-range artistic fields.
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

// testAnalysis returns a v3-style analysis with measured layers.
func testAnalysis(fingerprint string, duration float64, sections []dsp.SectionEvent, withRhythm bool) *dsp.Analysis {
	a := &dsp.Analysis{
		SchemaVersion:     3,
		SourceFingerprint: fingerprint,
		Duration:          duration,
		SampleRate:        48000,
		Channels:          2,
		AnalyzerID:        dsp.AnalyzerIDV3,
		HopSeconds:        0.01,
		Structure:         sections,
	}
	if withRhythm {
		a.Rhythm = &dsp.Rhythm{BPM: 128, TempoConfidence: 0.82, TempoReliable: true}
	}
	return a
}

func sampleSections() []dsp.SectionEvent {
	return []dsp.SectionEvent{
		{Start: 0, End: 18, Label: "intro", Confidence: 0.7},
		{Start: 18, End: 58, Label: "verse", Confidence: 0.8},
		{Start: 58, End: 96, Label: "chorus", Confidence: 0.9},
		{Start: 96, End: 118, Label: "bridge", Confidence: 0.6},
		{Start: 118, End: 152, Label: "chorus", Confidence: 0.85},
		{Start: 152, End: 171, Label: "outro", Confidence: 0.5},
	}
}

func TestBuildDeterministicPerTrack(t *testing.T) {
	a := testAnalysis("track-one", 171, sampleSections(), true)
	p1, err := Build(testDNA(42), a)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	p2, err := Build(testDNA(42), testAnalysis("track-one", 171, sampleSections(), true))
	if err != nil {
		t.Fatalf("rebuild: %v", err)
	}
	b1, _ := json.Marshal(p1)
	b2, _ := json.Marshal(p2)
	if string(b1) != string(b2) {
		t.Fatalf("plan is not deterministic for identical inputs")
	}
	// A different track (different fingerprint) must never share the
	// seed, even with the same DNA seed.
	p3, err := Build(testDNA(42), testAnalysis("track-two", 171, sampleSections(), true))
	if err != nil {
		t.Fatalf("other track: %v", err)
	}
	if p3.Seed == p1.Seed {
		t.Fatalf("two different tracks share seed %d", p1.Seed)
	}
	if p3.TrackFingerprint != "track-two" {
		t.Fatalf("fingerprint not carried into the plan: %q", p3.TrackFingerprint)
	}
}

func TestMeasuredFeaturesDriveTheWorld(t *testing.T) {
	a := testAnalysis("track-one", 171, sampleSections(), true)
	p, err := Build(testDNA(42), a)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if p.TempoBPM != 128 {
		t.Fatalf("measured tempo not carried: got %v want 128", p.TempoBPM)
	}
	if !p.Camera.BeatSynced {
		t.Fatalf("camera not beat-synced although the rhythm layer exists")
	}
	if p.Camera.BeatPeriodSec <= 0 {
		t.Fatalf("beat period missing for measured tempo")
	}
	// Chapters come from the MEASURED structure, not invention.
	if len(p.Chapters) != 6 {
		t.Fatalf("chapter count = %d, want 6 (one per measured section)", len(p.Chapters))
	}
	if p.Chapters[0].Name != "intro" || p.Chapters[2].Name != "chorus" {
		t.Fatalf("chapter names not derived from measured sections: %v", p.Chapters)
	}
	if p.Chapters[0].Environment != "threshold" || p.Chapters[2].Environment != "arena" {
		t.Fatalf("environment mapping wrong: %q / %q", p.Chapters[0].Environment, p.Chapters[2].Environment)
	}
	// Full coverage: the video spans the whole audio.
	if p.Chapters[0].Start != 0 || math.Abs(p.Chapters[len(p.Chapters)-1].End-171) > 1e-9 {
		t.Fatalf("chapters do not cover the full duration")
	}
}

func TestUnavailableLayersDegradeWithNotes(t *testing.T) {
	// No structure, no rhythm: the plan must still be valid and must
	// SAY what was unavailable instead of fabricating boundaries.
	p, err := Build(testDNA(7), testAnalysis("track-three", 90, nil, false))
	if err != nil {
		t.Fatalf("build with nil layers: %v", err)
	}
	if len(p.Chapters) != 1 || p.Chapters[0].Name != "continuous" {
		t.Fatalf("fallback chapter wrong: %+v", p.Chapters)
	}
	if p.TempoBPM != 0 || p.Camera.BeatSynced {
		t.Fatalf("fabricated tempo/beat data present without the rhythm layer")
	}
	joined := strings.Join(p.Notes, "\n")
	if !strings.Contains(joined, "structure layer unavailable") ||
		!strings.Contains(joined, "rhythm layer unavailable") {
		t.Fatalf("degradation notes missing: %v", p.Notes)
	}
}

func TestNilInputsRejected(t *testing.T) {
	if _, err := Build(nil, testAnalysis("x", 10, nil, false)); err == nil {
		t.Fatalf("nil DNA accepted")
	}
	if _, err := Build(testDNA(1), nil); err == nil {
		t.Fatalf("nil analysis accepted")
	}
	bad := testAnalysis("x", 0, nil, false)
	if _, err := Build(testDNA(1), bad); err == nil {
		t.Fatalf("zero-duration analysis accepted")
	}
}

func TestLongAudioBoundedWorld(t *testing.T) {
	// A 6-hour track (inside the export 8 h bound) with many sections
	// must plan in bounded memory with a bounded world: no per-section
	// element explosion.
	const hours6 = 6 * 60 * 60
	secs := make([]dsp.SectionEvent, 0, 1200)
	for s := 0.0; s < hours6; s += 30 {
		secs = append(secs, dsp.SectionEvent{Start: s, End: s + 30, Label: "verse", Confidence: 0.6})
	}
	a := testAnalysis("very-long-track", hours6, secs, true)
	p, err := Build(testDNA(99), a)
	if err != nil {
		t.Fatalf("long-audio build: %v", err)
	}
	if len(p.Elements) > MaxTotalElements {
		t.Fatalf("element count %d exceeds the bounded-world cap %d", len(p.Elements), MaxTotalElements)
	}
	if len(p.Chapters) > MaxChapters {
		t.Fatalf("chapter count %d exceeds cap %d", len(p.Chapters), MaxChapters)
	}
	if !p.LongAudio.WithinExportBound {
		t.Fatalf("6-hour plan flagged outside the export bound; policy math wrong")
	}
	if p.LongAudio.EstimatedFrames != int64(hours6*60) {
		t.Fatalf("estimated frames wrong: %d", p.LongAudio.EstimatedFrames)
	}
	if p.LongAudio.Strategy != "streamed-section-render" {
		t.Fatalf("long-audio strategy not recorded: %q", p.LongAudio.Strategy)
	}
	// Coverage: first chapter starts at 0, last ends at the duration.
	if p.Chapters[0].Start != 0 || math.Abs(p.Chapters[len(p.Chapters)-1].End-hours6) > 1e-9 {
		t.Fatalf("long track chapters do not cover the duration")
	}
	// A track beyond the export bound must be HONESTLY flagged there
	// (never silently planned as exportable).
	beyond, err := Build(testDNA(99), testAnalysis("beyond", 9*60*60, nil, false))
	if err != nil {
		t.Fatalf("9h build: %v", err)
	}
	if beyond.LongAudio.WithinExportBound {
		t.Fatalf("9-hour plan must be flagged outside the 8 h export bound")
	}
}

func TestElementsAtAndChapterAt(t *testing.T) {
	p, err := Build(testDNA(42), testAnalysis("track-one", 171, sampleSections(), true))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	// Inside the first chapter (intro => calm energy).
	ch := p.ChapterAt(9)
	if ch == nil || ch.Name != "intro" {
		t.Fatalf("ChapterAt(9) = %+v", ch)
	}
	if els := p.ElementsAt(9); len(els) == 0 {
		t.Fatalf("no elements active at t=9")
	}
	// Outside the plan: no chapter, no panic.
	if p.ChapterAt(-1) != nil || p.ChapterAt(p.Duration+10) != nil {
		t.Fatalf("ChapterAt outside coverage must be nil")
	}
	// Every element belongs to a valid chapter and carries a description.
	for _, e := range p.Elements {
		if e.Chapter < 0 || e.Chapter >= len(p.Chapters) {
			t.Fatalf("element %s references invalid chapter %d", e.ID, e.Chapter)
		}
		if e.End <= e.Start {
			t.Fatalf("element %s has empty lifetime", e.ID)
		}
		if e.Description == "" {
			t.Fatalf("element %s description empty", e.ID)
		}
	}
}

func TestPlanJSONRoundTrip(t *testing.T) {
	p, err := Build(testDNA(42), testAnalysis("track-one", 171, sampleSections(), true))
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back Plan
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.SchemaVersion != p.SchemaVersion || back.Seed != p.Seed ||
		len(back.Elements) != len(p.Elements) || len(back.Chapters) != len(p.Chapters) {
		t.Fatalf("round trip lost data")
	}
	if len(b) < 1000 {
		t.Fatalf("plan implausibly small for a 6-chapter world (%d bytes)", len(b))
	}
}
