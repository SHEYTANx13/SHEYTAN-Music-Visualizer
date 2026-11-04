package dsp

import (
	"math"
	"testing"
)

// feedSine feeds n seconds of a sine at freq/rate.
func feedSine(a *Analyzer, seconds int, freq float64) {
	n := SampleRate * seconds
	samples := make([]float32, n*2)
	for i := 0; i < n; i++ {
		v := float32(0.6 * math.Sin(2*math.Pi*freq*float64(i)/SampleRate))
		samples[2*i], samples[2*i+1] = v, v
	}
	a.Process(samples)
}

func TestBandEnergyPureTone(t *testing.T) {
	cases := []struct {
		freq  float64
		band  string
		check func(*Result) float64
	}{
		{80, "bass", func(r *Result) float64 { return r.BassEnergy }},
		{1000, "mid", func(r *Result) float64 { return r.MidEnergy }},
		{12000, "treble", func(r *Result) float64 { return r.TrebleEnergy }},
	}
	for _, c := range cases {
		a := NewAnalyzer()
		feedSine(a, 1, c.freq)
		res := a.Finish()
		got := c.check(res)
		if got < 0.6 {
			t.Fatalf("%d Hz tone: %s energy %.2f, want >= 0.6", int(c.freq), c.band, got)
		}
	}
}

func TestSpectralCentroid(t *testing.T) {
	a := NewAnalyzer()
	feedSine(a, 1, 2000)
	res := a.Finish()
	if res.MeanCentroid < 1800 || res.MeanCentroid > 2200 {
		t.Fatalf("centroid %.0f Hz, want ~2000", res.MeanCentroid)
	}
}

func TestAnalysisDeterministic(t *testing.T) {
	run := func() *Result {
		a := NewAnalyzer()
		feedSine(a, 1, 220)
		return a.Finish()
	}
	r1, r2 := run(), run()
	if len(r1.Frames) != len(r2.Frames) {
		t.Fatalf("frame count differs: %d vs %d", len(r1.Frames), len(r2.Frames))
	}
	for i := range r1.Frames {
		if r1.Frames[i].RMS != r2.Frames[i].RMS ||
			r1.Frames[i].Bass != r2.Frames[i].Bass ||
			r1.Frames[i].Centroid != r2.Frames[i].Centroid {
			t.Fatalf("frame %d differs between runs (non-deterministic analysis)", i)
		}
		for b := range r1.Frames[i].Bins {
			if r1.Frames[i].Bins[b] != r2.Frames[i].Bins[b] {
				t.Fatalf("frame %d bin %d differs", i, b)
			}
		}
	}
	if r1.BPM != r2.BPM || r1.TempoConfidence != r2.TempoConfidence {
		t.Fatal("tempo differs between runs")
	}
}

func TestTempoClick120BPM(t *testing.T) {
	// Build a 120 BPM click track: bass thump every 0.5 s.
	a := NewAnalyzer()
	totalSec := 8
	n := SampleRate * totalSec
	samples := make([]float32, n*2)
	clickLen := int(0.05 * SampleRate)
	for beat := 0; beat*SampleRate/2+clickLen < n; beat++ {
		start := beat * SampleRate / 2
		for i := 0; i < clickLen; i++ {
			env := 1.0 - float64(i)/float64(clickLen)
			v := float32(0.9 * env * math.Sin(2*math.Pi*100*float64(i)/SampleRate))
			samples[2*(start+i)] = v
			samples[2*(start+i)+1] = v
		}
	}
	a.Process(samples)
	res := a.Finish()
	if !res.TempoReliable {
		t.Fatalf("120 BPM click not detected as reliable: bpm=%.1f conf=%.2f (%s)",
			res.BPM, res.TempoConfidence, res.TempoNote)
	}
	if math.Abs(res.BPM-120) > 6 {
		t.Fatalf("BPM %.1f, want ~120 ±6 (conf %.2f)", res.BPM, res.TempoConfidence)
	}
}

func TestTempoShortTrackUnreliable(t *testing.T) {
	a := NewAnalyzer()
	feedSine(a, 1, 440) // 1 s steady tone: no rhythm
	res := a.Finish()
	if res.TempoReliable {
		t.Fatalf("steady tone reported reliable tempo %.1f", res.BPM)
	}
}

func TestWaveformEnvelope(t *testing.T) {
	a := NewAnalyzer()
	feedSine(a, 2, 220)
	res := a.Finish()
	// 2 s at 2 points/s ≈ 4 points (allow off-by-one from buffering)
	if len(res.Waveform) < 3 || len(res.Waveform) > 6 {
		t.Fatalf("waveform points %d, want ~4", len(res.Waveform))
	}
	if res.WaveformMax <= 0 || res.WaveformMax > 1 {
		t.Fatalf("waveform max %.2f out of range", res.WaveformMax)
	}
}

func TestOnsetDetectionClicks(t *testing.T) {
	// sharp clicks every 0.25 s must yield onsets
	a := NewAnalyzer()
	totalSec := 4
	n := SampleRate * totalSec
	samples := make([]float32, n*2)
	clickLen := int(0.02 * SampleRate)
	for beat := 0; beat*SampleRate/4+clickLen < n; beat++ {
		start := beat * SampleRate / 4
		for i := 0; i < clickLen; i++ {
			v := float32(0.9 * math.Sin(2*math.Pi*500*float64(i)/SampleRate))
			samples[2*(start+i)] = v
			samples[2*(start+i)+1] = v
		}
	}
	a.Process(samples)
	res := a.Finish()
	want := totalSec * 4 // 16 clicks
	if res.OnsetCount < want/2 || res.OnsetCount > want*2 {
		t.Fatalf("onsets %d, want in [%d,%d]", res.OnsetCount, want/2, want*2)
	}
}

func TestEmptyAnalyzer(t *testing.T) {
	a := NewAnalyzer()
	res := a.Finish()
	if res == nil || len(res.Frames) != 0 {
		t.Fatal("empty analysis must produce empty result without panic")
	}
	if res.TempoReliable {
		t.Fatal("empty analysis must not claim reliable tempo")
	}
}
