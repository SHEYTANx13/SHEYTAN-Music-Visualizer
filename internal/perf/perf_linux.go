//go:build linux

// perf_linux.go — true OS process-start anchor for Linux: /proc/self/stat
// field 22 (starttime, clock ticks since boot) + /proc/stat btime, both
// read directly from procfs (no subprocess, no shell).
package perf

import (
	"os"
	"time"
)

func init() {
	osProcessStart = procStartLinux
}

func procStartLinux() (time.Time, string, bool) {
	data, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return time.Time{}, "", false
	}
	t, ok := parseProcStatField22(string(data))
	if !ok {
		return time.Time{}, "", false
	}
	return t, "os:/proc/self/stat", true
}
