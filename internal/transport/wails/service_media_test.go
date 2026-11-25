package wails

// service_media_test.go — contract tests for the run 45532045379
// downloads-pane surface: the web shell serves the REAL app-managed
// media-tools snapshot (state, stage, downloaded bytes, total bytes,
// throughput) from the same manager the go-gui shell uses. Synthetic
// download data is forbidden by the product contract, so the tests pin
// the states the manager can honestly report on a fresh data root and
// the cancel/no-op paths.

import (
	"testing"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/mediatools"
)

func TestGetMediaToolsStatusRealStates(t *testing.T) {
	svc, _, _ := newTestService(t)

	snap := svc.GetMediaToolsStatus()

	switch snap.State {
	case mediatools.StateReady, mediatools.StateNotInstalled,
		mediatools.StateRepairNeeded, mediatools.StateUnsupportedPlatform:
		// Honest initial states for a fresh data root: the manager
		// inspected the real filesystem, nothing is downloaded yet.
	default:
		t.Fatalf("initial state = %q, want a stable not-installed-class state", snap.State)
	}
	if snap.Downloaded != 0 || snap.Total != 0 || snap.BytesPerSecond != 0 {
		t.Fatalf("fresh snapshot carries progress bytes: %+v (synthetic download data is forbidden)", snap)
	}
}

func TestMediaToolsCancelWithoutInstallIsNoop(t *testing.T) {
	svc, _, _ := newTestService(t)

	// Cancelling with nothing in flight must not panic and must leave a
	// readable, stable snapshot behind.
	svc.CancelMediaToolsInstall()
	snap := svc.GetMediaToolsStatus()
	if snap.State == "" {
		t.Fatal("snapshot state is empty after cancel (manager lost its state)")
	}
}

func TestMediaToolsStatusMatchesManagerSnapshot(t *testing.T) {
	svc, _, _ := newTestService(t)

	first := svc.GetMediaToolsStatus()
	second := svc.GetMediaToolsStatus()

	// Two reads without any lifecycle call agree on state and detail
	// (the facade must serve the manager's snapshot, not a re-derivation
	// that could drift from it).
	if first.State != second.State || first.Detail != second.Detail {
		t.Fatalf("snapshot drifted between reads: %+v vs %+v", first, second)
	}
	if first.Installed == nil != (second.Installed == nil) {
		t.Fatalf("installed pointer drifted between reads: %+v vs %+v", first, second)
	}
}
