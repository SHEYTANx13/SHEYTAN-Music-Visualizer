// cpu_features.go — CPU SIMD feature flags from golang.org/x/sys/cpu.
// The library declares every architecture's feature struct unconditionally
// (flags are false when the architecture does not match) and checks BOTH
// CPU capability and OS enablement — e.g. X86.HasAVX2 already folds in
// OSXSAVE/XGETBV OS state — matching the dispatch rule: vector paths run
// only when the CPU AND the OS have enabled the required state.
package sysprofile

import "golang.org/x/sys/cpu"

// cpuFeatures returns the enabled feature names relevant to DSP
// dispatch; an empty result means no reliable flags on this build and
// the profile reports that honestly.
func cpuFeatures() []string {
	var f []string
	// x86 (flags false on other architectures).
	if cpu.X86.HasSSE2 {
		f = append(f, "sse2")
	}
	if cpu.X86.HasSSSE3 {
		f = append(f, "ssse3")
	}
	if cpu.X86.HasSSE41 {
		f = append(f, "sse4.1")
	}
	if cpu.X86.HasSSE42 {
		f = append(f, "sse4.2")
	}
	if cpu.X86.HasAVX {
		f = append(f, "avx")
	}
	if cpu.X86.HasAVX2 {
		f = append(f, "avx2")
	}
	// ARM64 (ASIMD is the NEON-class advanced SIMD on ARMv8).
	if cpu.ARM64.HasASIMD {
		f = append(f, "asimd/neon")
	}
	// 32-bit ARM (OS-reported on Linux).
	if cpu.ARM.HasNEON {
		f = append(f, "neon")
	}
	return f
}
