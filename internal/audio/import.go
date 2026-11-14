package audio

// import.go — the import pipeline: one owned worker decodes the source
// file, converts to canonical PCM (48 kHz stereo float32) and writes
// the disk-backed cache atomically. Nothing here decodes on the UI
// thread; cancellation is honored between chunks. Bounded scratch
// buffers only — a long track never lives in RAM.

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ImportedTrack is the result of a successful import.
type ImportedTrack struct {
	Fingerprint string   `json:"fingerprint"`
	SourceName  string   `json:"sourceName"`
	Metadata    Metadata `json:"metadata"`
	Frames      uint64   `json:"frames"`   // canonical frames
	Duration    float64  `json:"duration"` // seconds
	Cache       *Cache   `json:"-"`
	Warnings    []string `json:"warnings,omitempty"`
}

// ResamplerID identifies the resampling pipeline version.
const ResamplerID = "kaiser-sinc32-b8.6-v1"

// DecoderIDs maps containers to the decoder modules used.
var DecoderIDs = map[string]string{
	"wav":  "go-audio/wav v1.1.0 (int PCM) + first-party float32 RIFF reader",
	"mp3":  "llehouerou/go-mp3 v1.5.0",
	"flac": "tphakala/go-flac v1.4.0 (/pcm)",
	"ogg":  "jfreymuth/oggvorbis v1.0.5",
}

// Progress reports import progress (canonical frames done; total
// estimate when known, 0 otherwise).
type Progress func(framesDone, framesTotal uint64)

// Import imports path into cache. On success the track owns a
// complete, validated cache entry; on failure no partial entry is
// left behind.
func Import(ctx context.Context, path string, cache *Cache, prog Progress) (*ImportedTrack, error) {
	if cache == nil {
		return nil, fmt.Errorf("audio: nil cache")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := checkSourceSize(path); err != nil {
		return nil, err
	}
	fp, err := fingerprintFile(path)
	if err != nil {
		return nil, err
	}
	// Cache hit?
	if sc, lerr := cache.Lookup(fp); lerr == nil {
		return trackFromSidecar(cache, sc), nil
	}
	dec, sf, err := Open(path)
	if err != nil {
		return nil, err
	}
	defer dec.Close()
	// Context-aware adapters (the optional FFmpeg AAC family) kill
	// their child process on cancellation; native decoders ignore
	// this no-op hook and honor ctx between chunks as before.
	attachDecodeContext(dec, ctx)

	md, err := dec.Probe()
	if err != nil {
		return nil, err
	}
	srcRate, srcCh, enc := dec.SourceFormat()
	if srcRate <= 0 || srcCh <= 0 || srcCh > MaxChannels {
		return nil, fmt.Errorf("audio: %s has invalid source format %d Hz %d ch", sf.Container, srcRate, srcCh)
	}
	pcmr, err := dec.PCMReader()
	if err != nil {
		return nil, err
	}
	// Frame alignment (run 43541087097 critical repair): every
	// decoder chunk boundary may split a sample frame (e.g. FFmpeg
	// f32le with 3/5/6/7 channels: 64 KiB is not a multiple of the
	// frame size). The alignment reader carries the partial tail
	// bytes into the next read so no sample is ever dropped and the
	// stream can never silently misalign into garbage.
	bpf := srcCh * bytesPerSample(enc)
	if bpf <= 0 {
		return nil, fmt.Errorf("audio: %s reports invalid frame size (%d ch)", sf.Container, srcCh)
	}
	return decodeToCache(ctx, cache, fp, filepath.Base(path), md, srcRate, srcCh, enc, &frameAlignReader{r: pcmr, frame: bpf}, prog)
}

// bytesPerSample is the wire width of one sample in an encoding.
func bytesPerSample(enc SampleEncoding) int {
	switch enc {
	case EncInt16LE:
		return 2
	case EncInt24LE:
		return 3
	case EncInt32LE, EncFloat32LE:
		return 4
	default:
		return 0
	}
}

// frameAlignReader guarantees every Read returns a byte count that is
// a multiple of the frame size, carrying partial frames between calls.
// It exists because decodeChunk floors to whole samples; without it,
// len(b) % frame bytes were silently discarded at every chunk
// boundary, permanently misaligning the stream (reproduced with a
// 24-bit stereo stream split at 64 KiB: divergence at sample 21845).
type frameAlignReader struct {
	r     io.Reader
	frame int
	carry []byte // pending partial-frame bytes (< frame)
}

func (a *frameAlignReader) Read(p []byte) (int, error) {
	if len(p) < a.frame {
		return 0, fmt.Errorf("audio: frameAlignReader buffer (%d bytes) smaller than one frame (%d bytes)", len(p), a.frame)
	}
	var n int
	// 1. Flush carried bytes first (always fewer than one frame).
	if len(a.carry) > 0 {
		n = copy(p, a.carry)
		a.carry = a.carry[n:]
		if len(a.carry) > 0 {
			return n, nil // p is full before the carry drained
		}
	}
	// 2. Read from the source into the remainder of p.
	m, err := a.r.Read(p[n:])
	total := n + m
	// 3. Trim any partial frame into carry so the returned count is
	// always a whole number of frames. A partial frame at EOF is
	// dropped (an incomplete final frame is not audio).
	if rem := total % a.frame; rem != 0 {
		cut := total - rem
		a.carry = append(a.carry[:0], p[cut:total]...)
		return cut, err
	}
	return total, err
}

// Scratch buffer sizes (bounded; ~1 MiB total).
const (
	readChunkBytes = 64 << 10                // 64 KiB source bytes per step
	convCap        = 64 << 10                // max decoded samples per step (int16: 2 B/sample)
	stereoCap      = 2 * convCap             // worst case mono→stereo
	rsOutCap       = stereoCap + stereoCap/8 // headroom for 44.1k→48k
	writeBufCap    = rsOutCap * 4            // float32 → bytes
)

// decodeToCache runs the owned decode loop. It is the single place a
// non-thread-safe decoder instance is consumed.
func decodeToCache(
	ctx context.Context, cache *Cache, fp, baseName string,
	md Metadata, srcRate, srcCh int, enc SampleEncoding, pcmr io.Reader,
	prog Progress,
) (track *ImportedTrack, err error) {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	raw, err := openAppend(cache.RawPath(fp))
	if err != nil {
		return nil, fmt.Errorf("audio: open cache raw: %w", err)
	}
	wavf, err := openAppend(cache.WavPath(fp))
	if err != nil {
		raw.abort()
		return nil, fmt.Errorf("audio: open cache wav: %w", err)
	}
	// provisional IEEE-float WAV header; sizes are patched by
	// trimToFrames once the final frame count is known
	if herr := writeWavHeader(wavf, 0); herr != nil {
		raw.abort()
		wavf.abort()
		return nil, fmt.Errorf("audio: wav header: %w", herr)
	}
	defer func() {
		if err != nil {
			raw.abort()
			wavf.abort()
			cache.Delete(fp)
		}
	}()

	needResample := srcRate != CanonicalSampleRate || srcCh != CanonicalChannels
	var rs *Resampler
	if needResample {
		rs, err = NewResampler(srcRate, CanonicalSampleRate, CanonicalChannels)
		if err != nil {
			return nil, err
		}
	}

	conv := make([]float32, convCap)
	stereo := make([]float32, stereoCap)
	rsOut := make([]float32, rsOutCap)
	buf := make([]byte, writeBufCap)
	chunk := make([]byte, readChunkBytes)

	var srcFrames, outFrames uint64

	// feedResampler pushes stereo samples through the resampler (or
	// straight to the writers when no resampling is needed) and drains
	// all produced output. The input is appended to the resampler queue
	// exactly once; the drain loop feeds nil so nothing is duplicated.
	feedResampler := func(in []float32) error {
		if rs != nil {
			if w := rs.Process(in, rsOut); w > 0 {
				if werr := writeCanonical(rsOut[:w], buf, wavf, raw, &outFrames); werr != nil {
					return werr
				}
			}
			for {
				w := rs.Process(nil, rsOut)
				if w == 0 {
					break
				}
				if werr := writeCanonical(rsOut[:w], buf, wavf, raw, &outFrames); werr != nil {
					return werr
				}
			}
		} else {
			if werr := writeCanonical(in, buf, wavf, raw, &outFrames); werr != nil {
				return werr
			}
		}
		return nil
	}

	// pump converts+downmixes one decoded byte chunk into the outputs.
	pump := func(b []byte) error {
		n := decodeChunk(enc, b, conv)
		if n == 0 {
			return nil
		}
		srcFrames += uint64(n / srcCh)
		// Early duration bound (run 43541087097 repair): the
		// limit is enforced DURING the decode, not after the
		// whole (possibly huge) cache write, so a small
		// low-bitrate file claiming hours of audio cannot fill
		// the disk before being rejected.
		if srcFrames > MaxDurationSeconds*uint64(srcRate) {
			err := fmt.Errorf("%w: %s", ErrUnsupported, truncateWarning("decoded duration", MaxDurationSeconds*CanonicalSampleRate))
			return err
		}
		st := downmix(conv[:n], srcCh, CanonicalChannels, stereo)
		if st == 0 {
			return nil
		}
		return feedResampler(stereo[:st])
	}
	// decode loop
	for {
		if cerr := ctx.Err(); cerr != nil {
			return nil, cerr
		}
		n, rerr := pcmr.Read(chunk)
		if n > 0 {
			if perr := pump(chunk[:n]); perr != nil {
				return nil, perr
			}
			if prog != nil {
				prog(srcFrames, 0)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return nil, fmt.Errorf("audio: decode %s: %w", md.Container, rerr)
		}
		if n == 0 && rerr == nil {
			// zero read without EOF: avoid a hot spin on a wedged
			// reader by treating it as end of stream.
			break
		}
	}
	// Resampler tail flush: feed zero frames once, then drain.
	if rs != nil {
		if ferr := feedResampler(rs.FlushFeed()); ferr != nil {
			return nil, ferr
		}
		for {
			w := rs.Process(nil, rsOut)
			if w == 0 {
				break
			}
			if werr := writeCanonical(rsOut[:w], buf, wavf, raw, &outFrames); werr != nil {
				return nil, werr
			}
		}
	}

	// Exact canonical frame count via integer math:
	// floor(srcFrames * 48000 / srcRate).
	expected := srcFrames * CanonicalSampleRate / uint64(srcRate)
	if expected == 0 {
		return nil, fmt.Errorf("audio: %s decoded to zero frames", md.Container)
	}
	if expected > MaxDurationSeconds*CanonicalSampleRate {
		return nil, fmt.Errorf("%w: %s", ErrUnsupported, truncateWarning("decoded duration", MaxDurationSeconds*CanonicalSampleRate))
	}
	if terr := trimToFrames(wavf, raw, expected); terr != nil {
		return nil, terr
	}
	// Finalize both cache files (sync + atomic rename into place).
	if ferr := raw.finish(); ferr != nil {
		return nil, ferr
	}
	if ferr := wavf.finish(); ferr != nil {
		return nil, ferr
	}

	dur := float64(expected) / CanonicalSampleRate
	sc := &Sidecar{
		Version:         SidecarVersion,
		Fingerprint:     fp,
		SourceName:      baseName,
		SourceSize:      md.FileSize,
		Container:       md.Container,
		Codec:           md.Codec,
		SampleRate:      srcRate,
		Channels:        srcCh,
		BitDepth:        md.BitDepth,
		Duration:        dur,
		Frames:          expected,
		Decoder:         DecoderIDs[md.Container],
		Resampler:       resamplerID(needResample),
		AnalysisVersion: 0,
		Warnings:        md.Warnings,
	}
	if serr := cache.storeSidecarLocked(sc); serr != nil {
		return nil, serr
	}
	md.Duration = dur
	md.DurationText = DurationText(dur)
	md.FormatText = FormatText(md)
	return &ImportedTrack{
		Fingerprint: fp,
		SourceName:  baseName,
		Metadata:    md,
		Frames:      expected,
		Duration:    dur,
		Cache:       cache,
	}, nil
}

// decodeChunk converts one raw byte chunk to float32 per enc.
func decodeChunk(enc SampleEncoding, b []byte, out []float32) int {
	switch enc {
	case EncFloat32LE:
		return bytesLEToFloat(b, out)
	case EncInt16LE:
		return int16ToFloat32(b, out)
	case EncInt24LE:
		return int24ToFloat32(b, out)
	case EncInt32LE:
		return int32ToFloat32(b, out)
	default:
		return 0
	}
}

// writeCanonical writes interleaved stereo float32 samples to both
// cache files, byte-buffered.
func writeCanonical(st []float32, buf []byte, wavf, raw *appendFile, outFrames *uint64) error {
	for off := 0; off < len(st); {
		n := floatToBytesLE(st[off:], buf)
		if n == 0 {
			break
		}
		if _, err := raw.Write(buf[:n]); err != nil {
			return fmt.Errorf("audio: cache raw write: %w", err)
		}
		if _, err := wavf.Write(buf[:n]); err != nil {
			return fmt.Errorf("audio: cache wav write: %w", err)
		}
		wrote := n / 4
		off += wrote
		*outFrames += uint64(wrote) / CanonicalChannels
	}
	return nil
}

// trimToFrames truncates both cache files to exactly want canonical
// frames and patches the WAV RIFF/data size fields.
func trimToFrames(wavf, raw *appendFile, want uint64) error {
	rawBytes := int64(want) * 8
	if fi, err := os.Stat(raw.tmp); err == nil && fi.Size() > rawBytes {
		if err := raw.f.Truncate(rawBytes); err != nil {
			return fmt.Errorf("audio: trim raw: %w", err)
		}
	}
	wavBytes := rawBytes + 44
	if fi, err := os.Stat(wavf.tmp); err == nil && fi.Size() > wavBytes {
		if err := wavf.f.Truncate(wavBytes); err != nil {
			return fmt.Errorf("audio: trim wav: %w", err)
		}
	}
	b := make([]byte, 4)
	binaryLEPutUint32(b, uint32(36+rawBytes))
	if _, err := wavf.f.WriteAt(b, 4); err != nil {
		return fmt.Errorf("audio: wav header patch: %w", err)
	}
	binaryLEPutUint32(b, uint32(rawBytes))
	if _, err := wavf.f.WriteAt(b, 40); err != nil {
		return fmt.Errorf("audio: wav header patch: %w", err)
	}
	return nil
}

func binaryLEPutUint32(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}

func resamplerID(needed bool) string {
	if !needed {
		return "none (source already 48000 Hz stereo)"
	}
	return ResamplerID
}

// trackFromSidecar rebuilds an ImportedTrack from a validated sidecar.
func trackFromSidecar(cache *Cache, sc *Sidecar) *ImportedTrack {
	return &ImportedTrack{
		Fingerprint: sc.Fingerprint,
		SourceName:  sc.SourceName,
		Metadata: Metadata{
			Title:        strings.TrimSuffix(sc.SourceName, filepath.Ext(sc.SourceName)),
			Container:    sc.Container,
			Codec:        sc.Codec,
			SampleRate:   sc.SampleRate,
			Channels:     sc.Channels,
			BitDepth:     sc.BitDepth,
			Duration:     sc.Duration,
			FileSize:     sc.SourceSize,
			DurationText: DurationText(sc.Duration),
			FormatText: FormatText(Metadata{
				Container: sc.Container, BitDepth: sc.BitDepth,
				SampleRate: sc.SampleRate, Channels: sc.Channels,
			}),
		},
		Frames:   sc.Frames,
		Duration: sc.Duration,
		Cache:    cache,
	}
}
