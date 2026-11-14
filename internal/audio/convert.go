package audio

import "math"

// convert.go — sample conversion helpers: integer PCM → float32 and
// channel downmix/upmix. All conversions are clipping-safe and
// deterministic. Full-scale int → ±1.0 mapping:
//
//      int16: v / 32768     int24: v / 8388608
//      int32: v / 2147483648 (shift-based, keeps symmetry)

// int16ToFloat32 converts a slice of interleaved int16 LE samples.
func int16ToFloat32(in []byte, out []float32) int {
	n := len(in) / 2
	if n > len(out) {
		n = len(out)
	}
	for i := 0; i < n; i++ {
		v := int16(uint16(in[2*i]) | uint16(in[2*i+1])<<8)
		out[i] = float32(v) / 32768.0
	}
	return n
}

// int24ToFloat32 converts interleaved signed 24-bit LE samples.
func int24ToFloat32(in []byte, out []float32) int {
	n := len(in) / 3
	if n > len(out) {
		n = len(out)
	}
	for i := 0; i < n; i++ {
		v := int32(in[3*i]) | int32(in[3*i+1])<<8 | int32(in[3*i+2])<<16
		if v&0x800000 != 0 {
			v |= ^int32(0xFFFFFF) // sign-extend 24 → 32 bits
		}
		out[i] = float32(v) / 8388608.0
	}
	return n
}

// int32ToFloat32 converts interleaved signed 32-bit LE samples.
func int32ToFloat32(in []byte, out []float32) int {
	n := len(in) / 4
	if n > len(out) {
		n = len(out)
	}
	for i := 0; i < n; i++ {
		v := int32(uint32(in[4*i]) | uint32(in[4*i+1])<<8 | uint32(in[4*i+2])<<16 | uint32(in[4*i+3])<<24)
		out[i] = float32(v) / 2147483648.0
	}
	return n
}

// downmix converts `from`-channel interleaved float32 into `to`-channel
// interleaved float32, accumulating with equal power (mean) for the
// mono→stereo case and plain mean for the stereo→mono case. Returns
// the number of interleaved output samples written. Input that is not
// a whole number of frames is truncated (defensive; the import path
// guarantees alignment via frameAlignReader).
func downmix(in []float32, from, to int, out []float32) int {
	if from == to {
		n := copy(out, in)
		return n
	}
	written := 0
	for f := 0; (f+1)*from <= len(in) && written+to <= len(out); f++ {
		base := f * from
		switch {
		case from == 1 && to == 2:
			s := in[base]
			out[written] = s
			out[written+1] = s
			written += 2
		case to == 1:
			var acc float64
			for c := 0; c < from; c++ {
				acc += float64(in[base+c])
			}
			out[written] = float32(acc / float64(from))
			written++
		default:
			// General N→M: mean of input scaled across outputs.
			for c := 0; c < to; c++ {
				out[written+c] = in[base+c%from]
			}
			written += to
		}
	}
	return written
}

// floatToBytesLE encodes interleaved float32 samples little-endian
// (native encoding on all supported platforms; explicit for clarity).
func floatToBytesLE(in []float32, out []byte) int {
	n := len(in)
	if len(out) < 4*n {
		n = len(out) / 4
	}
	for i := 0; i < n; i++ {
		bits := math.Float32bits(in[i])
		out[4*i] = byte(bits)
		out[4*i+1] = byte(bits >> 8)
		out[4*i+2] = byte(bits >> 16)
		out[4*i+3] = byte(bits >> 24)
	}
	return n * 4
}

// bytesLEToFloat decodes interleaved float32 LE bytes. Non-finite
// values (NaN/±Inf — craftable inside a float32 WAV or ffmpeg f32le
// output) are mapped to 0: audio samples must be finite, and NaN
// would otherwise propagate through the resampler (its ±1 clamps are
// false for NaN), the DSP and the visualizer (run 43541087097 repair).
func bytesLEToFloat(in []byte, out []float32) int {
	n := len(in) / 4
	if n > len(out) {
		n = len(out)
	}
	for i := 0; i < n; i++ {
		bits := uint32(in[4*i]) | uint32(in[4*i+1])<<8 | uint32(in[4*i+2])<<16 | uint32(in[4*i+3])<<24
		v := math.Float32frombits(bits)
		switch {
		case math.IsNaN(float64(v)) || math.IsInf(float64(v), 0):
			v = 0
		case v > 1:
			v = 1
		case v < -1:
			v = -1
		}
		out[i] = v
	}
	return n
}

// DecodeFloat32LE decodes interleaved float32 little-endian bytes into
// out (exported for the studio analysis stream).
func DecodeFloat32LE(in []byte, out []float32) int { return bytesLEToFloat(in, out) }

// EncodeFloat32LE encodes interleaved float32 samples as LE bytes.
func EncodeFloat32LE(in []float32, out []byte) int { return floatToBytesLE(in, out) }
