package ghreq

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidateOrigin(t *testing.T) {
	cases := []struct {
		url  string
		want error
	}{
		{"https://github.com/BtbN/FFmpeg-Builds/releases/download/tag/file.zip", nil},
		{"https://api.github.com/repos/a/b", nil},
		{"https://objects.githubusercontent.com/some/path", nil},
		{"https://release-assets.githubusercontent.com/x", nil},
		{"http://github.com/x", ErrInsecureScheme},
		{"https://evil.example.com/payload", ErrUnapprovedHost},
		{"https://user:pass@github.com/x", ErrURLCredentials},
		{"https://github.com:8080/x", nil},
		{"not a url", ErrNotCanonicalURL},
	}
	for _, tc := range cases {
		err := ValidateOrigin(tc.url)
		if tc.want == nil {
			if err != nil {
				t.Errorf("%q: unexpected error %v", tc.url, err)
			}
			continue
		}
		if !errors.Is(err, tc.want) {
			t.Errorf("%q: want %v, got %v", tc.url, tc.want, err)
		}
	}
	// Plain http on loopback is allowed for tests only.
	if err := ValidateOrigin("http://127.0.0.1:8080/asset"); err != nil {
		t.Errorf("loopback http rejected: %v", err)
	}
}

func TestRedirectPolicyBlocksUnapprovedHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://evil.example.com/payload.bin", http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	client := &http.Client{Transport: policyTransport(true), CheckRedirect: policyRedirectFn()}
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	_, err := client.Do(req)
	if err == nil || !errors.Is(err, ErrUnapprovedHost) {
		t.Fatalf("want unapproved-host rejection, got %v", err)
	}
	if !policyFailure(err) {
		t.Fatal("unapproved host must be a permanent failure class")
	}
}

func TestRedirectPolicyBlocksDowngrade(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://github.com/payload.bin", http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	client := &http.Client{Transport: policyTransport(true), CheckRedirect: policyRedirectFn()}
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	_, err := client.Do(req)
	if err == nil || !errors.Is(err, ErrInsecureScheme) {
		t.Fatalf("want downgrade rejection, got %v", err)
	}
}

func TestRedirectPolicyStripsCredentialsCrossHost(t *testing.T) {
	fn := policyRedirectFn()
	next, _ := http.NewRequest(http.MethodGet, "https://github.com/payload.bin", nil)
	next.Header.Set("Authorization", "Bearer secret")
	prev, _ := http.NewRequest(http.MethodGet, "https://api.github.com/repos/a/b/releases/latest", nil)
	if err := fn(next, []*http.Request{prev}); err != nil {
		t.Fatalf("legitimate cross-host redirect rejected: %v", err)
	}
	if got := next.Header.Get("Authorization"); got != "" {
		t.Fatalf("authorization header not stripped cross-host: %q", got)
	}

	// A token-bearing query on the previous hop must be refused.
	prev2, _ := http.NewRequest(http.MethodGet, "https://github.com/x?token=secret", nil)
	next2, _ := http.NewRequest(http.MethodGet, "https://objects.githubusercontent.com/y", nil)
	if err := fn(next2, []*http.Request{prev2}); !errors.Is(err, ErrURLCredentials) {
		t.Fatalf("token query not refused: %v", err)
	}
}

func TestRedirectPolicyBlocksPrivateAddresses(t *testing.T) {
	// The dial-level SSRF guard: reserved ranges must be classified
	// reserved while real public addresses must not be.
	if !reservedIP(mustIP(t, "192.0.2.55")) {
		t.Fatal("TEST-NET-1 not classified reserved")
	}
	if reservedIP(mustIP(t, "140.82.112.3")) {
		t.Fatal("public GitHub address misclassified as reserved")
	}
	if !reservedIP(mustIP(t, "10.1.2.3")) {
		t.Fatal("private range not classified reserved")
	}
	if !reservedIP(mustIP(t, "127.0.0.1")) {
		t.Fatal("loopback not classified reserved")
	}
	if !reservedIP(mustIP(t, "169.254.169.254")) {
		t.Fatal("link-local metadata address not classified reserved")
	}
	if !reservedIP(mustIP(t, "100.64.1.1")) {
		t.Fatal("carrier-grade NAT not classified reserved")
	}
	// A private-resolving redirect target must never be dialed.
	client := &http.Client{Transport: policyTransport(false)}
	req, _ := http.NewRequest(http.MethodGet, "https://10.9.8.7/secret", nil)
	_, err := client.Transport.RoundTrip(req)
	if err == nil || !errors.Is(err, ErrPrivateAddress) {
		t.Fatalf("private address dial not blocked: %v", err)
	}
}

func TestRedirectDepthBounded(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/next", http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	client := &http.Client{Transport: policyTransport(true), CheckRedirect: policyRedirectFn()}
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	_, err := client.Do(req)
	if err == nil || !errors.Is(err, ErrRedirectLoop) && !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("want redirect bound, got %v", err)
	}
}

func mustIP(t *testing.T, s string) net.IP {
	t.Helper()
	ip := net.ParseIP(s)
	if ip == nil {
		t.Fatalf("bad ip %q", s)
	}
	return ip
}
