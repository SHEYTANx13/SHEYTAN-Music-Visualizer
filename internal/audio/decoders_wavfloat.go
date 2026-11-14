package audio

// decoders_wavfloat.go — a first-party streaming reader for IEEE-float
// RIFF/WAVE files (format tag 3). go-audio/wav v1.1.0 decodes integer
// PCM; float WAVs are common from DAW exports, and the product claims
// WAV support, so the gap is closed here without adding a dependency.
// The parser implements only the minimal RIFF/WAVE subset: fmt + data
// chunks, 32-bit IEEE float, 1..8 channels.

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
)

type wavFloatDecoder struct {
	f      *os.File
	rate   int
	ch     int
	bd     int
	dataAt int64
	dataSz int64
	md     Metadata
}

// NewWAVFloatDecoder opens path as an IEEE-float WAVE file; it fails
// fast when the file is not a float WAV so the registry can fall back
// to the integer decoder path.
func NewWAVFloatDecoder(path string) (Decoder, error) {
	if err := checkSourceSize(path); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("audio: open wav(f32): %w", err)
	}
	d := &wavFloatDecoder{f: f}
	if err := d.parseHeader(); err != nil {
		f.Close()
		return nil, err
	}
	return d, nil
}

func (w *wavFloatDecoder) parseHeader() error {
	hdr := make([]byte, 12)
	if _, err := io.ReadFull(w.f, hdr); err != nil {
		return fmt.Errorf("audio: wav(f32): short RIFF header: %w", err)
	}
	if string(hdr[0:4]) != "RIFF" || string(hdr[8:12]) != "WAVE" {
		return fmt.Errorf("audio: wav(f32): not a RIFF/WAVE file")
	}
	// Walk top-level chunks looking for fmt and data.
	var haveFmt bool
	for {
		ch8 := make([]byte, 8)
		if _, err := io.ReadFull(w.f, ch8); err != nil {
			return fmt.Errorf("audio: wav(f32): truncated chunk header: %w", err)
		}
		id := string(ch8[0:4])
		sz := int64(binary.LittleEndian.Uint32(ch8[4:8]))
		switch id {
		case "fmt ":
			// Bounded fmt-chunk read (run 43541087097 repair):
			// the header controls sz (uint32, up to 4 GiB); a
			// 60-byte malformed file claiming a huge fmt chunk
			// must fail fast instead of allocating gigabytes.
			if sz > 4096 {
				return fmt.Errorf("audio: wav(f32): implausible fmt chunk size %d", sz)
			}
			b := make([]byte, sz)
			if _, err := io.ReadFull(w.f, b); err != nil {
				return fmt.Errorf("audio: wav(f32): truncated fmt: %w", err)
			}
			if len(b) < 16 {
				return fmt.Errorf("audio: wav(f32): fmt chunk too short")
			}
			tag := binary.LittleEndian.Uint16(b[0:2])
			w.ch = int(binary.LittleEndian.Uint16(b[2:4]))
			w.rate = int(binary.LittleEndian.Uint32(b[4:8]))
			w.bd = int(binary.LittleEndian.Uint16(b[14:16]))
			if tag != 3 || w.bd != 32 {
				return fmt.Errorf("audio: wav(f32): not IEEE float32 (tag=%d bd=%d)", tag, w.bd)
			}
			if w.ch <= 0 || w.ch > MaxChannels || w.rate <= 0 {
				return fmt.Errorf("audio: wav(f32): invalid format %dHz %dch", w.rate, w.ch)
			}
			haveFmt = true
		case "data":
			if !haveFmt {
				return fmt.Errorf("audio: wav(f32): data before fmt")
			}
			w.dataAt, _ = w.f.Seek(0, io.SeekCurrent)
			w.dataSz = sz
			return nil
		default:
			// skip unknown chunks (odd sizes are padded)
			if sz&1 == 1 {
				sz++
			}
			if _, err := w.f.Seek(sz, io.SeekCurrent); err != nil {
				return fmt.Errorf("audio: wav(f32): skip chunk: %w", err)
			}
		}
		if sz <= 0 {
			return fmt.Errorf("audio: wav(f32): invalid chunk size")
		}
	}
}

func (w *wavFloatDecoder) Probe() (Metadata, error) {
	if w.md.FileSize == 0 {
		frames := w.dataSz / int64(w.ch*4)
		w.md = Metadata{
			Container:  "wav",
			Codec:      "PCM float32",
			SampleRate: w.rate,
			Channels:   w.ch,
			BitDepth:   32,
			Duration:   float64(frames) / float64(w.rate),
			FileSize:   fileSize(nameOfFile(w.f)),
			Classifier: "RIFF WAVE IEEE float32",
		}
		if w.md.Duration > MaxDurationSeconds {
			return w.md, fmt.Errorf("%w: %s", ErrUnsupported, truncateWarning("duration", MaxDurationSeconds))
		}
	}
	return w.md, nil
}

func (w *wavFloatDecoder) PCMReader() (io.Reader, error) {
	return io.LimitReader(io.NewSectionReader(w.f, w.dataAt, w.dataSz), w.dataSz), nil
}

func (w *wavFloatDecoder) SourceFormat() (int, int, SampleEncoding) {
	return w.rate, w.ch, EncFloat32LE
}

func (w *wavFloatDecoder) Close() error { return w.f.Close() }
