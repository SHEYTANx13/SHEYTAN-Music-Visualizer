package render

// scene.go — deterministic per-frame visual state: attack/release
// smoothers, peak holds, bass drive and a seeded particle system.
// All evolution is a pure function of the frame inputs' media time
// and audio state — no wall clock, no global randomness.

import (
	"math"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/visualizer"
)

// SceneState evolves across frames. One instance belongs to one
// output (preview or export); export starts from a fresh SceneState
// so the exported video always begins from the same state.
type SceneState struct {
	bins       [visualizer.NumVizBins]float64
	peaks      [visualizer.NumVizBins]float64
	peakVel    [visualizer.NumVizBins]float64
	bassDrive  float64
	beatPulse  float64
	lastTime   float64
	hasLast    bool
	onsetFlash float64

	// particles (bounded count, stable seed)
	particles    []particle
	pCount       int
	prng         uint64
	particleInit bool
	phasePrev    float64
}

// MaxParticles bounds the particle system.
const MaxParticles = 512

type particle struct {
	x, y, vx, vy float64
	life, size   float64
	heat         float64
	alive        bool
}

// Reset returns the scene to its initial deterministic state.
func (s *SceneState) Reset() {
	for i := range s.bins {
		s.bins[i] = 0
		s.peaks[i] = 0
		s.peakVel[i] = 0
	}
	s.bassDrive = 0
	s.beatPulse = 0
	s.hasLast = false
	s.onsetFlash = 0
	s.particles = s.particles[:0]
	s.pCount = 0
	s.particleInit = false
	s.phasePrev = 0 // stale phase after a resize must not fake a beat
}

// Update advances the scene to in.Time and returns derived values the
// mode renderers share (smoothed bins, peaks, drive levels).
func (s *SceneState) Update(in *visualizer.FrameInput) {
	cfg := in.Config
	// frame delta from media time (never wall clock)
	dt := 1.0 / 60.0
	if s.hasLast && in.Time > s.lastTime {
		dt = in.Time - s.lastTime
		if dt > 0.25 {
			dt = 0.25
		}
	}
	s.lastTime = in.Time
	s.hasLast = true

	// Perceptual tilt (run 43541087097 repair): the studio supplies
	// NumVizBins inputs (already resampled from the full spectrum), so
	// the old code only applied the tilt when the input was SHORTER —
	// the configured SpectrumTilt (presets, DNA defaults, projects)
	// silently never affected the render. The tilt ramp is the same
	// formula InterpolateBins applies, so preview and export stay
	// consistent and tilt=0 remains a no-op.
	srcBins := in.Audio.Bins
	if len(srcBins) < visualizer.NumVizBins {
		srcBins = visualizer.InterpolateBins(srcBins, cfg.SpectrumTilt)
	}
	tilt := cfg.SpectrumTilt
	sm := clamp01(cfg.Smoothing)
	attack := 1 - math.Pow(1-0.85, dt*30)         // fast attack
	release := 1 - math.Pow(1-sm*0.9+0.02, dt*30) // configured release
	if release < 0.02 {
		release = 0.02
	}
	sens := cfg.Sensitivity
	for i := 0; i < visualizer.NumVizBins; i++ {
		v := srcBins[i]
		if tilt != 0 {
			tnorm := float64(i) / (visualizer.NumVizBins - 1)
			v *= 1 + tilt*(tnorm*2-1)
		}
		target := clamp01(v * sens)
		var k float64
		if target > s.bins[i] {
			k = attack
		} else {
			k = release
		}
		s.bins[i] += (target - s.bins[i]) * k
		// peak hold with gravity
		if s.bins[i] >= s.peaks[i] {
			s.peaks[i] = s.bins[i]
			s.peakVel[i] = 0
		} else {
			s.peakVel[i] += 1.6 * dt
			s.peaks[i] -= s.peakVel[i] * dt
			if s.peaks[i] < s.bins[i] {
				s.peaks[i] = s.bins[i]
			}
		}
	}
	// bass drive
	bassT := clamp01(in.Audio.Bass * sens * cfg.PulseResponse)
	if bassT > s.bassDrive {
		s.bassDrive += (bassT - s.bassDrive) * attack
	} else {
		s.bassDrive += (bassT - s.bassDrive) * release * 0.6
	}
	// beat pulse from beat phase (saw reset each beat)
	phase := in.Audio.BeatPhase
	if phase < s.phasePrev && cfg.PulseResponse > 0 {
		s.beatPulse = 1
	}
	s.phasePrev = phase
	s.beatPulse *= math.Pow(0.02, dt) // exponential decay
	// onset flash
	if in.Audio.Onset {
		s.onsetFlash = 1
	}
	s.onsetFlash *= math.Pow(0.01, dt)
}

// Scene holds the shared derived values for one frame render.
func (s *SceneState) Bin(i int) float64   { return s.bins[i] }
func (s *SceneState) Peak(i int) float64  { return s.peaks[i] }
func (s *SceneState) BassDrive() float64  { return s.bassDrive }
func (s *SceneState) BeatPulse() float64  { return s.beatPulse }
func (s *SceneState) OnsetFlash() float64 { return s.onsetFlash }

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// Seed initializes the particle PRNG deterministically.
func (s *SceneState) Seed(seed uint64) {
	s.prng = seed | 1
}

func (s *SceneState) nextRand() float64 {
	// xorshift64*
	s.prng ^= s.prng >> 12
	s.prng ^= s.prng << 25
	s.prng ^= s.prng >> 27
	return float64(s.prng*0x2545F4914F6CDD1D>>11) / float64(1<<53)
}

// UpdateParticles advances the particle system by dt (media-time
// derived). bassDrivesPulse: bass energy drives upward turbulence;
// onsets spawn bursts. density 0..1 scales the active count.
func (s *SceneState) UpdateParticles(dt, bass, mid, flux float64, onset bool, density float64, w, h int) {
	if !s.particleInit {
		s.particleInit = true
		if s.prng == 0 {
			s.Seed(0x54414E45)
		}
	}
	target := int(clamp01(density) * MaxParticles)
	// spawn on onset bursts and continuous background rate
	spawnBudget := 0
	if onset {
		spawnBudget += 8 + int(bass*24)
	}
	spawnBudget += int(6*dt*60*clamp01(flux*40+0.05) + 0.5)
	for spawnBudget > 0 && s.pCount < target {
		s.spawnParticle(w, h, bass)
		spawnBudget--
	}
	// physics: rise + turbulence + decay
	up := 0.06 + 0.22*bass
	for i := 0; i < s.pCount; i++ {
		p := &s.particles[i]
		if !p.alive {
			continue
		}
		p.life -= dt / (1.2 + p.size)
		if p.life <= 0 {
			p.alive = false
			continue
		}
		// curl-ish turbulence from mid energy
		t := s.nextRand()*2 - 1
		p.vx += t * mid * dt * 0.6
		p.vx *= 0.985
		p.vy = -up * (0.6 + 0.8*p.heat)
		p.x += p.vx * dt
		p.y += p.vy * dt
		if p.y < -0.05 || p.x < -0.05 || p.x > 1.05 {
			p.alive = false
		}
	}
	// compact dead particles (order is deterministic; compaction keeps
	// the bounded array dense without allocation)
	n := 0
	for i := 0; i < s.pCount; i++ {
		if s.particles[i].alive {
			s.particles[n] = s.particles[i]
			n++
		}
	}
	s.pCount = n
}

func (s *SceneState) spawnParticle(w, h int, bass float64) {
	if cap(s.particles) == 0 {
		s.particles = make([]particle, 0, MaxParticles)
	}
	p := particle{
		x:    0.1 + 0.8*s.nextRand(),
		y:    0.75 + 0.25*s.nextRand(),
		vx:   (s.nextRand() - 0.5) * 0.05,
		vy:   0,
		life: 1,
		size: 0.2 + 0.8*s.nextRand(),
		heat: clamp01(bass + 0.2*s.nextRand()),
	}
	p.alive = true
	s.particles = append(s.particles, p)
	s.pCount++
}

// ParticleCount returns the live particle count.
func (s *SceneState) ParticleCount() int { return s.pCount }

// Particle returns particle i (normalized 0..1 coordinates).
func (s *SceneState) Particle(i int) (x, y, life, size, heat float64) {
	p := s.particles[i]
	return p.x, p.y, p.life, p.size, p.heat
}
