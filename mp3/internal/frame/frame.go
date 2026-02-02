package frame

import (
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/mycophonic/primordium/simd"

	"github.com/mycophonic/saprobe/mp3/internal/consts"
	"github.com/mycophonic/saprobe/mp3/internal/frameheader"
	"github.com/mycophonic/saprobe/mp3/internal/imdct"
	"github.com/mycophonic/saprobe/mp3/internal/maindata"
	"github.com/mycophonic/saprobe/mp3/internal/sideinfo"
)

// Frame is a persistent MP3 frame decoder. Allocate once with [NewFrame] and
// reuse across successive [Frame.Read] / [Frame.Decode] cycles. All inter-frame
// state (bit reservoir, IMDCT overlap-add, polyphase synthesis FIFO) is carried
// forward automatically.
type Frame struct {
	header    *frameheader.Header
	sideInfo  sideinfo.SideInfo
	mainData  maindata.MainData
	reservoir maindata.Reservoir

	// IMDCT overlap-add store, indexed [channel][subband][sample].
	store [2][32][18]float32

	// Polyphase synthesis FIFO — doubled circular buffer eliminates the
	// 960-element shift that the naive implementation performs per call.
	// Indexed [channel][offset]. Logical size is 1024; the second half
	// (indices 1024..2047) mirrors the first so that contiguous reads
	// spanning the wrap boundary need no modular arithmetic.
	vVec [2][2048]float32
	vOff [2]int // current write offset per channel (decremented by 64 each call)
}

// NewFrame allocates a ready-to-use Frame. The returned Frame should be reused
// for all frames in a stream.
func NewFrame() *Frame {
	return &Frame{}
}

// Read parses the next MP3 frame from source starting at the given byte position.
// It returns the byte position where the frame header was found.
func (f *Frame) Read(source consts.FullReader, position int64) (int64, error) {
	header, pos, err := frameheader.Read(source, position)
	if err != nil {
		return 0, fmt.Errorf("mp3: frame.Read header: %w", err)
	}

	if header.Protected() {
		var crc [2]byte

		if n, err := source.ReadFull(crc[:]); n < 2 {
			if errors.Is(err, io.EOF) {
				return 0, &consts.UnexpectedEOF{At: "frame.Read: CRC"}
			}

			return 0, fmt.Errorf("mp3: frame.Read CRC: %w", err)
		}
	}

	f.header = &header

	if err := sideinfo.Read(source, f.header, &f.sideInfo); err != nil {
		return 0, fmt.Errorf("mp3: frame.Read sideinfo: %w", err)
	}

	if err := maindata.Read(source, f.header, &f.sideInfo, &f.mainData, &f.reservoir); err != nil {
		return 0, fmt.Errorf("mp3: frame.Read maindata: %w", err)
	}

	return pos, nil
}

// SampleRateHz returns the sample rate in Hz (e.g. 44100).
func (f *Frame) SampleRateHz() int { return f.header.SampleRateHz() }

// BytesPerFrame returns the number of decoded PCM bytes this frame produces.
func (f *Frame) BytesPerFrame() int { return f.header.BytesPerFrame() }

// Header returns the parsed frame header.
func (f *Frame) Header() *frameheader.Header { return f.header }

// Decode runs the full MP3 decode pipeline and writes interleaved 16-bit
// little-endian stereo PCM into dst. Returns the number of bytes written
// (always [Frame.BytesPerFrame]). The caller must ensure dst is large enough.
//
//nolint:varnamelen // gr, ch, nch mirror MPEG spec terminology
func (f *Frame) Decode(dst []byte) int {
	nch := f.header.Channels()
	sfreq := f.header.SamplingFrequency()
	lsf := f.header.LowSamplingFrequency()
	sfBandLong := consts.SfBandIndices[lsf][sfreq][consts.SfBandIndicesLong]
	sfBandShort := consts.SfBandIndices[lsf][sfreq][consts.SfBandIndicesShort]

	for gr := range f.header.Granules() {
		for ch := range nch {
			f.requantize(gr, ch, sfBandLong, sfBandShort)
			f.reorder(gr, ch, sfBandShort)
		}

		f.stereo(gr, sfBandLong, sfBandShort)

		for ch := range nch {
			f.antialias(gr, ch)
			f.hybridSynthesis(gr, ch)
			f.frequencyInversion(gr, ch)
			f.subbandSynthesis(gr, ch, dst[consts.SamplesPerGr*4*gr:])
		}
	}

	return f.header.BytesPerFrame()
}

// requantSample applies the gain to one Huffman-decoded frequency line.
// The compiler inlines this.
func requantSample(is, gain float32) float32 {
	if is < 0 {
		return -powtab34[int(-is)] * gain
	}

	return powtab34[int(is)] * gain
}

// requantize dequantizes the Huffman-decoded frequency lines for one
// granule/channel using scale factors and global gain.
//
//nolint:revive,varnamelen // cognitive complexity and short names mirror the MPEG spec
func (f *Frame) requantize(gr, ch int, sfBandLong, sfBandShort []int) {
	si := &f.sideInfo
	md := &f.mainData

	// sfMult2 is 4*sfMult where sfMult ∈ {0.5, 1.0}. Using the integer
	// multiplier lets us index pow2quarter directly without floating-point.
	sfMult2 := 2
	if si.ScalefacScale[gr][ch] != 0 {
		sfMult2 = 4
	}

	globalBase := si.GlobalGain[gr][ch] - 210
	preflag := si.Preflag[gr][ch]

	if si.WinSwitchFlag[gr][ch] == 1 && si.BlockType[gr][ch] == 2 {
		if si.MixedBlockFlag[gr][ch] != 0 {
			// Mixed blocks: long blocks for subbands 0-1 (36 samples), then short.
			sfb := 0
			nextSfb := sfBandLong[sfb+1]
			k := -sfMult2*(md.ScalefacL[gr][ch][0]+preflag*pretab[0]) + globalBase
			gain := pow2quarter[k+pow2quarterBias]

			for i := range 36 {
				if i == nextSfb {
					sfb++
					nextSfb = sfBandLong[sfb+1]
					k = -sfMult2*(md.ScalefacL[gr][ch][sfb]+preflag*pretab[sfb]) + globalBase
					gain = pow2quarter[k+pow2quarterBias]
				}

				md.Is[gr][ch][i] = requantSample(md.Is[gr][ch][i], gain)
			}

			// Short blocks for remaining samples.
			sfb = 3
			nextShort := sfBandShort[sfb+1] * 3
			winLen := sfBandShort[sfb+1] - sfBandShort[sfb]

			for i := 36; i < md.Count1[gr][ch]; {
				if i == nextShort {
					sfb++
					nextShort = sfBandShort[sfb+1] * 3
					winLen = sfBandShort[sfb+1] - sfBandShort[sfb]
				}

				for win := range 3 {
					k := -sfMult2*md.ScalefacS[gr][ch][sfb][win] +
						globalBase - 8*si.SubblockGain[gr][ch][win]
					gain := pow2quarter[k+pow2quarterBias]

					for range winLen {
						md.Is[gr][ch][i] = requantSample(md.Is[gr][ch][i], gain)
						i++
					}
				}
			}
		} else {
			// Pure short blocks.
			sfb := 0
			nextSfb := sfBandShort[sfb+1] * 3
			winLen := sfBandShort[sfb+1] - sfBandShort[sfb]

			for i := 0; i < md.Count1[gr][ch]; {
				if i == nextSfb {
					sfb++
					nextSfb = sfBandShort[sfb+1] * 3
					winLen = sfBandShort[sfb+1] - sfBandShort[sfb]
				}

				for win := range 3 {
					k := -sfMult2*md.ScalefacS[gr][ch][sfb][win] +
						globalBase - 8*si.SubblockGain[gr][ch][win]
					gain := pow2quarter[k+pow2quarterBias]

					for range winLen {
						md.Is[gr][ch][i] = requantSample(md.Is[gr][ch][i], gain)
						i++
					}
				}
			}
		}
	} else {
		// Long blocks only.
		sfb := 0
		nextSfb := sfBandLong[sfb+1]
		k := -sfMult2*(md.ScalefacL[gr][ch][0]+preflag*pretab[0]) + globalBase
		gain := pow2quarter[k+pow2quarterBias]

		for i := range md.Count1[gr][ch] {
			if i == nextSfb {
				sfb++
				nextSfb = sfBandLong[sfb+1]
				k = -sfMult2*(md.ScalefacL[gr][ch][sfb]+preflag*pretab[sfb]) + globalBase
				gain = pow2quarter[k+pow2quarterBias]
			}

			md.Is[gr][ch][i] = requantSample(md.Is[gr][ch][i], gain)
		}
	}
}

// reorder rearranges short-block frequency lines from sub-band order to
// frequency order. Long blocks pass through unchanged.
//
//nolint:revive,varnamelen,gosec // magic numbers, short names, and bounded slice access are MPEG spec constants
func (f *Frame) reorder(gr, ch int, sfBandShort []int) {
	si := &f.sideInfo
	md := &f.mainData

	if si.WinSwitchFlag[gr][ch] != 1 || si.BlockType[gr][ch] != 2 {
		return
	}

	var reordered [consts.SamplesPerGr]float32

	sfb := 0
	if si.MixedBlockFlag[gr][ch] != 0 {
		sfb = 3
	}

	nextSfb := sfBandShort[sfb+1] * 3
	winLen := sfBandShort[sfb+1] - sfBandShort[sfb]

	i := 36
	if sfb == 0 {
		i = 0
	}

	for i < consts.SamplesPerGr {
		if i == nextSfb {
			j := 3 * sfBandShort[sfb]
			copy(md.Is[gr][ch][j:j+3*winLen], reordered[:3*winLen])

			if i >= md.Count1[gr][ch] {
				return
			}

			sfb++
			nextSfb = sfBandShort[sfb+1] * 3
			winLen = sfBandShort[sfb+1] - sfBandShort[sfb]
		}

		for win := range 3 {
			for j := range winLen {
				reordered[j*3+win] = md.Is[gr][ch][i]
				i++
			}
		}
	}

	j := 3 * sfBandShort[12]
	copy(md.Is[gr][ch][j:j+3*winLen], reordered[:3*winLen])
}

// stereo applies MS stereo and/or intensity stereo processing.
//
//nolint:revive,varnamelen // cognitive complexity and short names mirror the MPEG spec
func (f *Frame) stereo(gr int, sfBandLong, sfBandShort []int) {
	md := &f.mainData

	if f.header.UseMSStereo() {
		// Determine how many frequency lines to transform.
		i := 1
		if md.Count1[gr][0] > md.Count1[gr][1] {
			i = 0
		}

		maxPos := md.Count1[gr][i]

		const invSqrt2 = math.Sqrt2 / 2

		for i := range maxPos {
			left := (md.Is[gr][0][i] + md.Is[gr][1][i]) * invSqrt2
			right := (md.Is[gr][0][i] - md.Is[gr][1][i]) * invSqrt2
			md.Is[gr][0][i] = left
			md.Is[gr][1][i] = right
		}
	}

	if !f.header.UseIntensityStereo() {
		return
	}

	si := &f.sideInfo

	if si.WinSwitchFlag[gr][0] == 1 && si.BlockType[gr][0] == 2 {
		if si.MixedBlockFlag[gr][0] != 0 {
			// Mixed blocks: long sfb's 0-7, then short sfb's 3-11.
			for sfb := range 8 {
				if sfBandLong[sfb] >= md.Count1[gr][1] {
					f.intensityStereoLong(gr, sfb, sfBandLong)
				}
			}

			for sfb := 3; sfb < 12; sfb++ {
				if sfBandShort[sfb]*3 >= md.Count1[gr][1] {
					f.intensityStereoShort(gr, sfb, sfBandShort)
				}
			}
		} else {
			for sfb := range 12 {
				if sfBandShort[sfb]*3 >= md.Count1[gr][1] {
					f.intensityStereoShort(gr, sfb, sfBandShort)
				}
			}
		}
	} else {
		for sfb := range 21 {
			if sfBandLong[sfb] >= md.Count1[gr][1] {
				f.intensityStereoLong(gr, sfb, sfBandLong)
			}
		}
	}
}

//nolint:varnamelen // gr, sfb are MPEG spec terminology
func (f *Frame) intensityStereoLong(gr, sfb int, sfBandLong []int) {
	isPos := f.mainData.ScalefacL[gr][1][sfb]
	if isPos >= 7 {
		return
	}

	ratioL := isRatioL[isPos]
	ratioR := isRatioR[isPos]

	for i := sfBandLong[sfb]; i < sfBandLong[sfb+1]; i++ {
		f.mainData.Is[gr][0][i] *= ratioL
		f.mainData.Is[gr][1][i] *= ratioR
	}
}

//nolint:varnamelen // gr, sfb, win are MPEG spec terminology
func (f *Frame) intensityStereoShort(gr, sfb int, sfBandShort []int) {
	winLen := sfBandShort[sfb+1] - sfBandShort[sfb]

	for win := range 3 {
		isPos := f.mainData.ScalefacS[gr][1][sfb][win]
		if isPos >= 7 {
			continue
		}

		ratioL := isRatioL[isPos]
		ratioR := isRatioR[isPos]
		sfbStart := sfBandShort[sfb]*3 + winLen*win
		sfbStop := sfbStart + winLen

		for i := sfbStart; i < sfbStop; i++ {
			f.mainData.Is[gr][0][i] *= ratioL
			f.mainData.Is[gr][1][i] *= ratioR
		}
	}
}

//nolint:revive,varnamelen // magic numbers and short names are MPEG spec constants
func (f *Frame) antialias(gr, ch int) {
	si := &f.sideInfo

	// No antialiasing for pure short blocks.
	if si.WinSwitchFlag[gr][ch] == 1 &&
		si.BlockType[gr][ch] == 2 &&
		si.MixedBlockFlag[gr][ch] == 0 {
		return
	}

	sblim := 32
	if si.WinSwitchFlag[gr][ch] == 1 &&
		si.BlockType[gr][ch] == 2 &&
		si.MixedBlockFlag[gr][ch] == 1 {
		sblim = 2
	}

	is := &f.mainData.Is[gr][ch]

	for sb := 1; sb < sblim; sb++ {
		for i := range 8 {
			li := 18*sb - 1 - i
			ui := 18*sb + i
			lb := is[li]*csAA[i] - is[ui]*caAA[i]
			ub := is[ui]*csAA[i] + is[li]*caAA[i]
			is[li] = lb
			is[ui] = ub
		}
	}
}

//nolint:revive,varnamelen // magic numbers and short names are MPEG spec constants
func (f *Frame) hybridSynthesis(gr, ch int) {
	var rawout [36]float32

	for sb := range 32 {
		blockType := f.sideInfo.BlockType[gr][ch]
		if f.sideInfo.WinSwitchFlag[gr][ch] == 1 &&
			f.sideInfo.MixedBlockFlag[gr][ch] == 1 && sb < 2 {
			blockType = 0
		}

		off := sb * 18
		freqIn := (*[18]float32)(f.mainData.Is[gr][ch][off : off+18])

		imdct.Win(freqIn, blockType, &rawout)

		for i := range 18 {
			f.mainData.Is[gr][ch][off+i] = rawout[i] + f.store[ch][sb][i]
			f.store[ch][sb][i] = rawout[i+18]
		}
	}
}

//nolint:revive // magic numbers are MPEG spec constants
func (f *Frame) frequencyInversion(gr, ch int) {
	is := &f.mainData.Is[gr][ch]

	for sb := 1; sb < 32; sb += 2 {
		for i := 1; i < 18; i += 2 {
			is[sb*18+i] = -is[sb*18+i]
		}
	}
}

// subbandSynthesis performs the 32-subband polyphase synthesis filterbank,
// producing 576 PCM samples (18 iterations × 32 samples) for one granule/channel.
//
//nolint:revive,varnamelen // MPEG spec constants and short names
func (f *Frame) subbandSynthesis(gr, ch int, out []byte) {
	var sVec [32]float32

	nch := f.header.Channels()
	vv := &f.vVec[ch]

	for ss := range 18 {
		// Advance the circular buffer write position.
		f.vOff[ch] = (f.vOff[ch] - 64) & 0x3FF
		off := f.vOff[ch]

		// Copy 32 time-domain samples from the frequency lines.
		d := &f.mainData.Is[gr][ch]
		for i := range 32 {
			sVec[i] = d[i*18+ss]
		}

		// Matrix multiply with the synthesis cosine window (SIMD-accelerated).
		// Write each result to both halves of the doubled buffer so that
		// reads spanning the logical wrap boundary are contiguous.
		var vOut [64]float32
		simd.MatVecMul64x32(&vOut, &synthNWin, &sVec)
		copy(vv[off:off+64], vOut[:])
		copy(vv[off+1024:off+1024+64], vOut[:])

		// Fused D-window + sum: accumulate 32 PCM samples directly from
		// the circular buffer, avoiding the intermediate 512-element U vector.
		// Each sample sums 16 terms: 8 groups × 2 halves (first 32 + skip 64 + next 32).
		for sampleIdx := range 32 {
			var sum float32

			for k := range 8 {
				j := off + 128*k
				sum += vv[j+sampleIdx] * synthDtbl[k*64+sampleIdx]
				sum += vv[j+96+sampleIdx] * synthDtbl[k*64+32+sampleIdx]
			}

			samp := int(sum * 32767)
			if samp > 32767 {
				samp = 32767
			} else if samp < -32767 {
				samp = -32767
			}

			s := int16(samp) //nolint:gosec // clamped to int16 range above

			idx := 4 * (32*ss + sampleIdx)
			if nch == 1 {
				out[idx] = byte(s)
				out[idx+1] = byte(s >> 8)
				out[idx+2] = byte(s)
				out[idx+3] = byte(s >> 8)

				continue
			}

			if ch == 0 {
				out[idx] = byte(s)
				out[idx+1] = byte(s >> 8)
			} else {
				out[idx+2] = byte(s)
				out[idx+3] = byte(s >> 8)
			}
		}
	}
}
