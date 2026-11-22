package visualizer

// composition_test.go — §7.1/§7.3/§8.3 property tests for the common
// time base: discrete vs continuous handling, measured-only beat phase,
// confidence availability, nil-analysis fallback.

import (
	"math"
	"testing"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/dsp"
)

func buildAnalysis(t *testing.T, tr dsp.StereoTrack) *dsp.Analysis {
	t.Helper()
	// Stream via the exported reader API.
	a, err := dsp.AnalyzeFullReader(testCtx(), &byteReader{data: interleaveF32(tr)}, "fp", "test")
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func interleaveF32(tr dsp.StereoTrack) []byte {
	inter := tr.Interleaved()
	out := make([]byte, 4*len(inter))
	for i, v := range inter {
		bits := math.Float32bits(v)
		out[4*i] = byte(bits)
		out[4*i+1] = byte(bits >> 8)
		out[4*i+2] = byte(bits >> 16)
		out[4*i+3] = byte(bits >> 24)
	}
	return out
}

func TestCompositionNilAnalysisFallback(t *testing.T) {
	c := NewComposition(nil)
	st := c.StateAt(1.0)
	if st.SectionIndex != -1 || st.Confidence != 0 || st.BeatPhase != -1 {
		t.Fatalf("nil analysis state = %+v, want all-unavailable", st)
	}
	var nilComp *Composition
	st = nilComp.StateAt(1.0)
	if st.Confidence != 0 {
		t.Fatal("nil receiver must be safe")
	}
}

func TestCompositionDiscreteTransients(t *testing.T) {
	tr := dsp.DropStructure(3)
	a := buildAnalysis(t, tr)
	c := NewComposition(a)
	if a.Rhythm == nil || len(a.Rhythm.Transients) == 0 {
		t.Fatal("no transients measured")
	}
	// Exactly AT a transient: strength delivered.
	tr0 := a.Rhythm.Transients[0]
	at := c.StateAt(tr0.Time)
	if at.Transient <= 0 {
		t.Fatalf("transient strength missing at event time: %+v", at)
	}
	// FAR from any transient: zero (discrete events are not smeared).
	far := tr0.Time + 2.0
	st := c.StateAt(far)
	if st.Transient != 0 {
		t.Fatalf("discrete transient leaked far from the event: %.3f", st.Transient)
	}
}

func TestCompositionChordAndSectionLookup(t *testing.T) {
	tr, err := dsp.CorpusFixture("drop", 0, 42)
	if err != nil {
		t.Fatal(err)
	}
	a := buildAnalysis(t, tr)
	c := NewComposition(a)
	st := c.StateAt(2.0)
	// Section lookup must resolve a section covering t (index >= 0 or
	// honestly unknown when no change points exist before t).
	if st.SectionIndex < 0 && len(a.Structure) > 0 {
		first := a.Structure[0]
		if 2.0 >= first.Start && 2.0 < first.End {
			t.Fatalf("section covering t not resolved: %+v", st)
		}
	}
	// Chord lookup: when chords were measured, the covering segment wins.
	if len(a.Harmony.Chords) > 0 {
		ch := a.Harmony.Chords[0]
		mid := ch.Start + (ch.End-ch.Start)/2
		got := c.StateAt(mid)
		if got.ChordRoot != ch.Root {
			t.Fatalf("chord lookup = %q, want %q", got.ChordRoot, ch.Root)
		}
	}
}

func TestCompositionBeatPhaseMeasuredOnly(t *testing.T) {
	// A steady tone: no reliable grid → phase stays -1 (never
	// fabricated from the coarse tempo estimate).
	tr, _ := dsp.CorpusFixture("tone-440", 3, 1)
	a := buildAnalysis(t, tr)
	if a.Rhythm.TempoReliable {
		t.Skip("steady tone unexpectedly reliable")
	}
	c := NewComposition(a)
	st := c.StateAt(1.5)
	if st.BeatPhase != -1 {
		t.Fatalf("unmeasured beat phase fabricated: %v", st.BeatPhase)
	}
	// A click train: measured grid → phase in 0..1.
	tr2 := dsp.Silence(4, 1)
	dsp.ClickTrain(&tr2, 0, 4, 120, 0.6, 1.5, 4)
	a2 := buildAnalysis(t, tr2)
	if !a2.Rhythm.TempoReliable {
		t.Skip("click train not reliable in this build")
	}
	c2 := NewComposition(a2)
	st2 := c2.StateAt(1.5)
	if st2.BeatPhase < 0 || st2.BeatPhase > 1 {
		t.Fatalf("measured beat phase out of range: %v", st2.BeatPhase)
	}
}

func TestCompositionContinuousInterpolation(t *testing.T) {
	tr, _ := dsp.CorpusFixture("crescendo", 4, 1)
	a := buildAnalysis(t, tr)
	c := NewComposition(a)
	// Dynamics envelope must be continuous: values at two close times
	// differ by less than the full-scale range.
	s1 := c.StateAt(1.0)
	s2 := c.StateAt(1.5)
	if math.Abs(s1.MomentaryLUFS-s2.MomentaryLUFS) > 40 {
		t.Fatalf("continuous series jumped discontinuously: %.1f vs %.1f", s1.MomentaryLUFS, s2.MomentaryLUFS)
	}
}
