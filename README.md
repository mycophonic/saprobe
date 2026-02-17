# Saprobe
> * a pure Go audio decoder supporting ALAC, FLAC, DSD (experimental), MP3, WAV, and OggVorbis.
> * [fungi involved in saprotrophic nutrition, a process of chemoheterotrophic extracellular digestion of organic matter](https://en.wikipedia.org/wiki/Saprobe)

![Saprobe](logo.jpg)

## Purpose

Saprobe provides an optimized, pure golang library and cli that supports decoding FLAC, ALAC, DSD, MP3, WAV, and OggVorbis.

Individual decoders live in their own repository (eg: saprobe-CODEC).

New implementations:
- ALAC is a from scratch port of the Apple library to Go (with some help from github.com/abema/go-mp4
for container parsing)
- MP3 (originally inspired by github.com/hajimehoshi/go-mp3 (Apache))
- DSD/DFF/DSF, experimental, from scratch

Provided by third-party libraries:
- FLAC (github.com/mewkiz/flac (Unlicense)), with additional optimizations (2x faster) and a couple of additional patches on our fork
- OggVorbis: github.com/jfreymuth/oggvorbis (MIT)

AAC is optionally supported on macOS through CoreAudio, but requires CGO if enabled, and just wraps macOS CoreAudio

Future roadmap includes Opus.

## Installation

On macOS:
```bash
brew install --head mycophonic/mycota/saprobe
```

Others need Go installed and configured, then:
```bash
go install github.com/mycophonic/saprobe/cmd/saprobe@latest
```

## Usage

```bash
# Just decode.
saprobe decode my_audio_file > decoded.wav
# Or
saprobe decode -o decoded.wav my_audio_file
# Just get the format info.
saprobe decode --info my_audio_file
# Default bit depth is to use the native from the source.
saprobe decode --bit-depth=[16|24|32] --info my_audio_file
```

For raw PCM:
```bash
saprobe decode --raw my_audio_file
```

## Decoding performance

Compared to the reference C implementations (xyph, Apple, lame), saprobe:
* FLAC is 2.0x slower
* ALAC is 1.4x slower
* MP3 is 6.8x slower

Not perf tested:
* AAC (if compiled in) is provided by macOS, no point in testing
* OGG (don't have enough of these)

## Features and support coverage

See [QA](docs/QA.md) for some data on testing scope.


### P0: FLAC

Full support, encoding and decoding, thanks again to the awesome github.com/mewkiz/flac

Our fork additionally provides:
- 32 bits support
- optimizations for the decoder (about 2x faster than upstream)

For performance measurements, detailed format support and comparison with other tools, see [FLAC](../saprobe-flac/docs/FLAC.md).

### P0: ALAC

Decoding only (full support).
Implementation is a straight port of Apple ALAC decoder.

### P1: MP3

Added gapless support on top of (former) upstream, and some cleanup and refactoring.

Shit show refresher on MPEG formats:
```
  ┌──────────────┬─────────────┬─────────────┬─────────────────────────────────┬────────────────┐
  │              │   Layer I   │  Layer II   │            Layer III            │   Supported    │
  ├──────────────┼─────────────┼─────────────┼─────────────────────────────────┼────────────────┤
  │ MPEG 1       │ ISO 11172-3 │ ISO 11172-3 │ ISO 11172-3 — "MP3"             │ Layer III only │
  ├──────────────┼─────────────┼─────────────┼─────────────────────────────────┼────────────────┤
  │ MPEG 2 (LSF) │ ISO 13818-3 │ ISO 13818-3 │ ISO 13818-3 — also called "MP3" │ Layer III only │
  ├──────────────┼─────────────┼─────────────┼─────────────────────────────────┼────────────────┤
  │ MPEG 2.5     │ unofficial  │ unofficial  │ unofficial — also called "MP3"  │ No             │
  └──────────────┴─────────────┴─────────────┴─────────────────────────────────┴────────────────┘

```

We support:
```
  - MPEG 1 and MPEG 2 Layer III (CBR and VBR)
  - All channel modes: stereo, joint stereo (MS + intensity), dual channel, mono
  - Gapless playback via LAME/XING headers
```

We do not support:
```
  BitrateIndex == 0 (arbitrary constant bitrate) from ISO 11172-3 / ISO 13818-3
  No CRC validation — the ProtectionBit is parsed from the header but the actual CRC bytes are never verified
  Tag/metadata handling gaps:
  - Only one ID3v2 tag at file start (no stacked ID3v2 tags)
  - No APE tag recognition
  - No LYRICS3 tag recognition
  - No ID3v2 footer handling
  - Mid-file tags cause sync scanner to waste cycles sliding through them byte-by-byte
```

### P2: OggVorbis

Implemented, but barely tested.
Unlikely to receive much love.

## Roadmap

P0:
* WAV support
* FLAC performance improvements
* DSD: TODO. Clusterfuck.
  * Presumably need DAC capabilities detection for the purists and stuff in DoP.
  * Decoding to 24-bit/352.8kHz PCM for hardware without DoP support. No Go implem. Must implement from scratch.

P1:
* Opus: TODO. No solution right now. Accept WASM as escape hatch? Implement from scratch?
* AAC: TODO. No solution right now. Likely need implement from scratch.

P3:
* MP3: Here because you can't avoid it, but unlikely to receive much love. It just works, and the format is dead anyhow, so...
* OggVorbis: Similar to MP3 situation (better format, but still a dead pony)
