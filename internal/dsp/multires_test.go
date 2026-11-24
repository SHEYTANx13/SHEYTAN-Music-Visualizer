package dsp

// multires_test.go — property tests for the multi-resolution layer:
// hat transient produces a sharpness spike on the short grid within
// ±3 frames of the onset, sub-bass vs bass-body placement at the long
// window's 2.93 Hz resolution, correct HopSeconds, and byte-identical
// series independent of Feed chunking (97 / 4096 / 48000 samples).

import (
	"reflect"
	"testing"
)

// feedMono feeds the mono mix in chunks of the given size.
func feedMono(m *MultiRes, mono []float64, chunk int) {
	for i := 0; i < len(mono); i += chunk {
		end := i + chunk
		if end > len(mono) {
			end = len(mono)
		}
		for _, x := range mono[i:end] {
			m.Feed(x)
		}
	}
}

func TestMultiResHatSharpness(t *testing.T) {
	t.Parallel()
	// HatLike proxy: very short high-frequency transient at 1.0 s.
	tr := Silence(2, 1)
	HatLike(&tr, 1.0, 0.5)
	m := NewMultiRes()
	feedMono(m, monoF64(tr), 48000)
	s := m.Series().Short

	onset := 1.0 * SampleRate / ShortHopSize // 375 short frames per second grid
	lo, hi := int(onset)-3, int(onset)+3
	if hi >= len(s.Sharpness) || lo < 0 {
		t.Fatalf("not enough short frames (%d) for onset window [%d,%d]", len(s.Sharpness), lo, hi)
	}

	// the global sharpness maximum must land within ±3 frames of onset
	best, bestIdx := -1.0, -1
	for i, v := range s.Sharpness {
		if float64(v) > best {
			best, bestIdx = float64(v), i
		}
	}
	if bestIdx < lo || bestIdx > hi {
		t.Fatalf("sharpness peak at frame %d, want within [%d,%d] (hat onset)", bestIdx, lo, hi)
	}
	if best < 0.5 {
		t.Fatalf("hat sharpness spike = %.3f, want ≥ 0.5", best)
	}
	// background before the hat stays quiet
	for i := 0; i < lo-5 && i < len(s.Sharpness); i++ {
		if float64(s.Sharpness[i]) > 0.2 {
			t.Fatalf("background sharpness at frame %d = %.3f, want < 0.2", i, float64(s.Sharpness[i]))
		}
	}
}

func TestMultiResSubBassPlacement(t *testing.T) {
	t.Parallel()
	// 42 Hz tone proxy → SubEnergy (20..60 Hz) must dominate BassBody
	// (60..250 Hz); 120 Hz → the reverse.
	for _, tc := range []struct {
		freq    float64
		subWins bool
	}{
		{42, true},
		{120, false},
	} {
		m := NewMultiRes()
		feedMono(m, monoF64(Tone(tc.freq, 2, 0.6)), 4096)
		l := m.Series().Long
		if len(l.SubEnergy) < 10 {
			t.Fatalf("%v Hz: only %d long frames", tc.freq, len(l.SubEnergy))
		}
		var sub, bass float64
		for i := range l.SubEnergy {
			sub += float64(l.SubEnergy[i])
			bass += float64(l.BassBody[i])
		}
		sub /= float64(len(l.SubEnergy))
		bass /= float64(len(l.BassBody))
		if tc.subWins && sub <= bass+0.2 {
			t.Fatalf("%v Hz: sub %.3f should beat bass body %.3f by > 0.2", tc.freq, sub, bass)
		}
		if !tc.subWins && bass <= sub+0.2 {
			t.Fatalf("%v Hz: bass body %.3f should beat sub %.3f by > 0.2", tc.freq, bass, sub)
		}
	}
}

func TestMultiResHopSeconds(t *testing.T) {
	t.Parallel()
	m := NewMultiRes()
	feedMono(m, monoF64(Tone(220, 1, 0.5)), 48000)
	d := m.Series()
	if d.Short.HopSeconds != float64(ShortHopSize)/float64(SampleRate) {
		t.Fatalf("short HopSeconds = %v, want %v", d.Short.HopSeconds, float64(ShortHopSize)/float64(SampleRate))
	}
	if d.Long.HopSeconds != float64(LongHopSize)/float64(SampleRate) {
		t.Fatalf("long HopSeconds = %v, want %v", d.Long.HopSeconds, float64(LongHopSize)/float64(SampleRate))
	}
	wantShort := (SampleRate-ShortFFTSize)/ShortHopSize + 1
	if len(d.Short.Energy) != wantShort {
		t.Fatalf("short frames = %d, want %d (one per %d-sample hop after the first full window)", len(d.Short.Energy), wantShort, ShortHopSize)
	}
	wantLong := (SampleRate-LongFFTSize)/LongHopSize + 1
	if len(d.Long.SubEnergy) != wantLong {
		t.Fatalf("long frames = %d, want %d", len(d.Long.SubEnergy), wantLong)
	}
}

func TestMultiResChunkIndependence(t *testing.T) {
	t.Parallel()
	tr := Tone(220, 2, 0.4)
	HatLike(&tr, 0.7, 0.5)
	KickLike(&tr, 1.3, 0.8)
	mono := monoF64(tr)

	var ref *MultiResData
	for _, chunk := range []int{97, 4096, 48000} {
		m := NewMultiRes()
		feedMono(m, mono, chunk)
		d := m.Series()
		if ref == nil {
			ref = d
			continue
		}
		if !reflect.DeepEqual(ref, d) {
			t.Fatalf("multi-res series differ when fed in %d-sample chunks", chunk)
		}
	}
}
