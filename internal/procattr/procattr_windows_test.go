//go:build windows

package procattr

import (
	"os/exec"
	"testing"
)

// TestConfigureHidesChildConsole is the Windows no-console contract
// (run 42598929646 V13): every helper process must be created with
// HideWindow and CREATE_NO_WINDOW so no command window can appear,
// while stdout/stderr redirection (the diagnostics channel) stays
// functional. Executed by the Windows CI gates; type-checked
// cross-platform via GOOS=windows go vet.
func TestConfigureHidesChildConsole(t *testing.T) {
	cmd := exec.Command("cmd.exe", "/c", "echo probe")
	Configure(cmd)
	if cmd.SysProcAttr == nil {
		t.Fatal("Configure must set SysProcAttr on Windows")
	}
	if !cmd.SysProcAttr.HideWindow {
		t.Fatal("HideWindow must be true for child helper processes")
	}
	if cmd.SysProcAttr.CreationFlags&createNoWindow == 0 {
		t.Fatalf("CreationFlags must include CREATE_NO_WINDOW (0x08000000), got 0x%08x", cmd.SysProcAttr.CreationFlags)
	}
	Configure(nil) // must not panic
}
