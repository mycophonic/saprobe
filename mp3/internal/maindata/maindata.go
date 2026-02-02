package maindata

import (
	"errors"
	"fmt"
	"io"

	"github.com/mycophonic/saprobe/mp3/internal/bits"
	"github.com/mycophonic/saprobe/mp3/internal/consts"
	"github.com/mycophonic/saprobe/mp3/internal/frameheader"
	"github.com/mycophonic/saprobe/mp3/internal/sideinfo"
)

// MainData contains the decoded MPEG Layer 3 main data including scale factors and frequency lines.
type MainData struct {
	ScalefacL [2][2][22]int      // 0-4 bits
	ScalefacS [2][2][13][3]int   // 0-4 bits
	Is        [2][2][576]float32 // Huffman coded freq. lines
	Count1    [2][2]int          // set by Huffman decoder, not parsed from bitstream
}

// Reservoir manages the bit reservoir for main data across frames.
// The MP3 format allows frames to reference main data bytes from previous frames
// via the MainDataBegin pointer in side information.
//
// Max buffer: 511 (MPEG1 MainDataBegin) + 1405 (max main data) = 1916 bytes normal.
// 4096 provides margin for the insufficient-reservoir append path.
type Reservoir struct {
	buf [4096]byte
	len int
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

//nolint:revive // magic numbers are MPEG2 slen lookup table indices from the spec
func initSlen() (nSlen2 [512]int) {
	for i := range 4 {
		for j := range 3 {
			n := j + i*3
			nSlen2[n+500] = i | (j << 3) | (2 << 12) | (1 << 15) //nolint:gosec // max n+500 = 2+3*3+500 = 511
		}
	}

	for i := range 5 {
		for j := range 5 {
			for k := range 4 {
				for l := range 4 {
					n := l + k*4 + j*16 + i*80
					nSlen2[n] = i | (j << 3) | (k << 6) | (l << 9) | (0 << 12) //nolint:gosec // max n = 3+12+64+320 = 399
				}
			}
		}
	}

	for i := range 5 {
		for j := range 5 {
			for k := range 4 {
				n := k + j*4 + i*20
				nSlen2[n+400] = i | (j << 3) | (k << 6) | (1 << 12) //nolint:gosec // max n+400 = 3+16+80+400 = 499
			}
		}
	}

	return nSlen2
}

// Read reads and parses the main data from an MP3 frame into dst.
// The caller provides dst (typically embedded in Frame as a value field)
// and res (the persistent bit reservoir, also embedded in Frame).
func Read(
	source consts.FullReader,
	header *frameheader.Header,
	sideInfo *sideinfo.SideInfo,
	dst *MainData,
	res *Reservoir,
) error {
	nch := header.Channels()

	// Main data size is the frame minus header (4 bytes), side info, and optional CRC (2 bytes).
	mainDataSize := header.FrameSize() - header.SideInfoSize() - 4
	if header.Protected() {
		mainDataSize -= 2
	}

	offset := sideInfo.MainDataBegin

	// Build the bitstream from reservoir tail + current frame data.
	if offset > res.len {
		// Insufficient reservoir — append current data and proceed.
		// The bitstream position won't align with MainDataBegin, but we replicate
		// the original behavior for compatibility.
		if res.len+mainDataSize > len(res.buf) {
			//nolint:err113 // runtime value in error message required for debugging
			return fmt.Errorf("mp3: maindata reservoir overflow: %d + %d > %d", res.len, mainDataSize, len(res.buf))
		}

		count, err := source.ReadFull(res.buf[res.len : res.len+mainDataSize])
		if count < mainDataSize {
			if errors.Is(err, io.EOF) {
				return &consts.UnexpectedEOF{At: "maindata.Read (1)"}
			}

			return fmt.Errorf("mp3: maindata.Read (1): %w", err)
		}

		res.len += mainDataSize
	} else {
		// Normal path: shift reservoir tail to start of buffer, then read current data.
		if offset+mainDataSize > len(res.buf) {
			//nolint:err113 // runtime value in error message required for debugging
			return fmt.Errorf("mp3: maindata reservoir overflow: %d + %d > %d", offset, mainDataSize, len(res.buf))
		}

		tailStart := res.len - offset
		copy(res.buf[:offset], res.buf[tailStart:res.len])

		count, err := source.ReadFull(res.buf[offset : offset+mainDataSize])
		if count < mainDataSize {
			if errors.Is(err, io.EOF) {
				return &consts.UnexpectedEOF{At: "maindata.Read (2)"}
			}

			return fmt.Errorf("mp3: maindata.Read (2): %w", err)
		}

		res.len = offset + mainDataSize
	}

	// Stack-local bit reader over the reservoir buffer — zero heap allocation.
	var reader bits.Reader
	reader.Reset(res.buf[:res.len])

	if header.LowSamplingFrequency() == 1 {
		return getScaleFactorsMpeg2(&reader, header, sideInfo, dst)
	}

	return getScaleFactorsMpeg1(nch, &reader, header, sideInfo, dst)
}

//nolint:revive // cognitive complexity mirrors the MPEG1 spec's branching structure
func getScaleFactorsMpeg1(
	nch int,
	bitStream *bits.Reader,
	header *frameheader.Header,
	sideInfo *sideinfo.SideInfo,
	dst *MainData,
) error {
	for granule := range 2 {
		for channel := range nch {
			part2Start := bitStream.BitPos()
			// Number of bits in the bitstream for the bands
			slen1 := scalefacSizesMpeg1[sideInfo.ScalefacCompress[granule][channel]][0]

			slen2 := scalefacSizesMpeg1[sideInfo.ScalefacCompress[granule][channel]][1]
			if sideInfo.WinSwitchFlag[granule][channel] == 1 && sideInfo.BlockType[granule][channel] == 2 {
				if sideInfo.MixedBlockFlag[granule][channel] != 0 {
					for sfb := range 8 {
						dst.ScalefacL[granule][channel][sfb] = bitStream.ReadBits(slen1)
					}

					for sfb := 3; sfb < 12; sfb++ {
						// slen1 for band 3-5, slen2 for 6-11
						nbits := slen2
						if sfb < 6 {
							nbits = slen1
						}

						for win := range 3 {
							dst.ScalefacS[granule][channel][sfb][win] = bitStream.ReadBits(nbits)
						}
					}
				} else {
					for sfb := range 12 {
						// slen1 for band 3-5, slen2 for 6-11
						nbits := slen2
						if sfb < 6 {
							nbits = slen1
						}

						for win := range 3 {
							dst.ScalefacS[granule][channel][sfb][win] = bitStream.ReadBits(nbits)
						}
					}
				}
			} else {
				// Scale factor bands 0-5
				if sideInfo.Scfsi[channel][0] == 0 || granule == 0 {
					for sfb := range 6 {
						dst.ScalefacL[granule][channel][sfb] = bitStream.ReadBits(slen1)
					}
				} else if sideInfo.Scfsi[channel][0] == 1 && granule == 1 {
					// Copy scalefactors from granule 0 to granule 1 (scfsi reuse, ISO 11172-3 §2.4.2.7).
					for sfb := range 6 {
						dst.ScalefacL[1][channel][sfb] = dst.ScalefacL[0][channel][sfb]
					}
				}
				// Scale factor bands 6-10
				if sideInfo.Scfsi[channel][1] == 0 || granule == 0 {
					for sfb := 6; sfb < 11; sfb++ {
						dst.ScalefacL[granule][channel][sfb] = bitStream.ReadBits(slen1)
					}
				} else if sideInfo.Scfsi[channel][1] == 1 && granule == 1 {
					// Copy scalefactors from granule 0 to granule 1
					for sfb := 6; sfb < 11; sfb++ {
						dst.ScalefacL[1][channel][sfb] = dst.ScalefacL[0][channel][sfb]
					}
				}
				// Scale factor bands 11-15
				if sideInfo.Scfsi[channel][2] == 0 || granule == 0 {
					for sfb := 11; sfb < 16; sfb++ {
						dst.ScalefacL[granule][channel][sfb] = bitStream.ReadBits(slen2)
					}
				} else if sideInfo.Scfsi[channel][2] == 1 && granule == 1 {
					// Copy scalefactors from granule 0 to granule 1
					for sfb := 11; sfb < 16; sfb++ {
						dst.ScalefacL[1][channel][sfb] = dst.ScalefacL[0][channel][sfb]
					}
				}
				// Scale factor bands 16-20
				if sideInfo.Scfsi[channel][3] == 0 || granule == 0 {
					for sfb := 16; sfb < 21; sfb++ {
						dst.ScalefacL[granule][channel][sfb] = bitStream.ReadBits(slen2)
					}
				} else if sideInfo.Scfsi[channel][3] == 1 && granule == 1 {
					// Copy scalefactors from granule 0 to granule 1
					for sfb := 16; sfb < 21; sfb++ {
						dst.ScalefacL[1][channel][sfb] = dst.ScalefacL[0][channel][sfb]
					}
				}
			}
			// Read Huffman coded data. Skip stuffing bits.
			if err := readHuffman(bitStream, header, sideInfo, dst, part2Start, granule, channel); err != nil {
				return err
			}
		}
	}

	return nil
}

//nolint:revive,varnamelen // cognitive complexity and short names mirror the MPEG2 spec
func getScaleFactorsMpeg2(
	bitStream *bits.Reader,
	header *frameheader.Header,
	sideInfo *sideinfo.SideInfo,
	dst *MainData,
) error {
	nch := header.Channels()

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

		d := (slen >> 12) & 0x7

		// Stack-local array replaces dynamic append.
		var sf [45]int

		sfIdx := 0

		for idx := range 4 {
			num := slen & 0x7
			slen >>= 3

			if num > 0 {
				for range scalefacSizesMpeg2[blockTypeIdx][d][idx] {
					sf[sfIdx] = bitStream.ReadBits(num) //nolint:gosec // sfIdx bounded by table sums (max 41)
					sfIdx++
				}

				numbits += scalefacSizesMpeg2[blockTypeIdx][d][idx] * num
			} else {
				for range scalefacSizesMpeg2[blockTypeIdx][d][idx] {
					// sf[sfIdx] is already 0
					sfIdx++
				}
			}
		}

		// Trailing zeros for block type alignment.
		trailingZeros := (blockTypeIdx << 1) + 1
		sfIdx += trailingZeros
		_ = numbits

		if sfIdx == 22 { //nolint:revive // 22 is the MPEG2 long-block scale factor count per spec
			for idx := range 22 {
				//nolint:gosec // idx bounded by range 22; sf is [45]int
				dst.ScalefacL[0][channel][idx] = sf[idx]
			}
		} else {
			for x := range 13 {
				for idx := range 3 {
					//nolint:gosec // max (x*3)+idx = 12*3+2 = 38; sf is [45]int
					dst.ScalefacS[0][channel][x][idx] = sf[(x*3)+idx]
				}
			}
		}

		// Read Huffman coded data. Skip stuffing bits.
		if err := readHuffman(bitStream, header, sideInfo, dst, part2Start, 0, channel); err != nil {
			return err
		}
	}

	return nil
}
