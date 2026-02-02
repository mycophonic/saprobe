package sideinfo

import (
	"errors"
	"fmt"
	"io"

	"github.com/mycophonic/saprobe/mp3/internal/consts"
	"github.com/mycophonic/saprobe/mp3/internal/frameheader"
)

// A SideInfo is MPEG1 Layer 3 Side Information.
// [2][2] means [gr][ch].
type SideInfo struct {
	MainDataBegin    int       // 9 bits
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
}

// bitReader reads bits MSB-first from a fixed [32]byte buffer.
// Stack-allocated; no heap escapes.
type bitReader struct {
	buf [32]byte
	pos int
}

func (r *bitReader) readBits(count int) int {
	// Load up to 5 bytes into a uint64 accumulator.
	byteIdx := r.pos >> 3
	bitOff := uint(r.pos & 7) //nolint:gosec // pos is always non-negative

	var acc uint64

	for i := range min(32-byteIdx, 5) {
		shift := (7 - i) * 8                           // 56, 48, 40, 32, 24
		acc |= uint64(r.buf[byteIdx+i]) << uint(shift) //nolint:gosec // byteIdx+i validated by min bound
	}

	acc <<= bitOff
	//nolint:gosec // count is 1..12; no overflow risk
	result := int(acc >> uint(64-count))
	r.pos += count

	return result
}

var sideInfoBitsToRead = [2][4]int{ //nolint:gochecknoglobals // immutable lookup table
	{ // MPEG 1
		9, 5, 3, 4,
	},
	{ // MPEG 2
		8, 1, 2, 9,
	},
}

// Read reads and parses the side information from an MP3 frame into dst.
// The caller provides dst (typically embedded in Frame as a value field).
// dst is fully overwritten for active granules/channels.
//
//revive:disable:cognitive-complexity
func Read(source consts.FullReader, header *frameheader.Header, dst *SideInfo) error {
	nch := header.Channels()

	sideInfoSize := header.SideInfoSize()

	// Stack-local buffer and bit reader — zero heap allocations.
	var reader bitReader

	n, err := source.ReadFull(reader.buf[:sideInfoSize])
	if n < sideInfoSize {
		if errors.Is(err, io.EOF) {
			return &consts.UnexpectedEOF{At: "sideinfo.Read"}
		}

		return fmt.Errorf("mp3: couldn't read sideinfo %d bytes: %w", sideInfoSize, err)
	}

	mpeg1Frame := header.LowSamplingFrequency() == 0
	bitsToRead := sideInfoBitsToRead[header.LowSamplingFrequency()]

	// Pointer to where we should start reading main data
	dst.MainDataBegin = reader.readBits(bitsToRead[0])

	// Get private bits. Not used for anything — read to advance position, discard value.
	if header.Mode() == consts.ModeSingleChannel {
		_ = reader.readBits(bitsToRead[1])
	} else {
		_ = reader.readBits(bitsToRead[2])
	}

	if mpeg1Frame {
		// Get scale factor selection information
		for channel := range nch {
			for scfsiBand := range 4 {
				dst.Scfsi[channel][scfsiBand] = reader.readBits(1)
			}
		}
	}

	// Get the rest of the side information
	for granule := range header.Granules() {
		for channel := range nch {
			//revive:disable:add-constant
			dst.Part2And3Length[granule][channel] = reader.readBits(12)
			dst.BigValues[granule][channel] = reader.readBits(9)
			dst.GlobalGain[granule][channel] = reader.readBits(8)
			//nolint:gosec // bitsToRead is [4]int, index 3 always valid
			dst.ScalefacCompress[granule][channel] = reader.readBits(bitsToRead[3])

			dst.WinSwitchFlag[granule][channel] = reader.readBits(1)
			if dst.WinSwitchFlag[granule][channel] == 1 {
				dst.BlockType[granule][channel] = reader.readBits(2)

				dst.MixedBlockFlag[granule][channel] = reader.readBits(1)
				for region := range 2 {
					dst.TableSelect[granule][channel][region] = reader.readBits(5)
				}

				for window := range 3 {
					dst.SubblockGain[granule][channel][window] = reader.readBits(3)
				}

				// Implicit region counts for short/mixed blocks (not transmitted in bitstream).
				// Pure short blocks (type 2, no mixed): region0 spans 8 scale factor bands.
				// Mixed blocks or other switched types: region0 spans 7 scale factor bands.
				if dst.BlockType[granule][channel] == 2 && dst.MixedBlockFlag[granule][channel] == 0 {
					dst.Region0Count[granule][channel] = 8
				} else {
					dst.Region0Count[granule][channel] = 7
				}
				// Region1 fills the remaining bands up to 20 (total = region0 + region1).
				dst.Region1Count[granule][channel] = 20 - dst.Region0Count[granule][channel]
			} else {
				for region := range 3 {
					dst.TableSelect[granule][channel][region] = reader.readBits(5)
				}

				dst.Region0Count[granule][channel] = reader.readBits(4)
				dst.Region1Count[granule][channel] = reader.readBits(3)

				dst.BlockType[granule][channel] = 0 // Implicit
				if !mpeg1Frame {
					dst.MixedBlockFlag[0][channel] = 0
				}
			}

			if mpeg1Frame {
				dst.Preflag[granule][channel] = reader.readBits(1)
			}

			dst.ScalefacScale[granule][channel] = reader.readBits(1)
			dst.Count1TableSelect[granule][channel] = reader.readBits(1)
		}
	}

	return nil
}
