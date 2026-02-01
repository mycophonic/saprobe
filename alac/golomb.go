package alac

import (
	"encoding/binary"
	"math/bits"
)

// Adaptive Golomb-Rice entropy decoder.
// Ported from ag_dec.c and aglib.h.

const (
	qbShift       = 9
	quantBits     = 1 << qbShift // 512
	mmulShift     = 2
	mdenShift     = qbShift - mmulShift - 1 // 6
	moff          = 1 << (mdenShift - 2)    // 16
	bitoff        = 24
	maxPrefix16   = 9
	maxPrefix32   = 9
	maxDatatype16 = 16
	nMaxMeanClamp = 0xffff
	nMeanClampVal = 0xffff
	maxZeroRun    = 65535 // Maximum zero-run length before resetting zmode.
)

type agParams struct {
	mb, mb0 uint32
	pb      uint32
	kb      uint32
	wb      uint32
	qb      uint32
	fw, sw  uint32
	maxrun  uint32
}

func setAGParams(params *agParams, meanBase, partBound, kBase, frameWin, sampleWin, maxrun uint32) {
	params.mb = meanBase
	params.mb0 = meanBase
	params.pb = partBound
	params.kb = kBase
	params.wb = (1 << kBase) - 1
	params.qb = quantBits - partBound
	params.fw = frameWin
	params.sw = sampleWin
	params.maxrun = maxrun
}

// lead returns the number of leading zeros in a 32-bit value.
// Equivalent to the Apple lead() function.
func lead(m int32) int32 {
	return int32(bits.LeadingZeros32(uint32(m)))
}

// lg3a computes floor(log2(x+3)).
func lg3a(x int32) int32 {
	return 31 - lead(x+3) //revive:disable-line:add-constant
}

// read32bit reads 4 bytes big-endian from a byte slice at the given offset.
// binary.BigEndian.Uint32 is intrinsified by the Go compiler as a single
// load + byte-swap instruction, replacing 4 individual byte loads.
func read32bit(buf []byte, offset int) uint32 {
	return binary.BigEndian.Uint32(buf[offset:])
}

// getStreamBits reads up to 32 bits from an arbitrary bit position in a byte buffer.
func getStreamBits(input []byte, bitOffset, numBits uint32) uint32 {
	byteOffset := bitOffset / 8
	load1 := read32bit(input, int(byteOffset))

	if numBits+(bitOffset&7) > 32 {
		// Need bits from a 5th byte.
		result := load1 << (bitOffset & 7)
		load2 := uint32(input[byteOffset+4])
		load2shift := 8 - (numBits + (bitOffset & 7) - 32)
		load2 >>= load2shift
		result >>= 32 - numBits
		result |= load2

		return result
	}

	result := load1 >> (32 - numBits - (bitOffset & 7))
	if numBits < 32 {
		result &= (1 << numBits) - 1
	}

	return result
}

// dynGet decodes one Golomb-coded value (16-bit variant used for zero-run counts).
// Returns (decoded value, updated bit position).
func dynGet(input []byte, bitPos, golombM, golombK uint32) (result, newBitPos uint32) {
	tempBits := bitPos

	streamLong := read32bit(input, int(tempBits>>3))
	streamLong <<= tempBits & 7

	// Count leading ones (= leading zeros in complement).
	pre := uint32(lead(int32(^streamLong)))

	if pre >= maxPrefix16 {
		pre = maxPrefix16
		tempBits += pre
		streamLong <<= pre
		result = streamLong >> (32 - maxDatatype16)
		tempBits += maxDatatype16

		return result, tempBits
	}

	tempBits += pre + 1
	streamLong <<= pre + 1
	val := streamLong >> (32 - golombK)
	tempBits += golombK

	if val < 2 {
		result = pre * golombM
		tempBits--
	} else {
		result = pre*golombM + val - 1
	}

	return result, tempBits
}

// dynGet32Bit decodes one Golomb-coded value (32-bit variant used for sample residuals).
// Returns (decoded value, updated bit position).
func dynGet32Bit(input []byte, bitPos, golombM uint32, golombK, maxBits int32) (result, newBitPos uint32) {
	tempBits := bitPos

	streamLong := read32bit(input, int(tempBits>>3))
	streamLong <<= tempBits & 7

	// Count leading ones.
	result = uint32(lead(int32(^streamLong)))

	if result >= maxPrefix32 {
		result = getStreamBits(input, tempBits+maxPrefix32, uint32(maxBits))
		tempBits += maxPrefix32 + uint32(maxBits)
	} else {
		tempBits += result + 1

		if golombK != 1 {
			streamLong <<= result + 1
			v := streamLong >> (32 - uint32(golombK))

			if v >= 2 {
				result = result*golombM + v - 1
				tempBits += uint32(golombK)
			} else {
				result *= golombM
				tempBits += uint32(golombK) - 1
			}
		}
	}

	return result, tempBits
}

// dynDecomp performs adaptive Golomb-Rice entropy decoding of a sample block.
// Writes decoded prediction residuals into predCoefs.
func dynDecomp(params *agParams, bitBuf *bitBuffer, predCoefs []int32, numSamples, maxSize int) error {
	input := bitBuf.buf[bitBuf.pos:]
	startPos := bitBuf.bitIdx
	maxPos := uint32(bitBuf.size) * 8
	bitPos := startPos

	meanAccum := params.mb0
	zmode := int32(0)
	count := 0

	pbLocal := params.pb
	kbLocal := params.kb
	wbLocal := params.wb

	var residual uint32

	for count < numSamples {
		if bitPos >= maxPos {
			return errBitstreamOverrun
		}

		m := meanAccum >> qbShift
		k := min(lg3a(int32(m)), int32(kbLocal))

		m = (1 << uint32(k)) - 1

		residual, bitPos = dynGet32Bit(input, bitPos, m, k, int32(maxSize))

		// Decode sign from LSB.
		ndecode := residual + uint32(zmode)
		multiplier := -int32(ndecode & 1)
		multiplier |= 1
		del := int32((ndecode+1)>>1) * multiplier

		predCoefs[count] = del
		count++

		// Update mean.
		meanAccum = pbLocal*(residual+uint32(zmode)) + meanAccum - ((pbLocal * meanAccum) >> qbShift)
		if residual > nMaxMeanClamp {
			meanAccum = nMeanClampVal
		}

		zmode = 0

		// Check for zero run mode.
		if (meanAccum<<mmulShift) < quantBits && count < numSamples {
			zmode = 1

			k32 := max(lead(int32(meanAccum))-bitoff+int32((meanAccum+moff)>>mdenShift), 0)

			mz := ((uint32(1) << uint32(k32)) - 1) & wbLocal

			residual, bitPos = dynGet(input, bitPos, mz, uint32(k32))

			if count+int(residual) > numSamples {
				return errSampleOverrun
			}

			for range residual {
				predCoefs[count] = 0
				count++
			}

			if residual >= maxZeroRun {
				zmode = 0
			}

			meanAccum = 0
		}
	}

	bitsConsumed := bitPos - startPos
	bitBuf.advance(bitsConsumed)

	return nil
}
