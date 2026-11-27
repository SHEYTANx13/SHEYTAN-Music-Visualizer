package wails

// service_system_test.go — run-45678113841 fixtures for the system
// profile and secure-network facade surface.
//
// Contract pins:
//   - the system profile is REAL collected data (non-zero audit stamp,
//     collector identity, renderer evidence carried verbatim) with a
//     TTL cache that replays the SAME snapshot;
//   - the network policy snapshot is the immutable ghreq origin policy
//     (HTTPS-only, allowlisted hosts, no credentials, digest-verified
//     downloads) and the probe is opt-in only;
//   - ProbeNetwork records the honest probe outcome (reachable or the
//     exact error class) and never fabricates an online state;
//   - no test touches the real network (probeFn is stubbed).

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/ghreq"
)

func newSystemTestService(t *testing.T) *SheytanService {
	t.Helper()
	svc, _, _ := newTestService(t)
	return svc
}

func TestSystemProfileRealSnapshotAndTTLCache(t *testing.T) {
	svc := newSystemTestService(t)
	p1 := svc.GetSystemProfile()
	if p1.CollectedAt.IsZero() {
		t.Fatal("profile must carry a real collection timestamp")
	}
	if p1.Collector == "" {
		t.Fatal("profile must name its collector (audit stamp)")
	}
	if p1.OS.Family.Value == "" && p1.OS.Arch.Value == "" && p1.CPU.LogicalCPUs.State == "" {
		t.Fatalf("profile must contain real OS/CPU facts, got %+v", p1.OS)
	}
	// Renderer evidence must match the facade's cached evidence verbatim.
	rd := svc.GetRendererEvidence()
	if string(p1.Render.Active.Backend) != rd.Backend {
		t.Fatalf("profile renderer %q disagrees with shell evidence %q", p1.Render.Active.Backend, rd.Backend)
	}
	// Within the TTL the snapshot is replayed byte-identical.
	p2 := svc.GetSystemProfile()
	if !p2.CollectedAt.Equal(p1.CollectedAt) {
		t.Fatal("TTL cache must replay the same snapshot, not re-collect")
	}
}

func TestNetworkPolicyImmutableContract(t *testing.T) {
	svc := newSystemTestService(t)
	pol := svc.GetNetworkPolicy()
	if !pol.HTTPSOnly {
		t.Fatal("the origin policy is HTTPS-only by contract")
	}
	if pol.CredentialsSent {
		t.Fatal("the application never sends credentials on network operations")
	}
	if !pol.DigestVerified || !pol.SizeBoundsApply {
		t.Fatal("downloads are digest-verified and size-bounded by contract")
	}
	if pol.AutoProbeEnabled {
		t.Fatal("auto-probing must stay disabled: the probe is user-triggered only")
	}
	if len(pol.AllowedHosts) == 0 {
		t.Fatal("the allowlist must be served from the ghreq authority")
	}
	for _, host := range pol.AllowedHosts {
		if host == "" {
			t.Fatal("empty host in the served allowlist")
		}
	}
}

func TestNetworkStatusNeverProbedByDefault(t *testing.T) {
	svc := newSystemTestService(t)
	status := svc.GetNetworkStatus()
	if status.ProbeStatus != "never-probed" {
		t.Fatalf("a fresh facade must report never-probed, got %q", status.ProbeStatus)
	}
	if status.LastProbe != nil {
		t.Fatal("no probe has run: the status must not invent a probe result")
	}
}

func TestProbeNetworkRecordsHonestOutcome(t *testing.T) {
	prev := probeFn
	t.Cleanup(func() { probeFn = prev })

	t.Run("reachable", func(t *testing.T) {
		probeFn = func(ctx context.Context, rawURL string, timeout time.Duration) ghreq.ProbeResult {
			if rawURL != ghreq.ProbeOrigin {
				t.Errorf("probe must target the pinned origin %q, got %q", ghreq.ProbeOrigin, rawURL)
			}
			if timeout != networkProbeTimeout {
				t.Errorf("probe must be bounded by networkProbeTimeout, got %v", timeout)
			}
			return ghreq.ProbeResult{Reachable: true, HTTPStatus: 200, LatencyMs: 42, CheckedAt: time.Now().UTC()}
		}
		svc := newSystemTestService(t)
		status := svc.ProbeNetwork()
		if status.ProbeStatus != "probed" {
			t.Fatalf("probe status = %q, want probed", status.ProbeStatus)
		}
		if status.LastProbe == nil || !status.LastProbe.Reachable || status.LastProbe.HTTPStatus != 200 {
			t.Fatalf("last probe = %+v, want the stubbed reachable result", status.LastProbe)
		}
	})

	t.Run("unreachable-is-honest", func(t *testing.T) {
		probeFn = func(ctx context.Context, rawURL string, timeout time.Duration) ghreq.ProbeResult {
			return ghreq.ProbeResult{Reachable: false, CheckedAt: time.Now().UTC(), Error: "dial timeout (honest)"}
		}
		svc := newSystemTestService(t)
		status := svc.ProbeNetwork()
		if status.LastProbe == nil || status.LastProbe.Reachable {
			t.Fatalf("an unreachable probe must stay unreachable, got %+v", status.LastProbe)
		}
		if status.LastProbe.Error != "dial timeout (honest)" {
			t.Fatalf("the error class must be carried verbatim, got %q", status.LastProbe.Error)
		}
	})
}

// TestSystemRoutesRegistered proves the /api/system and /api/network
// routes are wired on the same facade the Wails binding exposes.
func TestSystemRoutesRegistered(t *testing.T) {
	svc := newSystemTestService(t)
	handler := NewAssetHandler(svc)
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	for _, path := range []string{"/api/system", "/api/network"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", path, resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); ct[:16] != "application/json" {
			t.Fatalf("GET %s content type = %q, want JSON", path, ct)
		}
	}
	// The probe route must exist and answer POST (with the stubbed
	// probeFn so no real network is touched).
	prev := probeFn
	t.Cleanup(func() { probeFn = prev })
	probeFn = func(ctx context.Context, rawURL string, timeout time.Duration) ghreq.ProbeResult {
		return ghreq.ProbeResult{Reachable: false, CheckedAt: time.Now().UTC(), Error: "stubbed"}
	}
	resp, err := http.Post(srv.URL+"/api/network/probe", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /api/network/probe: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/network/probe = %d, want 200", resp.StatusCode)
	}
}
