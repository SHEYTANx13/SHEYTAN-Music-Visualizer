package dsp

// corpus.go — deterministic synthetic audio fixtures for analysis tests
// (§8.1 corpus). Everything is generated from fixed parameters and a
// seeded xorshift PRNG; no global randomness, no external files.
//
// These signals are TEST PROXIES: acoustically motivated synthetic
// stand-ins, not recordings of real instruments. Tests must describe
// them as proxies.

import (
	"fmt"
	"math"
	"math/rand"
)

// StereoTrack is a canonical synthetic stereo track (48 kHz, stereo).
type StereoTrack struct {
	SR   int       // sample rate (always SampleRate)
	L, R []float32 // channel samples, equal length
}

// Frames returns the interleaved analyzer input.
func (t StereoTrack) Interleaved() []float32 {
	out := make([]float32, 0, 2*len(t.L))
	for i := range t.L {
		out = append(out, t.L[i], t.R[i])
	}
	return out
}

// Duration returns the track duration in seconds.
func (t StereoTrack) Duration() float64 {
	return float64(len(t.L)) / float64(t.SR)
}

// mono returns the mid signal as float64 for mono-oriented assertions.
func (t StereoTrack) mono(i int) float64 {
	return (float64(t.L[i]) + float64(t.R[i])) * 0.5
}

// ---------------------------------------------------------------------------
// Generator primitives
// ---------------------------------------------------------------------------

// corpusRand is a seeded xorshift source (deterministic per seed).
func corpusRand(seed uint64) *rand.Rand {
	return rand.New(rand.NewSource(int64(seed | 1)))
}

// sigGen produces one sample at absolute sample index i for a voice.
type sigGen func(i int) float64

// renderVoice renders gen for n samples scaled by amp.
func renderVoice(n int, amp float64, gen sigGen) []float32 {
	out := make([]float32, n)
	for i := range out {
		out[i] = float32(amp * gen(i))
	}
	return out
}

// sine returns a sine voice at freq Hz.
func sine(freq float64) sigGen {
	return func(i int) float64 {
		return math.Sin(2 * math.Pi * freq * float64(i) / SampleRate)
	}
}

// saw returns a naive band-limited-ish saw (sum of 8 harmonics).
func saw(freq float64) sigGen {
	return func(i int) float64 {
		var v float64
		t := float64(i) / SampleRate
		for k := 1; k <= 8; k++ {
			v += math.Sin(2*math.Pi*freq*float64(k)*t) / float64(k)
		}
		return v / 2.4
	}
}

// whiteNoise returns a seeded white noise voice.
func whiteNoise(seed uint64) sigGen {
	r := corpusRand(seed)
	return func(i int) float64 { return r.NormFloat64() * 0.25 }
}

// pinkish approximates pink noise by stacking one-pole low-passed
// white stages (deterministic, seeded).
func pinkish(seed uint64) sigGen {
	r := corpusRand(seed)
	b := [6]float64{}
	coefs := [6]float64{0.999, 0.995, 0.98, 0.95, 0.9, 0.8}
	return func(i int) float64 {
		w := r.NormFloat64()
		var v float64
		for k := range coefs {
			b[k] = coefs[k]*b[k] + w*(1-coefs[k])
			v += b[k]
		}
		return v / 6
	}
}

// adsr wraps a voice with a deterministic attack/decay envelope over a
// fixed event duration (used for transient proxies).
func adsr(gen sigGen, attack, decaySec float64, eventStart, eventEnd int) sigGen {
	return func(i int) float64 {
		if i < eventStart || i >= eventEnd {
			return 0
		}
		x := float64(i-eventStart) / SampleRate
		var env float64
		if x < attack {
			env = x / attack
		} else {
			env = math.Exp(-(x - attack) / decaySec)
		}
		return env * gen(i)
	}
}

// ---------------------------------------------------------------------------
// Track builders (all §8.1 fixtures)
// ---------------------------------------------------------------------------

// TrackGen builds a full stereo track.
type TrackGen func(seconds float64, seed uint64) StereoTrack

func newTrack(n int) StereoTrack {
	return StereoTrack{SR: SampleRate, L: make([]float32, n), R: make([]float32, n)}
}

func nSamples(seconds float64) int {
	return int(seconds * SampleRate)
}

// Silence is digital silence.
func Silence(seconds float64, seed uint64) StereoTrack {
	return newTrack(nSamples(seconds))
}

// DCOffset is a constant +0.5 DC on both channels.
func DCOffset(seconds float64, seed uint64) StereoTrack {
	t := newTrack(nSamples(seconds))
	for i := range t.L {
		t.L[i], t.R[i] = 0.5, 0.5
	}
	return t
}

// Clipped is a loud sine driven beyond full scale and hard-clipped.
func Clipped(seconds float64, seed uint64) StereoTrack {
	t := newTrack(nSamples(seconds))
	for i := range t.L {
		v := 1.6 * sine(220)(i)
		if v > 1 {
			v = 1
		} else if v < -1 {
			v = -1
		}
		t.L[i], t.R[i] = float32(v), float32(v)
	}
	return t
}

// Tone is a pure stereo tone at freq (mono-compatible).
func Tone(freq, seconds float64, amp float64) StereoTrack {
	t := newTrack(nSamples(seconds))
	for i := range t.L {
		v := float32(amp * sine(freq)(i))
		t.L[i], t.R[i] = v, v
	}
	return t
}

// Sweep is a logarithmic frequency sweep lo..hi Hz.
func Sweep(lo, hi, seconds float64, amp float64) StereoTrack {
	t := newTrack(nSamples(seconds))
	phase := 0.0
	for i := range t.L {
		frac := float64(i) / float64(len(t.L))
		f := lo * math.Pow(hi/lo, frac)
		phase += 2 * math.Pi * f / SampleRate
		v := float32(amp * math.Sin(phase))
		t.L[i], t.R[i] = v, v
	}
	return t
}

// HarmonicStack is a fundamental with k harmonics at -6 dB/octave-ish
// amplitudes (harmonic proxy).
func HarmonicStack(f0 float64, harmonics int, seconds float64, amp float64) StereoTrack {
	t := newTrack(nSamples(seconds))
	for i := range t.L {
		var v float64
		for k := 1; k <= harmonics; k++ {
			v += math.Sin(2*math.Pi*f0*float64(k)*float64(i)/SampleRate) / float64(k)
		}
		v *= amp / 2.4
		t.L[i], t.R[i] = float32(v), float32(v)
	}
	return t
}

// Inharmonic mixes partials at non-integer ratios (bell-like proxy).
func Inharmonic(seconds float64, amp float64) StereoTrack {
	t := newTrack(nSamples(seconds))
	ratios := []float64{1, 2.76, 5.4, 8.93}
	for i := range t.L {
		var v float64
		for k, r := range ratios {
			v += math.Sin(2*math.Pi*180*r*float64(i)/SampleRate) / float64(k+1)
		}
		v *= amp / 2.2
		t.L[i], t.R[i] = float32(v), float32(v)
	}
	return t
}

// NoiseWhite / NoisePink are stereo noise references.
func NoiseWhite(seconds float64, seed uint64) StereoTrack {
	t := newTrack(nSamples(seconds))
	// The noise voices are created ONCE per channel: a fresh same-seed
	// voice per sample would emit an identical draw for every sample (DC).
	lv, rv := whiteNoise(seed), whiteNoise(seed^0x9E3779B9)
	for i := range t.L {
		t.L[i] = float32(lv(i))
		t.R[i] = float32(rv(i))
	}
	return t
}

func NoisePink(seconds float64, seed uint64) StereoTrack {
	t := newTrack(nSamples(seconds))
	lv, rv := pinkish(seed), pinkish(seed^0x85EBCA6B)
	for i := range t.L {
		t.L[i] = float32(lv(i))
		t.R[i] = float32(rv(i))
	}
	return t
}

// KickLike is a low-frequency transient with a short attack and pitch
// drop (kick proxy).
func KickLike(t *StereoTrack, atSec, amp float64) {
	start := int(atSec * SampleRate)
	dur := int(0.28 * SampleRate)
	gen := adsr(kickVoiceFrom(start), 0.001, 0.09, start, start+dur)
	for i := start; i < start+dur && i < len(t.L); i++ {
		v := float32(amp * gen(i))
		t.L[i] += v
		t.R[i] += v
	}
}

// kickVoiceFrom generates the kick relative to the event start: the
// pitch drop and decay are per-event envelopes (absolute time would
// silence every kick after ~0.5 s).
func kickVoiceFrom(start int) sigGen {
	return func(i int) float64 {
		x := float64(i-start) / SampleRate
		if x < 0 {
			return 0
		}
		f := 105 * math.Exp(-x/0.035)
		if f < 42 {
			f = 42
		}
		ph := 2 * math.Pi * f * x
		return math.Sin(ph) * math.Exp(-x/0.16)
	}
}

// SnareLike is a broadband mid transient (snare proxy).
func SnareLike(t *StereoTrack, atSec, amp float64) {
	start := int(atSec * SampleRate)
	dur := int(0.25 * SampleRate)
	gen := adsr(noiseVoice(0x51ED27), 0.001, 0.06, start, start+dur)
	tone := adsr(sine(185), 0.001, 0.05, start, start+dur)
	for i := start; i < start+dur && i < len(t.L); i++ {
		v := float32(amp * (0.7*gen(i) + 0.5*tone(i)))
		t.L[i] += v
		t.R[i] += v
	}
}

// HatLike is a high-frequency very short transient (hi-hat proxy).
func HatLike(t *StereoTrack, atSec, amp float64) {
	start := int(atSec * SampleRate)
	dur := int(0.08 * SampleRate)
	gen := adsr(hatVoice(0x2C1B3C), 0.0005, 0.02, start, start+dur)
	for i := start; i < start+dur && i < len(t.L); i++ {
		v := float32(amp * gen(i))
		t.L[i] += v
		t.R[i] += v
	}
}

// CymbalLike is a high-frequency sustained decaying crash (cymbal proxy).
func CymbalLike(t *StereoTrack, atSec, amp float64) {
	start := int(atSec * SampleRate)
	dur := int(1.6 * SampleRate)
	gen := adsr(hatVoice(0xD1B54A), 0.003, 0.55, start, start+dur)
	for i := start; i < start+dur && i < len(t.L); i++ {
		v := float32(amp * gen(i))
		t.L[i] += v
		t.R[i] += v
	}
}

func noiseVoice(seed uint64) sigGen { return whiteNoise(seed) }

func hatVoice(seed uint64) sigGen {
	n := whiteNoise(seed)
	return func(i int) float64 {
		// metallic ring: noise + high partials
		return 0.6*n(i) + 0.4*math.Sin(2*math.Pi*7120*float64(i)/SampleRate) + 0.25*math.Sin(2*math.Pi*9800*float64(i)/SampleRate)
	}
}

// SustainedBass adds a sustained bass note (bass proxy).
func SustainedBass(t *StereoTrack, startSec, durSec, amp float64) {
	start := int(startSec * SampleRate)
	end := start + int(durSec*SampleRate)
	gen := sine(55)
	for i := start; i < end && i < len(t.L); i++ {
		env := math.Min(1, float64(i-start)/(0.05*SampleRate))
		v := float32(amp * env * gen(i))
		t.L[i] += v
		t.R[i] += v
	}
}

// PianoLike adds a harmonic attack + decay event (keys proxy).
func PianoLike(t *StereoTrack, atSec, amp float64, f0 float64) {
	start := int(atSec * SampleRate)
	dur := int(1.2 * SampleRate)
	gen := adsr(harmVoice(f0, 6), 0.004, 0.35, start, start+dur)
	for i := start; i < start+dur && i < len(t.L); i++ {
		v := float32(amp * gen(i))
		t.L[i] += v
		t.R[i] += v
	}
}

// StringsLike adds a slow-attack harmonic swell (string proxy).
func StringsLike(t *StereoTrack, startSec, durSec, amp float64) {
	start := int(startSec * SampleRate)
	end := start + int(durSec*SampleRate)
	gen := harmVoice(196, 5)
	for i := start; i < end && i < len(t.L); i++ {
		x := float64(i-start) / SampleRate
		env := math.Min(1, x/0.6) * math.Min(1, math.Max(0.2, (durSec-x)/0.4))
		v := float32(amp * env * gen(i))
		t.L[i] += v
		t.R[i] += v
	}
}

// PadLike adds a sustained broad harmonic bed (pad proxy).
func PadLike(t *StereoTrack, startSec, durSec, amp float64) {
	start := int(startSec * SampleRate)
	end := start + int(durSec*SampleRate)
	gen := harmVoice(130.81, 4)
	gen2 := harmVoice(196, 4)
	for i := start; i < end && i < len(t.L); i++ {
		x := float64(i-start) / SampleRate
		env := math.Min(1, x/1.0) * math.Min(1, math.Max(0.1, (durSec-x)/1.0))
		v := float32(amp * env * (0.6*gen(i) + 0.4*gen2(i)))
		t.L[i] += v
		t.R[i] += v
	}
}

// VocalLike adds a vocal-like proxy: harmonic stack with formant-ish
// emphasis and syllabic AM (3-6 Hz), grouped into phrases.
func VocalLike(t *StereoTrack, startSec, durSec, amp float64, seed uint64) {
	start := int(startSec * SampleRate)
	end := start + int(durSec*SampleRate)
	gen := harmVoice(220, 8)
	r := corpusRand(seed)
	sylPhase := 0.0
	var sylRate float64 = 4.5
	for i := start; i < end && i < len(t.L); i++ {
		x := float64(i-start) / SampleRate
		// syllable AM: rise/fall bursts at ~4.5 events/s with jitter
		sylPhase += sylRate / SampleRate
		am := 0.35 + 0.65*math.Abs(math.Sin(math.Pi*sylPhase))
		if am < 0.35 {
			am = 0.35
		}
		// jitter the syllable rate slightly (deterministic per seed)
		if r.Float64() < 1.0/SampleRate {
			sylRate = 3.5 + 2.5*r.Float64()
		}
		env := math.Min(1, x/0.15) * math.Min(1, math.Max(0.1, (durSec-x)/0.15))
		// formant emphasis: presence-band content (a real voice carries
		// strong 1-4 kHz formant energy)
		v := float32(amp * env * am * gen(i) * 0.9)
		v += float32(amp * env * am * 0.6 * sine(660)(i))
		v += float32(amp * env * am * 0.45 * sine(1100)(i))
		v += float32(amp * env * am * 0.35 * sine(2600)(i))
		t.L[i] += v
		t.R[i] += v
	}
}

func harmVoice(f0 float64, harmonics int) sigGen {
	return func(i int) float64 {
		var v float64
		for k := 1; k <= harmonics; k++ {
			v += math.Sin(2*math.Pi*f0*float64(k)*float64(i)/SampleRate) / float64(k)
		}
		return v / 2.2
	}
}

// ChordChange writes a triad chord into a window (chord-change fixture).
func ChordChange(t *StereoTrack, startSec, durSec, amp float64, roots []float64, major bool) {
	start := int(startSec * SampleRate)
	end := start + int(durSec*SampleRate)
	intervals := []float64{0, 4, 7}
	if !major {
		intervals = []float64{0, 3, 7}
	}
	gens := make([]sigGen, 0, len(roots)*len(intervals))
	for _, root := range roots {
		for _, iv := range intervals {
			gens = append(gens, sine(root*math.Pow(2, iv/12)))
		}
	}
	for i := start; i < end && i < len(t.L); i++ {
		env := math.Min(1, float64(i-start)/(0.08*SampleRate))
		var v float64
		for _, g := range gens {
			v += g(i)
		}
		v = v / float64(len(gens)) * amp * env
		t.L[i] += float32(v)
		t.R[i] += float32(v)
	}
}

// Panned places a tone at a fixed pan position (-1 left .. +1 right).
func Panned(seconds float64, freq, pan, amp float64) StereoTrack {
	t := newTrack(nSamples(seconds))
	gl := amp * math.Min(1, 1-pan)
	gr := amp * math.Min(1, 1+pan)
	for i := range t.L {
		v := sine(freq)(i)
		t.L[i] = float32(gl * v)
		t.R[i] = float32(gr * v)
	}
	return t
}

// AntiPhase is L = -R (complete stereo cancellation when summed).
func AntiPhase(seconds float64, freq, amp float64) StereoTrack {
	t := newTrack(nSamples(seconds))
	for i := range t.L {
		v := float32(amp * sine(freq)(i))
		t.L[i], t.R[i] = v, -v
	}
	return t
}

// WideStereo is decorrelated noise channels (max width).
func WideStereo(seconds float64, seed uint64) StereoTrack {
	t := newTrack(nSamples(seconds))
	lv, rv := whiteNoise(seed), whiteNoise(seed^0x27D4EB2F)
	for i := range t.L {
		t.L[i] = float32(lv(i))
		t.R[i] = float32(rv(i))
	}
	return t
}

// ClickTrain writes percussive clicks at bpm with an accent every
// `meter` beats (downbeat proxy). ampAccent scales the downbeat hits.
func ClickTrain(t *StereoTrack, startSec, endSec, bpm, amp, ampAccent float64, meter int) {
	beat := 60.0 / bpm
	start := int(startSec * SampleRate)
	end := int(endSec * SampleRate)
	for beatIdx := 0; ; beatIdx++ {
		at := startSec + float64(beatIdx)*beat
		if at >= endSec {
			break
		}
		s := start + int(float64(beatIdx)*beat*SampleRate)
		if s >= end {
			break
		}
		a := amp
		if meter > 0 && beatIdx%meter == 0 {
			a = amp * ampAccent
		}
		dur := int(0.05 * SampleRate)
		gen := adsr(sine(1000), 0.0005, 0.012, s, s+dur)
		for i := s; i < s+dur && i < len(t.L); i++ {
			v := float32(a * gen(i))
			t.L[i] += v
			t.R[i] += v
		}
	}
}

// Syncopated writes clicks with off-beat emphasis (syncopation fixture).
func Syncopated(t *StereoTrack, startSec, endSec, bpm, amp float64) {
	beat := 60.0 / bpm
	start := int(startSec * SampleRate)
	end := int(endSec * SampleRate)
	// accents on the "and" (half-beat offsets), weak downbeats
	for k := 0; ; k++ {
		at := startSec + float64(k)*beat*0.5
		if at >= endSec {
			break
		}
		s := start + int(float64(k)*beat*0.5*SampleRate)
		if s >= end {
			break
		}
		a := amp * 0.35
		if k%2 == 1 {
			a = amp // off-beat emphasized
		}
		dur := int(0.05 * SampleRate)
		gen := adsr(sine(1000), 0.0005, 0.012, s, s+dur)
		for i := s; i < s+dur && i < len(t.L); i++ {
			v := float32(a * gen(i))
			t.L[i] += v
			t.R[i] += v
		}
	}
}

// Crescendo wraps a generator voice in a linear rise.
func Crescendo(seconds float64, freq, amp float64) StereoTrack {
	t := newTrack(nSamples(seconds))
	for i := range t.L {
		env := float64(i) / float64(len(t.L))
		v := float32(amp * env * sine(freq)(i))
		t.L[i], t.R[i] = v, v
	}
	return t
}

// FadeInFadeOut wraps a tone with linear fade in/out windows.
func FadeInFadeOut(seconds, freq, amp, fadeSec float64) StereoTrack {
	t := newTrack(nSamples(seconds))
	n := len(t.L)
	fade := int(fadeSec * SampleRate)
	for i := range t.L {
		env := 1.0
		if i < fade {
			env = float64(i) / float64(fade)
		}
		if i > n-fade {
			env = float64(n-i) / float64(fade)
		}
		v := float32(amp * env * sine(freq)(i))
		t.L[i], t.R[i] = v, v
	}
	return t
}

// DropStructure builds an intro (quiet bass) + drop (loud kick/hat
// grid) + break (silence) + return. Deterministic dance proxy.
func DropStructure(seed uint64) StereoTrack {
	t := newTrack(nSamples(16))
	// intro: quiet sustained bass 0-4 s
	SustainedBass(&t, 0, 4, 0.18)
	// build: rising hats 4-6 s
	for k := 0; k < 16; k++ {
		HatLike(&t, 4+float64(k)*0.125, 0.10+0.02*float64(k))
	}
	// drop: sustained bass + kicks + snare + hats 6-10 s (a real drop
	// carries sustained low energy under the transients)
	SustainedBass(&t, 6, 4, 0.5)
	for k := 0; k < 16; k++ {
		KickLike(&t, 6+float64(k)*0.25, 0.9)
		if k%2 == 1 {
			SnareLike(&t, 6+float64(k)*0.25, 0.6)
		}
		HatLike(&t, 6+float64(k)*0.25+0.125, 0.45)
	}
	// break: near-silence 10-12.5 s
	// return: sustained bass + kick grid 12.5-16 s
	SustainedBass(&t, 12.5, 3.5, 0.45)
	for k := 0; k < 14; k++ {
		KickLike(&t, 12.5+float64(k)*0.25, 0.85)
		HatLike(&t, 12.5+float64(k)*0.25+0.125, 0.4)
	}
	_ = seed
	return t
}

// RepeatedSection builds A A B B with distinct timbres (repetition fixture).
func RepeatedSection(seconds float64, seed uint64) StereoTrack {
	t := newTrack(nSamples(seconds))
	half := seconds / 2
	quarter := seconds / 4
	// A: bass + hats
	SustainedBass(&t, 0, quarter, 0.4)
	for k := 0; k < int(quarter/0.25); k++ {
		HatLike(&t, float64(k)*0.25, 0.35)
	}
	SustainedBass(&t, quarter, quarter, 0.4)
	for k := 0; k < int(quarter/0.25); k++ {
		HatLike(&t, quarter+float64(k)*0.25, 0.35)
	}
	// B: chords
	ChordChange(&t, half, quarter, 0.4, []float64{220}, true)
	ChordChange(&t, half+quarter, quarter, 0.4, []float64{220}, true)
	return t
}

// TempoRamp is a click train whose tempo rises linearly from lo to hi BPM.
func TempoRamp(loBPM, hiBPM, seconds float64, amp float64) StereoTrack {
	t := newTrack(nSamples(seconds))
	tPos := 0.0
	beat := 0
	for tPos < seconds {
		frac := tPos / seconds
		bpm := loBPM + (hiBPM-loBPM)*frac
		s := int(tPos * SampleRate)
		dur := int(0.05 * SampleRate)
		gen := adsr(sine(1000), 0.0005, 0.012, s, s+dur)
		for i := s; i < s+dur && i < len(t.L); i++ {
			v := float32(amp * gen(i))
			t.L[i] += v
			t.R[i] += v
		}
		tPos += 60.0 / bpm
		beat++
	}
	return t
}

// HalfDouble is a click train at 120 BPM with accents every 2 beats —
// deliberately ambiguous between 60 and 120 BPM readings.
func HalfDouble(seconds float64, amp float64) StereoTrack {
	t := newTrack(nSamples(seconds))
	ClickTrain(&t, 0, seconds, 120, amp, 1.6, 2)
	return t
}

// CorpusFixture builds any named fixture for tests. Names are stable
// identifiers used across the analysis test-suite.
func CorpusFixture(name string, seconds float64, seed uint64) (StereoTrack, error) {
	switch name {
	case "silence":
		return Silence(seconds, seed), nil
	case "dc":
		return DCOffset(seconds, seed), nil
	case "clipped":
		return Clipped(seconds, seed), nil
	case "tone-55":
		return Tone(55, seconds, 0.6), nil
	case "tone-440":
		return Tone(440, seconds, 0.5), nil
	case "tone-6000":
		return Tone(6000, seconds, 0.4), nil
	case "sweep":
		return Sweep(60, 8000, seconds, 0.5), nil
	case "harmonic-stack":
		return HarmonicStack(220, 8, seconds, 0.5), nil
	case "inharmonic":
		return Inharmonic(seconds, 0.5), nil
	case "noise-white":
		return NoiseWhite(seconds, seed), nil
	case "noise-pink":
		return NoisePink(seconds, seed), nil
	case "panned-left":
		return Panned(seconds, 440, -0.8, 0.5), nil
	case "antiphase":
		return AntiPhase(seconds, 440, 0.5), nil
	case "mono":
		return Tone(440, seconds, 0.5), nil
	case "wide-stereo":
		return WideStereo(seconds, seed), nil
	case "half-double":
		return HalfDouble(seconds, 0.6), nil
	case "tempo-ramp":
		return TempoRamp(90, 150, seconds, 0.5), nil
	case "crescendo":
		return Crescendo(seconds, 330, 0.7), nil
	case "fade":
		return FadeInFadeOut(seconds, 440, 0.5, 0.8), nil
	case "drop":
		return DropStructure(seed), nil
	case "repeat":
		return RepeatedSection(seconds, seed), nil
	case "syncopated":
		t := newTrack(nSamples(seconds))
		Syncopated(&t, 0.1, seconds, 120, 0.55)
		return t, nil
	default:
		return StereoTrack{}, fmt.Errorf("corpus: unknown fixture %q", name)
	}
}
