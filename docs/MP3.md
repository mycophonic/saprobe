# MP3 Support Matrix

## Take away

Decode support covers MPEG 1 and MPEG 2, Layer III, mono and stereo, CBR and VBR. Output is always stereo 16-bit.
No encoder is implemented.

The decoder is a complete rewrite of hajimehoshi/go-mp3 with critical fixes for MPEG2 frame sizing, gapless trimming, and seek safety.
Performance is compute-bound on in-memory decoding (subbandSynthesis at 39% cum, imdct.Win at 15%).
Disk-based reads use `filesystem.ReadSeeker` for buffered I/O. Frame reuse, lookup table
requantization, zero-allocation subband synthesis, and NEON/SSE SIMD for the polyphase matrix
multiply have brought the saprobe/ffmpeg ratio from 9.56x to 4.73x.

## MP3 Specification

MPEG Audio Layer III (ISO 11172-3 / ISO 13818-3). Key parameters:

| Parameter | MPEG 1 | MPEG 2 | MPEG 2.5 |
|-----------|--------|--------|----------|
| Sample rates | 32000, 44100, 48000 | 16000, 22050, 24000 | 8000, 11025, 12000 |
| Bit rates (Layer III) | 32-320 kbps | 8-160 kbps | 8-160 kbps |
| Granules per frame | 2 | 1 | 1 |
| Samples per frame | 1152 | 576 | 576 |
| Channel modes | Stereo, Joint Stereo, Dual Channel, Mono | Same | Same |
| Bit reservoir | Yes (mainDataBegin) | Yes | Yes |

## Encoders & Decoders

| Tool | Role | MPEG Versions | Bit Depth | Channels |
|------|------|--------------|-----------|----------|
| saprobe (go-mp3 fork) | Decode only | MPEG 1, MPEG 2 | 16-bit output | Always stereo (mono duplicated) |
| ffmpeg (libmp3lame) | Encode + Decode | MPEG 1, MPEG 2, MPEG 2.5 | 16-bit | Mono, Stereo |
| lame | Decode only | MPEG 1, MPEG 2, MPEG 2.5 | 16-bit | Mono, Stereo |

saprobe rejects MPEG 2.5, free bitrate (index 0), and non-Layer III frames at the sync scanner level.
These are skipped automatically during frame header scanning.

## MPEG Versions

| Version  | saprobe decode | ffmpeg encode | Sample Rates        | Tests   |
|----------|----------------|---------------|---------------------|---------|
| MPEG 1   | Yes            | Yes           | 32000, 44100, 48000 | 18 pass |
| MPEG 2   | Yes            | Yes           | 16000, 22050, 24000 | 18 pass |
| MPEG 2.5 | No (rejected)  | Yes           | 8000, 11025, 12000  | N/A     |

## Channel Modes

| Mode | saprobe decode | Notes |
|------|---------------|-------|
| Stereo | Yes | Direct L/R output |
| Joint Stereo (M/S) | Yes | Mid/Side decoded with 1/sqrt(2) normalization |
| Joint Stereo (Intensity) | Yes | Scale factor band intensity decode |
| Dual Channel | Yes | Independent L/R |
| Mono | Yes | Single channel duplicated to stereo output |

go-mp3 always outputs interleaved stereo 16-bit PCM. For mono sources, the single decoded channel is written to
both L and R in the synthesis filterbank. This is a direct duplication at full amplitude (no attenuation).

Note: ffmpeg's mono-to-stereo upmix (`-ac 2`) attenuates by 1/sqrt(2) for energy preservation. This is a different
convention. The test suite accounts for this by decoding ffmpeg to mono and manually duplicating.

## Bitrate Modes

| Mode | saprobe decode | Tests |
|------|---------------|-------|
| CBR (Constant Bit Rate) | Yes | MPEG1: 128k, 320k; MPEG2: 96k, 160k |
| VBR (Variable Bit Rate) | Yes | LAME quality 2 tested |
| ABR (Average Bit Rate) | Yes (same as VBR) | Not explicitly tested |
| Free bitrate (index 0) | No (rejected) | N/A |

## Gapless Playback

saprobe implements LAME-compatible gapless trimming:

1. **ID3v2 skip** -- Syncsafe size parsing, seeks past tag before frame scanning.
2. **XING/Info frame detection** -- Parsed from first valid frame after side info. Both "Xing" (VBR) and "Info" (CBR) tags recognized.
3. **LAME tag extraction** -- Encoder delay (12 bits) and padding (12 bits) from the LAME header at offset 21.
4. **Trimming calculation:**
   - Start trim = LAME delay + decoder delay (529) + samples-per-frame (if XING frame present)
   - End trim = max(LAME padding - decoder delay, 0)

The decoder delay of 529 samples is empirically measured for go-mp3's synthesis filterbank priming.
MPEG1 and MPEG2 use different samples-per-frame values (1152 vs 576) for the XING frame compensation.

## Test Structure

Tests are in `TestMP3Decode/{sampleRate}Hz_{channels}ch/{bitrateMode}` using synthetic white noise.
Three decoders are cross-compared: saprobe, ffmpeg, and lame.

For each test:
- Generate 1 second of white noise PCM (mono or stereo)
- Encode with ffmpeg (libmp3lame) at specified bitrate/quality
- Decode with all three decoders (saprobe, ffmpeg, lame)
- For mono sources: decode ffmpeg/lame to mono, then duplicate to stereo (avoids ffmpeg's 1/sqrt(2) attenuation)
- Cross-compare all decoder pairs: allow +/-2 per sample, up to 1% of samples with larger differences

### Test Matrix

| Dimension | Values |
|-----------|--------|
| Sample rates | 16000, 22050, 24000 (MPEG 2), 32000, 44100, 48000 (MPEG 1) |
| Channels | 1 (mono), 2 (stereo) |
| MPEG 1 bitrates | CBR 128k, CBR 320k, VBR q2 |
| MPEG 2 bitrates | CBR 96k, CBR 160k, VBR q2 |

Total: **36 sub-tests** (6 rates x 2 channels x 3 bitrates), all passing.

## Implementation Details

### Decoder Pipeline

```
MP3 file -> Skip ID3v2 tag (syncsafe size)
  -> Parse XING/Info frame for gapless metadata
  -> Seek to start
  -> go-mp3.NewDecoder (frame table scan)
  -> For each frame:
      -> Sync scanner (slide until valid frame header)
      -> Side info parse (9-32 bytes depending on version/channels)
      -> Main data read (bit reservoir: mainDataBegin bytes from prior frames)
      -> Per granule, per channel:
          -> Huffman decode (frequency lines)
          -> Requantize (inverse quantization with scale factors)
          -> Reorder (short blocks)
      -> Stereo processing (M/S and/or Intensity)
      -> Per granule, per channel:
          -> Antialias (butterfly computation)
          -> Hybrid synthesis (IMDCT + overlap-add)
          -> Frequency inversion
          -> Subband synthesis (polyphase filterbank, 32-sample output)
  -> Apply gapless trimming (start + end)
  -> Interleaved stereo 16-bit LE PCM
```

### Unsupported Features

| Feature | Status | Notes |
|---------|--------|-------|
| MPEG 2.5 | Rejected | Skipped at sync scanner (IsValid) |
| Layer I / Layer II | Rejected | Skipped at sync scanner |
| Free bitrate (index 0) | Rejected | Skipped at sync scanner |
| Encoding | Not implemented | Decode only |
| CRC verification | Parsed but ignored | CRC bytes read and discarded |

## Decode Performance

### Timing (synthetic benchmark)

Measured with `go test -run TestMP3BenchmarkDecode` (10 iterations, 10 seconds of 44.1kHz stereo audio).

| Format   | Tool    | Median  | Mean  | Min   | Max   |
|----------|---------|---------|-------|-------|-------|
| CBR 320k | saprobe | 143ms   | 143ms | 143ms | 144ms |
| CBR 320k | ffmpeg  | 44ms    | 44ms  | 43ms  | 46ms  |
| CBR 320k | lame    | 28ms    | 28ms  | 27ms  | 28ms  |
| CBR 128k | saprobe | 130ms   | 130ms | 129ms | 131ms |
| CBR 128k | ffmpeg  | 41ms    | 41ms  | 40ms  | 43ms  |
| CBR 128k | lame    | 21ms    | 21ms  | 20ms  | 22ms  |
| VBR q2   | saprobe | 135ms   | 135ms | 135ms | 136ms |
| VBR q2   | ffmpeg  | 43ms    | 42ms  | 41ms  | 44ms  |
| VBR q2   | lame    | 23ms    | 23ms  | 23ms  | 24ms  |

saprobe decode is ~3.3x slower than ffmpeg and ~6x slower than lame (pure Go vs C).

### Timing (real music)

Clifford Brown — "Caravan" (34.9 MB MP3, 44.1kHz stereo CBR 320k, ~15 min). 10 iterations.

Test file: `/Volumes/Anisotope/Music/Processor/Lossy/[LOSSY]/Clifford Brown - Collection Best/1954.Clifford Brown All Stars - Caravan [320]/01. Caravan.mp3`

| Tool    | Median  | Mean   | Min    | Max    |
|---------|---------|--------|--------|--------|
| saprobe | 5.402s  | 5.399s | 5.374s | 5.434s |
| ffmpeg  | 1.141s  | 1.155s | 1.130s | 1.277s |
| lame    | 1.641s  | 1.639s | 1.623s | 1.654s |

saprobe is ~4.7x slower than ffmpeg on real music. The gap widens versus synthetic
because real files are larger and the overhead accumulates.

For custom benchmarks, set `BENCH_MP3_FILE` and run `go test -run TestMP3BenchmarkDecodeFile`.

### CPU Profile

Profiled on the Caravan file above. Total samples: 53.5s across 10 saprobe decodes (plus ffmpeg/lame runs).

| Function                     | Flat    | Flat%  | Cum    | Cum%   |
|------------------------------|---------|--------|--------|--------|
| frame.subbandSynthesis       | 10.35s  | 19.35% | 21.08s | 39.41% |
| imdct.Win                    | 8.11s   | 15.16% | 8.12s  | 15.18% |
| simd.matVecProduct64x32      | 7.87s   | 14.71% | 7.87s  | 14.71% |
| runtime.memclrNoHeapPointers | 4.35s   | 8.13%  | 4.35s  | 8.13%  |
| runtime.memmove              | 4.27s   | 7.98%  | 4.27s  | 7.98%  |
| huffman.DecodePair           | 3.32s   | 6.21%  | 5.96s  | 11.14% |
| frame.requantSample          | 2.15s   | 4.02%  | 2.16s  | 4.04%  |
| frame.hybridSynthesis        | 0.64s   | 1.20%  | 8.76s  | 16.38% |
| frame.requantize             | 0.52s   | 0.97%  | 2.68s  | 5.01%  |

**The decoder is compute-bound on large in-memory reads.** When the MP3 is pre-loaded into a `bytes.Reader`
(as the benchmark does), syscall overhead is negligible. The dominant cost is `subbandSynthesis` at 39% cum,
of which 14.7% is the SIMD matrix multiply (`simd.matVecProduct64x32` via NEON on arm64) and the remaining
19.4% flat is the D-coefficient window multiply and 32-sample output sum.

**The polyphase 64x32 matrix multiply is SIMD-accelerated.** `primordium/simd.MatVecMul64x32` uses NEON
FMLA on arm64 and SSE MULPS on amd64. The 32-element vector is loaded into registers once and reused
across all 64 row dot products, eliminating per-row function call overhead. This replaced the scalar
double loop that was 43% flat in the previous profile.

**`math.Pow` and `math.IsInf` are gone from the profile.** Requantization now uses `powtab34[8207]` for
`|x|^(4/3)` and `pow2quarter[372]` for `2^(k/4)`, both computed at init. `requantSample` appears as an
inlined leaf at 4.0% flat (was `math.pow` at 4.70% + `requantizeProcessLong` at 4.63%).

**`runtime.memmove` at 8.0%.** The remaining memmove is internal buffer operations (copy of synthesis
results into the doubled vVec circular buffer, reservoir copying between frames).

**Neither sideinfo nor maindata parsing appear in the CPU profile.** Side info uses a stack-local
`[32]byte` buffer and `bitReader` struct. Main data uses a caller-provided `*MainData` (embedded as a
value field in Frame) and a persistent `Reservoir` with a fixed `[4096]byte` buffer, eliminating all
heap allocations from both.

By cumulative time, the main decode path is:

```
frame.Decode (62%)
  -> subbandSynthesis (39%) -- polyphase filterbank, SIMD matrix multiply, circular vVec
      -> simd.matVecProduct64x32 (15%) -- NEON FMLA / SSE MULPS
  -> hybridSynthesis (16%) -- IMDCT + overlap-add
      -> imdct.Win (15%) -- zero-allocation, transposed cosine tables
  -> huffman.DecodePair (11%) -- multi-level flat table lookup with BitCache
  -> requantize (5%) -- lookup table requantization
```

Note: when decoding from disk via `os.File` (not pre-loaded), syscall overhead dominates instead (~50% flat)
because the frame header sync scanner reads 1 byte at a time through the kernel. The benchmark pre-loads to
isolate decode performance from I/O.

### Memory Profile (alloc_space)

Total allocated: 13.88 GB across 10 decodes of the Caravan file (34.9 MB, ~15 min of audio).

| Function | Flat | Flat% | What |
|----------|------|-------|------|
| bytes.growSlice | 10.24 GB | 73.78% | Subprocess I/O (ffmpeg/lame output capture) |
| readFrame | 1.62 GB | 11.67% | Frame decode buffer (`decodeBuf` reallocation) |
| mp3.Decode | 1.54 GB | 11.08% | Output buffer assembly |
| ensureFrameStartsAndLength | 0.39 GB | 2.81% | Frame table scan at init |

**Only 4 allocation sites remain, and none are in the decode pipeline itself.** IMDCT, huffman,
sideinfo, and maindata are all allocation-free. The Frame struct is allocated once via `NewFrame()`
and reused across all frames — `frame.Read` no longer appears as an allocation site (was 10.93 GB).
`frame.Decode` no longer appears either — it writes into a caller-provided buffer.

Excluding subprocess I/O (`bytes.growSlice`), saprobe MP3 decode allocates ~3.55 GB for 10 decodes
of the 15-minute file (~0.35 GB per decode). This is down from ~16.2 GB before frame reuse (was
~1.62 GB per decode), a **78% reduction**.

### IMDCT Rewrite Impact

The `imdct` package was rewritten with three changes:

1. **Zero-allocation API** -- `Win(input *[18]float32, blockType int, out *[36]float32)` instead of
   `Win(input []float32, blockType int) []float32`. Both input and output are caller-provided stack arrays.
2. **Transposed cosine tables** -- `cosN36[sampleIdx][m]` instead of `cosN36[m][sampleIdx]`. The inner
   loop (over `m`) now accesses sequential memory instead of striding by 36 floats.
3. **Named block size constants** -- `longN`, `longHalfN`, `shortN`, `shortHalfN` replace bare integers.

Before/after on the Caravan file (10 iterations):

| Metric | Before | After | Delta |
|--------|--------|-------|-------|
| Median decode time | 14.713s | 13.943s | **-5.2%** |
| imdct.Win CPU flat | 28.35s (10.33%) | 23.98s (9.19%) | -15.4% |
| hybridSynthesis cum | 34.85s (12.70%) | 27.13s (10.39%) | -22.1% |
| runtime.memmove | 34.02s (12.40%) | 14.07s (5.39%) | -58.7% |
| runtime.madvise | 14.00s (5.10%) | 2.65s (1.02%) | -81.1% |
| imdct.Win alloc | 11.99 GB (19.72%) | 0 GB | -100% |
| Total alloc (excl. subprocess) | ~40.8 GB | ~28.8 GB | -29.4% |

### Huffman Rewrite Impact

The `huffman` package was replaced by `huffman2`, switching from a bit-at-a-time tree walker to
multi-level flat lookup tables built at init from the canonical ISO Huffman tree data.

1. **Flat table lookup** -- Most codewords resolve in a single `PeekBits(5)` + array index. Only
   codes longer than 5 bits require secondary lookups (rare by Huffman construction).
2. **BitCache** -- Inlined bit cache reduces per-symbol overhead vs the generic `bits.Reader`.
3. **Separate API** -- `DecodePair()` for tables 0-31 (big_values) and `DecodeQuad()` for tables
   32-33 (count1), each with correct return types. No error return (uses sticky bit reader error).
4. **Table sharing preserved** -- Tables 16-23 and 24-31 share the same flat entries (same tree,
   different linbits), matching the old package's slice aliasing.

Before/after on the Caravan file (10 iterations):

| Metric | Before (imdct only) | After (+ huffman2) | Delta |
|--------|---------------------|---------------------|-------|
| Median decode time | 13.943s | 12.897s | **-7.5%** |
| Huffman CPU flat% | 9.82% | 3.85% | **-60.8%** |
| Huffman CPU cum% | 12.40% | 6.45% | -48.0% |

Cumulative improvement from original baseline (before all rewrites):

| Metric | Original | Current | Delta |
|--------|----------|---------|-------|
| saprobe/ffmpeg ratio | 9.56x | 8.92x | **-6.7%** |

The absolute timing includes system-level variance between sessions. The ratio to ffmpeg is the
reliable metric for measuring decoder improvement, since ffmpeg runs the same workload on the
same system. See the Frame Rewrite Impact section for the full cumulative improvement (9.56x → 5.61x).

### SideInfo Rewrite Impact

The `sideinfo` package was replaced by `sideinfo2`, eliminating all per-frame heap allocations from
side info parsing.

1. **Zero-allocation Read API** -- `Read(source, header, dst *SideInfo)` instead of returning
   `*SideInfo`. The caller provides `dst` (embedded as a value field in Frame, not a pointer).
2. **Stack-local bitReader** -- A `[32]byte` buffer and `bitReader` struct replace `make([]byte)` +
   `bits.NewReader()`. Max side info size is 32 bytes (MPEG1 stereo), fits on stack.
3. **Count1 relocated** -- `Count1 [2][2]int` moved from `SideInfo` to `MainData` where it belongs
   (written by Huffman decoder, not parsed from bitstream).
4. **PrivateBits discarded** -- Read to advance bit position, value not stored (no consumer).
5. **FrameSize validation removed** -- Duplicated validation done elsewhere.

Before/after on the Caravan file (10 iterations):

| Metric | Before (+ huffman2) | After (+ sideinfo2) | Delta |
|--------|---------------------|----------------------|-------|
| Median decode time | 12.897s | 12.805s | **-0.7%** |
| sideinfo alloc | 0.23 GB (0.96%) | 0 GB | **-100%** |
| Total alloc (excl. subprocess) | ~14.4 GB | ~14.6 GB | ~flat |

The timing improvement is marginal because side info parsing was never a hot path. The win is in
allocation elimination: 3 heap allocations per frame (`&SideInfo{}`, `make([]byte, sideInfoSize)`,
`bits.NewReader(buf)`) reduced to zero.

### MainData Rewrite Impact

The `maindata` package was rewritten to eliminate all per-frame heap allocations from main data
parsing and scale factor extraction.

1. **Caller-provided MainData** -- `Read(source, header, sideInfo, dst *MainData, res *Reservoir)`
   instead of returning `*MainData`. The caller provides `dst` (embedded as a value field in Frame).
2. **Persistent Reservoir** -- A `Reservoir` struct with a fixed `[4096]byte` buffer replaces
   `make([]byte, size)` + `append(reservoir, currentData...)` + `bits.NewReader()`. The reservoir
   is copied forward between frames as a value field.
3. **Stack-local bits.Reader** -- `bits.Reader.Reset(data)` enables reuse of a stack-local reader
   instead of `bits.NewReader()` heap allocation per frame.
4. **MPEG2 stack array** -- `var sf [45]int` replaces `var scaleFactors []int` with dynamic append.
   Pre-computed total count determines long vs short block layout before parsing.

Before/after on the Caravan file (10 iterations, measured as ratio to ffmpeg):

| Metric | Before (+ sideinfo2) | After (+ maindata) | Delta |
|--------|----------------------|---------------------|-------|
| saprobe/ffmpeg ratio | 9.56x | 8.92x | **-6.7%** |
| maindata alloc | 4.83 GB (19.45%) | 0 GB | **-100%** |
| frame.Read alloc | 4.53 GB (18.26%) | 10.93 GB (41.28%) | +141% |
| runtime.memmove CPU | 8.50% | 19.39% | +128% |

The maindata allocations (scale factors + reservoir + bit reader) are fully eliminated. `frame.Read`
grows because the Frame struct now embeds `MainData` (~11KB) and `Reservoir` (~4KB) as values.
`runtime.memmove` increases due to copying the larger Frame struct between frames (store, vVec,
reservoir, mainData fields).

### Frame Rewrite Impact

The `frame` package was rewritten with six changes that eliminate per-frame allocation and replace
runtime math with precomputed lookup tables.

1. **Frame reuse** -- `NewFrame()` allocates once. `Read()` and `Decode()` are called repeatedly on
   the same Frame. Store, vVec, reservoir, and mainData fields persist across frames. Eliminates
   the 10.93 GB (41%) `frame.Read` allocation and the 19% `memmove` overhead from copying the
   ~27KB struct between frames.

2. **Lookup table requantization** -- `powtab34[8207]` replaces `math.Pow(|x|, 4.0/3.0)` for
   Huffman frequency line requantization. `pow2quarter[372]` replaces `math.Pow(2, k/4)` for
   requantization gain computation. Both tables computed at `init()`. Eliminates `math.pow` (4.70%)
   and `math.IsInf` (1.94%) from the CPU profile entirely.

3. **Zero-allocation subbandSynthesis** -- `var uVec [512]float32` and `var sVec [32]float32` on
   the stack replace `make([]float32, 512)` and `make([]float32, 32)` per call. 18 calls per
   subband per granule, so this eliminates thousands of heap allocations per frame.

4. **Doubled vVec circular buffer** -- `vVec [2][2048]float32` with a `vOff [2]int` circular index
   replaces copying 960 elements per subband synthesis call. The write position advances and wraps
   instead of shifting the entire array.

5. **Caller-provided decode buffer** -- `Decode(dst []byte)` writes into a caller-provided buffer
   instead of `make([]byte, BytesPerFrame())` per frame. Eliminates 1.65 GB (6.25%) of allocations.

6. **decodeBuf reuse** -- `decode.go` reuses `decodeBuf []byte`, only reallocating when capacity is
   insufficient. Combined with Frame reuse, this means steady-state decoding does zero allocations.

Before/after on the Caravan file (10 iterations, measured as ratio to ffmpeg):

| Metric | Before (+ maindata) | After (+ frame rewrite) | Delta |
|--------|---------------------|-------------------------|-------|
| saprobe/ffmpeg ratio | 8.92x | 5.61x | **-37.1%** |
| frame.Read alloc | 10.93 GB (41.28%) | 0 GB | **-100%** |
| frame.Decode alloc | 1.65 GB (6.25%) | 0 GB | **-100%** |
| runtime.memmove CPU | 19.39% | 4.97% | **-74.4%** |
| math.pow CPU | 4.70% | 0% | **-100%** |
| Total alloc (excl. subprocess) | ~16.2 GB | ~3.55 GB | **-78.1%** |

Cumulative improvement from original baseline (before all rewrites), excluding SIMD:

| Metric | Original | Current | Delta |
|--------|----------|---------|-------|
| saprobe/ffmpeg ratio | 9.56x | 5.61x | **-41.3%** |
| Total alloc (excl. subprocess) | ~40.8 GB | ~3.55 GB | **-91.3%** |

### SIMD MatVecMul64x32 Impact

The 64x32 polyphase synthesis matrix multiply -- the single hottest loop in the decoder at 43% flat --
was replaced by `primordium/simd.MatVecMul64x32`, a specialized NEON/SSE assembly function.

The key optimization is keeping the 32-element input vector in registers (V16-V23 on NEON, X8-X15 on SSE)
across all 64 row dot products. Each row uses 8 fused multiply-add instructions (FMLA on NEON) or
8 multiply-then-add pairs (MULPS/ADDPS on SSE) with 2 interleaved accumulators for instruction-level
parallelism, followed by horizontal reduction (FADDP / HADDPS).

One function call replaces 64 iterations of a 32-element inner loop, eliminating all per-row overhead.

Before/after on the Caravan file (10 iterations):

| Metric | Before (scalar) | After (SIMD) | Delta |
|--------|-----------------|--------------|-------|
| saprobe median | 6.419s | 5.402s | **-15.8%** |
| saprobe/ffmpeg ratio | 5.61x | 4.73x | **-15.7%** |
| subbandSynthesis flat | 26.26s (43.04%) | 10.35s (19.35%) | **-60.6%** |
| subbandSynthesis cum | 28.01s (45.91%) | 21.08s (39.41%) | -24.8% |
| matVecProduct64x32 (NEON) | — | 7.87s (14.71%) | new |

The NEON assembly runs the 64x32 multiply in ~541 ns per call (benchmarked on M1 Max). The scalar
Go version of the same operation was roughly 3.5x slower.

Cumulative improvement from original baseline (before all rewrites):

| Metric | Original | Current | Delta |
|--------|----------|---------|-------|
| saprobe/ffmpeg ratio | 9.56x | 4.73x | **-50.5%** |
| Total alloc (excl. subprocess) | ~40.8 GB | ~3.55 GB | **-91.3%** |

### Optimization Opportunities

1. ~~**Buffered I/O**~~ -- Done. The source is now wrapped in `filesystem.ReadSeeker` from `primordium`,
eliminating per-byte kernel syscalls when decoding from `os.File`.

2. ~~**Frame struct reuse**~~ -- Done. See Frame Rewrite Impact above.

3. ~~**subbandSynthesis allocations**~~ -- Done. See Frame Rewrite Impact above (stack arrays + circular vVec).

4. ~~**SIMD polyphase matrix multiply**~~ -- Done. See SIMD MatVecMul64x32 Impact above.

## Open Issues

### Decoder limitations

1. **MPEG 2.5 not supported.** Rejected at the sync scanner. Low priority -- MPEG 2.5 is rare in practice.

2. **Free bitrate not supported.** Bitrate index 0 ("free format") requires measuring frame boundaries
by finding the next sync word, rather than computing frame size from bitrate tables. Uncommon.

3. **VBR seek accuracy.** The `bytesPerFrame` field used for seeking is overwritten per-frame during decode,
leaving it set to the last frame's size. VBR files will have inaccurate seeks. Only affects the `Seek()` method,
not full-file decode.

4. **Bit reservoir underflow.** When `mainDataBegin` exceeds available reservoir data, the decoder silently
continues with incomplete data instead of reporting an error. May produce artifacts on corrupted files.

5. **Source ReadFull partial reads.** The `source.ReadFull` pushback path does not retry the underlying reader
when less than the requested amount is returned. Could truncate reads at file boundaries.

### Performance

6. ~~**Unbuffered I/O.**~~ Resolved. The source is now wrapped in `filesystem.ReadSeeker` from `primordium`,
buffering byte-at-a-time reads into 4KB chunks.

7. ~~**Per-frame allocations.**~~ Resolved. Frame is allocated once via `NewFrame()` and reused across all
frames. `frame.Read` allocations eliminated (was 10.93 GB), `memmove` dropped from 19% to 5%.

8. ~~**subbandSynthesis allocations.**~~ Resolved. Stack arrays and circular vVec buffer eliminate all
per-call heap allocations.

9. ~~**Scalar polyphase matrix multiply.**~~ Resolved. Replaced by NEON/SSE SIMD assembly via
`primordium/simd.MatVecMul64x32`. Wall-clock decode time reduced by 15.8%, ratio improved from 5.61x
to 4.73x.
