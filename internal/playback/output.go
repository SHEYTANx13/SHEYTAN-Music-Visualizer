// Package playback owns the audio transport: one Oto context per
// process, a single player per active track, play/pause/stop/seek,
// volume, and a buffering-aware playhead clock.
//
// Clock honesty: the playhead is derived from samples actually handed
// to the output minus the device-buffered remainder (Oto
// Player.BufferedSize), never from wall-clock time since play started.
// The remaining device-output latency is documented: the position can
// lag the listener by at most one device buffer (~100 ms default), and
// this uncertainty is reported via ClockInfo.
package playback

import (
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/ebitengine/oto/v3"
)

// Canonical playback format matches the audio package's canonical PCM.
const (
	SampleRate    = 48000
	Channels      = 2
	BytesPerFrame = Channels * 4 // stereo float32
)

// ErrClosed is returned after the transport is closed.
var ErrClosed = fmt.Errorf("playback: transport closed")

// Output abstracts the audio device so transports can be tested
// without hardware.
type Output interface {
	// NewPlayer returns a player reading from r.
	NewPlayer(r io.Reader) Player
	// Close releases the device context.
	Close() error
}

// Player abstracts an oto player.
type Player interface {
	Play()
	Pause()
	Reset()
	Seek(offset int64, whence int) (int64, error)
	IsPlaying() bool
	BufferedSize() int
	SetVolume(volume float64)
	Volume() float64
	Err() error
	Close() error
}

// OtoOutput is the real Oto device output. One context per process;
// the context lives as long as the process (oto forbids closing and
// recreating contexts), so Close is a no-op that only suspends.
type OtoOutput struct {
	ctx *oto.Context
}

// NewOtoOutput creates the process-wide Oto context at the canonical
// rate. It fails honestly when no audio device is available.
func NewOtoOutput() (*OtoOutput, error) {
	op := &oto.NewContextOptions{}
	op.SampleRate = SampleRate
	op.ChannelCount = Channels
	op.Format = oto.FormatFloat32LE
	ctx, ready, err := oto.NewContext(op)
	if err != nil {
		return nil, fmt.Errorf("playback: oto context: %w", err)
	}
	<-ready
	// oto v3 reports asynchronous device setup failures through
	// ctx.Err() after the ready channel closes (e.g. both PulseAudio
	// and ALSA unavailable). Without this check a device-less
	// machine got a "real" output that silently played nothing while
	// the UI reported DeviceReal=true — the documented silent
	// fallback was defeated (run 43541087097 repair).
	if aerr := ctx.Err(); aerr != nil {
		return nil, fmt.Errorf("playback: no audio device: %w", aerr)
	}
	return &OtoOutput{ctx: ctx}, nil
}

func (o *OtoOutput) NewPlayer(r io.Reader) Player { return o.ctx.NewPlayer(r) }
func (o *OtoOutput) Close() error                 { return o.ctx.Suspend() }

// SilentOutput is a deviceless output used when no audio device is
// available: the transport still runs (for visualizer preview timing
// in silent mode) but the UI must show the "no audio device" state.
// It consumes bytes in real time at the canonical rate so preview
// pacing matches audio pacing without a device.
type SilentOutput struct {
	cancel chan struct{}
	mu     sync.Mutex
	closed bool
}

// NewSilentOutput creates a silent output.
func NewSilentOutput() *SilentOutput {
	return &SilentOutput{cancel: make(chan struct{})}
}

// silentPlayer paces reads at the canonical byte rate. It runs at most
// one pacer goroutine per play session; Pause ends the session and the
// goroutine exits promptly, so repeated play/pause cycles never
// accumulate idle tickers.
type silentPlayer struct {
	r       io.Reader
	out     *SilentOutput
	playing bool
	volume  float64
	offset  int64
	session bool // a pacer session is live (its stop channel is open)
	mu      sync.Mutex
	stop    chan struct{} // owned by the current session
	done    chan struct{} // closed when the current pacer exits
}

func (o *SilentOutput) NewPlayer(r io.Reader) Player {
	return &silentPlayer{r: r, out: o, volume: 1}
}

func (o *SilentOutput) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.closed {
		o.closed = true
		close(o.cancel)
	}
	return nil
}

func (p *silentPlayer) Play() {
	p.mu.Lock()
	if p.playing || p.session {
		p.mu.Unlock()
		return
	}
	prev := p.done // previous session's done channel (closed when its pacer exited)
	p.playing = true
	p.session = true
	stop := make(chan struct{})
	done := make(chan struct{})
	p.stop = stop
	p.done = done
	p.mu.Unlock()
	if prev != nil {
		// Join the previous pacer before starting the new one: Pause
		// closes stop, but the old goroutine may still be inside
		// p.r.Read — two pacers on the same SectionReader would race
		// and miscount the offset (run 43541087097 repair). The join is
		// bounded: a SectionReader file read returns promptly.
		select {
		case <-prev:
		case <-time.After(50 * time.Millisecond):
		}
	}
	go p.pace(stop, done)
}

// pace is the one live pacer goroutine of a play session. It exits on
// session stop, output cancel, or EOF, exactly once per session.
func (p *silentPlayer) pace(stop, done chan struct{}) {
	defer close(done)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	// canonical bytes per 10 ms
	chunk := SampleRate / 100 * BytesPerFrame
	sink := make([]byte, chunk)
	for {
		select {
		case <-p.out.cancel:
			return
		case <-stop:
			return
		case <-tick.C:
		}
		p.mu.Lock()
		playing := p.playing
		p.mu.Unlock()
		if !playing {
			continue
		}
		n, err := p.r.Read(sink)
		p.mu.Lock()
		p.offset += int64(n)
		p.mu.Unlock()
		if err != nil {
			p.mu.Lock()
			p.playing = false
			p.session = false
			p.mu.Unlock()
			return
		}
	}
}

func (p *silentPlayer) Pause() {
	p.mu.Lock()
	p.playing = false
	live := p.session
	p.session = false
	stop := p.stop
	p.mu.Unlock()
	if live && stop != nil {
		close(stop)
	}
}

func (p *silentPlayer) Reset() { p.Seek(0, io.SeekStart) }

func (p *silentPlayer) Seek(offset int64, whence int) (int64, error) {
	if s, ok := p.r.(io.Seeker); ok {
		n, err := s.Seek(offset, whence)
		p.mu.Lock()
		p.offset = n
		p.mu.Unlock()
		return n, err
	}
	return 0, fmt.Errorf("playback: silent player reader is not seekable")
}

func (p *silentPlayer) IsPlaying() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.playing
}
func (p *silentPlayer) BufferedSize() int   { return 0 }
func (p *silentPlayer) SetVolume(v float64) { p.volume = v }
func (p *silentPlayer) Volume() float64     { return p.volume }
func (p *silentPlayer) Err() error          { return nil }
func (p *silentPlayer) Close() error        { p.Pause(); return nil }
