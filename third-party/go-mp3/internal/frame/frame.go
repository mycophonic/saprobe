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

package frame

import (
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/mycophonic/saprobe/third-party/go-mp3/internal/bits"
	"github.com/mycophonic/saprobe/third-party/go-mp3/internal/consts"
	"github.com/mycophonic/saprobe/third-party/go-mp3/internal/frameheader"
	"github.com/mycophonic/saprobe/third-party/go-mp3/internal/imdct"
	"github.com/mycophonic/saprobe/third-party/go-mp3/internal/maindata"
	"github.com/mycophonic/saprobe/third-party/go-mp3/internal/sideinfo"
)

var (
	powtab34 = make([]float64, 8207)
	pretab   = []float64{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 3, 3, 3, 2, 0}
)

func init() {
	for i := range powtab34 {
		powtab34[i] = math.Pow(float64(i), 4.0/3.0)
	}
}

// Frame represents a decoded MP3 audio frame.
type Frame struct {
	header   frameheader.FrameHeader
	sideInfo *sideinfo.SideInfo
	mainData *maindata.MainData

	mainDataBits *bits.Bits
	store        [2][32][18]float32
	vVec         [2][1024]float32
}

// FullReader is an interface for reading complete byte slices.
type FullReader interface {
	ReadFull([]byte) (int, error)
}

func readCRC(source FullReader) error {
	buf := make([]byte, 2)
	if n, err := source.ReadFull(buf); n < 2 {
		if errors.Is(err, io.EOF) {
			return &consts.UnexpectedEOF{At: "readCRC"}
		}

		return fmt.Errorf("mp3: error at readCRC: %w", err)
	}

	return nil
}

// Read reads and parses a complete MP3 frame from the source.
func Read(source FullReader, position int64, prev *Frame) (frame *Frame, startPosition int64, err error) {
	header, pos, err := frameheader.Read(source, position)
	if err != nil {
		return nil, 0, err
	}

	if header.ProtectionBit() == 0 {
		if err := readCRC(source); err != nil {
			return nil, 0, err
		}
	}

	// MPEG 2.5, non-Layer3, and free bitrate are already rejected by
	// frameheader.IsValid(), so the sync scanner skips past them.

	sideInfo, err := sideinfo.Read(source, header)
	if err != nil {
		return nil, 0, err
	}

	// If there's not enough main data in the bit reservoir,
	// signal to calling function so that decoding isn't done!
	// Get main data (scalefactors and Huffman coded frequency data)
	var prevM *bits.Bits
	if prev != nil {
		prevM = prev.mainDataBits
	}

	mainDataResult, mdb, err := maindata.Read(source, prevM, header, sideInfo)
	if err != nil {
		return nil, 0, err
	}

	newFrame := &Frame{
		header:       header,
		sideInfo:     sideInfo,
		mainData:     mainDataResult,
		mainDataBits: mdb,
	}
	if prev != nil {
		newFrame.store = prev.store
		newFrame.vVec = prev.vVec
	}

	return newFrame, pos, nil
}

// SamplingFrequency returns the sampling frequency of this frame in Hz.
func (f *Frame) SamplingFrequency() (int, error) {
	return f.header.SamplingFrequencyValue()
}

// Decode decodes the frame and returns the raw PCM audio data.
func (f *Frame) Decode() []byte {
	out := make([]byte, f.header.BytesPerFrame())

	nch := f.header.NumberOfChannels()
	for gr := 0; gr < f.header.Granules(); gr++ {
		for ch := range nch {
			f.requantize(gr, ch)
			f.reorder(gr, ch)
		}

		f.stereo(gr)

		for ch := range nch {
			f.antialias(gr, ch)
			f.hybridSynthesis(gr, ch)
			f.frequencyInversion(gr, ch)
			f.subbandSynthesis(gr, ch, out[consts.SamplesPerGr*4*gr:])
		}
	}

	return out
}

func (f *Frame) requantizeProcessLong(granule, channel, isPos, sfb int) {
	sfMult := 0.5
	if f.sideInfo.ScalefacScale[granule][channel] != 0 {
		sfMult = 1.0
	}

	pfXPt := float64(f.sideInfo.Preflag[granule][channel]) * pretab[sfb]
	idx := -(sfMult * (float64(f.mainData.ScalefacL[granule][channel][sfb]) + pfXPt)) +
		0.25*(float64(f.sideInfo.GlobalGain[granule][channel])-210)
	tmp1 := math.Pow(2.0, idx)

	tmp2 := 0.0
	if f.mainData.Is[granule][channel][isPos] < 0.0 {
		tmp2 = -powtab34[int(-f.mainData.Is[granule][channel][isPos])]
	} else {
		tmp2 = powtab34[int(f.mainData.Is[granule][channel][isPos])]
	}

	f.mainData.Is[granule][channel][isPos] = float32(tmp1 * tmp2)
}

func (f *Frame) requantizeProcessShort(granule, channel, isPos, sfb, win int) {
	sfMult := 0.5
	if f.sideInfo.ScalefacScale[granule][channel] != 0 {
		sfMult = 1.0
	}

	idx := -(sfMult * float64(f.mainData.ScalefacS[granule][channel][sfb][win])) +
		0.25*(float64(f.sideInfo.GlobalGain[granule][channel])-210.0-
			8.0*float64(f.sideInfo.SubblockGain[granule][channel][win]))
	tmp1 := math.Pow(2.0, idx)

	tmp2 := 0.0
	if f.mainData.Is[granule][channel][isPos] < 0 {
		tmp2 = -powtab34[int(-f.mainData.Is[granule][channel][isPos])]
	} else {
		tmp2 = powtab34[int(f.mainData.Is[granule][channel][isPos])]
	}

	f.mainData.Is[granule][channel][isPos] = float32(tmp1 * tmp2)
}

func getSfBandIndicesArray(header *frameheader.FrameHeader) ([]int, []int) {
	sfreq := header.SamplingFrequency() // Setup sampling frequency index
	lsf := header.LowSamplingFrequency()
	sfBandIndicesShort := consts.SfBandIndices[lsf][sfreq][consts.SfBandIndicesShort]
	sfBandIndicesLong := consts.SfBandIndices[lsf][sfreq][consts.SfBandIndicesLong]

	return sfBandIndicesLong, sfBandIndicesShort
}

func (f *Frame) requantize(granule, channel int) {
	sfBandIndicesLong, sfBandIndicesShort := getSfBandIndicesArray(&f.header)
	// Determine type of block to process
	if f.sideInfo.WinSwitchFlag[granule][channel] == 1 && f.sideInfo.BlockType[granule][channel] == 2 { // Short blocks
		// Check if the first two subbands
		// (=2*18 samples = 8 long or 3 short sfb's) uses long blocks
		if f.sideInfo.MixedBlockFlag[granule][channel] != 0 { // 2 longbl. sb  first
			// First process the 2 long block subbands at the start
			sfb := 0

			nextSfb := sfBandIndicesLong[sfb+1]
			for i := range 36 {
				if i == nextSfb {
					sfb++
					nextSfb = sfBandIndicesLong[sfb+1]
				}

				f.requantizeProcessLong(granule, channel, i, sfb)
			}
			// And next the remaining,non-zero,bands which uses short blocks
			sfb = 3
			nextSfb = sfBandIndicesShort[sfb+1] * 3
			winLen := sfBandIndicesShort[sfb+1] -
				sfBandIndicesShort[sfb]

			for i := 36; i < int(f.sideInfo.Count1[granule][channel]); /* i++ done below! */ {
				// Check if we're into the next scalefac band
				if i == nextSfb {
					sfb++
					nextSfb = sfBandIndicesShort[sfb+1] * 3
					winLen = sfBandIndicesShort[sfb+1] -
						sfBandIndicesShort[sfb]
				}

				for win := range 3 {
					for range winLen {
						f.requantizeProcessShort(granule, channel, i, sfb, win)
						i++
					}
				}
			}
		} else { // Only short blocks
			sfb := 0
			nextSfb := sfBandIndicesShort[sfb+1] * 3
			winLen := sfBandIndicesShort[sfb+1] -
				sfBandIndicesShort[sfb]

			for i := 0; i < f.sideInfo.Count1[granule][channel]; /* i++ done below! */ {
				// Check if we're into the next scalefac band
				if i == nextSfb {
					sfb++
					nextSfb = sfBandIndicesShort[sfb+1] * 3
					winLen = sfBandIndicesShort[sfb+1] -
						sfBandIndicesShort[sfb]
				}

				for win := range 3 {
					for range winLen {
						f.requantizeProcessShort(granule, channel, i, sfb, win)
						i++
					}
				}
			}
		}
	} else { // Only long blocks
		sfb := 0

		nextSfb := sfBandIndicesLong[sfb+1]
		for idx := range f.sideInfo.Count1[granule][channel] {
			if idx == nextSfb {
				sfb++
				nextSfb = sfBandIndicesLong[sfb+1]
			}

			f.requantizeProcessLong(granule, channel, idx, sfb)
		}
	}
}

func (f *Frame) reorder(granule, channel int) {
	reordered := make([]float32, consts.SamplesPerGr)

	_, sfBandIndicesShort := getSfBandIndicesArray(&f.header)

	// Only reorder short blocks
	if (f.sideInfo.WinSwitchFlag[granule][channel] == 1) &&
		(f.sideInfo.BlockType[granule][channel] == 2) { // Short blocks
		// Check if the first two subbands
		// (=2*18 samples = 8 long or 3 short sfb's) uses long blocks
		sfb := 0
		// 2 longbl. sb  first
		if f.sideInfo.MixedBlockFlag[granule][channel] != 0 {
			sfb = 3
		}

		nextSfb := sfBandIndicesShort[sfb+1] * 3
		winLen := sfBandIndicesShort[sfb+1] - sfBandIndicesShort[sfb]

		i := 36
		if sfb == 0 {
			i = 0
		}

		for i < consts.SamplesPerGr {
			// Check if we're into the next scalefac band
			if i == nextSfb {
				// Copy reordered data back to the original vector
				j := 3 * sfBandIndicesShort[sfb]
				copy(f.mainData.Is[granule][channel][j:j+3*winLen], reordered[0:3*winLen])
				// Check if this band is above the rzero region,if so we're done
				if i >= f.sideInfo.Count1[granule][channel] {
					return
				}

				sfb++
				nextSfb = sfBandIndicesShort[sfb+1] * 3
				winLen = sfBandIndicesShort[sfb+1] - sfBandIndicesShort[sfb]
			}

			for win := range 3 { // Do the actual reordering
				for j := range winLen {
					reordered[j*3+win] = f.mainData.Is[granule][channel][i]
					i++
				}
			}
		}
		// Copy reordered data of last band back to original vector
		j := 3 * sfBandIndicesShort[12]
		copy(f.mainData.Is[granule][channel][j:j+3*winLen], reordered[0:3*winLen])
	}
}

var isRatios = []float32{0.000000, 0.267949, 0.577350, 1.000000, 1.732051, 3.732051}

func (f *Frame) stereoProcessIntensityLong(granule, sfb int) {
	isRatioL := float32(0)
	isRatioR := float32(0)
	// Check that((isPos[sfb]=scalefac) < 7) => no intensity stereo
	if isPos := f.mainData.ScalefacL[granule][0][sfb]; isPos < 7 {
		sfBandIndicesLong, _ := getSfBandIndicesArray(&f.header)
		sfbStart := sfBandIndicesLong[sfb]
		sfbStop := sfBandIndicesLong[sfb+1]

		if isPos == 6 { // tan((6*PI)/12 = PI/2) needs special treatment!
			isRatioL = 1.0
			isRatioR = 0.0
		} else {
			isRatioL = isRatios[isPos] / (1.0 + isRatios[isPos])
			isRatioR = 1.0 / (1.0 + isRatios[isPos])
		}
		// Now decode all samples in this scale factor band
		for i := sfbStart; i < sfbStop; i++ {
			f.mainData.Is[granule][0][i] *= isRatioL
			f.mainData.Is[granule][1][i] *= isRatioR
		}
	}
}

func (f *Frame) stereoProcessIntensityShort(granule, sfb int) {
	isRatioL := float32(0)
	isRatioR := float32(0)
	_, sfBandIndicesShort := getSfBandIndicesArray(&f.header)
	// The window length
	winLen := sfBandIndicesShort[sfb+1] - sfBandIndicesShort[sfb]
	// The three windows within the band has different scalefactors
	for win := range 3 {
		// Check that((isPos[sfb]=scalefac) < 7) => no intensity stereo
		isPos := f.mainData.ScalefacS[granule][0][sfb][win]
		if isPos < 7 {
			sfbStart := sfBandIndicesShort[sfb]*3 + winLen*win
			sfbStop := sfbStart + winLen

			if isPos == 6 { // tan((6*PI)/12 = PI/2) needs special treatment!
				isRatioL = 1.0
				isRatioR = 0.0
			} else {
				isRatioL = isRatios[isPos] / (1.0 + isRatios[isPos])
				isRatioR = 1.0 / (1.0 + isRatios[isPos])
			}
			// Now decode all samples in this scale factor band
			for i := sfbStart; i < sfbStop; i++ {
				// https://github.com/technosaurus/PDMP3/issues/3
				f.mainData.Is[granule][0][i] *= isRatioL
				f.mainData.Is[granule][1][i] *= isRatioR
			}
		}
	}
}

func (f *Frame) stereo(granule int) {
	if f.header.UseMSStereo() {
		// Determine how many frequency lines to transform
		i := 1
		if f.sideInfo.Count1[granule][0] > f.sideInfo.Count1[granule][1] {
			i = 0
		}

		maxPos := int(f.sideInfo.Count1[granule][i])
		// Do the actual processing
		const invSqrt2 = math.Sqrt2 / 2
		for i := range maxPos {
			left := (f.mainData.Is[granule][0][i] + f.mainData.Is[granule][1][i]) * invSqrt2
			right := (f.mainData.Is[granule][0][i] - f.mainData.Is[granule][1][i]) * invSqrt2
			f.mainData.Is[granule][0][i] = left
			f.mainData.Is[granule][1][i] = right
		}
	}

	if f.header.UseIntensityStereo() {
		sfBandIndicesLong, sfBandIndicesShort := getSfBandIndicesArray(&f.header)
		// First band that is intensity stereo encoded is first band scale factor
		// band on or above count1 frequency line. N.B.: Intensity stereo coding is
		// only done for higher subbands, but logic is here for lower subbands.
		// Determine type of block to process
		if (f.sideInfo.WinSwitchFlag[granule][0] == 1) &&
			(f.sideInfo.BlockType[granule][0] == 2) { // Short blocks
			// Check if the first two subbands
			// (=2*18 samples = 8 long or 3 short sfb's) uses long blocks
			if f.sideInfo.MixedBlockFlag[granule][0] != 0 { // 2 longbl. sb  first
				for sfb := range 8 { // First process 8 sfb's at start
					// Is this scale factor band above count1 for the right channel?
					if sfBandIndicesLong[sfb] >= f.sideInfo.Count1[granule][1] {
						f.stereoProcessIntensityLong(granule, sfb)
					}
				}
				// And next the remaining bands which uses short blocks
				for sfb := 3; sfb < 12; sfb++ {
					// Is this scale factor band above count1 for the right channel?
					if sfBandIndicesShort[sfb]*3 >= f.sideInfo.Count1[granule][1] {
						f.stereoProcessIntensityShort(granule, sfb)
					}
				}
			} else { // Only short blocks
				for sfb := range 12 {
					// Is this scale factor band above count1 for the right channel?
					if sfBandIndicesShort[sfb]*3 >= f.sideInfo.Count1[granule][1] {
						f.stereoProcessIntensityShort(granule, sfb)
					}
				}
			}
		} else { // Only long blocks
			for sfb := range 21 {
				// Is this scale factor band above count1 for the right channel?
				if sfBandIndicesLong[sfb] >= f.sideInfo.Count1[granule][1] {
					f.stereoProcessIntensityLong(granule, sfb)
				}
			}
		}
	}
}

var (
	antialiasCs = []float32{0.857493, 0.881742, 0.949629, 0.983315, 0.995518, 0.999161, 0.999899, 0.999993}
	antialiasCa = []float32{-0.514496, -0.471732, -0.313377, -0.181913, -0.094574, -0.040966, -0.014199, -0.003700}
)

func (f *Frame) antialias(granule, channel int) {
	// No antialiasing is done for short blocks
	if (f.sideInfo.WinSwitchFlag[granule][channel] == 1) &&
		(f.sideInfo.BlockType[granule][channel] == 2) &&
		(f.sideInfo.MixedBlockFlag[granule][channel]) == 0 {
		return
	}
	// Setup the limit for how many subbands to transform
	sblim := 32
	if (f.sideInfo.WinSwitchFlag[granule][channel] == 1) &&
		(f.sideInfo.BlockType[granule][channel] == 2) &&
		(f.sideInfo.MixedBlockFlag[granule][channel] == 1) {
		sblim = 2
	}
	// Do the actual antialiasing
	for sb := 1; sb < sblim; sb++ {
		for i := range 8 {
			li := 18*sb - 1 - i
			ui := 18*sb + i
			lb := f.mainData.Is[granule][channel][li]*antialiasCs[i] - f.mainData.Is[granule][channel][ui]*antialiasCa[i]
			ub := f.mainData.Is[granule][channel][ui]*antialiasCs[i] + f.mainData.Is[granule][channel][li]*antialiasCa[i]
			f.mainData.Is[granule][channel][li] = lb
			f.mainData.Is[granule][channel][ui] = ub
		}
	}
}

func (f *Frame) hybridSynthesis(granule, channel int) {
	// Loop through all 32 subbands
	for subband := range 32 {
		// Determine blocktype for this subband
		bt := int(f.sideInfo.BlockType[granule][channel])
		if (f.sideInfo.WinSwitchFlag[granule][channel] == 1) &&
			(f.sideInfo.MixedBlockFlag[granule][channel] == 1) && (subband < 2) {
			bt = 0
		}
		// Do the inverse modified DCT and windowing
		in := make([]float32, 18)
		for i := range in {
			in[i] = f.mainData.Is[granule][channel][subband*18+i]
		}

		rawout := imdct.Win(in, bt)
		// Overlapp add with stored vector into main_data vector
		for i := range 18 {
			f.mainData.Is[granule][channel][subband*18+i] = rawout[i] + f.store[channel][subband][i]
			f.store[channel][subband][i] = rawout[i+18]
		}
	}
}

func (f *Frame) frequencyInversion(granule, channel int) {
	for sb := 1; sb < 32; sb += 2 {
		for i := 1; i < 18; i += 2 {
			f.mainData.Is[granule][channel][sb*18+i] = -f.mainData.Is[granule][channel][sb*18+i]
		}
	}
}

var synthNWin = [64][32]float32{}

func init() {
	for i := range 64 {
		for j := range 32 {
			synthNWin[i][j] = float32(math.Cos(float64((16+i)*(2*j+1)) * (math.Pi / 64.0)))
		}
	}
}

var synthDtbl = [512]float32{
	0.000000000, -0.000015259, -0.000015259, -0.000015259,
	-0.000015259, -0.000015259, -0.000015259, -0.000030518,
	-0.000030518, -0.000030518, -0.000030518, -0.000045776,
	-0.000045776, -0.000061035, -0.000061035, -0.000076294,
	-0.000076294, -0.000091553, -0.000106812, -0.000106812,
	-0.000122070, -0.000137329, -0.000152588, -0.000167847,
	-0.000198364, -0.000213623, -0.000244141, -0.000259399,
	-0.000289917, -0.000320435, -0.000366211, -0.000396729,
	-0.000442505, -0.000473022, -0.000534058, -0.000579834,
	-0.000625610, -0.000686646, -0.000747681, -0.000808716,
	-0.000885010, -0.000961304, -0.001037598, -0.001113892,
	-0.001205444, -0.001296997, -0.001388550, -0.001480103,
	-0.001586914, -0.001693726, -0.001785278, -0.001907349,
	-0.002014160, -0.002120972, -0.002243042, -0.002349854,
	-0.002456665, -0.002578735, -0.002685547, -0.002792358,
	-0.002899170, -0.002990723, -0.003082275, -0.003173828,
	0.003250122, 0.003326416, 0.003387451, 0.003433228,
	0.003463745, 0.003479004, 0.003479004, 0.003463745,
	0.003417969, 0.003372192, 0.003280640, 0.003173828,
	0.003051758, 0.002883911, 0.002700806, 0.002487183,
	0.002227783, 0.001937866, 0.001617432, 0.001266479,
	0.000869751, 0.000442505, -0.000030518, -0.000549316,
	-0.001098633, -0.001693726, -0.002334595, -0.003005981,
	-0.003723145, -0.004486084, -0.005294800, -0.006118774,
	-0.007003784, -0.007919312, -0.008865356, -0.009841919,
	-0.010848999, -0.011886597, -0.012939453, -0.014022827,
	-0.015121460, -0.016235352, -0.017349243, -0.018463135,
	-0.019577026, -0.020690918, -0.021789551, -0.022857666,
	-0.023910522, -0.024932861, -0.025909424, -0.026840210,
	-0.027725220, -0.028533936, -0.029281616, -0.029937744,
	-0.030532837, -0.031005859, -0.031387329, -0.031661987,
	-0.031814575, -0.031845093, -0.031738281, -0.031478882,
	0.031082153, 0.030517578, 0.029785156, 0.028884888,
	0.027801514, 0.026535034, 0.025085449, 0.023422241,
	0.021575928, 0.019531250, 0.017257690, 0.014801025,
	0.012115479, 0.009231567, 0.006134033, 0.002822876,
	-0.000686646, -0.004394531, -0.008316040, -0.012420654,
	-0.016708374, -0.021179199, -0.025817871, -0.030609131,
	-0.035552979, -0.040634155, -0.045837402, -0.051132202,
	-0.056533813, -0.061996460, -0.067520142, -0.073059082,
	-0.078628540, -0.084182739, -0.089706421, -0.095169067,
	-0.100540161, -0.105819702, -0.110946655, -0.115921021,
	-0.120697021, -0.125259399, -0.129562378, -0.133590698,
	-0.137298584, -0.140670776, -0.143676758, -0.146255493,
	-0.148422241, -0.150115967, -0.151306152, -0.151962280,
	-0.152069092, -0.151596069, -0.150497437, -0.148773193,
	-0.146362305, -0.143264771, -0.139450073, -0.134887695,
	-0.129577637, -0.123474121, -0.116577148, -0.108856201,
	0.100311279, 0.090927124, 0.080688477, 0.069595337,
	0.057617188, 0.044784546, 0.031082153, 0.016510010,
	0.001068115, -0.015228271, -0.032379150, -0.050354004,
	-0.069168091, -0.088775635, -0.109161377, -0.130310059,
	-0.152206421, -0.174789429, -0.198059082, -0.221984863,
	-0.246505737, -0.271591187, -0.297210693, -0.323318481,
	-0.349868774, -0.376800537, -0.404083252, -0.431655884,
	-0.459472656, -0.487472534, -0.515609741, -0.543823242,
	-0.572036743, -0.600219727, -0.628295898, -0.656219482,
	-0.683914185, -0.711318970, -0.738372803, -0.765029907,
	-0.791213989, -0.816864014, -0.841949463, -0.866363525,
	-0.890090942, -0.913055420, -0.935195923, -0.956481934,
	-0.976852417, -0.996246338, -1.014617920, -1.031936646,
	-1.048156738, -1.063217163, -1.077117920, -1.089782715,
	-1.101211548, -1.111373901, -1.120223999, -1.127746582,
	-1.133926392, -1.138763428, -1.142211914, -1.144287109,
	1.144989014, 1.144287109, 1.142211914, 1.138763428,
	1.133926392, 1.127746582, 1.120223999, 1.111373901,
	1.101211548, 1.089782715, 1.077117920, 1.063217163,
	1.048156738, 1.031936646, 1.014617920, 0.996246338,
	0.976852417, 0.956481934, 0.935195923, 0.913055420,
	0.890090942, 0.866363525, 0.841949463, 0.816864014,
	0.791213989, 0.765029907, 0.738372803, 0.711318970,
	0.683914185, 0.656219482, 0.628295898, 0.600219727,
	0.572036743, 0.543823242, 0.515609741, 0.487472534,
	0.459472656, 0.431655884, 0.404083252, 0.376800537,
	0.349868774, 0.323318481, 0.297210693, 0.271591187,
	0.246505737, 0.221984863, 0.198059082, 0.174789429,
	0.152206421, 0.130310059, 0.109161377, 0.088775635,
	0.069168091, 0.050354004, 0.032379150, 0.015228271,
	-0.001068115, -0.016510010, -0.031082153, -0.044784546,
	-0.057617188, -0.069595337, -0.080688477, -0.090927124,
	0.100311279, 0.108856201, 0.116577148, 0.123474121,
	0.129577637, 0.134887695, 0.139450073, 0.143264771,
	0.146362305, 0.148773193, 0.150497437, 0.151596069,
	0.152069092, 0.151962280, 0.151306152, 0.150115967,
	0.148422241, 0.146255493, 0.143676758, 0.140670776,
	0.137298584, 0.133590698, 0.129562378, 0.125259399,
	0.120697021, 0.115921021, 0.110946655, 0.105819702,
	0.100540161, 0.095169067, 0.089706421, 0.084182739,
	0.078628540, 0.073059082, 0.067520142, 0.061996460,
	0.056533813, 0.051132202, 0.045837402, 0.040634155,
	0.035552979, 0.030609131, 0.025817871, 0.021179199,
	0.016708374, 0.012420654, 0.008316040, 0.004394531,
	0.000686646, -0.002822876, -0.006134033, -0.009231567,
	-0.012115479, -0.014801025, -0.017257690, -0.019531250,
	-0.021575928, -0.023422241, -0.025085449, -0.026535034,
	-0.027801514, -0.028884888, -0.029785156, -0.030517578,
	0.031082153, 0.031478882, 0.031738281, 0.031845093,
	0.031814575, 0.031661987, 0.031387329, 0.031005859,
	0.030532837, 0.029937744, 0.029281616, 0.028533936,
	0.027725220, 0.026840210, 0.025909424, 0.024932861,
	0.023910522, 0.022857666, 0.021789551, 0.020690918,
	0.019577026, 0.018463135, 0.017349243, 0.016235352,
	0.015121460, 0.014022827, 0.012939453, 0.011886597,
	0.010848999, 0.009841919, 0.008865356, 0.007919312,
	0.007003784, 0.006118774, 0.005294800, 0.004486084,
	0.003723145, 0.003005981, 0.002334595, 0.001693726,
	0.001098633, 0.000549316, 0.000030518, -0.000442505,
	-0.000869751, -0.001266479, -0.001617432, -0.001937866,
	-0.002227783, -0.002487183, -0.002700806, -0.002883911,
	-0.003051758, -0.003173828, -0.003280640, -0.003372192,
	-0.003417969, -0.003463745, -0.003479004, -0.003479004,
	-0.003463745, -0.003433228, -0.003387451, -0.003326416,
	0.003250122, 0.003173828, 0.003082275, 0.002990723,
	0.002899170, 0.002792358, 0.002685547, 0.002578735,
	0.002456665, 0.002349854, 0.002243042, 0.002120972,
	0.002014160, 0.001907349, 0.001785278, 0.001693726,
	0.001586914, 0.001480103, 0.001388550, 0.001296997,
	0.001205444, 0.001113892, 0.001037598, 0.000961304,
	0.000885010, 0.000808716, 0.000747681, 0.000686646,
	0.000625610, 0.000579834, 0.000534058, 0.000473022,
	0.000442505, 0.000396729, 0.000366211, 0.000320435,
	0.000289917, 0.000259399, 0.000244141, 0.000213623,
	0.000198364, 0.000167847, 0.000152588, 0.000137329,
	0.000122070, 0.000106812, 0.000106812, 0.000091553,
	0.000076294, 0.000076294, 0.000061035, 0.000061035,
	0.000045776, 0.000045776, 0.000030518, 0.000030518,
	0.000030518, 0.000030518, 0.000015259, 0.000015259,
	0.000015259, 0.000015259, 0.000015259, 0.000015259,
}

func (f *Frame) subbandSynthesis(granule, channel int, out []byte) {
	uVec := make([]float32, 512)
	sVec := make([]float32, 32)

	nch := f.header.NumberOfChannels()
	// Setup the n_win windowing vector and the vVec intermediate vector
	for ss := range 18 { // Loop through 18 samples in 32 subbands
		copy(f.vVec[channel][64:1024], f.vVec[channel][0:1024-64])

		d := f.mainData.Is[granule][channel]
		for i := range 32 { // Copy next 32 time samples to a temp vector
			sVec[i] = d[i*18+ss]
		}

		for i := range 64 { // Matrix multiply input with n_win[][] matrix
			sum := float32(0)
			for j := range 32 {
				sum += synthNWin[i][j] * sVec[j]
			}

			f.vVec[channel][i] = sum
		}

		v := f.vVec[channel]
		for i := 0; i < 512; i += 64 { // Build the U vector
			copy(uVec[i:i+32], v[(i<<1):(i<<1)+32])
			copy(uVec[i+32:i+64], v[(i<<1)+96:(i<<1)+128])
		}

		for i := range 512 { // Window by uVec[i] with synthDtbl[i]
			uVec[i] *= synthDtbl[i]
		}

		for sampleIdx := range 32 { // Calc 32 samples,store in outdata vector
			sum := float32(0)
			for j := 0; j < 512; j += 32 {
				sum += uVec[j+sampleIdx]
			}
			// sum now contains time sample 32*ss+sampleIdx. Convert to 16-bit signed int
			samp := int(sum * 32767)
			if samp > 32767 {
				samp = 32767
			} else if samp < -32767 {
				samp = -32767
			}

			s := int16(samp)

			idx := 4 * (32*ss + sampleIdx)
			if nch == 1 {
				// We always run in stereo mode and duplicate channels here for mono.
				out[idx] = byte(s)
				out[idx+1] = byte(s >> 8)
				out[idx+2] = byte(s)
				out[idx+3] = byte(s >> 8)

				continue
			}

			if channel == 0 {
				out[idx] = byte(s)
				out[idx+1] = byte(s >> 8)
			} else {
				out[idx+2] = byte(s)
				out[idx+3] = byte(s >> 8)
			}
		}
	}
}
