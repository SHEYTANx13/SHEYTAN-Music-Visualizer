//go:build !windows

package procattr

import (
	"os/exec"
)

// configure is a deliberate no-op outside Windows: POSIX child
// processes have no console window to allocate or hide, and the Linux
// desktop integration never launches a terminal. Keeping the call
// sites uniform (one Configure before every Start) is what makes the
// Windows guarantee auditable.
func configure(cmd *exec.Cmd) {
	_ = cmd // no platform-specific attributes required
}
