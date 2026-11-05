package dsp

// dsp_rolloff_test.go — spectral rolloff fixtures (roadmap Phase 2
// #4: "Rolloff: NOT DONE"). A pure tone concentrates all energy at
// one frequency, so the 85% rolloff must sit at that frequency; a
// broadband click pushes the rolloff toward the spectrum's top.

import (
	"math"
	"testing"
)

func TestRolloffPureToneIsLow(t *testing.T) {
	a := NewAnalyzer()
	// 2 s of a 220 Hz sine at −6 dBFS (mono, canonical rate).
	const rate = SampleRate
	for i := 0; i < 2*rate; i++ {
		v := float32(0.5 * math.Sin(2*math.Pi*220*float64(i)/rate))
		a.Process([]float32{v, v}) // interleaved stereo
	}
	res := a.Finish()
	if len(res.Frames) == 0 {
		t.Fatal("no frames analyzed")
	}
	// Every frame's rolloff must land at the tone's bin (±1 bin ≈ 23.4 Hz).
	for _, f := range res.Frames {
		if f.RMS <= 0 {
			continue // silent lead-in frames carry no energy
		}
		if f.Rolloff < 200 || f.Rolloff > 300 {
			t.Fatalf("pure 220 Hz tone: rolloff = %.1f Hz, want ≈220 (±1 bin)", f.Rolloff)
		}
	}
	if res.MeanRolloff < 200 || res.MeanRolloff > 300 {
		t.Fatalf("mean rolloff = %.1f Hz, want ≈220", res.MeanRolloff)
	}
}

func TestRolloffBroadbandClickIsHigh(t *testing.T) {
	a := NewAnalyzer()
	// Impulse train: each impulse spreads energy across the whole
	// spectrum, so the 85% accumulation point is far up the spectrum.
	const rate = SampleRate
	for i := 0; i < 2*rate; i++ {
		v := float32(0.0)
		if i%(rate/10) == 0 { // 10 clicks per second
			v = 0.9
		}
		a.Process([]float32{v, v}) // interleaved stereo
	}
	res := a.Finish()
	found := false
	for _, f := range res.Frames {
		if f.Rolloff > 8000 {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("broadband clicks: no frame reached a high rolloff (>8 kHz)")
	}
	if res.MeanRolloff <= 1000 {
		t.Fatalf("mean rolloff = %.1f Hz, want well above the bass region for clicks", res.MeanRolloff)
	}
}

func TestRolloffSilenceIsZero(t *testing.T) {
	a := NewAnalyzer()
	for i := 0; i < SampleRate; i++ {
		a.Process([]float32{0, 0})
	}
	res := a.Finish()
	for _, f := range res.Frames {
		if f.Rolloff != 0 {
			t.Fatalf("silent frame rolloff = %.1f Hz, want 0", f.Rolloff)
		}
	}
}
