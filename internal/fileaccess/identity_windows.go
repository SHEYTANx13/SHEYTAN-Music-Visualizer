//go:build windows

package fileaccess

import (
	"fmt"
	"os"
	"time"

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

// filetimeUnixNanos converts a Windows FILETIME (100 ns intervals
// since 1601-01-01 UTC) to nanoseconds since the Unix epoch. Values
// that would land at or before 1970-01-01 are reported as 0 because
// the identity comparison contract treats the zero pair as an absent
// (unsupported) component, never as a real timestamp.
func filetimeUnixNanos(ft windows.Filetime) int64 {
	ticks := int64(uint64(ft.HighDateTime)<<32 | uint64(ft.LowDateTime))
	if ticks <= 0 {
		return 0
	}
	const epochDiffTicks = int64(116444736000000000) // 1601 to 1970, 100 ns units
	ns := (ticks - epochDiffTicks) * 100
	if ns <= 0 {
		return 0
	}
	return ns
}

// identityFromPath resolves the canonical filesystem identity of a
// path using the Windows volume/file identity primitives: the volume
// serial number plus the file index from GetFileInformationByHandle
// (the NTFS file ID), with modification time and size as additional
// discriminators.
//
// The handle's CreationTime is captured as the birth-time component.
// This is the run 44330231458 Windows go-test repair: a delete+recreate
// can REUSE the same MFT record (volume serial + file index), and the
// recreated directory's modification time can coincide with the
// recorded one, so mtime alone cannot distinguish the old and the new
// directory instance. CreationTime is the per-instance discriminator
// on Windows; without it a stale confirmation against a same-file-id
// replacement was accepted (TestSameInodeReplacementRequiresReconfirmation
// failed with "same-(dev,ino) replacement accepted"). A filesystem that
// does not report a usable creation time yields the zero pair, which
// the comparison skips as absent.
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
	id := RootIdentity{
		Dev:       uint64(bi.VolumeSerialNumber),
		Ino:       uint64(bi.FileIndexHigh)<<32 | uint64(bi.FileIndexLow),
		MtimeSec:  mt.Unix(),
		MtimeNsec: int64(mt.Nanosecond()),
		Size:      fi.Size(),
	}
	if bn := filetimeUnixNanos(bi.CreationTime); bn > 0 {
		id.BtimeSec = bn / int64(time.Second)
		id.BtimeNsec = bn % int64(time.Second)
	}
	return id, nil
}

// infoIdentityIsPrimary reports whether identityOf(os.FileInfo) carries
// the platform's primary identity components. Windows FileInfo carries
// no file identity, so the handle-level re-verification is skipped and
// the authorization path relies on identityFromPath (real file IDs).
func infoIdentityIsPrimary() bool { return false }
