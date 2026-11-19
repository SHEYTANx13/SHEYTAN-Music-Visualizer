//go:build windows

package fileaccess

import (
	"encoding/binary"
	"fmt"
	"os"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// fileIdInfo mirrors the undocumented-in-x/sys FILE_ID_INFO structure
// returned by GetFileInformationByHandleEx with the FileIdInfo class:
//
//	typedef struct _FILE_ID_INFO {
//	    ULONG       VolumeSerialNumber;
//	    FILE_ID_128 FileId;
//	} FILE_ID_INFO;
//
// On NTFS the low 64 bits of FileId are the file reference number: the
// MFT record index in bits 0..47 and the record SEQUENCE NUMBER in bits
// 48..63. On ReFS the full 128 bits are the durable file ID.
type fileIdInfo struct {
	VolumeSerialNumber uint32
	FileId             [16]byte
}

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
// path from the OPENED directory handle, using the Windows generation
// discriminator required for the selected-root security contract:
// volume serial + 128-bit file ID + directory creation FILETIME.
//
// The 128-bit file identity comes from GetFileInformationByHandleEx
// (class FileIdInfo). On NTFS its low quad carries the MFT record
// sequence number, which NTFS increments every time a record is freed
// and reused by a new directory instance. This is the per-instance
// generation and it defeats BOTH known reuse classes:
//
//  1. Same-(volume, file-index) replacement: the recreated directory
//     can land on the SAME MFT record, but the sequence number
//     differs, so the file reference differs.
//  2. NTFS file-system tunneling: when a name is deleted and
//     recreated within the ~15 s tunnel window, NTFS can RESTORE the
//     original CreationTime, so a creation-time-only discriminator
//     can still accept a replacement. The sequence number cannot be
//     restored by tunneling.
//
// This is the run 44330231458 Windows go-test repair
// (TestSameInodeReplacementRequiresReconfirmation failed with
// "same-(dev,ino) replacement accepted"), hardened per the selected-root
// identity contract: identity is derived from the actual opened handle,
// and when the FileIdInfo class is unsupported (exFAT/FAT/network
// shares) the ByHandleFileInformation file index and the creation-time
// discriminator remain as the weaker fallback, with InoHigh absent.
func identityFromPath(path string) (RootIdentity, error) {
	h, err := os.Open(path)
	if err != nil {
		return RootIdentity{}, err
	}
	defer h.Close()
	handle := windows.Handle(h.Fd())
	var bi windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &bi); err != nil {
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
	// Upgrade to the documented 128-bit file identity. On success Ino
	// becomes the full 64-bit file reference (index + MFT sequence) and
	// InoHigh the upper quad. A filesystem that rejects the class keeps
	// the ByHandleFileInformation index and zero InoHigh; the creation
	// time captured above stays the per-instance discriminator there.
	var fii fileIdInfo
	if err := windows.GetFileInformationByHandleEx(
		handle, uint32(windows.FileIdInfo),
		(*byte)(unsafe.Pointer(&fii)), uint32(unsafe.Sizeof(fii))); err == nil {
		id.Ino = binary.LittleEndian.Uint64(fii.FileId[0:8])
		id.InoHigh = binary.LittleEndian.Uint64(fii.FileId[8:16])
	}
	return id, nil
}

// infoIdentityIsPrimary reports whether identityOf(os.FileInfo) carries
// the platform's primary identity components. Windows FileInfo carries
// no file identity, so the handle-level re-verification is skipped and
// the authorization path relies on identityFromPath (real file IDs).
func infoIdentityIsPrimary() bool { return false }
