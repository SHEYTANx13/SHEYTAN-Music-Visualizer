//go:build windows

package procattr

import (
	"os/exec"
	"syscall"
)

// createNoWindow is the Microsoft CREATE_NO_WINDOW process creation
// flag (0x08000000): the child gets NO console window. Combined with
// SysProcAttr.HideWindow (CREATE_NO_WINDOW alone leaves HideWindow
// semantics incomplete for console hosts) this guarantees a helper
// process never flashes a command prompt, whatever the parent's
// subsystem.
//
// Reference: Microsoft Learn, "Process Creation Flags"
// (learn.microsoft.com/en-us/windows/win32/procthread/process-creation-flags).
const createNoWindow uint32 = 0x08000000

// configure applies the Windows hidden-child policy.
func configure(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
}
