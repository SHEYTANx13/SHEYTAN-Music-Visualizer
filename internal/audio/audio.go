// Package audio owns local media import: the decoder registry, the
// first-party decoders (WAV, MP3, FLAC, Ogg/Vorbis), sample conversion,
// resampling and the disk-backed canonical PCM cache.
//
// Canonical PCM format: 48000 Hz, stereo, float32, little-endian,
// interleaved. The choice is fixed at the playback/analysis/export
// boundary; every importer converts into it through the same tested
// pipeline. A canonical cache file is always accompanied by a sidecar
// JSON describing provenance (source fingerprint, decoder, resampler).
//
// Decoders are NOT safe for concurrent use: each import job decodes in
// one owned worker goroutine and only the finished canonical cache is
// shared with playback, analysis and export.
package audio

import (
	"errors"
	"fmt"
	"strings"
)

// Canonical sample format at the playback/analysis/export boundary.
const (
	CanonicalSampleRate = 48000
	CanonicalChannels   = 2
	CanonicalFormat     = "float32-le"
)

// ErrUnsupported is returned when no registered decoder accepts a file.
var ErrUnsupported = errors.New("audio: unsupported format")

// Limits guard against adversarial or accidental unbounded input. They
// are generous for real music yet bounded; exceeding them fails the
// import with a clear error instead of allocating without limit.
const (
	// MaxSourceBytes bounds the encoded source file size (512 MiB).
	MaxSourceBytes = 512 << 20
	// MaxDurationSeconds bounds decoded duration (2 hours).
	MaxDurationSeconds = 2 * 60 * 60
	// MaxChannels bounds the input channel count.
	MaxChannels = 8
)

// Metadata is what the UI shows after a successful import probe.
type Metadata struct {
	Title        string   `json:"title"`
	Container    string   `json:"container"`
	Codec        string   `json:"codec"`
	SampleRate   int      `json:"sampleRate"`
	Channels     int      `json:"channels"`
	BitDepth     int      `json:"bitDepth"`
	Duration     float64  `json:"duration"`
	FileSize     int64    `json:"fileSize"`
	Classifier   string   `json:"classifier"`
	TagTitle     string   `json:"tagTitle,omitempty"`
	TagArtist    string   `json:"tagArtist,omitempty"`
	TagAlbum     string   `json:"tagAlbum,omitempty"`
	DurationText string   `json:"-"`
	FormatText   string   `json:"-"`
	Warnings     []string `json:"warnings,omitempty"`
}

// NormalizeArtistTitle returns display title/artist for a track,
// preferring embedded tags and falling back to the file base name.
func NormalizeArtistTitle(md Metadata, baseName string) (title, artist string) {
	t := strings.TrimSpace(md.TagTitle)
	a := strings.TrimSpace(md.TagArtist)
	if t == "" {
		t = baseName
	}
	if a == "" {
		a = "Unknown artist"
	}
	return t, a
}

// truncateWarning renders the shared over-limit refusal message.
func truncateWarning(what string, limit int64) string {
	return fmt.Sprintf("%s exceeds the %s limit; import refused", what, humanBytes(limit))
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%d GiB", n>>30)
	case n >= 1<<20:
		return fmt.Sprintf("%d MiB", n>>20)
	default:
		return fmt.Sprintf("%d KiB", n>>10)
	}
}

// hours cap rendering for duration messages.
const maxDurationHours = 2
