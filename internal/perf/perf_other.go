//go:build !linux && !windows

// perf_other.go — platforms without a native process-start collector
// use the honest Go package-init fallback (documented in perf.go).
package perf

import "time"

func init() {
	osProcessStart = procStartOther
}

func procStartOther() (time.Time, string, bool) {
	return time.Time{}, "", false
}
