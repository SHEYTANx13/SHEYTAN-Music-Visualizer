package ghreq

// probe_test.go - run-45678113841 fixtures for the bounded connectivity
// probe: policy enforcement, honest failure states, bounded latency and
// the HTML-error-page guard.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// swapProbeClientForTLS installs, for the duration of one test, a
// loopback-exemption transport that also trusts the given httptest TLS
// server certificate, then restores the production policy client.
func swapProbeClientForTLS(t *testing.T, srv *httptest.Server) func() {
	t.Helper()
	prev := probeHTTPClient
	transport := policyTransport(true).Clone()
	transport.TLSClientConfig = srv.Client().Transport.(*http.Transport).TLSClientConfig
	probeHTTPClient = func() *http.Client {
		return &http.Client{Transport: transport, CheckRedirect: policyRedirectFn()}
	}
	return func() { probeHTTPClient = prev }
}

func TestProbeAllowlistedOriginSuccess(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Errorf("probe must never carry credentials, got %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"resources":{}}`))
	}))
	t.Cleanup(srv.Close)
	defer swapProbeClientForTLS(t, srv)()
	// srv.URL is https://127.0.0.1:<port>: the documented policy loopback
	// test-exemption host, with the server's own test certificate trusted
	// by the injected client.
	res := Probe(context.Background(), srv.URL, 3*time.Second)
	if !res.Reachable || res.HTTPStatus != 200 {
		t.Fatalf("probe = %+v, want reachable 200", res)
	}
	if res.Error != "" {
		t.Fatalf("unexpected probe error: %q", res.Error)
	}
}

func TestProbeRejectsNonAllowlistedOrigin(t *testing.T) {
	res := Probe(context.Background(), "https://example.invalid/rate_limit", time.Second)
	if res.Reachable {
		t.Fatal("a non-allowlisted origin must never be probed")
	}
	if !strings.Contains(res.Error, "unapproved download host") {
		t.Fatalf("error must name the policy class, got %q", res.Error)
	}
}

func TestProbeRejectsInsecureScheme(t *testing.T) {
	res := Probe(context.Background(), "http://api.github.com/rate_limit", time.Second)
	if res.Reachable {
		t.Fatal("plain HTTP origins must be refused")
	}
	if !strings.Contains(res.Error, "insecure download scheme") {
		t.Fatalf("error must name the scheme class, got %q", res.Error)
	}
}

func TestProbeUnreachableIsHonestFailure(t *testing.T) {
	// A server that accepts then hangs: the timeout must bound the
	// attempt and produce a reachable=false result with the error text.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	}))
	t.Cleanup(srv.Close)
	defer swapProbeClientForTLS(t, srv)()
	start := time.Now()
	res := Probe(context.Background(), srv.URL, 600*time.Millisecond)
	elapsed := time.Since(start)
	if res.Reachable {
		t.Fatal("a hanging endpoint must not be reported reachable")
	}
	if res.Error == "" {
		t.Fatal("the probe error must be recorded, never swallowed")
	}
	if elapsed > 4*time.Second {
		t.Fatalf("probe exceeded its timeout bound: %v", elapsed)
	}
}

func TestProbeHTMLBodyNotReachable(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><head><title>Access denied</title></head><body></body></html>"))
	}))
	t.Cleanup(srv.Close)
	defer swapProbeClientForTLS(t, srv)()
	res := Probe(context.Background(), srv.URL, 3*time.Second)
	if res.Reachable {
		t.Fatal("an HTML error page behind HTTP 200 is not a healthy probe")
	}
	if !strings.Contains(res.Error, "HTML error page") {
		t.Fatalf("error must name the HTML guard, got %q", res.Error)
	}
}

func TestProbeServerErrorCodeNotReachable(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	defer swapProbeClientForTLS(t, srv)()
	res := Probe(context.Background(), srv.URL, 3*time.Second)
	if res.Reachable || res.HTTPStatus != 500 {
		t.Fatalf("a 500 must be an honest unreachable state, got %+v", res)
	}
}

func TestProbeRespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := Probe(ctx, "https://api.github.com/rate_limit", 3*time.Second)
	if res.Reachable {
		t.Fatal("a cancelled context must produce an unreachable result")
	}
	if res.Error == "" {
		t.Fatal("the cancellation must be recorded as the probe error")
	}
}
