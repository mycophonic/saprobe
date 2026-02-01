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

package sideinfo

import (
	"errors"
	"fmt"
	"io"

	"github.com/mycophonic/saprobe/third-party/go-mp3/internal/bits"
	"github.com/mycophonic/saprobe/third-party/go-mp3/internal/consts"
	"github.com/mycophonic/saprobe/third-party/go-mp3/internal/frameheader"
)

// FullReader is an interface for reading complete byte slices.
type FullReader interface {
	ReadFull(buf []byte) (int, error)
}

// A SideInfo is MPEG1 Layer 3 Side Information.
// [2][2] means [gr][ch].
type SideInfo struct {
	MainDataBegin    int       // 9 bits
	PrivateBits      int       // 3 bits in mono, 5 in stereo
	Scfsi            [2][4]int // 1 bit
	Part2And3Length  [2][2]int // 12 bits
	BigValues        [2][2]int // 9 bits
	GlobalGain       [2][2]int // 8 bits
	ScalefacCompress [2][2]int // 4 bits
	WinSwitchFlag    [2][2]int // 1 bit

	BlockType      [2][2]int    // 2 bits
	MixedBlockFlag [2][2]int    // 1 bit
	TableSelect    [2][2][3]int // 5 bits
	SubblockGain   [2][2][3]int // 3 bits

	Region0Count [2][2]int // 4 bits
	Region1Count [2][2]int // 3 bits

	Preflag           [2][2]int // 1 bit
	ScalefacScale     [2][2]int // 1 bit
	Count1TableSelect [2][2]int // 1 bit
	Count1            [2][2]int // Not in file, calc by huffman decoder
}

var sideInfoBitsToRead = [2][4]int{
	{ // MPEG 1
		9, 5, 3, 4,
	},
	{ // MPEG 2
		8, 1, 2, 9,
	},
}

// Read reads and parses the side information from an MP3 frame.
func Read(source FullReader, header frameheader.FrameHeader) (*SideInfo, error) {
	nch := header.NumberOfChannels()

	framesize, err := header.FrameSize()
	if err != nil {
		return nil, err
	}

	if framesize > 2000 {
		return nil, fmt.Errorf("mp3: framesize = %d\n", framesize)
	}

	sideInfoSize := header.SideInfoSize()

	// Read sideinfo from bitstream into buffer used by Bits()
	buf := make([]byte, sideInfoSize)

	n, err := source.ReadFull(buf)
	if n < sideInfoSize {
		if errors.Is(err, io.EOF) {
			return nil, &consts.UnexpectedEOF{At: "sideinfo.Read"}
		}

		return nil, fmt.Errorf("mp3: couldn't read sideinfo %d bytes: %w", sideInfoSize, err)
	}

	bitReader := bits.New(buf)

	mpeg1Frame := header.LowSamplingFrequency() == 0
	bitsToRead := sideInfoBitsToRead[header.LowSamplingFrequency()]

	// Parse audio data
	// Pointer to where we should start reading main data
	sideInfo := &SideInfo{}
	sideInfo.MainDataBegin = bitReader.Bits(bitsToRead[0])
	// Get private bits. Not used for anything.
	if header.Mode() == consts.ModeSingleChannel {
		sideInfo.PrivateBits = bitReader.Bits(bitsToRead[1])
	} else {
		sideInfo.PrivateBits = bitReader.Bits(bitsToRead[2])
	}

	if mpeg1Frame {
		// Get scale factor selection information
		for channel := range nch {
			for scfsiBand := range 4 {
				sideInfo.Scfsi[channel][scfsiBand] = bitReader.Bits(1)
			}
		}
	}
	// Get the rest of the side information
	for gr := range header.Granules() {
		for channel := range nch {
			sideInfo.Part2And3Length[gr][channel] = bitReader.Bits(12)
			sideInfo.BigValues[gr][channel] = bitReader.Bits(9)
			sideInfo.GlobalGain[gr][channel] = bitReader.Bits(8)
			sideInfo.ScalefacCompress[gr][channel] = bitReader.Bits(bitsToRead[3])

			sideInfo.WinSwitchFlag[gr][channel] = bitReader.Bits(1)
			if sideInfo.WinSwitchFlag[gr][channel] == 1 {
				sideInfo.BlockType[gr][channel] = bitReader.Bits(2)

				sideInfo.MixedBlockFlag[gr][channel] = bitReader.Bits(1)
				for region := range 2 {
					sideInfo.TableSelect[gr][channel][region] = bitReader.Bits(5)
				}

				for window := range 3 {
					sideInfo.SubblockGain[gr][channel][window] = bitReader.Bits(3)
				}

				// TODO: This is not listed on the spec. Is this correct??
				if sideInfo.BlockType[gr][channel] == 2 && sideInfo.MixedBlockFlag[gr][channel] == 0 {
					sideInfo.Region0Count[gr][channel] = 8 // Implicit
				} else {
					sideInfo.Region0Count[gr][channel] = 7 // Implicit
				}
				// The standard is wrong on this!!!
				// Implicit
				sideInfo.Region1Count[gr][channel] = 20 - sideInfo.Region0Count[gr][channel]
			} else {
				for region := range 3 {
					sideInfo.TableSelect[gr][channel][region] = bitReader.Bits(5)
				}

				sideInfo.Region0Count[gr][channel] = bitReader.Bits(4)
				sideInfo.Region1Count[gr][channel] = bitReader.Bits(3)

				sideInfo.BlockType[gr][channel] = 0 // Implicit
				if !mpeg1Frame {
					sideInfo.MixedBlockFlag[0][channel] = 0
				}
			}

			if mpeg1Frame {
				sideInfo.Preflag[gr][channel] = bitReader.Bits(1)
			}

			sideInfo.ScalefacScale[gr][channel] = bitReader.Bits(1)
			sideInfo.Count1TableSelect[gr][channel] = bitReader.Bits(1)
		}
	}

	return sideInfo, nil
}
