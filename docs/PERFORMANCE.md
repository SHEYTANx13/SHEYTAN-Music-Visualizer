# Performance — methodology, measured baselines and limits

Status: current as of run 42939743766 repair (2026-11-11). The canonical
measured baseline lives in [performance-baseline.md](performance-baseline.md);
this document defines the methodology, the current numbers and the honesty
rules.

## Methodology

- Every number is a HOST-SPECIFIC sample. Compare only same-host,
  same-commit, same-command runs. Hosted-runner timings (about 1.70 s for
  the 90-frame 1080p30 export case and about 5.47 s for the 72-frame 4K24
  case in run 42939743766) are runner-specific baselines, never universal
  guarantees.
- Startup (`first-window-usable`) is measured from the true OS process-start
  anchor (`/proc/self/stat` on Linux, `GetProcessTimes` on Windows) to the
  moment the Fyne event loop starts iterating — a real visible/usable
  window. Command: `scripts/bench/startup-bench.sh 5` (requires a display;
  Xvfb counts as virtual-display evidence).
- Render/DSP/export work is measured with `go test -bench` plus Go CPU /
  heap / block / mutex profiles where the code path merits it
  (`runtime/pprof` / `runtime/trace` in explicit diagnostic or test modes
  only). The normal desktop runtime never exposes a profiler HTTP server.
- Optimization is evidence-led: profile first, change the measured hotspot,
  rerun the SAME benchmarks plus the full correctness suite. Frame cadence,
  sample-rate, FFT/window parameters, export quality and color accuracy are
  never traded for speed.

## Current measured baselines (from performance-baseline.md, same host)

| Metric | Value (median unless noted) | Context |
|---|---|---|
| cold start → first usable window | ~1115 ms (run 42939743766) / ~1196 ms (run 43090342995 baseline) / ~1183 ms (after first-frame deferral) | Linux sandbox, Xvfb, 2 vCPU; medians of 5–7 samples, see performance-baseline.md |
| warm start → first usable window | ~1110 ms (run 42939743766) / ~1187 ms (run 43090342995 baseline) / ~1191 ms (after) | same host |
| 1080p30 export, 90 frames | ~1.6 s (local host) / ~1.70 s (hosted runner, run 42939743766) | ffprobe-validated H.264+AAC |
| 4K24 export, 72 frames | ~4.9 s (local host) / ~5.47 s (hosted runner) | ffprobe-validated |
| render allocations | bounded; reused uniform staging + frame buffers | `internal/render` |

## What run 43090342995 changed (first-frame + idle contract)

- **Two blocking waits were removed from the first-frame path** (both
  measured on the same host as performance-baseline.md):
  1. the wgpu renderer probe (`render.SelectRenderer`) moved from the
     pre-show path into the render-loop goroutine — the window shows the
     truthful "probing…" state until selection completes;
  2. the audio-device open (`oto` context) is deferred to the first
     playback attempt (`Studio.ensureOutput`), with the honest
     not-yet-initialized state shown in the transport.
- **Measured outcome (honest):** on the measurement host the addressable
  pre-show work is only ~140 ms of the ~1190 ms `first-window-usable`
  total — ~950 ms is Go runtime + dynamic-library load and ~90 ms is
  Fyne/GLFW window creation, none of which the application can avoid.
  Removing the two waits therefore leaves the headline median unchanged
  within run-to-run noise (cold 1196 ms → 1183 ms, warm 1187 ms → 1191 ms,
  7 samples each, same binary profile): no regression, and on hosts with
  real GPU drivers/audio stacks the removed probes are the slow paths.
- **Idle render throttling:** the preview loop repaints at ~6 fps while
  nothing plays (30 fps while playing or scrubbing) — an idle window no
  longer renders 30 CPU frames per second.
- No frame cadence, sample-rate, FFT/window or color-accuracy parameter
  was traded for speed; correctness suites were re-run after every change.

## What run 42939743766 changed

- **No speculative optimization was introduced.** The repair session's
  performance work is redundancy removal with measured effect:
  `TestPaletteReachesGPUShader` now runs EXACTLY ONCE per platform in the
  dedicated `gpu-validation` gate instead of three times (default suite +
  race suite + full render-suite gpu gate). The remaining hot paths
  (deterministic canvas caching, uniform staging reuse) were already
  profiled in run 42837709122 and are unchanged.
- The GPU palette repair adds no per-frame allocation: `packUniforms`
  writes into the reused staging buffer and the WGSL peak-highlight mix is
  branch-free ALU work inside the existing fragment loop.

## Known limits

- Startup stage timings require a display; headless runs record the stage
  breakdown only where a display exists.
- Hosted CI has no physical GPU/audio/display hardware; hardware frame-time
  distributions are out of scope for CI evidence.
- Windows hosted runners carry no FFmpeg, so Windows export throughput is
  not measured by CI.
