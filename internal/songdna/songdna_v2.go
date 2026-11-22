package songdna

// songdna_v2.go — v2 derivation (priority-D): the DNA now consumes the
// v3 measured analysis document, so the artistic defaults come from the
// real stereo/band/rhythm/harmony/vocal/section measurements instead of
// the legacy coarse fields alone. Version bumped to 2; projects store
// the version and invalidate on mismatch (§6.11).
//
// The derivation stays deterministic: content-derived seed, no
// randomness beyond the seeded PRNG the v1 path already uses.

import (
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/dsp"
)

// DeriveFromAnalysis computes the v2 DNA from the full analysis
// document. full may be nil (pre-v3 result) — the call then falls back
// to the v1 derivation over the legacy result, with the version still
// marked 2 (the stored defaults remain valid; only the richer evidence
// is missing).
func DeriveFromAnalysis(fingerprint string, full *dsp.Analysis, userSeed uint64) *DNA {
	if full == nil || full.Legacy == nil {
		return Derive(fingerprint, nil, userSeed)
	}
	d := Derive(fingerprint, full.Legacy, userSeed)
	d.Version = Version

	// Punchiness from measured kick-class transients (density of
	// low-band transients), not just the legacy bass field.
	if full.Rhythm != nil && full.Rhythm.TempoReliable {
		kicks := 0
		for _, tr := range full.Rhythm.Transients {
			if tr.Band == 0 || tr.Band == 1 {
				kicks++
			}
		}
		perMin := float64(kicks) / (full.Duration / 60)
		punch := perMin / 480 // 480 kick hits/min ≈ dense electronic
		if punch > 1 {
			punch = 1
		}
		d.Punchiness = 0.5*d.Punchiness + 0.5*punch
	}

	// Warmth from the measured low-band vs presence-band balance.
	if full.Bands != nil && len(full.Bands.Energy[0]) > 0 {
		var low, pres int
		for f := 0; f < len(full.Bands.Energy[0]); f++ {
			l := float64(full.Bands.Energy[0][f]) + float64(full.Bands.Energy[1][f]) + float64(full.Bands.Energy[2][f])
			p := float64(full.Bands.Energy[5][f]) + float64(full.Bands.Energy[6][f]) + float64(full.Bands.Energy[7][f])
			low += int(l * 1000)
			pres += int(p * 1000)
		}
		total := low + pres
		if total > 0 {
			warm := float64(low) / float64(total)
			d.Warmth = clamp01v(0.5*d.Warmth + 0.5*warm)
		}
	}

	// Vocal material biases the default mode toward bloom (its vocal
	// layer is most visible there) and raises glow slightly.
	if full.Vocal != nil && full.Vocal.OverallConfidence >= 0.5 && len(full.Vocal.Phrases) > 0 {
		d.DefaultMode = "bloom"
		d.GlowIntensity = clampRangev(d.GlowIntensity+0.15, 0.5, 1.4)
	}

	// Measured loudness (BS.1770-5 integrated) maps to the loudness
	// feature: -23 LUFS (quiet) → 0, -8 LUFS (loud) → 1.
	if full.Dynamics != nil && full.Dynamics.IntegratedLUFS > -69 {
		lufs := full.Dynamics.IntegratedLUFS
		l := (-lufs - 8) / 15 // -8 → 0, -23 → 1
		if l < 0 {
			l = 0
		}
		if l > 1 {
			l = 1
		}
		d.Loudness = clamp01v(1 - l)
	}

	// Descriptor: honest measured-feature summary (ASCII English).
	d.Descriptor = describeV2(full)
	return d
}

func clamp01v(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func clampRangev(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// describeV2 summarizes the measured layers (ASCII English; honest
// about uncertainty).
func describeV2(full *dsp.Analysis) string {
	s := "measured"
	if full.Rhythm != nil && full.Rhythm.TempoReliable {
		s += ", reliable tempo"
	} else {
		s += ", tempo uncertain"
	}
	if full.Harmony != nil && full.Harmony.Key != "" {
		s += ", " + full.Harmony.Key
	} else {
		s += ", key uncertain"
	}
	if full.Vocal != nil && len(full.Vocal.Phrases) > 0 && full.Vocal.OverallConfidence >= 0.5 {
		s += ", vocal-like material"
	}
	if full.Stereo != nil && full.Stereo.AntiPhaseRatio() >= 0.2 {
		s += ", anti-phase stereo"
	}
	if full.Structure != nil && len(full.Structure) > 0 {
		s += ", structured sections"
	}
	return s
}
