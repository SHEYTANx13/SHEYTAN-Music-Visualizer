// sysint.go — the System Integration surface INSIDE the single main
// window (run 42598929646 contract: no second top-level window, no
// diagnostic terminal, no floating hardware window — this is a dialog
// attached to the main window, like Help/Diagnostics).
//
// It reports the honest machine profile (OS, CPU, effective
// parallelism, RAM, GPU/render path, audio, power) with explicit
// provenance states, the auto-tuning policy decisions, and the
// persisted renderer preference (Auto / CPU / Vulkan / DX12 / Software)
// with revalidation on launch. Scanning runs on a goroutine and every
// widget mutation is marshaled back through fyne.Do; the UI and the
// audio/render paths are never blocked by a scan.
package fyneapp

import (
	"fmt"
	"path/filepath"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/apppaths"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/brand"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/render"
	"github.com/SHEYTANx13/SHEYTAN-Music-Visualizer/internal/sysprofile"
)

// prefStorePath is the persisted renderer preference document inside
// the resolved config directory (same pattern as window-state.json).
func prefStorePath(lay apppaths.Layout) string {
	return filepath.Join(lay.Config, "renderer-preference.json")
}

// rendererPrefChoices are the UI-facing preference labels in display
// order (auto first — the honest default).
var rendererPrefChoices = []string{"auto", "cpu", "vulkan", "dx12", "software"}

// onSystemIntegration opens the System Integration dialog attached to
// the main window and starts the first scan.
func (u *studioUI) onSystemIntegration() {
	u.logInfo("system-integration opened")
	status := widget.NewLabel("Scanning this machine…")
	status.Wrapping = fyne.TextWrapWord

	profileArea := container.NewVBox(status)

	scanBtn := widget.NewButton("Refresh / Rescan", func() {
		u.logInfo("system-integration rescan requested")
		status.SetText("Scanning this machine…")
		u.scanSystemProfile(profileArea, status)
	})

	prefSelect := widget.NewSelect(rendererPrefChoices, nil)
	if u.prefStore != nil {
		if stored, found, err := u.prefStore.Load(); err != nil {
			// A corrupt document is surfaced, never silently reset.
			u.logErr("renderer preference unreadable", "error", err.Error())
			prefSelect.PlaceHolder = "current: auto (stored preference unreadable)"
		} else if found {
			prefSelect.SetSelected(string(stored))
		} else {
			prefSelect.SetSelected(string(render.PrefAuto))
		}
	}
	prefSelect.OnChanged = func(choice string) {
		u.applyRendererPreferenceFromUI(choice)
	}

	prefNote := widget.NewLabel("Renderer preference is persisted and revalidated on every launch; " +
		"a manual GPU choice that cannot resolve falls back to the CPU renderer with the exact reason.")
	prefNote.Wrapping = fyne.TextWrapWord

	content := container.NewVBox(
		widget.NewLabelWithStyle("Machine profile — honest provenance states only", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		profileArea,
		widget.NewSeparator(),
		widget.NewLabelWithStyle("Render path", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		prefSelect,
		prefNote,
		scanBtn,
	)
	d := dialog.NewCustom("System Integration — "+brand.ProductName, "Close", content, u.win)
	d.Resize(fyne.NewSize(760, 560))
	d.SetOnClosed(func() { u.reconcileFullscreen("system-integration-closed") })
	u.sysDialog = d
	d.Show()

	u.scanSystemProfile(profileArea, status)
}

// scanSystemProfile collects the profile OFF the UI thread and marshals
// the widget update back through fyne.Do. Scans never poll: they run on
// open and on the explicit Refresh action only.
func (u *studioUI) scanSystemProfile(area *fyne.Container, status *widget.Label) {
	active := u.currentRendererInfo()
	go func() {
		start := time.Now()
		p := sysprofile.Collect(active)
		// Optional verification probe with the REAL render path (same
		// shader workload the app renders). On explicit rescan only —
		// never at startup, never polled.
		probeInfo, probeVerified := sysprofile.ProbeRenderer(render.PrefAuto)
		text := formatProfile(p, probeInfo, probeVerified, time.Since(start))
		pol := sysprofile.WorkerPolicyFor()
		budget := sysprofile.MemoryBudgetFor(p)
		text += "\nWorker policy: " + fmt.Sprintf("%d of %d effective — %s", pol.Workers, pol.Effective, pol.Source)
		text += "\nMemory budget: default export height " + fmt.Sprintf("%d p — %s", budget.RecommendedExportHeight, budget.Reason)
		fyne.Do(func() {
			status.SetText(fmt.Sprintf("Scan complete in %s (collector: %s)",
				time.Since(start).Round(time.Millisecond), p.Collector))
			area.Objects = []fyne.CanvasObject{widget.NewLabel(text)}
			area.Refresh()
			u.logInfo("system-integration scan complete",
				"collector", p.Collector,
				"render_decision", p.Render.Decision)
		})
	}()
}

// currentRendererInfo snapshots the ACTIVE renderer evidence under the
// render mutex (safe from any goroutine).
func (u *studioUI) currentRendererInfo() render.BackendInfo {
	u.renderMu.Lock()
	defer u.renderMu.Unlock()
	return u.rendererInfo
}

// formatProfile renders the profile with explicit provenance states.
// Unknown facts always print their reason — never a blank, never a
// guess. GPU utilization is deliberately absent: the pinned backend
// exposes no trustworthy telemetry, so measurable evidence (backend,
// adapter, verified state) is reported instead of a made-up load %.
func formatProfile(p sysprofile.Profile, probe render.BackendInfo, probeVerified bool, scan time.Duration) string {
	gi := func(q sysprofile.Quantity) string {
		switch q.State {
		case sysprofile.StateDetected:
			return fmt.Sprintf("%s (source: %s)", humanBytes(q.Value), q.Source)
		default:
			return "Unknown — " + q.Reason
		}
	}
	gf := func(f sysprofile.Fact) string {
		switch f.State {
		case sysprofile.StateDetected, sysprofile.StateSupported:
			return f.Value + " (source: " + f.Source + ")"
		case sysprofile.StateFailed:
			return "Failed — " + f.Reason
		default:
			return "Unknown — " + f.Reason
		}
	}
	s := "Operating system: " + gf(p.OS.Family) + "\n"
	s += "OS version: " + gf(p.OS.Version) + "\n"
	s += "Architecture: " + gf(p.OS.Arch) + " · process: " + gf(p.OS.ProcessBitness) + "\n"
	s += "App build: " + gf(p.OS.AppVersion) + " @ " + gf(p.OS.AppCommit) + "\n"
	s += "\nCPU: " + gf(p.CPU.Model) + "\n"
	s += "Logical CPUs available: " + fmt.Sprintf("%d (source: %s)", p.CPU.LogicalCPUs.Value, p.CPU.LogicalCPUs.Source) + "\n"
	s += "Effective parallelism: " + fmt.Sprintf("%d (source: %s)", p.CPU.EffectiveParallelism.Value, p.CPU.EffectiveParallelism.Source) + "\n"
	s += "Physical cores: " + gf(p.CPU.PhysicalCores) + "\n"
	s += "SIMD features: " + gf(p.CPU.Features) + "\n"
	s += "\nMemory total: " + gi(p.Memory.TotalBytes) + "\n"
	s += "Memory available: " + gi(p.Memory.AvailableBytes) + "\n"
	s += "\nPower: " + gf(p.Power.State) + "\n"
	s += "Audio output: " + gf(p.Audio.DefaultOutput) + "\n"
	s += "Audio engine: " + gf(p.Audio.Backend) + "\n"
	s += "\nRender path (ACTIVE): " + rendererSummary(p.Render.Active) + "\n"
	s += "Decision: " + p.Render.Decision + "\n"
	s += fmt.Sprintf("Verified: %v (executed + validated; never inferred from a name)\n", p.Render.Verified)
	if probe.Adapter != "" || probe.Detail != "" {
		if probeVerified {
			s += "Verification probe: PASSED — " + rendererSummary(probe) + "\n"
		} else {
			s += "Verification probe: not verified — " + rendererSummary(probe) + "\n"
		}
	}
	s += "\nDisplay facts: read from the running window (Fyne) — no display hardware inventory is collected.\n"
	s += "All inspection is local-only: no telemetry, no upload, no serial numbers or user identifiers."
	return s
}

// humanBytes formats a byte quantity for humans.
func humanBytes(v int64) string {
	const unit = 1024
	if v < unit {
		return fmt.Sprintf("%d B", v)
	}
	div, exp := int64(unit), 0
	for n := v / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(v)/float64(div), "KMGTPE"[exp])
}

// applyRendererPreferenceFromUI persists the chosen preference and asks
// the render loop to perform the actual renderer transition (the loop
// owns renderer lifecycle, so the swap can never race a mid-flight
// frame). The UI thread only persists and reports.
func (u *studioUI) applyRendererPreferenceFromUI(choice string) {
	pref, err := render.ParsePreference(choice)
	if err != nil {
		u.logErr("renderer preference invalid", "choice", choice, "error", err.Error())
		u.dialogError("Renderer preference", err.Error())
		return
	}
	if u.prefStore != nil {
		if serr := u.prefStore.Save(pref); serr != nil {
			u.logErr("renderer preference save failed", "error", serr.Error())
			u.dialogError("Renderer preference", "could not persist: "+serr.Error())
			return
		}
	}
	u.logInfo("renderer preference applied", "preference", string(pref))
	u.requestRenderer(pref)
	u.status("Renderer preference: " + string(pref) + " (revalidated on every launch)")
}

// requestRenderer hands a preference change to the render loop.
func (u *studioUI) requestRenderer(pref render.Preference) {
	u.rendererReqMu.Lock()
	select {
	case u.rendererReq <- pref:
	default:
		// A request is already pending; replace it (the newest choice
		// supersedes) without blocking the UI thread.
		select {
		case <-u.rendererReq:
		default:
		}
		u.rendererReq <- pref
	}
	u.rendererReqMu.Unlock()
}
