package frameheader

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/mycophonic/saprobe/third-party/go-mp3/internal/consts"
)

// Sentinel errors returned by Parse.
var (
	ErrInvalidSync      = errors.New("frameheader2: invalid sync word")
	ErrReservedVersion  = errors.New("frameheader2: reserved MPEG version")
	ErrUnsupportedMPEG2 = errors.New("frameheader2: MPEG 2.5 not supported")
	ErrReservedLayer    = errors.New("frameheader2: reserved layer")
	ErrUnsupportedLayer = errors.New("frameheader2: only Layer III supported")
	ErrInvalidBitrate   = errors.New("frameheader2: invalid bitrate index")
	ErrFreeBitrate      = errors.New("frameheader2: free bitrate not supported")
	ErrReservedSfreq    = errors.New("frameheader2: reserved sampling frequency")
	ErrInvalidEmphasis  = errors.New("frameheader2: invalid emphasis value")
)

// Bit field extraction constants.
const (
	maskSync        = 0xFFE00000
	shiftID         = 19
	maskID          = 0x3
	shiftLayer      = 17
	maskLayer       = 0x3
	shiftProtection = 16
	maskProtection  = 0x1
	shiftBitrate    = 12
	maskBitrate     = 0xF
	shiftSfreq      = 10
	maskSfreq       = 0x3
	shiftPadding    = 9
	maskPadding     = 0x1
	shiftPrivate    = 8
	maskPrivate     = 0x1
	shiftMode       = 6
	maskMode        = 0x3
	shiftModeExt    = 4
	maskModeExt     = 0x3
	shiftCopyright  = 3
	maskCopyright   = 0x1
	shiftOriginal   = 2
	maskOriginal    = 0x1
	maskEmphasis    = 0x3

	// Validation boundary constants.
	invalidBitrateIdx  = 15
	invalidEmphasisVal = 2

	// Mode extension bit masks for joint stereo.
	modeExtMSBit        = 0x2
	modeExtIntensityBit = 0x1

	// Frame size calculation constant: slots per frame for Layer III.
	frameSizeSlots = 144
)

// bitrateTable[lsf][layer-1][bitrateIndex] = bits per second.
var bitrateTable = [2][3][16]int{ //nolint:gochecknoglobals // immutable lookup table
	{
		// MPEG 1 Layer 3
		{
			0, 32000, 40000, 48000, 56000, 64000, 80000, 96000,
			112000, 128000, 160000, 192000, 224000, 256000, 320000, 0,
		},
		// MPEG 1 Layer 2
		{
			0, 32000, 48000, 56000, 64000, 80000, 96000, 112000,
			128000, 160000, 192000, 224000, 256000, 320000, 384000, 0,
		},
		// MPEG 1 Layer 1
		{
			0, 32000, 64000, 96000, 128000, 160000, 192000, 224000,
			256000, 288000, 320000, 352000, 384000, 416000, 448000, 0,
		},
	},
	{
		// MPEG 2 Layer 3
		{
			0, 8000, 16000, 24000, 32000, 40000, 48000, 56000,
			64000, 80000, 96000, 112000, 128000, 144000, 160000, 0,
		},
		// MPEG 2 Layer 2
		{
			0, 8000, 16000, 24000, 32000, 40000, 48000, 56000,
			64000, 80000, 96000, 112000, 128000, 144000, 160000, 0,
		},
		// MPEG 2 Layer 1
		{
			0, 32000, 48000, 56000, 64000, 80000, 96000, 112000,
			128000, 144000, 160000, 176000, 192000, 224000, 256000, 0,
		},
	},
}

// sampleRateTable[sfreqIndex] = base Hz (MPEG-1 rates; halved for MPEG-2).
var sampleRateTable = [3]int{44100, 48000, 32000} //nolint:gochecknoglobals // immutable lookup table

// sideInfoTable[lsf][mono] = side info bytes.
// Index 0 = stereo, index 1 = mono.
var sideInfoTable = [2][2]int{ //nolint:gochecknoglobals // immutable lookup table
	{32, 17}, // MPEG-1: stereo=32, mono=17
	{17, 9},  // MPEG-2: stereo=17, mono=9
}

// Header is a parsed, validated MPEG audio frame header.
// All fields are precomputed at parse time; method calls are field reads.
type Header struct {
	version       consts.Version
	layer         consts.Layer
	protected     bool // true = CRC present (protection_bit == 0 in spec)
	bitrateIndex  uint8
	sfreqIndex    consts.SamplingFrequency
	padded        bool
	privateBit    bool
	mode          consts.Mode
	modeExtension uint8
	copyright     bool
	original      bool
	emphasis      uint8

	// Precomputed derived values (validated at parse time).
	lsf          bool // true for MPEG-2/2.5
	channels     int  // 1 or 2
	granules     int  // 1 or 2
	sampleRateHz int  // actual Hz value
	bitrate      int  // bits per second
	frameSize    int  // bytes per frame (including padding)
	sideInfoSize int  // bytes of side info
}

// Version returns the MPEG version.
func (h *Header) Version() consts.Version { return h.version }

// Layer returns the MPEG layer.
func (h *Header) Layer() consts.Layer { return h.layer }

// Protected returns true if CRC protection is present (protection_bit == 0 in spec).
func (h *Header) Protected() bool { return h.protected }

// BitrateIndex returns the raw bitrate index from the header.
func (h *Header) BitrateIndex() int { return int(h.bitrateIndex) }

// SamplingFrequency returns the sampling frequency index.
func (h *Header) SamplingFrequency() consts.SamplingFrequency { return h.sfreqIndex }

// Padded returns true if the frame has a padding byte.
func (h *Header) Padded() bool { return h.padded }

// Mode returns the channel mode.
func (h *Header) Mode() consts.Mode { return h.mode }

// Channels returns the number of audio channels (1 or 2).
func (h *Header) Channels() int { return h.channels }

// Granules returns the number of granules per frame.
func (h *Header) Granules() int { return h.granules }

// SampleRateHz returns the actual sampling frequency in Hz.
func (h *Header) SampleRateHz() int { return h.sampleRateHz }

// Bitrate returns the bitrate in bits per second.
func (h *Header) Bitrate() int { return h.bitrate }

// FrameSize returns the frame size in bytes (including padding).
func (h *Header) FrameSize() int { return h.frameSize }

// SideInfoSize returns the side information size in bytes.
func (h *Header) SideInfoSize() int { return h.sideInfoSize }

// BytesPerFrame returns the number of PCM bytes produced per frame.
func (h *Header) BytesPerFrame() int { return consts.SamplesPerGr * h.granules * 4 }

// LowSamplingFrequency returns 0 for MPEG-1, 1 for MPEG-2.
// Kept as int for index compatibility with existing consumers.
func (h *Header) LowSamplingFrequency() int {
	if h.lsf {
		return 1
	}

	return 0
}

// UseMSStereo returns true if the frame uses middle/side stereo.
func (h *Header) UseMSStereo() bool {
	return h.mode == consts.ModeJointStereo && h.modeExtension&modeExtMSBit != 0
}

// UseIntensityStereo returns true if the frame uses intensity stereo.
func (h *Header) UseIntensityStereo() bool {
	return h.mode == consts.ModeJointStereo && h.modeExtension&modeExtIntensityBit != 0
}

// Parse validates a raw 32-bit frame header and returns a fully populated Header.
// If Parse succeeds, all derived values are guaranteed valid and no accessor
// method will ever return an error.
func Parse(raw uint32) (Header, error) {
	if raw&maskSync != maskSync {
		return Header{}, ErrInvalidSync
	}

	version := consts.Version((raw >> shiftID) & maskID)

	switch version { //nolint:exhaustive // default covers future values
	case consts.Version1, consts.Version2:
		// OK
	case consts.Version2_5:
		return Header{}, ErrUnsupportedMPEG2
	default:
		return Header{}, ErrReservedVersion
	}

	layer := consts.Layer((raw >> shiftLayer) & maskLayer)

	switch layer { //nolint:exhaustive // default covers future values
	case consts.Layer3:
		// OK — only layer we support
	case consts.Layer2, consts.Layer1:
		return Header{}, ErrUnsupportedLayer
	default:
		return Header{}, ErrReservedLayer
	}

	bitrateIndex := (raw >> shiftBitrate) & maskBitrate
	if bitrateIndex == invalidBitrateIdx {
		return Header{}, ErrInvalidBitrate
	}

	if bitrateIndex == 0 {
		return Header{}, ErrFreeBitrate
	}

	sfreqIndex := consts.SamplingFrequency((raw >> shiftSfreq) & maskSfreq)
	if sfreqIndex == consts.SamplingFrequencyReserved {
		return Header{}, ErrReservedSfreq
	}

	emphasis := raw & maskEmphasis
	if emphasis == invalidEmphasisVal {
		return Header{}, ErrInvalidEmphasis
	}

	// All validation passed — compute derived values.
	lsf := version != consts.Version1
	mode := consts.Mode((raw >> shiftMode) & maskMode)

	mono := 0
	channels := 2

	if mode == consts.ModeSingleChannel {
		mono = 1
		channels = 1
	}

	granules := consts.GranulesMpeg1
	if lsf {
		granules = consts.GranulesMpeg2
	}

	lsfInt := 0
	if lsf {
		lsfInt = 1
	}

	sampleRateHz := sampleRateTable[sfreqIndex] >> lsfInt
	bitrate := bitrateTable[lsfInt][layer-1][bitrateIndex]
	frameSize := (frameSizeSlots*bitrate/sampleRateHz)>>lsfInt + int((raw>>shiftPadding)&maskPadding)
	sideInfoSize := sideInfoTable[lsfInt][mono]

	return Header{
		version:       version,
		layer:         layer,
		protected:     (raw>>shiftProtection)&maskProtection == 0,
		bitrateIndex:  uint8(bitrateIndex), //nolint:gosec // validated <= 14 above
		sfreqIndex:    sfreqIndex,
		padded:        (raw>>shiftPadding)&maskPadding != 0,
		privateBit:    (raw>>shiftPrivate)&maskPrivate != 0,
		mode:          mode,
		modeExtension: uint8((raw >> shiftModeExt) & maskModeExt), //nolint:gosec // 2-bit field, max 3
		copyright:     (raw>>shiftCopyright)&maskCopyright != 0,
		original:      (raw>>shiftOriginal)&maskOriginal != 0,
		emphasis:      uint8(emphasis), //nolint:gosec // validated != 2, max 3
		lsf:           lsf,
		channels:      channels,
		granules:      granules,
		sampleRateHz:  sampleRateHz,
		bitrate:       bitrate,
		frameSize:     frameSize,
		sideInfoSize:  sideInfoSize,
	}, nil
}

// Read reads and validates an MPEG frame header from the source.
// It returns the parsed header, the byte position where the header starts,
// and any error. On clean EOF at the start of a read, it returns io.EOF.
// Stack-allocated buffers ensure zero heap allocation in the sync loop.
func Read(source consts.FullReader, position int64) (Header, int64, error) {
	var buf [4]byte

	n, err := source.ReadFull(buf[:])
	if n < 4 {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			if n == 0 {
				return Header{}, 0, io.EOF
			}

			return Header{}, 0, &consts.UnexpectedEOF{At: "readHeader (1)"}
		}

		return Header{}, 0, fmt.Errorf("frameheader2: initial read: %w", err)
	}

	raw := binary.BigEndian.Uint32(buf[:])

	header, parseErr := Parse(raw)
	if parseErr == nil {
		return header, position, nil
	}

	// Sync loop: slide one byte at a time.
	for {
		copy(buf[:3], buf[1:4])

		if _, err = source.ReadFull(buf[3:4]); err != nil {
			if errors.Is(err, io.EOF) {
				return Header{}, 0, &consts.UnexpectedEOF{At: "readHeader (2)"}
			}

			return Header{}, 0, fmt.Errorf("frameheader2: sync scan read: %w", err)
		}

		position++

		raw = binary.BigEndian.Uint32(buf[:])

		header, parseErr = Parse(raw)
		if parseErr == nil {
			return header, position, nil
		}
	}
}
