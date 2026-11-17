//go:build unix

package fileaccess

import "syscall"

// mkfifoUnix creates a FIFO for the adversarial special-file test.
func mkfifoUnix(path string) error {
	return syscall.Mkfifo(path, 0o644)
}
