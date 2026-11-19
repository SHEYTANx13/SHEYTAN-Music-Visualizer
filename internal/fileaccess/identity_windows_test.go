//go:build windows

package fileaccess

import (
	"os"
	"testing"
)

// TestWindowsIdentityCarriesCreationTime pins the run 44330231458
// Windows go-test repair: the canonical identity primitive must carry
// the handle's CreationTime, because a same-file-id replacement
// (volume serial + MFT record reuse) is only distinguishable through
// the per-instance creation timestamp when the recreated directory's
// modification time coincides with the recorded one. Without the
// birth-time component, TestSameInodeReplacementRequiresReconfirmation
// phase 1 accepted the stale confirmation on Windows ("same-(dev,ino)
// replacement accepted").
func TestWindowsIdentityCarriesCreationTime(t *testing.T) {
	dir := t.TempDir()
	id, err := identityFromPath(dir)
	if err != nil {
		t.Fatalf("identityFromPath: %v", err)
	}
	if id.Dev == 0 || id.Ino == 0 {
		t.Fatalf("volume/file identity missing: dev=%d ino=%d", id.Dev, id.Ino)
	}
	if id.BtimeSec == 0 && id.BtimeNsec == 0 {
		t.Fatal("identity primitive carries no creation time: a same-file-id replacement cannot be rejected deterministically")
	}
	id2, err := identityFromPath(dir)
	if err != nil {
		t.Fatalf("identityFromPath (second stat): %v", err)
	}
	if !sameRootIdentity(id, id2) {
		t.Fatalf("identity of the same directory instance is not stable: %+v vs %+v", id, id2)
	}
}

// TestWindowsRecreatedDirectoryRejected exercises the real Windows
// filesystem primitive end to end: deleting and recreating the granted
// root must produce an identity the comparison rejects, whether the
// volume reused the file id (rejected through the creation-time
// discriminator) or allocated a fresh one (rejected through dev+ino).
func TestWindowsRecreatedDirectoryRejected(t *testing.T) {
	dir := t.TempDir()
	granted, err := identityFromPath(dir)
	if err != nil {
		t.Fatalf("identityFromPath (granted): %v", err)
	}
	if err := os.Remove(dir); err != nil {
		t.Fatalf("remove granted root: %v", err)
	}
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("recreate root: %v", err)
	}
	recreated, err := identityFromPath(dir)
	if err != nil {
		t.Fatalf("identityFromPath (recreated): %v", err)
	}
	if sameRootIdentity(granted, recreated) {
		t.Fatalf("recreated directory accepted as the same instance: granted=%+v recreated=%+v", granted, recreated)
	}
}
