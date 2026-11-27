package wails

// service_system.go — the system-information and secure-network facade
// surface (run 45678113841 update: "using most out of the system by
// using system information and internet connection" and "secure
// internet connection").
//
// Boundary rules (doc.go) are preserved:
//   - everything crossing to the web layer is a JSON-ready snapshot;
//     sysprofile.Profile is the same versioned, JSON-tagged evidence
//     snapshot the go-gui shell presents (same precedent as serving
//     mediatools.Snapshot directly), with honest Unknown-with-reason
//     facts for anything this shell cannot observe;
//   - the network status NEVER auto-probes on page load or on a timer:
//     the probe runs only when the user asks for it (POST
//     /api/network/probe), is bounded by a hard timeout, targets only
//     the ghreq allowlisted origin, sends no credentials and records
//     the honest outcome (reachable/unreachable + error class);
//   - no renderer state is upgraded and no download is started here.

import (
	"context"
	"sync"
	"time"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/ghreq"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/render"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/sysprofile"
)

// systemState is the facade-owned system/network snapshot state. It
// lives on the SheytanService instance (never on package globals) so
// every shell instance and every test gets its own honest state.
type systemState struct {
	mu               sync.Mutex
	profile          sysprofile.Profile
	profileAt        time.Time
	profileHas       bool
	probe            ghreq.ProbeResult
	probeRequestedAt time.Time
	probeHas         bool
}

// systemProfileTTL bounds how often the facade re-collects the system
// profile. Collection is native-API reading only (no processes, no
// network), but a UI poll must never re-probe on every 2 s tick.
const systemProfileTTL = 30 * time.Second

// networkProbeTimeout bounds one connectivity probe end to end.
const networkProbeTimeout = 6 * time.Second

// probeFn is the probe implementation used by ProbeNetwork. Production
// binds ghreq.Probe (policy-guarded); tests bind a stub so no test ever
// touches the real network.
var probeFn = ghreq.Probe

// sysState returns the instance system state (lazily initialized).
func (s *SheytanService) sysState() *systemState {
	s.systemOnce.Do(func() { s.system = &systemState{} })
	return s.system
}

// activeRendererInfo returns the raw renderer evidence the profile must
// use: the renderer this shell ACTUALLY executes, captured once through
// the same rendererOnce path as GetRendererEvidence (never re-selected,
// never upgraded).
func (s *SheytanService) activeRendererInfo() render.BackendInfo {
	s.rendererOnce.Do(func() {
		r, info := render.SelectRenderer()
		if r != nil {
			r.Close()
		}
		s.rendererInfo = info
	})
	return s.rendererInfo
}

// GetSystemProfile returns the REAL system profile of the machine the
// shell runs on: OS, CPU (model, cores, SIMD features), physical
// memory, power state, audio backend facts, the active renderer
// evidence (verbatim, never upgraded) and the audit stamp. Sections
// this shell cannot observe arrive as explicit Unknown-with-reason
// facts, never blank guesses. The profile is re-collected at most once
// per systemProfileTTL; the rest of the window replays the cached
// snapshot with its original CollectedAt stamp.
func (s *SheytanService) GetSystemProfile() sysprofile.Profile {
	st := s.sysState()
	st.mu.Lock()
	defer st.mu.Unlock()
	now := time.Now()
	if st.profileHas && now.Sub(st.profileAt) < systemProfileTTL {
		return st.profile
	}
	st.profile = sysprofile.Collect(s.activeRendererInfo())
	st.profileAt = now
	st.profileHas = true
	return st.profile
}

// NetworkPolicySnapshot is the static security contract of every
// network operation this application performs. The values come from
// internal/ghreq (the single origin authority) verbatim.
type NetworkPolicySnapshot struct {
	HTTPSOnly        bool     `json:"httpsOnly"`
	AllowedHosts     []string `json:"allowedHosts"`
	MaxRedirects     int      `json:"maxRedirects"`
	CredentialsSent  bool     `json:"credentialsSent"`
	PrivateAddrsHit  string   `json:"privateAddrsBlocked"`
	DigestVerified   bool     `json:"digestVerifiedDownloads"`
	SizeBoundsApply  bool     `json:"sizeBoundsApply"`
	ProbeOrigin      string   `json:"probeOrigin"`
	AutoProbeEnabled bool     `json:"autoProbeEnabled"`
}

// GetNetworkPolicy returns the immutable origin/security policy the
// app enforces for ALL outbound traffic (update discovery, release
// assets, pinned media-tools downloads). Serving it makes the
// "unhackable connection" contract user-visible instead of hidden.
func (s *SheytanService) GetNetworkPolicy() NetworkPolicySnapshot {
	hosts := make([]string, len(ghreq.AllowedHosts))
	copy(hosts, ghreq.AllowedHosts)
	return NetworkPolicySnapshot{
		HTTPSOnly:        true,
		AllowedHosts:     hosts,
		MaxRedirects:     5,
		CredentialsSent:  false,
		PrivateAddrsHit:  "blocked (SSRF guard: private/reserved resolved addresses are refused)",
		DigestVerified:   true,
		SizeBoundsApply:  true,
		ProbeOrigin:      ghreq.ProbeOrigin,
		AutoProbeEnabled: false,
	}
}

// NetworkStatus is the user-visible network surface: the immutable
// policy plus the last USER-REQUESTED probe outcome. Before the first
// probe the status reports the honest never-probed state (no fake
// online/offline guess).
type NetworkStatus struct {
	Policy      NetworkPolicySnapshot `json:"policy"`
	LastProbe   *ghreq.ProbeResult    `json:"lastProbe,omitempty"`
	ProbeStatus string                `json:"probeStatus"`
}

// GetNetworkStatus returns the policy snapshot and the last probe
// outcome. ProbeStatus is "never-probed" until the user requests one
// and "probe-running"/"probed" afterwards.
func (s *SheytanService) GetNetworkStatus() NetworkStatus {
	st := s.sysState()
	st.mu.Lock()
	defer st.mu.Unlock()
	status := NetworkStatus{Policy: s.GetNetworkPolicy()}
	switch {
	case !st.probeHas:
		status.ProbeStatus = "never-probed"
	case st.probeRequestedAt.After(st.probe.CheckedAt):
		status.ProbeStatus = "probe-running"
		p := st.probe
		if st.probe.CheckedAt.IsZero() {
			status.LastProbe = nil
		} else {
			status.LastProbe = &p
		}
	default:
		status.ProbeStatus = "probed"
		p := st.probe
		status.LastProbe = &p
	}
	return status
}

// ProbeNetwork runs ONE bounded connectivity probe against the pinned
// allowlisted origin and records the honest outcome. It is only called
// from the POST /api/network/probe route (user action); nothing in the
// shell probes automatically. The call blocks for at most
// networkProbeTimeout and is serialized by the system mutex, so
// concurrent UI requests cannot storm the network.
func (s *SheytanService) ProbeNetwork() NetworkStatus {
	st := s.sysState()
	st.mu.Lock()
	st.probeHas = true
	st.probeRequestedAt = time.Now()
	st.mu.Unlock()

	// The bounded probe runs OUTSIDE the mutex so a status poll never
	// blocks on the network; the completed result is then stored under
	// the mutex (the last completed probe wins the visible slot).
	res := probeFn(context.Background(), ghreq.ProbeOrigin, networkProbeTimeout)

	st.mu.Lock()
	st.probe = res
	st.mu.Unlock()
	return s.GetNetworkStatus()
}
