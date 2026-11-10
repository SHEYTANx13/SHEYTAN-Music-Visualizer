// probe.go — the GPU verification probe. It exercises the SAME
// meaningful render path the application uses — adapter request, device
// creation, pipeline build, a real non-empty shader workload, command
// submission and a readback compare — through the existing
// internal/render renderer. A no-op allocation or an adapter NAME is
// never a proof; only a GpuVerified state (readback succeeded after the
// shader executed) sets verified=true.
package sysprofile

import (
	"math"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/render"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/visualizer"
)

// probeFrame builds the deterministic probe input: a full 64-bin
// non-empty spectrum on the radial mode (the GPU shader path; other
// modes deliberately execute on the CPU renderer).
func probeFrame() visualizer.FrameInput {
	bins := make([]float64, visualizer.NumVizBins)
	for i := range bins {
		bins[i] = 0.4 + 0.3*math.Sin(float64(i)/7)
	}
	return visualizer.FrameInput{
		FrameIndex: 1,
		Time:       1.0 / 30,
		Width:      64,
		Height:     64,
		Config: visualizer.Config{
			Mode:          visualizer.ModeRadial,
			Sensitivity:   1,
			PulseResponse: 1,
		},
		Audio: visualizer.AudioState{Bass: 0.5, Mid: 0.3, Treble: 0.2, Bins: bins},
	}
}

// ProbeRenderer verifies a render backend with the real workload.
//
// verified=true means: the backend executed the actual shader workload
// AND the pixel readback validated (render.GpuVerified). verified=false
// with an error/Detail explains exactly why (no adapter, software
// adapter refused as hardware, device loss…). The probe renderer is
// always closed; a device failure never crashes the caller — it is
// returned as evidence.
func ProbeRenderer(pref render.Preference) (info render.BackendInfo, verified bool) {
	g, info := render.NewGPURendererWithPreference(pref)
	if g == nil {
		// info carries the exact unavailability reason (no adapter,
		// instance failure, device creation failure…).
		return info, false
	}
	defer g.Close()

	img, err := g.RenderFrame(probeFrame())
	if err != nil || img == nil {
		return g.Info(), false
	}
	fi := g.Info()
	return fi, fi.GpuState == render.GpuVerified
}
