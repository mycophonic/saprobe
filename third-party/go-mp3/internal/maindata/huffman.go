package maindata

import (
	"fmt"

	"github.com/mycophonic/saprobe/third-party/go-mp3/internal/bits"
	"github.com/mycophonic/saprobe/third-party/go-mp3/internal/consts"
	"github.com/mycophonic/saprobe/third-party/go-mp3/internal/frameheader"
	"github.com/mycophonic/saprobe/third-party/go-mp3/internal/huffman"
	"github.com/mycophonic/saprobe/third-party/go-mp3/internal/sideinfo"
)

//nolint:revive // magic numbers are MPEG spec constants (36=sfb boundary, 572=SamplesPerGr-4, 0.0=zero)
func readHuffman(
	bitStream *bits.Reader,
	header *frameheader.Header,
	sideInfo *sideinfo.SideInfo,
	mainData *MainData,
	part2Start, granule, channel int,
) error {
	// Check that there is any data to decode. If not, zero the array.
	if sideInfo.Part2And3Length[granule][channel] == 0 {
		for i := range consts.SamplesPerGr {
			mainData.Is[granule][channel][i] = 0.0
		}

		return nil
	}

	// Calculate bitPosEnd which is the index of the last bit for this part.
	bitPosEnd := part2Start + sideInfo.Part2And3Length[granule][channel] - 1
	// Determine region boundaries
	var region1Start, region2Start int

	if (sideInfo.WinSwitchFlag[granule][channel] == 1) && (sideInfo.BlockType[granule][channel] == 2) {
		region1Start = 36                  // sfb[9/3]*3=36
		region2Start = consts.SamplesPerGr // No Region2 for short block case.
	} else {
		sfreq := header.SamplingFrequency()
		lsf := header.LowSamplingFrequency()
		sfBandLong := consts.SfBandIndices[lsf][sfreq][consts.SfBandIndicesLong]

		regionIdx := sideInfo.Region0Count[granule][channel] + 1
		if regionIdx < 0 || len(sfBandLong) <= regionIdx {
			//nolint:err113 // runtime value in error message required for debugging
			return fmt.Errorf("mp3: readHuffman failed: invalid index i: %d", regionIdx)
		}

		region1Start = sfBandLong[regionIdx]

		regionEndIdx := sideInfo.Region0Count[granule][channel] + sideInfo.Region1Count[granule][channel] + 2
		if regionEndIdx < 0 || len(sfBandLong) <= regionEndIdx {
			//nolint:err113 // runtime value in error message required for debugging
			return fmt.Errorf("mp3: readHuffman failed: invalid index j: %d", regionEndIdx)
		}

		region2Start = sfBandLong[regionEndIdx]
	}
	// Initialize bit cache for Huffman decoding (amortized across all decode calls).
	bitCache := huffman.NewBitCache(bitStream)

	// Read big_values using tables according to region_x_start
	for isPos := 0; isPos < sideInfo.BigValues[granule][channel]*2; isPos++ {
		// #22
		if isPos >= len(mainData.Is[granule][channel]) {
			//nolint:err113 // runtime value in error message required for debugging
			return fmt.Errorf("mp3: isPos was too big: %d", isPos)
		}

		var tableNum int

		switch {
		case isPos < region1Start:
			tableNum = sideInfo.TableSelect[granule][channel][0]
		case isPos < region2Start:
			tableNum = sideInfo.TableSelect[granule][channel][1]
		default:
			tableNum = sideInfo.TableSelect[granule][channel][2]
		}
		// Get next Huffman coded words
		x, freqLineY := huffman.DecodePair(&bitCache, tableNum)
		// In the big_values area there are two freq lines per Huffman word
		mainData.Is[granule][channel][isPos] = float32(x)
		isPos++
		mainData.Is[granule][channel][isPos] = float32(freqLineY)
	}
	// Read small values until isPos = 576 or we run out of huffman data
	// TODO: Is this comment wrong?
	tableNum := sideInfo.Count1TableSelect[granule][channel] + 32

	isPos := sideInfo.BigValues[granule][channel] * 2
	for isPos <= 572 && bitCache.BitPos() <= bitPosEnd {
		// Get next Huffman coded words
		v, freqLineW, freqLineX, freqLineY := huffman.DecodeQuad(&bitCache, tableNum)

		mainData.Is[granule][channel][isPos] = float32(v)

		isPos++
		if isPos >= consts.SamplesPerGr {
			break
		}

		mainData.Is[granule][channel][isPos] = float32(freqLineW)

		isPos++
		if isPos >= consts.SamplesPerGr {
			break
		}

		mainData.Is[granule][channel][isPos] = float32(freqLineX)

		isPos++
		if isPos >= consts.SamplesPerGr {
			break
		}

		mainData.Is[granule][channel][isPos] = float32(freqLineY)
		isPos++
	}
	// Check that we didn't read past the end of this section
	if bitCache.BitPos() > (bitPosEnd + 1) {
		// Remove last words read
		isPos -= 4
	}

	if isPos < 0 {
		isPos = 0
	}

	// Setup count1 which is the index of the first sample in the rzero reg.
	mainData.Count1[granule][channel] = isPos

	// Zero out the last part if necessary
	for isPos < consts.SamplesPerGr {
		mainData.Is[granule][channel][isPos] = 0.0
		isPos++
	}
	// Set the bitpos to point to the next part to read
	bitStream.SetBitPos(bitPosEnd + 1)

	return nil
}
