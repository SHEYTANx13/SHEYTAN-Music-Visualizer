package dsp

// vocalflow_test.go — §6.5/§8.2 property tests for vocal-like activity
// and phrase/flow estimates. The fixtures are acoustic PROXIES; the
// layer is an estimate, never a transcription.

import (
	"math"
	"testing"
)

func TestVocalPhraseDetected(t *testing.T) {
	tr := newTrack(nSamples(6))
	VocalLike(&tr, 1.0, 3.0, 0.5, 21)
	frames, bands, hp, sp := layerInputs(t, tr)
	v := EstimateVocalFlow(frames.Frames, bands, hp, sp, hopSec)
	if v == nil {
		t.Fatal("no vocal flow result")
	}
	if len(v.Phrases) == 0 {
		t.Fatalf("no phrases on a clear vocal-like proxy (mean likelihood %.3f)", meanVocalLik(v))
	}
	p := v.Phrases[0]
	if p.Start < 0.6 || p.Start > 1.5 {
		t.Fatalf("phrase start = %.2f, want ~1.0 (0.6..1.5)", p.Start)
	}
	if p.End < 3.5 || p.End > 4.4 {
		t.Fatalf("phrase end = %.2f, want ~4.0 (3.5..4.4)", p.End)
	}
	if p.Confidence < 0.4 {
		t.Fatalf("phrase confidence = %.2f, want >= 0.4", p.Confidence)
	}
	if p.SyllableRate < 1.5 || p.SyllableRate > 9 {
		t.Fatalf("syllable rate = %.2f/s, want 1.5..9 (fixture ~4.5)", p.SyllableRate)
	}
	if v.OverallConfidence < 0.3 {
		t.Fatalf("overall confidence = %.2f", v.OverallConfidence)
	}
}

func TestVocalNoPhrasesOnPad(t *testing.T) {
	tr := newTrack(nSamples(6))
	PadLike(&tr, 0.5, 5, 0.4)
	frames, bands, hp, sp := layerInputs(t, tr)
	v := EstimateVocalFlow(frames.Frames, bands, hp, sp, hopSec)
	if len(v.Phrases) != 0 && v.OverallConfidence >= 0.5 {
		t.Fatalf("pad proxy must not produce confident vocal phrases: %d phrases conf %.2f",
			len(v.Phrases), v.OverallConfidence)
	}
}

func TestVocalAntiPhaseNotVocal(t *testing.T) {
	tr, _ := CorpusFixture("antiphase", 3, 1)
	frames, bands, hp, sp := layerInputs(t, tr)
	v := EstimateVocalFlow(frames.Frames, bands, hp, sp, hopSec)
	if len(v.Phrases) != 0 {
		t.Fatalf("anti-phase material must not be vocal evidence, got %d phrases", len(v.Phrases))
	}
	if v.OverallConfidence > 0.4 {
		t.Fatalf("anti-phase overall confidence = %.2f, want <= 0.4", v.OverallConfidence)
	}
}

func TestVocalSeriesAligned(t *testing.T) {
	tr := newTrack(nSamples(4))
	VocalLike(&tr, 0.5, 2.5, 0.5, 3)
	frames, bands, hp, sp := layerInputs(t, tr)
	v := EstimateVocalFlow(frames.Frames, bands, hp, sp, hopSec)
	if len(v.Likelihood) != len(frames.Frames) {
		t.Fatalf("likelihood series length %d != frame count %d", len(v.Likelihood), len(frames.Frames))
	}
	if len(v.SyllableDensity) != len(frames.Frames) {
		t.Fatalf("syllable density length %d != frame count", len(v.SyllableDensity))
	}
	for _, x := range v.Likelihood {
		if x < 0 || x > 1 {
			t.Fatalf("likelihood out of range: %v", x)
		}
	}
}

func TestVocalDeterministic(t *testing.T) {
	tr := newTrack(nSamples(5))
	VocalLike(&tr, 0.5, 3, 0.5, 9)
	frames, bands, hp, sp := layerInputs(t, tr)
	a := EstimateVocalFlow(frames.Frames, bands, hp, sp, hopSec)
	b := EstimateVocalFlow(frames.Frames, bands, hp, sp, hopSec)
	if len(a.Phrases) != len(b.Phrases) {
		t.Fatal("vocal flow not deterministic (phrase count)")
	}
	for i := range a.Phrases {
		if math.Abs(a.Phrases[i].Start-b.Phrases[i].Start) > 0 || a.Phrases[i].Confidence != b.Phrases[i].Confidence {
			t.Fatal("vocal flow not deterministic (phrase values)")
		}
	}
}

func meanVocalLik(v *VocalFlow) float64 {
	if v == nil || len(v.Likelihood) == 0 {
		return 0
	}
	var s float64
	for _, x := range v.Likelihood {
		s += float64(x)
	}
	return s / float64(len(v.Likelihood))
}
