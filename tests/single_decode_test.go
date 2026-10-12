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

package tests_test

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mycophonic/agar/pkg/agar"
	alac "github.com/mycophonic/saprobe-alac"
	flac "github.com/mycophonic/saprobe-flac"
	mp3 "github.com/mycophonic/saprobe-mp3"

	"github.com/mycophonic/saprobe"
	"github.com/mycophonic/saprobe/aiff"
	"github.com/mycophonic/saprobe/detect"
	"github.com/mycophonic/saprobe/vorbis"
	"github.com/mycophonic/saprobe/wav"
)

//nolint:gochecknoglobals // a test flag, bound once at init
var singleFile = flag.String("single-file", "", "single audio file for detailed decode comparison")

// TestSingleDecode performs detailed comparison of a single file between saprobe and ffmpeg.
//
// Run: go test ./tests/ -v -run TestSingleDecode -single-file /path/to/file.m4a.
func TestSingleDecode(t *testing.T) {
	t.Parallel()

	if *singleFile == "" {
		t.Skip("no -single-file flag provided")
	}

	path := *singleFile
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not found: %s", path)
	}

	t.Log("=== SINGLE FILE DECODE ANALYSIS ===")
	t.Logf("File: %s", path)

	// Step 1: Probe file properties with ffprobe
	props := probeFileProperties(t, path)
	t.Log("")
	t.Log("=== FILE PROPERTIES (ffprobe) ===")
	t.Logf("Codec:       %s", props.codec)
	t.Logf("Sample Rate: %d Hz", props.sampleRate)
	t.Logf("Channels:    %d", props.channels)
	t.Logf("Bit Depth:   %d", props.bitDepth)
	t.Logf("Duration:    %.3f seconds", props.duration)
	t.Logf("Total Samples: %d (per channel)", props.totalSamples)

	// Step 2: Determine PCM format
	decodeBitDepth := decodeBitDepthForFile(props.bitDepth, filepath.Ext(path))
	pcmFormat := agar.RawPCMFormat(decodeBitDepth)
	bytesPerSample := bytesPerSampleFromFormat(pcmFormat)
	t.Logf("PCM Format:  %s (%d bytes/sample)", pcmFormat, bytesPerSample)

	// Step 3: Decode with ffmpeg
	t.Log("")
	t.Log("=== DECODING WITH FFMPEG ===")
	ffmpegPCM := agar.FFmpegDecode(t, agar.FFmpegDecodeOptions{Src: path, BitDepth: decodeBitDepth})
	t.Logf("FFmpeg output: %d bytes", len(ffmpegPCM))

	// Step 4: Decode with saprobe
	t.Log("")
	t.Log("=== DECODING WITH SAPROBE ===")

	saprobePCM, saprobeFormat, err := decodeWithSaprobe(t, path)
	if err != nil {
		t.Fatalf("saprobe decode failed: %v", err)
	}

	t.Logf("Saprobe output: %d bytes", len(saprobePCM))
	t.Logf("Saprobe format: %d Hz, %d-bit, %d channels",
		saprobeFormat.SampleRate, saprobeFormat.BitDepth, saprobeFormat.Channels)

	// Step 5: Compare
	t.Log("")
	t.Log("=== COMPARISON ===")

	if len(saprobePCM) != len(ffmpegPCM) {
		t.Logf("SIZE MISMATCH: saprobe=%d, ffmpeg=%d (diff=%d bytes)",
			len(saprobePCM), len(ffmpegPCM), len(saprobePCM)-len(ffmpegPCM))

		sampleSize := bytesPerSample * props.channels
		saprobeSamples := len(saprobePCM) / sampleSize
		ffmpegSamples := len(ffmpegPCM) / sampleSize
		t.Logf("Sample count: saprobe=%d, ffmpeg=%d (diff=%d samples)",
			saprobeSamples, ffmpegSamples, saprobeSamples-ffmpegSamples)
	}

	if bytes.Equal(saprobePCM, ffmpegPCM) {
		t.Log("PERFECT MATCH!")

		return
	}

	// Find and analyze differences
	analyzeDiscrepancies(t, saprobePCM, ffmpegPCM, props.channels, bytesPerSample)
}

type fileProperties struct {
	codec        string
	sampleRate   int
	channels     int
	bitDepth     int
	duration     float64
	totalSamples int
}

func probeFileProperties(t *testing.T, path string) fileProperties {
	t.Helper()

	result, err := agar.FFProbe(path)
	if err != nil {
		t.Fatalf("ffprobe failed: %v", err)
	}

	stream, streamErr := result.AudioStream()
	if streamErr != nil {
		t.Fatalf("ffprobe: no audio stream in %s", path)
	}

	return fileProperties{
		codec:        stream.CodecName,
		sampleRate:   stream.SampleRateInt(),
		channels:     stream.Channels,
		bitDepth:     stream.BitDepth(),
		duration:     stream.DurationFloat(),
		totalSamples: int(stream.DurationFloat() * float64(stream.SampleRateInt())),
	}
}

// decodeBitDepthForFile returns the output bit depth for ffmpeg decoding.
// Lossy codecs always decode to 16-bit; lossless uses the source bit depth.
func decodeBitDepthForFile(bitDepth int, ext string) int {
	ext = strings.ToLower(ext)
	if ext == ".mp3" || ext == ".ogg" {
		return 16
	}

	return bitDepth
}

func bytesPerSampleFromFormat(pcmFormat string) int {
	switch pcmFormat {
	case "s32le":
		return 4
	case "s24le":
		return 3
	default:
		return 2
	}
}

func decodeWithSaprobe(t *testing.T, path string) ([]byte, saprobe.PCMFormat, error) {
	t.Helper()

	file, err := os.Open(path)
	if err != nil {
		return nil, saprobe.PCMFormat{}, err
	}
	defer file.Close()

	codec, err := detect.Identify(file)
	if err != nil {
		return nil, saprobe.PCMFormat{}, fmt.Errorf("detecting codec: %w", err)
	}

	t.Logf("Detected codec: %s", codec)

	switch codec { //revive:disable-line:identical-switch-branches
	case detect.FLAC:
		dec, flacErr := flac.NewDecoder(file)
		if flacErr != nil {
			return nil, saprobe.PCMFormat{}, flacErr
		}
		defer dec.Close()

		pcm, readErr := io.ReadAll(dec)
		if readErr != nil {
			return nil, saprobe.PCMFormat{}, readErr
		}

		ff := dec.Format()

		return pcm, saprobe.PCMFormat{
			SampleRate: ff.SampleRate,
			BitDepth:   saprobe.BitDepth(ff.BitDepth),
			Channels:   ff.Channels,
		}, nil
	case detect.MP3:
		dec, mp3Err := mp3.NewDecoder(file)
		if mp3Err != nil {
			return nil, saprobe.PCMFormat{}, mp3Err
		}

		pcm, readErr := io.ReadAll(dec)
		if readErr != nil {
			return nil, saprobe.PCMFormat{}, readErr
		}

		mf := dec.Format()

		return pcm, saprobe.PCMFormat{
			SampleRate: mf.SampleRate,
			BitDepth:   saprobe.BitDepth(mf.BitDepth),
			Channels:   uint(mf.Channels),
		}, nil
	case detect.Vorbis:
		dec, vorbisErr := vorbis.NewDecoder(file)
		if vorbisErr != nil {
			return nil, saprobe.PCMFormat{}, vorbisErr
		}

		pcm, readErr := io.ReadAll(dec)
		if readErr != nil {
			return nil, saprobe.PCMFormat{}, readErr
		}

		return pcm, dec.Format(), nil
	case detect.ALAC:
		dec, alacErr := alac.NewDecoder(file)
		if alacErr != nil {
			return nil, saprobe.PCMFormat{}, alacErr
		}

		pcm, readErr := io.ReadAll(dec)
		if readErr != nil {
			return nil, saprobe.PCMFormat{}, readErr
		}

		af := dec.Format()

		return pcm, saprobe.PCMFormat{
			SampleRate: af.SampleRate,
			BitDepth:   saprobe.BitDepth(af.BitDepth),
			Channels:   uint(af.Channels),
		}, nil
	case detect.WAV:
		dec, wavErr := wav.NewDecoder(file)
		if wavErr != nil {
			return nil, saprobe.PCMFormat{}, wavErr
		}

		pcm, readErr := io.ReadAll(dec)
		if readErr != nil {
			return nil, saprobe.PCMFormat{}, readErr
		}

		return pcm, dec.Format(), nil
	case detect.AIFF:
		dec, aiffErr := aiff.NewDecoder(file)
		if aiffErr != nil {
			return nil, saprobe.PCMFormat{}, aiffErr
		}

		pcm, readErr := io.ReadAll(dec)
		if readErr != nil {
			return nil, saprobe.PCMFormat{}, readErr
		}

		return pcm, dec.Format(), nil
	case detect.AAC, detect.DSF, detect.DFF:
		return nil, saprobe.PCMFormat{}, fmt.Errorf("unsupported codec: %s", codec)
	case detect.Unknown:
		return nil, saprobe.PCMFormat{}, fmt.Errorf("unsupported codec: %s", codec)
	default:
		return nil, saprobe.PCMFormat{}, fmt.Errorf("unsupported codec: %s", codec)
	}
}

func analyzeDiscrepancies(t *testing.T, saprobeData, ffmpegData []byte, channels, bytesPerSample int) {
	t.Helper()

	minLen := min(len(saprobeData), len(ffmpegData))
	frameSize := channels * bytesPerSample

	// Find first difference
	firstDiffByte := -1

	for i := range minLen {
		if saprobeData[i] != ffmpegData[i] {
			firstDiffByte = i

			break
		}
	}

	if firstDiffByte == -1 && len(saprobeData) != len(ffmpegData) {
		t.Logf("Data identical up to byte %d, then size differs", minLen)

		return
	}

	if firstDiffByte == -1 {
		t.Log("No differences found (unexpected)")

		return
	}

	// Calculate sample position
	sampleIndex := firstDiffByte / frameSize
	channelIndex := (firstDiffByte % frameSize) / bytesPerSample
	byteInSample := firstDiffByte % bytesPerSample

	t.Log("FIRST DIFFERENCE:")
	t.Logf("  Byte offset:    %d (0x%X)", firstDiffByte, firstDiffByte)
	t.Logf("  Sample index:   %d", sampleIndex)
	t.Logf("  Channel:        %d", channelIndex)
	t.Logf("  Byte in sample: %d", byteInSample)
	t.Logf("  Saprobe byte:   0x%02X", saprobeData[firstDiffByte])
	t.Logf("  FFmpeg byte:    0x%02X", ffmpegData[firstDiffByte])

	// Show context around first difference (aligned to sample boundaries)
	contextSamples := 5
	startSample := max(0, sampleIndex-contextSamples)
	endSample := min(minLen/frameSize, sampleIndex+contextSamples+1)

	t.Log("")
	t.Logf("CONTEXT (samples %d to %d):", startSample, endSample-1)

	for s := startSample; s < endSample; s++ {
		marker := "  "
		if s == sampleIndex {
			marker = ">>"
		}

		saprobeSamples := extractSampleValues(saprobeData, s, channels, bytesPerSample)
		ffmpegSamples := extractSampleValues(ffmpegData, s, channels, bytesPerSample)

		t.Logf("%s Sample %6d: saprobe=%v  ffmpeg=%v", marker, s, saprobeSamples, ffmpegSamples)
	}

	// Count total differences
	diffCount := 0
	diffSamples := make(map[int]bool)

	for i := range minLen {
		if saprobeData[i] != ffmpegData[i] {
			diffCount++
			diffSamples[i/frameSize] = true
		}
	}

	t.Log("")
	t.Log("STATISTICS:")
	t.Logf("  Total differing bytes:   %d", diffCount)
	t.Logf("  Total differing samples: %d", len(diffSamples))
	t.Logf("  Percentage of samples:   %.2f%%", float64(len(diffSamples))*100/float64(minLen/frameSize))

	// Analyze difference patterns
	analyzeDifferencePatterns(t, saprobeData, ffmpegData, channels, bytesPerSample, minLen)
}

func extractSampleValues(data []byte, sampleIndex, channels, bytesPerSample int) []int32 {
	values := make([]int32, channels)
	frameSize := channels * bytesPerSample
	offset := sampleIndex * frameSize

	if offset+frameSize > len(data) {
		return values
	}

	for ch := range channels {
		chOffset := offset + ch*bytesPerSample
		switch bytesPerSample {
		case 2:
			values[ch] = int32(int16(binary.LittleEndian.Uint16(data[chOffset:])))
		case 3:
			// Sign extend 24-bit
			val := int32(data[chOffset]) | int32(data[chOffset+1])<<8 | int32(data[chOffset+2])<<16
			if val&0x800000 != 0 {
				val |= -0x1000000 // sign extend
			}

			values[ch] = val
		case 4:
			values[ch] = int32(binary.LittleEndian.Uint32(data[chOffset:]))

		default:
		}
	}

	return values
}

func analyzeDifferencePatterns(t *testing.T, saprobeData, ffmpegData []byte, channels, bytesPerSample, minLen int) {
	t.Helper()

	frameSize := channels * bytesPerSample

	// Check if differences are concentrated at start, middle, or end
	thirds := minLen / 3
	diffFirst, diffMiddle, diffLast := 0, 0, 0

	for i := range minLen {
		if saprobeData[i] != ffmpegData[i] {
			switch {
			case i < thirds:
				diffFirst++
			case i < 2*thirds:
				diffMiddle++
			default:
				diffLast++
			}
		}
	}

	t.Log("")
	t.Log("DIFFERENCE DISTRIBUTION:")
	t.Logf("  First third:  %d bytes", diffFirst)
	t.Logf("  Middle third: %d bytes", diffMiddle)
	t.Logf("  Last third:   %d bytes", diffLast)

	// Check for systematic offsets (e.g., all values off by same amount)
	offsetCounts := make(map[int32]int)
	samplesDiffering := 0

	for s := range minLen / frameSize {
		saprobeSamples := extractSampleValues(saprobeData, s, channels, bytesPerSample)
		ffmpegSamples := extractSampleValues(ffmpegData, s, channels, bytesPerSample)

		for ch := range channels {
			if saprobeSamples[ch] != ffmpegSamples[ch] {
				diff := saprobeSamples[ch] - ffmpegSamples[ch]
				offsetCounts[diff]++
				samplesDiffering++
			}
		}
	}

	if samplesDiffering > 0 {
		t.Log("")
		t.Log("SAMPLE VALUE DIFFERENCES (saprobe - ffmpeg):")

		// Show top 10 most common differences
		type diffEntry struct {
			diff  int32
			count int
		}

		var diffs []diffEntry
		for d, c := range offsetCounts {
			diffs = append(diffs, diffEntry{d, c})
		}

		// Sort by count (simple bubble sort, good enough for small sets)
		for i := range len(diffs) - 1 {
			for j := i + 1; j < len(diffs); j++ {
				if diffs[j].count > diffs[i].count {
					diffs[i], diffs[j] = diffs[j], diffs[i]
				}
			}
		}

		shown := min(10, len(diffs))
		for i := range shown {
			t.Logf("  Offset %+d: %d occurrences (%.1f%%)",
				diffs[i].diff, diffs[i].count,
				float64(diffs[i].count)*100/float64(samplesDiffering))
		}

		if len(diffs) > 10 {
			t.Logf("  ... and %d more unique differences", len(diffs)-10)
		}
	}
}
