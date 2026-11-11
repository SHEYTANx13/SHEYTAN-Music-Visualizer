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
| cold start → first usable window | ~1115 ms | Linux sandbox, Xvfb, 2 vCPU, go1.27.3 |
| warm start → first usable window | ~1110 ms | same host |
| 1080p30 export, 90 frames | ~1.6 s (local host) / ~1.70 s (hosted runner, run 42939743766) | ffprobe-validated H.264+AAC |
| 4K24 export, 72 frames | ~4.9 s (local host) / ~5.47 s (hosted runner) | ffprobe-validated |
| render allocations | bounded; reused uniform staging + frame buffers | `internal/render` |

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
