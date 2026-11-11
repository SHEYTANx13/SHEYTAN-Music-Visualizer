# Performance baseline — SHEYTAN Music Visualizer

Run 42837709122 (§5.4): reproducible measurements, exact commands,
host identity and caveats. Every number below is a HOST-SPECIFIC
sample; compare only same-host, same-commit, same-command runs. No
number here is a universal claim about other machines.

## Measurement host identity

| Property | Value |
| --- | --- |
| Host | Linux 5.10.134-013.15.kangaroo.al8.x86_64, x86_64 (agent sandbox) |
| CPU | Intel(R) Xeon(R) Processor, 2 logical CPUs |
| Memory | 4 GiB total (3 GiB available at measurement time) |
| GPU/display | none physically present; Xvfb virtual display (software rendering only) |
| Go | go1.27.3 (go.mod toolchain) |
| Commit | 1487628eebbfc3d6f8a173ebefd931c307230909 (plus the local run-42837709122 changes; see note.md) |

## Startup (cold / warm) — `first-window-usable`

Instrumentation: the application records its own startup stages from
the true OS process-start anchor (`/proc/self/stat` on Linux,
`GetProcessTimes` on Windows; the anchor provenance is logged in every
record). The stage `first-window-usable` is marked when the Fyne event
loop starts iterating — a real visible/usable window, never "process
exited" and never build time.

Command (requires the built executable and Xvfb):

    scripts/bench/startup-bench.sh 5

Results (5 samples each, milliseconds, median in bold):

| Sample | cold | warm |
| --- | --- | --- |
| 1 | 1132 | 1110 |
| 2 | 1109 | 1117 |
| 3 | 1115 | 1106 |
| 4 | 1105 | 1109 |
| 5 | 1115 | 1111 |
| **median** | **1115** | **1110** |

After the render-path optimization (same host, same command): cold
median **1109 ms**, warm median **1109 ms** — within run-to-run noise
(the optimization targets steady-state rendering, not startup; no
startup regression was introduced).

Stage breakdown from a representative log record (`startup stages
complete`): `runtime-entry` ≈ 874 ms of the total is Go runtime +
dynamic-library initialization before `main()` (libGL/libwayland/wgpu
loader work — outside application control); the application-side path
(runtime entry → first usable window) is ≈ 0.4–0.6 s. Optional work
(system profile scan, FFmpeg probing) is NOT on this path: it runs on
background goroutines after the window is usable, per the §5.2
contract.

## CPU renderer frame throughput (before / after optimization)

Profile evidence (`go test -cpuprofile`, 1080p bar frame):
`glowCircle` 68.7% cumulative (inside it `math.Sqrt` per box pixel and
the per-pixel `image.Bounds()` call from `addPixel`), `vbar`'s per-pixel
`math.Pow`, and the background row fill dominated.

Command (identical before/after, 3 s benchtime):

    go test -run XXX -bench BenchmarkCPURender -benchmem -benchtime 3s ./internal/render/

| Benchmark | before (ns/op) | after (ns/op) | Δ time | allocs after |
| --- | --- | --- | --- | --- |
| CPURender1080pBars | 52 211 397 | 31 324 344 | **−40.0 %** | 0 allocs/op |
| CPURender1080pRadial | 18 064 914 | 12 511 420 | **−30.7 %** | 0 allocs/op |
| CPURender4KWaveform | 151 310 278 | 112 934 596 | **−25.4 %** | 0 allocs/op |
| CPURender1080pBarsWithLayer | 83 880 581 | 57 252 057 | **−31.7 %** | 0 allocs/op |

Changes (all output-identical, verified by the deterministic frame
tests in `internal/render` and the full test suite):

1. `canvas` caches frame geometry once (`newCanvasWith`) — no
   `image.Bounds()` call per blended pixel.
2. `glowCircle` rejects out-of-disc pixels with the squared distance
   (no `math.Sqrt`); in-disc pixels keep the identical `d = sqrt(d2)`.
3. `vbar` computes the per-y falloff table once per call (reused
   scratch buffer on the renderer) instead of recomputing
   `math.Pow(t, 0.85)` for every (dx, y) pair.
4. `background` converts the constant-in-x row color once per row.

Race tests (`-race`) for render, studio and export pass unchanged; the
scratch buffer is owned by the render-loop goroutine that owns
`RenderFrame`.

## DSP / audio / export baselines

Command:

    go test -run XXX -bench . -benchmem ./internal/dsp/ ./internal/audio/

Samples on the same host (single runs; re-run the command for medians):

| Benchmark | ns/op | B/op | allocs/op |
| --- | --- | --- | --- |
| BenchmarkDSPFrameCost | 70 154 | 9 923 | 1 |
| BenchmarkAudioImportWav5s | 6 117 500 | 1 568 728 | 209 |
| BenchmarkResample1s48to44 | 39 671 284 | 192 512 | 0 |

(The resampler is allocation-free in steady state.)

Export throughput (1080p30 / 4K24) is measured by the ffprobe-validated
export integration tests; the run-42837709122 hosted-runner reference
(≈1.57 s for 90 frames 1080p30, ≈4.92 s for 72 frames 4K24) remains an
environment-specific sample. Re-run `go test ./internal/export/` on the
same host to compare.

## Run-42921967187 measurement pass (2026-11-11)

Method: `go test -tags goffi_static -bench <name> -benchmem -benchtime=5x
-count=3`, hot path first (`go test -cpuprofile`), same host for before
and after (see host identity above — a 2-vCPU shared sandbox, slower and
noisier than the packaged reference host; only the deltas measured on it
are meaningful). Every change below preserves bit-identical outputs and
is covered by the deterministic package tests.

### DSP analysis (one minute of canonical audio, streamed in 1 s chunks)

`BenchmarkDSPAnalyzeOneMinute` — before / after:

| metric | before | after | Δ |
| --- | --- | --- | --- |
| wall time | 399.1–405.0 ms/op | 401.6–407.9 ms/op | unchanged (within host noise) |
| memory | 55.71 MB/op | 49.32 MB/op | **−11.5 %** |
| allocations | 5 649 allocs/op | 115 allocs/op | **−98 %** |

Changes (output-identical):

1. `Analyzer.emitFrame` no longer allocates one 1025-float64 display
   spectrum per hop. Per-frame `Bins` are sliced from a chunked backing
   store (64 frames per allocation, `nextBins`), eliminating ≈5.6 k
   allocator operations and their GC bookkeeping per minute of audio.
2. Bin center frequencies come from a precomputed `binHzTable` (identical
   values to the previous per-bin recomputation; same float64 rounding).
   A per-bin energy scratch table was also tried and REJECTED: it traded
   a small wall-time regression for a negligible gain and was reverted.

The analyzer retains its other scratch buffers (`buf`, `coeff`, `spec`,
`prev`, `wrk`) from previous optimization passes; `Reset` releases the
chunked backing store together with the frame list.

### CPU renderer frame cost

`BenchmarkCPURender*` — before / after (10× × 2 counts):

| Benchmark | before (ns/op) | after (ns/op) | Δ time |
| --- | --- | --- | --- |
| CPURender1080pBars | 30.70–30.91 M | 24.98–25.02 M | **−18.7 %** |
| CPURender1080pRadial | 12.39–12.43 M | 12.386 M | unchanged (centered disc — clip is a no-op) |
| CPURender4KWaveform | 110.72–110.96 M | 103.23–104.47 M | **−6.0 %** |
| CPURender1080pBarsWithLayer | 56.34–56.63 M | 48.72–49.61 M | **−13.0 %** |

Change (output-identical, verified by the deterministic frame tests):
`glowCircle` clips its disc bounding box to the canvas once instead of
iterating off-screen rows/columns that `addPixel` rejected per pixel.

### What was measured but NOT changed

- The render profile after the clip shows `glowCircle`'s in-disc blend
  (`addPixel` per channel) as the irreducible remaining cost; further
  gains would require approximation changes that risk visual output
  identity — out of scope for a correctness-preserving pass.
- Startup stage timings (`startup stages complete`) require a display
  (Xvfb) and are measured by `scripts/bench/startup-bench.sh` on hosts
  that provide one; this sandbox had no Xvfb, so no startup numbers were
  produced here and none are claimed. The hosted Windows/Linux jobs
  remain the startup-evidence path.

## Runtime memory / GC evidence

One structured record (`runtime snapshot`) is logged once at startup
completion — `gomaxprocs`, `num_cpu`, `heap_alloc_bytes`, `gc_cycles` —
never polled, never per-frame. For deeper profiling use:

    go test -cpuprofile cpu.prof -memprofile mem.prof ./internal/render/
    go tool pprof -top cpu.prof

No pprof HTTP listener is exposed by the release application.

## Caveats

- Xvfb is a virtual display: these numbers exercise the CPU renderer
  path only. No physical GPU/audio hardware is involved, and no GPU
  performance claim is made or implied.
- The startup harness terminates each sample after the structured
  startup record; first-launch data creation is included in cold runs.
- Benchmarks on shared CI runners have scheduler noise; medians over ≥5
  samples are the comparison basis, single runs are not.
