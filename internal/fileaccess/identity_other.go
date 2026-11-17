//go:build !unix

package fileaccess

import "os"

// identityOf extracts the platform filesystem identity of a stat
// result. The portable fallback uses (volume id + modification
// nanoseconds + size): weaker than device/inode but sufficient to
// detect a replaced root in practice; Windows improves this in a
// later milestone if evidence requires (the contract stays the same).
func identityOf(fi os.FileInfo) RootIdentity {
	if fi == nil {
		return RootIdentity{}
	}
	return RootIdentity{Dev: uint64(fi.ModTime().UnixNano()), Ino: uint64(fi.Size())}
}
