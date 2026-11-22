package dsp

// harmony_test.go — §6.6/§8.2 property tests: tuning, chroma, key and
// chord honesty (confidence floors, no fabricated labels).

import (
	"math"
	"testing"
)

func TestHarmonyTuning(t *testing.T) {
	a440, _ := CorpusFixture("tone-440", 3, 1)
	fa := newFullAnalyzer()
	fa.Process(a440.Interleaved())
	a := fa.Finish("", "")
	if a.Harmony == nil {
		t.Fatal("no harmony layer")
	}
	if math.Abs(a.Harmony.TuningOffsetCents) > 3 {
		t.Fatalf("440 Hz tuning offset = %.1f cents, want |off| <= 3", a.Harmony.TuningOffsetCents)
	}
	// 445 Hz is +19.6 cents sharp of A440.
	a445 := Tone(445, 3, 0.5)
	fb := newFullAnalyzer()
	fb.Process(a445.Interleaved())
	b := fb.Finish("", "")
	if b.Harmony.TuningOffsetCents < 8 || b.Harmony.TuningOffsetCents > 32 {
		t.Fatalf("445 Hz tuning offset = %.1f cents, want ~+20 (8..32)", b.Harmony.TuningOffsetCents)
	}
}

func TestChromaPureToneIsAPitchClass(t *testing.T) {
	tr, _ := CorpusFixture("tone-440", 2, 1)
	fa := newFullAnalyzer()
	fa.Process(tr.Interleaved())
	a := fa.Finish("", "")
	n := len(a.Harmony.Chroma)
	if n == 0 {
		t.Fatal("no chroma frames")
	}
	var avg [12]float64
	for _, c := range a.Harmony.Chroma {
		for j := 0; j < 12; j++ {
			avg[j] += float64(c.Chroma[j])
		}
	}
	best, bestV := 0, 0.0
	total := 0.0
	for j := 0; j < 12; j++ {
		avg[j] /= float64(n)
		total += avg[j]
		if avg[j] > bestV {
			bestV, best = avg[j], j
		}
	}
	if best != 9 { // A
		t.Fatalf("440 Hz dominant pitch class = %d, want 9 (A)", best)
	}
	if bestV/total < 0.5 {
		t.Fatalf("A share = %.2f, want >= 0.5", bestV/total)
	}
}

func TestHarmonyChordMajorDetected(t *testing.T) {
	tr := newTrack(nSamples(4))
	ChordChange(&tr, 0.5, 3, 0.4, []float64{261.63}, true) // C major triad
	frames, bands, chroma := analyzeFrames(t, tr)
	rhythm := DetectRhythm(frames.Frames, bands, hopSec)
	h := DetectHarmony(frames.Frames, chroma, hopSec)
	found := false
	for _, c := range h.Chords {
		if c.Quality == "maj" && c.Confidence >= 0.55 && c.Root == "C" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no confident C major chord event: %+v", h.Chords)
	}
	if len(h.ChordChanges) == 0 {
		t.Fatal("no chord-change timestamps")
	}
	_ = rhythm
}

func TestHarmonyKeyHonestOnAmbiguous(t *testing.T) {
	// A steady 440 Hz tone carries one pitch class: a key label would
	// be a guess. Either no key clears the floor (Key == "") or the
	// candidates exist with the honest floor visible.
	tr, _ := CorpusFixture("tone-440", 4, 1)
	frames, _, chroma := analyzeFrames(t, tr)
	h := DetectHarmony(frames.Frames, chroma, hopSec)
	if h.Key != "" && h.KeyConfidence < 0.6 {
		t.Fatalf("key %q reported at confidence %.2f below the 0.6 floor", h.Key, h.KeyConfidence)
	}
}

func TestHarmonyNoChordOnNoise(t *testing.T) {
	tr, _ := CorpusFixture("noise-white", 3, 5)
	frames, _, chroma := analyzeFrames(t, tr)
	h := DetectHarmony(frames.Frames, chroma, hopSec)
	for _, c := range h.Chords {
		if c.Root != "N" && c.Confidence > 0.75 {
			t.Fatalf("noise must not produce a confident tonal chord: %+v", c)
		}
	}
}

func TestHarmonyTensionReflectsChange(t *testing.T) {
	tr := newTrack(nSamples(8))
	ChordChange(&tr, 0.5, 3.5, 0.4, []float64{261.63}, true)  // C major
	ChordChange(&tr, 4.5, 3.0, 0.4, []float64{311.13}, false) // D# minor (far away)
	frames, _, chroma := analyzeFrames(t, tr)
	h := DetectHarmony(frames.Frames, chroma, hopSec)
	if len(h.Tension) == 0 {
		t.Fatal("no tension series")
	}
	// Tension around the change must not be systematically lower than
	// the stable first second.
	around := 0.0
	n := 0
	changeIdx := int(4.5 / hopSec)
	for f := changeIdx - 10; f <= changeIdx+10; f++ {
		if f >= 0 && f < len(h.Tension) {
			around += float64(h.Tension[f])
			n++
		}
	}
	around /= float64(n)
	base := 0.0
	m := 0
	for f := 0; f < int(1.0/hopSec) && f < len(h.Tension); f++ {
		base += float64(h.Tension[f])
		m++
	}
	base /= float64(m)
	if around < base {
		t.Fatalf("tension around change (%.3f) lower than stable base (%.3f)", around, base)
	}
}

func TestHarmonyDeterministic(t *testing.T) {
	tr := newTrack(nSamples(6))
	ChordChange(&tr, 0.5, 2.5, 0.4, []float64{220}, false)
	ChordChange(&tr, 3.5, 2.0, 0.4, []float64{261.63}, true)
	frames, _, chroma := analyzeFrames(t, tr)
	a := DetectHarmony(frames.Frames, chroma, hopSec)
	b := DetectHarmony(frames.Frames, chroma, hopSec)
	if len(a.Chords) != len(b.Chords) || a.Key != b.Key ||
		math.Abs(a.TuningOffsetCents-b.TuningOffsetCents) > 0 {
		t.Fatal("harmony not deterministic")
	}
	if len(a.Chords) > 0 && len(a.Chords[0].Quality) == 0 {
		t.Fatal("chord event missing quality")
	}
}
