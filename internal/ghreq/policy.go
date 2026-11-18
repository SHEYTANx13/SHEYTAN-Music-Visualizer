package ghreq

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Download-origin policy (work order section 6.5): HTTPS only,
// canonical GitHub hosts plus the documented GitHub-controlled asset
// redirect hosts, no credential forwarding, no downgrade redirects,
// no userinfo, no private or reserved IP targets.
var (
	ErrInsecureScheme  = errors.New("ghreq: insecure download scheme")
	ErrUnapprovedHost  = errors.New("ghreq: unapproved download host")
	ErrURLCredentials  = errors.New("ghreq: url carries credentials")
	ErrRedirectLoop    = errors.New("ghreq: too many redirects")
	ErrPrivateAddress  = errors.New("ghreq: private or reserved address blocked")
	ErrNotCanonicalURL = errors.New("ghreq: non-canonical url")
)

// AllowedHosts are the only download origins this application may
// contact. api.github.com serves metadata; github.com serves release
// pages and the first redirect hop of release assets; the
// *githubusercontent.com hosts are GitHub's documented object-storage
// and release-asset targets.
var AllowedHosts = []string{
	"api.github.com",
	"github.com",
	"objects.githubusercontent.com",
	"release-assets.githubusercontent.com",
	"codeload.github.com",
}

func hostAllowed(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	if i := strings.LastIndexByte(h, ':'); i >= 0 && !strings.Contains(h, "]") {
		h = h[:i]
	}
	h = strings.Trim(h, "[]")
	if isLoopbackTestHost(h) {
		// Loopback is admitted at the origin layer for deterministic
		// tests only; the dial-level guard still blocks it whenever the
		// transport is built without the test exemption.
		return true
	}
	for _, a := range AllowedHosts {
		if h == a {
			return true
		}
	}
	return false
}

// ValidateOrigin checks one download URL against the origin policy.
func ValidateOrigin(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("%w: %q", ErrNotCanonicalURL, raw)
	}
	if u.User != nil {
		return ErrURLCredentials
	}
	if u.Scheme != "https" && !isLoopbackTestHost(u.Host) {
		return fmt.Errorf("%w: %s", ErrInsecureScheme, u.Scheme)
	}
	if !hostAllowed(u.Host) {
		return fmt.Errorf("%w: %s", ErrUnapprovedHost, u.Host)
	}
	return nil
}

// reservedIP reports whether the IP is private, loopback, link-local
// or otherwise non-routable. GitHub-owned hosts are validated by name
// before any connection; the dial check is defense in depth for
// redirect targets that resolve inside the runner network.
func reservedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return true
	}
	// IPv4-mapped IPv6 unwrap.
	if v4 := ip.To4(); v4 != nil && !ip.Equal(v4) {
		return reservedIP(v4)
	}
	// Documentation, benchmarking and other reserved ranges.
	for _, cidr := range reservedCIDRs {
		if _, found := parsedCIDR(cidr); found && parsedCIDRContains(cidr, ip) {
			return true
		}
	}
	return false
}

var reservedCIDRs = []string{
	"192.0.2.0/24",    // TEST-NET-1
	"198.51.100.0/24", // TEST-NET-2
	"203.0.113.0/24",  // TEST-NET-3
	"100.64.0.0/10",   // carrier-grade NAT
	"169.254.0.0/16",  // link-local (v4, belt and braces)
	"fe80::/10",       // link-local (v6)
}

// policyTransport returns a transport that refuses to dial private or
// reserved addresses (SSRF defense for redirect targets) with a
// bounded dial timeout. Loopback is exempted only for tests via
// allowLoopback.
func policyTransport(allowLoopback bool) *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		ips, err := net.LookupIP(host)
		if err != nil {
			return nil, fmt.Errorf("%w: lookup %s: %v", ErrPrivateAddress, host, err)
		}
		for _, ip := range ips {
			if reservedIP(ip) {
				if allowLoopback && ip.IsLoopback() {
					continue
				}
				return nil, fmt.Errorf("%w: %s resolved %s", ErrPrivateAddress, host, ip)
			}
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("%w: %s resolved nothing", ErrPrivateAddress, host)
		}
		d := net.Dialer{Timeout: 15 * time.Second}
		return d.DialContext(ctx, network, net.JoinHostPort(host, port))
	}
	return t
}

// policyHTTPClient returns the API client with the strict redirect
// policy: maximum 5 hops, HTTPS-only, only allowlisted hosts,
// credentials never forwarded cross-host.
func policyHTTPClient() *http.Client {
	return &http.Client{
		Timeout:       60 * time.Second,
		Transport:     policyTransport(false),
		CheckRedirect: policyRedirectFn(),
	}
}

// policyRedirectFn is the shared redirect contract.
func policyRedirectFn() func(req *http.Request, via []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("%w: stopped after %d redirects", ErrRedirectLoop, len(via))
		}
		next := req.URL
		if next.User != nil {
			return ErrURLCredentials
		}
		if next.Scheme != "https" && !isLoopbackTestHost(next.Host) {
			return fmt.Errorf("%w: redirect to %s", ErrInsecureScheme, next.Scheme)
		}
		if !hostAllowed(next.Host) {
			return fmt.Errorf("%w: redirect to %s", ErrUnapprovedHost, next.Host)
		}
		if len(via) > 0 {
			prev := via[len(via)-1]
			if !sameHost(prev.URL.Host, next.Host) {
				// Strip any credential-bearing or session headers
				// before leaving the original host.
				req.Header.Del("Authorization")
				req.Header.Del("Cookie")
				for k := range prev.URL.Query() {
					if strings.EqualFold(k, "token") || strings.EqualFold(k, "access_token") {
						return ErrURLCredentials
					}
				}
			}
		}
		return nil
	}
}

func sameHost(a, b string) bool {
	na := strings.ToLower(strings.Trim(a, "[]"))
	nb := strings.ToLower(strings.Trim(b, "[]"))
	if i := strings.LastIndexByte(na, ':'); i >= 0 && !strings.Contains(na, "]") {
		na = na[:i]
	}
	if i := strings.LastIndexByte(nb, ':'); i >= 0 && !strings.Contains(nb, "]") {
		nb = nb[:i]
	}
	return strings.Trim(na, "[]") == strings.Trim(nb, "[]")
}

// parsedCIDR / parsedCIDRContains are tiny helpers so the reserved
// range table stays declarative without package-level parse work per
// call site.
func parsedCIDR(cidr string) (net.IPNet, bool) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil || ipnet == nil {
		return net.IPNet{}, false
	}
	return *ipnet, true
}

func parsedCIDRContains(cidr string, ip net.IP) bool {
	ipnet, ok := parsedCIDR(cidr)
	return ok && ipnet.Contains(ip)
}
