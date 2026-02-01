package mp3

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/mycophonic/saprobe"
	gomp3 "github.com/mycophonic/saprobe/third-party/go-mp3"
)

const (
	channels       = 2 // go-mp3 always decodes to stereo
	bytesPerSample = 2 // 16-bit
	bytesPerFrame  = channels * bytesPerSample

	// Samples per granule in Layer III.
	samplesPerGranule = 576

	// MPEG1 has 2 granules per frame, MPEG2/2.5 has 1.
	samplesPerFrameMPEG1 = samplesPerGranule * 2 // 1152
	samplesPerFrameMPEG2 = samplesPerGranule * 1 // 576

	// go-mp3 decoder has additional delay from synthesis filterbank priming.
	// This is the difference between go-mp3's output and what LAME header expects.
	// Empirically measured: 529 samples.
	decoderDelay = 529
)

// MPEG version identifiers (2-bit field in frame header).
const (
	mpegVersion25   = 0x00 // MPEG 2.5
	mpegVersionRsvd = 0x01 // Reserved (invalid)
	mpegVersion2    = 0x02 // MPEG 2
	mpegVersion1    = 0x03 // MPEG 1
	channelModeMono = 0x03 // Mono channel mode
)

// MPEG side information sizes (bytes).
const (
	sideInfoMPEG1Stereo = 32
	sideInfoMPEG1Mono   = 17
	sideInfoMPEG2Stereo = 17
	sideInfoMPEG2Mono   = 9
)

// XING header layout.
const (
	xingPreambleSize = 8    // "Xing"/"Info" tag (4) + flags (4).
	xingFlagFrames   = 0x01 // Frame count field present (4 bytes).
	xingFlagBytes    = 0x02 // Byte count field present (4 bytes).
	xingFlagTOC      = 0x04 // Table of contents present (100 bytes).
	xingFlagQuality  = 0x08 // Quality indicator present (4 bytes).
	xingTOCSize      = 100  // TOC is always 100 bytes.
)

// LAME tag layout.
const (
	lameTagMinSize     = 24    // Minimum bytes for a valid LAME tag.
	lameGaplessOffset  = 21    // Offset of gapless info within LAME tag.
	gaplessFieldBits   = 12    // Each gapless field (delay/padding) is 12 bits.
	gaplessPaddingMask = 0xFFF // 12-bit mask for gapless padding field.
)

// ID3v2 constants.
const (
	id3v2HeaderSize = 10 // ID3v2 header: "ID3" (3) + version (2) + flags (1) + size (4).
)

// Parsing thresholds.
const (
	headerBufSize   = 4096 // Buffer size for reading first MPEG frame.
	minHeaderBytes  = 256  // Minimum bytes needed to find a valid frame header.
	minXINGPlusLAME = 120  // Minimum bytes from XING offset for XING+LAME headers.
	encoderTagLen   = 9    // Length to check for printable encoder tag.
)

// Printable ASCII range.
const (
	printableASCIIMin = 0x20
	printableASCIIMax = 0x7E
)

// gaplessInfo contains encoder delay and padding from LAME header.
type gaplessInfo struct {
	delay           int  // samples to skip at start (LAME encoder delay)
	padding         int  // samples to skip at end (LAME padding)
	hasXINGTag      bool // true if XING/Info frame present (adds decoded samples to output)
	samplesPerFrame int  // decoded samples per frame: 1152 for MPEG1, 576 for MPEG2/2.5
}

// Decode reads an MP3 stream and decodes it to interleaved little-endian signed 16-bit PCM bytes.
// The output is always stereo (2 channels) at the source sample rate.
// If the file contains LAME gapless metadata, encoder delay and padding are trimmed automatically.
func Decode(reader io.ReadSeeker) ([]byte, saprobe.PCMFormat, error) {
	// Parse gapless info before decoding.
	gapless := parseGaplessInfo(reader)

	// Seek back to start for decoding.
	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		return nil, saprobe.PCMFormat{}, fmt.Errorf("seeking to start: %w", err)
	}

	decoder, err := gomp3.NewDecoder(reader)
	if err != nil {
		return nil, saprobe.PCMFormat{}, fmt.Errorf("creating mp3 decoder: %w", err)
	}

	format := saprobe.PCMFormat{
		SampleRate: decoder.SampleRate(),
		BitDepth:   saprobe.Depth16,
		Channels:   channels,
	}

	// Pre-allocate output buffer when total length is known.
	var buf []byte
	if length := decoder.Length(); length > 0 {
		buf = make([]byte, 0, length)
	}

	chunk := make([]byte, 32*1024)

	for {
		readN, readErr := decoder.Read(chunk)
		if readN > 0 {
			buf = append(buf, chunk[:readN]...)
		}

		if errors.Is(readErr, io.EOF) {
			break
		}

		if readErr != nil {
			return nil, saprobe.PCMFormat{}, fmt.Errorf("decoding mp3: %w", readErr)
		}
	}

	// Apply gapless trimming if we have valid info.
	buf = applyGaplessTrimming(buf, gapless)

	return buf, format, nil
}

// applyGaplessTrimming removes encoder delay from start and padding from end.
// For go-mp3 decoder, we need to account for:
// 1. XING/Info frame being decoded as audio (1152 MPEG1 / 576 MPEG2 samples) if present
// 2. go-mp3's synthesis filterbank priming delay (529 samples).
func applyGaplessTrimming(buf []byte, info gaplessInfo) []byte {
	if info.delay == 0 && info.padding == 0 && !info.hasXINGTag {
		return buf
	}

	// Calculate start trim: LAME delay + decoder delay + XING frame (if present).
	startSamples := info.delay + decoderDelay
	if info.hasXINGTag {
		startSamples += info.samplesPerFrame
	}

	// Calculate end trim: LAME padding - decoder delay (decoder delay shifts from end to start).
	endSamples := max(info.padding-decoderDelay, 0)

	startBytes := startSamples * bytesPerFrame
	endBytes := endSamples * bytesPerFrame
	totalTrim := startBytes + endBytes

	// Sanity check: don't trim more than we have.
	if totalTrim >= len(buf) {
		return buf
	}

	return buf[startBytes : len(buf)-endBytes]
}

// parseGaplessInfo attempts to extract LAME encoder delay and padding from the MP3 file.
// Returns zero values if no LAME header is found.
func parseGaplessInfo(reader io.ReadSeeker) gaplessInfo {
	// Skip ID3v2 tag if present (can be very large, 100KB+).
	id3Size := skipID3v2(reader)
	if id3Size < 0 {
		return gaplessInfo{}
	}

	// Read enough bytes to find XING/LAME header (first frame is typically < 2KB).
	header := make([]byte, headerBufSize)
	bytesRead, err := reader.Read(header)

	if err != nil || bytesRead < minHeaderBytes {
		return gaplessInfo{}
	}

	header = header[:bytesRead]

	// Find first MPEG sync word.
	syncPos := findSyncWord(header)
	if syncPos < 0 || syncPos+4 > len(header) {
		return gaplessInfo{}
	}

	// Parse frame header to get side info size.
	frameHeader := header[syncPos : syncPos+4]
	sideInfoSize := getSideInfoSize(frameHeader)

	if sideInfoSize < 0 {
		return gaplessInfo{}
	}

	// XING header starts after frame header (4 bytes) + side info.
	xingOffset := syncPos + 4 + sideInfoSize
	if xingOffset+minXINGPlusLAME > len(header) {
		return gaplessInfo{}
	}

	// Look for XING or Info tag.
	xingData := header[xingOffset:]
	if !bytes.HasPrefix(xingData, []byte("Xing")) && !bytes.HasPrefix(xingData, []byte("Info")) {
		return gaplessInfo{}
	}

	// XING/Info frame found - go-mp3 will decode this as audio.
	hasXING := true
	spf := frameSamplesPerFrame(frameHeader)

	// Parse XING header to find LAME tag offset.
	// XING structure: "Xing" (4) + flags (4) + optional fields based on flags.
	lameOffset := findLAMETag(xingData)
	if lameOffset < 0 || lameOffset+lameTagMinSize > len(xingData) {
		// XING present but no LAME tag - still need to skip the XING frame.
		return gaplessInfo{hasXINGTag: hasXING, samplesPerFrame: spf}
	}

	// LAME tag structure: "LAME" (4) + version (5) + ... + gapless info at offset 21-23.
	// Gapless info: 24 bits = 12-bit delay + 12-bit padding.
	lameData := xingData[lameOffset:]
	if len(lameData) < lameTagMinSize {
		return gaplessInfo{hasXINGTag: hasXING, samplesPerFrame: spf}
	}

	// Gapless bytes are at offset 21 from LAME tag start.
	gaplessBytes := lameData[lameGaplessOffset:lameTagMinSize]
	gapless24 := uint32(gaplessBytes[0])<<16 | uint32(gaplessBytes[1])<<8 | uint32(gaplessBytes[2])

	delay := int(gapless24 >> gaplessFieldBits)
	padding := int(gapless24 & gaplessPaddingMask)

	return gaplessInfo{
		delay:           delay,
		padding:         padding,
		hasXINGTag:      hasXING,
		samplesPerFrame: spf,
	}
}

// skipID3v2 skips past any ID3v2 tag at the start of the file.
// Returns the size of the tag (0 if none), or -1 on error.
func skipID3v2(reader io.ReadSeeker) int {
	// ID3v2 header: "ID3" (3 bytes) + version (2) + flags (1) + size (4, syncsafe)
	header := make([]byte, id3v2HeaderSize)
	bytesRead, err := reader.Read(header)

	if err != nil || bytesRead < id3v2HeaderSize {
		// No ID3v2 tag or read error - seek back and continue.
		_, _ = reader.Seek(0, io.SeekStart)

		return 0
	}

	// Check for "ID3" signature.
	//nolint:gosec // G602: bounds guaranteed by bytesRead < 10 guard above.
	if header[0] != 'I' || header[1] != 'D' || header[2] != '3' {
		// No ID3v2 tag - seek back to start.
		_, _ = reader.Seek(0, io.SeekStart)

		return 0
	}

	// Parse syncsafe size (4 bytes, 7 bits each).
	//nolint:gosec // Bounds guaranteed by bytesRead < 10 guard above.
	//revive:disable-next-line:add-constant
	size := (int(header[6]) << 21) | (int(header[7]) << 14) | (int(header[8]) << 7) | int(
		header[9],
	)

	// Total tag size = header + size.
	totalSize := id3v2HeaderSize + size

	// Seek past the ID3v2 tag.
	if _, err := reader.Seek(int64(totalSize), io.SeekStart); err != nil {
		return -1
	}

	return totalSize
}

// findSyncWord locates the first MPEG audio sync word (0xFF followed by 0xE0+).
func findSyncWord(data []byte) int {
	for i := range len(data) - 1 {
		if data[i] == 0xFF && (data[i+1]&0xE0) == 0xE0 {
			// Verify it's a valid MPEG audio frame header.
			if i+4 <= len(data) && isValidFrameHeader(data[i:i+4]) {
				return i
			}
		}
	}

	return -1
}

// isValidFrameHeader checks if 4 bytes form a valid MPEG audio frame header.
func isValidFrameHeader(header []byte) bool {
	if len(header) < 4 {
		return false
	}

	// Sync word check.
	if header[0] != 0xFF || (header[1]&0xE0) != 0xE0 {
		return false
	}

	// Extract fields.
	versionBits := (header[1] >> 3) & 0x03
	layerBits := (header[1] >> 1) & 0x03
	bitrateBits := (header[2] >> 4) & 0x0F

	// Version 01 is reserved.
	if versionBits == mpegVersionRsvd {
		return false
	}

	// Layer 00 is reserved.
	if layerBits == 0x00 {
		return false
	}

	// Bitrate 1111 is invalid, 0000 is "free" (unusual but valid).
	if bitrateBits == 0x0F {
		return false
	}

	return true
}

// getSideInfoSize returns the side information size based on MPEG version and channel mode.
// Returns -1 if the header is invalid.
func getSideInfoSize(header []byte) int {
	if len(header) < 4 {
		return -1
	}

	versionBits := (header[1] >> 3) & 0x03
	channelBits := (header[3] >> 6) & 0x03

	isMono := channelBits == channelModeMono

	// MPEG version: 00=2.5, 01=reserved, 10=2, 11=1.
	switch versionBits {
	case mpegVersion1:
		if isMono {
			return sideInfoMPEG1Mono
		}

		return sideInfoMPEG1Stereo
	case mpegVersion2, mpegVersion25:
		if isMono {
			return sideInfoMPEG2Mono
		}

		return sideInfoMPEG2Stereo
	default:
		return -1
	}
}

// findLAMETag locates the LAME tag within XING header data.
// Returns offset from start of xingData, or -1 if not found.
func findLAMETag(xingData []byte) int {
	// XING header: "Xing"/"Info" (4) + flags (4) + optional data.
	// Flags indicate which optional fields are present:
	//   bit 0: frames count (4 bytes)
	//   bit 1: bytes count (4 bytes)
	//   bit 2: TOC (100 bytes)
	//   bit 3: quality (4 bytes)
	// LAME tag follows immediately after XING optional fields.
	if len(xingData) < xingPreambleSize {
		return -1
	}

	flags := binary.BigEndian.Uint32(xingData[4:xingPreambleSize])
	offset := xingPreambleSize // Start after "Xing" + flags.

	// Skip optional fields based on flags.
	if flags&xingFlagFrames != 0 {
		offset += 4 // frames
	}

	if flags&xingFlagBytes != 0 {
		offset += 4 // bytes
	}

	if flags&xingFlagTOC != 0 {
		offset += xingTOCSize
	}

	if flags&xingFlagQuality != 0 {
		offset += 4 // quality
	}

	// Check for LAME tag.
	if offset+4 > len(xingData) {
		return -1
	}

	if bytes.HasPrefix(xingData[offset:], []byte("LAME")) {
		return offset
	}

	// Some encoders use different tags (Lavf, Lavc, etc.) with same structure.
	// Check if there's encoder info at this position anyway.
	// Look for printable ASCII which would indicate an encoder tag.
	if offset+encoderTagLen <= len(xingData) {
		tag := xingData[offset : offset+4]
		if isPrintableASCII(tag) {
			return offset
		}
	}

	return -1
}

// frameSamplesPerFrame returns the number of decoded samples per frame
// based on the MPEG version in the frame header.
// MPEG1 Layer III: 2 granules × 576 = 1152 samples.
// MPEG2/2.5 Layer III: 1 granule × 576 = 576 samples.
func frameSamplesPerFrame(header []byte) int {
	versionBits := (header[1] >> 3) & 0x03
	if versionBits == mpegVersion1 {
		return samplesPerFrameMPEG1
	}

	return samplesPerFrameMPEG2
}

// isPrintableASCII checks if all bytes are printable ASCII characters.
func isPrintableASCII(data []byte) bool {
	for _, b := range data {
		if b < printableASCIIMin || b > printableASCIIMax {
			return false
		}
	}

	return true
}
