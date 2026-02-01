# ALAC Support Matrix

## Take away

Decode support is complete for all ALAC-defined bit depths (16, 20, 24, 32) and channel counts (1-8). No encoder is implemented.

Only 16-bit and 24-bit can be tested against ffmpeg since no available encoder produces 20-bit or 32-bit ALAC.
For mono and stereo, both ffmpeg and Apple's `alacconvert` are used as reference encoders/decoders.
Multichannel (3-8) tests verify successful decode and correct output length; byte-level comparison is skipped due to channel layout remapping.

## ALAC Specification

Apple released the ALAC codec as open source (Apache 2.0) in 2011. Key parameters from the reference implementation:

| Parameter | Spec Range | Notes |
|-----------|-----------|-------|
| Bit depths | 16, 20, 24, 32 | Stored in ALACSpecificConfig |
| Channels | 1-8 | Mono through 7.1 surround |
| Sample rates | 1-4,294,967,295 Hz | Any uint32; practically 8kHz-192kHz |
| Frame length | Any uint32 | Typically 4096 samples |
| Predictor orders | 0-31 | 5-bit field; 31 = delta mode |
| Element types | SCE, CPE, CCE, LFE, DSE, PCE, FIL, END | CCE and PCE rarely used |
| Shift modes | 0, 1, 2 | LSB stripping for 24/32-bit |
| Container | MP4/M4A, CAF | M4A is standard distribution format |

## Encoders & Decoders

| Tool | Role | Bit Depths | Channels | Container |
|------|------|-----------|----------|-----------|
| saprobe | Decode only | 16, 20, 24, 32 | 1-8 | M4A |
| ffmpeg | Encode + Decode | 16, 24 | 1-8 | M4A |
| alacconvert | Encode + Decode | 16, 24 | 1-2 | CAF |

ffmpeg's ALAC encoder only accepts `s16p` (16-bit) and `s32p` (24-bit) sample formats.
32-bit input is silently encoded as 24-bit. No available encoder produces 20-bit or 32-bit ALAC.

`alacconvert` is Apple's open-source reference implementation (C). It encodes WAV to CAF and decodes CAF to WAV.
It only supports mono and stereo WAV input.

## Bit Depth

| Bit Depth | ALAC Spec | saprobe decode | ffmpeg encode | alacconvert encode | Tests |
|-----------|-----------|----------------|---------------|-------------------|-------|
| 16 | Yes | Yes | Yes (s16p) | Yes (mono/stereo) | 110 pass |
| 20 | Yes | Yes | No | No | untestable |
| 24 | Yes | Yes | Yes (s32p) | Yes (mono/stereo) | 110 pass |
| 32 | Yes | Yes | No | No | untestable |

20-bit and 32-bit decode is implemented and matches the reference C source, but cannot be verified since no encoder produces these formats.

## Channels

saprobe decodes the ALAC bitstream faithfully for all channel counts. For multichannel (3+), the ALAC encoder applies
channel layout mapping (e.g., 3.0 = L/R/C), which reorders channels compared to raw PCM input. Both ffmpeg encode and
decode apply their own remapping, making byte-level comparison unreliable. Multichannel tests verify successful decode
and correct output size.

| Channels | Layout | saprobe | ffmpeg encode | alacconvert encode | Source comparison |
|----------|--------|---------|---------------|-------------------|-------------------|
| 1 | mono | Pass | Pass | Pass | Exact match |
| 2 | stereo | Pass | Pass | Pass | Exact match |
| 3 | 3.0 | Pass | Pass | N/A | Skipped (remapping) |
| 4 | 4.0 | Pass | Pass | N/A | Skipped (remapping) |
| 5 | 5.0 | Pass | Pass | N/A | Skipped (remapping) |
| 6 | 5.1 | Pass | Pass | N/A | Skipped (remapping) |
| 7 | 6.1(back) | Pass | Pass | N/A | Skipped (remapping) |
| 8 | 7.1(wide) | Pass | Pass | N/A | Skipped (remapping) |

## Sample Rates

All 11 tested sample rates pass for both 16-bit and 24-bit across all channel counts:

8000, 11025, 16000, 22050, 32000, 44100, 48000, 88200, 96000, 176400, 192000 Hz

## Test Structure

Tests are organized as `TestALACDecode/{bitDepth}/{encoder}/{sampleRate}_{channels}`.

For each encoder (ffmpeg → M4A, alacconvert → CAF where supported):

For mono and stereo, each test:
- Encodes white noise PCM with the selected encoder
- Decodes with all compatible decoders
- Compares each decoder output vs original source (bit-for-bit)
- Cross-compares all decoder outputs against each other

For multichannel (3-8ch, ffmpeg only):
- Encodes with ffmpeg using explicit channel layout
- Decodes with saprobe and ffmpeg
- Verifies format metadata and output length

Total: **220 sub-tests**, all passing.

## Unsupported Features

| Feature | Status | Notes |
|---------|--------|-------|
| CCE (Coupling Channel Element) | Unsupported | Returns error |
| PCE (Program Config Element) | Unsupported | Returns error |
| Encoding | Not implemented | Decode only |
| CAF container | Not implemented | MP4/M4A only |

## Implementation Details

### Decoder Pipeline

```
M4A file -> MP4 box traversal (moov/trak/mdia/minf/stbl)
  -> Extract ALACSpecificConfig from stsd box
  -> Build flat sample table from stco/stsc/stsz
  -> For each packet:
      -> BitBuffer (bit-level reader with 4-byte padding)
      -> Element dispatch (SCE/CPE/LFE/DSE/FIL/END)
      -> Per element:
          -> Frame header (partial flag, bytesShifted, escape flag)
          -> If compressed:
              -> Adaptive Golomb-Rice entropy decode
              -> Linear predictor (FIR filter, adaptive coefficients)
          -> If escape (uncompressed):
              -> Read raw PCM samples
          -> Stereo: matrix unmix (mid/side decorrelation)
          -> Mono: direct output
      -> Interleaved little-endian signed PCM bytes
```

### Predictor Optimization

Three predictor paths based on order (numActive):

| Order | Path | Notes |
|-------|------|-------|
| 0 | Copy | No prediction |
| 4 | `unpcBlock4` | Hand-unrolled 4-tap FIR |
| 8 | `unpcBlock8` | Hand-unrolled 8-tap FIR |
| 31 | Delta mode | First-order delta decode |
| Other | `unpcBlockGeneral` | General FIR with BCE sub-slicing |

### Output Format

| Bit Depth | Bytes/Sample | Format |
|-----------|-------------|--------|
| 16 | 2 | Signed LE |
| 20 | 3 | Left-aligned in 24-bit, signed LE |
| 24 | 3 | Signed LE, optional shift buffer |
| 32 | 4 | Signed LE, optional shift buffer |

## Decode Performance

Measured with `TestALACBenchmarkDecodeFile` using "Song for My Father" (Horace Silver, 7:06, 44.1kHz/16bit stereo,
40.6 MB ALAC). 20 iterations, no CPU profiling.

### Timing (median)

| Tool | Median | Mean | Min | Max |
|------|--------|------|-----|-----|
| saprobe | 1184ms | 1183ms | 1151ms | 1228ms |
| alacconvert | 846ms | 847ms | 834ms | 873ms |
| ffmpeg | 405ms | 406ms | 312ms | 600ms |

saprobe is ~1.4x slower than Apple's reference C implementation (`alacconvert`) and ~2.9x slower than ffmpeg.
The dominant cost is the linear predictor (`unpcBlockGeneral`), which is a faithful port of the reference C:
the gap is Go vs C per-instruction throughput (no SIMD, GC safepoint checks in hot loops).

Note: `alacconvert` decodes its own CAF-encoded file (Apple reference encoder), while saprobe and ffmpeg decode
ffmpeg's M4A-encoded file. The ALAC bitstreams differ slightly due to different encoder choices (prediction order, entropy parameters).

### CPU Profile (decode only, real music)

Total samples: 23.29s across 20 decode iterations.

| Function | Flat | Flat% | Cum | Cum% |
|----------|------|-------|-----|------|
| unpcBlockGeneral | 12.08s | 51.9% | 12.62s | 54.2% |
| writeStereo16 | 3.48s | 14.9% | 3.49s | 15.0% |
| dynGet32Bit | 2.35s | 10.1% | 2.72s | 11.7% |
| dynDecomp | 2.20s | 9.4% | 4.94s | 21.2% |
| read32bit | 0.29s | 1.2% | 0.36s | 1.5% |
| unpcBlock4 | 0.13s | 0.6% | 0.25s | 1.1% |

The linear predictor (`unpcBlockGeneral`) dominates at 51.9%, followed by output formatting (`writeStereo16` at 14.9%)
and entropy decoding (`dynDecomp` + `dynGet32Bit` at 19.5% combined). The BCE optimization in `writeStereo16` nearly
halved its share from the pre-optimization 24.2%. Real music exercises the compressed path heavily;
synthetic white noise is mostly incompressible (escape path).

### Optimizations Applied

- **bitBuffer reuse**: Pool and reset per-packet bit readers instead of allocating.
- **decodePacketInto**: Write decoded PCM directly into the output buffer, eliminating intermediate copies.
- **writeStereo16 BCE**: Sub-slice pattern (`dst := out[pos:pos+4:pos+4]`) with sequential `pos += stride` increment
or bounds check elimination.
- **read32bit intrinsic**: `binary.BigEndian.Uint32` compiles to a single load+bswap instruction, replacing 4
individual byte loads with 4 bounds checks.
- **Entropy decoder register optimization**: `dynGet32Bit`/`dynGet` return `(result, newBitPos)`
instead of taking `*uint32`, allowing the compiler to keep `bitPos` in a register across the `dynDecomp` main loop.
- **Specialized predictors**: Hand-unrolled `unpcBlock4` and `unpcBlock8` for the two most common prediction orders,
with coefficients in local variables instead of slice access.

### Memory Profile (alloc_space, decode only)

After optimization (bitBuffer reuse + direct-write output):

| Source | Before | After | Reduction |
|--------|--------|-------|-----------|
| Total alac allocs | 438 MB | 148 MB | 66% |
| DecodePacket | 150 MB | 0 MB | eliminated |
| newBitBuffer | 143 MB | 0 MB | eliminated |

## Summary

- **Test suite:** 220 sub-tests, all passing (0 failures)
- **Decode:** 16, 20, 24, 32-bit (20/32 untestable, no encoder)
- **Channels:** 1-8 (multichannel verified via decode + length check)
- **Sample rates:** 11 rates tested (8kHz-192kHz)
- **Encoders tested:** ffmpeg (M4A, 1-8ch), alacconvert (CAF, 1-2ch)
- **Performance:** ~1.4x slower than Apple reference C, ~2.9x slower than ffmpeg; predictor-dominated
- **Multichannel:** Channel reordering by ffmpeg encoder prevents byte-level comparison for 3-8ch.
saprobe decodes the bitstream faithfully; the difference is layout mapping.
