//go:build darwin

// profile_darwin.go — native macOS collectors for the system profile.
// Every value comes from kernel interfaces read directly in-process:
// sysctl(3) via golang.org/x/sys/unix (Sysctl/SysctlRaw — real
// syscalls, never the `sysctl` subprocess, per the package-wide hard
// rule) and uname(2) via x/sys/unix.Uname.
//
// Honesty rules:
//   - hw.memsize is the installed PHYSICAL memory size (64-bit).
//   - No honest available-memory read exists without Mach
//     host_statistics64, which the pure-Go toolchain path does not
//     provide; collectMemory reports Unknown (not zero-as-fact) for the
//     non-positive available value instead of fabricating one.
//   - Power state requires IOKit, so hookPower stays nil and the power
//     section stays Unknown with that exact reason.
package sysprofile

import (
	"encoding/binary"
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

const platformCollectorName = "darwin-native"

func init() {
	hookCPUModel = cpuModelDarwin
	hookMemory = memoryDarwin
	hookOSVersion = osVersionDarwin
	hookPhysicalCores = physicalCoresDarwin
}

// cpuModelDarwin reads the CPU brand string. Apple Silicon does not
// export machdep.cpu.brand_string, so hw.model ("Mac14,2") is the
// honest fallback identifier; a machine exposing neither stays Unknown
// ("" with nil error = genuinely not exposed).
func cpuModelDarwin() (string, string, error) {
	for _, name := range []string{"machdep.cpu.brand_string", "hw.model"} {
		v, err := unix.Sysctl(name)
		if err == nil && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v), "sysctl " + name, nil
		}
	}
	return "", "", nil
}

// memoryDarwin reads the installed physical memory via hw.memsize.
// The kernel exports 64-bit integer sysctls as their raw 8 bytes,
// little-endian on every shipping macOS target (amd64/arm64).
func memoryDarwin() (total, avail int64, source string, err error) {
	raw, err := unix.SysctlRaw("hw.memsize")
	if err != nil {
		return 0, 0, "", fmt.Errorf("sysctl hw.memsize: %w", err)
	}
	if len(raw) != 8 {
		return 0, 0, "", fmt.Errorf("sysctl hw.memsize: expected 8 raw bytes for an int64, got %d", len(raw))
	}
	total = int64(binary.LittleEndian.Uint64(raw))
	if total <= 0 {
		return 0, 0, "", fmt.Errorf("sysctl hw.memsize reported a non-positive size %d", total)
	}
	source = "sysctl hw.memsize"
	// AvailableBytes deliberately stays 0: collectMemory surfaces that
	// as Unknown with the exact reason (no Mach host_statistics64 on
	// the pure-Go path). Returning a guessed number would violate the
	// never-fabricate rule.
	return total, 0, source, nil
}

// osVersionDarwin combines the macOS product version (direct read of
// the system's own SystemVersion.plist) with the kernel release
// (uname(2) syscall). A machine where neither is readable stays
// genuinely unavailable ("", nil).
func osVersionDarwin() (string, string, error) {
	var parts []string
	if v := darwinProductVersion(); v != "" {
		parts = append(parts, "macOS "+v)
	}
	var uts unix.Utsname
	if err := unix.Uname(&uts); err == nil {
		parts = append(parts, "kernel "+utsStr(&uts.Release))
	}
	if len(parts) == 0 {
		return "", "", nil
	}
	return strings.Join(parts, ", "),
		"/System/Library/CoreServices/SystemVersion.plist + uname(2) syscall", nil
}

// darwinProductVersion extracts ProductVersion from the system plist.
// The file is XML; the scan is bounded, read-only and subprocess-free.
// A parse miss returns "" so the version stays honestly Unknown.
func darwinProductVersion() string {
	data, err := os.ReadFile("/System/Library/CoreServices/SystemVersion.plist")
	if err != nil {
		return ""
	}
	s := string(data)
	const key = "<key>ProductVersion</key>"
	i := strings.Index(s, key)
	if i < 0 {
		return ""
	}
	rest := s[i+len(key):]
	open := strings.Index(rest, "<string>")
	if open < 0 {
		return ""
	}
	rest = rest[open+len("<string>"):]
	closeIdx := strings.Index(rest, "</string>")
	if closeIdx < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:closeIdx])
}

// physicalCoresDarwin reads hw.physicalcpu (a 32-bit integer sysctl
// exported as 4 raw bytes). Not exposed → honestly unknown.
func physicalCoresDarwin() (string, string, error) {
	raw, err := unix.SysctlRaw("hw.physicalcpu")
	if err != nil || len(raw) != 4 {
		return "", "", nil
	}
	return strconv.FormatUint(uint64(binary.LittleEndian.Uint32(raw)), 10),
		"sysctl hw.physicalcpu", nil
}

// utsStr converts a fixed-size byte array from Utsname into a string.
// (Local copy with the darwin Utsname field width: 256 bytes, unlike
// the linux 65-byte arrays; profile_linux.go is linux-tagged and
// cannot provide it.)
func utsStr(a *[256]byte) string {
	b := make([]byte, 0, len(a))
	for _, c := range a {
		if c == 0 {
			break
		}
		b = append(b, byte(c))
	}
	return string(b)
}
