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

//nolint:gochecknoglobals // the bit depths WAV carries, read-only
var wavBitDepths = []saprobe.BitDepth{
	saprobe.Depth16,
	saprobe.Depth24,
	saprobe.Depth32,
}

// WAV format constants.
const (
	wavFormatPCM        = 1
	wavFormatIEEEFloat  = 3
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
//nolint:gochecknoglobals // a WAVE_FORMAT_EXTENSIBLE subformat GUID, read-only
var wavGUIDPCM = [16]byte{
	0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x10, 0x00,
	0x80, 0x00, 0x00, 0xaa, 0x00, 0x38, 0x9b, 0x71,
}

// GUID for IEEE float in WAVEFORMATEXTENSIBLE.
//
//nolint:gochecknoglobals // a WAVE_FORMAT_EXTENSIBLE subformat GUID, read-only
var wavGUIDFloat = [16]byte{
	0x03, 0x00, 0x00, 0x00, 0x00, 0x00, 0x10, 0x00,
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

// Decoder streams decoded PCM from a WAV source.
type Decoder struct {
	reader   io.Reader
	format   saprobe.PCMFormat
	dataSize int64
	read     int64
}

// NewDecoder opens a WAV file and returns a streaming decoder.
// The fmt chunk is parsed immediately; PCM data is read on demand via Read.
func NewDecoder(reader io.ReadSeeker) (*Decoder, error) {
	var format saprobe.PCMFormat

	// Read RIFF header.
	var riffHeader [12]byte
	if _, err := io.ReadFull(reader, riffHeader[:]); err != nil {
		return nil, fmt.Errorf("reading RIFF header: %w", err)
	}

	if string(riffHeader[0:4]) != chunkRIFF || string(riffHeader[8:12]) != chunkWAVE {
		return nil, ErrNotWAV
	}

	// Parse chunks until we find both fmt and data.
	fmtFound := false

	var dataSize uint32

	for {
		var chunkHeader [8]byte
		if _, err := io.ReadFull(reader, chunkHeader[:]); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return nil, fmt.Errorf("reading chunk header: %w", err)
		}

		chunkID := string(chunkHeader[0:4])
		chunkSize := binary.LittleEndian.Uint32(chunkHeader[4:8])

		switch chunkID {
		case chunkFmt:
			if err := parseFmtChunk(reader, chunkSize, &format); err != nil {
				return nil, err
			}

			fmtFound = true

		case chunkData:
			if !fmtFound {
				return nil, ErrNoFmtChunk
			}

			dataSize = chunkSize

			// Reader is now positioned at the start of PCM data.
			return &Decoder{
				reader:   reader,
				format:   format,
				dataSize: int64(dataSize),
			}, nil

		default:
			if _, err := reader.Seek(int64(chunkSize), io.SeekCurrent); err != nil {
				return nil, fmt.Errorf("skipping chunk %s: %w", chunkID, err)
			}
		}

		// Chunks are word-aligned (pad byte if odd size).
		if chunkSize%2 == 1 {
			if _, err := reader.Seek(1, io.SeekCurrent); err != nil {
				return nil, fmt.Errorf("seeking past pad byte: %w", err)
			}
		}
	}

	if !fmtFound {
		return nil, ErrNoFmtChunk
	}

	return nil, ErrNoDataChunk
}

// Format returns the PCM output format.
func (d *Decoder) Format() saprobe.PCMFormat { return d.format }

// Read reads decoded PCM bytes from the WAV data chunk.
func (d *Decoder) Read(p []byte) (int, error) {
	remaining := d.dataSize - d.read
	if remaining <= 0 {
		return 0, io.EOF
	}

	if int64(len(p)) > remaining {
		p = p[:remaining]
	}

	n, err := d.reader.Read(p)
	d.read += int64(n)

	if d.read >= d.dataSize && err == nil {
		err = io.EOF
	}

	return n, err
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

	isFloat := false

	switch audioFormat {
	case wavFormatPCM:
	// Standard integer PCM.

	case wavFormatIEEEFloat:
		isFloat = true

	case wavFormatExtensible:
		if size < fmtExtensibleSize {
			return ErrUnsupportedFmt
		}
		// validBitsPerSample at buf[18:20]
		// channelMask at buf[20:24]
		// SubFormat GUID at buf[24:40]
		var subFormat [16]byte
		copy(subFormat[:], buf[24:fmtExtensibleSize])

		switch subFormat {
		case wavGUIDPCM:
		// Integer PCM.
		case wavGUIDFloat:
			isFloat = true
		default:
			return ErrUnsupportedFmt
		}

	default:
		return ErrUnsupportedFmt
	}

	format.SampleRate = int(sampleRate)
	format.Channels = uint(channels)
	format.IsFloat = isFloat

	bitDepth := saprobe.BitDepth(bitsPerSample)
	if !slices.Contains(wavBitDepths, bitDepth) {
		return fmt.Errorf("%w: %d", ErrInvalidBitDepth, bitsPerSample)
	}

	format.BitDepth = bitDepth

	return nil
}
