# AAC Decoder - Implementation Plan

## Overview

CoreAudio-based AAC decoder via CGO. macOS-only, gated behind the `with_aac` build tag.
Linux and Windows remain pure Go. Using `with_aac` on those platforms is a compile error.

## Build Constraint Matrix

| Platform | `with_aac` set | Behavior |
|----------|----------------|----------|
| macOS    | no             | Pure Go. `aac.Decode` returns `ErrNotSupported`. |
| macOS    | yes            | CGO_ENABLED=1. CoreAudio AAC decoder compiles in. |
| Linux    | no             | Pure Go. `aac.Decode` returns `ErrNotSupported`. |
| Linux    | yes            | **Compile error.** |
| Windows  | no             | Pure Go. `aac.Decode` returns `ErrNotSupported`. |
| Windows  | yes            | **Compile error.** |

## Decoding Pipeline (CoreAudio path)

```
M4A/AAC file (io.ReadSeeker)
  → AudioFileOpenWithCallbacks (read/seek/size C callbacks → Go ReadSeeker)
  → ExtAudioFileWrapAudioFileID
  → Query kExtAudioFileProperty_FileDataFormat (sample rate, channels)
  → Set client format: LPCM, signed int, little-endian, 16-bit
  → ExtAudioFileRead (loop until EOF)
  → Interleaved little-endian signed 16-bit PCM bytes
```

## File Layout

| File                   | Build Constraint              | Purpose                                    |
|------------------------|-------------------------------|--------------------------------------------|
| doc.go                 | (none)                        | Package documentation                      |
| errors.go              | (none)                        | ErrNotSupported sentinel                   |
| decode_darwin_cgo.go   | `with_aac && darwin`          | Real CoreAudio CGO decoder                 |
| decode_stub.go         | `!with_aac`                   | Stub returning ErrNotSupported             |
| unsupported.go         | `with_aac && !darwin`         | Intentional compile error                  |

## Public API

```go
func Decode(io.ReadSeeker) ([]byte, saprobe.PCMFormat, error)
```

Same signature as all other saprobe decoders.

## Detection

Both AAC and ALAC use M4A/MP4 containers (`ftyp` at offset 4).
`detect.Identify` probes the stsd box FourCC to distinguish:
- `alac` → ALAC
- `mp4a` → AAC

## Key Decisions

1. **CGO callbacks**: Use `AudioFileOpenWithCallbacks` to bridge Go's `io.ReadSeeker` to CoreAudio
2. **Output format**: Always 16-bit signed LE (AAC is lossy, no native bit depth to preserve)
3. **Platform guard**: Undefined function reference in `unsupported.go` for clear compile error
4. **No fallback**: Stub returns error, not silence — caller knows AAC is not available

## Usage

```bash
# macOS with AAC:
GOFLAGS="-tags=netgo,osusergo,with_aac" CGO_ENABLED=1 make build

# Default (all platforms, pure Go, no AAC):
make build
```
