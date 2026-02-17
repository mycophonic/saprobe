// Package wav implements WAV (RIFF WAVE) encoding and decoding for PCM audio.
package wav

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/mycophonic/saprobe"
)

//nolint:gochecknoglobals
var wavBitDepths = []saprobe.BitDepth{
	saprobe.Depth16,
	saprobe.Depth24,
	saprobe.Depth32,
}

// WAV format constants.
const (
	wavFormatPCM        = 1
	wavFormatExtensible = 0xFFFE
)

// WAV chunk IDs and container markers.
const (
	chunkRIFF = "RIFF"
	chunkWAVE = "WAVE"
	chunkFmt  = "fmt "
	chunkData = "data"
)

// WAVEFORMATEXTENSIBLE layout constants.
const (
	fmtExtensibleSize = 40 // Total fmt chunk size for WAVEFORMATEXTENSIBLE.
	riffHeaderSize    = 12 // "RIFF" + fileSize + "WAVE".
	cbSizeExtensible  = 22 // Extra bytes after WAVEFORMATEX in extensible format.
)

// Speaker position channel masks (Microsoft WAVEFORMATEXTENSIBLE).
const (
	channelMaskMono   = 0x4   // FC
	channelMaskStereo = 0x3   // FL | FR
	channelMaskQuad   = 0x33  // FL | FR | BL | BR
	channelMask51     = 0x3F  // FL | FR | FC | LFE | BL | BR
	channelMask71     = 0x63F // FL | FR | FC | LFE | BL | BR | SL | SR
)

// GUID for PCM in WAVEFORMATEXTENSIBLE.
//
//nolint:gochecknoglobals
var wavGUIDPCM = [16]byte{
	0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x10, 0x00,
	0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71,
}

// ErrNotWAV indicates the input is not a valid WAV file.
var ErrNotWAV = errors.New("not a WAV file")

// ErrUnsupportedFmt indicates the WAV file uses an unsupported audio format.
var ErrUnsupportedFmt = errors.New("unsupported WAV format")

// ErrNoFmtChunk indicates the WAV file is missing the required fmt chunk.
var ErrNoFmtChunk = errors.New("missing fmt chunk")

// ErrNoDataChunk indicates the WAV file is missing the required data chunk.
var ErrNoDataChunk = errors.New("missing data chunk")

// ErrInvalidBitDepth indicates an unsupported bit depth for WAV encoding or decoding.
var ErrInvalidBitDepth = errors.New("invalid bit depth")

// Decode reads a WAV file and returns raw PCM samples.
func Decode(reader io.ReadSeeker) ([]byte, saprobe.PCMFormat, error) {
	var format saprobe.PCMFormat

	// Read RIFF header
	var riffHeader [12]byte
	if _, err := io.ReadFull(reader, riffHeader[:]); err != nil {
		return nil, format, fmt.Errorf("reading RIFF header: %w", err)
	}

	if string(riffHeader[0:4]) != chunkRIFF || string(riffHeader[8:12]) != chunkWAVE {
		return nil, format, ErrNotWAV
	}

	// Parse chunks
	var pcmData []byte

	fmtFound := false

	for {
		var chunkHeader [8]byte
		if _, err := io.ReadFull(reader, chunkHeader[:]); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return nil, format, fmt.Errorf("reading chunk header: %w", err)
		}

		chunkID := string(chunkHeader[0:4])
		chunkSize := binary.LittleEndian.Uint32(chunkHeader[4:8])

		switch chunkID {
		case chunkFmt:
			if err := parseFmtChunk(reader, chunkSize, &format); err != nil {
				return nil, format, err
			}

			fmtFound = true

		case chunkData:
			pcmData = make([]byte, chunkSize)
			if _, err := io.ReadFull(reader, pcmData); err != nil {
				return nil, format, fmt.Errorf("reading PCM data: %w", err)
			}

		default:
			// Skip unknown chunks
			if _, err := reader.Seek(int64(chunkSize), io.SeekCurrent); err != nil {
				return nil, format, fmt.Errorf("skipping chunk %s: %w", chunkID, err)
			}
		}

		// Chunks are word-aligned (pad byte if odd size)
		if chunkSize%2 == 1 {
			if _, err := reader.Seek(1, io.SeekCurrent); err != nil {
				return nil, format, fmt.Errorf("seeking past pad byte: %w", err)
			}
		}
	}

	if !fmtFound {
		return nil, format, ErrNoFmtChunk
	}

	if pcmData == nil {
		return nil, format, ErrNoDataChunk
	}

	return pcmData, format, nil
}

func parseFmtChunk(reader io.ReadSeeker, size uint32, format *saprobe.PCMFormat) error {
	if size < 16 {
		return ErrUnsupportedFmt
	}

	var buf [fmtExtensibleSize]byte

	toRead := min(size, fmtExtensibleSize)

	if _, err := io.ReadFull(reader, buf[:toRead]); err != nil {
		return fmt.Errorf("reading fmt chunk: %w", err)
	}

	// Skip remaining bytes if chunk is larger than what we consumed.
	if size > fmtExtensibleSize {
		if _, err := reader.Seek(int64(size-fmtExtensibleSize), io.SeekCurrent); err != nil {
			return fmt.Errorf("skipping fmt chunk tail: %w", err)
		}
	}

	audioFormat := binary.LittleEndian.Uint16(buf[0:2])
	channels := binary.LittleEndian.Uint16(buf[2:4])
	sampleRate := binary.LittleEndian.Uint32(buf[4:8])
	// byteRate at buf[8:12] - not needed
	// blockAlign at buf[12:14] - not needed
	bitsPerSample := binary.LittleEndian.Uint16(buf[14:16])

	switch audioFormat {
	case wavFormatPCM:
		// Standard PCM, we're good

	case wavFormatExtensible:
		if size < fmtExtensibleSize {
			return ErrUnsupportedFmt
		}
		// validBitsPerSample at buf[18:20]
		// channelMask at buf[20:24]
		// SubFormat GUID at buf[24:40]
		var subFormat [16]byte
		copy(subFormat[:], buf[24:fmtExtensibleSize])

		if subFormat != wavGUIDPCM {
			return ErrUnsupportedFmt // Not PCM (could be float, etc.)
		}

	default:
		return ErrUnsupportedFmt
	}

	format.SampleRate = int(sampleRate)
	format.Channels = uint(channels)

	bitDepth := saprobe.BitDepth(bitsPerSample)
	if !slices.Contains(wavBitDepths, bitDepth) {
		return fmt.Errorf("%w: %d", ErrInvalidBitDepth, bitsPerSample)
	}

	format.BitDepth = bitDepth

	return nil
}

// Encode writes PCM samples as a WAV file.
func Encode(writer io.Writer, pcm []byte, format saprobe.PCMFormat) error {
	if !slices.Contains(wavBitDepths, format.BitDepth) {
		return fmt.Errorf("%w: %d", ErrInvalidBitDepth, format.BitDepth)
	}

	channels := uint16(format.Channels)      //nolint:gosec // Channels is 1-8, validated by caller.
	sampleRate := uint32(format.SampleRate)  //nolint:gosec // Sample rate is positive, fits uint32.
	bitsPerSample := uint16(format.BitDepth) //nolint:gosec // BitDepth is validated above (16/24/32).
	byteRate := sampleRate * uint32(channels) * uint32(bitsPerSample) / 8
	blockAlign := channels * bitsPerSample / 8
	dataSize := uint32(len(pcm)) //nolint:gosec // PCM length is bounded by available memory.

	// Use WAVEFORMATEXTENSIBLE for >2 channels or >16 bits
	useExtensible := channels > 2 || bitsPerSample > 16

	if useExtensible {
		return writeWAVExtensible(writer, pcm, channels, sampleRate, bitsPerSample, byteRate, blockAlign, dataSize)
	}

	return writeWAVSimple(writer, pcm, channels, sampleRate, bitsPerSample, byteRate, blockAlign, dataSize)
}

func writeWAVSimple(
	writer io.Writer,
	pcm []byte,
	channels uint16,
	sampleRate uint32,
	bitsPerSample uint16,
	byteRate uint32,
	blockAlign uint16,
	dataSize uint32,
) error {
	var header [44]byte

	copy(header[0:4], chunkRIFF)
	binary.LittleEndian.PutUint32(header[4:8], dataSize+36)
	copy(header[8:12], chunkWAVE)
	copy(header[12:16], chunkFmt)
	binary.LittleEndian.PutUint32(header[16:20], 16) // fmt chunk size
	binary.LittleEndian.PutUint16(header[20:22], wavFormatPCM)
	binary.LittleEndian.PutUint16(header[22:24], channels)
	binary.LittleEndian.PutUint32(header[24:28], sampleRate)
	binary.LittleEndian.PutUint32(header[28:32], byteRate)
	binary.LittleEndian.PutUint16(header[32:34], blockAlign)
	binary.LittleEndian.PutUint16(header[34:36], bitsPerSample)
	copy(header[36:40], chunkData)
	binary.LittleEndian.PutUint32(header[40:44], dataSize)

	if _, err := writer.Write(header[:]); err != nil {
		return fmt.Errorf("writing WAV header: %w", err)
	}

	if _, err := writer.Write(pcm); err != nil {
		return fmt.Errorf("writing PCM data: %w", err)
	}

	return nil
}

func writeWAVExtensible(
	writer io.Writer,
	pcm []byte,
	channels uint16,
	sampleRate uint32,
	bitsPerSample uint16,
	byteRate uint32,
	blockAlign uint16,
	dataSize uint32,
) error {
	// WAVEFORMATEXTENSIBLE: fmt chunk is 40 bytes instead of 16
	fmtChunkSize := uint32(fmtExtensibleSize)
	headerSize := riffHeaderSize + 8 + fmtChunkSize + 8 // RIFF + fmt header + fmt data + data header
	fileSize := headerSize + dataSize - 8               // -8 for RIFF header not counted

	var header [68]byte // 12 + 8 + 40 + 8

	// RIFF header
	copy(header[0:4], chunkRIFF)
	binary.LittleEndian.PutUint32(header[4:8], fileSize)
	copy(header[8:12], chunkWAVE)

	// fmt chunk header
	copy(header[12:16], chunkFmt)
	binary.LittleEndian.PutUint32(header[16:20], fmtChunkSize)

	// WAVEFORMATEX part
	binary.LittleEndian.PutUint16(header[20:22], wavFormatExtensible)
	binary.LittleEndian.PutUint16(header[22:24], channels)
	binary.LittleEndian.PutUint32(header[24:28], sampleRate)
	binary.LittleEndian.PutUint32(header[28:32], byteRate)
	binary.LittleEndian.PutUint16(header[32:34], blockAlign)
	binary.LittleEndian.PutUint16(header[34:36], bitsPerSample)
	binary.LittleEndian.PutUint16(header[36:38], cbSizeExtensible)

	// WAVEFORMATEXTENSIBLE extension
	binary.LittleEndian.PutUint16(header[38:40], bitsPerSample) // validBitsPerSample
	binary.LittleEndian.PutUint32(header[40:44], speakerMask(channels))
	copy(header[44:60], wavGUIDPCM[:])

	// data chunk header
	copy(header[60:64], chunkData)
	binary.LittleEndian.PutUint32(header[64:68], dataSize)

	if _, err := writer.Write(header[:]); err != nil {
		return fmt.Errorf("writing WAV header: %w", err)
	}

	if _, err := writer.Write(pcm); err != nil {
		return fmt.Errorf("writing PCM data: %w", err)
	}

	return nil
}

// speakerMask returns standard speaker position mask for common channel configurations.
func speakerMask(channels uint16) uint32 {
	switch channels {
	case 1:
		return channelMaskMono
	case 2:
		return channelMaskStereo
	case 4:
		return channelMaskQuad
	case 6:
		return channelMask51
	case 8:
		return channelMask71
	default:
		return 0 // Unspecified
	}
}
