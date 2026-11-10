//go:build linux

// profile_linux.go — native Linux collectors for the system profile.
// Every value comes from kernel interfaces read directly (procfs,
// sysfs, os-release, x/sys/unix.Uname) — never from a shell, never
// from wmic/PowerShell/dxdiag-style subprocesses (hard run-42598929646
// rule), and always with an explicit reason when unavailable.
package sysprofile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

const platformCollectorName = "linux-native"

func init() {
	hookCPUModel = cpuModelLinux
	hookMemory = memoryLinux
	hookOSVersion = osVersionLinux
	hookPhysicalCores = physicalCoresLinux
	hookPower = powerLinux
}

// cpuModelLinux reads "model name" from /proc/cpuinfo (native procfs
// read — the OS's own CPU description, no subprocess of any kind).
func cpuModelLinux() (string, string, error) {
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return "", "", fmt.Errorf("/proc/cpuinfo unreadable: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "model name") {
			if _, v, ok := strings.Cut(line, ":"); ok {
				return strings.TrimSpace(v), "/proc/cpuinfo", nil
			}
		}
	}
	// ARM SBCs often lack "model name"; the device-tree model is the
	// honest native alternative (still a plain file read).
	if dt, err := os.ReadFile("/proc/device-tree/model"); err == nil {
		if m := strings.TrimSpace(strings.TrimRight(string(dt), "\x00")); m != "" {
			return m, "/proc/device-tree/model", nil
		}
	}
	return "", "", nil // genuinely not exposed on this machine
}

// memoryLinux reads MemTotal/MemAvailable from /proc/meminfo and folds
// in a cgroup v2 limit when one exists (container quota is a REAL
// memory ceiling; reporting the bare host total inside a container
// would overstate the budget). Sources are labelled verbatim.
func memoryLinux() (total, avail int64, source string, err error) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, "", fmt.Errorf("/proc/meminfo unreadable: %w", err)
	}
	fields := map[string]int64{}
	for _, line := range strings.Split(string(data), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(v), " kB"))
		n, err := strconv.ParseInt(strings.Fields(v + " 0")[0], 10, 64)
		if err != nil {
			continue
		}
		fields[strings.TrimSpace(k)] = n * 1024 // kB → bytes
	}
	total, okTotal := fields["MemTotal"]
	avail, okAvail := fields["MemAvailable"]
	if !okTotal || total <= 0 {
		return 0, 0, "", errors.New("/proc/meminfo has no usable MemTotal")
	}
	if !okAvail {
		// MemAvailable appeared in kernel 3.14; fall back to
		// MemFree+buffers+cache rather than guessing 0.
		avail = fields["MemFree"] + fields["Buffers"] + fields["Cached"]
	}
	source = "/proc/meminfo"
	// cgroup v2 quota (when present) narrows both numbers honestly.
	if cgTotal, cgAvail, ok := cgroupV2Memory(); ok {
		if cgTotal > 0 && cgTotal < total {
			total = cgTotal
		}
		if cgAvail < avail {
			avail = cgAvail
		}
		source = "/proc/meminfo + cgroup v2 limit"
	}
	return total, avail, source, nil
}

// cgroupV2Memory returns (limit, limit-current, true) when a v2 memory
// limit applies. A missing/unlimited controller returns false.
func cgroupV2Memory() (limit, avail int64, ok bool) {
	const base = "/sys/fs/cgroup"
	max, err := os.ReadFile(filepath.Join(base, "memory.max"))
	if err != nil || strings.TrimSpace(string(max)) == "max" {
		return 0, 0, false
	}
	cur, err := os.ReadFile(filepath.Join(base, "memory.current"))
	if err != nil {
		return 0, 0, false
	}
	limit, err = strconv.ParseInt(strings.TrimSpace(string(max)), 10, 64)
	if err != nil || limit <= 0 {
		return 0, 0, false
	}
	curN, err := strconv.ParseInt(strings.TrimSpace(string(cur)), 10, 64)
	if err != nil || curN < 0 {
		return limit, limit, true
	}
	used := curN
	if used > limit {
		used = limit
	}
	return limit, limit - used, true
}

// osVersionLinux combines the distro identity (/etc/os-release) with
// the kernel release (x/sys/unix.Uname — a native syscall, not `uname`
// the subprocess).
func osVersionLinux() (string, string, error) {
	var parts []string
	if data, err := os.ReadFile("/etc/os-release"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "PRETTY_NAME=") {
				parts = append(parts, strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), `"`))
				break
			}
		}
	}
	var uts unix.Utsname
	if err := unix.Uname(&uts); err == nil {
		parts = append(parts, "kernel "+utsStr(&uts.Release))
	}
	if len(parts) == 0 {
		return "", "", nil
	}
	return strings.Join(parts, ", "), "/etc/os-release + uname(2) syscall", nil
}

// utsStr converts a fixed-size byte array from Utsname into a string.
func utsStr(a *[65]byte) string {
	b := make([]byte, 0, len(a))
	for _, c := range a {
		if c == 0 {
			break
		}
		b = append(b, byte(c))
	}
	return string(b)
}

// physicalCoresLinux reports the physical core count only when the
// kernel exposes per-CPU topology (core_id siblings). Hyper-thread
// pairs share a physical core; absent topology, it stays Unknown.
func physicalCoresLinux() (string, string, error) {
	matches, err := filepath.Glob("/sys/devices/system/cpu/cpu[0-9]*/topology/thread_siblings_list")
	if err != nil || len(matches) == 0 {
		return "", "", nil // topology not exposed → honestly unknown
	}
	seen := map[string]bool{}
	for _, m := range matches {
		data, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		seen[strings.TrimSpace(string(data))] = true
	}
	if len(seen) == 0 {
		return "", "", nil
	}
	return strconv.Itoa(len(seen)), "/sys/devices/system/cpu/*/topology", nil
}

// powerLinux reads the sysfs power supply class. A machine with no
// batteries/AC node (most desktops/servers) is genuinely unavailable —
// reported as such, never guessed.
func powerLinux() (string, string, error) {
	supplies, err := filepath.Glob("/sys/class/power_supply/*")
	if err != nil || len(supplies) == 0 {
		return "", "", nil
	}
	for _, s := range supplies {
		typ, _ := os.ReadFile(filepath.Join(s, "type"))
		if strings.TrimSpace(string(typ)) != "Mains" {
			continue
		}
		online, err := os.ReadFile(filepath.Join(s, "online"))
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(online)) == "1" {
			return "ac", filepath.Join(s, "online"), nil
		}
		return "battery", filepath.Join(s, "online"), nil
	}
	return "", "", nil
}
