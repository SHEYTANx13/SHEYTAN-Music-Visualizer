//go:build unix && !linux

package fileaccess

import (
	"os"
	"syscall"
)

// identityOf extracts the platform filesystem identity (device +
// inode) of a stat result. On non-Linux Unix (darwin/BSD) this is the
// canonical identity used to detect a replaced root (delete + recreate,
// bind-mount change). APFS and the BSD filesystems allocate new inode
// numbers on recreation, so device+inode is stable here; Linux uses the
// statx-based primitive in identity_linux.go.
func identityOf(fi os.FileInfo) RootIdentity {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return RootIdentity{Dev: uint64(st.Dev), Ino: uint64(st.Ino)}
	}
	return RootIdentity{}
}

// infoIdentityIsPrimary reports whether identityOf(os.FileInfo) carries
// the platform's primary identity components (device + inode).
func infoIdentityIsPrimary() bool { return true }

// identityFromPath resolves the filesystem identity of a path via the
// platform stat primitive. Non-Linux Unix has no statx; device+inode
// (allocated fresh on recreation by APFS/BSD filesystems) is the
// canonical identity here.
func identityFromPath(path string) (RootIdentity, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return RootIdentity{}, err
	}
	return identityOf(fi), nil
}
