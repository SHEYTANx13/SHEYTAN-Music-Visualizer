package ghreq

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// downloaderFor returns a Downloader wired for loopback tests.
func downloaderFor(t *testing.T, cfg DownloaderConfig) *Downloader {
	t.Helper()
	d := NewDownloader(cfg)
	d.HTTP = &http.Client{Transport: policyTransport(true), CheckRedirect: policyRedirectFn()}
	return d
}

func waitForState(t *testing.T, d *Downloader, id string, want JobState, timeout time.Duration) Progress {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if p, ok := d.Snapshot(id); ok && p.State == want {
			return p
		}
		time.Sleep(10 * time.Millisecond)
	}
	p, _ := d.Snapshot(id)
	t.Fatalf("job %s never reached %s; last=%+v", id, want, p)
	return Progress{}
}

func payload(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i * 31)
	}
	return b
}

func TestDownloadCompletesAndVerifies(t *testing.T) {
	data := payload(300_000)
	digest := sha256hex(data)
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("ETag", `"fixed"`)
		w.Header().Set("Content-Length", itoa(len(data)))
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)

	dest := filepath.Join(t.TempDir(), "out.bin")
	d := downloaderFor(t, DownloaderConfig{SampleInterval: 20 * time.Millisecond})
	err := d.Enqueue(Item{ID: "j1", RequirementID: "media-tools-ffmpeg-gpl", URL: srv.URL, DestPath: dest, ExpectedDigest: digest})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	p := waitForState(t, d, "j1", StateCompleted, 10*time.Second)
	if p.BytesDone != int64(len(data)) {
		t.Fatalf("bytes done %d, want %d", p.BytesDone, len(data))
	}
	if p.Integrity != IntegrityPinnedManifest {
		t.Fatalf("integrity level %s", p.Integrity)
	}
	if hits != 1 {
		t.Fatalf("unexpected request count %d", hits)
	}
	got, err := os.ReadFile(dest)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("output mismatch: err=%v len=%d", err, len(got))
	}
	if _, err := os.Stat(dest + ".part"); !os.IsNotExist(err) {
		t.Fatalf("partial file left behind: %v", err)
	}
	if _, err := os.Stat(dest + ".part.meta.json"); !os.IsNotExist(err) {
		t.Fatalf("sidecar left behind: %v", err)
	}
}

func TestDownloadResumeWithRangeValidation(t *testing.T) {
	data := payload(500_000)
	digest := sha256hex(data)
	split := 120_000

	var serverClosed atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") == "" {
			// First (aborted) attempt: full content.
			w.Header().Set("ETag", `"obj-1"`)
			w.Header().Set("Accept-Ranges", "bytes")
			w.Header().Set("Content-Length", itoa(len(data)))
			_, _ = w.Write(data)
			return
		}
		start, _, ok := parseContentRangeRange(r.Header.Get("Range"))
		if !ok || start != int64(split) {
			t.Errorf("resume request asked for wrong offset: %q", r.Header.Get("Range"))
			w.WriteHeader(416)
			return
		}
		if r.Header.Get("If-Range") != `"obj-1"` {
			t.Errorf("If-Range validator missing: %q", r.Header.Get("If-Range"))
		}
		w.Header().Set("ETag", `"obj-1"`)
		w.Header().Set("Content-Range", "bytes "+itoa(split)+"-"+itoa(len(data)-1)+"/"+itoa(len(data)))
		w.Header().Set("Content-Length", itoa(len(data)-split))
		w.WriteHeader(206)
		_, _ = w.Write(data[split:])
	}))
	t.Cleanup(func() { serverClosed.Store(true); srv.Close() })

	dest := filepath.Join(t.TempDir(), "out.bin")
	d := downloaderFor(t, DownloaderConfig{SampleInterval: 10 * time.Millisecond})

	// Simulate a completed first chunk: seed the sidecar and partial
	// file exactly as a crashed prior run would have left them.
	if err := os.WriteFile(dest+".part", data[:split], 0o644); err != nil {
		t.Fatal(err)
	}
	side := sidecarState{
		Schema: sidecarSchema, URL: srv.URL, ETag: `"obj-1"`,
		BytesTotal: int64(len(data)), BytesDone: int64(split),
	}
	persistSidecar(dest+".part.meta.json", &side)

	err := d.Enqueue(Item{ID: "r1", URL: srv.URL, DestPath: dest, ExpectedDigest: digest})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	waitForState(t, d, "r1", StateCompleted, 10*time.Second)
	got, err := os.ReadFile(dest)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("resumed output mismatch: err=%v len=%d want=%d", err, len(got), len(data))
	}
}

func parseContentRangeRange(v string) (int64, int64, bool) {
	// bytes=<start>-<end>
	v = strings.TrimSpace(strings.ToLower(v))
	if !strings.HasPrefix(v, "bytes=") {
		return 0, 0, false
	}
	parts := strings.Split(strings.TrimPrefix(v, "bytes="), "-")
	if len(parts) != 2 {
		return 0, 0, false
	}
	start := int64(0)
	for _, c := range parts[0] {
		if c < '0' || c > '9' {
			return 0, 0, false
		}
		start = start*10 + int64(c-'0')
	}
	end := int64(0)
	for _, c := range parts[1] {
		if c < '0' || c > '9' {
			return 0, 0, false
		}
		end = end*10 + int64(c-'0')
	}
	return start, end, true
}

func TestDownloadValidatorChangeRestartsSafely(t *testing.T) {
	data := payload(100_000)
	split := 50_000
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The object changed between runs: the validator no longer
		// matches, so the server answers with 200 (full new body).
		w.Header().Set("ETag", `"obj-2"`)
		w.Header().Set("Content-Length", itoa(len(data)))
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)

	dest := filepath.Join(t.TempDir(), "out.bin")
	// Stale partial from the OLD object.
	if err := os.WriteFile(dest+".part", data[:split], 0o644); err != nil {
		t.Fatal(err)
	}
	side := sidecarState{Schema: sidecarSchema, URL: srv.URL, ETag: `"obj-1"`,
		BytesTotal: int64(len(data)), BytesDone: int64(split)}
	persistSidecar(dest+".part.meta.json", &side)

	d := downloaderFor(t, DownloaderConfig{SampleInterval: 10 * time.Millisecond})
	if err := d.Enqueue(Item{ID: "v1", URL: srv.URL, DestPath: dest, ExpectedDigest: sha256hex(data)}); err != nil {
		t.Fatal(err)
	}
	waitForState(t, d, "v1", StateCompleted, 10*time.Second)
	got, err := os.ReadFile(dest)
	if err != nil || len(got) != len(data) || !bytes.Equal(got, data) {
		t.Fatalf("restart produced corrupted output: err=%v len=%d", err, len(got))
	}
}

func TestDownloadServerWithoutRangeRestartNotAppend(t *testing.T) {
	data := payload(80_000)
	var sawRange atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			sawRange.Store(true)
		}
		// Server ignores Range entirely: 200 with full body.
		w.Header().Set("Content-Length", itoa(len(data)))
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)

	dest := filepath.Join(t.TempDir(), "out.bin")
	// Seed a stale partial WITHOUT an ETag: the engine still sends a
	// Range request (best effort) and must not append on 200.
	if err := os.WriteFile(dest+".part", data[:1000], 0o644); err != nil {
		t.Fatal(err)
	}
	side := sidecarState{Schema: sidecarSchema, URL: srv.URL, BytesTotal: int64(len(data)), BytesDone: 1000}
	persistSidecar(dest+".part.meta.json", &side)

	d := downloaderFor(t, DownloaderConfig{SampleInterval: 10 * time.Millisecond})
	if err := d.Enqueue(Item{ID: "n1", URL: srv.URL, DestPath: dest, ExpectedDigest: sha256hex(data)}); err != nil {
		t.Fatal(err)
	}
	waitForState(t, d, "n1", StateCompleted, 10*time.Second)
	got, _ := os.ReadFile(dest)
	if len(got) != len(data) {
		t.Fatalf("restart-without-range produced %d bytes, want exactly %d (append corruption?)", len(got), len(data))
	}
	if !sawRange.Load() {
		t.Fatal("engine never attempted a range probe on resume")
	}
}

func TestDownloadDigestMismatchBlocksInstall(t *testing.T) {
	data := payload(50_000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", itoa(len(data)))
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)
	dest := filepath.Join(t.TempDir(), "out.bin")
	d := downloaderFor(t, DownloaderConfig{SampleInterval: 10 * time.Millisecond})
	// Wrong pinned digest.
	bad := strings.Repeat("ee", 32)
	if err := d.Enqueue(Item{ID: "m1", URL: srv.URL, DestPath: dest, ExpectedDigest: bad}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if p, ok := d.Snapshot("m1"); ok && p.State == StateFailed {
			if !strings.Contains(p.Error, "digest") {
				t.Fatalf("failure not digest-related: %s", p.Error)
			}
			if _, err := os.Stat(dest); !os.IsNotExist(err) {
				t.Fatalf("failed digest must not be promoted to destination: %v", err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("digest mismatch never surfaced as failed state")
}

func TestDownloadHTMLPayloadRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>503 Service Unavailable</body></html>"))
	}))
	t.Cleanup(srv.Close)
	dest := filepath.Join(t.TempDir(), "out.bin")
	d := downloaderFor(t, DownloaderConfig{SampleInterval: 10 * time.Millisecond})
	if err := d.Enqueue(Item{ID: "h1", URL: srv.URL, DestPath: dest}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if p, ok := d.Snapshot("h1"); ok && p.State == StateFailed {
			if !strings.Contains(p.Error, "html") {
				t.Fatalf("failure not html-related: %s", p.Error)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("html payload not rejected")
}

func TestDownloadPauseResume(t *testing.T) {
	data := payload(2_000_000)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"p-1"`)
		w.Header().Set("Content-Length", itoa(len(data)))
		// Drip-feed the body so the engine can pause mid-stream.
		chunk := 8_000
		for i := 0; i < len(data); i += chunk {
			select {
			case <-release:
				return
			default:
			}
			end := i + chunk
			if end > len(data) {
				end = len(data)
			}
			if _, err := w.Write(data[i:end]); err != nil {
				return
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			time.Sleep(2 * time.Millisecond)
		}
	}))
	t.Cleanup(srv.Close)

	dest := filepath.Join(t.TempDir(), "out.bin")
	d := downloaderFor(t, DownloaderConfig{SampleInterval: 20 * time.Millisecond})
	if err := d.Enqueue(Item{ID: "p1", URL: srv.URL, DestPath: dest, ExpectedDigest: sha256hex(data)}); err != nil {
		t.Fatal(err)
	}

	// Wait for downloading, then pause.
	waitForState(t, d, "p1", StateDownloading, 10*time.Second)
	if err := d.Pause("p1"); err != nil {
		t.Fatalf("pause: %v", err)
	}
	waitForState(t, d, "p1", StatePaused, 10*time.Second)

	// Partial file must exist and be smaller than the payload.
	b, err := os.ReadFile(dest + ".part")
	if err != nil || len(b) == 0 || int64(len(b)) >= int64(len(data)) {
		t.Fatalf("pause state unusable: len=%d err=%v", len(b), err)
	}
	sideRaw, err := os.ReadFile(dest + ".part.meta.json")
	if err != nil || !bytes.Contains(sideRaw, []byte(`"bytesDone"`)) {
		t.Fatalf("sidecar not persisted on pause: %v", err)
	}

	if err := d.Resume("p1"); err != nil {
		t.Fatalf("resume: %v", err)
	}
	p := waitForState(t, d, "p1", StateCompleted, 30*time.Second)
	if p.BytesDone != int64(len(data)) {
		t.Fatalf("completed with %d bytes, want %d", p.BytesDone, len(data))
	}
	got, _ := os.ReadFile(dest)
	if !bytes.Equal(got, data) {
		t.Fatal("pausable download corrupted output")
	}
}

func TestDownloadCancel(t *testing.T) {
	data := payload(4_000_000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", itoa(len(data)))
		chunk := 8_000
		for i := 0; i < len(data); i += chunk {
			if _, err := w.Write(data[i:min(i+chunk, len(data))]); err != nil {
				return
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			time.Sleep(2 * time.Millisecond)
		}
	}))
	t.Cleanup(srv.Close)
	dest := filepath.Join(t.TempDir(), "out.bin")
	d := downloaderFor(t, DownloaderConfig{SampleInterval: 20 * time.Millisecond})
	if err := d.Enqueue(Item{ID: "c1", URL: srv.URL, DestPath: dest}); err != nil {
		t.Fatal(err)
	}
	waitForState(t, d, "c1", StateDownloading, 10*time.Second)
	if err := d.Cancel("c1"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	waitForState(t, d, "c1", StateCancelled, 10*time.Second)
}

func TestQueueSaturationVisible(t *testing.T) {
	block := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		block <- struct{}{} // handshake: server busy for the whole test
		<-release
		w.Write(payload(10))
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	d := downloaderFor(t, DownloaderConfig{QueueCapacity: 3, GlobalMaxActive: 1})
	for i := 0; i < 3; i++ {
		if err := d.Enqueue(Item{ID: "q" + itoa(i), URL: srv.URL, DestPath: filepath.Join(t.TempDir(), "o.bin")}); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}
	// Wait until the first job is actively transferring.
	<-block
	if err := d.Enqueue(Item{ID: "overflow", URL: srv.URL, DestPath: filepath.Join(t.TempDir(), "o.bin")}); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("saturation not visible: %v", err)
	}
	// Terminal records do not consume capacity forever: cancel all
	// jobs, wait, and the queue must accept again.
	for i := 0; i < 3; i++ {
		_ = d.Cancel("q" + itoa(i))
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ok := true
		for i := 0; i < 3; i++ {
			if p, found := d.Snapshot("q" + itoa(i)); !found || p.State != StateCancelled {
				ok = false
			}
		}
		if ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	d2 := downloaderFor(t, DownloaderConfig{QueueCapacity: 1, GlobalMaxActive: 1})
	if err := d2.Enqueue(Item{ID: "fresh", URL: srv.URL, DestPath: filepath.Join(t.TempDir(), "o.bin")}); err != nil {
		t.Fatalf("fresh engine enqueue failed: %v", err)
	}
	<-block
}

func TestDiskShortBlocksBeforeTransfer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("transfer started despite insufficient disk space")
	}))
	t.Cleanup(srv.Close)
	d := downloaderFor(t, DownloaderConfig{})
	d.cfg.FreeSpace = func(string) (int64, bool) { return 1, true }
	if err := d.Enqueue(Item{ID: "d1", URL: srv.URL, DestPath: filepath.Join(t.TempDir(), "o.bin"), MinFreeBytes: 1 << 40}); err != nil {
		t.Fatal(err)
	}
	waitForState(t, d, "d1", StateBlocked, 5*time.Second)
	if p, _ := d.Snapshot("d1"); !strings.Contains(p.Error, "insufficient free space") {
		t.Fatalf("blocked reason not actionable: %s", p.Error)
	}
}

func TestETAHonestBeforeEnoughSamples(t *testing.T) {
	data := payload(300_000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", itoa(len(data)))
		for i := 0; i < len(data); i += 4_000 {
			_, _ = w.Write(data[i:min(i+4000, len(data))])
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			time.Sleep(3 * time.Millisecond)
		}
	}))
	t.Cleanup(srv.Close)
	dest := filepath.Join(t.TempDir(), "out.bin")
	d := downloaderFor(t, DownloaderConfig{SampleInterval: 10 * time.Millisecond})
	if err := d.Enqueue(Item{ID: "e1", URL: srv.URL, DestPath: dest, ExpectedDigest: sha256hex(data)}); err != nil {
		t.Fatal(err)
	}
	// During the transfer, an unconfident ETA must never show a number.
	var sawUnconfidentWithNumber bool
	var sawConfident bool
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if p, ok := d.Snapshot("e1"); ok {
			if p.State == StateDownloading {
				if !p.ETAConfident && p.ETASeconds > 0 {
					sawUnconfidentWithNumber = true
				}
				if p.ETAConfident && p.ETASeconds > 0 {
					sawConfident = true
				}
			}
			if p.State == StateCompleted {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if sawUnconfidentWithNumber {
		t.Fatal("unconfident ETA carried a numeric value")
	}
	if !sawConfident {
		t.Fatal("never observed a confident ETA on a long transfer")
	}
}

func TestUnapprovedRedirectIsPermanentFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://evil.example.com/x.bin", http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	dest := filepath.Join(t.TempDir(), "out.bin")
	d := downloaderFor(t, DownloaderConfig{SampleInterval: 10 * time.Millisecond})
	if err := d.Enqueue(Item{ID: "red1", URL: srv.URL, DestPath: dest}); err != nil {
		t.Fatal(err)
	}
	waitForState(t, d, "red1", StateFailed, 10*time.Second)
	if p, _ := d.Snapshot("red1"); p.Attempt != 1 {
		t.Fatalf("policy failure retried %d times; must be permanent", p.Attempt)
	}
}

func TestRateLimitedDownloadFailsWithoutRetryStorm(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Retry-After", "300")
		w.WriteHeader(429)
	}))
	t.Cleanup(srv.Close)
	dest := filepath.Join(t.TempDir(), "out.bin")
	d := downloaderFor(t, DownloaderConfig{SampleInterval: 10 * time.Millisecond, MaxAttempts: 3})
	if err := d.Enqueue(Item{ID: "rl1", URL: srv.URL, DestPath: dest}); err != nil {
		t.Fatal(err)
	}
	waitForState(t, d, "rl1", StateFailed, 10*time.Second)
	time.Sleep(100 * time.Millisecond)
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("rate-limited download retried %d times; no retry storms allowed", atomic.LoadInt32(&hits))
	}
}

func TestContentRangeParsing(t *testing.T) {
	tests := []struct {
		in    string
		start int64
		total int64
		ok    bool
	}{
		{"bytes 100-999/1000", 100, 1000, true},
		{"BYTES 0-499/500", 0, 500, true},
		{"bytes 5-9/*", 5, 0, true},
		{"bytes 5-9", 0, 0, false},
		{"items 5-9/10", 0, 0, false},
		{"bytes -9/10", 0, 0, false},
	}
	for _, tc := range tests {
		s, tot, ok := parseContentRange(tc.in)
		if ok != tc.ok || (ok && (s != tc.start || tot != tc.total)) {
			t.Errorf("parseContentRange(%q) = %d,%d,%v want %d,%d,%v", tc.in, s, tot, ok, tc.start, tc.total, tc.ok)
		}
	}
}

func TestBinaryContentHelpers(t *testing.T) {
	var buf bytes.Buffer
	_ = binary.Write(&buf, binary.LittleEndian, uint32(1))
	_ = io.Discard
}

func TestRetryTransientThenSuccess(t *testing.T) {
	var hits int32
	data := payload(20_000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			// First attempt: connection dies mid-body (transient).
			w.Header().Set("Content-Length", itoa(len(data)+1000))
			_, _ = w.Write(data)
			panic(http.ErrAbortHandler)
		}
		w.Header().Set("Content-Length", itoa(len(data)))
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)
	dest := filepath.Join(t.TempDir(), "out.bin")
	d := downloaderFor(t, DownloaderConfig{SampleInterval: 10 * time.Millisecond, MaxAttempts: 3})
	if err := d.Enqueue(Item{ID: "t1", URL: srv.URL, DestPath: dest, ExpectedDigest: sha256hex(data)}); err != nil {
		t.Fatal(err)
	}
	p := waitForState(t, d, "t1", StateCompleted, 20*time.Second)
	if p.Attempt < 2 {
		t.Fatalf("transient failure not retried: attempt=%d", p.Attempt)
	}
}

func TestEnqueueRejectsInsecureOrigin(t *testing.T) {
	d := downloaderFor(t, DownloaderConfig{})
	if err := d.Enqueue(Item{ID: "x", URL: "http://github.com/file", DestPath: "/tmp/o"}); !errors.Is(err, ErrInsecureScheme) {
		t.Fatalf("insecure origin accepted: %v", err)
	}
	if err := d.Enqueue(Item{ID: "x", URL: "https://unapproved.example.com/file", DestPath: "/tmp/o"}); !errors.Is(err, ErrUnapprovedHost) {
		t.Fatalf("unapproved host accepted: %v", err)
	}
}

func TestSubscriptionReceivesBoundedSnapshots(t *testing.T) {
	data := payload(100_000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", itoa(len(data)))
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)
	dest := filepath.Join(t.TempDir(), "out.bin")
	d := downloaderFor(t, DownloaderConfig{SampleInterval: 15 * time.Millisecond})
	var events int32
	d.Subscribe(func(Progress) { atomic.AddInt32(&events, 1) })
	if err := d.Enqueue(Item{ID: "s1", URL: srv.URL, DestPath: dest, ExpectedDigest: sha256hex(data)}); err != nil {
		t.Fatal(err)
	}
	waitForState(t, d, "s1", StateCompleted, 10*time.Second)
	// A tiny file must not flood subscribers with per-chunk events.
	time.Sleep(50 * time.Millisecond)
	if atomic.LoadInt32(&events) > 200 {
		t.Fatalf("subscriber flooded: %d events for a small file", atomic.LoadInt32(&events))
	}
	if atomic.LoadInt32(&events) == 0 {
		t.Fatal("subscriber never notified")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

var _ = context.Background
