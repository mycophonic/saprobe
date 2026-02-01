// Copyright 2017 Hajime Hoshi
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package maindata

import (
	"fmt"

	"github.com/mycophonic/saprobe/third-party/go-mp3/internal/bits"
	"github.com/mycophonic/saprobe/third-party/go-mp3/internal/consts"
	"github.com/mycophonic/saprobe/third-party/go-mp3/internal/frameheader"
	"github.com/mycophonic/saprobe/third-party/go-mp3/internal/huffman"
	"github.com/mycophonic/saprobe/third-party/go-mp3/internal/sideinfo"
)

func readHuffman(
	bitStream *bits.Bits,
	header frameheader.FrameHeader,
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
	region1Start := 0
	region2Start := 0

	if (sideInfo.WinSwitchFlag[granule][channel] == 1) && (sideInfo.BlockType[granule][channel] == 2) {
		region1Start = 36                  // sfb[9/3]*3=36
		region2Start = consts.SamplesPerGr // No Region2 for short block case.
	} else {
		sfreq := header.SamplingFrequency()
		lsf := header.LowSamplingFrequency()
		sfBandLong := consts.SfBandIndices[lsf][sfreq][consts.SfBandIndicesLong]

		regionIdx := sideInfo.Region0Count[granule][channel] + 1
		if regionIdx < 0 || len(sfBandLong) <= regionIdx {
			// TODO: Better error messages (#3)
			return fmt.Errorf("mp3: readHuffman failed: invalid index i: %d", regionIdx)
		}

		region1Start = sfBandLong[regionIdx]

		regionEndIdx := sideInfo.Region0Count[granule][channel] + sideInfo.Region1Count[granule][channel] + 2
		if regionEndIdx < 0 || len(sfBandLong) <= regionEndIdx {
			// TODO: Better error messages (#3)
			return fmt.Errorf("mp3: readHuffman failed: invalid index j: %d", regionEndIdx)
		}

		region2Start = sfBandLong[regionEndIdx]
	}
	// Read big_values using tables according to region_x_start
	for isPos := 0; isPos < sideInfo.BigValues[granule][channel]*2; isPos++ {
		// #22
		if isPos >= len(mainData.Is[granule][channel]) {
			return fmt.Errorf("mp3: isPos was too big: %d", isPos)
		}

		tableNum := 0
		if isPos < region1Start {
			tableNum = sideInfo.TableSelect[granule][channel][0]
		} else if isPos < region2Start {
			tableNum = sideInfo.TableSelect[granule][channel][1]
		} else {
			tableNum = sideInfo.TableSelect[granule][channel][2]
		}
		// Get next Huffman coded words
		x, freqLineY, _, _, err := huffman.Decode(bitStream, tableNum)
		if err != nil {
			return err
		}
		// In the big_values area there are two freq lines per Huffman word
		mainData.Is[granule][channel][isPos] = float32(x)
		isPos++
		mainData.Is[granule][channel][isPos] = float32(freqLineY)
	}
	// Read small values until isPos = 576 or we run out of huffman data
	// TODO: Is this comment wrong?
	tableNum := sideInfo.Count1TableSelect[granule][channel] + 32

	isPos := sideInfo.BigValues[granule][channel] * 2
	for isPos <= 572 && bitStream.BitPos() <= bitPosEnd {
		// Get next Huffman coded words
		freqLineX, freqLineY, v, freqLineW, err := huffman.Decode(bitStream, tableNum)
		if err != nil {
			return err
		}

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
	if bitStream.BitPos() > (bitPosEnd + 1) {
		// Remove last words read
		isPos -= 4
	}

	if isPos < 0 {
		isPos = 0
	}

	// Setup count1 which is the index of the first sample in the rzero reg.
	sideInfo.Count1[granule][channel] = isPos

	// Zero out the last part if necessary
	for isPos < consts.SamplesPerGr {
		mainData.Is[granule][channel][isPos] = 0.0
		isPos++
	}
	// Set the bitpos to point to the next part to read
	bitStream.SetPos(bitPosEnd + 1)

	return nil
}
