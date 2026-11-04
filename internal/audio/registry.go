package audio

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// SampleEncoding describes the raw interleaved PCM sample encoding a
// Decoder stream yields.
type SampleEncoding int

const (
	EncFloat32LE SampleEncoding = iota // interleaved float32 little-endian
	EncInt16LE                         // interleaved signed int16 little-endian
	EncInt24LE                         // interleaved signed int24 little-endian (3 bytes/sample)
	EncInt32LE                         // interleaved signed int32 little-endian
)

// SourceFormat describes one input container the app can decode.
type SourceFormat struct {
	Container     string   `json:"container"`     // "wav", "mp3", "flac", "ogg"
	Extensions    []string `json:"extensions"`    // claimed file extensions
	Classifier    string   `json:"classifier"`    // human description
	Lossy         bool     `json:"lossy"`         // true for lossy codecs
	NativeDecoder bool     `json:"nativeDecoder"` // first-party Go decoder
}

// Decoder turns one source file into a raw PCM stream plus metadata.
// A Decoder instance is owned by one goroutine (the import worker);
// implementations are not safe for concurrent use.
type Decoder interface {
	// Probe validates the stream and returns source metadata without
	// decoding the whole file.
	Probe() (Metadata, error)
	// PCMReader returns a stream of interleaved PCM samples laid out
	// as SourceFormat describes. It must surface io.EOF at end.
	PCMReader() (io.Reader, error)
	// SourceFormat returns the decoded PCM layout.
	SourceFormat() (rate, channels int, enc SampleEncoding)
	// Close releases file handles and decoder state.
	Close() error
}

// DecoderFactory opens path and returns a Decoder. When path is empty
// the factory returns only format metadata via a best-effort decoder
// (or ErrUnsupported when impossible); production factories require a
// real path.
type DecoderFactory func(path string) (Decoder, error)

var (
	regMu sync.RWMutex
	reg   = map[string]DecoderFactory{} // key: lowercase extension
	fmtMu sync.RWMutex
	fmts  = map[string]SourceFormat{} // key: container name
)

// RegisterFormat installs a decoder factory for the given extensions.
func RegisterFormat(format SourceFormat, f DecoderFactory) {
	if format.Container == "" || f == nil || len(format.Extensions) == 0 {
		panic("audio: RegisterFormat needs container, extensions and factory")
	}
	regMu.Lock()
	for _, ext := range format.Extensions {
		reg[strings.ToLower(strings.TrimPrefix(ext, "."))] = f
	}
	regMu.Unlock()
	fmtMu.Lock()
	fmts[format.Container] = format
	fmtMu.Unlock()
}

// SupportedFormats lists registered containers in registration-stable
// order (sorted by container name).
func SupportedFormats() []SourceFormat {
	fmtMu.RLock()
	defer fmtMu.RUnlock()
	names := make([]string, 0, len(fmts))
	for n := range fmts {
		names = append(names, n)
	}
	sortStrings(names)
	out := make([]SourceFormat, 0, len(names))
	for _, n := range names {
		out = append(out, fmts[n])
	}
	return out
}

// Open chooses a decoder for path by extension and returns it together
// with the format description. Unknown extensions fail with
// ErrUnsupported before any file is opened.
func Open(path string) (Decoder, SourceFormat, error) {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	regMu.RLock()
	f, ok := reg[ext]
	regMu.RUnlock()
	if !ok {
		return nil, SourceFormat{}, fmt.Errorf("%w: unknown extension %q", ErrUnsupported, ext)
	}
	d, err := f(path)
	if err != nil {
		return nil, SourceFormat{}, err
	}
	fmtMu.RLock()
	var sf SourceFormat
	for _, v := range fmts {
		if v.Container != "" {
			// resolve below
		}
	}
	fmtMu.RUnlock()
	container := containerFor(ext)
	fmtMu.RLock()
	sf = fmts[container]
	fmtMu.RUnlock()
	return d, sf, nil
}

func containerFor(ext string) string {
	fmtMu.RLock()
	defer fmtMu.RUnlock()
	for name, sf := range fmts {
		for _, e := range sf.Extensions {
			if strings.TrimPrefix(e, ".") == ext {
				return name
			}
		}
	}
	return ""
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// fileSize returns the size of path or 0 when unavailable.
func fileSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// limitedReader bounds reads and counts bytes for progress.
type limitedReader struct {
	r   io.Reader
	rem int64
}

func (l *limitedReader) Read(p []byte) (int, error) {
	if l.rem <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > l.rem {
		p = p[:l.rem]
	}
	n, err := l.r.Read(p)
	l.rem -= int64(n)
	return n, err
}
