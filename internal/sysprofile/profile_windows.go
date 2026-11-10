//go:build windows

// profile_windows.go — native Windows collectors for the system
// profile. All values come from native OS surfaces: kernel32 via
// x/sys/windows LazyDLL proc calls (GlobalMemoryStatusEx,
// GetSystemPowerStatus) and the registry (hardware description, NT
// version). HARD RULE honored: no WMI/PowerShell/wmic/dxdiag
// subprocess is ever spawned for hardware facts, and ordinary user
// rights suffice for every read here.
package sysprofile

import (
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const platformCollectorName = "windows-native"

func init() {
	hookCPUModel = cpuModelWindows
	hookMemory = memoryWindows
	hookOSVersion = osVersionWindows
	hookPhysicalCores = nil // no reliable native count without WMI — honestly unknown
	hookPower = powerWindows
}

// cpuModelWindows reads ProcessorNameString from the hardware
// description registry key — the native location Windows itself
// populates (read-only, user rights, no subprocess).
func cpuModelWindows() (string, string, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`HARDWARE\DESCRIPTION\System\CentralProcessor\0`, registry.QUERY_VALUE)
	if err != nil {
		return "", "", fmt.Errorf("hardware description key unavailable: %w", err)
	}
	defer k.Close()
	v, _, err := k.GetStringValue("ProcessorNameString")
	if err != nil {
		return "", "", nil // genuinely not exposed
	}
	return strings.TrimSpace(v), `HKLM\HARDWARE\DESCRIPTION\System\CentralProcessor\0`, nil
}

// memoryStatusEx mirrors the Win32 MEMORYSTATUSEX structure exactly.
type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

// memoryWindows reads physical memory via kernel32!GlobalMemoryStatusEx.
// The pinned x/sys/windows does not wrap this call, so it is invoked
// through the documented LazyDLL proc mechanism — still a native API
// call in-process, no shell. TotalPhysical/AvailablePhysical are
// reported; process virtual address space is deliberately NOT reported
// as memory capacity.
func memoryWindows() (total, avail int64, source string, err error) {
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	proc := kernel32.NewProc("GlobalMemoryStatusEx")
	var ms memoryStatusEx
	ms.Length = uint32(unsafe.Sizeof(ms))
	r, _, callErr := proc.Call(uintptr(unsafe.Pointer(&ms)))
	if r == 0 {
		return 0, 0, "", fmt.Errorf("GlobalMemoryStatusEx failed: %v", callErr)
	}
	return int64(ms.TotalPhys), int64(ms.AvailPhys),
		"kernel32!GlobalMemoryStatusEx (physical memory)", nil
}

// osVersionWindows reads the NT product/version identity from the
// registry (native, user rights): ProductName + DisplayVersion +
// CurrentBuildNumber.
func osVersionWindows() (string, string, error) {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE)
	if err != nil {
		return "", "", fmt.Errorf("NT current-version key unavailable: %w", err)
	}
	defer k.Close()
	name, _, nerr := k.GetStringValue("ProductName")
	display, _, derr := k.GetStringValue("DisplayVersion")
	build, _, berr := k.GetStringValue("CurrentBuildNumber")
	var parts []string
	if nerr == nil && strings.TrimSpace(name) != "" {
		parts = append(parts, strings.TrimSpace(name))
	}
	if derr == nil && strings.TrimSpace(display) != "" {
		parts = append(parts, strings.TrimSpace(display))
	}
	if berr == nil && strings.TrimSpace(build) != "" {
		parts = append(parts, "build "+strings.TrimSpace(build))
	}
	if len(parts) == 0 {
		return "", "", nil
	}
	return strings.Join(parts, ", "), `HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion`, nil
}

// systemPowerStatus mirrors the Win32 SYSTEM_POWER_STATUS structure.
type systemPowerStatus struct {
	ACLineStatus        byte // 0 offline, 1 online, 255 unknown
	BatteryFlag         byte
	BatteryLifePercent  byte
	Reserved1           byte
	BatteryLifeTime     uint32
	BatteryFullLifeTime uint32
}

// powerWindows reads AC/battery state via kernel32!GetSystemPowerStatus.
func powerWindows() (string, string, error) {
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")
	proc := kernel32.NewProc("GetSystemPowerStatus")
	var sps systemPowerStatus
	r, _, callErr := proc.Call(uintptr(unsafe.Pointer(&sps)))
	if r == 0 {
		return "", "", fmt.Errorf("GetSystemPowerStatus failed: %v", callErr)
	}
	switch sps.ACLineStatus {
	case 1:
		return "ac", "kernel32!GetSystemPowerStatus (ACLineStatus)", nil
	case 0:
		return "battery", "kernel32!GetSystemPowerStatus (ACLineStatus)", nil
	default: // 255 = unknown
		return "", "", nil
	}
}
