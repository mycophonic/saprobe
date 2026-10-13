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

// Package aiff implements AIFF and AIFF-C decoding for PCM audio.
package aiff

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"

	"github.com/mycophonic/saprobe"
)

//nolint:gochecknoglobals // the bit depths AIFF carries, read-only
var aiffBitDepths = []saprobe.BitDepth{
	saprobe.Depth16,
	saprobe.Depth24,
	saprobe.Depth32,
}

// Container markers and chunk IDs.
const (
	chunkFORM = "FORM"
	formAIFF  = "AIFF"
	formAIFC  = "AIFC"
	chunkCOMM = "COMM"
	chunkSSND = "SSND"
)

// AIFF-C compression types.
const (
	compNone = "NONE" // Uncompressed big-endian PCM.
	compSowt = "sowt" // Little-endian PCM (byte-swapped).
)

// Chunk and header sizes.
const (
	formHeaderSize = 12 // "FORM"(4) + file size(4) + form type(4).
	commBaseSize   = 18 // channels(2) + frames(4) + sampleSize(2) + rate(10).
	ssndPrefixSize = 8  // offset(4) + blockSize(4) before sound data.
)

// IEEE 754 extended precision constants for the AIFF sample rate field.
const (
	extendedBias     = 16383  // Exponent bias for 80-bit extended precision.
	extendedMantBits = 63     // Mantissa bit count (excluding implicit integer bit).
	exponentMask     = 0x7FFF // 15-bit exponent mask.
)

// ErrNotAIFF indicates the input is not a valid AIFF or AIFF-C file.
var ErrNotAIFF = errors.New("not an AIFF file")

// ErrUnsupportedFmt indicates the AIFF file uses an unsupported compression type.
var ErrUnsupportedFmt = errors.New("unsupported AIFF format")

// ErrNoCommChunk indicates the AIFF file is missing the required COMM chunk.
var ErrNoCommChunk = errors.New("missing COMM chunk")

// ErrNoSsndChunk indicates the AIFF file is missing the required SSND chunk.
var ErrNoSsndChunk = errors.New("missing SSND chunk")

// ErrInvalidBitDepth indicates an unsupported bit depth for AIFF decoding.
var ErrInvalidBitDepth = errors.New("invalid bit depth")

// Decoder streams decoded PCM from an AIFF or AIFF-C source.
// Output is always interleaved little-endian signed PCM.
type Decoder struct {
	reader         io.Reader
	format         saprobe.PCMFormat
	dataSize       int64
	read           int64
	bigEndian      bool
	bytesPerSample int
}

// NewDecoder opens an AIFF file and returns a streaming decoder.
// The COMM chunk is parsed immediately; PCM data is read on demand via Read.
// Both AIFF and AIFF-C (with NONE or sowt compression) are supported.
func NewDecoder(reader io.ReadSeeker) (*Decoder, error) {
	formType, err := readFormHeader(reader)
	if err != nil {
		return nil, err
	}

	return parseChunks(reader, formType)
}

// Format returns the PCM output format.
func (d *Decoder) Format() saprobe.PCMFormat { return d.format }

// Read reads decoded PCM bytes from the AIFF sound data.
// Big-endian samples are byte-swapped to little-endian on the fly.
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

	if d.bigEndian && n > 0 {
		swapEndian(p[:n], d.bytesPerSample)
	}

	if d.read >= d.dataSize && err == nil {
		err = io.EOF
	}

	return n, err
}

// readFormHeader validates the FORM container header and returns the form type
// ("AIFF" or "AIFC").
func readFormHeader(reader io.ReadSeeker) (string, error) {
	var formHeader [formHeaderSize]byte
	if _, err := io.ReadFull(reader, formHeader[:]); err != nil {
		return "", fmt.Errorf("reading FORM header: %w", err)
	}

	if string(formHeader[0:4]) != chunkFORM {
		return "", ErrNotAIFF
	}

	formType := string(formHeader[8:formHeaderSize])
	if formType != formAIFF && formType != formAIFC {
		return "", ErrNotAIFF
	}

	return formType, nil
}

// parseChunks iterates IFF chunks to find COMM and SSND, then returns a Decoder
// positioned at the start of the sound data.
//
//nolint:gocognit // the IFF chunk walk, one branch per chunk kind plus the pad byte: the container's own
func parseChunks(reader io.ReadSeeker, formType string) (*Decoder, error) {
	var format saprobe.PCMFormat

	commFound := false
	bigEndian := true

	for {
		var chunkHeader [8]byte
		if _, err := io.ReadFull(reader, chunkHeader[:]); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return nil, fmt.Errorf("reading chunk header: %w", err)
		}

		chunkID := string(chunkHeader[0:4])
		chunkSize := binary.BigEndian.Uint32(chunkHeader[4:8])

		switch chunkID {
		case chunkCOMM:
			var commErr error

			bigEndian, commErr = parseCommChunk(reader, chunkSize, formType, &format)
			if commErr != nil {
				return nil, commErr
			}

			commFound = true

		case chunkSSND:
			if !commFound {
				return nil, ErrNoCommChunk
			}

			return parseSsndChunk(reader, chunkSize, format, bigEndian)

		default:
			if _, err := reader.Seek(int64(chunkSize), io.SeekCurrent); err != nil {
				return nil, fmt.Errorf("skipping chunk %s: %w", chunkID, err)
			}
		}

		// IFF chunks are word-aligned (pad byte if odd size).
		if chunkSize%2 == 1 {
			if _, err := reader.Seek(1, io.SeekCurrent); err != nil {
				return nil, fmt.Errorf("seeking past pad byte: %w", err)
			}
		}
	}

	if !commFound {
		return nil, ErrNoCommChunk
	}

	return nil, ErrNoSsndChunk
}

// parseSsndChunk reads the SSND prefix, seeks past any offset padding,
// and returns a Decoder positioned at the start of sample data.
func parseSsndChunk(
	reader io.ReadSeeker, chunkSize uint32, format saprobe.PCMFormat, bigEndian bool,
) (*Decoder, error) {
	var ssndPrefix [ssndPrefixSize]byte
	if _, err := io.ReadFull(reader, ssndPrefix[:]); err != nil {
		return nil, fmt.Errorf("reading SSND header: %w", err)
	}

	ssndOffset := binary.BigEndian.Uint32(ssndPrefix[0:4])

	if ssndOffset > 0 {
		if _, err := reader.Seek(int64(ssndOffset), io.SeekCurrent); err != nil {
			return nil, fmt.Errorf("seeking past SSND offset: %w", err)
		}
	}

	dataSize := int64(chunkSize) - int64(ssndPrefixSize) - int64(ssndOffset)

	return &Decoder{
		reader:         reader,
		format:         format,
		dataSize:       dataSize,
		bigEndian:      bigEndian,
		bytesPerSample: format.BitDepth.BytesPerSample(),
	}, nil
}

// parseCommChunk reads the COMM chunk fields and returns whether the PCM data
// is big-endian. The formType ("AIFF" or "AIFC") determines whether a
// compression type field is expected.
func parseCommChunk(
	reader io.ReadSeeker, size uint32, formType string, format *saprobe.PCMFormat,
) (bool, error) {
	if size < commBaseSize {
		return true, ErrUnsupportedFmt
	}

	var buf [commBaseSize]byte
	if _, err := io.ReadFull(reader, buf[:]); err != nil {
		return true, fmt.Errorf("reading COMM chunk: %w", err)
	}

	channels := binary.BigEndian.Uint16(buf[0:2])
	// numSampleFrames at buf[2:6] — not needed for streaming.
	sampleSize := binary.BigEndian.Uint16(buf[6:8])
	sampleRate := extendedToFloat64(buf[8:commBaseSize])

	format.SampleRate = int(math.Round(sampleRate))
	format.Channels = uint(channels)

	bitDepth := saprobe.BitDepth(sampleSize)
	if !slices.Contains(aiffBitDepths, bitDepth) {
		return true, fmt.Errorf("%w: %d", ErrInvalidBitDepth, sampleSize)
	}

	format.BitDepth = bitDepth

	bigEndian := true
	remaining := size - commBaseSize

	if formType == formAIFC && remaining >= 4 {
		var compType [4]byte
		if _, err := io.ReadFull(reader, compType[:]); err != nil {
			return true, fmt.Errorf("reading AIFF-C compression type: %w", err)
		}

		remaining -= 4

		switch string(compType[:]) {
		case compNone:
			// Uncompressed big-endian — same as AIFF.
		case compSowt:
			bigEndian = false
		default:
			return true, fmt.Errorf("%w: compression %q", ErrUnsupportedFmt, string(compType[:]))
		}
	}

	if remaining > 0 {
		if _, err := reader.Seek(int64(remaining), io.SeekCurrent); err != nil {
			return true, fmt.Errorf("skipping COMM chunk tail: %w", err)
		}
	}

	return bigEndian, nil
}

// extendedToFloat64 converts a 10-byte 80-bit IEEE 754 extended precision
// float to float64. Used for the AIFF sample rate field.
func extendedToFloat64(extended []byte) float64 {
	exponent := int(binary.BigEndian.Uint16(extended[0:2])) & exponentMask
	mantissa := binary.BigEndian.Uint64(extended[2:])

	if exponent == 0 && mantissa == 0 {
		return 0.0
	}

	result := math.Ldexp(float64(mantissa), exponent-extendedBias-extendedMantBits)

	if extended[0]>>7 != 0 {
		result = -result
	}

	return result
}

// swapEndian reverses the byte order of each sample in buf,
// converting big-endian PCM to little-endian.
func swapEndian(buf []byte, bytesPerSample int) {
	switch bytesPerSample {
	case 2:
		for i := 0; i+1 < len(buf); i += 2 {
			buf[i], buf[i+1] = buf[i+1], buf[i]
		}
	case 3:
		for i := 0; i+2 < len(buf); i += 3 {
			buf[i], buf[i+2] = buf[i+2], buf[i]
		}
	case 4:
		for i := 0; i+3 < len(buf); i += 4 {
			buf[i], buf[i+3] = buf[i+3], buf[i]
			buf[i+1], buf[i+2] = buf[i+2], buf[i+1]
		}
	default:
	}
}
