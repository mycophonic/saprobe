/*
Copyright Mycophonic.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package wav

import (
	"encoding/binary"
	"fmt"
	"io"
	"slices"

	"github.com/mycophonic/saprobe"
)

// wavParams bundles the numeric fields derived from saprobe.PCMFormat
// that are needed by the WAV header writers.
type wavParams struct {
	channels      uint16
	sampleRate    uint32
	bitsPerSample uint16
	byteRate      uint32
	blockAlign    uint16
	dataSize      uint32
	isFloat       bool
}

// Encode writes PCM samples as a WAV file.
func Encode(writer io.Writer, pcm []byte, format saprobe.PCMFormat) error {
	if !slices.Contains(wavBitDepths, format.BitDepth) {
		return fmt.Errorf("%w: %d", ErrInvalidBitDepth, format.BitDepth)
	}

	params := wavParams{
		channels:      uint16(format.Channels),   //nolint:gosec // Channels is 1-8, validated by caller.
		sampleRate:    uint32(format.SampleRate), //nolint:gosec // Sample rate is positive, fits uint32.
		bitsPerSample: uint16(format.BitDepth),   //nolint:gosec // BitDepth is validated above (16/24/32).
		dataSize:      uint32(len(pcm)),          //nolint:gosec // PCM length is bounded by available memory.
		isFloat:       format.IsFloat,
	}

	params.byteRate = params.sampleRate * uint32(params.channels) * uint32(params.bitsPerSample) / 8
	params.blockAlign = params.channels * params.bitsPerSample / 8

	// Use WAVEFORMATEXTENSIBLE for >2 channels or >16 bits
	useExtensible := params.channels > 2 || params.bitsPerSample > 16

	if useExtensible {
		return writeWAVExtensible(writer, pcm, params)
	}

	return writeWAVSimple(writer, pcm, params)
}

func writeWAVSimple(writer io.Writer, pcm []byte, wparams wavParams) error {
	var header [44]byte

	copy(header[0:4], chunkRIFF)
	binary.LittleEndian.PutUint32(header[4:8], wparams.dataSize+36)
	copy(header[8:12], chunkWAVE)
	copy(header[12:16], chunkFmt)
	binary.LittleEndian.PutUint32(header[16:20], 16) // fmt chunk size
	binary.LittleEndian.PutUint16(header[20:22], wavFormatPCM)
	binary.LittleEndian.PutUint16(header[22:24], wparams.channels)
	binary.LittleEndian.PutUint32(header[24:28], wparams.sampleRate)
	binary.LittleEndian.PutUint32(header[28:32], wparams.byteRate)
	binary.LittleEndian.PutUint16(header[32:34], wparams.blockAlign)
	binary.LittleEndian.PutUint16(header[34:36], wparams.bitsPerSample)
	copy(header[36:40], chunkData)
	binary.LittleEndian.PutUint32(header[40:44], wparams.dataSize)

	if _, err := writer.Write(header[:]); err != nil {
		return fmt.Errorf("writing WAV header: %w", err)
	}

	if _, err := writer.Write(pcm); err != nil {
		return fmt.Errorf("writing PCM data: %w", err)
	}

	return nil
}

func writeWAVExtensible(writer io.Writer, pcm []byte, wparams wavParams) error {
	// WAVEFORMATEXTENSIBLE: fmt chunk is 40 bytes instead of 16
	fmtChunkSize := uint32(fmtExtensibleSize)
	headerSize := riffHeaderSize + 8 + fmtChunkSize + 8 // RIFF + fmt header + fmt data + data header
	fileSize := headerSize + wparams.dataSize - 8       // -8 for RIFF header not counted

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
	binary.LittleEndian.PutUint16(header[22:24], wparams.channels)
	binary.LittleEndian.PutUint32(header[24:28], wparams.sampleRate)
	binary.LittleEndian.PutUint32(header[28:32], wparams.byteRate)
	binary.LittleEndian.PutUint16(header[32:34], wparams.blockAlign)
	binary.LittleEndian.PutUint16(header[34:36], wparams.bitsPerSample)
	binary.LittleEndian.PutUint16(header[36:38], cbSizeExtensible)

	// WAVEFORMATEXTENSIBLE extension
	binary.LittleEndian.PutUint16(header[38:40], wparams.bitsPerSample) // validBitsPerSample
	binary.LittleEndian.PutUint32(header[40:44], speakerMask(wparams.channels))

	if wparams.isFloat {
		copy(header[44:60], wavGUIDFloat[:])
	} else {
		copy(header[44:60], wavGUIDPCM[:])
	}

	// data chunk header
	copy(header[60:64], chunkData)
	binary.LittleEndian.PutUint32(header[64:68], wparams.dataSize)

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
