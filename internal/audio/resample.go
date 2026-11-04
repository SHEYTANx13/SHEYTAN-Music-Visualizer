package audio

import (
	"fmt"
	"math"
	"sync"
)

// sincTaps is the sinc kernel support: 32 taps with a Kaiser window
// (beta 8.6) gives >100 dB stopband attenuation, suitable for
// audio-grade resampling used by playback and export alike.
const sincTaps = 32

// flushFramesPerSide is how many zero frames the caller feeds after
// end-of-stream so the final kernel windows can be evaluated.
const flushFramesPerSide = sincTaps

// Resampler converts interleaved PCM between sample rates with a
// windowed-sinc (Kaiser-windowed) kernel. Linear interpolation is
// deliberately not used: it creates audible aliasing, which the
// product forbids for export quality.
//
// Design: an internal pending queue holds unconsumed input. An output
// frame at input-time t is produced only once its full kernel support
// [floor(t)-taps/2+1, floor(t)+taps/2] has been received; frames older
// than the support are dropped from the queue front. Samples before
// t=0 and after end-of-stream (+ zero flush) are treated as silence.
// The resampler is deterministic, allocation-bounded and owned by a
// single goroutine.
type Resampler struct {
	mu     sync.Mutex
	fromSR int
	toSR   int
	ch     int
	ratio  float64 // input frames per output frame (fromSR/toSR)

	pending []float32 // interleaved input frames not yet consumed
	offset  int       // absolute input-frame index of pending[0]
	t       float64   // output position in input-frame units
	taps    int
	kaiser  []float64
}

// NewResampler creates a resampler converting rate from → to for ch
// interleaved channels.
func NewResampler(from, to, ch int) (*Resampler, error) {
	if from <= 0 || to <= 0 || ch <= 0 || ch > MaxChannels {
		return nil, fmt.Errorf("audio: invalid resampler config from=%d to=%d ch=%d", from, to, ch)
	}
	r := &Resampler{
		fromSR: from, toSR: to, ch: ch,
		ratio: float64(from) / float64(to),
		taps:  sincTaps,
	}
	r.kaiser = kaiserWindow(sincTaps, 8.6)
	return r, nil
}

// Rates returns the source and target sample rates.
func (r *Resampler) Rates() (from, to int) { return r.fromSR, r.toSR }

// Reset clears all state for a fresh stream.
func (r *Resampler) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending = r.pending[:0]
	r.offset = 0
	r.t = 0
}

// EstimatedOutputFrames reports the expected interleaved output sample
// count for n input frames (before end flush).
func (r *Resampler) EstimatedOutputFrames(inFrames int) int {
	return int(math.Ceil(float64(inFrames) * float64(r.toSR) / float64(r.fromSR)))
}

// Process appends interleaved input (at fromSR) and writes converted
// interleaved samples (at toSR) into out, returning how many
// interleaved samples were written.
func (r *Resampler) Process(in []float32, out []float32) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending = append(r.pending, in...)
	return r.drain(out)
}

// FlushFeed returns a zero-filled buffer of flushFramesPerSide frames
// that the caller must pass to Process once at end of stream so the
// kernel can evaluate the tail window.
func (r *Resampler) FlushFeed() []float32 {
	return make([]float32, flushFramesPerSide*r.ch)
}

func (r *Resampler) drain(out []float32) int {
	have := len(r.pending) / r.ch
	outFrames := len(out) / r.ch
	written := 0
	for written < outFrames {
		last := int(math.Floor(r.t)) + r.taps/2 // absolute index of the last frame the kernel needs
		if last-r.offset >= have {              // that frame has not arrived yet
			break
		}
		for c := 0; c < r.ch; c++ {
			var acc float64
			for k := 0; k < r.taps; k++ {
				idx := int(math.Floor(r.t)) - r.taps/2 + 1 + k
				s := float64(r.pendingSample(idx, c))
				d := float64(idx) - r.t
				acc += s * sinc(d) * r.kaiser[k]
			}
			if acc > 1 {
				acc = 1
			} else if acc < -1 {
				acc = -1
			}
			out[written*r.ch+c] = float32(acc)
		}
		written++
		r.t += r.ratio
	}
	// Drop frames that can never again be needed: older than the
	// support start of the next output frame.
	keepFrom := int(math.Floor(r.t)) - r.taps/2 + 1
	if keepFrom > r.offset {
		drop := keepFrom - r.offset
		if drop > have {
			drop = have
		}
		if drop > 0 {
			copy(r.pending, r.pending[drop*r.ch:])
			r.pending = r.pending[:len(r.pending)-drop*r.ch]
			r.offset += drop
		}
	}
	// Return interleaved samples (the documented contract), not frames.
	return written * r.ch
}

// pendingSample reads input frame idx (absolute stream index) channel
// c from the pending queue; frames before 0 or not retained read 0.
func (r *Resampler) pendingSample(idx, c int) float32 {
	rel := idx - r.offset
	if rel < 0 || rel >= len(r.pending)/r.ch {
		return 0
	}
	return r.pending[rel*r.ch+c]
}

// sinc computes the (un-windowed) sinc of x.
func sinc(x float64) float64 {
	if x == 0 {
		return 1
	}
	px := math.Pi * x
	return math.Sin(px) / px
}

// kaiserWindow returns a Kaiser-Bessel window of n points, beta.
func kaiserWindow(n int, beta float64) []float64 {
	w := make([]float64, n)
	denom := besselI0(beta)
	for i := 0; i < n; i++ {
		t := 2.0*float64(i)/float64(n-1) - 1.0
		w[i] = besselI0(beta*math.Sqrt(math.Max(0, 1-t*t))) / denom
	}
	return w
}

// besselI0 computes the modified Bessel function of the first kind,
// order 0, via series expansion (stable for the betas used here).
func besselI0(x float64) float64 {
	sum := 1.0
	term := 1.0
	for k := 1; k < 32; k++ {
		term *= (x / 2) / float64(k)
		add := term * term
		sum += add
		if add < 1e-14*sum {
			break
		}
	}
	return sum
}
