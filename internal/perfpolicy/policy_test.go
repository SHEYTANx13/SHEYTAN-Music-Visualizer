package perfpolicy

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/apppaths"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/sysprofile"
)

func TestPolicyStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := OpenPolicyStore(filepath.Join(dir, DefaultFilename))
	m, found, err := s.Load()
	if err != nil || found {
		t.Fatalf("first load: found=%v err=%v", found, err)
	}
	if m != sysprofile.ModeAuto {
		t.Fatalf("default mode = %q, want auto", m)
	}
	if err := s.Save(sysprofile.ModeHighPerformance); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, found, err := s.Load()
	if err != nil || !found {
		t.Fatalf("reload: found=%v err=%v", found, err)
	}
	if got != sysprofile.ModeHighPerformance {
		t.Fatalf("round trip mode = %q", got)
	}
}

func TestPolicyStoreRejectsInvalidMode(t *testing.T) {
	s := OpenPolicyStore(filepath.Join(t.TempDir(), "system-policy.json"))
	if err := s.Save(sysprofile.PerformanceMode("bogus")); err == nil {
		t.Fatal("invalid mode accepted")
	}
}

func TestPolicyStoreCorruptIsError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "system-policy.json")
	if err := os.WriteFile(p, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := OpenPolicyStore(p)
	_, _, err := s.Load()
	if err == nil {
		t.Fatal("corrupt policy document must be an error, never a silent reset")
	}
}

func TestStorePathUsesConfig(t *testing.T) {
	lay := apppaths.Layout{}
	// StorePath only joins Config with the canonical file name.
	lay.Config = filepath.Join("root", "config")
	if got := StorePath(lay); got != filepath.Join("root", "config", DefaultFilename) {
		t.Fatalf("StorePath = %q", got)
	}
}
