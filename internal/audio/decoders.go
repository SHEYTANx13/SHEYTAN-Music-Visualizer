package audio

// decoders.go — first-party Go decoders for WAV, MP3, FLAC and
// Ogg/Vorbis. Each decoder wraps a third-party pure-Go codec behind
// the package's Decoder contract. Decoders are single-goroutine: the
// import worker owns one instance for probe + decode + close.

import (
	"fmt"
	"io"
	"os"
	"strings"

	goaudioint "github.com/go-audio/audio"
	goaudio "github.com/go-audio/wav"
	mp3dec "github.com/llehouerou/go-mp3"

	flacpcm "github.com/tphakala/go-flac/pcm"

	"github.com/jfreymuth/oggvorbis"
)

// enforce limits shared by all decoders.
func checkSourceSize(path string) error {
	if sz := fileSize(path); sz > MaxSourceBytes {
		return fmt.Errorf("%w: %s", ErrUnsupported, truncateWarning("source file", MaxSourceBytes))
	}
	return nil
}

// ---------------------------------------------------------------------------
// WAV (github.com/go-audio/wav v1.1.0, Apache-2.0)
// ---------------------------------------------------------------------------

type wavDecoder struct {
	f        *os.File
	d        *goaudio.Decoder
	md       Metadata
	ibufData []int // scratch shared with the streaming reader
}

// NewWAVDecoder opens path as a RIFF/WAVE file.
func NewWAVDecoder(path string) (Decoder, error) {
	if err := checkSourceSize(path); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("audio: open wav: %w", err)
	}
	d := goaudio.NewDecoder(f)
	if !d.IsValidFile() {
		f.Close()
		return nil, fmt.Errorf("audio: not a valid RIFF/WAVE file: %s", path)
	}
	return &wavDecoder{f: f, d: d}, nil
}

func (w *wavDecoder) Probe() (Metadata, error) {
	if w.md.FileSize == 0 {
		f := w.d.Format()
		dur, _ := w.d.Duration()
		bitDepth := w.d.SampleBitDepth()
		w.md = Metadata{
			Container:  "wav",
			Codec:      "PCM",
			SampleRate: f.SampleRate,
			Channels:   f.NumChannels,
			BitDepth:   int(bitDepth),
			Duration:   dur.Seconds(),
			FileSize:   fileSize(nameOfFile(w.f)),
			Classifier: fmt.Sprintf("RIFF WAVE PCM %d-bit", bitDepth),
		}
		if w.md.SampleRate <= 0 || w.md.Channels <= 0 || w.md.Channels > MaxChannels {
			return w.md, fmt.Errorf("audio: wav has invalid format %dHz %dch", w.md.SampleRate, w.md.Channels)
		}
		if w.md.Duration > MaxDurationSeconds {
			return w.md, fmt.Errorf("%w: %s", ErrUnsupported, truncateWarning("duration", MaxDurationSeconds))
		}
	}
	return w.md, nil
}

func (w *wavDecoder) PCMReader() (io.Reader, error) {
	return &wavReader{w: w}, nil
}

func (w *wavDecoder) SourceFormat() (int, int, SampleEncoding) {
	f := w.d.Format()
	// go-audio/wav yields native-endian int samples; the reader below
	// converts to float32 immediately, so the wire format is float32.
	return f.SampleRate, f.NumChannels, EncFloat32LE
}

func (w *wavDecoder) Close() error { return w.f.Close() }

// wavReader streams the decoder through a bounded buffer, converting
// go-audio int buffers to interleaved float32.
type wavReader struct {
	w    *wavDecoder
	buf  []float32 // pending converted samples
	pos  int
	err  error
	eof  bool
	ibuf []int // go-audio int data scratch
}

func (r *wavReader) Read(p []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	// refill converted float buffer when drained
	for r.pos*4 >= len(r.buf)*4 && !r.eof {
		if err := r.fill(); err != nil {
			return 0, err
		}
	}
	avail := (len(r.buf) - r.pos) * 4
	if avail == 0 {
		if r.eof {
			r.err = io.EOF
			return 0, io.EOF
		}
		return 0, nil
	}
	n := len(p)
	if n > avail {
		n = avail
	}
	floatToBytesLE(r.buf[r.pos:r.pos+n/4], p[:n])
	r.pos += n / 4
	return n, nil
}

// fill decodes the next bounded chunk of WAV PCM into r.buf as float32.
func (r *wavReader) fill() error {
	_, ch, _ := r.w.SourceFormat()
	if r.ibuf == nil {
		r.ibuf = make([]int, 4096*ch)
		r.buf = make([]float32, 0, 4096*ch)
	}
	// Pre-set Data to the full scratch so PCMBuffer reads a bounded
	// batch (its read size = len(Data) * bytesPerSample).
	r.w.ibufData = r.ibuf
	intbuf := &goaudioint.IntBuffer{Format: r.w.d.Format(), Data: r.ibuf}
	n, err := r.w.d.PCMBuffer(intbuf)
	if err != nil {
		r.err = fmt.Errorf("audio: wav decode: %w", err)
		return r.err
	}
	if n == 0 {
		r.eof = true
		return nil
	}
	// Convert go-audio int data to float32 in [-1,1] based on bit depth.
	bd := int(r.w.d.SampleBitDepth())
	if bd == 0 {
		bd = 16
	}
	scale := float32(1.0) / float32(int64(1)<<(bd-1))
	r.buf = r.buf[:0]
	for _, v := range intbuf.Data[:n] {
		r.buf = append(r.buf, float32(v)*scale)
	}
	r.pos = 0
	return nil
}

func nameOfFile(f *os.File) string {
	if f == nil {
		return ""
	}
	return f.Name()
}

// ---------------------------------------------------------------------------
// MP3 (github.com/llehouerou/go-mp3 v1.5.0, Apache-2.0)
// Not safe for concurrent use: owned by one import worker.
// ---------------------------------------------------------------------------

type mp3Decoder struct {
	f  *os.File
	d  *mp3dec.Decoder
	md Metadata
}

// NewMP3Decoder opens path as an MPEG audio stream.
func NewMP3Decoder(path string) (Decoder, error) {
	if err := checkSourceSize(path); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("audio: open mp3: %w", err)
	}
	d, err := mp3dec.NewDecoder(f)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("audio: mp3 decode init: %w", err)
	}
	return &mp3Decoder{f: f, d: d}, nil
}

func (m *mp3Decoder) Probe() (Metadata, error) {
	if m.md.FileSize == 0 {
		m.md = Metadata{
			Container:  "mp3",
			Codec:      "MPEG audio",
			SampleRate: m.d.SampleRate(),
			Channels:   2, // MPEG-1 layer 3 decodes to stereo/mono; decoder reports via frame format
			Duration:   m.d.Duration().Seconds(),
			FileSize:   fileSize(nameOfFile(m.f)),
			Classifier: "MPEG Layer III (LAME gapless supported)",
		}
		if m.md.SampleRate <= 0 {
			return m.md, fmt.Errorf("audio: mp3 has invalid sample rate")
		}
		if m.md.Duration > MaxDurationSeconds {
			return m.md, fmt.Errorf("%w: %s", ErrUnsupported, truncateWarning("duration", MaxDurationSeconds))
		}
	}
	return m.md, nil
}

func (m *mp3Decoder) PCMReader() (io.Reader, error) { return m.d, nil }

func (m *mp3Decoder) SourceFormat() (int, int, SampleEncoding) {
	return m.d.SampleRate(), 2, EncInt16LE
}

func (m *mp3Decoder) Close() error { return m.f.Close() }

// ---------------------------------------------------------------------------
// FLAC (github.com/tphakala/go-flac v1.4.0 + /pcm, MIT)
// ---------------------------------------------------------------------------

type flacDecoder struct {
	f  *os.File
	d  *flacpcm.Decoder
	md Metadata
	bd int
}

// NewFLACDecoder opens path as a FLAC stream.
func NewFLACDecoder(path string) (Decoder, error) {
	if err := checkSourceSize(path); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("audio: open flac: %w", err)
	}
	d, err := flacpcm.NewDecoder(f)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("audio: flac decode init: %w", err)
	}
	return &flacDecoder{f: f, d: d}, nil
}

func (x *flacDecoder) Probe() (Metadata, error) {
	if x.md.FileSize == 0 {
		info := x.d.Info()
		x.bd = x.d.OutputBitDepth()
		x.md = Metadata{
			Container:  "flac",
			Codec:      "FLAC",
			SampleRate: info.SampleRate,
			Channels:   info.Channels,
			BitDepth:   x.bd,
			Duration:   float64(info.TotalSamples) / float64(info.SampleRate),
			FileSize:   fileSize(nameOfFile(x.f)),
			Classifier: fmt.Sprintf("FLAC %d-bit lossless", x.bd),
		}
		if x.md.SampleRate <= 0 || x.md.Channels <= 0 || x.md.Channels > MaxChannels {
			return x.md, fmt.Errorf("audio: flac has invalid format %dHz %dch", x.md.SampleRate, x.md.Channels)
		}
		if x.md.Duration > MaxDurationSeconds {
			return x.md, fmt.Errorf("%w: %s", ErrUnsupported, truncateWarning("duration", MaxDurationSeconds))
		}
	}
	return x.md, nil
}

func (x *flacDecoder) PCMReader() (io.Reader, error) { return x.d, nil }

func (x *flacDecoder) SourceFormat() (int, int, SampleEncoding) {
	info := x.d.Info()
	bd := x.d.OutputBitDepth()
	switch bd {
	case 8, 16:
		return info.SampleRate, info.Channels, EncInt16LE
	case 32:
		return info.SampleRate, info.Channels, EncInt32LE
	default:
		return info.SampleRate, info.Channels, EncInt24LE
	}
}

func (x *flacDecoder) Close() error { return x.f.Close() }

// ---------------------------------------------------------------------------
// Ogg/Vorbis (github.com/jfreymuth/oggvorbis v1.0.5, MIT)
// ---------------------------------------------------------------------------

type oggDecoder struct {
	f  *os.File
	r  *oggvorbis.Reader
	md Metadata
}

// NewOGGDecoder opens path as an Ogg/Vorbis stream.
func NewOGGDecoder(path string) (Decoder, error) {
	if err := checkSourceSize(path); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("audio: open ogg: %w", err)
	}
	r, err := oggvorbis.NewReader(f)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("audio: ogg decode init: %w", err)
	}
	return &oggDecoder{f: f, r: r}, nil
}

func (o *oggDecoder) Probe() (Metadata, error) {
	if o.md.FileSize == 0 {
		o.md = Metadata{
			Container:  "ogg",
			Codec:      "Vorbis",
			SampleRate: o.r.SampleRate(),
			Channels:   o.r.Channels(),
			BitDepth:   0,
			Duration:   float64(o.r.Length()) / float64(o.r.SampleRate()),
			FileSize:   fileSize(nameOfFile(o.f)),
			Classifier: "Ogg Vorbis lossy",
		}
		if o.md.SampleRate <= 0 || o.md.Channels <= 0 || o.md.Channels > MaxChannels {
			return o.md, fmt.Errorf("audio: ogg has invalid format %dHz %dch", o.md.SampleRate, o.md.Channels)
		}
		if o.md.Duration > MaxDurationSeconds {
			return o.md, fmt.Errorf("%w: %s", ErrUnsupported, truncateWarning("duration", MaxDurationSeconds))
		}
	}
	return o.md, nil
}

func (o *oggDecoder) PCMReader() (io.Reader, error) {
	return &oggReader{o: o}, nil
}

func (o *oggDecoder) SourceFormat() (int, int, SampleEncoding) {
	return o.r.SampleRate(), o.r.Channels(), EncFloat32LE
}

func (o *oggDecoder) Close() error { return o.f.Close() }

// oggReader converts the vorbis float output into little-endian
// float32 bytes on the wire.
type oggReader struct {
	o       *oggDecoder
	samples []float32
	pos     int
	err     error
	eof     bool
}

func (r *oggReader) Read(p []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	ch := r.o.r.Channels()
	if r.pos*4 >= len(r.samples)*4 && !r.eof {
		if cap(r.samples) < 4096*ch {
			r.samples = make([]float32, 4096*ch)
		}
		buf := r.samples
		n := 0
		for n < len(buf) {
			m, err := r.o.r.Read(buf[n:])
			n += m
			if err != nil {
				if err == io.EOF {
					r.eof = true
				} else {
					r.err = fmt.Errorf("audio: ogg decode: %w", err)
					return 0, r.err
				}
				break
			}
		}
		r.samples = buf[:n]
		r.pos = 0
	}
	avail := (len(r.samples) - r.pos) * 4
	if avail == 0 {
		r.err = io.EOF
		return 0, io.EOF
	}
	n := len(p)
	if n > avail {
		n = avail
	}
	floatToBytesLE(r.samples[r.pos:r.pos+n/4], p[:n])
	r.pos += n / 4
	return n, nil
}

// newWAVAny sniffs the format tag: IEEE-float WAVs go through the
// first-party float parser, integer PCM through go-audio/wav.
func newWAVAny(path string) (Decoder, error) {
	if path == "" {
		return nil, fmt.Errorf("audio: path required")
	}
	if d, err := NewWAVFloatDecoder(path); err == nil {
		return d, nil
	}
	return NewWAVDecoder(path)
}

// RegisterAll registers the four native decoders. Call once at startup.
func RegisterAll() {
	RegisterFormat(SourceFormat{
		Container: "wav", Extensions: []string{"wav", "wave"},
		Classifier: "RIFF WAVE (PCM int16/int24/int32/float32)", NativeDecoder: true,
	}, newWAVAny)
	RegisterFormat(SourceFormat{
		Container: "mp3", Extensions: []string{"mp3"},
		Classifier: "MPEG Layer III (mp3)", Lossy: true, NativeDecoder: true,
	}, NewMP3Decoder)
	RegisterFormat(SourceFormat{
		Container: "flac", Extensions: []string{"flac"},
		Classifier: "FLAC lossless", NativeDecoder: true,
	}, NewFLACDecoder)
	RegisterFormat(SourceFormat{
		Container: "ogg", Extensions: []string{"ogg", "oga"},
		Classifier: "Ogg Vorbis", Lossy: true, NativeDecoder: true,
	}, NewOGGDecoder)
}

// FormatText renders the "format" line shown in the UI.
func FormatText(md Metadata) string {
	parts := []string{strings.ToUpper(md.Container)}
	if md.BitDepth > 0 {
		parts = append(parts, fmt.Sprintf("%d-bit", md.BitDepth))
	}
	parts = append(parts, fmt.Sprintf("%d Hz", md.SampleRate), fmt.Sprintf("%d ch", md.Channels))
	return strings.Join(parts, " · ")
}

// DurationText renders m:ss.
func DurationText(sec float64) string {
	if sec < 0 || sec > 36000 {
		return "--:--"
	}
	m := int(sec) / 60
	s := int(sec) % 60
	return fmt.Sprintf("%d:%02d", m, s)
}
