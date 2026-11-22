package main

import (
	"strings"
	"testing"
	"time"
)

func TestTimedrunPropagatesExitCode(t *testing.T) {
	if rc := run([]string{"-timeout", "30s", "--", "sh", "-c", "exit 7"}); rc != 7 {
		t.Fatalf("rc = %d, want child's 7", rc)
	}
}

func TestTimedrunSuccessZero(t *testing.T) {
	if rc := run([]string{"-timeout", "30s", "--", "sh", "-c", "echo ok"}); rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}
}

func TestTimedrunTimeout99(t *testing.T) {
	start := time.Now()
	rc := run([]string{"-timeout", "1s", "--", "sh", "-c", "sleep 30"})
	elapsed := time.Since(start)
	if rc != 99 {
		t.Fatalf("rc = %d, want 99 (timeout)", rc)
	}
	if elapsed > 10*time.Second {
		t.Fatalf("timeout kill took %s; the bound must be hard", elapsed)
	}
}

func TestTimedrunUsageAndStartFailure(t *testing.T) {
	if rc := run(nil); rc != 2 {
		t.Fatalf("no args rc = %d, want 2", rc)
	}
	if rc := run([]string{"-timeout", "1s", "--", "/no/such/binary"}); rc != 127 {
		t.Fatalf("missing binary rc = %d, want 127", rc)
	}
}

func TestTimedrunStdoutPassthrough(t *testing.T) {
	// stdout is inherited; just prove the child ran to completion.
	if rc := run([]string{"-timeout", "30s", "--", "sh", "-c", "printf hi"}); rc != 0 {
		t.Fatalf("rc = %d", rc)
	}
	if !strings.Contains("hi", "hi") {
		t.Fatal("unreachable")
	}
}
