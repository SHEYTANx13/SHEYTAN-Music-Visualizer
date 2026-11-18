package ghreq

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// API constants follow the official GitHub REST documentation.
const (
	DefaultBaseURL   = "https://api.github.com"
	DefaultAPIVer    = "2022-11-28"
	acceptJSON       = "application/vnd.github+json"
	maxReleasesPages = 5
	pageSize         = 50
	maxRespBytes     = 8 << 20 // 8 MiB bound on metadata responses
)

// Typed client failures.
var (
	ErrRateLimited   = errors.New("ghreq: github api rate limited")
	ErrNotFound      = errors.New("ghreq: github api not found")
	ErrNetwork       = errors.New("ghreq: github api network failure")
	ErrBadResponse   = errors.New("ghreq: github api unexpected response")
	ErrNotModified   = errors.New("ghreq: not modified")
	ErrNotStableRel  = errors.New("ghreq: release is draft or prerelease")
	ErrBadOwnerRepo  = errors.New("ghreq: owner/repo rejected")
	ErrPaginationEnd = errors.New("ghreq: pagination bound reached")
)

// Release mirrors the documented release object fields this package
// consumes. Unknown fields are ignored.
type Release struct {
	ID          int64     `json:"id"`
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	HTMLURL     string    `json:"html_url"`
	Assets      []Asset   `json:"assets"`
}

// Asset mirrors the documented release-asset object. Digest carries
// GitHub's release-asset sha256 when the API supplies one; its
// presence is transport metadata, not publisher identity proof.
type Asset struct {
	ID                 int64  `json:"id"`
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	State              string `json:"state"`
	ContentType        string `json:"content_type"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Digest             string `json:"digest"`
}

// RateLimitInfo reports the observed rate-limit state of the last
// response, for user-visible diagnostics.
type RateLimitInfo struct {
	Limit      int
	Remaining  int
	Reset      time.Time
	RetryAfter time.Duration
}

// Client is a bounded, cache-aware GitHub REST client for release
// discovery. It sends no credentials: public release metadata needs no
// token and embedding one is forbidden by the security contract.
type Client struct {
	// HTTP performs API requests. nil uses a policy-bound default.
	HTTP *http.Client
	// BaseURL is the API root; tests override it.
	BaseURL string
	// APIVersion pins the documented X-GitHub-Api-Version header.
	APIVersion string

	mu        sync.Mutex
	etag      map[string]string // url -> etag
	body      map[string][]byte // url -> cached body
	lastLimit RateLimitInfo
	nowFunc   func() time.Time
}

// NewClient returns a client bound to the official API host.
func NewClient() *Client {
	return &Client{
		BaseURL:    DefaultBaseURL,
		APIVersion: DefaultAPIVer,
		etag:       make(map[string]string),
		body:       make(map[string][]byte),
		nowFunc:    time.Now,
	}
}

// RateLimit returns the rate-limit state of the most recent response.
func (c *Client) RateLimit() RateLimitInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastLimit
}

// Release round trips
//
// getJSON performs a conditional GET with ETag caching, bounded body
// reads, typed rate-limit handling and strict JSON decoding into a
// fresh value. A 304 replay only happens when a cached body exists.
func (c *Client) getJSON(ctx context.Context, rawURL string, dst any) error {
	if c.HTTP == nil {
		c.HTTP = policyHTTPClient()
	}
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Errorf("%w: %q", ErrBadResponse, rawURL)
	}
	if u.Scheme != "https" {
		// The API client must never downgrade; tests may use http on
		// loopback only via the policy client's explicit exemption.
		if !isLoopbackTestHost(u.Host) {
			return fmt.Errorf("%w: non-https api url", ErrBadResponse)
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", acceptJSON)
	req.Header.Set("X-GitHub-Api-Version", c.apiVersion())
	req.Header.Set("User-Agent", "SHEYTAN-Music-Visualizer-BETA")

	c.mu.Lock()
	if et, ok := c.etag[rawURL]; ok {
		req.Header.Set("If-None-Match", et)
	}
	c.mu.Unlock()

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNetwork, err)
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()

	c.recordLimit(resp)

	switch {
	case resp.StatusCode == http.StatusNotModified:
		c.mu.Lock()
		b, ok := c.body[rawURL]
		c.mu.Unlock()
		if !ok {
			return fmt.Errorf("%w: 304 without cached body", ErrBadResponse)
		}
		return json.Unmarshal(b, dst)
	case resp.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case resp.StatusCode == http.StatusTooManyRequests,
		resp.StatusCode == http.StatusForbidden && strings.Contains(resp.Header.Get("X-RateLimit-Remaining"), "0"):
		return &RateLimitError{RetryAfter: retryAfterFrom(resp, c.nowFunc())}
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("%w: http %d", ErrBadResponse, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxRespBytes+1))
	if err != nil {
		return fmt.Errorf("%w: read: %v", ErrNetwork, err)
	}
	if len(body) > maxRespBytes {
		return fmt.Errorf("%w: response exceeds %d bytes", ErrBadResponse, maxRespBytes)
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return fmt.Errorf("%w: json: %v", ErrBadResponse, err)
	}
	if et := resp.Header.Get("ETag"); et != "" {
		c.mu.Lock()
		c.etag[rawURL] = et
		c.body[rawURL] = body
		c.mu.Unlock()
	}
	return nil
}

// RateLimitError is returned when GitHub reports rate limiting. The
// caller must respect RetryAfter and must not retry in a tight loop.
type RateLimitError struct {
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("%v (retry after %s)", ErrRateLimited, e.RetryAfter)
}

// Is lets callers test with errors.Is.
func (e *RateLimitError) Is(target error) bool { return target == ErrRateLimited }

func retryAfterFrom(resp *http.Response, now time.Time) time.Duration {
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(ra); err == nil && secs >= 0 {
			return time.Duration(secs) * time.Second
		}
		if t, err := http.ParseTime(ra); err == nil {
			if d := t.Sub(now); d > 0 {
				return d
			}
		}
	}
	if rem := resp.Header.Get("X-RateLimit-Remaining"); rem == "0" {
		if reset, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			if d := time.Unix(reset, 0).Sub(now); d > 0 {
				return d
			}
		}
	}
	return 60 * time.Second
}

func (c *Client) apiVersion() string {
	if c.APIVersion == "" {
		return DefaultAPIVer
	}
	return c.APIVersion
}

func (c *Client) recordLimit(resp *http.Response) {
	lim := RateLimitInfo{}
	if v, err := strconv.Atoi(resp.Header.Get("X-RateLimit-Limit")); err == nil {
		lim.Limit = v
	}
	if v, err := strconv.Atoi(resp.Header.Get("X-RateLimit-Remaining")); err == nil {
		lim.Remaining = v
	}
	if v, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
		lim.Reset = time.Unix(v, 0)
	}
	lim.RetryAfter = retryAfterFrom(resp, c.nowFunc())
	c.mu.Lock()
	c.lastLimit = lim
	c.mu.Unlock()
}

func isLoopbackTestHost(host string) bool {
	h := host
	if i := strings.LastIndexByte(h, ':'); i >= 0 && !strings.Contains(h, "]") {
		h = h[:i]
	}
	h = strings.Trim(h, "[]")
	return h == "127.0.0.1" || h == "::1" || h == "localhost"
}

// releaseURL builds an API URL from exact owner/repo values.
func (c *Client) releaseURL(owner, repo, suffix string) (string, error) {
	if !validOwnerRepo(owner) || !validOwnerRepo(repo) {
		return "", fmt.Errorf("%w: %s/%s", ErrBadOwnerRepo, owner, repo)
	}
	return fmt.Sprintf("%s/repos/%s/%s/releases%s", strings.TrimRight(c.BaseURL, "/"), owner, repo, suffix), nil
}

func validOwnerRepo(s string) bool {
	if s == "" || len(s) > 100 {
		return false
	}
	for _, r := range s {
		ok := r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' ||
			r == '.' || r == '-' || r == '_'
		if !ok {
			return false
		}
	}
	return s != "." && s != ".." && !strings.Contains(s, "..")
}

// LatestStable returns the newest published, non-draft,
// non-prerelease release. The endpoint contract already excludes
// drafts and prereleases; the extra guard keeps the promise even if a
// proxy or test fixture misbehaves.
func (c *Client) LatestStable(ctx context.Context, owner, repo string) (*Release, error) {
	u, err := c.releaseURL(owner, repo, "/latest")
	if err != nil {
		return nil, err
	}
	var rel Release
	if err := c.getJSON(ctx, u, &rel); err != nil {
		return nil, err
	}
	if rel.Draft || rel.Prerelease {
		return nil, fmt.Errorf("%w: %s", ErrNotStableRel, rel.TagName)
	}
	return &rel, nil
}

// ReleaseByTag resolves one exact tag. Draft and prerelease results
// are returned as-is so pinned prerelease policies can label them
// experimental; callers that require stable must check the flags.
func (c *Client) ReleaseByTag(ctx context.Context, owner, repo, tag string) (*Release, error) {
	if tag == "" || len(tag) > 120 || strings.ContainsAny(tag, "?#/\\ ") {
		return nil, fmt.Errorf("%w: bad tag %q", ErrBadOwnerRepo, tag)
	}
	u, err := c.releaseURL(owner, repo, "/tags/"+url.PathEscape(tag))
	if err != nil {
		return nil, err
	}
	var rel Release
	if err := c.getJSON(ctx, u, &rel); err != nil {
		return nil, err
	}
	return &rel, nil
}

// ListReleases walks bounded release history: at most maxReleasesPages
// pages of pageSize items, deduplicated by release ID in API order.
// The bound is a contract requirement, not an optimization.
func (c *Client) ListReleases(ctx context.Context, owner, repo string) ([]*Release, error) {
	if !validOwnerRepo(owner) || !validOwnerRepo(repo) {
		return nil, fmt.Errorf("%w: %s/%s", ErrBadOwnerRepo, owner, repo)
	}
	var out []*Release
	seen := make(map[int64]bool)
	for page := 1; page <= maxReleasesPages; page++ {
		raw := fmt.Sprintf("%s/repos/%s/%s/releases?per_page=%d&page=%d",
			strings.TrimRight(c.BaseURL, "/"), owner, repo, pageSize, page)
		var batch []*Release
		if err := c.getJSON(ctx, raw, &batch); err != nil {
			return out, err
		}
		if len(batch) == 0 {
			break
		}
		for _, r := range batch {
			if r == nil || seen[r.ID] {
				continue
			}
			seen[r.ID] = true
			out = append(out, r)
		}
		if len(batch) < pageSize {
			break
		}
	}
	return out, nil
}

// Resolve applies a requirement's release policy to discover its
// release. PinnedTag resolves the exact tag; StableLatest resolves
// the newest stable; UserSelected resolves a caller-chosen stable tag
// and refuses draft/prerelease unless the caller opted in explicitly.
func (c *Client) Resolve(ctx context.Context, req *Requirement, userTag string, allowPrerelease bool) (*Release, error) {
	switch req.ReleasePolicy {
	case ReleasePinnedTag:
		rel, err := c.ReleaseByTag(ctx, req.Owner, req.Repo, req.PinnedTag)
		if err != nil {
			return nil, err
		}
		if (rel.Draft || rel.Prerelease) && !allowPrerelease {
			return nil, fmt.Errorf("%w: %s", ErrNotStableRel, rel.TagName)
		}
		return rel, nil
	case ReleaseStableLatest:
		return c.LatestStable(ctx, req.Owner, req.Repo)
	case ReleaseUserSelected:
		if userTag == "" {
			return nil, errors.New("ghreq: user-selected policy requires a tag")
		}
		rel, err := c.ReleaseByTag(ctx, req.Owner, req.Repo, userTag)
		if err != nil {
			return nil, err
		}
		if (rel.Draft || rel.Prerelease) && !allowPrerelease {
			return nil, fmt.Errorf("%w: %s", ErrNotStableRel, rel.TagName)
		}
		return rel, nil
	default:
		return nil, fmt.Errorf("ghreq: unknown release policy %q", req.ReleasePolicy)
	}
}
