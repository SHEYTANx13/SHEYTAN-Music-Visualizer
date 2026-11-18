package ghreq

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubRelease builds a release fixture with deterministic assets.
func stubRelease(tag string, prerelease, draft bool, assets ...Asset) Release {
	return Release{
		ID:         1,
		TagName:    tag,
		Name:       "Release " + tag,
		Draft:      draft,
		Prerelease: prerelease,
		Assets:     assets,
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func testClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := NewClient()
	c.BaseURL = srv.URL
	c.HTTP = &http.Client{Transport: policyTransport(true), CheckRedirect: policyRedirectFn()}
	return c
}

func TestLatestStableExcludesPrereleaseAndDraft(t *testing.T) {
	// The endpoint contract returns one release; the client must
	// reject a draft or prerelease payload defensively.
	for _, bad := range []Release{
		stubRelease("v2.0.0", true, false),
		stubRelease("v2.0.0", false, true),
	} {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/repos/acme/widget/releases/latest" {
				http.NotFound(w, r)
				return
			}
			writeJSON(w, 200, bad)
		})
		if _, err := c.LatestStable(t.Context(), "acme", "widget"); err == nil {
			t.Fatalf("LatestStable accepted draft/prerelease %+v", bad)
		}
	}
	good := stubRelease("v2.0.0", false, false, Asset{ID: 7, Name: "widget-win64.zip", Size: 10, State: "uploaded", BrowserDownloadURL: "https://github.com/acme/widget/releases/download/v2.0.0/widget-win64.zip"})
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, good)
	})
	rel, err := c.LatestStable(t.Context(), "acme", "widget")
	if err != nil {
		t.Fatalf("stable release rejected: %v", err)
	}
	if rel.TagName != "v2.0.0" {
		t.Fatalf("unexpected tag %s", rel.TagName)
	}
}

func TestReleaseByTagResolvesExactTag(t *testing.T) {
	want := stubRelease("autobuild-2026-11-08-12-35", false, false)
	var gotTag string
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotTag = strings.TrimPrefix(r.URL.Path, "/repos/BtbN/FFmpeg-Builds/releases/tags/")
		writeJSON(w, 200, want)
	})
	rel, err := c.ReleaseByTag(t.Context(), "BtbN", "FFmpeg-Builds", "autobuild-2026-11-08-12-35")
	if err != nil {
		t.Fatalf("ReleaseByTag: %v", err)
	}
	if gotTag != "autobuild-2026-11-08-12-35" {
		t.Fatalf("request asked for tag %q", gotTag)
	}
	if rel.TagName != want.TagName {
		t.Fatalf("wrong release returned")
	}
	if _, err := c.ReleaseByTag(t.Context(), "BtbN", "FFmpeg-Builds", "bad tag"); err == nil {
		t.Fatal("bad tag accepted")
	}
}

func TestListReleasesBoundedAndDeduplicated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.RawQuery, "page=") {
			t.Errorf("list request missing page parameter: %q", r.URL.RawQuery)
		}
		page := 1
		for i := 1; i <= 20; i++ {
			if strings.HasSuffix(r.URL.RawQuery, "page="+itoa(i)) {
				page = i
			}
		}
		if page > 5 {
			t.Errorf("pagination exceeded the 5-page bound")
			writeJSON(w, 200, []Release{})
			return
		}
		// Every page returns the same release IDs: duplicates must be
		// deduplicated by ID, not appended.
		out := []Release{stubRelease("v1.0."+itoa(page), false, false)}
		writeJSON(w, 200, out)
	}))
	t.Cleanup(srv.Close)
	c := NewClient()
	c.BaseURL = srv.URL
	c.HTTP = &http.Client{Transport: policyTransport(true), CheckRedirect: policyRedirectFn()}
	rels, err := c.ListReleases(t.Context(), "acme", "widget")
	if err != nil {
		t.Fatalf("ListReleases: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("deduplication failed: got %d releases, want 1", len(rels))
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func TestETagCacheReplay(t *testing.T) {
	etag := `"v1"`
	calls := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("If-None-Match") == etag {
			w.Header().Set("ETag", etag)
			w.WriteHeader(304)
			return
		}
		w.Header().Set("ETag", etag)
		writeJSON(w, 200, stubRelease("v1.0.0", false, false))
	})
	var first Release
	if err := c.getJSON(t.Context(), c.BaseURL+"/repos/a/b/releases/latest", &first); err != nil {
		t.Fatalf("first get: %v", err)
	}
	var second Release
	if err := c.getJSON(t.Context(), c.BaseURL+"/repos/a/b/releases/latest", &second); err != nil {
		t.Fatalf("cached get: %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 requests (cache stores after first), got %d", calls)
	}
	if second.TagName != "v1.0.0" {
		t.Fatalf("cached body replay broken: %+v", second)
	}
}

func TestRateLimitRetryAfterHonored(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(429)
	})
	err := c.getJSON(t.Context(), c.BaseURL+"/x", &map[string]any{})
	var rle *RateLimitError
	if !asRateLimit(err, &rle) {
		t.Fatalf("expected RateLimitError, got %v", err)
	}
	if rle.RetryAfter != 120_000_000_000 {
		t.Fatalf("Retry-After not honored: %s", rle.RetryAfter)
	}
	rl := c.RateLimit()
	if rl.RetryAfter != 120_000_000_000 {
		t.Fatalf("RateLimit snapshot missing retry-after: %+v", rl)
	}
}

func asRateLimit(err error, dst **RateLimitError) bool {
	if err == nil {
		return false
	}
	rle, ok := err.(*RateLimitError)
	if ok {
		*dst = rle
	}
	return ok
}

func TestNotFoundAndOfflineProduceTypedErrors(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	if _, err := c.LatestStable(t.Context(), "nope", "gone"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	// Offline: unreachable port on loopback.
	c2 := NewClient()
	c2.BaseURL = "http://127.0.0.1:1"
	c2.HTTP = &http.Client{Transport: policyTransport(true)}
	if _, err := c2.LatestStable(t.Context(), "a", "b"); err == nil {
		t.Fatal("offline produced no error")
	} else if !strings.Contains(err.Error(), "network") {
		t.Fatalf("offline error not typed as network: %v", err)
	}
}

func TestResolvePinnedPolicyRejectsPrerelease(t *testing.T) {
	req := &Requirement{
		ID: "x", Owner: "a", Repo: "b", ReleasePolicy: ReleasePinnedTag, PinnedTag: "v1",
	}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, stubRelease("v1", true, false))
	})
	if _, err := c.Resolve(t.Context(), req, "", false); err == nil {
		t.Fatal("pinned prerelease accepted without opt-in")
	}
	if _, err := c.Resolve(t.Context(), req, "", true); err != nil {
		t.Fatalf("pinned prerelease with opt-in failed: %v", err)
	}
}
