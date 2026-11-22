package dsp

// rhythm_test.go — §6.5/§8.2 property tests: tempo, beats, downbeats,
// half/double ambiguity, syncopation, honest uncertainty.

import (
	"math"
	"testing"
)

func TestRhythmClick120(t *testing.T) {
	tr := newTrack(nSamples(6))
	ClickTrain(&tr, 0, 6, 120, 0.6, 1.6, 4)
	frames, bands, _ := analyzeFrames(t, tr)
	r := DetectRhythm(frames.Frames, bands, hopSec)
	if r == nil {
		t.Fatal("no rhythm result")
	}
	if r.TempoConfidence < 0.5 {
		t.Fatalf("tempo confidence = %.2f, want >= 0.5 for a clean click train", r.TempoConfidence)
	}
	if !r.TempoReliable {
		t.Fatal("clean click train must be tempo-reliable")
	}
	if math.Abs(r.BPM-120) > 3 {
		t.Fatalf("BPM = %.1f, want 120 +/-3", r.BPM)
	}
	if len(r.Beats) == 0 {
		t.Fatal("no beats on a reliable click train")
	}
	if len(r.Beats) < 2 {
		t.Fatalf("too few beats: %d", len(r.Beats))
	}
	// Beat spacing regularity.
	spacing := make([]float64, 0, len(r.Beats)-1)
	for i := 1; i < len(r.Beats); i++ {
		spacing = append(spacing, r.Beats[i].Time-r.Beats[i-1].Time)
	}
	var sum, sumSq float64
	for _, s := range spacing {
		sum += s
		sumSq += s * s
	}
	mean := sum / float64(len(spacing))
	variance := sumSq/float64(len(spacing)) - mean*mean
	if math.Sqrt(variance) > 0.015 {
		t.Fatalf("beat spacing std = %.1f ms, want <= 15 ms", math.Sqrt(variance)*1000)
	}
	if math.Abs(mean-0.5) > 0.03 {
		t.Fatalf("mean beat spacing = %.3f s, want ~0.5 s", mean)
	}
	// Meter: 4/4 expected among the candidates with a downbeat grid.
	if len(r.Meters) == 0 {
		t.Fatal("no meter candidates")
	}
	if len(r.Downbeats) == 0 {
		t.Fatal("no downbeats for an accented 4/4 click train")
	}
}

func TestRhythmHalfDoubleAmbiguity(t *testing.T) {
	tr, _ := CorpusFixture("half-double", 8, 1)
	frames, bands, _ := analyzeFrames(t, tr)
	r := DetectRhythm(frames.Frames, bands, hopSec)
	if !r.HalfDoubleAmbiguous {
		// The fixture accents every 2nd beat at 120 BPM, which is
		// deliberately also a clean 60 BPM pattern; the candidate set
		// must expose the ambiguity even when the primary read wins.
		found := false
		for _, c := range r.Candidates {
			if c.Relation == "half" || c.Relation == "double" {
				found = true
			}
		}
		if !found {
			t.Fatalf("half/double ambiguity not represented: bpm=%.1f candidates=%+v", r.BPM, r.Candidates)
		}
	}
}

func TestRhythmSyncopation(t *testing.T) {
	syncTr := newTrack(nSamples(6))
	Syncopated(&syncTr, 0.1, 6, 120, 0.55)
	straightTr := newTrack(nSamples(6))
	ClickTrain(&straightTr, 0, 6, 120, 0.55, 1.2, 4)
	fs, bs, _ := analyzeFrames(t, syncTr)
	rs := DetectRhythm(fs.Frames, bs, hopSec)
	ff, bf, _ := analyzeFrames(t, straightTr)
	rst := DetectRhythm(ff.Frames, bf, hopSec)
	if rs.Syncopation <= rst.Syncopation {
		t.Fatalf("syncopated fixture (%.2f) must exceed straight grid (%.2f)", rs.Syncopation, rst.Syncopation)
	}
	if rs.Syncopation < 0.2 {
		t.Fatalf("syncopation = %.2f, want >= 0.2 for an off-beat-emphasized grid", rs.Syncopation)
	}
}

func TestRhythmSteadyToneUncertain(t *testing.T) {
	tr, _ := CorpusFixture("tone-440", 5, 1)
	frames, bands, _ := analyzeFrames(t, tr)
	r := DetectRhythm(frames.Frames, bands, hopSec)
	if r.TempoReliable {
		t.Fatalf("steady tone must not claim reliable tempo (conf=%.2f)", r.TempoConfidence)
	}
	if r.TempoConfidence >= 0.35 {
		t.Fatalf("steady tone confidence = %.2f, want < 0.35", r.TempoConfidence)
	}
	if len(r.Beats) != 0 {
		t.Fatalf("no beat grid may be fabricated for a steady tone, got %d beats", len(r.Beats))
	}
}

func TestRhythmSilenceNoTransients(t *testing.T) {
	tr, _ := CorpusFixture("silence", 3, 1)
	frames, bands, _ := analyzeFrames(t, tr)
	r := DetectRhythm(frames.Frames, bands, hopSec)
	if len(r.Transients) != 0 {
		t.Fatalf("silence must produce no transients, got %d", len(r.Transients))
	}
	if r.Density != 0 {
		t.Fatalf("silence density = %v", r.Density)
	}
}

func TestRhythmTempoRampSegments(t *testing.T) {
	tr, _ := CorpusFixture("tempo-ramp", 12, 1)
	frames, bands, _ := analyzeFrames(t, tr)
	r := DetectRhythm(frames.Frames, bands, hopSec)
	// A ramp is honest evidence of local tempo change; the segment list
	// should be non-empty OR the global estimate should sit between the
	// extremes with reduced confidence (never a fabricated constant).
	if len(r.TempoSegments) == 0 {
		if r.BPM < 100 || r.BPM > 140 {
			t.Fatalf("ramp BPM = %.1f, want within 100..140 or explicit segments", r.BPM)
		}
	}
}

func TestRhythmDeterministic(t *testing.T) {
	tr := DropStructure(11)
	frames, bands, _ := analyzeFrames(t, tr)
	a := DetectRhythm(frames.Frames, bands, hopSec)
	b := DetectRhythm(frames.Frames, bands, hopSec)
	if len(a.Transients) != len(b.Transients) || len(a.Beats) != len(b.Beats) {
		t.Fatal("rhythm detection not deterministic")
	}
	for i := range a.Transients {
		if a.Transients[i] != b.Transients[i] {
			t.Fatal("transients differ between runs")
		}
	}
}
