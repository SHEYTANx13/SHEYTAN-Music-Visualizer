package dsp

// This file is the v3 analysis contract: the versioned Analysis
// document produced by the streaming analyzer (analysis.go) and the
// per-layer types implemented by the analysis sub-modules (stereo.go,
// bands.go, spectral.go, hpss.go, multires.go, loudness.go, rhythm.go,
// harmony.go, vocalflow.go, roles.go, structure.go).
//
// Design rules that apply to every layer:
//
//   - Deterministic: a layer depends only on the canonical PCM content
//     and fixed parameters. No wall-clock time, no global randomness,
//     no map iteration in output assembly.
//   - Bounded: per-frame series use float32 in parallel arrays; event
//     timelines are sparse. Memory is a documented linear function of
//     duration (see the per-type memory notes below).
//   - Honest: every uncertain layer carries a Confidence in 0..1 and
//     may report Unavailable instead of a fabricated value. A stable
//     "unknown" is always preferable to a wrong musical label.
//   - Time-aligned: all times are media seconds (float64, window or
//     event start). Frame series are sampled at the hop grid
//     (hopSeconds); a series value at index i covers time
//     i*hopSeconds. Multi-resolution series carry their own rate.
//   - Compact: the serialized document stores compact frame series and
//     sparse events, never raw spectra or raw audio.
//
// Confidence convention: 0.0 means "no usable evidence", 1.0 means
// "strong converging evidence". 0.5 is a weak but usable estimate.
// Consumers must treat confidence below their configured threshold as
// unavailable (see visualizer confidence gating).

import "math"

// ---------------------------------------------------------------------------
// Top-level document
// ---------------------------------------------------------------------------

// AnalysisSchemaVersion identifies the Analysis document layout. It
// bumps whenever the stored data or the semantics of any field change.
// Audio cache sidecars store this value; mismatched sidecars are
// invalidated, never reinterpreted.
const AnalysisSchemaVersion = 3

// Analysis is the complete, versioned analysis document for one track.
// It embeds the legacy Result (frames, waveform, tempo) for backward
// compatibility with existing consumers and adds the measured layers.
type Analysis struct {
	SchemaVersion int `json:"schemaVersion"`

	// Source identity.
	SourceFingerprint string  `json:"sourceFingerprint"`
	Duration          float64 `json:"duration"`
	SampleRate        int     `json:"sampleRate"`
	Channels          int     `json:"channels"`
	DecoderID         string  `json:"decoderId,omitempty"`
	AnalyzerID        string  `json:"analyzerId"` // e.g. "dsp-analyzer-v3"

	// FrameTiming: hop resolution of the Frame-aligned series.
	HopSeconds float64 `json:"hopSeconds"`
	FrameCount int     `json:"frameCount"`

	// Legacy result (frames with display spectrum, RMS envelope,
	// waveform, coarse tempo). Non-nil for v3 analyses.
	Legacy *Result `json:"legacy,omitempty"`

	// Measured layers. Pointers are nil when the layer was unavailable.
	Stereo   *StereoSeries   `json:"stereo,omitempty"`
	Bands    *BandTimeline   `json:"bands,omitempty"`
	Spectral *SpectralSeries `json:"spectral,omitempty"`
	HPSS     *HPSSSeries     `json:"hpss,omitempty"`
	MultiRes *MultiResData   `json:"multiRes,omitempty"`
	Rhythm   *Rhythm         `json:"rhythm,omitempty"`
	Harmony  *Harmony        `json:"harmony,omitempty"`
	Vocal    *VocalFlow      `json:"vocal,omitempty"`
	// Roles holds heuristic instrument-role intervals (not source
	// separation; see roles.go for the evidence model).
	Roles []RoleInterval `json:"roles,omitempty"`
	// Structure holds candidate structural sections with neutral
	// fallback labels.
	Structure []SectionEvent `json:"structure,omitempty"`
	// Dynamics holds BS.1770-5 loudness and peak measurements.
	Dynamics *LoudnessSummary `json:"dynamics,omitempty"`

	// Warnings are human-readable analysis limitations (ASCII, English),
	// e.g. anti-phase stereo detected, emulator-grade uncertainty.
	Warnings []string `json:"warnings,omitempty"`
	// Unavailable lists named features that could not be measured with
	// usable confidence, so consumers never confuse "missing" with zero.
	Unavailable []string `json:"unavailable,omitempty"`
}

// AnalyzeFullResult pairs the document with an analyzer identity.
const AnalyzerIDV3 = "dsp-analyzer-v3"

// ---------------------------------------------------------------------------
// Stereo layer (stereo.go)
// ---------------------------------------------------------------------------

// StereoSeries is the per-hop stereo measurement timeline. All series
// are FrameCount long (float32).
//
// Memory: 6 * FrameCount * 4 bytes (≈17 MB for a 2 h track).
type StereoSeries struct {
	// LEnergy and REnergy are windowed RMS of the pure left/right
	// channels (linear, ≈0..1).
	LEnergy []float32 `json:"l"`
	REnergy []float32 `json:"r"`
	// MEnergy is mid ((L+R)/2) RMS; SEnergy is side ((L-R)/2) RMS.
	MEnergy []float32 `json:"m"`
	SEnergy []float32 `json:"s"`
	// Width is side/mid RMS ratio mapped to 0..1
	// (width = s/(s+m) guarded; 0 on mono material). It is a measured
	// ratio, not a display gain.
	Width []float32 `json:"width"`
	// Correlation is the windowed Pearson correlation of L and R
	// (-1..1). -1 indicates anti-phase material.
	Correlation []float32 `json:"corr"`
}

// AntiPhaseRatio is the fraction of frames with correlation <= -0.7
// among frames with usable energy.
func (s *StereoSeries) AntiPhaseRatio() float64 {
	if s == nil || len(s.Correlation) == 0 {
		return 0
	}
	var anti, usable int
	for _, c := range s.Correlation {
		if float64(c) <= -0.7 {
			anti++
		}
		usable++
	}
	if usable == 0 {
		return 0
	}
	return float64(anti) / float64(usable)
}

// ---------------------------------------------------------------------------
// Frequency-band layer (bands.go)
// ---------------------------------------------------------------------------

// BandCount is the number of measured bands in the v3 timeline.
const BandCount = 9

// BandEdgeHz are the band edges: 9 bands covering 20 Hz..16 kHz at the
// 48 kHz analysis rate (Nyquist 24 kHz; the 16..24 kHz octave is not a
// separate band — its energy is folded into Brilliance and reported in
// the spectral flatness/entropy of the full spectrum instead).
var BandEdgeHz = [BandCount + 1]float64{20, 60, 120, 250, 500, 1000, 2000, 4000, 8000, 16000}

// BandNames are the visual role names of the bands, matching
// BandEdgeHz pairs. Index 0 is SubBass .. index 8 is Brilliance.
var BandNames = [BandCount]string{
	"sub-bass", "bass", "upper-bass", "low-mids", "midrange",
	"upper-mids", "presence", "high-presence", "brilliance",
}

// BandTimeline is the per-band measurement timeline.
//
// Memory: 3 arrays * BandCount * FrameCount * 4 bytes
// (≈77 MB for 2 h; ≈3 MB for 5 min at hop 512/48 kHz).
type BandTimeline struct {
	// Energy[i][f] is the band's energy share of the frame (0..1,
	// sum over bands <= 1) at hop frame f.
	Energy [BandCount][]float32 `json:"energy"`
	// Flux[i][f] is the positive spectral flux of band i at frame f
	// (mean per bin of the band's magnitude increase, bounded 0..1
	// after calibration).
	Flux [BandCount][]float32 `json:"flux"`
	// Envelope[i][f] is the smoothed band envelope: attack on rising
	// energy (fast, per-band coefficient), release on falling energy
	// (slower). This is the calibrated measurement envelope; display
	// gain is applied by the visualizer, never here.
	Envelope [BandCount][]float32 `json:"envelope"`
}

// ---------------------------------------------------------------------------
// Spectral descriptor layer (spectral.go)
// ---------------------------------------------------------------------------

// SpectralSeries adds per-hop spectral descriptors beyond the legacy
// Frame fields (centroid and rolloff already live on Frame).
//
// Memory: 7 * FrameCount * 4 bytes (≈20 MB for 2 h).
type SpectralSeries struct {
	// Bandwidth is the second spectral moment around the centroid (Hz).
	Bandwidth []float32 `json:"bandwidth"`
	// Contrast is the mean peak/valley dB difference over 6
	// log-spaced sub-bands (dB, 0..80).
	Contrast []float32 `json:"contrast"`
	// Flatness is the geometric/arithmetic mean ratio of the spectrum
	// (0..1; 1 = noise-like).
	Flatness []float32 `json:"flatness"`
	// Entropy is the normalized spectral entropy (0..1).
	Entropy []float32 `json:"entropy"`
	// ZCR is the windowed zero-crossing rate (0..1).
	ZCR []float32 `json:"zcr"`
	// Harmonicity is the fraction of window energy explained by the
	// detected harmonic template (0..1).
	Harmonicity []float32 `json:"harmonicity"`
	// Inharmonicity is a 0..1 indicator of non-harmonic spectral peaks
	// (detuned partials / metallic content).
	Inharmonicity []float32 `json:"inharmonicity"`
}

// ChromaPoint is one frame's 12-bin chroma (HPCP-style, harmonic
// weighted, tuning-corrected) plus tuning state. Stored at hop
// resolution but consumed beat-synchronously by the harmony layer.
//
// Memory: 12 * FrameCount * 4 bytes (≈34 MB for 2 h).
type ChromaPoint struct {
	// Chroma[i] is pitch-class energy for C, C#, D .. B (0..1, sum <= 1).
	Chroma [12]float32 `json:"chroma"`
	// TuningOffsetCents is the estimated global tuning offset
	// (-50..+50 cents) at this frame (carried for diagnostics; the
	// global estimate lives in Harmony.TuningOffsetCents).
	TuningOffsetCents float32 `json:"tuningOffsetCents,omitempty"`
}

// ---------------------------------------------------------------------------
// HPSS layer (hpss.go)
// ---------------------------------------------------------------------------

// HPSSSeries is the streaming median-filter harmonic/percussive
// decomposition evidence. It is NOT source separation: values are
// energy-share evidence (0..1 of the frame's total magnitude energy).
//
// Memory: 3 * FrameCount * 4 bytes (≈8.6 MB for 2 h).
type HPSSSeries struct {
	// Harmonic is the median-filtered (time axis) energy share.
	Harmonic []float32 `json:"harmonic"`
	// Percussive is the median-filtered (frequency axis) energy share.
	Percussive []float32 `json:"percussive"`
	// Residual is the energy not attributed to either mask (≈0..1;
	// harmonic+percussive+residual ≈ 1 within tolerance).
	Residual []float32 `json:"residual"`
}

// ---------------------------------------------------------------------------
// Multi-resolution layer (multires.go)
// ---------------------------------------------------------------------------

// Multi-resolution windows. The main STFT (FFTSize 2048 / hop 512)
// stays the time base; two extra resolutions serve documented purposes:
//
//   - Short: 512-sample window (~10.7 ms) / 128-sample hop (~2.7 ms)
//     resolves attack sharpness and transient micro-timing that the
//     2048 window smears.
//   - Long: 16384-sample window (~341 ms) / 2048-sample hop (~42.7 ms)
//     gives 2.93 Hz bin resolution for sub-bass and sustained harmonic
//     detail the 2048 window cannot separate.
const (
	ShortFFTSize    = 512
	ShortHopSize    = 128
	LongFFTSize     = 16384
	LongHopSize     = 2048
	MultiResMaxSecs = 7200 // bounded coverage cap (2 h), like maxFrames
)

// ShortSeries is the short-window transient layer.
//
// Memory: 3 * shortFrames * 4 bytes; shortFrames ≈ 4*FrameCount.
type ShortSeries struct {
	// HopSeconds is ShortHopSize/SampleRate (~2.67 ms).
	HopSeconds float64 `json:"hopSeconds"`
	// Time[f] is implicit (f*HopSeconds); Energy[f] is the windowed
	// RMS (0..1).
	Energy []float32 `json:"energy"`
	// Attack is the per-window positive energy slope (0..1, calibrated).
	Attack []float32 `json:"attack"`
	// Sharpness is transient sharpness: energy rise speed normalized
	// (0..1; high = click-like, low = slow swell).
	Sharpness []float32 `json:"sharpness"`
}

// LongSeries is the long-window low-frequency/harmonic layer.
//
// Memory: 3 * longFrames * 4 bytes; longFrames ≈ FrameCount/4.
type LongSeries struct {
	// HopSeconds is LongHopSize/SampleRate (~42.7 ms).
	HopSeconds float64 `json:"hopSeconds"`
	// SubEnergy is the 20..60 Hz energy share of the long window (0..1).
	SubEnergy []float32 `json:"subEnergy"`
	// BassBody is the 60..250 Hz share (0..1).
	BassBody []float32 `json:"bassBody"`
	// LowHarmonicity is harmonicity measured at 2.93 Hz resolution
	// (0..1), used to separate sustained bass notes from broadband
	// low rumble.
	LowHarmonicity []float32 `json:"lowHarmonicity"`
}

// MultiResData bundles both auxiliary resolutions.
type MultiResData struct {
	Short *ShortSeries `json:"short,omitempty"`
	Long  *LongSeries  `json:"long,omitempty"`
}

// ---------------------------------------------------------------------------
// Dynamics / loudness layer (loudness.go)
// ---------------------------------------------------------------------------

// LoudnessSummary holds ITU-R BS.1770-5 / EBU R128-style measurements.
// Standards are measurement references only; the visualizer never
// normalizes audio to any broadcast target.
type LoudnessSummary struct {
	// Integrated loudness (LUFS, gated per BS.1770-5).
	IntegratedLUFS float64 `json:"integratedLUFS"`
	// Loudness range (LU, EBU Tech 3342). 0 when unmeasurable.
	LoudnessRangeLU float64 `json:"loudnessRangeLU"`
	// SamplePeakDBFS is the max absolute sample level (dBFS).
	SamplePeakDBFS float64 `json:"samplePeakDBFS"`
	// TruePeakDBFS is the 4x-oversampled inter-sample peak (dBTP).
	TruePeakDBFS float64 `json:"truePeakDBFS"`
	// CrestFactorDB is SamplePeak - RMS full-scale difference (dB).
	CrestFactorDB float64 `json:"crestFactorDB"`
	// ClippingFrames is the count of 400 ms blocks with sample values
	// at or beyond ±0.999 of full scale for >2 consecutive samples.
	ClippingFrames int `json:"clippingFrames"`
	// ClippingRatio is ClippingFrames / total blocks (0..1).
	ClippingRatio float64 `json:"clippingRatio"`
	// MomentaryLUFS[f] and ShortTermLUFS[f] are per-block series
	// decimated to the hop grid (float32; -70 or below maps to -70).
	// Memory: 2 * FrameCount * 4 bytes.
	MomentaryLUFS []float32 `json:"momentary,omitempty"`
	ShortTermLUFS []float32 `json:"shortTerm,omitempty"`
	// SilenceIntervals are measured low-energy intervals
	// (short-term loudness below SilenceThresholdLUFS).
	SilenceIntervals []SilenceInterval `json:"silenceIntervals,omitempty"`
}

// SilenceThresholdLUFS is the measured silence gate for pauses.
const SilenceThresholdLUFS = -60.0

// SilenceInterval is one measured pause.
type SilenceInterval struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	// MinLUFS is the quietest short-term level inside the interval.
	MinLUFS float64 `json:"minLUFS"`
}

// ---------------------------------------------------------------------------
// Rhythm layer (rhythm.go)
// ---------------------------------------------------------------------------

// Rhythm is the time-aligned rhythm representation. Every estimate
// carries confidence; unreliable results are reported as unreliable.
type Rhythm struct {
	// Transients are distinct detected events (multi-band onset merge).
	// Do not equate every transient with a beat.
	Transients []TransientEvent `json:"transients,omitempty"`
	// Beats are the tracked beat timeline (sparse, discrete).
	Beats []BeatEvent `json:"beats,omitempty"`
	// BeatPhaseTimeline decimates beat phase to the hop grid for the
	// visualizer (0..1 within the current beat, or -1 when no usable
	// beat context). Memory: FrameCount * 4 bytes.
	BeatPhase []float32 `json:"beatPhase,omitempty"`
	// BPM is the global tempo candidate (legacy result stays on
	// Result.BPM; this carries the measured candidate set).
	BPM float64 `json:"bpm"`
	// TempoConfidence 0..1; below 0.35 unreliable (same gate as v2).
	TempoConfidence float64 `json:"tempoConfidence"`
	TempoReliable   bool    `json:"tempoReliable"`
	// Candidates are up to 3 tempo candidates (BPM + confidence),
	// including half/double-time alternatives when ambiguous.
	Candidates []TempoCandidate `json:"candidates,omitempty"`
	// HalfDoubleAmbiguous reports half-time/double-time ambiguity.
	HalfDoubleAmbiguous bool `json:"halfDoubleAmbiguous,omitempty"`
	// TempoSegments are local tempo intervals (rubs, ramps, changes).
	TempoSegments []TempoSegment `json:"tempoSegments,omitempty"`
	// DownbeatBeatIndex is the beat index within a bar that downbeats
	// map to (0-based), with meter candidates.
	DownbeatBeatIndex int `json:"downbeatBeatIndex,omitempty"`
	// Meters are meter candidates with confidence (e.g. 4/4, 3/4, 6/8).
	Meters []MeterCandidate `json:"meters,omitempty"`
	// Downbeats are the tracked downbeat events (discrete).
	Downbeats []BeatEvent `json:"downbeats,omitempty"`
	// Bars are bar boundary times (downbeat-derived; sparse).
	BarTimes []float64 `json:"barTimes,omitempty"`
	// PhraseBoundaries are candidate phrase boundaries (grouped bars).
	PhraseBoundaries []float64 `json:"phraseBoundaries,omitempty"`
	// Syncopation is the measured off-beat energy share (0..1).
	Syncopation float64 `json:"syncopation"`
	// Density is transients per second averaged over the track.
	Density float64 `json:"density"`
	// AccentPattern is the normalized beat-position energy profile of
	// one bar (length = meter beats; confidence-gated).
	AccentPattern    []float32 `json:"accentPattern,omitempty"`
	AccentConfidence float64   `json:"accentConfidence,omitempty"`
	// Motifs are repeated rhythmic motifs where measurable.
	Motifs []RhythmicMotif `json:"motifs,omitempty"`
}

// TransientEvent is one detected transient with its strongest band
// evidence. Band is a BandNames index or -1 when broadband.
type TransientEvent struct {
	Time       float64 `json:"time"`
	Strength   float64 `json:"strength"` // 0..1 calibrated
	Band       int     `json:"band"`     // -1 = broadband
	Percussive float64 `json:"percussive"`
}

// BeatEvent is one discrete beat/downbeat.
type BeatEvent struct {
	Time     float64 `json:"time"`
	Strength float64 `json:"strength"` // 0..1 local evidence
	Index    int     `json:"index"`    // global beat index
	Bar      int     `json:"bar,omitempty"`
}

// TempoCandidate is one tempo hypothesis.
type TempoCandidate struct {
	BPM        float64 `json:"bpm"`
	Confidence float64 `json:"confidence"`
	// Relation explains the candidate: "primary", "half", "double".
	Relation string `json:"relation"`
}

// TempoSegment is a local tempo interval.
type TempoSegment struct {
	Start      float64 `json:"start"`
	End        float64 `json:"end"`
	BPM        float64 `json:"bpm"`
	Confidence float64 `json:"confidence"`
}

// MeterCandidate is one meter hypothesis.
type MeterCandidate struct {
	// Name is "4/4", "3/4", "6/8" etc.
	Name       string  `json:"name"`
	Beats      int     `json:"beats"`
	Confidence float64 `json:"confidence"`
}

// RhythmicMotif is a repeated rhythm pattern where measurable.
type RhythmicMotif struct {
	Start       float64   `json:"start"`
	End         float64   `json:"end"`
	Occurrences []float64 `json:"occurrences"`
	Confidence  float64   `json:"confidence"`
}

// ---------------------------------------------------------------------------
// Harmony layer (harmony.go)
// ---------------------------------------------------------------------------

// Harmony holds tuning, key, chord and progression estimates.
type Harmony struct {
	// TuningOffsetCents is the global tuning estimate (-50..+50).
	TuningOffsetCents float64 `json:"tuningOffsetCents"`
	// TuningConfidence 0..1.
	TuningConfidence float64 `json:"tuningConfidence"`
	// Key is the best key candidate (e.g. "F# minor") or "" when no
	// candidate clears the confidence floor.
	Key           string  `json:"key,omitempty"`
	KeyConfidence float64 `json:"keyConfidence"`
	// KeyCandidates lists major/minor alternatives.
	KeyCandidates []KeyCandidate `json:"keyCandidates,omitempty"`
	// Chords are chord candidates (sparse; only stable segments).
	Chords []ChordEvent `json:"chords,omitempty"`
	// ChordChanges are chord-change timestamps (Chords boundaries).
	ChordChanges []float64 `json:"chordChanges,omitempty"`
	// Progressions are multi-chord segments (e.g. i-VI-III-VII).
	Progressions []ProgressionSegment `json:"progressions,omitempty"`
	// Tension is the per-hop harmonic instability indicator decimated
	// to the hop grid (0..1). Memory: FrameCount * 4 bytes.
	Tension []float32 `json:"tension,omitempty"`
	// Chroma is the per-hop chroma timeline (see ChromaPoint).
	Chroma []ChromaPoint `json:"chroma,omitempty"`
}

// KeyCandidate is one key hypothesis.
type KeyCandidate struct {
	Key        string  `json:"key"` // e.g. "A major"
	Confidence float64 `json:"confidence"`
}

// ChordEvent is one chord segment with confidence.
type ChordEvent struct {
	Start      float64 `json:"start"`
	End        float64 `json:"end"`
	Root       string  `json:"root"`    // "C", "C#", .. ("N" = no chord)
	Quality    string  `json:"quality"` // "maj","min","7","m7","dim","aug"
	Confidence float64 `json:"confidence"`
}

// ProgressionSegment is a repeated or notable chord sequence.
type ProgressionSegment struct {
	Start      float64  `json:"start"`
	End        float64  `json:"end"`
	Chords     []string `json:"chords"` // e.g. ["A min","F maj"]
	Confidence float64  `json:"confidence"`
}

// ---------------------------------------------------------------------------
// Vocal-flow layer (vocalflow.go)
// ---------------------------------------------------------------------------

// VocalFlow is the vocal-like activity and phrase/flow estimate.
// These are acoustic estimates, NOT lyric or syllable transcription.
type VocalFlow struct {
	// Likelihood is the per-hop vocal-like probability decimated to the
	// hop grid (0..1). Memory: FrameCount * 4 bytes.
	Likelihood []float32 `json:"likelihood,omitempty"`
	// Phrases are detected vocal-like phrase intervals.
	Phrases []VocalPhrase `json:"phrases,omitempty"`
	// Transitions are flow changes between adjacent phrases.
	Transitions []FlowTransition `json:"flowTransitions,omitempty"`
	// SyllableDensity is the per-hop syllable-like event density
	// decimated to the hop grid (events/s, 0..12). Memory: FrameCount*4.
	SyllableDensity []float32 `json:"syllableDensity,omitempty"`
	// OverallConfidence is the mean interval confidence (0 when no
	// vocal-like material was detected).
	OverallConfidence float64 `json:"overallConfidence"`
}

// VocalPhrase is one vocal-like phrase with measured delivery stats.
type VocalPhrase struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	// SyllableRate is syllable-like events per second.
	SyllableRate float64 `json:"syllableRate"`
	// PauseAfter is the pause length following the phrase (s).
	PauseAfter float64 `json:"pauseAfter"`
	// AccentScore measures accent placement regularity vs the beat
	// grid (0..1).
	AccentScore float64 `json:"accentScore"`
	// Syncopation is the off-beat share of syllable-like events (0..1).
	Syncopation float64 `json:"syncopation"`
	// Confidence of the interval being vocal-like (0..1).
	Confidence float64 `json:"confidence"`
}

// FlowTransition describes a delivery change between adjacent phrases.
type FlowTransition struct {
	At float64 `json:"at"`
	// Kind is "fast-to-slow", "slow-to-fast", "denser", "sparser".
	Kind string `json:"kind"`
	// Magnitude is the delivery-rate change ratio (|new/old - 1|).
	Magnitude  float64 `json:"magnitude"`
	Confidence float64 `json:"confidence"`
}

// ---------------------------------------------------------------------------
// Instrument-role layer (roles.go)
// ---------------------------------------------------------------------------

// Role IDs (exported so the visualizer and config can reference them).
const (
	RoleKick    = "kick-like"
	RoleBass    = "bass-like"
	RoleSnare   = "snare-like"
	RoleHiHat   = "hihat-like"
	RoleCymbal  = "cymbal-like"
	RolePerc    = "percussion-like"
	RoleKeys    = "keys-like"
	RoleStrings = "strings-like"
	RolePad     = "pad-like"
	RoleVocal   = "vocal-like"
)

// RoleNote is the honest classification disclaimer.
const RoleNote = "heuristic acoustic role estimates, not source separation"

// RoleInterval is one role activity interval with its evidence.
type RoleInterval struct {
	Role       string  `json:"role"`
	Start      float64 `json:"start"`
	End        float64 `json:"end"`
	Confidence float64 `json:"confidence"`
	// Evidence summarizes the acoustic basis (ASCII English), e.g.
	// "low transient + short attack + percussive hpss".
	Evidence string `json:"evidence"`
}

// ---------------------------------------------------------------------------
// Structure layer (structure.go)
// ---------------------------------------------------------------------------

// Section labels (neutral fallbacks: "transition", "unknown").
const (
	SectionIntro      = "intro"
	SectionBuild      = "build-up"
	SectionDrop       = "drop"
	SectionHigh       = "high-energy"
	SectionLow        = "low-energy"
	SectionBreak      = "break"
	SectionTransition = "transition"
	SectionOutro      = "outro"
	SectionRepeat     = "repeat"
	SectionUnknown    = "unknown"
)

// SectionEvent is one candidate structural section.
type SectionEvent struct {
	Start      float64 `json:"start"`
	End        float64 `json:"end"`
	Label      string  `json:"label"`
	Confidence float64 `json:"confidence"`
}

// ---------------------------------------------------------------------------
// Helpers shared by all layers
// ---------------------------------------------------------------------------

// clampUnit clamps x to 0..1 (NaN-safe).
func clampUnit(x float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return 0
	}
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}

// clampLUFSCeil bounds a loudness value to the -70 LUFS measurement
// floor used by the stored series.
func clampLUFSCeil(lufs float64) float64 {
	if math.IsNaN(lufs) || math.IsInf(lufs, 0) {
		return -70
	}
	if lufs < -70 {
		return -70
	}
	return lufs
}

// sanitizeFloat32 returns 0 for NaN/Inf, otherwise the value clamped to
// [min, max] when the range is valid.
func sanitizeFloat32(v float64, min, max float64) float32 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return float32(min)
	}
	if v < min {
		return float32(min)
	}
	if v > max {
		return float32(max)
	}
	return float32(v)
}
