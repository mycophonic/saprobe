package alac

import "encoding/binary"

// Matrix unmix and output byte formatting.
// Ported from matrix_dec.c.
//
// All output is interleaved little-endian signed PCM.

// --- Stereo unmix (channel pair) ---

func writeStereo16(out []byte, mixU, mixV []int32, chanIdx, numChan, numSamples int, mixBits, mixRes int32) {
	stride := numChan * 2
	pos := chanIdx * 2

	if mixRes != 0 {
		for idx := range numSamples {
			left := mixU[idx] + mixV[idx] - ((mixRes * mixV[idx]) >> mixBits)
			right := left - mixV[idx]

			dst := out[pos : pos+4 : pos+4]
			dst[0] = byte(left)
			dst[1] = byte(left >> 8)
			dst[2] = byte(right)
			dst[3] = byte(right >> 8)
			pos += stride
		}
	} else {
		for idx := range numSamples {
			l := mixU[idx]
			r := mixV[idx]
			dst := out[pos : pos+4 : pos+4]
			dst[0] = byte(l)
			dst[1] = byte(l >> 8)
			dst[2] = byte(r)
			dst[3] = byte(r >> 8)
			pos += stride
		}
	}
}

func writeStereo20(out []byte, mixU, mixV []int32, chanIdx, numChan, numSamples int, mixBits, mixRes int32) {
	stride := numChan * 3
	pos := chanIdx * 3

	if mixRes != 0 {
		for idx := range numSamples {
			left := mixU[idx] + mixV[idx] - ((mixRes * mixV[idx]) >> mixBits)
			right := left - mixV[idx]
			left <<= 4
			right <<= 4

			dst := out[pos : pos+6 : pos+6]
			dst[0] = byte(left)
			dst[1] = byte(left >> 8)
			dst[2] = byte(left >> 16)
			dst[3] = byte(right)
			dst[4] = byte(right >> 8)
			dst[5] = byte(right >> 16)
			pos += stride
		}
	} else {
		for idx := range numSamples {
			dst := out[pos : pos+6 : pos+6]
			val := mixU[idx] << 4
			dst[0] = byte(val)
			dst[1] = byte(val >> 8)
			dst[2] = byte(val >> 16)

			val = mixV[idx] << 4
			dst[3] = byte(val)
			dst[4] = byte(val >> 8)
			dst[5] = byte(val >> 16)
			pos += stride
		}
	}
}

//revive:disable-next-line:argument-limit
func writeStereo24(out []byte, mixU, mixV []int32, chanIdx, numChan, numSamples int,
	mixBits, mixRes int32, shiftBuf []uint16, bytesShifted int,
) {
	stride := numChan * 3
	pos := chanIdx * 3
	shift := bytesShifted * 8

	if mixRes != 0 {
		for idx := range numSamples {
			left := mixU[idx] + mixV[idx] - ((mixRes * mixV[idx]) >> mixBits)
			right := left - mixV[idx]

			if bytesShifted != 0 {
				left = (left << shift) | int32(shiftBuf[idx*2+0])
				right = (right << shift) | int32(shiftBuf[idx*2+1])
			}

			dst := out[pos : pos+6 : pos+6]
			dst[0] = byte(left)
			dst[1] = byte(left >> 8)
			dst[2] = byte(left >> 16)
			dst[3] = byte(right)
			dst[4] = byte(right >> 8)
			dst[5] = byte(right >> 16)
			pos += stride
		}
	} else {
		for idx := range numSamples {
			left := mixU[idx]
			right := mixV[idx]

			if bytesShifted != 0 {
				left = (left << shift) | int32(shiftBuf[idx*2+0])
				right = (right << shift) | int32(shiftBuf[idx*2+1])
			}

			dst := out[pos : pos+6 : pos+6]
			dst[0] = byte(left)
			dst[1] = byte(left >> 8)
			dst[2] = byte(left >> 16)
			dst[3] = byte(right)
			dst[4] = byte(right >> 8)
			dst[5] = byte(right >> 16)
			pos += stride
		}
	}
}

//revive:disable-next-line:argument-limit
func writeStereo32(out []byte, mixU, mixV []int32, chanIdx, numChan, numSamples int,
	mixBits, mixRes int32, shiftBuf []uint16, bytesShifted int,
) {
	stride := numChan * 4
	pos := chanIdx * 4
	shift := bytesShifted * 8

	if mixRes != 0 {
		for idx := range numSamples {
			left := mixU[idx] + mixV[idx] - ((mixRes * mixV[idx]) >> mixBits)
			right := left - mixV[idx]

			if bytesShifted != 0 {
				left = (left << shift) | int32(shiftBuf[idx*2+0])
				right = (right << shift) | int32(shiftBuf[idx*2+1])
			}

			binary.LittleEndian.PutUint32(out[pos:], uint32(left))
			binary.LittleEndian.PutUint32(out[pos+4:], uint32(right))
			pos += stride
		}
	} else {
		for idx := range numSamples {
			left := mixU[idx]
			right := mixV[idx]

			if bytesShifted != 0 {
				left = (left << shift) | int32(shiftBuf[idx*2+0])
				right = (right << shift) | int32(shiftBuf[idx*2+1])
			}

			binary.LittleEndian.PutUint32(out[pos:], uint32(left))
			binary.LittleEndian.PutUint32(out[pos+4:], uint32(right))
			pos += stride
		}
	}
}

// --- Mono output (single channel) ---

func writeMono16(out []byte, mixU []int32, chanIdx, numChan, numSamples int) {
	stride := numChan * 2
	pos := chanIdx * 2

	for idx := range numSamples {
		binary.LittleEndian.PutUint16(out[pos:], uint16(int16(mixU[idx])))
		pos += stride
	}
}

func writeMono20(out []byte, mixU []int32, chanIdx, numChan, numSamples int) {
	stride := numChan * 3
	pos := chanIdx * 3

	for idx := range numSamples {
		val := mixU[idx] << 4
		dst := out[pos : pos+3 : pos+3]
		dst[0] = byte(val)
		dst[1] = byte(val >> 8)
		dst[2] = byte(val >> 16)
		pos += stride
	}
}

func writeMono24(out []byte, mixU []int32, chanIdx, numChan, numSamples int, shiftBuf []uint16, bytesShifted int) {
	stride := numChan * 3
	pos := chanIdx * 3
	shift := bytesShifted * 8

	for idx := range numSamples {
		val := mixU[idx]
		if bytesShifted != 0 {
			val = (val << shift) | int32(shiftBuf[idx])
		}

		dst := out[pos : pos+3 : pos+3]
		dst[0] = byte(val)
		dst[1] = byte(val >> 8)
		dst[2] = byte(val >> 16)
		pos += stride
	}
}

func writeMono32(out []byte, mixU []int32, chanIdx, numChan, numSamples int, shiftBuf []uint16, bytesShifted int) {
	stride := numChan * 4
	pos := chanIdx * 4
	shift := bytesShifted * 8

	for idx := range numSamples {
		val := mixU[idx]
		if bytesShifted != 0 {
			val = (val << shift) | int32(shiftBuf[idx])
		}

		binary.LittleEndian.PutUint32(out[pos:], uint32(val))
		pos += stride
	}
}
