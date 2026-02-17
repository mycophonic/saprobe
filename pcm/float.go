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
	"math"
)

// Errors returned by FloatToInt, IntToFloat, and FloatConvert.
var (
	ErrInvalidIntDepth   = errors.New("pcm: integer bit depth must be 8, 16, 24, or 32")
	ErrInvalidFloatDepth = errors.New("pcm: float bit depth must be 32 or 64")
	ErrSameFloatDepth    = errors.New("pcm: source and destination float depths are equal")
	ErrSrcAlignment      = errors.New("pcm: source length not aligned to sample size")
	ErrDstTooSmall       = errors.New("pcm: destination buffer too small")
)

const (
	bits8  = 8
	bits16 = 16
	bits24 = 24
	bits32 = 32
	bits64 = 64

	bytesPerFloat32 = 4
	bytesPerFloat64 = 8
	maxInt8         = (1 << 7) - 1
	maxInt16        = (1 << 15) - 1
	maxInt24        = (1 << 23) - 1
	maxInt32        = (1 << 31) - 1
)

// FloatToInt converts interleaved little-endian float PCM samples to
// signed integer PCM.
//
// srcBits must be 32 (float32) or 64 (float64).
// dstBits must be 8, 16, 24, or 32.
//
// Samples are clamped to [-1.0, 1.0] before scaling. Output is
// interleaved little-endian signed integer.
//
// Returns the number of bytes written to dst.
func FloatToInt(dst, src []byte, srcBits, dstBits int) (int, error) {
	srcBytes, err := validateFloat(src, srcBits, dstBits)
	if err != nil {
		return 0, err
	}

	samples := len(src) / srcBytes
	dstBytes := dstBits / bitsPerByte
	needed := samples * dstBytes

	if len(dst) < needed {
		return 0, ErrDstTooSmall
	}

	switch srcBits {
	case bits32:
		float32ToInt(dst, src, dstBits, samples)
	case bits64:
		float64ToInt(dst, src, dstBits, samples)
	default:
	}

	return needed, nil
}

// FloatToIntSize returns the output byte count for converting srcLen bytes
// of float PCM at srcBits depth to the given integer bit depth.
func FloatToIntSize(srcLen, srcBits, dstBits int) int {
	srcBytes := srcBits / bitsPerByte

	if srcBytes == 0 {
		return 0
	}

	samples := srcLen / srcBytes
	dstBytes := dstBits / bitsPerByte

	return samples * dstBytes
}

// IntToFloat converts interleaved little-endian signed integer PCM to
// float PCM.
//
// srcBits must be 8, 16, 24, or 32.
// dstBits must be 32 (float32) or 64 (float64).
//
// Output values are in [-1.0, 1.0]. Uses the same scaling factors as
// FloatToInt for round-trip consistency: sample / maxIntN where
// maxIntN = (1 << (bits-1)) - 1.
//
// Returns the number of bytes written to dst.
func IntToFloat(dst, src []byte, srcBits, dstBits int) (int, error) {
	if !validBits(srcBits) {
		return 0, ErrInvalidIntDepth
	}

	if !validFloatBits(dstBits) {
		return 0, ErrInvalidFloatDepth
	}

	srcBytes := srcBits / bitsPerByte

	if len(src)%srcBytes != 0 {
		return 0, ErrAlignment
	}

	samples := len(src) / srcBytes
	dstBytes := dstBits / bitsPerByte
	needed := samples * dstBytes

	if len(dst) < needed {
		return 0, ErrDstTooSmall
	}

	switch dstBits {
	case bits32:
		intToFloat32(dst, src, srcBits, samples)
	case bits64:
		intToFloat64(dst, src, srcBits, samples)
	default:
	}

	return needed, nil
}

// IntToFloatSize returns the output byte count for converting srcLen bytes
// of integer PCM at srcBits depth to float at dstBits depth.
func IntToFloatSize(srcLen, srcBits, dstBits int) int {
	srcBytes := srcBits / bitsPerByte

	if srcBytes == 0 {
		return 0
	}

	dstBytes := dstBits / bitsPerByte

	return (srcLen / srcBytes) * dstBytes
}

// FloatConvert converts between float32 and float64 PCM representations.
//
// srcBits and dstBits must each be 32 (float32) or 64 (float64), and must
// differ. Conversion from float64 to float32 may lose precision.
//
// Returns the number of bytes written to dst.
func FloatConvert(dst, src []byte, srcBits, dstBits int) (int, error) {
	if !validFloatBits(srcBits) || !validFloatBits(dstBits) {
		return 0, ErrInvalidFloatDepth
	}

	if srcBits == dstBits {
		return 0, ErrSameFloatDepth
	}

	srcBytes := srcBits / bitsPerByte

	if len(src)%srcBytes != 0 {
		return 0, ErrSrcAlignment
	}

	samples := len(src) / srcBytes
	dstBytes := dstBits / bitsPerByte
	needed := samples * dstBytes

	if len(dst) < needed {
		return 0, ErrDstTooSmall
	}

	switch srcBits {
	case bits32:
		for i := range samples {
			writeFloat64(dst, i, float64(readFloat32(src, i)))
		}
	case bits64:
		for i := range samples {
			writeFloat32(dst, i, float32(readFloat64(src, i)))
		}
	default:
	}

	return needed, nil
}

// FloatConvertSize returns the output byte count for converting srcLen bytes
// between float32 and float64.
func FloatConvertSize(srcLen, srcBits, dstBits int) int {
	srcBytes := srcBits / bitsPerByte

	if srcBytes == 0 {
		return 0
	}

	dstBytes := dstBits / bitsPerByte

	return (srcLen / srcBytes) * dstBytes
}

// validateFloat checks float source alignment and bit depths.
// Returns the source bytes per sample.
func validateFloat(src []byte, srcBits, dstBits int) (int, error) {
	if !validFloatBits(srcBits) {
		return 0, ErrInvalidFloatDepth
	}

	if !validBits(dstBits) {
		return 0, ErrInvalidIntDepth
	}

	srcBytes := srcBits / bitsPerByte

	if len(src)%srcBytes != 0 {
		return 0, ErrSrcAlignment
	}

	return srcBytes, nil
}

func validFloatBits(bits int) bool {
	return bits == bits32 || bits == bits64
}

// float32ToInt converts float32 samples to the target integer bit depth.
func float32ToInt(dst, src []byte, dstBits, samples int) {
	switch dstBits {
	case bits8:
		for i := range samples {
			v := int8(clampF64(float64(readFloat32(src, i))) * maxInt8)

			dst[i] = byte(v)
		}

	case bits16:
		for i := range samples {
			v := int16(clampF64(float64(readFloat32(src, i))) * maxInt16)

			binary.LittleEndian.PutUint16(
				dst[i*2:],
				uint16(v), //nolint:gosec // Signed-to-unsigned preserves bit pattern.
			)
		}

	case bits24:
		for i := range samples {
			v := int32(clampF64(float64(readFloat32(src, i))) * maxInt24)
			off := i * 3 //nolint:mnd // 3 bytes per 24-bit sample.

			dst[off] = byte(v)
			dst[off+1] = byte(v >> 8)  //nolint:mnd // Second byte of 24-bit LE.
			dst[off+2] = byte(v >> 16) //nolint:mnd // Third byte of 24-bit LE.
		}

	case bits32:
		for i := range samples {
			v := int32(clampF64(float64(readFloat32(src, i))) * maxInt32)

			binary.LittleEndian.PutUint32(
				dst[i*4:],
				uint32(v), //nolint:gosec // Signed-to-unsigned preserves bit pattern.
			)
		}
	default:
	}
}

// float64ToInt converts float64 samples to the target integer bit depth.
func float64ToInt(dst, src []byte, dstBits, samples int) {
	switch dstBits {
	case bits8:
		for i := range samples {
			v := int8(clampF64(readFloat64(src, i)) * maxInt8)

			dst[i] = byte(v)
		}

	case bits16:
		for i := range samples {
			v := int16(clampF64(readFloat64(src, i)) * maxInt16)

			binary.LittleEndian.PutUint16(
				dst[i*2:],
				uint16(v), //nolint:gosec // Signed-to-unsigned preserves bit pattern.
			)
		}

	case bits24:
		for i := range samples {
			v := int32(clampF64(readFloat64(src, i)) * maxInt24)
			off := i * 3 //nolint:mnd // 3 bytes per 24-bit sample.

			dst[off] = byte(v)
			dst[off+1] = byte(v >> 8)  //nolint:mnd // Second byte of 24-bit LE.
			dst[off+2] = byte(v >> 16) //nolint:mnd // Third byte of 24-bit LE.
		}

	case bits32:
		for i := range samples {
			v := int32(clampF64(readFloat64(src, i)) * maxInt32)

			binary.LittleEndian.PutUint32(
				dst[i*4:],
				uint32(v), //nolint:gosec // Signed-to-unsigned preserves bit pattern.
			)
		}
	default:
	}
}

// intToFloat32 converts integer samples to float32 at the given source bit depth.
//
//nolint:dupl // Float32/64 variants are structurally identical but type-distinct.
func intToFloat32(
	dst, src []byte,
	srcBits, samples int,
) {
	switch srcBits {
	case bits8:
		for i := range samples {
			v := int8(src[i])

			writeFloat32(dst, i, float32(v)/maxInt8)
		}

	case bits16:
		for i := range samples {
			v := int16( //nolint:gosec // Unsigned-to-signed preserves bit pattern.
				binary.LittleEndian.Uint16(src[i*2:]),
			)

			writeFloat32(dst, i, float32(v)/maxInt16)
		}

	case bits24:
		for sample := range samples {
			off := sample * 3 //nolint:mnd // 3 bytes per 24-bit sample.

			// Read 24-bit LE and sign-extend to int32.
			val := int32(
				src[off],
			) | int32(
				src[off+1],
			)<<8 | int32(
				src[off+2],
			)<<16 //nolint:mnd // Byte shifts for 24-bit LE.
			val = (val << 8) >> 8 //nolint:mnd // Sign-extend 24→32.

			writeFloat32(dst, sample, float32(val)/maxInt24)
		}

	case bits32:
		for i := range samples {
			v := int32( //nolint:gosec // Unsigned-to-signed preserves bit pattern.
				binary.LittleEndian.Uint32(src[i*4:]),
			)

			writeFloat32(dst, i, float32(v)/maxInt32)
		}
	default:
	}
}

// intToFloat64 converts integer samples to float64 at the given source bit depth.
//
//nolint:dupl // Float32/64 variants are structurally identical but type-distinct.
func intToFloat64(
	dst, src []byte,
	srcBits, samples int,
) {
	switch srcBits {
	case bits8:
		for i := range samples {
			v := int8(src[i])

			writeFloat64(dst, i, float64(v)/maxInt8)
		}

	case bits16:
		for i := range samples {
			v := int16( //nolint:gosec // Unsigned-to-signed preserves bit pattern.
				binary.LittleEndian.Uint16(src[i*2:]),
			)

			writeFloat64(dst, i, float64(v)/maxInt16)
		}

	case bits24:
		for sample := range samples {
			off := sample * 3 //nolint:mnd // 3 bytes per 24-bit sample.

			val := int32(
				src[off],
			) | int32(
				src[off+1],
			)<<8 | int32(
				src[off+2],
			)<<16 //nolint:mnd // Byte shifts for 24-bit LE.
			val = (val << 8) >> 8 //nolint:mnd // Sign-extend 24→32.

			writeFloat64(dst, sample, float64(val)/maxInt24)
		}

	case bits32:
		for i := range samples {
			v := int32( //nolint:gosec // Unsigned-to-signed preserves bit pattern.
				binary.LittleEndian.Uint32(src[i*4:]),
			)

			writeFloat64(dst, i, float64(v)/maxInt32)
		}
	default:
	}
}

func readFloat32(src []byte, i int) float32 {
	bits := binary.LittleEndian.Uint32(src[i*bytesPerFloat32:])

	return math.Float32frombits(bits)
}

func readFloat64(src []byte, i int) float64 {
	bits := binary.LittleEndian.Uint64(src[i*bytesPerFloat64:])

	return math.Float64frombits(bits)
}

func writeFloat32(dst []byte, i int, v float32) {
	binary.LittleEndian.PutUint32(dst[i*bytesPerFloat32:], math.Float32bits(v))
}

func writeFloat64(dst []byte, i int, v float64) {
	binary.LittleEndian.PutUint64(dst[i*bytesPerFloat64:], math.Float64bits(v))
}

// clampF64 clamps a float64 sample to [-1.0, 1.0].
func clampF64(sample float64) float64 {
	if sample > 1.0 {
		return 1.0
	}

	if sample < -1.0 {
		return -1.0
	}

	return sample
}
