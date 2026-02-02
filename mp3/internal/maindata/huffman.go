package maindata

import (
	"fmt"

	"github.com/mycophonic/saprobe/mp3/internal/bits"
	"github.com/mycophonic/saprobe/mp3/internal/consts"
	"github.com/mycophonic/saprobe/mp3/internal/frameheader"
	"github.com/mycophonic/saprobe/mp3/internal/huffman"
	"github.com/mycophonic/saprobe/mp3/internal/sideinfo"
)

//nolint:revive,varnamelen // magic numbers and short names are MPEG spec constants
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

		mainData.Count1[granule][channel] = 0

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

	// Read big_values pairs: each Huffman word produces two frequency lines.
	is := &mainData.Is[granule][channel]
	bigValuesEnd := sideInfo.BigValues[granule][channel] * 2

	for isPos := 0; isPos < bigValuesEnd; isPos += 2 {
		var tableNum int

		switch {
		case isPos < region1Start:
			tableNum = sideInfo.TableSelect[granule][channel][0]
		case isPos < region2Start:
			tableNum = sideInfo.TableSelect[granule][channel][1]
		default:
			tableNum = sideInfo.TableSelect[granule][channel][2]
		}

		freqLineX, freqLineY := huffman.DecodePair(&bitCache, tableNum)
		is[isPos] = float32(freqLineX)
		is[isPos+1] = float32(freqLineY)
	}

	// Read count1 quadruples: each Huffman word produces four frequency lines.
	tableNum := sideInfo.Count1TableSelect[granule][channel] + 32

	isPos := bigValuesEnd
	for isPos <= consts.SamplesPerGr-4 && bitCache.BitPos() <= bitPosEnd {
		v, w, x, y := huffman.DecodeQuad(&bitCache, tableNum)
		is[isPos] = float32(v)
		is[isPos+1] = float32(w)
		is[isPos+2] = float32(x)
		is[isPos+3] = float32(y)
		isPos += 4
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

	// Zero out the rzero region.
	for i := isPos; i < consts.SamplesPerGr; i++ {
		is[i] = 0.0
	}
	// Set the bitpos to point to the next part to read
	bitStream.SetBitPos(bitPosEnd + 1)

	return nil
}
