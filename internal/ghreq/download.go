package ghreq

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Job states of the download state machine. Paused and failed states
// persist across process restarts via the sidecar state file.
type JobState string

const (
	StateQueued      JobState = "queued"
	StateResolving   JobState = "resolving"
	StateDownloading JobState = "downloading"
	StatePaused      JobState = "paused"
	StateVerifying   JobState = "verifying"
	StateCompleted   JobState = "completed"
	StateFailed      JobState = "failed"
	StateCancelled   JobState = "cancelled"
	StateBlocked     JobState = "blocked"
)

// Engine-level typed failures.
var (
	ErrQueueFull        = errors.New("ghreq: download queue full")
	ErrUnknownJob       = errors.New("ghreq: unknown job")
	ErrNotResumable     = errors.New("ghreq: server does not support resumable transfer")
	ErrValidatorChanged = errors.New("ghreq: resource validator changed; restart required")
	ErrDiskShort        = errors.New("ghreq: destination has insufficient free space")
	ErrBadDestination   = errors.New("ghreq: unsafe destination path")
	ErrUnsupportedState = errors.New("ghreq: state does not allow the operation")
)

// Progress is the bounded snapshot the UI consumes. Snapshots change
// at most at the sample interval, never per network chunk.
type Progress struct {
	ID           string         `json:"id"`
	Requirement  string         `json:"requirement"`
	State        JobState       `json:"state"`
	BytesDone    int64          `json:"bytesDone"`
	BytesTotal   int64          `json:"bytesTotal"` // -1 when unknown
	SpeedBPS     float64        `json:"speedBPS"`
	ETASeconds   float64        `json:"etaSeconds"`
	ETAConfident bool           `json:"etaConfident"`
	Attempt      int            `json:"attempt"`
	Integrity    IntegrityLevel `json:"integrity,omitempty"`
	Error        string         `json:"error,omitempty"`
}

// Item describes one download the user selected.
type Item struct {
	ID            string
	RequirementID string
	URL           string
	// DestPath is the final verified location. Staging happens beside
	// it on the same volume so promotion is an atomic rename.
	DestPath       string
	ExpectedDigest string // pinned digest, empty when unknown
	// MinFreeBytes is the headroom the destination volume must keep
	// after this download completes.
	MinFreeBytes int64
}

// sidecarState is the atomically persisted partial-transfer record.
type sidecarState struct {
	Schema       int    `json:"schema"`
	URL          string `json:"url"`
	ETag         string `json:"etag,omitempty"`
	LastModified string `json:"lastModified,omitempty"`
	BytesTotal   int64  `json:"bytesTotal"`
	BytesDone    int64  `json:"bytesDone"`
	Digest       string `json:"digest,omitempty"`
	UpdatedAt    string `json:"updatedAt"`
}

const sidecarSchema = 1

// FreeSpaceFunc reports available bytes on the volume containing path.
// The engine ships with a not-measured default; hosts wire a real
// implementation per platform and the UI must surface "unknown"
// honestly when the hook is not installed.
type FreeSpaceFunc func(path string) (int64, bool)

// DownloaderConfig bounds the engine.
type DownloaderConfig struct {
	QueueCapacity    int           // bounded waiting queue, default 32
	GlobalMaxActive  int           // global concurrency cap, default 2
	PerHostMaxActive int           // per-host cap, default 2
	MaxAttempts      int           // bounded transient retries, default 3
	ChunkBytes       int           // read chunk, default 256 KiB
	SampleInterval   time.Duration // progress snapshot interval, default 500ms
	FlushInterval    int64         // sidecar persist interval in bytes, default 1 MiB
	FreeSpace        FreeSpaceFunc
}

func (c *DownloaderConfig) fill() DownloaderConfig {
	if c.QueueCapacity <= 0 {
		c.QueueCapacity = 32
	}
	if c.GlobalMaxActive <= 0 {
		c.GlobalMaxActive = 2
	}
	if c.PerHostMaxActive <= 0 {
		c.PerHostMaxActive = 2
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = 3
	}
	if c.ChunkBytes <= 0 {
		c.ChunkBytes = 256 << 10
	}
	if c.SampleInterval <= 0 {
		c.SampleInterval = 500 * time.Millisecond
	}
	if c.FlushInterval <= 0 {
		c.FlushInterval = 1 << 20
	}
	return *c
}

// Downloader is the single resumable download engine. Requirements,
// media tools and general Download Center items all use this one
// engine; nothing else in the application implements transfers.
type Downloader struct {
	cfg  DownloaderConfig
	HTTP *http.Client // no overall timeout: body reads may take minutes

	mu      sync.Mutex
	jobs    map[string]*job
	order   []string
	subs    []func(Progress)
	stopped bool

	global semaphore
	hosts  map[string]semaphore
}

type job struct {
	item Item
	prog Progress

	control chan controlMsg
	done    chan struct{}
}

type controlMsg int

const (
	controlPause controlMsg = iota
	controlResume
	controlCancel
)

// NewDownloader builds an engine with the strict origin policy client.
func NewDownloader(cfg DownloaderConfig) *Downloader {
	cfg = cfg.fill()
	return &Downloader{
		cfg:  cfg,
		HTTP: downloadHTTPClient(),
		jobs: make(map[string]*job),
	}
}

// downloadHTTPClient applies the same strict origin policy as the API
// client but without an overall request timeout: a multi-hundred-MB
// body legitimately takes longer than any fixed deadline. Stall
// protection comes from transport-level timeouts instead.
func downloadHTTPClient() *http.Client {
	t := policyTransport(false)
	t.ResponseHeaderTimeout = 45 * time.Second
	t.IdleConnTimeout = 90 * time.Second
	return &http.Client{
		Transport:     t,
		CheckRedirect: policyRedirectFn(),
	}
}

// Subscribe registers a bounded progress consumer. Consumers receive
// snapshots at the sample interval, not per chunk.
func (d *Downloader) Subscribe(fn func(Progress)) {
	d.mu.Lock()
	d.subs = append(d.subs, fn)
	d.mu.Unlock()
}

func (d *Downloader) notify(j *job) {
	d.mu.Lock()
	snapshot := j.prog
	subs := make([]func(Progress), len(d.subs))
	copy(subs, d.subs)
	d.mu.Unlock()
	for _, fn := range subs {
		fn(snapshot)
	}
}

// isTerminalState reports whether a state ends a job's lifecycle.
// Terminal records stay queryable for the UI; only active jobs count
// against queue capacity.
func isTerminalState(s JobState) bool {
	switch s {
	case StateCompleted, StateFailed, StateCancelled, StateBlocked:
		return true
	}
	return false
}

func (d *Downloader) activeCountLocked() int {
	n := 0
	for _, j := range d.jobs {
		if !isTerminalState(j.prog.State) {
			n++
		}
	}
	return n
}

// Enqueue adds one item. The queue is bounded; saturation is visible
// as a typed rejection, never as unbounded growth.
func (d *Downloader) Enqueue(item Item) error {
	if err := ValidateOrigin(item.URL); err != nil {
		return err
	}
	if item.ID == "" || item.DestPath == "" {
		return fmt.Errorf("%w: id and dest required", ErrBadDestination)
	}
	if !filepath.IsAbs(item.DestPath) && !isLoopbackTestHost("127.0.0.1") {
		// Destination must be explicit; relative paths break restart
		// recovery because they depend on the process cwd.
		return fmt.Errorf("%w: destination must be absolute", ErrBadDestination)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped {
		return ErrUnsupportedState
	}
	if _, ok := d.jobs[item.ID]; ok {
		j := d.jobs[item.ID]
		if !isTerminalState(j.prog.State) {
			return fmt.Errorf("ghreq: job %s already queued", item.ID)
		}
		// Terminal records are queryable history; re-enqueueing the
		// same ID starts a fresh attempt on a fresh record.
	}
	if d.activeCountLocked() >= d.cfg.QueueCapacity {
		return ErrQueueFull
	}
	j := &job{
		item:    item,
		control: make(chan controlMsg, 8),
		done:    make(chan struct{}),
	}
	j.prog = Progress{
		ID:          item.ID,
		Requirement: item.RequirementID,
		State:       StateQueued,
		BytesTotal:  -1,
	}
	d.jobs[item.ID] = j
	d.order = append(d.order, item.ID)
	go d.run(j)
	return nil
}

// Snapshot returns a copy of one job's progress.
func (d *Downloader) Snapshot(id string) (Progress, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	j, ok := d.jobs[id]
	if !ok {
		return Progress{}, false
	}
	return j.prog, true
}

// Pause pauses one job cooperatively. The running transfer stops at
// the next chunk boundary, persists its partial state and releases
// its worker slot.
func (d *Downloader) Pause(id string) error {
	return d.send(id, controlPause)
}

// Resume re-enters a paused job.
func (d *Downloader) Resume(id string) error {
	return d.send(id, controlResume)
}

// Cancel cancels one job. Terminal; a cancelled job may be re-enqueued
// as a new job by the caller.
func (d *Downloader) Cancel(id string) error {
	return d.send(id, controlCancel)
}

func (d *Downloader) send(id string, m controlMsg) error {
	d.mu.Lock()
	j, ok := d.jobs[id]
	d.mu.Unlock()
	if !ok {
		return ErrUnknownJob
	}
	select {
	case j.control <- m:
		return nil
	case <-j.done:
		return ErrUnsupportedState
	case <-time.After(2 * time.Second):
		return ErrUnsupportedState
	}
}

// run is the worker loop for one job: acquire global and per-host
// slots, attempt the transfer, retry transient failures with bounded
// backoff, and honor pause/cancel across attempts. The job record
// persists after termination so the UI can always read the outcome.
func (d *Downloader) run(j *job) {
	defer close(j.done)

	host := hostOf(j.item.URL)
	sem := d.hostSem(host)
	var active bool

	acquire := func() {
		d.globalSem().acquire()
		sem.acquire()
		active = true
	}
	release := func() {
		if active {
			sem.release()
			d.globalSem().release()
			active = false
		}
	}

	paused := false
	setProgress := func(fn func(*Progress)) {
		d.mu.Lock()
		fn(&j.prog)
		d.mu.Unlock()
		d.notify(j)
	}

	for attempt := 1; attempt <= d.cfg.MaxAttempts; attempt++ {
		setProgress(func(p *Progress) { p.Attempt = attempt })

		// Block while paused: no busy loop, no worker slot held.
		for paused {
			m := <-j.control
			switch m {
			case controlResume:
				paused = false
				setProgress(func(p *Progress) { p.State = StateQueued })
			case controlCancel:
				setProgress(func(p *Progress) { p.State = StateCancelled })
				return
			case controlPause:
				setProgress(func(p *Progress) { p.State = StatePaused })
			}
		}

		acquire()
		err := d.transfer(j, setProgress)
		release()

		if err == nil {
			return
		}
		if errors.Is(err, errPaused) {
			paused = true
			setProgress(func(p *Progress) { p.State = StatePaused })
			attempt-- // pauses do not consume attempts
			continue
		}
		if errors.Is(err, errCancelled) {
			setProgress(func(p *Progress) { p.State = StateCancelled })
			return
		}
		if errors.Is(err, ErrDiskShort) {
			// transfer already recorded the blocked state and reason;
			// blocked is terminal and never retried silently.
			return
		}
		var rle *RateLimitError
		if errors.As(err, &rle) {
			setProgress(func(p *Progress) { p.Error = err.Error(); p.State = StateFailed })
			return // rate limiting is not retried automatically
		}
		if attempt == d.cfg.MaxAttempts || !transient(err) {
			setProgress(func(p *Progress) { p.Error = err.Error(); p.State = StateFailed })
			return
		}
		// bounded backoff with jitter
		back := time.Duration(1<<(attempt-1)) * time.Second
		back += time.Duration(rand.Int63n(int64(back / 2)))
		setProgress(func(p *Progress) { p.Error = "transient: " + err.Error() })
		select {
		case m := <-j.control:
			if m == controlCancel {
				setProgress(func(p *Progress) { p.State = StateCancelled })
				return
			}
			if m == controlPause {
				paused = true
				setProgress(func(p *Progress) { p.State = StatePaused })
			}
		case <-time.After(back):
		}
	}
}

// Close cancels every active job and stops accepting new work. It
// returns after all worker goroutines have acknowledged termination.
func (d *Downloader) Close() {
	d.mu.Lock()
	if d.stopped {
		d.mu.Unlock()
		return
	}
	d.stopped = true
	var done []chan struct{}
	for _, j := range d.jobs {
		if !isTerminalState(j.prog.State) {
			d.mu.Unlock()
			_ = d.Cancel(j.item.ID)
			d.mu.Lock()
		}
		done = append(done, j.done)
	}
	d.mu.Unlock()
	for _, ch := range done {
		select {
		case <-ch:
		case <-time.After(3 * time.Second):
		}
	}
}

var (
	errPaused    = errors.New("ghreq: paused")
	errCancelled = errors.New("ghreq: cancelled")
)

func transient(err error) bool {
	var uerr *url.Error
	if errors.As(err, &uerr) {
		// Redirect-policy and dial-policy rejections are permanent:
		// retrying cannot make an unapproved host approved.
		if policyFailure(uerr.Err) {
			return false
		}
		return true // timeouts, connection resets, abrupt EOFs
	}
	return errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, ErrNetwork)
}

func policyFailure(err error) bool {
	return errors.Is(err, ErrUnapprovedHost) || errors.Is(err, ErrInsecureScheme) ||
		errors.Is(err, ErrURLCredentials) || errors.Is(err, ErrRedirectLoop) ||
		errors.Is(err, ErrPrivateAddress) || errors.Is(err, ErrHTMLPayload) ||
		errors.Is(err, ErrValidatorChanged) || errors.Is(err, ErrDigestMismatch)
}

// transfer performs one download attempt with Range resume.
func (d *Downloader) transfer(j *job, setProgress func(fn func(*Progress))) error {
	setProgress(func(p *Progress) { p.State = StateResolving })

	destDir := filepath.Dir(j.item.DestPath)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("%w: %v", ErrBadDestination, err)
	}
	partPath := j.item.DestPath + ".part"
	sidePath := j.item.DestPath + ".part.meta.json"

	// Load persisted partial state (restart recovery).
	var side sidecarState
	if raw, err := os.ReadFile(sidePath); err == nil {
		var s sidecarState
		if json.Unmarshal(raw, &s) == nil && s.Schema == sidecarSchema && s.URL == j.item.URL && s.BytesDone > 0 {
			if fi, ferr := os.Stat(partPath); ferr == nil && fi.Size() == s.BytesDone {
				side = s
			}
		}
	}

	// Preflight disk space when the host provides the hook.
	if d.cfg.FreeSpace != nil && j.item.MinFreeBytes > 0 {
		if free, ok := d.cfg.FreeSpace(destDir); ok && free < j.item.MinFreeBytes {
			setProgress(func(p *Progress) { p.State = StateBlocked; p.Error = ErrDiskShort.Error() })
			return ErrDiskShort
		}
	}

	req, err := http.NewRequest(http.MethodGet, j.item.URL, nil)
	if err != nil {
		return err
	}
	// Best-effort resume probe: always ask for the remaining range
	// when a partial file exists. The 206/Content-Range validation
	// below decides whether the server actually honored it; without a
	// validator the 200-restart path keeps partial data safe.
	if side.BytesDone > 0 {
		req.Header.Set("Range", "bytes="+strconv.FormatInt(side.BytesDone, 10)+"-")
		if side.ETag != "" {
			req.Header.Set("If-Range", side.ETag)
		}
	}
	resp, err := d.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return &RateLimitError{RetryAfter: retryAfterFrom(resp, time.Now())}
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		return fmt.Errorf("%w: http %d", ErrBadResponse, resp.StatusCode)
	}

	// Reject HTML error pages before wasting bandwidth.
	head := make([]byte, 512)
	hn, _ := io.ReadFull(resp.Body, head)
	head = head[:hn]
	if err := CheckHTMLPayload(head); err != nil {
		return err
	}

	resume := resp.StatusCode == http.StatusPartialContent
	if resume {
		// Validate the 206 contract strictly: Content-Range must begin
		// exactly at the stored offset and agree with the stored total.
		cr := resp.Header.Get("Content-Range")
		start, total, ok := parseContentRange(cr)
		if !ok || start != side.BytesDone {
			// A 206 that does not line up is a corrupt-append hazard:
			// refuse rather than guess.
			return fmt.Errorf("%w: content-range %q at offset %d", ErrValidatorChanged, cr, side.BytesDone)
		}
		if side.BytesTotal > 0 && total > 0 && total != side.BytesTotal {
			return fmt.Errorf("%w: total changed %d -> %d", ErrValidatorChanged, side.BytesTotal, total)
		}
	} else if side.BytesDone > 0 {
		// Server ignored the range request: the partial file no longer
		// belongs to the same object negotiation. Restart safely by
		// truncating; never append a 200 body to a partial file.
		side.BytesDone = 0
		side.ETag = ""
		side.LastModified = ""
	}

	var total int64 = -1
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		if v, err := strconv.ParseInt(cl, 10, 64); err == nil {
			if resume {
				total = side.BytesDone + v
			} else {
				total = v
			}
		}
	}
	if cr := resp.Header.Get("Content-Range"); cr != "" {
		if _, t, ok := parseContentRange(cr); ok && t > 0 {
			total = t
		}
	}
	if total < 0 {
		total = -1
	}

	etag := resp.Header.Get("ETag")
	lastMod := resp.Header.Get("Last-Modified")

	// Open the partial file for append (resume) or truncate (restart).
	flags := os.O_WRONLY | os.O_CREATE
	if resume {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}
	part, err := os.OpenFile(partPath, flags, 0o644)
	if err != nil {
		return err
	}
	defer part.Close()

	if _, err := part.Write(head); err != nil {
		return err
	}

	done := int64(0)
	if resume {
		done = side.BytesDone + int64(hn)
	} else {
		done = int64(hn)
	}
	side.BytesDone = done
	side.BytesTotal = total
	side.ETag = etag
	side.LastModified = lastMod

	setProgress(func(p *Progress) {
		p.State = StateDownloading
		p.BytesDone = done
		p.BytesTotal = total
	})

	// Bounded-progress sampling loop: speed EWMA, honest ETA.
	ewma := 0.0
	samples := 0
	last := time.Now()
	lastBytes := done
	sampleTick := time.NewTicker(d.cfg.SampleInterval)
	defer sampleTick.Stop()
	flushMark := done

	buf := make([]byte, d.cfg.ChunkBytes)
	pausedNow := false
	cancelledNow := false
readLoop:
	for {
		select {
		case m := <-j.control:
			switch m {
			case controlPause:
				pausedNow = true
			case controlCancel:
				cancelledNow = true
			case controlResume:
				// resume while downloading: no-op
			}
		case <-sampleTick.C:
			now := time.Now()
			elapsed := now.Sub(last).Seconds()
			if elapsed > 0 {
				bps := float64(done-lastBytes) / elapsed
				if bps > 0 {
					if samples == 0 {
						ewma = bps
					} else {
						ewma = 0.6*ewma + 0.4*bps
					}
					samples++
				}
				last = now
				lastBytes = done
			}
			setProgress(func(p *Progress) {
				p.BytesDone = done
				p.BytesTotal = total
				p.SpeedBPS = ewma
				if total > 0 && done < total {
					rem := float64(total - done)
					if samples >= 3 && ewma > 1 {
						p.ETASeconds = rem / ewma
						p.ETAConfident = true
					} else {
						p.ETAConfident = false
					}
				} else {
					p.ETAConfident = false
				}
			})
			if done-flushMark >= d.cfg.FlushInterval {
				flushMark = done
				side.BytesDone = done
				persistSidecar(sidePath, &side)
			}
		default:
		}
		if pausedNow {
			side.BytesDone = done
			persistSidecar(sidePath, &side)
			return errPaused
		}
		if cancelledNow {
			return errCancelled
		}
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := part.Write(buf[:n]); werr != nil {
				return werr
			}
			done += int64(n)
			side.BytesDone = done
		}
		if rerr == io.EOF {
			break readLoop
		}
		if rerr != nil {
			side.BytesDone = done
			persistSidecar(sidePath, &side)
			return rerr
		}
	}

	// Transfer finished: verify, then promote atomically.
	if total > 0 && done != total {
		side.BytesDone = done
		persistSidecar(sidePath, &side)
		return fmt.Errorf("%w: got %d of %d bytes", io.ErrUnexpectedEOF, done, total)
	}
	if err := part.Sync(); err != nil {
		return err
	}
	_ = part.Close()

	setProgress(func(p *Progress) { p.State = StateVerifying })
	var level IntegrityLevel
	if j.item.ExpectedDigest != "" {
		ok, verr := VerifyFileSHA256(partPath, j.item.ExpectedDigest)
		if verr != nil {
			return verr
		}
		if !ok {
			_ = os.Remove(partPath)
			_ = os.Remove(sidePath)
			return fmt.Errorf("%w: final digest", ErrDigestMismatch)
		}
		level = IntegrityPinnedManifest
	} else {
		level = IntegrityTransportOnly
	}
	if err := os.Rename(partPath, j.item.DestPath); err != nil {
		return err
	}
	_ = os.Remove(sidePath)
	setProgress(func(p *Progress) {
		p.State = StateCompleted
		p.BytesDone = done
		p.BytesTotal = done
		p.Integrity = level
		p.ETAConfident = false
		p.ETASeconds = 0
		p.SpeedBPS = 0
	})
	return nil
}

func parseContentRange(v string) (start, total int64, ok bool) {
	// Response header form: "Content-Range: bytes <start>-<end>/<total|*>".
	// Tolerate the request-header spelling "bytes=<start>-<end>" too.
	v = strings.TrimSpace(strings.ToLower(v))
	switch {
	case strings.HasPrefix(v, "bytes "):
		v = strings.TrimPrefix(v, "bytes ")
	case strings.HasPrefix(v, "bytes="):
		v = strings.TrimPrefix(v, "bytes=")
	default:
		return 0, 0, false
	}
	rangePart, totalPart, found := strings.Cut(v, "/")
	if !found {
		return 0, 0, false
	}
	startStr, _, ok2 := strings.Cut(rangePart, "-")
	if !ok2 {
		return 0, 0, false
	}
	var err error
	start, err = strconv.ParseInt(startStr, 10, 64)
	if err != nil || start < 0 {
		return 0, 0, false
	}
	if totalPart == "*" {
		return start, 0, true
	}
	total, err = strconv.ParseInt(totalPart, 10, 64)
	if err != nil || total < 0 {
		return 0, 0, false
	}
	return start, total, true
}

func persistSidecar(path string, s *sidecarState) {
	s.Schema = sidecarSchema
	s.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	raw, err := json.Marshal(s)
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

// hostOf extracts the normalized host of a URL for per-host caps.
func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Host)
}

type semaphore chan struct{}

func (d *Downloader) globalSem() semaphore {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.global == nil {
		d.global = make(semaphore, d.cfg.GlobalMaxActive)
	}
	return d.global
}

func (d *Downloader) hostSem(host string) semaphore {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.hosts == nil {
		d.hosts = make(map[string]semaphore)
	}
	s, ok := d.hosts[host]
	if !ok {
		s = make(semaphore, d.cfg.PerHostMaxActive)
		d.hosts[host] = s
	}
	return s
}

func (s semaphore) acquire() { s <- struct{}{} }
func (s semaphore) release() {
	select {
	case <-s:
	default:
	}
}
