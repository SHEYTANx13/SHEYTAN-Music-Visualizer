// Package dsp owns local audio analysis: streaming FFT spectrum
// (Gonum, Hann-windowed), RMS/loudness envelope, perceptual frequency
// bands (bass/mid/treble), spectral centroid, spectral flux/onset
// strength, tempo estimate with explicit confidence, beat/onset events
// and a compact waveform envelope for timeline navigation.
//
// Determinism: analysis derives only from the canonical PCM content
// and fixed parameters; the same cache always produces the same
// results (verified by tests). Analysis never uses wall-clock time or
// global randomness.
package dsp

import (
	"math"
	"math/cmplx"

	"gonum.org/v1/gonum/dsp/fourier"
)

// Fixed analysis parameters (documented contract, see readme.md).
const (
	// SampleRate matches the canonical PCM rate.
	SampleRate = 48000
	// FFTSize is the STFT window size in samples (≈42.7 ms).
	FFTSize = 2048
	// HopSize is the uniform STFT hop in samples (≈10.7 ms, 75% overlap).
	HopSize = 512
	// BandSplitHz are the bass/mid/treble boundaries.
	BassMaxHz = 250.0
	MidMaxHz  = 4000.0
	// AnalysisVersion bumps when analysis semantics change; it keys
	// the per-track sidecar cache.
	// v2 (run 43541087097): WaveformPointsPerSecond raised 2 → 60 so the
	// Waveform visualizer mode draws a real envelope trace (the ±2 s
	// window previously carried only 8 points); consumers of Result
	// JSON can distinguish generations via this field.
	AnalysisVersion = 2
	// WaveformPointsPerSecond fixes the envelope resolution. 60 pts/s
	// gives the 4-second waveform window 240 points (a visible trace)
	// while staying bounded (a 2 h track ≈ 1.7 MB of envelope).
	WaveformPointsPerSecond = 60
	// maxFrames caps stored frames (≈2 hours of audio).
	maxFrames = 720000
)

// Frame is one analysis hop with its derived metrics.
type Frame struct {
	Time     float64 `json:"time"` // seconds (window start)
	RMS      float64 `json:"rms"`  // windowed RMS (≈0..1)
	Bass     float64 `json:"bass"` // band energy share (0..1)
	Mid      float64 `json:"mid"`
	Treble   float64 `json:"treble"`
	Centroid float64 `json:"centroid"` // spectral centroid, Hz
	// Rolloff is the frequency below which 85%% of the window's
	// spectral energy accumulates (Hz). 0 when the window is silent.
	Rolloff float64  `json:"rolloff"`
	Flux    float64  `json:"flux"`  // positive spectral flux
	Onset   bool     `json:"onset"` // adaptive-threshold onset
	Bins    Spectrum `json:"bins"`  // display spectrum (0..1), len FFTSize/2+1
}

// Spectrum is one display spectrum (0..1 per bin).
type Spectrum []float64

// Result is the complete analysis of one track.
type Result struct {
	Version    int     `json:"version"`
	Frames     []Frame `json:"frames"`
	HopSeconds float64 `json:"hopSeconds"`
	Duration   float64 `json:"duration"`

	// Summary metrics (measured, never invented).
	PeakRMS      float64 `json:"peakRMS"`
	MeanRMS      float64 `json:"meanRMS"`
	BassEnergy   float64 `json:"bassEnergy"`
	MidEnergy    float64 `json:"midEnergy"`
	TrebleEnergy float64 `json:"trebleEnergy"`
	MeanCentroid float64 `json:"meanCentroid"`
	// MeanRolloff is the mean spectral rolloff frequency (Hz).
	MeanRolloff float64 `json:"meanRolloff"`
	OnsetCount  int     `json:"onsetCount"`

	// Tempo — reported with confidence; never presented as certainty.
	BPM             float64 `json:"bpm"`
	TempoConfidence float64 `json:"tempoConfidence"` // 0..1; <0.35 unreliable
	TempoReliable   bool    `json:"tempoReliable"`
	TempoNote       string  `json:"tempoNote,omitempty"`

	// Waveform envelope for timeline navigation.
	Waveform    []float32 `json:"waveform"`
	WaveformMax float32   `json:"waveformMax"`
}

// binsChunkFrames is the number of per-frame display spectra pooled in
// one backing allocation (64 frames ≈ 0.69 s of audio ≈ 520 KB chunk).
const binsChunkFrames = 64

// hannWindow is the precomputed Hann window for FFTSize.
var hannWindow = buildHann(FFTSize)

func buildHann(n int) []float64 {
	w := make([]float64, n)
	for i := range w {
		w[i] = 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/float64(n-1)))
	}
	return w
}

// binHz is the center frequency of analysis bin i.
func binHz(i int) float64 { return float64(i) * SampleRate / FFTSize }

// binHzTable precomputes the center frequency of every analysis bin
// once (run-42921967187 performance repair: emitFrame previously
// re-evaluated the same division twice per bin per frame in the
// centroid/band pass and again in the rolloff pass). Values are
// identical to binHz(i) — same expression, same float64 rounding — so
// analysis output stays deterministic and unchanged.
var binHzTable = buildBinHz()

func buildBinHz() []float64 {
	t := make([]float64, FFTSize/2+1)
	for i := range t {
		t[i] = binHz(i)
	}
	return t
}

// Analyzer converts canonical interleaved stereo float32 samples into
// a Result. One instance analyzes one stream; it is not safe for
// concurrent use. FFT plans and scratch buffers are allocated once
// and reused, so per-track allocation is bounded to the frame list.
type Analyzer struct {
	fft   *fourier.FFT
	buf   []float64 // windowed input scratch (oldest→newest)
	coeff []complex128
	spec  []float64 // raw magnitude scratch
	prev  []float64 // previous raw magnitudes for flux
	wrk   []float64 // input ring (FFTSize)

	frames   []Frame
	waveform []float32

	// binsBacking chunks the per-frame display spectra into one
	// allocation per binsChunkFrames frames (run-42921967187
	// performance repair: emitFrame previously made one 1025-float64
	// allocation per hop, ≈5.6k allocator calls and ≈46 MB of churn
	// per minute of audio). Frame.Bins still slices into this
	// backing store, so JSON output and byte-level analysis results
	// are unchanged.
	binsBacking []float64
	binsUsed    int

	samplesPerPoint int
	samplesInPoint  int
	wfMax           float64
	sampleIdx       int
}

// NewAnalyzer creates an analyzer.
func NewAnalyzer() *Analyzer {
	a := &Analyzer{
		fft:  fourier.NewFFT(FFTSize),
		buf:  make([]float64, FFTSize),
		wrk:  make([]float64, FFTSize),
		spec: make([]float64, FFTSize/2+1),
	}
	a.coeff = make([]complex128, FFTSize/2+1)
	return a
}

// Reset clears state for a new stream.
func (a *Analyzer) Reset() {
	for i := range a.wrk {
		a.wrk[i] = 0
	}
	a.prev = nil
	a.binsBacking = nil
	a.binsUsed = 0
	a.frames = a.frames[:0]
	a.waveform = a.waveform[:0]
	a.samplesPerPoint = 0
	a.samplesInPoint = 0
	a.wfMax = 0
	a.sampleIdx = 0
}

// Process consumes interleaved stereo float32 samples. Call Finish
// after the stream ends. Any chunking is allowed.
func (a *Analyzer) Process(samples []float32) {
	if a.samplesPerPoint == 0 {
		// frames per envelope point (stereo pairs are counted as frames)
		a.samplesPerPoint = SampleRate / WaveformPointsPerSecond
	}
	for i := 0; i+1 < len(samples); i += 2 {
		mono := (float64(samples[i]) + float64(samples[i+1])) * 0.5
		a.feedSample(mono)
	}
}

func (a *Analyzer) feedSample(mono float64) {
	// waveform envelope (peak abs per ~0.5 s point)
	if al := math.Abs(mono); al > a.wfMax {
		a.wfMax = al
	}
	a.samplesInPoint++
	if a.samplesInPoint >= a.samplesPerPoint {
		a.waveform = append(a.waveform, float32(math.Min(a.wfMax, 1)))
		a.wfMax = 0
		a.samplesInPoint = 0
	}
	// STFT ring buffer
	pos := a.sampleIdx % FFTSize
	a.wrk[pos] = mono
	a.sampleIdx++
	if a.sampleIdx >= FFTSize && a.sampleIdx%HopSize == 0 {
		a.emitFrame()
	}
}

// emitFrame windows the most recent FFTSize samples (ring order) and
// computes one Frame.
func (a *Analyzer) emitFrame() {
	start := a.sampleIdx % FFTSize // oldest element
	copy(a.buf, a.wrk[start:])
	copy(a.buf[FFTSize-start:], a.wrk[:start])

	// windowed RMS (measured loudness envelope)
	var sq float64
	for i, x := range a.buf {
		wx := x * hannWindow[i]
		a.buf[i] = wx
		sq += wx * wx
	}
	rms := math.Sqrt(sq / FFTSize)

	// real-input FFT of the windowed frame; gonum returns N/2+1
	// coefficients (DC..Nyquist) directly.
	coeff := a.fft.Coefficients(a.coeff, a.buf)
	half := len(coeff) - 1

	var bassSum, midSum, trebleSum, totalSum float64
	var centNum, centDen float64
	spec := a.spec
	for i := 0; i <= half && i < len(spec); i++ {
		m := cmplx.Abs(coeff[i])
		spec[i] = m
		var e float64 // energy contribution: |X|^2, doubled on interior bins
		if i == 0 || i == half {
			e = m * m
		} else {
			e = 2 * m * m
		}
		totalSum += e
		ms := m * m
		centNum += binHzTable[i] * ms
		centDen += ms
		f := binHzTable[i]
		switch {
		case f <= BassMaxHz:
			bassSum += e
		case f <= MidMaxHz:
			midSum += e
		default:
			trebleSum += e
		}
	}
	norm := func(x float64) float64 {
		if totalSum <= 0 {
			return 0
		}
		return math.Min(1, x/totalSum)
	}

	// spectral rolloff: the frequency below which 85% of the
	// window's energy accumulates. Deterministic; 0 on silence.
	rolloff := 0.0
	if totalSum > 0 {
		cum := 0.0
		for i := 0; i <= half && i < len(spec); i++ {
			m := spec[i]
			var e float64
			if i == 0 || i == half {
				e = m * m
			} else {
				e = 2 * m * m
			}
			cum += e
			if cum >= 0.85*totalSum {
				rolloff = binHzTable[i]
				break
			}
		}
	}

	// positive spectral flux on raw magnitudes (bounded, mean per bin)
	var flux float64
	if a.prev != nil {
		for i, m := range spec {
			if i < len(a.prev) {
				if d := m - a.prev[i]; d > 0 {
					flux += d
				}
			}
		}
		flux /= float64(len(spec))
	}
	if a.prev == nil {
		a.prev = make([]float64, len(spec))
	}
	copy(a.prev, spec)

	// display spectrum: dB-scaled, bounded to 96 dB, 0..1. Sliced
	// from the chunked backing store instead of one allocation per
	// frame (run-42921967187 performance repair; identical values,
	// identical JSON shape).
	bins := a.nextBins(half + 1)
	for i, m := range spec {
		db := 20 * math.Log10(m+1e-9)
		if db < -96 {
			db = -96
		}
		if db > 0 {
			db = 0
		}
		bins[i] = (db + 96) / 96
	}
	centroid := 0.0
	if centDen > 0 {
		centroid = centNum / centDen
	}
	fr := Frame{
		Time:     float64(a.sampleIdx-FFTSize) / SampleRate,
		RMS:      rms,
		Bass:     norm(bassSum),
		Mid:      norm(midSum),
		Treble:   norm(trebleSum),
		Centroid: centroid,
		Rolloff:  rolloff,
		Flux:     flux,
		Bins:     bins,
	}
	a.frames = append(a.frames, fr)
	if len(a.frames) > maxFrames {
		a.frames = a.frames[1:]
	}
}

// nextBins returns the next half+1 slice of the chunked backing store,
// allocating a fresh chunk when the current one is exhausted.
func (a *Analyzer) nextBins(n int) Spectrum {
	need := a.binsUsed + n
	if len(a.binsBacking) < need {
		chunk := binsChunkFrames * n
		if chunk < n {
			chunk = n
		}
		a.binsBacking = make([]float64, chunk)
		a.binsUsed = 0
	}
	s := a.binsBacking[a.binsUsed : a.binsUsed+n : a.binsUsed+n]
	a.binsUsed += n
	return Spectrum(s)
}

// Finish computes onset events, summary metrics and the tempo
// estimate, then returns the result. The analyzer can be Reset and
// reused afterwards.
func (a *Analyzer) Finish() *Result {
	frames := a.frames
	res := &Result{
		Version:    AnalysisVersion,
		Frames:     frames,
		HopSeconds: float64(HopSize) / SampleRate,
		Waveform:   a.waveform,
		Duration:   float64(a.sampleIdx) / SampleRate,
	}
	if len(frames) == 0 {
		res.TempoNote = "no analysis frames"
		return res
	}
	var sumRMS, sumB, sumM, sumT, sumC, sumR float64
	peak := 0.0
	for _, f := range frames {
		sumRMS += f.RMS
		sumB += f.Bass
		sumM += f.Mid
		sumT += f.Treble
		sumC += f.Centroid
		sumR += f.Rolloff
		if f.RMS > peak {
			peak = f.RMS
		}
	}
	n := float64(len(frames))
	res.PeakRMS = peak
	res.MeanRMS = sumRMS / n
	res.BassEnergy = sumB / n
	res.MidEnergy = sumM / n
	res.TrebleEnergy = sumT / n
	res.MeanCentroid = sumC / n
	res.MeanRolloff = sumR / n

	res.OnsetCount = detectOnsets(frames)
	bpm, conf, note := estimateTempo(frames, res.HopSeconds)
	res.BPM = bpm
	res.TempoConfidence = conf
	res.TempoReliable = conf >= 0.35
	res.TempoNote = note

	var wmax float32
	for _, w := range res.Waveform {
		if w > wmax {
			wmax = w
		}
	}
	res.WaveformMax = wmax
	return res
}
