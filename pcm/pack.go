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

// Package pcm provides PCM sample transformations: bit packing and
// float-to-integer conversion.
//
// All operations are pure byte-level transforms with no platform dependencies.
package pcm

import "errors"

// Errors returned by Pack.
var (
	ErrBitDepth  = errors.New("pcm: unsupported bit depth (must be 8, 16, 24, or 32)")
	ErrNarrow    = errors.New("pcm: destination narrower than source")
	ErrShortDst  = errors.New("pcm: destination buffer too small")
	ErrAlignment = errors.New("pcm: source length not aligned to sample size")
)

const bitsPerByte = 8

// Pack packs src samples from srcBits width into dstBits containers
// in little-endian byte order.
//
// When alignHigh is true (MSB-justified), source bytes occupy the high
// bytes of each container and low bytes are zero-padded. This is the
// CoreAudio convention for non-mixable integer formats with IsAlignedHigh.
//
// When alignHigh is false (LSB-justified), source bytes occupy the low
// bytes of each container and high bytes are zero-padded.
//
// Returns the number of bytes written to dst.
func Pack(dst, src []byte, srcBits, dstBits int, alignHigh bool) (int, error) {
	srcBytes := srcBits / bitsPerByte
	dstBytes := dstBits / bitsPerByte

	if err := validate(src, dst, srcBits, dstBits, srcBytes, dstBytes); err != nil {
		return 0, err
	}

	if srcBytes == dstBytes {
		return copy(dst, src), nil
	}

	samples := len(src) / srcBytes
	padBytes := dstBytes - srcBytes

	if alignHigh {
		// LE MSB-justified: zero-pad low bytes, source in high bytes.
		for i := range samples {
			srcOff := i * srcBytes
			dstOff := i * dstBytes

			for j := range padBytes {
				dst[dstOff+j] = 0
			}

			copy(dst[dstOff+padBytes:dstOff+dstBytes], src[srcOff:srcOff+srcBytes])
		}
	} else {
		// LE LSB-justified: source in low bytes, zero-pad high bytes.
		for i := range samples {
			srcOff := i * srcBytes
			dstOff := i * dstBytes

			copy(dst[dstOff:dstOff+srcBytes], src[srcOff:srcOff+srcBytes])

			for j := range padBytes {
				dst[dstOff+srcBytes+j] = 0
			}
		}
	}

	return samples * dstBytes, nil
}

// PackedSize returns the output byte count for packing srcLen input bytes
// from srcBits to dstBits.
func PackedSize(srcLen, srcBits, dstBits int) int {
	srcBytes := srcBits / bitsPerByte
	dstBytes := dstBits / bitsPerByte

	if srcBytes == 0 {
		return 0
	}

	return (srcLen / srcBytes) * dstBytes
}

func validate(src, dst []byte, srcBits, dstBits, srcBytes, dstBytes int) error {
	if !validBits(srcBits) || !validBits(dstBits) {
		return ErrBitDepth
	}

	if dstBits < srcBits {
		return ErrNarrow
	}

	if len(src)%srcBytes != 0 {
		return ErrAlignment
	}

	samples := len(src) / srcBytes
	if len(dst) < samples*dstBytes {
		return ErrShortDst
	}

	return nil
}

func validBits(bits int) bool {
	return bits == 8 || bits == 16 || bits == 24 || bits == 32
}
