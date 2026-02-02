package mp3

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/mycophonic/saprobe"
	"github.com/mycophonic/saprobe/mp3/internal/consts"
	"github.com/mycophonic/saprobe/mp3/internal/frame"
)

const (
	channels       = 2 // always decodes to stereo
	bytesPerSample = 2 // 16-bit
	bytesPerFrame  = channels * bytesPerSample

	// Samples per granule in Layer III.
	samplesPerGranule = 576

	// MPEG1 has 2 granules per frame, MPEG2/2.5 has 1.
	samplesPerFrameMPEG1 = samplesPerGranule * 2 // 1152
	samplesPerFrameMPEG2 = samplesPerGranule * 1 // 576

	// Decoder delay from synthesis filterbank priming (empirically measured).
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

// source is a minimal position-tracking wrapper around io.ReadSeeker.
// It implements consts.FullReader so internal packages can track the stream position.
type source struct {
	reader io.ReadSeeker
	pos    int64
}

func (s *source) ReadFull(buf []byte) (int, error) {
	read, err := io.ReadFull(s.reader, buf)
	s.pos += int64(read)

	if errors.Is(err, io.ErrUnexpectedEOF) {
		err = io.EOF
	}

	return read, err
}

// gaplessInfo contains encoder delay and padding from LAME header.
type gaplessInfo struct {
	delay           int  // samples to skip at start (LAME encoder delay)
	padding         int  // samples to skip at end (LAME padding)
	hasXINGTag      bool // true if XING/Info frame present (adds decoded samples to output)
	samplesPerFrame int  // decoded samples per frame: 1152 for MPEG1, 576 for MPEG2/2.5
}

// Decoder streams decoded PCM from an MP3 source.
// Output is always stereo 16-bit little-endian at the source sample rate.
// LAME gapless metadata is applied automatically (start trim + end trim).
type Decoder struct {
	src     *source
	mpFrame *frame.Frame
	format  saprobe.PCMFormat

	// Gapless trimming state.
	startTrimRemaining int // decoded bytes still to discard at start
	endTrimBytes       int // bytes to hold back for end trim

	// Per-frame decoded PCM buffer, drained by Read.
	buf    []byte
	bufOff int

	// Hold buffer: retains the last endTrimBytes of decoded output.
	// On EOF these bytes are discarded (they are encoder padding).
	holdBuf []byte

	eof       bool
	firstRead bool // true until first frame is decoded
}

// NewDecoder opens an MP3 stream and returns a streaming decoder.
// The first frame is read immediately to determine the output format.
func NewDecoder(rs io.ReadSeeker) (*Decoder, error) { //nolint:varnamelen // rs is idiomatic for io.ReadSeeker
	gapless := parseGaplessInfo(rs)

	if _, err := rs.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seeking to start: %w", err)
	}

	if err := skipTags(rs); err != nil {
		return nil, fmt.Errorf("skipping tags: %w", err)
	}

	pos, err := rs.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, fmt.Errorf("getting position: %w", err)
	}

	src := &source{reader: rs, pos: pos}
	mpFrame := frame.NewFrame()

	// Read first frame to determine format.
	if _, err := mpFrame.Read(src, src.pos); err != nil {
		return nil, fmt.Errorf("reading first mp3 frame: %w", err)
	}

	// Calculate gapless trim in bytes.
	startSamples := gapless.delay + decoderDelay
	if gapless.hasXINGTag {
		startSamples += gapless.samplesPerFrame
	}

	endSamples := max(gapless.padding-decoderDelay, 0)

	return &Decoder{
		src:     src,
		mpFrame: mpFrame,
		format: saprobe.PCMFormat{
			SampleRate: mpFrame.SampleRateHz(),
			BitDepth:   saprobe.Depth16,
			Channels:   channels,
		},
		startTrimRemaining: startSamples * bytesPerFrame,
		endTrimBytes:       endSamples * bytesPerFrame,
		firstRead:          true,
	}, nil
}

// Format returns the PCM output format.
func (d *Decoder) Format() saprobe.PCMFormat { return d.format }

// Read reads decoded PCM bytes from the MP3 stream.
func (d *Decoder) Read(p []byte) (int, error) { //nolint:varnamelen // p is idiomatic for io.Reader.Read
	total := 0

	for len(p) > 0 {
		// Drain buffered output.
		if d.bufOff < len(d.buf) {
			n := copy(p, d.buf[d.bufOff:])
			d.bufOff += n
			total += n
			p = p[n:]

			continue
		}

		if d.eof {
			if total > 0 {
				return total, nil
			}

			return 0, io.EOF
		}

		// Decode next frame into d.buf (with gapless trimming applied).
		if err := d.decodeNextFrame(); err != nil {
			if total > 0 {
				return total, nil
			}

			return 0, err
		}
	}

	return total, nil
}

// decodeNextFrame reads and decodes the next MP3 frame, applying gapless
// trimming, and stores the result in d.buf for Read to drain.
// Returns io.EOF when the stream is exhausted.
func (d *Decoder) decodeNextFrame() error {
	decoded, err := d.readFrame()
	if err != nil {
		d.eof = true

		return io.EOF
	}

	// Apply start trim.
	if d.startTrimRemaining > 0 {
		if d.startTrimRemaining >= len(decoded) {
			d.startTrimRemaining -= len(decoded)

			return d.decodeNextFrame() // Frame fully trimmed, advance to next.
		}

		decoded = decoded[d.startTrimRemaining:]
		d.startTrimRemaining = 0
	}

	d.applyEndTrim(decoded)

	return nil
}

// readFrame reads and decodes a single MP3 frame, returning raw PCM bytes.
// Frames with insufficient reservoir data are skipped (the frame's main data
// is consumed into the reservoir for subsequent frames, but no PCM is emitted).
func (d *Decoder) readFrame() ([]byte, error) {
	for {
		if d.firstRead {
			d.firstRead = false
		} else {
			if _, readErr := d.mpFrame.Read(d.src, d.src.pos); readErr != nil {
				if errors.Is(readErr, io.EOF) {
					return nil, io.EOF
				}

				var unexpectedEOF *consts.UnexpectedEOF
				if errors.As(readErr, &unexpectedEOF) {
					return nil, io.EOF
				}

				return nil, fmt.Errorf("decoding mp3: %w", readErr)
			}
		}

		if d.mpFrame.SkipDecode() {
			// Reservoir was insufficient — frame data has been consumed into
			// the reservoir for future frames. Skip this frame so it doesn't
			// create an alignment offset in the output.
			continue
		}

		buf := make([]byte, d.mpFrame.BytesPerFrame())
		d.mpFrame.Decode(buf)

		return buf, nil
	}
}

// applyEndTrim feeds decoded PCM through the end-trim hold buffer and
// stores releasable bytes in d.buf.
func (d *Decoder) applyEndTrim(decoded []byte) {
	if d.endTrimBytes == 0 {
		d.buf = decoded
		d.bufOff = 0

		return
	}

	// Combine holdBuf + decoded, then split: output all but last endTrimBytes.
	combined := make([]byte, len(d.holdBuf)+len(decoded))
	copy(combined, d.holdBuf)
	copy(combined[len(d.holdBuf):], decoded)

	if len(combined) <= d.endTrimBytes {
		d.holdBuf = combined
		d.buf = d.buf[:0]
		d.bufOff = 0

		return
	}

	release := len(combined) - d.endTrimBytes
	d.buf = combined[:release]
	d.bufOff = 0

	d.holdBuf = make([]byte, d.endTrimBytes)
	copy(d.holdBuf, combined[release:])
}

// Decode reads an MP3 stream and decodes it to interleaved little-endian signed 16-bit PCM bytes.
// The output is always stereo (2 channels) at the source sample rate.
// If the file contains LAME gapless metadata, encoder delay and padding are trimmed automatically.
func Decode(reader io.ReadSeeker) ([]byte, saprobe.PCMFormat, error) {
	dec, err := NewDecoder(reader)
	if err != nil {
		return nil, saprobe.PCMFormat{}, err
	}

	pcm, format, err := saprobe.ReadAll(dec)
	if err != nil {
		return nil, saprobe.PCMFormat{}, fmt.Errorf("decoding mp3: %w", err)
	}

	return pcm, format, nil
}

// skipTags skips past ID3v1 ("TAG") and ID3v2 ("ID3") headers at the start of the stream.
func skipTags(reader io.ReadSeeker) error {
	var buf [3]byte
	if _, err := io.ReadFull(reader, buf[:]); err != nil {
		// Can't read 3 bytes — seek to start and let the frame decoder handle it.
		_, _ = reader.Seek(0, io.SeekStart)

		return nil //nolint:nilerr // truncated stream is not fatal; frame decoder will report it
	}

	switch string(buf[:]) {
	case "TAG":
		// ID3v1 tag at start (non-standard). Skip remaining 125 bytes.
		//revive:disable-next-line:add-constant
		if _, err := reader.Seek(125, io.SeekCurrent); err != nil {
			return fmt.Errorf("skipping ID3v1: %w", err)
		}

	case "ID3":
		// ID3v2: skip version (2 bytes) + flags (1 byte).
		if _, err := reader.Seek(3, io.SeekCurrent); err != nil {
			return fmt.Errorf("skipping ID3v2 header: %w", err)
		}

		// Read syncsafe size (4 bytes, 7 bits each).
		var sizeBuf [4]byte
		if _, err := io.ReadFull(reader, sizeBuf[:]); err != nil {
			return nil //nolint:nilerr // truncated ID3v2 is not fatal
		}

		//revive:disable-next-line:add-constant
		size := (int64(sizeBuf[0]) << 21) | (int64(sizeBuf[1]) << 14) |
			(int64(sizeBuf[2]) << 7) | int64(sizeBuf[3])

		if _, err := reader.Seek(size, io.SeekCurrent); err != nil {
			return fmt.Errorf("skipping ID3v2 body: %w", err)
		}

	default:
		// Not a tag — seek back 3 bytes.
		if _, err := reader.Seek(-3, io.SeekCurrent); err != nil {
			return fmt.Errorf("seeking back after tag probe: %w", err)
		}
	}

	return nil
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

	// XING/Info frame found — the decoder will decode this as audio.
	hasXING := true
	spf := frameSamplesPerFrame(frameHeader)

	// Parse XING header to find LAME tag offset.
	// XING structure: "Xing" (4) + flags (4) + optional fields based on flags.
	lameOffset := findLAMETag(xingData)
	if lameOffset < 0 || lameOffset+lameTagMinSize > len(xingData) {
		// XING present but no LAME tag — still need to skip the XING frame.
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
		// No ID3v2 tag or read error — seek back and continue.
		if _, seekErr := reader.Seek(0, io.SeekStart); seekErr != nil {
			return -1
		}

		return 0
	}

	// Check for "ID3" signature.
	//nolint:gosec // G602: bounds guaranteed by bytesRead < 10 guard above.
	if header[0] != 'I' || header[1] != 'D' || header[2] != '3' {
		// No ID3v2 tag — seek back to start.
		if _, seekErr := reader.Seek(0, io.SeekStart); seekErr != nil {
			return -1
		}

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
// MPEG1 Layer III: 2 granules x 576 = 1152 samples.
// MPEG2/2.5 Layer III: 1 granule x 576 = 576 samples.
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
