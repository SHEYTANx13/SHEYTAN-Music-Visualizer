package audio

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// fingerprintFile computes a stable content fingerprint: SHA-256 over
// the file size and up to 16 evenly spaced 4 KiB windows. Sampling
// keeps large files cheap while remaining sensitive to real content
// changes. It identifies cache entries and feeds Song DNA; it is not a
// security primitive.
func fingerprintFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("audio: open for fingerprint: %w", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("audio: stat for fingerprint: %w", err)
	}
	h := sha256.New()
	var sizeBuf [8]byte
	binary.BigEndian.PutUint64(sizeBuf[:], uint64(fi.Size()))
	h.Write(sizeBuf[:])

	const window = 4096
	const windows = 16
	size := fi.Size()
	if size == 0 {
		return hex.EncodeToString(h.Sum(nil)), nil
	}
	buf := make([]byte, window)
	step := size / windows
	if step < window {
		step = window
	}
	for i := int64(0); i < windows; i++ {
		off := i * step
		if off+window > size {
			off = size - window
			if off < 0 {
				break
			}
		}
		if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
			return "", fmt.Errorf("audio: fingerprint read: %w", err)
		}
		h.Write(buf)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
