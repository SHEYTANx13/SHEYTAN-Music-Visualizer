package songdna

import (
	"testing"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/dsp"
)

func fakeAnalysis(bass, centroid, onsetCount, conf float64) *dsp.Result {
	return &dsp.Result{
		MeanRMS:         0.2,
		BassEnergy:      bass,
		MidEnergy:       0.3,
		TrebleEnergy:    0.1,
		MeanCentroid:    centroid,
		OnsetCount:      int(onsetCount),
		BPM:             120,
		TempoConfidence: conf,
		TempoReliable:   conf >= 0.35,
	}
}

func TestDNADeterministic(t *testing.T) {
	fp := "abc123"
	d1 := Derive(fp, fakeAnalysis(0.6, 1500, 200, 0.8), 0)
	d2 := Derive(fp, fakeAnalysis(0.6, 1500, 200, 0.8), 0)
	if *d1 != *d2 {
		t.Fatal("same input produced different DNA")
	}
}

func TestDNASeedChangesArtisticChoices(t *testing.T) {
	fp := "abc123"
	d1 := Derive(fp, fakeAnalysis(0.5, 1200, 100, 0.5), 0)
	d2 := Derive(fp, fakeAnalysis(0.5, 1200, 100, 0.5), 42)
	if d1.Seed == d2.Seed {
		t.Fatal("user seed must change the effective seed")
	}
	if d1.UserSeed != 0 || d2.UserSeed != 42 {
		t.Fatal("user seed not recorded")
	}
}

func TestDNAMeasuredFeaturesHonest(t *testing.T) {
	bright := Derive("fp1", fakeAnalysis(0.1, 5000, 50, 0.2), 0)
	warm := Derive("fp2", fakeAnalysis(0.9, 300, 50, 0.2), 0)
	if bright.Brightness <= warm.Brightness {
		t.Fatal("brightness must track centroid")
	}
	if warm.Warmth <= bright.Warmth {
		t.Fatal("warmth must track bass/treble balance")
	}
	if bright.Descriptor == "" {
		t.Fatal("descriptor must describe measured features")
	}
}

func TestDNAVersion(t *testing.T) {
	d := Derive("fp", fakeAnalysis(0.5, 1000, 10, 0.1), 0)
	if d.Version != Version {
		t.Fatal("version mismatch")
	}
}
