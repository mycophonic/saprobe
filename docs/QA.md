# Testing

Codec-specific conformance tests and benchmarks have moved to their individual repositories:

- **ALAC:** [saprobe-alac/docs/QA.md](https://github.com/mycophonic/saprobe-alac/blob/main/docs/QA.md)
- **DSD:** [saprobe-dsd/docs/QA.md](https://github.com/mycophonic/saprobe-dsd/blob/main/docs/QA.md)
- **FLAC:** [saprobe-flac/docs/QA.md](https://github.com/mycophonic/saprobe-flac/blob/main/docs/QA.md)
- **MP3:** [saprobe-mp3/docs/QA.md](https://github.com/mycophonic/saprobe-mp3/blob/main/docs/QA.md)

The tests below are cross-codec or format-level tests that remain in the main saprobe repo.

## Single file decode (`TestSingleDecode`)

Detailed comparison of a single file between saprobe and ffmpeg. Supports all codecs (ALAC, FLAC, MP3, OggVorbis, AAC).

```bash
go test ./tests/ -run TestSingleDecode -single-file /path/to/file.m4a -count=1 -v
```

## Mass decode (`TestMassDecode`)

Comparative decoding across a large file collection. Walks a directory tree, decodes each file with both saprobe and ffmpeg, and compares output.

```bash
# All codecs
go test -v -timeout 24h ./tests/ -run TestMassDecode -audio-path /path/to/music

# Single codec
go test -v -timeout 24h ./tests/ -run TestMassDecode -only flac -audio-path /path/to/music
go test -v -timeout 24h ./tests/ -run TestMassDecode -only m4a -audio-path /path/to/music
```

### Results (Anisotope collection)

**FLAC** (467 files, 44.1kHz/16bit stereo):
```
PASS — 467/467
```

**ALAC/AAC** (7434 files):
```
by codec: aac=105, alac=7329
by sample rate: 44100=7399, 48000=21, 96000=14
by channels: 1=2, 2=7432
by bit depth / bit rate: 16=7307, 24=22, N/A=105

1 AAC failure: corrupt M4A container (Tzadik TZ 7205, timescale not set).
ffmpeg tolerates it, CoreAudio rejects it.
```

**OggVorbis** (14 files):
```
PASS — 14/14
```

## WAV round-trip (`TestWAVRoundTrip`)

Validates WAV encoding by round-tripping through ffmpeg: generate PCM, wav.Encode, ffmpeg decode, compare against original PCM. Tests simple WAVEFORMAT (mono/stereo 16-bit) and WAVEFORMATEXTENSIBLE (multichannel, 24/32-bit) headers.

```bash
go test ./tests/ -run TestWAVRoundTrip -count=1 -v
```
