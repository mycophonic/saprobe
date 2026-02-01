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
	"errors"
	"fmt"
	"io"

	"github.com/mycophonic/saprobe/third-party/go-mp3/internal/bits"
	"github.com/mycophonic/saprobe/third-party/go-mp3/internal/consts"
	"github.com/mycophonic/saprobe/third-party/go-mp3/internal/frameheader"
	"github.com/mycophonic/saprobe/third-party/go-mp3/internal/sideinfo"
)

// FullReader is an interface for reading complete byte slices.
type FullReader interface {
	ReadFull(buf []byte) (int, error)
}

// MainData contains the decoded MPEG Layer 3 main data including scale factors and frequency lines.
type MainData struct {
	ScalefacL [2][2][22]int      // 0-4 bits
	ScalefacS [2][2][13][3]int   // 0-4 bits
	Is        [2][2][576]float32 // Huffman coded freq. lines
}

var scalefacSizesMpeg1 = [16][2]int{ //nolint:gochecknoglobals // immutable lookup table
	{0, 0},
	{0, 1},
	{0, 2},
	{0, 3},
	{3, 0},
	{1, 1},
	{1, 2},
	{1, 3},
	{2, 1},
	{2, 2},
	{2, 3},
	{3, 1},
	{3, 2},
	{3, 3},
	{4, 2},
	{4, 3},
}

var scalefacSizesMpeg2 = [3][6][4]int{ //nolint:gochecknoglobals // immutable lookup table
	{
		{6, 5, 5, 5},
		{6, 5, 7, 3},
		{11, 10, 0, 0},
		{7, 7, 7, 0},
		{6, 6, 6, 3},
		{8, 8, 5, 0},
	},
	{
		{9, 9, 9, 9},
		{9, 9, 12, 6},
		{18, 18, 0, 0},
		{12, 12, 12, 0},
		{12, 9, 9, 6},
		{15, 12, 9, 0},
	},
	{
		{6, 9, 9, 9},
		{6, 9, 12, 6},
		{15, 18, 0, 0},
		{6, 15, 12, 0},
		{6, 12, 9, 6},
		{6, 18, 9, 0},
	},
}

var nSlen2 = initSlen() //nolint:gochecknoglobals // immutable lookup table (MPEG 2.0 slen for 'normal' mode)

func initSlen() (nSlen2 [512]int) {
	for i := range 4 {
		for j := range 3 {
			n := j + i*3
			nSlen2[n+500] = i | (j << 3) | (2 << 12) | (1 << 15)
		}
	}

	for i := range 5 {
		for j := range 5 {
			for k := range 4 {
				for l := range 4 {
					n := l + k*4 + j*16 + i*80
					nSlen2[n] = i | (j << 3) | (k << 6) | (l << 9) | (0 << 12)
				}
			}
		}
	}

	for i := range 5 {
		for j := range 5 {
			for k := range 4 {
				n := k + j*4 + i*20
				nSlen2[n+400] = i | (j << 3) | (k << 6) | (1 << 12)
			}
		}
	}

	return nSlen2
}

// Read reads and parses the main data from an MP3 frame.
func Read(
	source FullReader,
	prev *bits.Reader,
	header frameheader.FrameHeader,
	sideInfo *sideinfo.SideInfo,
) (*MainData, *bits.Reader, error) {
	nch := header.NumberOfChannels()
	// Calculate header audio data size
	framesize, err := header.FrameSize()
	if err != nil {
		return nil, nil, err
	}

	if framesize > 2000 {
		return nil, nil, fmt.Errorf("mp3: framesize = %d", framesize)
	}

	sideInfoSize := header.SideInfoSize()

	// Main data size is the rest of the frame,including ancillary data
	mainDataSize := framesize - sideInfoSize - 4 // sync+header
	// CRC is 2 bytes
	if header.ProtectionBit() == 0 {
		mainDataSize -= 2
	}
	// Assemble main data buffer with data from this frame and the previous
	// two frames. main_data_begin indicates how many bytes from previous
	// frames that should be used. This buffer is later accessed by the
	// Bits function in the same way as the side info is.
	bitStream, err := read(source, prev, mainDataSize, sideInfo.MainDataBegin)
	if err != nil {
		// This could be due to not enough data in reservoir
		return nil, nil, err
	}

	if header.LowSamplingFrequency() == 1 {
		return getScaleFactorsMpeg2(bitStream, header, sideInfo)
	}

	return getScaleFactorsMpeg1(nch, bitStream, header, sideInfo)
}

func getScaleFactorsMpeg2(
	bitStream *bits.Reader,
	header frameheader.FrameHeader,
	sideInfo *sideinfo.SideInfo,
) (*MainData, *bits.Reader, error) {
	nch := header.NumberOfChannels()

	mainData := &MainData{}

	for channel := range nch {
		part2Start := bitStream.BitPos()
		numbits := 0
		slen := nSlen2[sideInfo.ScalefacCompress[0][channel]]
		sideInfo.Preflag[0][channel] = (slen >> 15) & 0x1

		blockTypeIdx := 0
		if sideInfo.BlockType[0][channel] == 2 {
			blockTypeIdx++
			if sideInfo.MixedBlockFlag[0][channel] != 0 {
				blockTypeIdx++
			}
		}

		var scaleFactors []int

		d := (slen >> 12) & 0x7

		for idx := range 4 {
			num := slen & 0x7
			slen >>= 3

			if num > 0 {
				for range scalefacSizesMpeg2[blockTypeIdx][d][idx] {
					scaleFactors = append(scaleFactors, bitStream.ReadBits(num))
				}

				numbits += scalefacSizesMpeg2[blockTypeIdx][d][idx] * num
			} else {
				for range scalefacSizesMpeg2[blockTypeIdx][d][idx] {
					scaleFactors = append(scaleFactors, 0)
				}
			}
		}

		blockTypeIdx = (blockTypeIdx << 1) + 1
		for range blockTypeIdx {
			scaleFactors = append(scaleFactors, 0)
		}

		if len(scaleFactors) == 22 {
			for idx := range 22 {
				mainData.ScalefacL[0][channel][idx] = scaleFactors[idx]
			}
		} else {
			for x := range 13 {
				for idx := range 3 {
					mainData.ScalefacS[0][channel][x][idx] = scaleFactors[(x*3)+idx]
				}
			}
		}

		// Read Huffman coded data. Skip stuffing bits.
		if err := readHuffman(bitStream, header, sideInfo, mainData, part2Start, 0, channel); err != nil {
			return nil, nil, err
		}
	}
	// The ancillary data is stored here,but we ignore it.
	return mainData, bitStream, nil
}

func getScaleFactorsMpeg1(
	nch int,
	bitStream *bits.Reader,
	header frameheader.FrameHeader,
	sideInfo *sideinfo.SideInfo,
) (*MainData, *bits.Reader, error) {
	mainData := &MainData{}

	for granule := range 2 {
		for channel := range nch {
			part2Start := bitStream.BitPos()
			// Number of bits in the bitstream for the bands
			slen1 := scalefacSizesMpeg1[sideInfo.ScalefacCompress[granule][channel]][0]

			slen2 := scalefacSizesMpeg1[sideInfo.ScalefacCompress[granule][channel]][1]
			if sideInfo.WinSwitchFlag[granule][channel] == 1 && sideInfo.BlockType[granule][channel] == 2 {
				if sideInfo.MixedBlockFlag[granule][channel] != 0 {
					for sfb := range 8 {
						mainData.ScalefacL[granule][channel][sfb] = bitStream.ReadBits(slen1)
					}

					for sfb := 3; sfb < 12; sfb++ {
						// slen1 for band 3-5,slen2 for 6-11
						nbits := slen2
						if sfb < 6 {
							nbits = slen1
						}

						for win := range 3 {
							mainData.ScalefacS[granule][channel][sfb][win] = bitStream.ReadBits(nbits)
						}
					}
				} else {
					for sfb := range 12 {
						// slen1 for band 3-5,slen2 for 6-11
						nbits := slen2
						if sfb < 6 {
							nbits = slen1
						}

						for win := range 3 {
							mainData.ScalefacS[granule][channel][sfb][win] = bitStream.ReadBits(nbits)
						}
					}
				}
			} else {
				// Scale factor bands 0-5
				if sideInfo.Scfsi[channel][0] == 0 || granule == 0 {
					for sfb := range 6 {
						mainData.ScalefacL[granule][channel][sfb] = bitStream.ReadBits(slen1)
					}
				} else if sideInfo.Scfsi[channel][0] == 1 && granule == 1 {
					// Copy scalefactors from granule 0 to granule 1
					// TODO: This is not listed on the spec.
					for sfb := range 6 {
						mainData.ScalefacL[1][channel][sfb] = mainData.ScalefacL[0][channel][sfb]
					}
				}
				// Scale factor bands 6-10
				if sideInfo.Scfsi[channel][1] == 0 || granule == 0 {
					for sfb := 6; sfb < 11; sfb++ {
						mainData.ScalefacL[granule][channel][sfb] = bitStream.ReadBits(slen1)
					}
				} else if sideInfo.Scfsi[channel][1] == 1 && granule == 1 {
					// Copy scalefactors from granule 0 to granule 1
					for sfb := 6; sfb < 11; sfb++ {
						mainData.ScalefacL[1][channel][sfb] = mainData.ScalefacL[0][channel][sfb]
					}
				}
				// Scale factor bands 11-15
				if sideInfo.Scfsi[channel][2] == 0 || granule == 0 {
					for sfb := 11; sfb < 16; sfb++ {
						mainData.ScalefacL[granule][channel][sfb] = bitStream.ReadBits(slen2)
					}
				} else if sideInfo.Scfsi[channel][2] == 1 && granule == 1 {
					// Copy scalefactors from granule 0 to granule 1
					for sfb := 11; sfb < 16; sfb++ {
						mainData.ScalefacL[1][channel][sfb] = mainData.ScalefacL[0][channel][sfb]
					}
				}
				// Scale factor bands 16-20
				if sideInfo.Scfsi[channel][3] == 0 || granule == 0 {
					for sfb := 16; sfb < 21; sfb++ {
						mainData.ScalefacL[granule][channel][sfb] = bitStream.ReadBits(slen2)
					}
				} else if sideInfo.Scfsi[channel][3] == 1 && granule == 1 {
					// Copy scalefactors from granule 0 to granule 1
					for sfb := 16; sfb < 21; sfb++ {
						mainData.ScalefacL[1][channel][sfb] = mainData.ScalefacL[0][channel][sfb]
					}
				}
			}
			// Read Huffman coded data. Skip stuffing bits.
			if err := readHuffman(bitStream, header, sideInfo, mainData, part2Start, granule, channel); err != nil {
				return nil, nil, err
			}
		}
	}
	// The ancillary data is stored here,but we ignore it.
	return mainData, bitStream, nil
}

func read(source FullReader, prev *bits.Reader, size, offset int) (*bits.Reader, error) {
	if size > 1500 {
		return nil, fmt.Errorf("mp3: size = %d", size)
	}
	// Check that there's data available from previous frames if needed
	if prev != nil && offset > prev.Len() {
		// No, there is not, so we skip decoding this frame, but we have to
		// read the main_data bits from the bitstream in case they are needed
		// for decoding the next frame.
		buf := make([]byte, size)
		if n, err := source.ReadFull(buf); n < size {
			if errors.Is(err, io.EOF) {
				return nil, &consts.UnexpectedEOF{At: "maindata.Read (1)"}
			}

			return nil, err
		}
		// TODO: Define a special error and enable to continue the next frame.
		return bits.NewReader(append(prev.Bytes(), buf...)), nil
	}
	// Copy data from previous frames
	vec := []byte{}
	if prev != nil {
		vec = prev.Tail(offset)
	}
	// Read the main_data from file
	buf := make([]byte, size)
	if n, err := source.ReadFull(buf); n < size {
		if errors.Is(err, io.EOF) {
			return nil, &consts.UnexpectedEOF{At: "maindata.Read (2)"}
		}

		return nil, err
	}

	return bits.NewReader(append(vec, buf...)), nil
}
