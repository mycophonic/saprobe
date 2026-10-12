/*
   Copyright Mycophonic.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package detect

import (
	"encoding/binary"
	"errors"
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
	// DSF is a DSD Stream File container.
	DSF
	// DFF is a DSDIFF (DSD Interchange File Format) container.
	DFF
	// AIFF is Audio Interchange File Format (Apple/SGI uncompressed PCM).
	AIFF
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
	case DSF:
		return "DSF"
	case DFF:
		return "DFF"
	case AIFF:
		return "AIFF"
	}

	return "unknown"
}

// headerSize is the minimum number of bytes needed to identify any supported codec.
// FLAC: 4 bytes at offset 0 ("fLaC").
// M4A:  4 bytes at offset 4 ("ftyp" in an M4A/MP4 container) — then deep probe for ALAC vs AAC.
// MP3:  3 bytes at offset 0 ("ID3") or 2-byte MPEG sync word (0xFF 0xE0 mask).
// OGG:  4 bytes at offset 0 ("OggS").
// WAV:  "RIFF" at offset 0 and "WAVE" at offset 8.
// DSF:  "DSD " at offset 0.
// DFF:  "FRM8" at offset 0 and "DSD " at offset 12 (16 bytes total).
// AIFF: "FORM" at offset 0 and "AIFF" or "AIFC" at offset 8.
const (
	headerSize = 16

	// mpegSyncByte is the first byte of an MPEG audio frame sync word.
	mpegSyncByte = 0xFF
	// mpegSyncMask masks the upper 3 bits of the second byte in the sync word.
	mpegSyncMask = 0xE0

	// mp4BoxHeaderSize is the size of a standard MP4 box header (size + type).
	mp4BoxHeaderSize = 8

	// Container magic strings used in format detection.
	magicDSD  = "DSD "
	magicAIFF = "AIFF"
	magicAIFC = "AIFC"

	// id3v2 synchsafe integer bit widths (4 × 7-bit groups in bytes 6-9).
	id3v2Shift3 = 21
	id3v2Shift2 = 14
	id3v2Shift1 = 7
)

// errUnrecognizedSignature is returned when the audio data after an ID3v2 tag
// does not match any supported codec signature.
var errUnrecognizedSignature = errors.New("unrecognized signature after ID3v2")

// Identify reads the header from rs and returns the detected audio codec.
// The reader position is reset to the start before returning.
//
//nolint:gocognit // one test per signature the detector knows: the branching is the list of formats
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

	// DSF: first four bytes are "DSD " (DSD Stream File).
	if string(header[:4]) == magicDSD {
		return DSF, nil
	}

	// DFF/DSDIFF: "FRM8" at offset 0 with form type "DSD " at offset 12.
	if string(header[:4]) == "FRM8" && string(header[12:16]) == magicDSD {
		return DFF, nil
	}

	// AIFF/AIFF-C: IFF container with "FORM" at offset 0 and "AIFF" or "AIFC" at offset 8.
	if string(header[:4]) == "FORM" {
		formType := string(header[8:12])
		if formType == magicAIFF || formType == magicAIFC {
			return AIFF, nil
		}
	}

	// M4A/MP4 container: bytes 4-7 are "ftyp". Probe deeper to distinguish ALAC from AAC.
	if string(header[4:8]) == "ftyp" {
		return probeM4ACodec(reader)
	}

	// ID3v2 tag: skip it and check the actual audio data underneath.
	// FLAC files occasionally have prepended ID3v2 tags; defaulting to MP3
	// without probing past the tag misidentifies them.
	if string(header[:3]) == "ID3" {
		codec, err := probeAfterID3v2(reader, header)
		if err == nil {
			return codec, nil
		}

		// Could not probe past the ID3v2 tag — assume MP3 (most common case).
		return MP3, nil
	}

	// MP3: MPEG frame sync word (11 set bits).
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
	moov, err := findBox(reader, 0, -1, "moov")
	if err != nil || moov.contentSize == 0 {
		return ALAC, nil // fallback: assume ALAC if we can't probe
	}

	// moov → trak → mdia → minf → stbl → stsd
	codec, found := probeM4ATraks(reader, moov.contentOffset, moov.contentSize)
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
		boxHeader, err := readBoxHeader(reader, pos)
		if err != nil || boxHeader.totalSize == 0 {
			break
		}

		if boxHeader.boxType == "trak" {
			contentSize := boxHeader.totalSize - mp4BoxHeaderSize
			if codec, matched := probeM4ATrak(reader, boxHeader.contentOffset, contentSize); matched {
				return codec, true
			}
		}

		pos += boxHeader.totalSize
	}

	return Unknown, false
}

// probeM4ATrak probes a single trak box for an audio sample entry.
func probeM4ATrak(reader io.ReadSeeker, trakOffset, trakSize int64) (Codec, bool) {
	// trak → mdia
	mdia, err := findBox(reader, trakOffset, trakSize, "mdia")
	if err != nil || mdia.contentSize == 0 {
		return Unknown, false
	}

	// mdia → minf
	minf, err := findBox(reader, mdia.contentOffset, mdia.contentSize, "minf")
	if err != nil || minf.contentSize == 0 {
		return Unknown, false
	}

	// minf → stbl
	stbl, err := findBox(reader, minf.contentOffset, minf.contentSize, "stbl")
	if err != nil || stbl.contentSize == 0 {
		return Unknown, false
	}

	// stbl → stsd
	stsd, err := findBox(reader, stbl.contentOffset, stbl.contentSize, "stsd")
	if err != nil || stsd.contentSize == 0 {
		return Unknown, false
	}

	return probeStsd(reader, stsd.contentOffset, stsd.contentSize)
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

// mp4BoxResult holds the result of a box search.
type mp4BoxResult struct {
	contentOffset int64
	contentSize   int64
}

// maxScanOffset is the upper bound when scanning top-level boxes without a known parent size.
const maxScanOffset = 1<<62 - 1

// findBox searches for a box with the given type among direct children
// starting at parentContentOffset within parentSize bytes.
// Returns the content offset (past the box header) and content size of the found box.
func findBox(reader io.ReadSeeker, parentContentOffset, parentSize int64, target string) (mp4BoxResult, error) {
	end := parentContentOffset + parentSize
	if parentSize < 0 {
		// Scan until read failure (used for top-level).
		end = maxScanOffset
	}

	pos := parentContentOffset

	for pos < end {
		boxHeader, err := readBoxHeader(reader, pos)
		if err != nil || boxHeader.totalSize == 0 {
			return mp4BoxResult{}, err
		}

		if boxHeader.boxType == target {
			return mp4BoxResult{
				contentOffset: boxHeader.contentOffset,
				contentSize:   boxHeader.totalSize - mp4BoxHeaderSize,
			}, nil
		}

		pos = boxHeader.contentOffset - mp4BoxHeaderSize + boxHeader.totalSize
	}

	return mp4BoxResult{}, nil
}

// mp4BoxHeader holds a parsed MP4 box header.
type mp4BoxHeader struct {
	contentOffset int64
	totalSize     int64
	boxType       string
}

// readBoxHeader reads an MP4 box header at the given position.
func readBoxHeader(reader io.ReadSeeker, pos int64) (mp4BoxHeader, error) {
	if _, err := reader.Seek(pos, io.SeekStart); err != nil {
		return mp4BoxHeader{}, fmt.Errorf("seeking to box at %d: %w", pos, err)
	}

	var header [mp4BoxHeaderSize]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return mp4BoxHeader{}, fmt.Errorf("reading box header: %w", err)
	}

	size := int64(binary.BigEndian.Uint32(header[0:4]))
	boxType := string(header[4:8])

	if size == 0 {
		// Size 0 means "box extends to end of file" — we can't handle that for scanning.
		return mp4BoxHeader{boxType: boxType}, nil
	}

	// size == 1 means 64-bit extended size follows, but that's rare for these boxes.
	// Skip it for detection purposes.

	return mp4BoxHeader{
		contentOffset: pos + mp4BoxHeaderSize,
		totalSize:     size,
		boxType:       boxType,
	}, nil
}

// id3v2HeaderSize is the fixed size of the ID3v2 header (before the tag body).
const id3v2HeaderSize = 10

// probeAfterID3v2 skips past a prepended ID3v2 tag and identifies the codec
// from the actual audio data signature. The reader is always reset to position 0
// before returning (success or failure).
//
// ID3v2 layout: "ID3" (3) + version (2) + flags (1) + size (4 synchsafe bytes).
// The size field encodes the tag body length (excluding the 10-byte header).
func probeAfterID3v2(reader io.ReadSeeker, header [headerSize]byte) (Codec, error) {
	defer func() {
		_, _ = reader.Seek(0, io.SeekStart)
	}()

	// Parse synchsafe integer (4 × 7-bit groups) from bytes 6-9.
	tagSize := int64(header[6])<<id3v2Shift3 | int64(header[7])<<id3v2Shift2 |
		int64(header[8])<<id3v2Shift1 | int64(header[9])

	audioStart := id3v2HeaderSize + tagSize

	// Seek to the start of actual audio data.
	if _, err := reader.Seek(audioStart, io.SeekStart); err != nil {
		return Unknown, fmt.Errorf("seeking past ID3v2: %w", err)
	}

	var sig [4]byte
	if _, err := io.ReadFull(reader, sig[:]); err != nil {
		return Unknown, fmt.Errorf("reading post-ID3v2 signature: %w", err)
	}

	// Check all supported codec signatures against the post-ID3v2 data.
	// ID3v2 tags are most common on MP3, but can appear on FLAC, AAC/ADTS,
	// WAV, and others.
	switch {
	case string(sig[:]) == "fLaC":
		return FLAC, nil
	case string(sig[:]) == "OggS":
		return Vorbis, nil
	case string(sig[:]) == "RIFF":
		// Need 12 bytes to confirm WAVE — read 8 more.
		var rest [8]byte
		if _, err := io.ReadFull(reader, rest[:]); err == nil && string(rest[4:8]) == "WAVE" {
			return WAV, nil
		}
	case string(sig[:]) == magicDSD:
		return DSF, nil
	case string(sig[:]) == "FORM":
		// AIFF/AIFF-C: need 8 more bytes to read the form type at offset 8.
		var rest [8]byte
		if _, err := io.ReadFull(reader, rest[:]); err == nil {
			formType := string(rest[4:8])
			if formType == magicAIFF || formType == magicAIFC {
				return AIFF, nil
			}
		}

	case sig[0] == mpegSyncByte && sig[1]&mpegSyncMask == mpegSyncMask:
		return MP3, nil
	default:
	}

	// Unrecognized — fall back to caller's default (MP3).
	return Unknown, fmt.Errorf("%w: %x", errUnrecognizedSignature, sig)
}
