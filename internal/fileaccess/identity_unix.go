//go:build unix

package fileaccess

import (
	"os"
	"syscall"
)

// identityOf extracts the platform filesystem identity (device +
// inode) of a stat result. On Unix this is the canonical identity used
// to detect a replaced root (delete + recreate, bind-mount change).
func identityOf(fi os.FileInfo) RootIdentity {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return RootIdentity{Dev: uint64(st.Dev), Ino: uint64(st.Ino)}
	}
	return RootIdentity{}
}
