package render

// preference.go — profile-neutral renderer preference contract.
// Extracted from gpu.go so both the linked-GPU profile and the
// sheytan_nogpu profile share the same preference vocabulary.

import (
	"fmt"
	"os"
	"strings"
)

type Preference string

const (
	PrefAuto     Preference = "auto"
	PrefCPU      Preference = "cpu"
	PrefVulkan   Preference = "vulkan"
	PrefDX12     Preference = "dx12"
	PrefSoftware Preference = "software"
)

// ParsePreference validates a user-supplied preference string
// (case-insensitive). Unknown values are an error, never silently auto.
func ParsePreference(s string) (Preference, error) {
	switch Preference(strings.ToLower(strings.TrimSpace(s))) {
	case "", PrefAuto:
		return PrefAuto, nil
	case PrefCPU:
		return PrefCPU, nil
	case PrefVulkan:
		return PrefVulkan, nil
	case PrefDX12:
		return PrefDX12, nil
	case PrefSoftware:
		return PrefSoftware, nil
	default:
		return "", fmt.Errorf("render: unknown GPU preference %q (want auto|cpu|vulkan|dx12|software)", s)
	}
}

// PreferenceFromEnv reads SHEYTAN_GPU_PREFERENCE (optional). Missing
// env var → (PrefAuto, false); malformed value → error so callers can
// surface it instead of silently falling back.
func PreferenceFromEnv() (Preference, bool, error) {
	v := strings.TrimSpace(os.Getenv("SHEYTAN_GPU_PREFERENCE"))
	if v == "" {
		return PrefAuto, false, nil
	}
	p, err := ParsePreference(v)
	if err != nil {
		return "", true, err
	}
	return p, true, nil
}

func containsFold(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if eqFold(s[i:i+len(sub)], sub) {
			return true
		}
	}
	return false
}

func eqFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

// isHardwareBackend reports whether a backend API is a real graphics
// driver (noop/software adapters never count as GPU execution).

// eqFold is an ASCII-only case-insensitive comparison used by containsFold.
