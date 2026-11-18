//go:build !unix && !windows

package fileaccess

import "os"

// identityOf extracts the platform filesystem identity of a stat
// result. The portable fallback uses (modification nanoseconds + size):
// weaker than device/inode but sufficient to detect a replaced root in
// practice; platforms with real identity primitives (Linux statx,
// Windows file IDs) use identityFromPath on the authorization path.
func identityOf(fi os.FileInfo) RootIdentity {
	if fi == nil {
		return RootIdentity{}
	}
	mt := fi.ModTime()
	return RootIdentity{
		MtimeSec:  mt.Unix(),
		MtimeNsec: int64(mt.Nanosecond()),
		Size:      fi.Size(),
	}
}

// infoIdentityIsPrimary reports whether identityOf(os.FileInfo) carries
// the platform's primary identity components (false on this fallback).
func infoIdentityIsPrimary() bool { return false }

// identityFromPath resolves the filesystem identity of a path via the
// portable fallback primitive (mtime+size).
func identityFromPath(path string) (RootIdentity, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return RootIdentity{}, err
	}
	return identityOf(fi), nil
}
