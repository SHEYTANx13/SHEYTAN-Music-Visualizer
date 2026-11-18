//go:build windows

package fileaccess

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// identityOf extracts the platform filesystem identity of a stat
// result. The FileInfo-only view on Windows carries no file identity,
// so this fallback stays the weak mtime+size pair (used only where a
// caller has a bare FileInfo; the authorization path uses
// identityFromPath below).
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

// identityFromPath resolves the canonical filesystem identity of a
// path using the Windows volume/file identity primitives: the volume
// serial number plus the file index from GetFileInformationByHandle
// (the NTFS file ID), with modification time and size as additional
// discriminators. A delete+recreate may reuse an MFT record, so the
// timestamps pin the identity to the specific directory instance.
func identityFromPath(path string) (RootIdentity, error) {
	h, err := os.Open(path)
	if err != nil {
		return RootIdentity{}, err
	}
	defer h.Close()
	var bi windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(windows.Handle(h.Fd()), &bi); err != nil {
		return RootIdentity{}, fmt.Errorf("fileaccess: windows file identity: %w", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		return RootIdentity{}, err
	}
	mt := fi.ModTime()
	return RootIdentity{
		Dev:       uint64(bi.VolumeSerialNumber),
		Ino:       uint64(bi.FileIndexHigh)<<32 | uint64(bi.FileIndexLow),
		MtimeSec:  mt.Unix(),
		MtimeNsec: int64(mt.Nanosecond()),
		Size:      fi.Size(),
	}, nil
}

// infoIdentityIsPrimary reports whether identityOf(os.FileInfo) carries
// the platform's primary identity components. Windows FileInfo carries
// no file identity, so the handle-level re-verification is skipped and
// the authorization path relies on identityFromPath (real file IDs).
func infoIdentityIsPrimary() bool { return false }
