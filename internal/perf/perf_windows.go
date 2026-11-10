//go:build windows

// perf_windows.go — true OS process-start anchor for Windows:
// GetProcessTimes().CreationTime via golang.org/x/sys/windows (no
// subprocess, no shell, no WMI).
package perf

import (
	"time"

	"golang.org/x/sys/windows"
)

func init() {
	osProcessStart = procStartWindows
}

func procStartWindows() (time.Time, string, bool) {
	h := windows.CurrentProcess()
	var create, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &create, &exit, &kernel, &user); err != nil {
		return time.Time{}, "", false
	}
	t := time.Unix(0, create.Nanoseconds())
	return t, "os:GetProcessTimes", true
}
