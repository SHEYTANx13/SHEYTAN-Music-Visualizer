# GPU rendering — palette flow, uniform contract and honest adapter states

Status: current as of run 42939743766 repair (2026-11-11). This document is
the canonical description of the GPU render path, the Go↔WGSL uniform
layout contract and what the different hardware states actually prove.

## Palette data path (end to end)

```
user palette selection (Config.Palette, 0..3)
  → Config.Clamp()                    internal/visualizer/config.go
  → paletteFor(cfg.Palette)           internal/render/cpu.go
  → packUniforms(dst, in, cfg, pal)   internal/render/gpu.go  (SINGLE writer)
  → queue.WriteBuffer(uniform, 0, staging)   gpu.go renderGPU
  → bind group 0 (binding 0, uniform buffer)
  → WGSL Params struct (wgslShader)   internal/render/gpu.go
  → fragment shader fs() reads P.colLow..P.colBack
  → render pass → readback → GPURenderer.lastImage
```

## Go↔WGSL uniform layout contract (authoritative)

The WGSL `Params` struct is laid out under the WGSL uniform address-space
constraints (W3C WGSL §14.4.5,
https://www.w3.org/TR/2026/CRD-WGSL-20261021/#address-space-layout-constraints).
The Go packer writes exactly this byte sequence; the constants live once in
`internal/render/gpu.go` (`uniform*At`, `colorLaneAt`) and are asserted by
tests.

| Offset | Size | Member   | WGSL type          | Alignment | Go writer |
|-------:|-----:|----------|--------------------|-----------|-----------|
|      0 |    8 | res      | vec2f              | 8         | width, height |
|      8 |    4 | time     | f32                | 4         | media time |
|     12 |    4 | bass     | f32                | 4         | bass × sensitivity |
|     16 |    4 | beat     | f32                | 4         | bass × pulse response × 0.5 |
|     20 |    4 | glow     | f32                | 4         | 1.0 + glow × 0.2 |
|     24 |    4 | rotSpeed | f32                | 4         | motion-style derived |
|     32 |    8 | (pad)    | vec2f (unused)     | 8         | zeros (28 rounds up to 32) |
|     48 |  256 | bins     | array<vec4f, 16>   | 16        | 64 interleaved f32s |
|    304 |   16 | colLow   | vec4f              | 16        | palette Low, R,G,B,A |
|    320 |   16 | colMid   | vec4f              | 16        | palette Mid, R,G,B,A |
|    336 |   16 | colHigh  | vec4f              | 16        | palette High, R,G,B,A |
|    352 |   16 | colPeak  | vec4f              | 16        | palette Peak, R,G,B,A |
|    368 |   16 | colBack  | vec4f              | 16        | palette Back, R,G,B,A |
|    384 |      | (end)    | struct size = 384  | 16        | |

Every color is one 16-byte `vec4f` lane in R,G,B,A order with alpha 1.0.
A `vec3`-packed 12-byte color was never used and must not be introduced:
`vec3<f32>` has 16-byte alignment with a 12-byte payload, which is exactly
the class of silent misalignment this layout exists to prevent.

### Contract tests (all must pass; any change to the table must update them)

- `TestUniformLayoutMatchesWGSL` — the executable layout table (offsets,
  16-byte lane alignment, struct size, bind-group MinBindingSize).
- `TestColorLaneOrderMatchesPaletteFields` — lane order is exactly
  Low, Mid, High, Peak, Back.
- `TestPackUniformsContract` — packed byte values at documented offsets,
  zero pad region, alpha = 1.0, no stale bytes when the staging buffer is
  reused across palettes.
- `TestWGSLParamsContractMatchesGoLayout` — static check that the shipped
  WGSL declares the members in this order and the shader reads every
  packed lane.
- `TestPaletteReachesGPUShader` — end-to-end: palettes 0 and 1 must
  produce different GPU frames through the real pipeline.

## Run 42939743766 root cause (recorded so it stays fixed)

`TestPaletteReachesGPUShader` failed identically on hosted Windows and
Linux: palettes 0 and 1 produced byte-identical frames. Root causes:

1. **Fragment write mask.** The color target was declared
   `gputypes.ColorTargetState{Format: ...}` without `WriteMask`. In the
   pinned `gputypes`, the zero value of `ColorWriteMask` is
   `ColorWriteMaskNone` (0), and the project's supported software adapter
   applies the mask verbatim. Every fragment write was masked out, so the
   render target kept the load-op clear color for the whole frame. The fix
   states `WriteMask: gputypes.ColorWriteMaskAll` explicitly
   (`internal/render/gpu.go`, `buildPipeline`).
2. **Vacuous readback check.** `imageUniform` compared BYTES, so a uniform
   clear color frame `(8,8,8,255)` counted as "non-uniform" content and
   the general pipeline readback test passed without proving rasterization.
   The check now compares whole RGBA pixels (`gpu_noop_test.go`).

The uniform layout itself (bins@48, colors@304, size 384) was already
correct and is unchanged; the repair formalized it into the single
`packUniforms` packer with the table above.

## Adapter classification and honest states

`BackendInfo.DeviceClass` is derived from adapter PROPERTIES (PCI vendor /
device identity, device type, backend), never from the display name alone:

- `hardware` — proven physical adapter on a real driver backend
  (Vulkan / DX12 / Metal / GL).
- `software` — CPU/WARP/llvmpipe-class renderer, including the project's
  supported software test adapter and the wgpu noop backend.
- `virtual` — virtual GPU (VM/remote); never reported as physical.
- `unknown` — properties do not prove hardware; never labeled hardware.

The renderer state machine reports DETECTED → AVAILABLE → SELECTED →
EXECUTED → VERIFIED, and any failure downgrades to the CPU renderer with
the exact reason recorded. Device loss is named explicitly
(`wgpu.ErrDeviceLost`) and drilled by `internal/render/gpu_recovery_test.go`.

## What proves actual execution — and what CI cannot prove

- The dedicated `gpu-validation` gate (once per platform, per §2.4 of the
  run-42939743766 contract) runs `TestPaletteReachesGPUShader` through the
  real pipeline on the supported software adapter and requires the two
  palettes to differ in the palette-carrying channels after GPU queue
  completion and readback.
- Hosted CI runs are software-adapter evidence only. They NEVER prove the
  user's physical GPU, NPU, display driver or audio device. Hardware
  validation is a separate activity on real machines and is recorded
  separately when it happens.
