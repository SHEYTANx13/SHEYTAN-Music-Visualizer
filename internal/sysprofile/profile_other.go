//go:build !linux && !windows

// profile_other.go — honest stub for platforms without a native
// collector set (e.g. darwin/web builds). Every platform hook stays
// nil so Collect reports Unknown with the exact reason instead of
// guessing; runtime-level facts (NumCPU, GOMAXPROCS, GOARCH) remain
// fully collected by the platform-neutral code.
package sysprofile

const platformCollectorName = "generic (no native collector for this platform)"
