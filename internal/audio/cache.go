package audio

// cache.go — disk-backed canonical PCM cache.
//
// Canonical format: 48000 Hz, stereo, float32, little-endian,
// interleaved (frames*8 bytes). Three artifacts per track, keyed by
// source fingerprint (never by filename or mtime):
//
//	<fp>.f32   raw canonical interleaved float32 samples
//	<fp>.wav   canonical WAV wrapper (IEEE float32) used for export
//	           audio input and provenance inspection
//	<fp>.json  sidecar metadata/provenance record
//
// All writes are atomic (temp file + rename) so a crash or cancelled
// import never leaves a half cache entry that could be mistaken for a
// complete one.

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// SidecarVersion bumps when sidecar semantics change.
const SidecarVersion = 1

// ErrCacheCorrupt marks cache entries that fail validation.
var ErrCacheCorrupt = errors.New("audio: canonical cache entry corrupt")

// Sidecar is the provenance record of one canonical cache entry.
type Sidecar struct {
	Version         int      `json:"version"`
	Fingerprint     string   `json:"fingerprint"`
	SourceName      string   `json:"sourceName"` // base name only (no full path — privacy)
	SourceSize      int64    `json:"sourceSize"` // bytes
	Container       string   `json:"container"`
	Codec           string   `json:"codec"`
	SampleRate      int      `json:"sampleRate"` // source
	Channels        int      `json:"channels"`   // source
	BitDepth        int      `json:"bitDepth"`   // source; 0 for lossy
	Duration        float64  `json:"duration"`   // seconds, exact (frames/48000)
	Frames          uint64   `json:"frames"`     // canonical frames (per-channel samples)
	Decoder         string   `json:"decoder"`    // decoder identity
	Resampler       string   `json:"resampler"`  // resampler identity (or "none")
	AnalysisVersion int      `json:"analysisVersion"`
	Warnings        []string `json:"warnings,omitempty"`
}

// Cache manages a canonical PCM cache directory.
type Cache struct {
	dir string
	mu  sync.Mutex // serializes entry writes; reads are independent
}

// NewCache opens (creating if needed) a canonical cache directory.
func NewCache(dir string) (*Cache, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("audio: cache dir must not be empty")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("audio: cannot create cache dir: %w", err)
	}
	return &Cache{dir: dir}, nil
}

// Dir returns the cache directory.
func (c *Cache) Dir() string { return c.dir }

// RawPath returns the raw float32 cache path for a fingerprint.
func (c *Cache) RawPath(fp string) string { return filepath.Join(c.dir, fp+".f32") }

// WavPath returns the canonical WAV path for a fingerprint.
func (c *Cache) WavPath(fp string) string { return filepath.Join(c.dir, fp+".wav") }

// SidecarPath returns the sidecar JSON path for a fingerprint.
func (c *Cache) SidecarPath(fp string) string { return filepath.Join(c.dir, fp+".json") }

// Lookup returns the sidecar of a valid, complete cache entry, or an
// error when absent/corrupt/incomplete.
func (c *Cache) Lookup(fp string) (*Sidecar, error) {
	b, err := os.ReadFile(c.SidecarPath(fp))
	if err != nil {
		return nil, err
	}
	var sc Sidecar
	if err := json.Unmarshal(b, &sc); err != nil {
		return nil, fmt.Errorf("%w: sidecar: %v", ErrCacheCorrupt, err)
	}
	if sc.Version != SidecarVersion || sc.Fingerprint != fp {
		return nil, fmt.Errorf("%w: sidecar version/fingerprint mismatch", ErrCacheCorrupt)
	}
	if sc.Frames == 0 || sc.Duration <= 0 {
		return nil, fmt.Errorf("%w: empty frames", ErrCacheCorrupt)
	}
	fi, err := os.Stat(c.RawPath(fp))
	if err != nil {
		return nil, err
	}
	if fi.Size() != int64(sc.Frames)*8 { // stereo float32
		return nil, fmt.Errorf("%w: raw size %d != %d", ErrCacheCorrupt, fi.Size(), int64(sc.Frames)*8)
	}
	if fi2, err := os.Stat(c.WavPath(fp)); err != nil || fi2.Size() <= 44 {
		return nil, fmt.Errorf("%w: wav missing", ErrCacheCorrupt)
	}
	return &sc, nil
}

// StoreSidecar atomically writes the sidecar JSON.
func (c *Cache) StoreSidecar(sc *Sidecar) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.storeSidecarLocked(sc)
}

// storeSidecarLocked writes the sidecar without re-locking (used by
// the import worker, which already holds the cache mutex).
func (c *Cache) storeSidecarLocked(sc *Sidecar) error {
	b, err := json.MarshalIndent(sc, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(c.SidecarPath(sc.Fingerprint), b)
}

// Reader opens the canonical raw samples for sequential or seek reads.
func (c *Cache) Reader(fp string) (*os.File, error) {
	return os.Open(c.RawPath(fp))
}

// OpenWAV opens the canonical WAV wrapper.
func (c *Cache) OpenWAV(fp string) (*os.File, error) {
	return os.Open(c.WavPath(fp))
}

// Delete removes all artifacts of one entry (used by failed imports).
func (c *Cache) Delete(fp string) {
	for _, p := range []string{c.RawPath(fp), c.WavPath(fp), c.SidecarPath(fp)} {
		_ = os.Remove(p)
	}
}

// atomicWrite writes data to a temp file in the same directory and
// renames it into place.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".sheytan-tmp-*")
	if err != nil {
		return fmt.Errorf("audio: temp write: %w", err)
	}
	name := tmp.Name()
	_, werr := tmp.Write(data)
	if werr == nil {
		werr = tmp.Sync()
	}
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(name)
		if werr != nil {
			return werr
		}
		return cerr
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("audio: atomic rename: %w", err)
	}
	return nil
}

// appendFile opens a temp raw/wav file for streaming append and
// finishes with an atomic rename.
type appendFile struct {
	f    *os.File
	path string
	tmp  string
}

func openAppend(path string) (*appendFile, error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".sheytan-audio-*")
	if err != nil {
		return nil, err
	}
	return &appendFile{f: tmp, path: path, tmp: tmp.Name()}, nil
}

func (a *appendFile) Write(p []byte) (int, error) { return a.f.Write(p) }

func (a *appendFile) finish() error {
	if a.f == nil {
		return nil
	}
	if err := a.f.Sync(); err != nil {
		a.f.Close()
		a.f = nil
		_ = os.Remove(a.tmp)
		return err
	}
	if err := a.f.Close(); err != nil {
		a.f = nil
		_ = os.Remove(a.tmp)
		return err
	}
	a.f = nil
	return os.Rename(a.tmp, a.path)
}

func (a *appendFile) abort() {
	if a.f != nil {
		_ = a.f.Close()
		a.f = nil
	}
	_ = os.Remove(a.tmp)
}

// writeWavHeader emits a canonical IEEE-float32 WAVE header for the
// given frame count.
func writeWavHeader(w *appendFile, frames uint64) error {
	const (
		rate = CanonicalSampleRate
		ch   = CanonicalChannels
		bps  = 4
	)
	dataLen := frames * uint64(ch) * uint64(bps)
	h := make([]byte, 44)
	copy(h[0:4], "RIFF")
	binary.LittleEndian.PutUint32(h[4:8], uint32(36+dataLen))
	copy(h[8:12], "WAVE")
	copy(h[12:16], "fmt ")
	binary.LittleEndian.PutUint32(h[16:20], 16)
	binary.LittleEndian.PutUint16(h[20:22], 3) // IEEE float
	binary.LittleEndian.PutUint16(h[22:24], ch)
	binary.LittleEndian.PutUint32(h[24:28], rate)
	binary.LittleEndian.PutUint32(h[28:32], rate*ch*bps)
	binary.LittleEndian.PutUint16(h[32:34], ch*bps)
	binary.LittleEndian.PutUint16(h[34:36], bps*8)
	copy(h[36:40], "data")
	binary.LittleEndian.PutUint32(h[40:44], uint32(dataLen))
	_, err := w.Write(h)
	return err
}
