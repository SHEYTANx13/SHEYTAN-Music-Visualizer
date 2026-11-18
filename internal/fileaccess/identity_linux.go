//go:build linux

package fileaccess

import (
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// identityOf extracts the platform filesystem identity of a stat
// result. On Linux this carries device + inode + status-change time:
// the ctime component catches a same-inode recreation whose delete and
// recreate land in the same coarse timestamp tick as the original.
func identityOf(fi os.FileInfo) RootIdentity {
	if fi == nil {
		return RootIdentity{}
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return RootIdentity{}
	}
	return RootIdentity{
		Dev:       uint64(st.Dev),
		Ino:       uint64(st.Ino),
		CtimeSec:  int64(st.Ctim.Sec),
		CtimeNsec: int64(st.Ctim.Nsec),
	}
}

// identityFromPath resolves the canonical filesystem identity of a
// path. Linux uses statx so the birth (creation) time is available:
// after a delete+recreate, a recreated directory can REUSE the same
// device+inode pair (observed on CI ext4 runners), so the birth time is
// the discriminator that makes stale-confirmation rejection
// deterministic instead of filesystem-luck-dependent. Unsupported
// components come back zero and are skipped by the comparison.
func identityFromPath(path string) (RootIdentity, error) {
	var st unix.Statx_t
	err := unix.Statx(unix.AT_FDCWD, path,
		unix.AT_SYMLINK_NOFOLLOW,
		unix.STATX_BASIC_STATS|unix.STATX_BTIME, &st)
	if err != nil {
		return RootIdentity{}, err
	}
	id := RootIdentity{
		// Mkdev composes the exact kernel dev_t encoding that fstat
		// reports in Stat_t.Dev, so identityOf (fstat) and
		// identityFromPath (statx) produce directly comparable Dev
		// values on the same filesystem.
		Dev:       unix.Mkdev(st.Dev_major, st.Dev_minor),
		Ino:       st.Ino,
		Size:      int64(st.Size),
		CtimeSec:  int64(st.Ctime.Sec),
		CtimeNsec: int64(st.Ctime.Nsec),
	}
	if st.Mask&unix.STATX_BTIME != 0 {
		id.BtimeSec = int64(st.Btime.Sec)
		id.BtimeNsec = int64(st.Btime.Nsec)
	}
	return id, nil
}

// infoIdentityIsPrimary reports whether identityOf(os.FileInfo) carries
// the platform's primary identity components (device + inode). Linux
// FileInfo always carries syscall.Stat_t, so the handle-level
// re-verification in ConfirmAuthorization is active here.
func infoIdentityIsPrimary() bool { return true }
