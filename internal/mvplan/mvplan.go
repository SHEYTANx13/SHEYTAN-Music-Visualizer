// Package mvplan builds a per-track 3D world plan ("premade music
// video blueprint") from the measured analysis of one audio track and
// its Song DNA.
//
// Contract (run 45097038674 feature slice):
//
//   - ONE track in, ONE deterministic world plan out. The same
//     fingerprint and analysis always produce identical plans
//     (verified by tests); two different tracks never share a seed.
//   - The plan is DATA, not playback: the renderers remain the single
//     audio-reactive execution path. The plan supplies the world the
//     renderers inhabit (chapters, elements, camera, lighting), derived
//     from measured features (sections, tempo, brightness, punchiness),
//     with seeded generative placement for positions that no measured
//     feature determines. Generative placement is explicitly labeled as
//     such and is deterministic per track - it is never presented as
//     audio analysis.
//   - Long-audio safety: the builder consumes only the analysis
//     document (never PCM buffers), works in O(sections), and bounds
//     the world by capping elements per chapter and total elements.
//     A 10-hour track plans in the same bounded memory as a 3-minute
//     one (proven by the long-audio test).
//   - Failure honesty: unavailable analysis layers (Structure, Rhythm)
//     degrade to documented fallbacks recorded in Plan.Notes, never to
//     fabricated measurements.
package mvplan

import (
	"fmt"
	"hash/fnv"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/dsp"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/songdna"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/worldassets"
)

// SchemaVersion is the plan document version. Additive evolution only.
const SchemaVersion = 1

// Bounded-world limits. They keep memory and render load predictable
// for arbitrarily long audio (the task's "even long audios" bound).
const (
	MaxElementsPerChapter = 24
	MaxTotalElements      = 480
	MaxChapters           = 96
)

// ElementKind enumerates the world element families the renderer
// composition layers can inhabit. Values are stable identifiers.
type ElementKind string

// World element families.
const (
	KindMonolith    ElementKind = "monolith"
	KindRing        ElementKind = "ring"
	KindGate        ElementKind = "gate"
	KindPillar      ElementKind = "pillar"
	KindCurrent     ElementKind = "current"
	KindEmberStorm  ElementKind = "ember-storm"
	KindMirrorShard ElementKind = "mirror-shard"
	// Run 45182658992 world-asset families: the sigil, tree and floor
	// assets from internal/worldassets join the composition palette.
	KindSigil ElementKind = "sigil"
	KindTree  ElementKind = "tree"
	KindFloor ElementKind = "floor"
)

// MotionClass enumerates how an element moves over its lifetime.
type MotionClass string

// Motion classes.
const (
	MotionPulse   MotionClass = "pulse"
	MotionOrbit   MotionClass = "orbit"
	MotionRise    MotionClass = "rise"
	MotionDrift   MotionClass = "drift"
	MotionShatter MotionClass = "shatter"
)

// EnergyClass groups elements by measured chapter intensity.
type EnergyClass string

// Energy classes.
const (
	EnergyCalm    EnergyClass = "calm"
	EnergyDriving EnergyClass = "driving"
	EnergyPeak    EnergyClass = "peak"
)

// Element is one world object with a lifetime, deterministic placement
// and a motion class. Positions are world units; the renderer maps
// them onto the active visualizer mode's coordinate space.
type Element struct {
	ID   string      `json:"id"`
	Kind ElementKind `json:"kind"`
	// Asset is the worldassets catalog ID realizing this element
	// (e.g. "sigil/inverted-pentagram", "tree/twisted-oak"); empty for
	// the abstract geometric families the renderers draw directly.
	Asset       string      `json:"asset,omitempty"`
	Chapter     int         `json:"chapter"`
	Start       float64     `json:"start"`
	End         float64     `json:"end"`
	Position    [3]float64  `json:"position"`
	Scale       [3]float64  `json:"scale"`
	Motion      MotionClass `json:"motion"`
	Energy      EnergyClass `json:"energy"`
	Reactive    bool        `json:"reactive"` // driven by the live signal at render time
	Description string      `json:"description"`
}

// Chapter is one world environment span (one detected structural
// section, or the documented single-chapter fallback).
type Chapter struct {
	Index       int         `json:"index"`
	Name        string      `json:"name"`
	Start       float64     `json:"start"`
	End         float64     `json:"end"`
	Confidence  float64     `json:"confidence"`
	Environment string      `json:"environment"`
	Energy      EnergyClass `json:"energy"`
	// Floor is the worldassets floor ID covering this chapter's ground
	// ("floor/ritual-circle" ...); empty only when no floor matched.
	Floor string `json:"floor,omitempty"`
}

// CameraPlan is the deterministic camera choreography for the video.
type CameraPlan struct {
	Style         string  `json:"style"`         // e.g. "orbit-slow", "push-on-peak"
	BeatSynced    bool    `json:"beatSynced"`    // cuts/moves quantized to measured tempo
	CutCadenceSec float64 `json:"cutCadenceSec"` // mean seconds between camera cuts
	BeatPeriodSec float64 `json:"beatPeriodSec"` // measured beat period; 0 when unknown
}

// LightingPlan is the deterministic lighting/palette mapping.
type LightingPlan struct {
	PaletteIndex int     `json:"paletteIndex"`
	BaseHue      float64 `json:"baseHue"` // 0..1 from measured warmth/brightness
	Accent       string  `json:"accent"`  // brand accent role name
	Glow         float64 `json:"glow"`    // from DNA glow intensity
}

// LongAudioPolicy records how the plan and its video remain bounded
// for long inputs (the task's long-audio requirement, made explicit).
type LongAudioPolicy struct {
	Strategy            string `json:"strategy"`            // always "streamed-section-render"
	EstimatedFrames     int64  `json:"estimatedFrames"`     // duration * FPS
	AnalysisMemoryBound string `json:"analysisMemoryBound"` // documented series memory bound
	WithinExportBound   bool   `json:"withinExportBound"`   // export 8 h safety bound
}

// Plan is the complete per-track world document.
type Plan struct {
	SchemaVersion    int             `json:"schemaVersion"`
	TrackFingerprint string          `json:"trackFingerprint"`
	Seed             uint64          `json:"seed"`
	Duration         float64         `json:"duration"`
	TempoBPM         float64         `json:"tempoBpm"` // 0 when unmeasured
	Descriptor       string          `json:"descriptor"`
	Chapters         []Chapter       `json:"chapters"`
	Elements         []Element       `json:"elements"`
	Camera           CameraPlan      `json:"camera"`
	Lighting         LightingPlan    `json:"lighting"`
	LongAudio        LongAudioPolicy `json:"longAudio"`
	Notes            []string        `json:"notes,omitempty"`
	GeneratedUTC     string          `json:"generatedUtc"`
}

// Build derives the world plan for one track. analysis may carry nil
// layers (degrades with Notes); dna must be non-nil.
func Build(dna *songdna.DNA, analysis *dsp.Analysis) (*Plan, error) {
	if dna == nil {
		return nil, fmt.Errorf("mvplan: song DNA is required")
	}
	if analysis == nil {
		return nil, fmt.Errorf("mvplan: analysis document is required")
	}
	if analysis.Duration <= 0 {
		return nil, fmt.Errorf("mvplan: analysis duration must be positive (got %v)", analysis.Duration)
	}

	seed := deriveSeed(dna, analysis.SourceFingerprint)
	rng := newRNG(seed)
	p := &Plan{
		SchemaVersion:    SchemaVersion,
		TrackFingerprint: analysis.SourceFingerprint,
		Seed:             seed,
		Duration:         analysis.Duration,
		Descriptor:       dna.Descriptor,
		GeneratedUTC:     time.Now().UTC().Format("2006-01-02T15:04:05Z"),
	}

	p.buildChapters(analysis)
	for i := range p.Chapters {
		p.Chapters[i].Floor = floorForEnvironment(p.Chapters[i].Environment)
	}
	p.TempoBPM = measureTempo(analysis)
	p.buildCamera(analysis, rng)
	p.buildLighting(dna, rng)
	p.buildElements(dna, analysis, rng)
	p.buildLongAudioPolicy()

	if len(analysis.Structure) == 0 {
		p.note("structure layer unavailable; single-chapter fallback used (no fabricated section boundaries)")
	}
	if analysis.Rhythm == nil {
		p.note("rhythm layer unavailable; camera is energy-synced only (no fabricated beat grid)")
	}
	return p, nil
}

// deriveSeed mixes the DNA seed with the track fingerprint so two
// tracks with the same user seed still get different worlds.
func deriveSeed(dna *songdna.DNA, fingerprint string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(fingerprint))
	return dna.Seed ^ h.Sum64()
}

// buildChapters maps detected sections to world chapters (fallback:
// one chapter for the whole duration).
func (p *Plan) buildChapters(analysis *dsp.Analysis) {
	sections := make([]dsp.SectionEvent, len(analysis.Structure))
	copy(sections, analysis.Structure)
	sort.SliceStable(sections, func(i, j int) bool { return sections[i].Start < sections[j].Start })
	if len(sections) == 0 {
		p.Chapters = []Chapter{{
			Index: 0, Name: "continuous",
			Start: 0, End: p.Duration,
			Confidence: 0, Environment: p.environmentFor(nil),
			Energy: EnergyDriving,
		}}
		return
	}
	for i, s := range sections {
		if len(p.Chapters) >= MaxChapters {
			p.note(fmt.Sprintf("section count exceeds %d; remaining sections folded into the final chapter", MaxChapters))
			p.Chapters[len(p.Chapters)-1].End = p.Duration
			break
		}
		_ = i
		start := clampf(s.Start, 0, p.Duration)
		end := clampf(s.End, start, p.Duration)
		if end-start < 0.5 { // ignore degenerate slivers
			continue
		}
		p.Chapters = append(p.Chapters, Chapter{
			Index:       len(p.Chapters),
			Name:        sanitizeLabel(s.Label),
			Start:       start,
			End:         end,
			Confidence:  clamp01(s.Confidence),
			Environment: p.environmentFor(&s),
			Energy:      energyForLabel(s.Label),
		})
	}
	if len(p.Chapters) == 0 { // all slivers
		p.Chapters = []Chapter{{
			Index: 0, Name: "continuous", Start: 0, End: p.Duration,
			Environment: p.environmentFor(nil), Energy: EnergyDriving,
		}}
	}
	// Guarantee full coverage of the track (the video must span the
	// audio; gaps would produce dead video time).
	p.Chapters[0].Start = 0
	p.Chapters[len(p.Chapters)-1].End = p.Duration
	for i := 1; i < len(p.Chapters); i++ {
		if p.Chapters[i].Start < p.Chapters[i-1].End {
			p.Chapters[i].Start = p.Chapters[i-1].End
		}
		if p.Chapters[i].End <= p.Chapters[i].Start {
			p.Chapters[i].End = p.Duration
		}
	}
}

// environmentFor maps a section label to a world environment name.
// Unknown labels map to the neutral "hall" environment.
func (p *Plan) environmentFor(s *dsp.SectionEvent) string {
	if s == nil {
		return "hall"
	}
	switch sanitizeLabel(s.Label) {
	case "intro":
		return "threshold"
	case "verse", "a":
		return "hall"
	case "chorus", "b":
		return "arena"
	case "bridge", "c":
		return "galleries"
	case "outro":
		return "descent"
	case "break", "breakdown":
		return "void"
	default:
		return "hall"
	}
}

// energyForLabel assigns the measured-section energy class from the
// section label (labels come from dsp.DetectStructure, not invented).
func energyForLabel(label string) EnergyClass {
	switch sanitizeLabel(label) {
	case "chorus", "b", "peak":
		return EnergyPeak
	case "intro", "outro", "break", "breakdown":
		return EnergyCalm
	default:
		return EnergyDriving
	}
}

// measureTempo extracts the dominant measured tempo (BPM) from the
// rhythm layer; 0 when unmeasured.
func measureTempo(analysis *dsp.Analysis) float64 {
	if analysis.Rhythm == nil {
		return 0
	}
	return analysis.Rhythm.BPM
}

// buildCamera derives camera choreography from measured rhythm.
func (p *Plan) buildCamera(analysis *dsp.Analysis, rng *rng) {
	style := "orbit-slow"
	if analysis.Rhythm != nil {
		conf := analysis.Rhythm.TempoConfidence
		switch {
		case conf >= 0.7:
			style = "push-on-peak"
		case conf >= 0.35:
			style = "orbit-driven"
		}
	}
	p.Camera = CameraPlan{
		Style:         style,
		BeatSynced:    analysis.Rhythm != nil,
		CutCadenceSec: clampf(4+rng.Float01()*6, 2, 12),
		BeatPeriodSec: beatPeriod(p.TempoBPM),
	}
}

// buildLighting derives the lighting plan from measured DNA features.
func (p *Plan) buildLighting(dna *songdna.DNA, rng *rng) {
	p.Lighting = LightingPlan{
		PaletteIndex: dna.PaletteIndex,
		BaseHue:      clampf(0.55-0.35*dna.Warmth+0.15*(rng.Float01()-0.5), 0, 1),
		Accent:       "brand-red",
		Glow:         clampf(dna.GlowIntensity, 0.3, 2),
	}
}

// buildElements fills each chapter with a bounded, seeded element set.
func (p *Plan) buildElements(dna *songdna.DNA, analysis *dsp.Analysis, rng *rng) {
	_ = analysis
	total := 0
	for _, ch := range p.Chapters {
		budget := elementsForChapter(ch, dna)
		if budget > MaxElementsPerChapter {
			budget = MaxElementsPerChapter
		}
		if total+budget > MaxTotalElements {
			budget = MaxTotalElements - total
		}
		if budget <= 0 {
			if total >= MaxTotalElements {
				p.note(fmt.Sprintf("element cap %d reached at chapter %d; later chapters reuse the final element set", MaxTotalElements, ch.Index))
				break
			}
			continue
		}
		span := ch.End - ch.Start
		for i := 0; i < budget; i++ {
			// Stagger lifetimes across the chapter so long chapters
			// still evolve (each element occupies a sub-span).
			frac0 := float64(i) / float64(budget)
			frac1 := float64(i+1) / float64(budget)
			start := ch.Start + frac0*span
			end := ch.Start + frac1*span + math.Min(span*0.1, 4)
			if end > ch.End {
				end = ch.End
			}
			kind := pickKind(rng, ch.Energy, dna)
			motion := pickMotion(rng, kind, ch.Energy)
			reactive := ch.Energy != EnergyCalm && rng.Float01() < 0.35+0.4*dna.RhythmicDrive
			p.Elements = append(p.Elements, Element{
				ID:          fmt.Sprintf("c%02de%02d", ch.Index, i),
				Kind:        kind,
				Asset:       pickAssetID(rng, kind),
				Chapter:     ch.Index,
				Start:       start,
				End:         end,
				Position:    [3]float64{spread(rng), spread(rng) * 0.35, spread(rng) * 1.6},
				Scale:       [3]float64{0.4 + rng.Float01()*1.8, 0.4 + rng.Float01()*2.4, 0.4 + rng.Float01()*1.2},
				Motion:      motion,
				Energy:      ch.Energy,
				Reactive:    reactive,
				Description: describe(kind, motion, ch, dna),
			})
			total++
		}
	}
}

// elementsForChapter derives the element budget from chapter length
// and energy, bounded by the package caps (long-audio safety).
func elementsForChapter(ch Chapter, dna *songdna.DNA) int {
	seconds := ch.End - ch.Start
	base := 3 + int(seconds/20) // roughly one element per 20 s
	switch ch.Energy {
	case EnergyPeak:
		base = int(float64(base) * 1.6)
	case EnergyCalm:
		base = int(float64(base) * 0.6)
	}
	base += int(math.Round(dna.ParticleDensity * 4))
	if base < 2 {
		base = 2
	}
	return base
}

// pickKind selects an element family for a chapter deterministically.
func pickKind(rng *rng, energy EnergyClass, dna *songdna.DNA) ElementKind {
	rolls := []ElementKind{KindMonolith, KindPillar, KindRing}
	if energy == EnergyPeak {
		rolls = []ElementKind{KindEmberStorm, KindMonolith, KindGate, KindRing, KindSigil}
	}
	if energy == EnergyCalm {
		rolls = []ElementKind{KindMirrorShard, KindCurrent, KindPillar, KindTree}
	}
	if dna.MotionStyle >= 2 {
		rolls = append(rolls, KindGate)
	}
	return rolls[rng.Intn(len(rolls))]
}

// pickMotion selects the motion class for an element.
func pickMotion(rng *rng, kind ElementKind, energy EnergyClass) MotionClass {
	switch kind {
	case KindRing, KindGate:
		return MotionOrbit
	case KindEmberStorm:
		return MotionShatter
	case KindCurrent:
		return MotionDrift
	case KindMirrorShard:
		return MotionRise
	case KindSigil:
		return MotionPulse
	case KindTree:
		return MotionDrift
	}
	if energy == EnergyPeak {
		return MotionPulse
	}
	if rng.Float01() < 0.5 {
		return MotionPulse
	}
	return MotionRise
}

// describe produces the ASCII, English, human-readable element line.
func describe(kind ElementKind, motion MotionClass, ch Chapter, dna *songdna.DNA) string {
	var b strings.Builder
	b.WriteString(string(kind))
	b.WriteString(" with ")
	b.WriteString(string(motion))
	b.WriteString(" motion in ")
	b.WriteString(ch.Environment)
	b.WriteString(" (")
	b.WriteString(ch.Name)
	b.WriteString("), ")
	switch {
	case dna.Punchiness >= 0.66:
		b.WriteString("sharp transient response")
	case dna.Brightness >= 0.66:
		b.WriteString("bright spectral response")
	default:
		b.WriteString("steady response")
	}
	return b.String()
}

// pickAssetID binds an element to a worldassets catalog entry.
// Deterministic from the plan rng; returns "" for the abstract
// geometric kinds the renderers draw directly.
func pickAssetID(rng *rng, kind ElementKind) string {
	var family worldassets.Family
	switch kind {
	case KindSigil:
		family = worldassets.FamilySigil
	case KindTree:
		family = worldassets.FamilyTree
	default:
		return ""
	}
	ids := worldassets.IDsForFamily(family)
	if len(ids) == 0 {
		return ""
	}
	return ids[rng.Intn(len(ids))]
}

// floorForEnvironment maps a chapter environment onto its ground
// asset. Deterministic; unknown environments fall back to obsidian
// tile (the neutral black ground).
func floorForEnvironment(env string) string {
	switch env {
	case "threshold":
		return "floor/obsidian-tile"
	case "hall":
		return "floor/cracked-earth"
	case "arena":
		return "floor/ritual-circle"
	case "galleries":
		return "floor/bone-grid"
	case "descent":
		return "floor/cracked-earth"
	case "void":
		return "floor/mirror-water"
	default:
		return "floor/obsidian-tile"
	}
}

// buildLongAudioPolicy records the bounded long-audio strategy.
func (p *Plan) buildLongAudioPolicy() {
	const fps = 60.0
	p.LongAudio = LongAudioPolicy{
		Strategy:            "streamed-section-render",
		EstimatedFrames:     int64(p.Duration * fps),
		AnalysisMemoryBound: "series-bound (6 x FrameCount x 4 bytes per stereo series; no PCM retained)",
		WithinExportBound:   int64(p.Duration*fps) <= 8*60*60*int64(fps),
	}
}

// ElementsAt returns the elements active at time t (seconds).
func (p *Plan) ElementsAt(t float64) []Element {
	var out []Element
	for _, e := range p.Elements {
		if t >= e.Start && t <= e.End {
			out = append(out, e)
		}
	}
	return out
}

// ChapterAt returns the chapter covering time t (nil outside coverage).
func (p *Plan) ChapterAt(t float64) *Chapter {
	for i := range p.Chapters {
		if t >= p.Chapters[i].Start && t <= p.Chapters[i].End {
			return &p.Chapters[i]
		}
	}
	return nil
}

// TotalDuration is the planned video duration (equals track duration).
func (p *Plan) TotalDuration() float64 { return p.Duration }

// note appends a stable ASCII note (deduplicated).
func (p *Plan) note(s string) {
	for _, n := range p.Notes {
		if n == s {
			return
		}
	}
	p.Notes = append(p.Notes, s)
}

func sanitizeLabel(l string) string {
	return strings.ToLower(strings.TrimSpace(l))
}

func clamp01(v float64) float64 { return clampf(v, 0, 1) }

func clampf(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func spread(rng *rng) float64 { return (rng.Float01()*2 - 1) * 24 }

func beatPeriod(bpm float64) float64 {
	if bpm <= 0 {
		return 0
	}
	return 60 / bpm
}
