# Analysis v3 — measured music analysis (priority-C/D implementation)

## What the analyzer measures

One streaming pass over the canonical 48 kHz stereo PCM
(`dsp.AnalyzeFullReader`) produces the versioned `dsp.Analysis`
document (schema v3) containing:

- **Stereo (never averaged away)**: per-hop L/R/M/S energies, measured
  width, windowed L/R correlation, anti-phase detection. Anti-phase
  material is reported as a warning and is never interpreted as
  silence; the mono path uses the mid channel.
- **Nine measured frequency bands** (20-60-120-250-500-1k-2k-4k-8k-16k
  Hz): energy share, relative positive flux, calibrated attack/release
  envelopes. Nyquist at 48 kHz is 24 kHz; the 16-24 kHz octave folds
  into the brilliance band's spectral descriptors.
- **Spectral descriptors**: centroid, bandwidth, rolloff (legacy), plus
  bandwidth, contrast (6 ranges), flatness, entropy, ZCR, harmonicity,
  inharmonicity; HPCP-style 12-bin chroma with tuning-offset estimate.
- **Multi-resolution**: short window 512/128 (attack sharpness) and
  long window 16384/2048 (2.93 Hz sub-bass resolution), same
  window-start time contract, bounded memory, scratch reuse.
- **HPSS**: streaming median-filter harmonic/percussive evidence with
  energy accounting (H+P+R ≈ 1). Evidence, not source separation.
- **Rhythm**: multi-band onsets with an absolute attack gate (recent-
  trough rise), transient events with amplitude-proportional strength,
  beat/downbeat grids with meter candidates (4/4, 3/4, 6/8), tempo
  candidates including half/double-time ambiguity, local tempo
  segments, syncopation, density, accent patterns, repeated motifs.
  Confidence below 0.35 means NO beat grid is emitted (unknown beats
  are never fabricated).
- **Harmony**: tuning offset, Krumhansl-Schmuckler key candidates with
  a 0.6 confidence floor (no key = no label), chord templates with
  beat-synchronous segmentation, progressions, per-hop tension.
- **Vocal-flow**: vocal-like activity from an ABSOLUTE power-scaled
  syllabic carrier (a share-based carrier is AM-invariant), phrase
  segmentation, syllable-like event density, flow transitions,
  modulation-scaled confidence. Acoustic estimates only - never lyric
  or syllable transcription.
- **Instrument roles**: heuristic intervals (kick/bass/snare/hihat/
  cymbal/percussion/keys/strings/pad/vocal-like) with evidence text
  and confidence. Heuristic role estimates, not stems.
- **Structure**: checkerboard + Euclidean novelty, direct measured
  cues (silence runs, RMS steps), labeled sections with neutral
  fallbacks (unknown/transition) and confidence.
- **Dynamics**: ITU-R BS.1770-5 K-weighted momentary/short-term/
  integrated loudness, EBU Tech 3342 loudness range, sample peak,
  4x-oversampled true peak, crest factor, clipping detection, measured
  silence intervals. Measurement only - the visualizer never
  normalizes audio to any broadcast target.

## Composition integration (priority-D)

`visualizer.Composition` is the common time base: preview and export
read the SAME measured state through `AudioState` (composition fields +
confidence), so the two can never disagree. Continuous series
interpolate on the hop grid; transients, chords, sections and beats
are discrete lookups that are never smeared. The scene drives a
vocal-reactive layer, section-transition pulse, measured stereo spread
and discrete kick/snare/hat class decays; chord changes rotate the
palette inside the SHEYTAN black/red identity. Every layer is gated by
the configured confidence threshold: below it the feature has NO
effect, never a degraded guess.

User knobs (grouped, never raw DSP): bass impact, transient intensity,
vocal sensitivity + layer toggle, stereo width, section intensity,
beat emphasis, chord reactivity, confidence threshold, role layers.

## Cache and versions

`dsp.AnalysisVersion` = 3 and `AnalysisSchemaVersion` = 3. The analysis
sidecar (`<fingerprint>.analysis.json`) is reused only when BOTH the
schema version and the fingerprint match; any mismatch is deleted and
regenerated - cached data is never reinterpreted with new semantics.
`songdna.Version` = 2 (derives from the measured layers).

## Performance

Streaming analysis with reused scratch buffers, cancellation support
and chunk-size independence (verified byte-identical across 10 ms /
1 s / whole-track chunkings). Measured on a 2-vCPU sandbox:
~7.9 s of analysis per minute of audio, linear in duration (5 min ≈
40 s, 30 min ≈ 238 s); retained memory is a documented linear function
of duration (float32 series + sparse events, ~115 MB worst case for a
2 h track, ≈3 MB for 5 min).
