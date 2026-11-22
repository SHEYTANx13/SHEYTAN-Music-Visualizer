package dsp

// multires.go — v3 multi-resolution layer (§ analysis_types.go
// "Multi-resolution layer"). Two auxiliary resolutions run alongside
// the main STFT:
//
//   - Short: 512-sample window / 128-sample hop (~2.67 ms) resolves
//     attack sharpness and transient micro-timing. Emits windowed RMS
//     Energy, the positive energy slope vs the previous short frame
//     (Attack, 0..1) and Sharpness (normalized rise fraction, 0..1).
//   - Long: 16384-sample window / 2048-sample hop (~42.7 ms, 2.93 Hz
//     bins) resolves sub-bass and sustained harmonic detail. Emits the
//     20..60 Hz share (SubEnergy), the 60..250 Hz share (BassBody) and
//     a long-window harmonicity (LowHarmonicity, 0..1) from a harmonic
//     product spectrum searched over 40..400 Hz.
//
// FFT plans (gonum fourier.NewFFT(512) and NewFFT(16384)), Hann
// windows and all scratch buffers are allocated once in NewMultiRes;
// per-sample cost is bounded and no per-frame allocation happens
// except the (bounded) series appends. Series are capped at
// MultiResMaxSecs by dropping the oldest entries (documented linear
// memory: short ≈ 3·SR/ShortHopSize·4 B per second of audio).
//
// Determinism: Feed is per-sample state, so any chunking of the same
// sample stream produces byte-identical series (verified by tests).

import (
	"math"

	"gonum.org/v1/gonum/dsp/fourier"
)

// MultiRes feeds a mono sample stream and accumulates both auxiliary
// timelines. One instance analyzes one stream; not safe for
// concurrent use.
type MultiRes struct {
	fftShort  *fourier.FFT
	fftLong   *fourier.FFT
	hannShort []float64
	hannLong  []float64

	shortRing  []float64 // ShortFFTSize samples
	longRing   []float64 // LongFFTSize samples
	sPos, lPos int
	fed        int

	shortWin  []float64 // windowed-input scratch (reused)
	longWin   []float64
	shortCoef []complex128 // FFT output scratch (reused)
	longCoef  []complex128
	shortMag  []float64
	longMag   []float64

	prevE    float64 // previous short-frame energy
	havePrev bool

	short ShortSeries
	long  LongSeries
}

// NewMultiRes allocates the analyzer (FFT plans and windows built once).
func NewMultiRes() *MultiRes {
	m := &MultiRes{
		fftShort:  fourier.NewFFT(ShortFFTSize),
		fftLong:   fourier.NewFFT(LongFFTSize),
		hannShort: buildHann(ShortFFTSize),
		hannLong:  buildHann(LongFFTSize),
		shortRing: make([]float64, ShortFFTSize),
		longRing:  make([]float64, LongFFTSize),
		shortWin:  make([]float64, ShortFFTSize),
		longWin:   make([]float64, LongFFTSize),
		shortMag:  make([]float64, ShortFFTSize/2+1),
		longMag:   make([]float64, LongFFTSize/2+1),
	}
	m.shortCoef = make([]complex128, ShortFFTSize/2+1)
	m.longCoef = make([]complex128, LongFFTSize/2+1)
	m.short.HopSeconds = float64(ShortHopSize) / float64(SampleRate)
	m.long.HopSeconds = float64(LongHopSize) / float64(SampleRate)
	return m
}

// Feed consumes one mono sample. Short frames are emitted every
// ShortHopSize samples once ShortFFTSize are buffered; long frames
// every LongHopSize samples once LongFFTSize are buffered.
func (m *MultiRes) Feed(mono float64) {
	if !finite(mono) {
		mono = 0
	}
	m.shortRing[m.sPos] = mono
	m.sPos++
	if m.sPos == ShortFFTSize {
		m.sPos = 0
	}
	m.longRing[m.lPos] = mono
	m.lPos++
	if m.lPos == LongFFTSize {
		m.lPos = 0
	}
	m.fed++
	if m.fed >= ShortFFTSize && m.fed%ShortHopSize == 0 {
		m.emitShort()
	}
	if m.fed >= LongFFTSize && m.fed%LongHopSize == 0 {
		m.emitLong()
	}
}

// emitShort windows the last ShortFFTSize samples and appends one
// short-frame point (Energy, Attack, Sharpness — all 0..1).
func (m *MultiRes) emitShort() {
	n := ShortFFTSize
	start := m.sPos // ring position of the oldest sample
	var sq float64
	for i := 0; i < n; i++ {
		x := m.shortRing[(start+i)%n] * m.hannShort[i]
		m.shortWin[i] = x
		sq += x * x
	}
	e := math.Sqrt(sq / float64(n))

	attack, sharp := 0.0, 0.0
	if m.havePrev {
		if rise := e - m.prevE; rise > 0 {
			// Attack: positive energy slope vs the previous short
			// frame, relative to the previous level (floored so a
			// rise out of silence saturates), clamped 0..1.
			attack = clampUnit(rise / math.Max(m.prevE, 1e-4))
			// Sharpness: normalized rise fraction — the share of
			// current+previous energy that appeared within one hop,
			// doubled to use the full 0..1 range (1 = step onset).
			sharp = clampUnit(2 * rise / (e + m.prevE + 1e-9))
		}
	}

	m.short.Energy = append(m.short.Energy, sanitizeFloat32(e, 0, 1))
	m.short.Attack = append(m.short.Attack, float32(attack))
	m.short.Sharpness = append(m.short.Sharpness, float32(sharp))
	m.prevE = e
	m.havePrev = true
	m.trimShort()
}

// emitLong windows the last LongFFTSize samples, FFTs it once and
// appends one long-frame point (shares 0..1).
func (m *MultiRes) emitLong() {
	n := LongFFTSize
	start := m.lPos
	for i := 0; i < n; i++ {
		m.longWin[i] = m.longRing[(start+i)%n] * m.hannLong[i]
	}
	m.longCoef = m.fftLong.Coefficients(m.longCoef, m.longWin)
	for i, c := range m.longCoef {
		m.longMag[i] = math.Hypot(real(c), imag(c))
	}
	binW := float64(SampleRate) / float64(LongFFTSize)
	sub := bandShareBins(m.longMag, 20, 60, binW)
	bass := bandShareBins(m.longMag, 60, 250, binW)
	f0 := hpsFundamental(m.longMag, binW, 40, 400)
	lowH := harmonicPowerShare(m.longMag, binW, f0)

	m.long.SubEnergy = append(m.long.SubEnergy, sanitizeFloat32(sub, 0, 1))
	m.long.BassBody = append(m.long.BassBody, sanitizeFloat32(bass, 0, 1))
	m.long.LowHarmonicity = append(m.long.LowHarmonicity, sanitizeFloat32(lowH, 0, 1))
	m.trimLong()
}

// Series returns the accumulated timelines (pointers alias tracker
// state; HopSeconds fields are hop/SampleRate).
func (m *MultiRes) Series() *MultiResData {
	return &MultiResData{Short: &m.short, Long: &m.long}
}

// trimShort/trimLong cap the series at MultiResMaxSecs of coverage by
// dropping the oldest entries (all arrays of a series drop in lockstep
// so frame f stays time f·HopSeconds).
func (m *MultiRes) trimShort() {
	max := MultiResMaxSecs * SampleRate / ShortHopSize
	m.short.Energy = trimF32(m.short.Energy, max)
	m.short.Attack = trimF32(m.short.Attack, max)
	m.short.Sharpness = trimF32(m.short.Sharpness, max)
}

func (m *MultiRes) trimLong() {
	max := MultiResMaxSecs * SampleRate / LongHopSize
	m.long.SubEnergy = trimF32(m.long.SubEnergy, max)
	m.long.BassBody = trimF32(m.long.BassBody, max)
	m.long.LowHarmonicity = trimF32(m.long.LowHarmonicity, max)
}

// trimF32 drops the oldest entries when s exceeds max. Front slack is
// compacted in place in chunks so the backing array stays bounded.
func trimF32(s []float32, max int) []float32 {
	if len(s) <= max {
		return s
	}
	drop := len(s) - max
	if drop >= 4096 {
		copy(s[:max], s[drop:])
		return s[:max]
	}
	return s[drop:]
}
