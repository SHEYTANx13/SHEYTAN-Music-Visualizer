// Package perf — startup stage instrumentation for SHEYTAN Music
// Visualizer (run 42837709122 spec §5.1).
//
// It records monotonic elapsed durations from the earliest observable
// process boundary to each named startup stage and emits ONE structured
// log record at the end of startup (never polled, never per-frame).
//
// Honest anchor semantics:
//
//   - On Linux, ProcessStart reads /proc/self/stat field 22
//     (starttime in clock ticks since boot) and returns the true OS
//     process start instant.
//   - On Windows it reads GetProcessTimes().CreationTime (kernel truth).
//   - Anywhere else (and on any read failure) it falls back to the
//     package-variable initialization instant of the importing binary
//     (the earliest Go-side anchor) and says so in Source.
//
// The measured "cold start" therefore always names its own anchor —
// workflow build time is application startup time nowhere in this
// package.
package perf

import (
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Stage is one recorded stage: name, monotonic duration from the
// anchor, and the absolute wall-clock instant it was marked.
type Stage struct {
	Name     string        `json:"name"`
	Elapsed  time.Duration `json:"elapsed"`
	At       time.Time     `json:"at"`
	Anchor   string        `json:"anchor"`   // anchor provenance of the run
	Sequence int           `json:"sequence"` // mark order (0-based)
}

// goInitAnchor is the earliest instant THIS package can know about in
// every build: the initialization of this package variable in the
// importing process.
var goInitAnchor = time.Now()

// processStartOnce guards the one-shot OS process-start read.
var (
	processStartOnce sync.Once
	processStartVal  time.Time
	processStartSrc  string
)

// ProcessStart returns the best available process-start instant and
// its provenance ("os:/proc/self/stat", "os:GetProcessTimes", or
// "go:package-init fallback").
func ProcessStart() (time.Time, string) {
	processStartOnce.Do(func() {
		if t, src, ok := osProcessStart(); ok {
			processStartVal, processStartSrc = t, src
			return
		}
		processStartVal, processStartSrc = goInitAnchor, "go:package-init fallback"
	})
	return processStartVal, processStartSrc
}

// Recorder accumulates named stages for one startup run. Mark is safe
// for concurrent use; stages are numbered in mark order and reported
// sorted by sequence.
type Recorder struct {
	mu     sync.Mutex
	anchor time.Time
	source string
	stages []Stage
	closed bool
}

// NewRecorder creates a recorder anchored at the true process start
// (or the honest fallback) — NOT at first use, so late first marks
// still carry the full cold-start elapsed time.
func NewRecorder() *Recorder {
	t, src := ProcessStart()
	return &Recorder{anchor: t, source: src}
}

// Mark records a stage now. Re-marking a name records a second entry
// (sequence distinguishes them) — it never silently overwrites.
func (r *Recorder) Mark(name string) {
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	r.stages = append(r.stages, Stage{
		Name:     name,
		Elapsed:  now.Sub(r.anchor),
		At:       now.UTC(),
		Anchor:   r.source,
		Sequence: len(r.stages),
	})
}

// Stages returns the recorded stages in mark order.
func (r *Recorder) Stages() []Stage {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Stage, len(r.stages))
	copy(out, r.stages)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Sequence < out[j].Sequence })
	return out
}

// Close freezes the recorder (later Marks are dropped, not panicked).
func (r *Recorder) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
}

// Closed reports whether the recorder is frozen.
func (r *Recorder) Closed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closed
}

// AnchorSource returns the anchor provenance string.
func (r *Recorder) AnchorSource() string {
	return r.source
}

// KVPairs returns a log/slog-friendly flattened stage list
// ("anchor", source, stage1, "12ms", ...).
func (r *Recorder) KVPairs() []any {
	st := r.Stages()
	out := make([]any, 0, len(st)*2+2)
	out = append(out, "anchor", r.source)
	for _, s := range st {
		out = append(out, s.Name, strconv.FormatInt(s.Elapsed.Milliseconds(), 10)+"ms")
	}
	return out
}

// osProcessStart is set by the platform files (procfs / GetProcessTimes).
// ok=false means "use the Go package-init fallback".
var osProcessStart func() (t time.Time, source string, ok bool)

// parseProcStatField22 extracts the process start instant from a
// /proc/self/stat line (named helper so the Linux collector stays
// testable without spawning anything).
func parseProcStatField22(stat string) (time.Time, bool) {
	// /proc/self/stat: the comm field may contain spaces/parentheses;
	// everything after the LAST ')' is fixed-position.
	idx := strings.LastIndex(stat, ")")
	if idx < 0 || idx+2 >= len(stat) {
		return time.Time{}, false
	}
	fields := strings.Fields(stat[idx+2:])
	// After removing pid+comm, the canonical field 22 (starttime) is
	// index 19 of the remainder.
	if len(fields) < 20 {
		return time.Time{}, false
	}
	ticks, err := strconv.ParseInt(fields[19], 10, 64)
	if err != nil || ticks < 0 {
		return time.Time{}, false
	}
	clkTck := int64(100) // documented userspace-visible default (sysconf(_SC_CLK_TCK))
	if v := os.Getenv("SHEYTAN_TEST_CLKTCK"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			clkTck = n
		}
	}
	elapsed := time.Duration(ticks) * time.Second / time.Duration(clkTck)
	// boot time: read /proc/stat btime (seconds since epoch) so the
	// returned instant is absolute wall-clock, comparable with logs.
	btime, ok := readBtime()
	if !ok {
		return time.Time{}, false
	}
	return time.Unix(btime, 0).Add(elapsed), true
}

func readBtime() (int64, bool) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "btime ") {
			v, err := strconv.ParseInt(strings.TrimSpace(line[len("btime "):]), 10, 64)
			if err == nil {
				return v, true
			}
			return 0, false
		}
	}
	return 0, false
}
