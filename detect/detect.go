package detect

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Codec represents a recognized audio codec.
type Codec uint8

const (
	// Unknown indicates the file format was not recognized.
	Unknown Codec = iota
	// FLAC is the Free Lossless Audio Codec.
	FLAC
	// ALAC is the Apple Lossless Audio Codec (inside an M4A/MP4 container).
	ALAC
	// MP3 is MPEG-1/2 Audio Layer III.
	MP3
	// Vorbis is Ogg Vorbis.
	Vorbis
	// WAV is RIFF WAVE (PCM).
	WAV
	// AAC is Advanced Audio Coding (inside an M4A/MP4 container).
	AAC
)

// String returns the human-readable name of the codec.
func (c Codec) String() string {
	switch c {
	case Unknown:
		return "unknown"
	case FLAC:
		return "FLAC"
	case ALAC:
		return "ALAC"
	case MP3:
		return "MP3"
	case Vorbis:
		return "Vorbis"
	case WAV:
		return "WAV"
	case AAC:
		return "AAC"
	}

	return "unknown"
}

// headerSize is the minimum number of bytes needed to identify any supported codec.
// FLAC: 4 bytes at offset 0 ("fLaC").
// M4A:  4 bytes at offset 4 ("ftyp" in an M4A/MP4 container) — then deep probe for ALAC vs AAC.
// MP3:  3 bytes at offset 0 ("ID3") or 2-byte MPEG sync word (0xFF 0xE0 mask).
// OGG:  4 bytes at offset 0 ("OggS").
// WAV:  "RIFF" at offset 0 and "WAVE" at offset 8 (12 bytes total).
const (
	headerSize = 12

	// mpegSyncByte is the first byte of an MPEG audio frame sync word.
	mpegSyncByte = 0xFF
	// mpegSyncMask masks the upper 3 bits of the second byte in the sync word.
	mpegSyncMask = 0xE0

	// mp4BoxHeaderSize is the size of a standard MP4 box header (size + type).
	mp4BoxHeaderSize = 8
)

// Identify reads the header from rs and returns the detected audio codec.
// The reader position is reset to the start before returning.
func Identify(reader io.ReadSeeker) (Codec, error) {
	var header [headerSize]byte

	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return Unknown, fmt.Errorf("reading header: %w", err)
	}

	if _, err := reader.Seek(0, io.SeekStart); err != nil {
		return Unknown, fmt.Errorf("seeking to start: %w", err)
	}

	// WAV: "RIFF" at offset 0 and "WAVE" at offset 8.
	if string(header[:4]) == "RIFF" && string(header[8:12]) == "WAVE" {
		return WAV, nil
	}

	// FLAC: first four bytes are "fLaC".
	if string(header[:4]) == "fLaC" {
		return FLAC, nil
	}

	// Ogg container (Vorbis): first four bytes are "OggS".
	if string(header[:4]) == "OggS" {
		return Vorbis, nil
	}

	// M4A/MP4 container: bytes 4-7 are "ftyp". Probe deeper to distinguish ALAC from AAC.
	if string(header[4:8]) == "ftyp" {
		return probeM4ACodec(reader)
	}

	// MP3: ID3v2 tag header starts with "ID3".
	if string(header[:3]) == "ID3" {
		return MP3, nil
	}

	// MP3: MPEG frame sync word (11 set bits).
	//nolint:gosec // header is a fixed [8]byte array
	if header[0] == mpegSyncByte && header[1]&mpegSyncMask == mpegSyncMask {
		return MP3, nil
	}

	return Unknown, nil
}

// probeM4ACodec walks the MP4 box tree to find the first audio sample entry
// in an stsd box, returning ALAC or AAC based on the FourCC.
// The reader position is reset to the start before returning.
func probeM4ACodec(reader io.ReadSeeker) (Codec, error) {
	defer func() {
		// Always reset to start, best-effort.
		_, _ = reader.Seek(0, io.SeekStart)
	}()

	// Walk top-level boxes to find "moov".
	moovOffset, moovSize, err := findBox(reader, 0, -1, "moov")
	if err != nil || moovSize == 0 {
		return ALAC, nil // fallback: assume ALAC if we can't probe
	}

	// moov → trak → mdia → minf → stbl → stsd
	codec, found := probeM4ATraks(reader, moovOffset, moovSize)
	if found {
		return codec, nil
	}

	return ALAC, nil // fallback
}

// probeM4ATraks iterates over trak boxes inside moov looking for audio sample entries.
func probeM4ATraks(reader io.ReadSeeker, moovOffset, moovSize int64) (Codec, bool) {
	end := moovOffset + moovSize
	pos := moovOffset

	for pos < end {
		contentOffset, totalSize, boxType, err := readBoxHeader(reader, pos)
		if err != nil || totalSize == 0 {
			break
		}

		if boxType == "trak" {
			contentSize := totalSize - mp4BoxHeaderSize
			if codec, ok := probeM4ATrak(reader, contentOffset, contentSize); ok {
				return codec, true
			}
		}

		pos += totalSize
	}

	return Unknown, false
}

// probeM4ATrak probes a single trak box for an audio sample entry.
func probeM4ATrak(reader io.ReadSeeker, trakOffset, trakSize int64) (Codec, bool) {
	// trak → mdia
	mdiaOff, mdiaSize, err := findBox(reader, trakOffset, trakSize, "mdia")
	if err != nil || mdiaSize == 0 {
		return Unknown, false
	}

	// mdia → minf
	minfOff, minfSize, err := findBox(reader, mdiaOff, mdiaSize, "minf")
	if err != nil || minfSize == 0 {
		return Unknown, false
	}

	// minf → stbl
	stblOff, stblSize, err := findBox(reader, minfOff, minfSize, "stbl")
	if err != nil || stblSize == 0 {
		return Unknown, false
	}

	// stbl → stsd
	stsdOff, stsdSize, err := findBox(reader, stblOff, stblSize, "stsd")
	if err != nil || stsdSize == 0 {
		return Unknown, false
	}

	return probeStsd(reader, stsdOff, stsdSize)
}

// probeStsd reads the stsd box payload and returns the codec from the first sample entry FourCC.
func probeStsd(reader io.ReadSeeker, contentOffset, contentSize int64) (Codec, bool) {
	// stsd payload: version(1) + flags(3) + entry_count(4) = 8 bytes header,
	// then each entry starts with size(4) + FourCC(4).
	const stsdHeaderSize = 8

	if contentSize < stsdHeaderSize+mp4BoxHeaderSize {
		return Unknown, false
	}

	// Seek past the stsd version/flags/count header.
	if _, err := reader.Seek(contentOffset+stsdHeaderSize, io.SeekStart); err != nil {
		return Unknown, false
	}

	// Read the first sample entry header: size(4) + FourCC(4).
	var entry [mp4BoxHeaderSize]byte
	if _, err := io.ReadFull(reader, entry[:]); err != nil {
		return Unknown, false
	}

	fourCC := string(entry[4:8])

	switch fourCC {
	case "alac":
		return ALAC, true
	case "mp4a":
		return AAC, true
	default:
		return Unknown, false
	}
}

// findBox searches for a box with the given type among direct children
// starting at parentContentOffset within parentSize bytes.
// Returns the content offset (past the box header) and content size of the found box.
func findBox(reader io.ReadSeeker, parentContentOffset, parentSize int64, target string) (int64, int64, error) {
	end := parentContentOffset + parentSize
	if parentSize < 0 {
		// Scan until read failure (used for top-level).
		end = 1<<62 - 1
	}

	pos := parentContentOffset

	for pos < end {
		offset, size, boxType, err := readBoxHeader(reader, pos)
		if err != nil || size == 0 {
			return 0, 0, err
		}

		if boxType == target {
			contentSize := size - mp4BoxHeaderSize

			return offset, contentSize, nil
		}

		pos = offset - mp4BoxHeaderSize + size
	}

	return 0, 0, nil
}

// readBoxHeader reads an MP4 box header at the given position.
// Returns content offset (past header), total box size, box type, and any error.
func readBoxHeader(reader io.ReadSeeker, pos int64) (contentOffset, totalSize int64, boxType string, err error) {
	if _, err = reader.Seek(pos, io.SeekStart); err != nil {
		return 0, 0, "", err
	}

	var header [mp4BoxHeaderSize]byte
	if _, err = io.ReadFull(reader, header[:]); err != nil {
		return 0, 0, "", err
	}

	size := int64(binary.BigEndian.Uint32(header[0:4]))
	boxType = string(header[4:8])

	if size == 0 {
		// Size 0 means "box extends to end of file" — we can't handle that for scanning.
		return 0, 0, boxType, nil
	}

	// size == 1 means 64-bit extended size follows, but that's rare for these boxes.
	// Skip it for detection purposes.

	return pos + mp4BoxHeaderSize, size, boxType, nil
}
