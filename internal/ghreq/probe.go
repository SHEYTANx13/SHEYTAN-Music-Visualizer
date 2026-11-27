package ghreq

// probe.go — bounded connectivity probe for the user-visible network
// status surface (run 45678113841 update: "using system information and
// internet connection" + "secure internet connection").
//
// The probe is a REAL, bounded HTTPS request against one ALLOWLISTED
// origin. It never sends credentials, never follows a redirect outside
// the origin policy (the policy client enforces that), never reads the
// response body into memory beyond a bounded head check, and never
// runs automatically: the user (or a shell refresh action) triggers it.
// The result is displayed verbatim — an unreachable network is an
// honest state, never an error hidden behind a fake "ok".

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// ProbeOrigin is the fixed, allowlisted origin the network status
// surface probes: the GitHub API root used by the app's own update
// discovery (pinned media-tools manifest). Probing the exact origin the
// app actually depends on keeps the status honest.
const ProbeOrigin = DefaultBaseURL + "/rate_limit"

// probeHTTPClient is the client factory for one probe. Production uses
// the strict policy client (loopback blocked at dial level); tests
// swap in the documented loopback-exemption transport so the policy
// behavior itself stays under test against a real HTTP server.
var probeHTTPClient = policyHTTPClient

// ProbeResult is one bounded probe outcome, safe to serialize to the
// UI verbatim (all fields ASCII; Error is sanitized and capped).
type ProbeResult struct {
	Reachable  bool      `json:"reachable"`
	HTTPStatus int       `json:"httpStatus,omitempty"`
	LatencyMs  int64     `json:"latencyMs,omitempty"`
	CheckedAt  time.Time `json:"checkedAt"`
	Error      string    `json:"error,omitempty"`
}

// Probe performs ONE bounded GET against rawURL under the download
// origin policy (HTTPS-only, allowlisted hosts, SSRF-guarded dial,
// bounded redirects, credentials never attached). The context and
// timeout bound the whole attempt; a nil context becomes a fresh
// background context. The response body is closed after a bounded
// head check; nothing from the body is returned.
func Probe(ctx context.Context, rawURL string, timeout time.Duration) ProbeResult {
	res := ProbeResult{CheckedAt: time.Now().UTC()}
	if ctx == nil {
		ctx = context.Background()
	}
	if timeout <= 0 {
		timeout = 6 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := ValidateOrigin(rawURL); err != nil {
		res.Error = asciiCap(err.Error(), 200)
		return res
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		res.Error = asciiCap(err.Error(), 200)
		return res
	}
	req.Header.Set("Accept", acceptJSON)
	req.Header.Set("User-Agent", "SHEYTAN-Music-Visualizer-BETA")
	// No Authorization header by contract: the probe carries no
	// credentials and must stay useful for anonymous, unauthenticated
	// connectivity diagnosis.

	start := time.Now()
	resp, err := probeHTTPClient().Do(req)
	res.LatencyMs = time.Since(start).Milliseconds()
	if err != nil {
		res.Error = asciiCap(err.Error(), 200)
		return res
	}
	defer resp.Body.Close()
	res.HTTPStatus = resp.StatusCode
	res.Reachable = resp.StatusCode >= 200 && resp.StatusCode < 500
	// Bounded head read: an HTML error page (captive portal, proxy
	// block page) is NOT a healthy probe even behind a 200.
	head := make([]byte, 512)
	n, _ := resp.Body.Read(head)
	if LooksLikeHTMLErrorPage(head[:n]) {
		res.Reachable = false
		res.Error = asciiCap("response looks like an HTML error page, not the GitHub API", 200)
	}
	return res
}

// asciiCap degrades a message to printable ASCII and caps its length so
// probe failures never leak control characters into the UI or logs.
func asciiCap(s string, cap int) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 0x20 && r <= 0x7E {
			b.WriteRune(r)
		} else {
			b.WriteByte('?')
		}
	}
	out := b.String()
	if len(out) > cap {
		out = out[:cap]
	}
	return out
}
