package dsp

// roles_test.go — §6.8/§8.2 property tests for the heuristic
// instrument-role layer (RoleNote: estimates, not source separation).

import (
	"strings"
	"testing"
)

// layerInputs runs a full analysis and returns the measured series the
// musical layers consume.
func layerInputs(t *testing.T, tr StereoTrack) (*Result, *BandTimeline, *HPSSSeries, *SpectralSeries) {
	t.Helper()
	fa := newFullAnalyzer()
	fa.Process(tr.Interleaved())
	a := fa.Finish("", "")
	return a.Legacy, a.Bands, a.HPSS, a.Spectral
}

func TestRolesKickDetected(t *testing.T) {
	t.Parallel()
	tr := DropStructure(5)
	_, bands, hp, sp := layerInputs(t, tr)
	frames, _, _ := analyzeFrames(t, tr)
	rhythm := DetectRhythm(frames.Frames, bands, hopSec)
	roles := EstimateRoles(bands, hp, rhythm, sp, hopSec, frames.Frames)
	kick := false
	for _, r := range roles {
		if r.Role == RoleKick && r.Start < 10.5 && r.End > 5.5 && r.Confidence >= 0.3 {
			kick = true
		}
	}
	if !kick {
		t.Fatalf("no kick-like interval over the drop window: %+v", roles)
	}
}

func TestRolesPadVsKickSeparation(t *testing.T) {
	t.Parallel()
	tr := newTrack(nSamples(9))
	PadLike(&tr, 0.5, 4, 0.4)
	for k := 0; k < 14; k++ {
		KickLike(&tr, 5+float64(k)*0.25, 0.85)
	}
	_, bands, hp, sp := layerInputs(t, tr)
	frames, _, _ := analyzeFrames(t, tr)
	rhythm := DetectRhythm(frames.Frames, bands, hopSec)
	roles := EstimateRoles(bands, hp, rhythm, sp, hopSec, frames.Frames)
	var padMid, kickWin bool
	for _, r := range roles {
		switch r.Role {
		case RolePad:
			if r.Start < 3.5 && r.End > 1 {
				padMid = true
			}
		case RoleKick:
			if r.Start < 8.5 && r.End > 5.5 {
				kickWin = true
			}
		}
	}
	if !padMid || !kickWin {
		t.Fatalf("pad/kick separation incomplete: padMid=%v kickWin=%v roles=%+v", padMid, kickWin, roles)
	}
}

func TestRolesHatDetected(t *testing.T) {
	t.Parallel()
	tr := newTrack(nSamples(5))
	for k := 0; k < 18; k++ {
		HatLike(&tr, 0.3+float64(k)*0.25, 0.5)
	}
	_, bands, hp, sp := layerInputs(t, tr)
	frames, _, _ := analyzeFrames(t, tr)
	rhythm := DetectRhythm(frames.Frames, bands, hopSec)
	roles := EstimateRoles(bands, hp, rhythm, sp, hopSec, frames.Frames)
	hat := false
	for _, r := range roles {
		if r.Role == RoleHiHat && r.Confidence >= 0.25 {
			hat = true
		}
	}
	if !hat {
		t.Fatalf("no hihat-like intervals on a hats-only track: %+v", roles)
	}
}

func TestRolesEvidenceFields(t *testing.T) {
	t.Parallel()
	tr := DropStructure(2)
	_, bands, hp, sp := layerInputs(t, tr)
	frames, _, _ := analyzeFrames(t, tr)
	rhythm := DetectRhythm(frames.Frames, bands, hopSec)
	roles := EstimateRoles(bands, hp, rhythm, sp, hopSec, frames.Frames)
	valid := map[string]bool{
		RoleKick: true, RoleBass: true, RoleSnare: true, RoleHiHat: true,
		RoleCymbal: true, RolePerc: true, RoleKeys: true, RoleStrings: true,
		RolePad: true, RoleVocal: true,
	}
	for _, r := range roles {
		if !valid[r.Role] {
			t.Fatalf("unknown role %q", r.Role)
		}
		if r.End <= r.Start {
			t.Fatalf("degenerate interval: %+v", r)
		}
		if r.Confidence <= 0 || r.Confidence > 1 {
			t.Fatalf("confidence out of range: %+v", r)
		}
		if r.Evidence == "" {
			t.Fatalf("empty evidence for %+v", r)
		}
	}
}

func TestRolesEmptyOnSilence(t *testing.T) {
	t.Parallel()
	tr, _ := CorpusFixture("silence", 4, 1)
	_, bands, hp, sp := layerInputs(t, tr)
	frames, _, _ := analyzeFrames(t, tr)
	rhythm := DetectRhythm(frames.Frames, bands, hopSec)
	roles := EstimateRoles(bands, hp, rhythm, sp, hopSec, frames.Frames)
	if len(roles) != 0 {
		t.Fatalf("silence must produce no role intervals, got %+v", roles)
	}
}

func TestRolesDeterministicAndCapped(t *testing.T) {
	t.Parallel()
	tr := DropStructure(8)
	_, bands, hp, sp := layerInputs(t, tr)
	frames, _, _ := analyzeFrames(t, tr)
	rhythm := DetectRhythm(frames.Frames, bands, hopSec)
	a := EstimateRoles(bands, hp, rhythm, sp, hopSec, frames.Frames)
	b := EstimateRoles(bands, hp, rhythm, sp, hopSec, frames.Frames)
	if len(a) != len(b) {
		t.Fatal("roles not deterministic (count)")
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatal("roles not deterministic (values)")
		}
		if !strings.Contains(a[i].Evidence, " ") && len(a[i].Evidence) < 3 {
			t.Fatalf("evidence too terse: %q", a[i].Evidence)
		}
	}
}
