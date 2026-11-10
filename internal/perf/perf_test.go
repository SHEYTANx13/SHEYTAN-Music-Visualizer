package perf

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// The Linux procfs parser must handle a comm field containing spaces
// and parentheses (the documented parsing hazard) and honor the
// injected clock-tick constant for deterministic arithmetic.
func TestParseProcStatField22(t *testing.T) {
	t.Setenv("SHEYTAN_TEST_CLKTCK", "100")
	btime, ok := readBtime()
	if !ok {
		t.Skip("no /proc/stat btime on this host — absolute-instant assertions not computable")
	}
	// pid (comm with ) and spaces) state ppid pgrp session tty_nr tpgid
	// flags minflt cminflt majflt cmajflt utime stime cutime cstime
	// priority nice num_threads itrealvalue starttime ...
	// starttime = 1234 ticks = 12.34 s at CLK_TCK=100.
	stat := "4242 (SHEYTAN vi)sual) R 1 1 1 0 -1 4194560 100 0 0 0 10 5 0 0 20 0 4 0 1234 " +
		"0 0 18446744073709551615 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0"
	got, ok := parseProcStatField22(stat)
	if !ok {
		t.Fatalf("parse failed for a comm field with spaces/parens")
	}
	want := time.Unix(btime, 0).Add(1234 * 10 * time.Millisecond)
	if got.Sub(want) > time.Millisecond || want.Sub(got) > time.Millisecond {
		t.Fatalf("start instant mismatch: got %v want %v", got, want)
	}
}

func TestParseProcStatField22Malformed(t *testing.T) {
	for _, bad := range []string{"", "1", "12 (abc", "1 (x) S 1"} {
		if _, ok := parseProcStatField22(bad); ok {
			t.Fatalf("malformed stat %q unexpectedly parsed", bad)
		}
	}
}

// Recorder: monotonic ordering, sequence stability, closed drops marks,
// KVPairs flattening, and concurrent Mark safety.
func TestRecorderStagesAndConcurrency(t *testing.T) {
	r := NewRecorder()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			r.Mark("stage")
		}(i)
	}
	r.Mark("final")
	wg.Wait()
	st := r.Stages()
	if len(st) != 9 {
		t.Fatalf("expected 9 stages, got %d", len(st))
	}
	for i, s := range st {
		if s.Sequence != i {
			t.Fatalf("sequence not compact/ordered: [%d]=%d", i, s.Sequence)
		}
		if s.Elapsed < 0 {
			t.Fatalf("negative elapsed at %d", i)
		}
		if !strings.HasPrefix(s.Anchor, "os:") && !strings.HasPrefix(s.Anchor, "go:") {
			t.Fatalf("anchor provenance missing: %q", s.Anchor)
		}
	}
	kv := r.KVPairs()
	if len(kv) != 2*(9+1) {
		t.Fatalf("KVPairs shape wrong: %d", len(kv))
	}
	r.Close()
	r.Mark("after-close")
	if got := len(r.Stages()); got != 9 {
		t.Fatalf("closed recorder accepted a mark: %d stages", got)
	}
}

// ProcessStart must always report an anchor provenance and never a
// zero instant.
func TestProcessStartHasProvenance(t *testing.T) {
	ts, src := ProcessStart()
	if ts.IsZero() {
		t.Fatal("process start instant is zero")
	}
	if src == "" {
		t.Fatal("process start provenance empty")
	}
	if !strings.HasPrefix(src, "os:") && !strings.HasPrefix(src, "go:") {
		t.Fatalf("unexpected provenance %q", src)
	}
}
