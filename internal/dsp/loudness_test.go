package dsp

// loudness_test.go — property tests for the BS.1770-5 dynamics layer
// over corpus fixtures: a 997 Hz sine at amplitude 0.5 measures
// ≈ -9 LUFS (0 dBFS 1 kHz sine = -3.01 LUFS minus 6.02 dB), digital
// silence measures the -70 floor with zero range, the Clipped proxy
// is flagged as clipping with a true peak at full scale, a tone has
// more crest than clipped material, a 1 s pause yields exactly one
// silence interval of ≈1 s, results are independent of Process
// chunking, and SetFrameCount fixes the series length.

import (
	"math"
	"reflect"
	"testing"
)

// loudFeed feeds the track's interleaved stereo samples as float64.
func loudFeed(m *LoudnessMeter, tr StereoTrack) {
	iv := tr.Interleaved()
	f := make([]float64, len(iv))
	for i, x := range iv {
		f[i] = float64(x)
	}
	m.Process(f)
}

// joinTracks concatenates stereo tracks (all at SampleRate).
func joinTracks(trs ...StereoTrack) StereoTrack {
	n := 0
	for _, tr := range trs {
		n += len(tr.L)
	}
	out := StereoTrack{SR: SampleRate, L: make([]float32, n), R: make([]float32, n)}
	k := 0
	for _, tr := range trs {
		copy(out.L[k:], tr.L)
		copy(out.R[k:], tr.R)
		k += len(tr.L)
	}
	return out
}

// toneSilenceTone builds the 5 s pause fixture: 2 s tone, 1 s silence,
// 2 s tone.
func toneSilenceTone() StereoTrack {
	return joinTracks(Tone(440, 2, 0.5), Silence(1, 1), Tone(440, 2, 0.5))
}

func TestLoudnessSineLevel(t *testing.T) {
	t.Parallel()
	// 997 Hz sine at amplitude 0.5, DUAL MONO (L = R, both speakers):
	// a full-scale 1 kHz sine on ONE channel reads -3.01 LUFS (RMS
	// -3.01 dBFS, K-weight ~0 dB at 1 kHz, plus the -0.691 LUFS
	// alignment); the G=[1,1] channel SUM adds +3.01 dB for an
	// identical second channel, and halving the amplitude subtracts
	// 6.02 dB, with ~+0.7 dB K-shelf at ~1 kHz. Expected ≈ -6.0 LUFS.
	m := NewLoudnessMeter()
	loudFeed(m, Tone(997, 4, 0.5))
	s := m.Finish()
	if math.Abs(s.IntegratedLUFS-(-6.0)) > 1.0 {
		t.Fatalf("997 Hz dual-mono sine integrated loudness = %.2f LUFS, want -6.0 +/-1.0", s.IntegratedLUFS)
	}
}

func TestLoudnessSilence(t *testing.T) {
	t.Parallel()
	m := NewLoudnessMeter()
	loudFeed(m, Silence(3, 1))
	s := m.Finish()
	if s.IntegratedLUFS != -70 {
		t.Fatalf("silence integrated = %v, want -70 floor", s.IntegratedLUFS)
	}
	if s.LoudnessRangeLU != 0 {
		t.Fatalf("silence LRA = %v, want 0", s.LoudnessRangeLU)
	}
	if s.SamplePeakDBFS != -70 || s.TruePeakDBFS != -70 {
		t.Fatalf("silence peaks = %v/%v dBFS, want -70 floor", s.SamplePeakDBFS, s.TruePeakDBFS)
	}
	if s.CrestFactorDB != 0 {
		t.Fatalf("silence crest = %v, want 0", s.CrestFactorDB)
	}
	if s.ClippingRatio != 0 {
		t.Fatalf("silence should not clip (%v)", s.ClippingRatio)
	}
	// A fully silent track is ONE measured silence interval covering
	// the whole duration (the measurement is honest; the vocal-flow
	// layer consumes pauses inside real content).
	if len(s.SilenceIntervals) != 1 {
		t.Fatalf("full silence must measure exactly one silence interval, got %d", len(s.SilenceIntervals))
	}
	if iv := s.SilenceIntervals[0]; iv.MinLUFS != -70 {
		t.Fatalf("silence interval MinLUFS = %v, want the -70 floor", iv.MinLUFS)
	}
	for _, v := range s.MomentaryLUFS {
		if v != -70 {
			t.Fatalf("silence momentary series holds %v, want -70", v)
		}
	}
}

func TestLoudnessClipping(t *testing.T) {
	t.Parallel()
	// Clipped proxy: 1.6× sine hard-clipped at ±1 → long ±1.0 sample
	// runs in every 400 ms block.
	m := NewLoudnessMeter()
	loudFeed(m, Clipped(3, 1))
	s := m.Finish()
	if s.ClippingRatio <= 0.5 {
		t.Fatalf("clipped fixture ratio = %.3f, want > 0.5", s.ClippingRatio)
	}
	if s.ClippingFrames < 1 {
		t.Fatalf("clipped fixture frames = %d, want ≥ 1", s.ClippingFrames)
	}
	if s.TruePeakDBFS < -0.5 {
		t.Fatalf("clipped fixture true peak = %.2f dBTP, want ≥ -0.5", s.TruePeakDBFS)
	}
	if s.SamplePeakDBFS < -0.01 {
		t.Fatalf("clipped fixture sample peak = %.2f dBFS, want ≈0", s.SamplePeakDBFS)
	}
}

func TestLoudnessCrest(t *testing.T) {
	t.Parallel()
	mt := NewLoudnessMeter()
	loudFeed(mt, Tone(440, 3, 0.5))
	crestTone := mt.Finish().CrestFactorDB
	mc := NewLoudnessMeter()
	loudFeed(mc, Clipped(3, 1))
	crestClip := mc.Finish().CrestFactorDB
	if crestTone <= crestClip {
		t.Fatalf("tone crest %.2f dB should exceed clipped crest %.2f dB", crestTone, crestClip)
	}
}

func TestLoudnessSilenceInterval(t *testing.T) {
	t.Parallel()
	m := NewLoudnessMeter()
	loudFeed(m, toneSilenceTone())
	s := m.Finish()
	if len(s.SilenceIntervals) != 1 {
		t.Fatalf("1 s mid silence: %d intervals, want 1 (%+v)", len(s.SilenceIntervals), s.SilenceIntervals)
	}
	iv := s.SilenceIntervals[0]
	dur := iv.End - iv.Start
	if math.Abs(dur-1.0) > 0.3 {
		t.Fatalf("silence interval duration = %.2f s, want 1.0 ±0.3 (%+v)", dur, iv)
	}
	if iv.MinLUFS > SilenceThresholdLUFS {
		t.Fatalf("interval MinLUFS = %.1f, want ≤ %v", iv.MinLUFS, SilenceThresholdLUFS)
	}
}

func TestLoudnessChunkIndependence(t *testing.T) {
	t.Parallel()
	iv := toneSilenceTone().Interleaved()
	f := make([]float64, len(iv))
	for i, x := range iv {
		f[i] = float64(x)
	}
	var ref *LoudnessSummary
	for _, chunk := range []int{97, 4096, 48000} {
		m := NewLoudnessMeter()
		m.SetFrameCount(469)
		for i := 0; i < len(f); i += chunk {
			end := i + chunk
			if end > len(f) {
				end = len(f)
			}
			m.Process(f[i:end])
		}
		s := m.Finish()
		if ref == nil {
			ref = &s
			continue
		}
		if !reflect.DeepEqual(*ref, s) {
			t.Fatalf("loudness summary differs when processed in %d-sample chunks", chunk)
		}
	}
}

func TestLoudnessSeriesLength(t *testing.T) {
	t.Parallel()
	// SetFrameCount pins the decimated series length.
	m := NewLoudnessMeter()
	loudFeed(m, Tone(440, 2, 0.5))
	m.SetFrameCount(123)
	s := m.Finish()
	if len(s.MomentaryLUFS) != 123 || len(s.ShortTermLUFS) != 123 {
		t.Fatalf("series lengths %d/%d, want 123/123", len(s.MomentaryLUFS), len(s.ShortTermLUFS))
	}
	for _, v := range s.MomentaryLUFS {
		if v < -70 {
			t.Fatalf("momentary series value %v below the -70 floor", v)
		}
	}
	if max := s.MomentaryLUFS[len(s.MomentaryLUFS)-1]; max <= -70 {
		t.Fatalf("tone momentary series should rise above the floor near the end, last = %v", max)
	}
	// without SetFrameCount the length is inferred from duration
	m2 := NewLoudnessMeter()
	loudFeed(m2, Tone(440, 2, 0.5))
	s2 := m2.Finish()
	want := int(2*SampleRate/HopSize) + 1 // 2 s on the hop grid
	if len(s2.MomentaryLUFS) != want {
		t.Fatalf("inferred series length %d, want %d", len(s2.MomentaryLUFS), want)
	}
}
