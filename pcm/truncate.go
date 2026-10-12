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

package pcm

import (
	"encoding/binary"
	"errors"
	"time"
)

// Errors returned by Truncate.
var (
	ErrWiden        = errors.New("pcm: destination wider than source (use Pack)")
	ErrSameBitDepth = errors.New("pcm: source and destination bit depths are equal")
)

// xorshift128+ PRNG constants.
const (
	// xsFallbackSeed is used when the system clock returns zero.
	xsFallbackSeed = 0x853c49e6748fea9b
	// xsGoldenGamma is the golden ratio constant (2^64 / phi) used to spread
	// bits in the initial state.
	xsGoldenGamma = 0x9e3779b97f4a7c15

	// xorshift128+ shift and rotation widths (from Vigna 2017).
	xsShiftA  = 55 // Left shift of s0.
	xsShiftB  = 9  // Complement right shift of s0 (64 - xsShiftA).
	xsShiftC  = 14 // Left shift of s1.
	xsRotateA = 36 // Left rotation of s1.
	xsRotateB = 28 // Complement right rotation of s1 (64 - xsRotateA).
)

// Ditherer holds PRNG state for TPDF-dithered bit truncation.
// A single Ditherer should be reused across calls within one playback
// session. Not safe for concurrent use by multiple goroutines.
type Ditherer struct {
	s0, s1 uint64
}

// NewDitherer creates a Ditherer seeded from the system clock.
func NewDitherer() *Ditherer {
	seed := uint64(time.Now().UnixNano())
	if seed == 0 {
		seed = xsFallbackSeed
	}

	return &Ditherer{
		s0: seed,
		s1: seed ^ xsGoldenGamma,
	}
}

// Truncate narrows src samples from srcBits to dstBits with TPDF dither.
// Source and destination are little-endian signed integers.
// Supported depths: 16, 24, 32 (srcBits > dstBits required).
//
// Returns the number of bytes written to dst.
func (d *Ditherer) Truncate(dst, src []byte, srcBits, dstBits int) (int, error) {
	if !validBits(srcBits) || !validBits(dstBits) {
		return 0, ErrBitDepth
	}

	if dstBits > srcBits {
		return 0, ErrWiden
	}

	if dstBits == srcBits {
		return 0, ErrSameBitDepth
	}

	srcBytes := srcBits / bitsPerByte

	if len(src)%srcBytes != 0 {
		return 0, ErrAlignment
	}

	samples := len(src) / srcBytes
	dstBytes := dstBits / bitsPerByte
	needed := samples * dstBytes

	if len(dst) < needed {
		return 0, ErrShortDst
	}

	shift := uint(srcBits - dstBits)
	mask := int64(1)<<shift - 1 // [0, lsb-1] for uniform random range.
	maxOut := int32(1)<<(dstBits-1) - 1
	minOut := -maxOut - 1

	for idx := range samples {
		sample := readSample(src, idx*srcBytes, srcBytes)

		// TPDF dither: r1 - r2 gives triangular distribution in [-(lsb-1), +(lsb-1)].
		r1 := int64(d.next() & uint64(mask)) // #nosec G115 -- mask is non-negative (shift ≤ 24).
		r2 := int64(d.next() & uint64(mask)) // #nosec G115 -- mask is non-negative (shift ≤ 24).
		dithered := int64(sample) + r1 - r2

		// Arithmetic right-shift to truncate.
		out := int32(dithered >> shift) // #nosec G115 -- Result clamped to target range below.

		// Clamp to target range.
		if out > maxOut {
			out = maxOut
		} else if out < minOut {
			out = minOut
		}

		writeSample(dst, idx*dstBytes, out, dstBytes)
	}

	return needed, nil
}

// TruncateSize returns the output byte count for truncating srcLen bytes
// from srcBits to dstBits.
func TruncateSize(srcLen, srcBits, dstBits int) int {
	srcBytes := srcBits / bitsPerByte

	if srcBytes == 0 {
		return 0
	}

	dstBytes := dstBits / bitsPerByte

	return (srcLen / srcBytes) * dstBytes
}

// readSample reads a signed integer sample from src at the given byte offset.
// srcBytes must be 1, 2, 3, or 4.
func readSample(src []byte, off, srcBytes int) int32 {
	switch srcBytes {
	case 2:
		return int32(
			int16(binary.LittleEndian.Uint16(src[off:])), // #nosec G115 -- Unsigned-to-signed preserves bit pattern.
		)
	case 3:
		v := int32(src[off]) | int32(src[off+1])<<8 | int32(src[off+2])<<16

		return (v << 8) >> 8
	case 4:
		return int32(binary.LittleEndian.Uint32(src[off:])) // #nosec G115 -- Unsigned-to-signed preserves bit pattern.
	default:
		return int32(int8(src[off]))
	}
}

// writeSample writes a signed integer sample to dst at the given byte offset.
// dstBytes must be 1, 2, 3, or 4.
func writeSample(dst []byte, off int, val int32, dstBytes int) {
	switch dstBytes {
	case 2:
		binary.LittleEndian.PutUint16(
			dst[off:],
			uint16(int16(val)), // #nosec G115 -- val clamped to int16 range by caller.
		)
	case 3:
		dst[off] = byte(val)
		dst[off+1] = byte(val >> 8)
		dst[off+2] = byte(val >> 16)
	case 4:
		binary.LittleEndian.PutUint32(
			dst[off:],
			uint32(val), // #nosec G115 -- Signed-to-unsigned preserves bit pattern.
		)
	default:
		dst[off] = byte(val)
	}
}

// next returns the next pseudo-random uint64 using xorshift128+.
func (d *Ditherer) next() uint64 {
	s0, s1 := d.s0, d.s1
	result := s0 + s1

	s1 ^= s0
	d.s0 = (s0<<xsShiftA | s0>>xsShiftB) ^ s1 ^ (s1 << xsShiftC)
	d.s1 = s1<<xsRotateA | s1>>xsRotateB

	return result
}
