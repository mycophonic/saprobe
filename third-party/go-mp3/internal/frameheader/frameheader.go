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

package frameheader

import (
	"errors"
	"io"

	"github.com/mycophonic/saprobe/third-party/go-mp3/internal/consts"
)

// FrameHeader is an MPEG1 Layer 1-3 frame header.
type FrameHeader uint32

// ID returns this header's ID stored in position 20,19.
func (f FrameHeader) ID() consts.Version {
	return consts.Version((f & 0x00180000) >> 19)
}

// Layer returns the mpeg layer of this frame stored in position 18,17.
func (f FrameHeader) Layer() consts.Layer {
	return consts.Layer((f & 0x00060000) >> 17)
}

// ProtectionBit returns the protection bit stored in position 16.
func (f FrameHeader) ProtectionBit() int {
	return int(f&0x00010000) >> 16
}

// BitrateIndex returns the bitrate index stored in position 15,12.
func (f FrameHeader) BitrateIndex() int {
	return int(f&0x0000f000) >> 12
}

// SamplingFrequency returns the SamplingFrequency in Hz stored in position 11,10.
func (f FrameHeader) SamplingFrequency() consts.SamplingFrequency {
	return consts.SamplingFrequency(int(f&0x00000c00) >> 10)
}

// SamplingFrequencyValue returns the actual sampling frequency in Hz.
func (f FrameHeader) SamplingFrequencyValue() (int, error) {
	switch f.SamplingFrequency() {
	case consts.SamplingFrequency44100:
		return 44100 >> uint(f.LowSamplingFrequency()), nil
	case consts.SamplingFrequency48000:
		return 48000 >> uint(f.LowSamplingFrequency()), nil
	case consts.SamplingFrequency32000:
		return 32000 >> uint(f.LowSamplingFrequency()), nil
	case consts.SamplingFrequencyReserved:
		return 0, errors.New("mp3: frame header has reserved sample frequency")
	}

	return 0, errors.New("mp3: frame header has invalid sample frequency")
}

// PaddingBit returns the padding bit stored in position 9.
func (f FrameHeader) PaddingBit() int {
	return int(f&0x00000200) >> 9
}

// PrivateBit returns the private bit stored in position 8 - this bit may be used to store arbitrary data to be used
// by an application.
func (f FrameHeader) PrivateBit() int {
	return int(f&0x00000100) >> 8
}

// Mode returns the channel mode, stored in position 7,6.
func (f FrameHeader) Mode() consts.Mode {
	return consts.Mode((f & 0x000000c0) >> 6)
}

// modeExtension returns the mode_extension - for use with Joint Stereo - stored in position 4,5.
func (f FrameHeader) modeExtension() int {
	return int(f&0x00000030) >> 4
}

// UseMSStereo returns a boolean value indicating whether the frame uses middle/side stereo.
func (f FrameHeader) UseMSStereo() bool {
	if f.Mode() != consts.ModeJointStereo {
		return false
	}

	return f.modeExtension()&0x2 != 0
}

// UseIntensityStereo returns a boolean value indicating whether the frame uses intensity stereo.
func (f FrameHeader) UseIntensityStereo() bool {
	if f.Mode() != consts.ModeJointStereo {
		return false
	}

	return f.modeExtension()&0x1 != 0
}

// Copyright returns whether or not this recording is copywritten - stored in position 3.
func (f FrameHeader) Copyright() int {
	return int(f&0x00000008) >> 3
}

// OriginalOrCopy returns whether or not this is an Original recording or a copy of one - stored in position 2.
func (f FrameHeader) OriginalOrCopy() int {
	return int(f&0x00000004) >> 2
}

// Emphasis returns emphasis - the emphasis indication is here to tell the decoder that the file must be de-emphasized -
// stored in position 0,1.
func (f FrameHeader) Emphasis() int {
	return int(f&0x00000003) >> 0
}

// LowSamplingFrequency returns whether the frame is encoded in a low sampling frequency => 0 = MPEG-1, 1 = MPEG-2/2.5.
func (f FrameHeader) LowSamplingFrequency() int {
	if f.ID() == consts.Version1 {
		return 0
	}

	return 1
}

// BytesPerFrame returns the number of PCM bytes produced per frame.
func (f FrameHeader) BytesPerFrame() int {
	return consts.SamplesPerGr * f.Granules() * 4
}

// Granules returns the number of granules per frame.
func (f FrameHeader) Granules() int {
	if f.LowSamplingFrequency() == 0 {
		return consts.GranulesMpeg1
	}

	return consts.GranulesMpeg2
}

// IsValid returns a boolean value indicating whether the header is valid or not.
// This function drives the sync scanner loop — any header failing IsValid() causes
// the scanner to slide forward and retry. All conditions that this decoder cannot
// handle must be checked here so false syncs are skipped automatically.
func (f FrameHeader) IsValid() bool {
	const sync = 0xffe00000
	if (f & sync) != sync {
		return false
	}

	if f.ID() == consts.VersionReserved {
		return false
	}

	// MPEG 2.5 is not supported by this decoder.
	if f.ID() == consts.Version2_5 {
		return false
	}

	if f.BitrateIndex() == 15 {
		return false
	}

	// Free bitrate (index 0) is not supported by this decoder.
	if f.BitrateIndex() == 0 {
		return false
	}

	if f.SamplingFrequency() == consts.SamplingFrequencyReserved {
		return false
	}

	if f.Layer() == consts.LayerReserved {
		return false
	}

	// Only Layer III is supported by this decoder.
	if f.Layer() != consts.Layer3 {
		return false
	}

	if f.Emphasis() == 2 {
		return false
	}

	return true
}

// Bitrate returns the bitrate of the frame in bits per second.
func (f FrameHeader) Bitrate() int {
	bitrates := [2][3][16]int{
		{
			// MPEG 1 Layer 3
			{
				0, 32000, 40000, 48000, 56000, 64000, 80000, 96000,
				112000, 128000, 160000, 192000, 224000, 256000, 320000,
			},

			// MPEG 1 Layer 2
			{
				0, 32000, 48000, 56000, 64000, 80000, 96000, 112000,
				128000, 160000, 192000, 224000, 256000, 320000, 384000,
			},

			// MPEG 1 Layer 1
			{
				0, 32000, 64000, 96000, 128000, 160000, 192000, 224000,
				256000, 288000, 320000, 352000, 384000, 416000, 448000,
			},
		},
		{
			// MPEG2 2 Layer 3
			{
				0, 8000, 16000, 24000, 32000, 40000, 48000, 56000,
				64000, 80000, 96000, 112000, 128000, 144000, 160000,
			},

			// MPEG 2 Layer 2
			{
				0, 8000, 16000, 24000, 32000, 40000, 48000, 56000,
				64000, 80000, 96000, 112000, 128000, 144000, 160000,
			},

			// MPEG 2 Layer 1
			{
				0, 32000, 48000, 56000, 64000, 80000, 96000, 112000,
				128000, 144000, 160000, 176000, 192000, 224000, 256000,
			},
		},
	}

	return bitrates[f.LowSamplingFrequency()][f.Layer()-1][f.BitrateIndex()]
}

// FrameSize returns the size of the frame in bytes.
func (f FrameHeader) FrameSize() (int, error) {
	freq, err := f.SamplingFrequencyValue()
	if err != nil {
		return 0, err
	}

	// MPEG1: 144 * bitrate / freq + padding
	// MPEG2: 72 * bitrate / freq + padding
	// The padding bit must be added AFTER the division, not before the shift.
	size := (144*f.Bitrate()/freq)>>uint(f.LowSamplingFrequency()) +
		f.PaddingBit()

	return size, nil
}

// SideInfoSize returns the size of the side information in bytes.
func (f FrameHeader) SideInfoSize() int {
	mono := f.Mode() == consts.ModeSingleChannel

	var sideInfoSize int

	if f.LowSamplingFrequency() == 1 {
		if mono {
			sideInfoSize = 9
		} else {
			sideInfoSize = 17
		}
	} else {
		if mono {
			sideInfoSize = 17
		} else {
			sideInfoSize = 32
		}
	}

	return sideInfoSize
}

// NumberOfChannels returns the number of audio channels.
func (f FrameHeader) NumberOfChannels() int {
	if f.Mode() == consts.ModeSingleChannel {
		return 1
	}

	return 2
}

// FullReader is an interface for reading complete byte slices.
type FullReader interface {
	ReadFull(buf []byte) (int, error)
}

// Read reads and validates an MPEG frame header from the source.
func Read(source FullReader, position int64) (h FrameHeader, startPosition int64, err error) {
	buf := make([]byte, 4)
	if n, err := source.ReadFull(buf); n < 4 {
		if errors.Is(err, io.EOF) {
			if n == 0 {
				// Expected EOF
				return 0, 0, io.EOF
			}

			return 0, 0, &consts.UnexpectedEOF{At: "readHeader (1)"}
		}

		return 0, 0, err
	}

	byte1 := uint32(buf[0])
	byte2 := uint32(buf[1])
	byte3 := uint32(buf[2])
	byte4 := uint32(buf[3])

	header := FrameHeader((byte1 << 24) | (byte2 << 16) | (byte3 << 8) | (byte4 << 0))
	for !header.IsValid() {
		byte1 = byte2
		byte2 = byte3
		byte3 = byte4

		buf := make([]byte, 1)
		if _, err := source.ReadFull(buf); err != nil {
			if errors.Is(err, io.EOF) {
				return 0, 0, &consts.UnexpectedEOF{At: "readHeader (2)"}
			}

			return 0, 0, err
		}

		byte4 = uint32(buf[0])
		header = FrameHeader((byte1 << 24) | (byte2 << 16) | (byte3 << 8) | (byte4 << 0))
		position++
	}

	// If we get here we've found the sync word, and can decode the header
	// which is in the low 20 bits of the 32-bit sync+header word.
	// Free bitrate, non-Layer3, and MPEG 2.5 are already rejected by IsValid().

	return header, position, nil
}
