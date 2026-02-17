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

package saprobe

import (
	"errors"
	"fmt"
	"io"
)

// BitDepth represents the bit depth of PCM audio samples.
type BitDepth uint

// Standard PCM bit depths.
const (
	Depth4  BitDepth = 4
	Depth8  BitDepth = 8
	Depth12 BitDepth = 12
	Depth16 BitDepth = 16
	Depth20 BitDepth = 20
	Depth24 BitDepth = 24
	Depth32 BitDepth = 32
)

// BytesPerSample returns the number of bytes needed to store one sample.
// Sub-byte depths (4-bit) are stored in 1 byte (sign-extended).
// 12-bit samples are stored in 2 bytes (sign-extended to 16-bit).
// 20-bit samples are stored in 3 bytes (sign-extended to 24-bit).
func (d BitDepth) BytesPerSample() int {
	switch d {
	case Depth4, Depth8:
		return 1
	case Depth12, Depth16:
		return 2
	case Depth20, Depth24:
		return 3
	case Depth32:
		return 4
	default:
		panic(fmt.Sprintf("saprobe: BytesPerSample called with unsupported bit depth %d", d))
	}
}

// PCMFormat describes the format of raw PCM audio data.
type PCMFormat struct {
	SampleRate int
	BitDepth   BitDepth
	Channels   uint
	// IsFloat indicates the samples are IEEE 754 floating-point rather than
	// signed integer. When true, BitDepth is 32 (float32) or 64 (float64).
	IsFloat bool
}

// PCMDecoder streams decoded interleaved little-endian signed PCM.
// Callers obtain one via each codec's NewDecoder function.
// Format is valid immediately after construction. Read fills the caller's
// buffer with decoded PCM bytes, returning io.EOF when the stream is exhausted.
type PCMDecoder interface {
	io.Reader
	// Format returns the PCM output format (sample rate, bit depth, channels).
	Format() PCMFormat
}

// BitOrder represents the bit ordering within each DSD octet.
type BitOrder uint8

const (
	// MSBFirst means the most-significant bit is the earliest sample (DFF/DSDIFF convention).
	MSBFirst BitOrder = iota
	// LSBFirst means the least-significant bit is the earliest sample (DSF convention).
	LSBFirst
)

// DSDFormat describes the format of raw DSD audio data extracted from a container.
type DSDFormat struct {
	// SampleRate is the DSD sample rate in Hz (e.g. 2822400 for DSD64).
	SampleRate int
	// Channels is the number of audio channels.
	Channels uint
	// SamplesPerChannel is the total number of DSD samples (bits) per channel.
	SamplesPerChannel int64
	// BitOrder indicates MSB-first (DFF) or LSB-first (DSF) bit ordering.
	BitOrder BitOrder
}

var errUnsupportedBitDepth = errors.New("unsupported bit depth")

// ToBitDepth converts a numeric bit depth to the BitDepth type.
func ToBitDepth(bps uint8) (BitDepth, error) {
	switch BitDepth(bps) {
	case Depth4:
		return Depth4, nil
	case Depth8:
		return Depth8, nil
	case Depth12:
		return Depth12, nil
	case Depth16:
		return Depth16, nil
	case Depth20:
		return Depth20, nil
	case Depth24:
		return Depth24, nil
	case Depth32:
		return Depth32, nil
	default:
		return 0, fmt.Errorf("%d-bit: %w", bps, errUnsupportedBitDepth)
	}
}
