package dsp

// analysis.go — the v3 streaming orchestrator. It drives the legacy
// Analyzer (frames, waveform, coarse tempo) and every measured layer
// from ONE pass over the canonical interleaved stereo PCM:
//
//   - the legacy pipeline stays byte-compatible (same frames/bins);
//   - the frameHook exposes the raw magnitude spectrum so the band,
//     spectral, chroma and HPSS layers reuse the SAME FFT computation;
//   - stereo metrics are computed from the pure L/R windows before any
//     mid/downmix is formed (§6.1: stereo information is never
//     destroyed; the mono path uses the mid channel and anti-phase
//     material is reported, never interpreted as silence);
//   - loudness (BS.1770-5) runs on the interleaved stream;
//   - musical layers (rhythm, harmony, vocal-flow, roles, structure)
//     run at Finish over the measured series;
//
// Determinism: results depend only on PCM content and fixed
// parameters. Chunk-size independence is verified by tests. Bounded
// memory: series are float32 and events are sparse; per-track
// allocation is a documented linear function of duration.

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
)

// AnalysisInput is the canonical PCM source: interleaved stereo
// float32 little-endian at SampleRate, streamed in arbitrary chunks.
type AnalysisInput interface {
	// ReadChunk returns the next chunk of interleaved float32 samples.
	// It returns io.EOF (with a possibly empty chunk) when the stream
	// ends. Implementations must be deterministic per content.
	ReadChunk() ([]float32, error)
}

// ReaderInput adapts an io.Reader of raw float32-LE interleaved PCM
// (the audio cache raw format) to AnalysisInput.
type ReaderInput struct {
	r io.Reader
}

// NewReaderInput wraps a raw PCM reader.
func NewReaderInput(r io.Reader) *ReaderInput { return &ReaderInput{r: r} }

// ReadChunk reads up to one second of PCM per call. A partial final
// chunk (not a multiple of 4 bytes) is a truncated-stream error; a
// clean end returns io.EOF with whatever complete samples remain.
func (ri *ReaderInput) ReadChunk() ([]float32, error) {
	const f32PerChunk = 2 * SampleRate
	b := make([]byte, 4*f32PerChunk)
	total := 0
	eof := false
	for total < len(b) {
		m, err := ri.r.Read(b[total:])
		total += m
		if err != nil {
			if err != io.EOF {
				return nil, err
			}
			eof = true
			break
		}
	}
	if total%4 != 0 {
		return nil, fmt.Errorf("dsp: truncated PCM stream (%d bytes, not a multiple of 4)", total)
	}
	out := decodeLEFloat32(b[:total])
	if eof && len(out) == 0 {
		return nil, io.EOF
	}
	return out, nil
}

// decodeLEFloat32 decodes little-endian float32 samples.
func decodeLEFloat32(b []byte) []float32 {
	out := make([]float32, len(b)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return out
}

// fullAnalyzer owns the streaming state of one analysis pass.
type fullAnalyzer struct {
	legacy     *Analyzer
	bt         *BandTracker
	sp         SpectralSeries
	chroma     []ChromaPoint
	hp         *HPSS
	hpss       HPSSSeries
	mr         *MultiRes
	lm         *LoudnessMeter
	st         StereoSeries
	sampleIdx  int       // stereo sample-pair count
	frameIndex int       // legacy frames emitted (drives the band tracker)
	prevMag    []float64 // previous raw magnitudes for band flux

	lWin, rWin []float64 // hop-window L/R scratch (FFTSize)
	pendingL   []float64 // last <= FFTSize L samples (hop-drained)
	pendingR   []float64
}

// AnalyzeFull runs the complete v3 analysis over the input. ctx
// cancellation is honored between chunks; the returned error wraps
// context.Canceled so callers can distinguish cancellation from
// failure. The analysis is deterministic and chunk-size independent.
func AnalyzeFull(ctx context.Context, in AnalysisInput, fingerprint, decoderID string) (*Analysis, error) {
	fa := newFullAnalyzer()
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("dsp: analysis canceled: %w", err)
		}
		chunk, err := in.ReadChunk()
		if err != nil && err != io.EOF {
			return nil, fmt.Errorf("dsp: reading analysis input: %w", err)
		}
		if len(chunk) > 0 {
			fa.Process(chunk)
		}
		if err == io.EOF {
			break
		}
	}
	return fa.Finish(fingerprint, decoderID), nil
}

// AnalyzeFullReader is a convenience wrapper for an io.Reader of raw
// interleaved float32-LE PCM (the audio cache format).
func AnalyzeFullReader(ctx context.Context, r io.Reader, fingerprint, decoderID string) (*Analysis, error) {
	return AnalyzeFull(ctx, NewReaderInput(r), fingerprint, decoderID)
}

func newFullAnalyzer() *fullAnalyzer {
	fa := &fullAnalyzer{
		legacy: NewAnalyzer(),
		bt:     NewBandTracker(),
		hp:     NewHPSS(),
		mr:     NewMultiRes(),
		lm:     NewLoudnessMeter(),
	}
	fa.lWin = make([]float64, FFTSize)
	fa.rWin = make([]float64, FFTSize)
	a := fa.legacy
	a.frameHook = func(mag []float64, windowed []float64) {
		// Band layer (energy share, relative flux, calibrated envelope).
		fa.bt.Update(mag, fa.prevMag, fa.frameIndex)
		// Spectral descriptor layer + chroma + tuning.
		AppendSpectralPoint(&fa.sp, ComputeSpectralFrame(mag, windowed))
		tOff := EstimateTuningOffsetCents(mag)
		fa.chroma = append(fa.chroma, ComputeChromaPoint(mag, tOff))
		// HPSS layer (streaming median decomposition).
		fa.hp.Push(mag)
		AppendHPSSPoint(&fa.hpss, fa.hp.Decompose())
		// Remember for the next frame's flux/tuning deltas.
		if cap(fa.prevMag) < len(mag) {
			fa.prevMag = make([]float64, len(mag))
		}
		copy(fa.prevMag, mag)
		fa.frameIndex++
	}
	return fa
}

// Process consumes one chunk of interleaved stereo float32 samples.
func (fa *fullAnalyzer) Process(samples []float32) {
	// 1. Legacy path (frames, display spectrum, waveform envelope) —
	//    consumes the same interleaved stream and emits frames, whose
	//    hook drives the spectral layers.
	fa.legacy.Process(samples)
	// 2. Stereo + loudness + multi-resolution layers, fed per pair.
	pairs := len(samples) / 2
	for i := 0; i < pairs; i++ {
		l := float64(samples[2*i])
		r := float64(samples[2*i+1])
		fa.pendingL = append(fa.pendingL, l)
		fa.pendingR = append(fa.pendingR, r)
		fa.lm.Process([]float64{l, r})
		fa.mr.Feed((l + r) * 0.5)
		fa.sampleIdx++
		// Hop cadence: every HopSize pairs, the legacy analyzer emits a
		// frame whose window is the last FFTSize samples; the stereo
		// layer measures the SAME window (pure L/R, before any mix).
		if fa.sampleIdx%HopSize == 0 && len(fa.pendingL) >= FFTSize {
			copy(fa.lWin, fa.pendingL[len(fa.pendingL)-FFTSize:])
			copy(fa.rWin, fa.pendingR[len(fa.pendingR)-FFTSize:])
			AppendStereoSeries(&fa.st, ComputeStereoFrame(fa.lWin, fa.rWin))
			// Keep the trailing FFTSize-HopSize samples so the next hop
			// still has a full window (append reuses the dropped prefix,
			// keeping the backing array bounded).
			keep := FFTSize - HopSize
			n := len(fa.pendingL)
			copy(fa.pendingL, fa.pendingL[n-keep:])
			copy(fa.pendingR, fa.pendingR[n-keep:])
			fa.pendingL = fa.pendingL[:keep]
			fa.pendingR = fa.pendingR[:keep]
		}
	}
}

// Finish assembles the Analysis document. The layers that run here
// (rhythm, harmony, vocal flow, roles, structure) are pure functions
// of the measured series, so Finish is deterministic and cheap.
func (fa *fullAnalyzer) Finish(fingerprint, decoderID string) *Analysis {
	res := fa.legacy.Finish()
	n := len(res.Frames)
	if n > 0 {
		fa.lm.SetFrameCount(n)
	}
	dyn := fa.lm.Finish()

	rhythm := DetectRhythm(res.Frames, fa.bt.Series(), res.HopSeconds)
	harmony := DetectHarmony(res.Frames, fa.chroma, res.HopSeconds)
	if rhythm != nil && len(rhythm.Beats) > 0 && len(fa.chroma) > 0 {
		// Beat-synchronous chord segmentation refines the boundaries.
		if chords := DetectChords(fa.chroma, rhythm.Beats, res.HopSeconds); len(chords) > 0 {
			harmony.Chords = chords
			harmony.ChordChanges = harmony.ChordChanges[:0]
			for _, c := range chords {
				harmony.ChordChanges = append(harmony.ChordChanges, c.Start)
			}
			harmony.Progressions = harmonyProgressions(chords, res.HopSeconds)
		}
	}
	vocal := EstimateVocalFlow(res.Frames, fa.bt.Series(), &fa.hpss, &fa.sp, res.HopSeconds)
	roles := EstimateRoles(fa.bt.Series(), &fa.hpss, rhythm, &fa.sp, res.HopSeconds, res.Frames)
	structure := DetectStructure(res.Frames, fa.chroma, fa.bt.Series(), rhythm, res.HopSeconds)

	a := &Analysis{
		SchemaVersion:     AnalysisSchemaVersion,
		SourceFingerprint: fingerprint,
		Duration:          res.Duration,
		SampleRate:        SampleRate,
		Channels:          2,
		DecoderID:         decoderID,
		AnalyzerID:        AnalyzerIDV3,
		HopSeconds:        res.HopSeconds,
		FrameCount:        n,
		Legacy:            res,
		Stereo:            &fa.st,
		Bands:             fa.bt.Series(),
		Spectral:          &fa.sp,
		HPSS:              &fa.hpss,
		MultiRes:          fa.mr.Series(),
		Rhythm:            rhythm,
		Harmony:           harmony,
		Vocal:             vocal,
		Roles:             roles,
		Structure:         structure,
		Dynamics:          &dyn,
	}
	a.collectWarnings()
	return a
}

// collectWarnings records measured limitations so consumers never
// confuse "missing" with zero or silence (§6.1, §6.5).
func (a *Analysis) collectWarnings() {
	if r := a.Stereo.AntiPhaseRatio(); r >= 0.2 {
		a.Warnings = append(a.Warnings, fmt.Sprintf(
			"anti-phase stereo material detected in %.0f%% of measurable frames; the mid channel legitimately cancels and MUST NOT be interpreted as silence", r*100))
	}
	if a.Rhythm != nil && !a.Rhythm.TempoReliable && a.Duration > 5 {
		a.Warnings = append(a.Warnings, "tempo estimate is unreliable (confidence below 0.35); beat timeline intentionally omitted rather than fabricated")
	}
	if a.Harmony != nil && a.Harmony.Key == "" && a.Duration > 10 {
		a.Warnings = append(a.Warnings, "no key candidate cleared the confidence floor; key reported as unknown")
	}
	if a.Vocal != nil && a.Vocal.OverallConfidence > 0 && a.Vocal.OverallConfidence < 0.4 {
		a.Warnings = append(a.Warnings, "vocal-like activity confidence is low; vocal-reactive layers should fall back gracefully")
	}
	if a.FrameCount == 0 {
		a.Warnings = append(a.Warnings, "no analysis frames were produced (empty or sub-window input)")
		a.Unavailable = append(a.Unavailable,
			"rhythm", "harmony", "vocal-flow", "roles", "structure")
	}
	if a.Stereo != nil && len(a.Stereo.Correlation) == 0 && a.Duration > 0 {
		a.Unavailable = append(a.Unavailable, "stereo-metrics")
	}
}

// harmonyProgressions recomputes progressions after beat-synchronous
// chord refinement (mzProgressions lives in harmony.go).
func harmonyProgressions(chords []ChordEvent, hopSec float64) []ProgressionSegment {
	return mzProgressions(chords, hopSec)
}

// Serialize encodes the analysis document deterministically.
func (a *Analysis) Serialize() ([]byte, error) {
	b, err := json.Marshal(a)
	if err != nil {
		return nil, fmt.Errorf("dsp: encoding analysis: %w", err)
	}
	return b, nil
}

// LoadAnalysis decodes an analysis document and enforces the schema
// version: a mismatched version is an error, NEVER reinterpreted with
// new semantics (§6.11 safe cache invalidation). The caller must
// re-analyze or discard the sidecar.
func LoadAnalysis(data []byte) (*Analysis, error) {
	var a Analysis
	if err := json.Unmarshal(data, &a); err != nil {
		return nil, fmt.Errorf("dsp: decoding analysis: %w", err)
	}
	if a.SchemaVersion != AnalysisSchemaVersion {
		return nil, fmt.Errorf("dsp: analysis schema version %d does not match required %d; the cached document must be regenerated",
			a.SchemaVersion, AnalysisSchemaVersion)
	}
	if strings.TrimSpace(a.SourceFingerprint) == "" {
		return nil, fmt.Errorf("dsp: analysis document has no source fingerprint")
	}
	return &a, nil
}

// Validate checks the invariants every layer must uphold: no NaN/Inf
// anywhere in the stored series, aligned series lengths, monotonic
// event times, and a sane duration.
func (a *Analysis) Validate() error {
	if a.SampleRate != SampleRate {
		return fmt.Errorf("dsp: analysis sample rate %d != canonical %d", a.SampleRate, SampleRate)
	}
	if a.Duration < 0 || a.Duration > float64(maxFrames)*float64(HopSize)/SampleRate+1 {
		return fmt.Errorf("dsp: analysis duration %v out of contract", a.Duration)
	}
	for i := range a.Warnings {
		if len(a.Warnings[i]) > 1024 {
			return fmt.Errorf("dsp: warning %d exceeds the length contract", i)
		}
	}
	if a.Bands != nil {
		for b := 0; b < BandCount; b++ {
			if len(a.Bands.Energy[b]) != a.FrameCount ||
				len(a.Bands.Flux[b]) != a.FrameCount ||
				len(a.Bands.Envelope[b]) != a.FrameCount {
				return fmt.Errorf("dsp: band %d series length != frame count", b)
			}
		}
	}
	if a.Stereo != nil {
		for _, arr := range [][]float32{a.Stereo.LEnergy, a.Stereo.REnergy,
			a.Stereo.MEnergy, a.Stereo.SEnergy, a.Stereo.Width, a.Stereo.Correlation} {
			if len(arr) != a.FrameCount {
				return fmt.Errorf("dsp: stereo series length != frame count")
			}
		}
	}
	if a.Spectral != nil {
		for _, arr := range [][]float32{a.Spectral.Bandwidth, a.Spectral.Contrast,
			a.Spectral.Flatness, a.Spectral.Entropy, a.Spectral.ZCR,
			a.Spectral.Harmonicity, a.Spectral.Inharmonicity} {
			if len(arr) != a.FrameCount {
				return fmt.Errorf("dsp: spectral series length != frame count")
			}
		}
	}
	if a.HPSS != nil {
		for _, arr := range [][]float32{a.HPSS.Harmonic, a.HPSS.Percussive, a.HPSS.Residual} {
			if len(arr) != a.FrameCount {
				return fmt.Errorf("dsp: hpss series length != frame count")
			}
		}
	}
	checkF32 := func(name string, arr []float32) error {
		for _, v := range arr {
			f := float64(v)
			if f != f || f > 1e30 || f < -1e30 {
				return fmt.Errorf("dsp: %s contains a non-finite value", name)
			}
		}
		return nil
	}
	if a.Stereo != nil {
		for _, arr := range [][]float32{a.Stereo.LEnergy, a.Stereo.REnergy, a.Stereo.MEnergy,
			a.Stereo.SEnergy, a.Stereo.Width, a.Stereo.Correlation} {
			if err := checkF32("stereo", arr); err != nil {
				return err
			}
		}
	}
	if a.Bands != nil {
		for b := 0; b < BandCount; b++ {
			for _, arr := range [][]float32{a.Bands.Energy[b], a.Bands.Flux[b], a.Bands.Envelope[b]} {
				if err := checkF32("bands", arr); err != nil {
					return err
				}
			}
		}
	}
	prev := -1.0
	for _, t := range a.Structure {
		if t.Start < prev-1e-9 || t.End <= t.Start {
			return fmt.Errorf("dsp: structure events not sorted/valid at %v", t.Start)
		}
		prev = t.Start
	}
	return nil
}
