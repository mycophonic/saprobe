package alac

import (
	"encoding/binary"
	"fmt"
	"io"

	mp4 "github.com/abema/go-mp4"

	"github.com/mycophonic/saprobe"
)

// Decode reads an M4A/MP4 stream and decodes the first ALAC audio track
// to interleaved little-endian signed PCM bytes.
func Decode(reader io.ReadSeeker) ([]byte, saprobe.PCMFormat, error) {
	cookie, samples, err := findALACTrack(reader)
	if err != nil {
		return nil, saprobe.PCMFormat{}, err
	}

	config, err := ParseConfig(cookie)
	if err != nil {
		return nil, saprobe.PCMFormat{}, fmt.Errorf("parsing ALAC config: %w", err)
	}

	dec, err := NewDecoder(config)
	if err != nil {
		return nil, saprobe.PCMFormat{}, err
	}

	format := dec.Format()
	bps := format.BitDepth.BytesPerSample()
	frameBytes := int(config.FrameLength) * int(config.NumChannels) * bps

	// Pre-allocate the full output buffer. Last frame may be shorter,
	// so we allocate for max and truncate at the end.
	pcm := make([]byte, len(samples)*frameBytes)
	offset := 0

	var packetBuf []byte

	for idx, sample := range samples {
		if int(sample.size) > len(packetBuf) {
			packetBuf = make([]byte, sample.size)
		}

		packet := packetBuf[:sample.size]

		if _, err := reader.Seek(int64(sample.offset), io.SeekStart); err != nil {
			return nil, saprobe.PCMFormat{}, fmt.Errorf(
				"seeking to sample %d at offset %d: %w",
				idx,
				sample.offset,
				err,
			)
		}

		if _, err := io.ReadFull(reader, packet); err != nil {
			return nil, saprobe.PCMFormat{}, fmt.Errorf("reading sample %d: %w", idx, err)
		}

		n, err := dec.decodePacketInto(packet, pcm[offset:])
		if err != nil {
			return nil, saprobe.PCMFormat{}, fmt.Errorf("decoding packet %d: %w", idx, err)
		}

		offset += n
	}

	return pcm[:offset], format, nil
}

// sampleInfo holds the byte offset and size of a single encoded ALAC packet
// within the MP4 container.
type sampleInfo struct {
	offset uint64
	size   uint32
}

// findALACTrack walks the MP4 box tree to locate the first track containing
// an ALAC sample entry. It returns the magic cookie and a flat sample table.
func findALACTrack(reader io.ReadSeeker) ([]byte, []sampleInfo, error) {
	stbls, err := mp4.ExtractBox(reader, nil, mp4.BoxPath{
		mp4.BoxTypeMoov(), mp4.BoxTypeTrak(), mp4.BoxTypeMdia(),
		mp4.BoxTypeMinf(), mp4.BoxTypeStbl(),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("reading container structure: %w", err)
	}

	for _, stbl := range stbls {
		cookie, err := extractCookie(reader, stbl)
		if err != nil {
			continue // not an ALAC track
		}

		samples, err := buildSampleTable(reader, stbl)
		if err != nil {
			return nil, nil, fmt.Errorf("building sample table: %w", err)
		}

		return cookie, samples, nil
	}

	return nil, nil, errNoALACTrack
}

const (
	alacFourCC            = "alac"
	sampleEntryHeaderSize = 8  // box header: size(4) + type(4)
	sampleEntryBaseSize   = 28 // standard AudioSampleEntry fields
	sampleEntryV1Extra    = 16 // QuickTime version 1 extra fields
	stsdPayloadHeader     = 8  // version(1) + flags(3) + entryCount(4)
)

// extractCookie reads the stsd box from stbl, finds an 'alac' sample entry,
// and extracts the raw magic cookie (ALACSpecificConfig, possibly wrapped in
// 'frma'+'alac' atoms which ParseConfig handles).
func extractCookie(reader io.ReadSeeker, stbl *mp4.BoxInfo) ([]byte, error) {
	stsds, err := mp4.ExtractBox(reader, stbl, mp4.BoxPath{mp4.BoxTypeStsd()})
	if err != nil || len(stsds) == 0 {
		return nil, errNoALACTrack
	}

	stsd := stsds[0]
	payloadSize := int(stsd.Size - stsd.HeaderSize)
	data := make([]byte, payloadSize)

	if _, err := reader.Seek(int64(stsd.Offset+stsd.HeaderSize), io.SeekStart); err != nil {
		return nil, fmt.Errorf("seeking to stsd payload: %w", err)
	}

	if _, err := io.ReadFull(reader, data); err != nil {
		return nil, fmt.Errorf("reading stsd payload: %w", err)
	}

	if len(data) < stsdPayloadHeader {
		return nil, errNoALACTrack
	}

	entryCount := binary.BigEndian.Uint32(data[4:8])
	pos := stsdPayloadHeader

	for range entryCount {
		if pos+sampleEntryHeaderSize > len(data) {
			break
		}

		entrySize := int(binary.BigEndian.Uint32(data[pos : pos+4]))
		if entrySize < sampleEntryHeaderSize+sampleEntryBaseSize || pos+entrySize > len(data) {
			pos += entrySize

			continue
		}

		if string(data[pos+4:pos+8]) != alacFourCC {
			pos += entrySize

			continue
		}

		// Found ALAC sample entry. Determine cookie start from QT version field.
		// Layout after 8-byte box header: reserved(6) + dataRefIdx(2) + version(2) + ...
		// Version is at offset 8 within the payload (i.e., pos + headerSize + 8).
		version := binary.BigEndian.Uint16(data[pos+sampleEntryHeaderSize+8 : pos+sampleEntryHeaderSize+10])

		skip := sampleEntryHeaderSize + sampleEntryBaseSize
		if version == 1 {
			skip += sampleEntryV1Extra
		}

		cookieStart := pos + skip
		cookieEnd := pos + entrySize

		if cookieStart >= cookieEnd {
			return nil, errInvalidCookie
		}

		return data[cookieStart:cookieEnd], nil
	}

	return nil, errNoALACTrack
}

// buildSampleTable constructs a flat list of sample offsets and sizes from
// the stco/co64, stsc, and stsz boxes within the given stbl box.
func buildSampleTable(reader io.ReadSeeker, stbl *mp4.BoxInfo) ([]sampleInfo, error) {
	chunkOffsets, err := readChunkOffsets(reader, stbl)
	if err != nil {
		return nil, err
	}

	stscEntries, err := readStsc(reader, stbl)
	if err != nil {
		return nil, err
	}

	entrySizes, constantSize, sampleCount, err := readStsz(reader, stbl)
	if err != nil {
		return nil, err
	}

	samples := make([]sampleInfo, 0, sampleCount)
	sampleIdx := 0

	for chunkIdx := range chunkOffsets {
		spc := lookupSamplesPerChunk(stscEntries, uint32(chunkIdx+1)) // stsc uses 1-based chunk numbers
		offset := chunkOffsets[chunkIdx]

		for s := uint32(0); s < spc && sampleIdx < int(sampleCount); s++ {
			var size uint32
			if constantSize != 0 {
				size = constantSize
			} else {
				size = entrySizes[sampleIdx]
			}

			samples = append(samples, sampleInfo{offset: offset, size: size})
			offset += uint64(size)
			sampleIdx++
		}
	}

	return samples, nil
}

func readChunkOffsets(reader io.ReadSeeker, stbl *mp4.BoxInfo) ([]uint64, error) {
	// Try 32-bit stco first.
	if boxes, err := mp4.ExtractBoxWithPayload(reader, stbl,
		mp4.BoxPath{mp4.BoxTypeStco()}); err == nil && len(boxes) > 0 {
		if stco, ok := boxes[0].Payload.(*mp4.Stco); ok {
			offsets := make([]uint64, len(stco.ChunkOffset))
			for i, off := range stco.ChunkOffset {
				offsets[i] = uint64(off)
			}

			return offsets, nil
		}
	}

	// Fall back to 64-bit co64.
	boxes, err := mp4.ExtractBoxWithPayload(reader, stbl, mp4.BoxPath{mp4.BoxTypeCo64()})
	if err != nil || len(boxes) == 0 {
		return nil, errNoChunkOffset
	}

	co64, ok := boxes[0].Payload.(*mp4.Co64)
	if !ok {
		return nil, errInvalidCo64
	}

	return co64.ChunkOffset, nil
}

func readStsc(reader io.ReadSeeker, stbl *mp4.BoxInfo) ([]mp4.StscEntry, error) {
	boxes, err := mp4.ExtractBoxWithPayload(reader, stbl, mp4.BoxPath{mp4.BoxTypeStsc()})
	if err != nil || len(boxes) == 0 {
		return nil, errNoStsc
	}

	stsc, ok := boxes[0].Payload.(*mp4.Stsc)
	if !ok {
		return nil, errInvalidStsc
	}

	return stsc.Entries, nil
}

//revive:disable:function-result-limit,confusing-results
func readStsz(reader io.ReadSeeker, stbl *mp4.BoxInfo) ([]uint32, uint32, uint32, error) {
	boxes, err := mp4.ExtractBoxWithPayload(reader, stbl, mp4.BoxPath{mp4.BoxTypeStsz()})
	if err != nil || len(boxes) == 0 {
		return nil, 0, 0, errNoStsz
	}

	stsz, ok := boxes[0].Payload.(*mp4.Stsz)
	if !ok {
		return nil, 0, 0, errInvalidStsz
	}

	return stsz.EntrySize, stsz.SampleSize, stsz.SampleCount, nil
}

// lookupSamplesPerChunk finds the samples-per-chunk count for a 1-based
// chunk number from the stsc run-length table.
func lookupSamplesPerChunk(entries []mp4.StscEntry, chunkNumber uint32) uint32 {
	var spc uint32

	for _, e := range entries {
		if e.FirstChunk > chunkNumber {
			break
		}

		spc = e.SamplesPerChunk
	}

	return spc
}
