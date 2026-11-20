//go:build darwin

// identity_darwin.go — macOS filesystem identity for granted-root
// reconfirmation. Device+inode remain the PRIMARY identity components,
// but APFS (and HFS+) can reuse a directory inode number soon after a
// delete+recreate, so a same-(dev,ino) pair alone must never confirm a
// stale authorization. The stat birth time (st_birthtimespec) and the
// status-change time (st_ctimespec) are therefore carried as
// RootIdentity discriminators: sameRootIdentity compares any optional
// component that is present on BOTH sides, so a same-inode recreation
// (different creation instant) is rejected and requires fresh user
// confirmation. Field names verified against the pinned Go 1.27.4
// toolchain's syscall.Stat_t for darwin: the timespec members are named
// Atimespec, Mtimespec, Ctimespec and Birthtimespec (syscall.Timespec
// with Sec/Nsec int64 fields).
package fileaccess

import (
	"os"
	"syscall"
)

// identityOf extracts the macOS filesystem identity (device + inode
// + birth/change-time discriminators) of a stat result.
func identityOf(fi os.FileInfo) RootIdentity {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return RootIdentity{}
	}
	return RootIdentity{
		Dev:       uint64(st.Dev),
		Ino:       st.Ino,
		BtimeSec:  st.Birthtimespec.Sec,
		BtimeNsec: st.Birthtimespec.Nsec,
		CtimeSec:  st.Ctimespec.Sec,
		CtimeNsec: st.Ctimespec.Nsec,
	}
}

// infoIdentityIsPrimary reports whether identityOf(os.FileInfo)
// carries the platform's primary identity components (device + inode).
func infoIdentityIsPrimary() bool { return true }

// identityFromPath resolves the filesystem identity of a path via the
// platform stat primitive.
func identityFromPath(path string) (RootIdentity, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return RootIdentity{}, err
	}
	return identityOf(fi), nil
}
