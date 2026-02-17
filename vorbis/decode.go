package vorbis

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"

	"github.com/jfreymuth/oggvorbis"

	"github.com/mycophonic/saprobe"
)

// Decode reads an Ogg Vorbis stream and decodes it to interleaved little-endian signed 16-bit PCM bytes.
func Decode(rs io.ReadSeeker) ([]byte, saprobe.PCMFormat, error) {
	samples, format, err := oggvorbis.ReadAll(rs)
	if err != nil {
		return nil, saprobe.PCMFormat{}, fmt.Errorf("decoding vorbis: %w", err)
	}

	pcmFormat := saprobe.PCMFormat{
		SampleRate: format.SampleRate,
		BitDepth:   saprobe.Depth16,
		Channels:   uint(format.Channels), //nolint:gosec // channel count is always small positive
	}

	buf := make([]byte, len(samples)*2)

	for i, s := range samples {
		scaled := math.Round(float64(s) * math.MaxInt16)
		scaled = max(math.MinInt16, min(math.MaxInt16, scaled))

		binary.LittleEndian.PutUint16(buf[i*2:], uint16(int16(scaled))) //nolint:gosec // clamped to int16 range
	}

	return buf, pcmFormat, nil
}
