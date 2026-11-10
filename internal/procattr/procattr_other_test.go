//go:build !windows

package procattr

import (
	"os/exec"
	"testing"
)

// TestConfigureIsNoOpOutsideWindows pins the non-Windows contract: the
// helper never mutates the command on POSIX platforms (there is no
// console-window concept), so process behavior is untouched.
func TestConfigureIsNoOpOutsideWindows(t *testing.T) {
	cmd := exec.Command("true")
	Configure(cmd)
	if cmd.SysProcAttr != nil {
		t.Fatalf("Configure must not set SysProcAttr on non-Windows platforms, got %+v", cmd.SysProcAttr)
	}
	Configure(nil) // must not panic
}
