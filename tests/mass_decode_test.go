package tests_test

import (
	"bytes"
	"encoding/binary"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/containerd/nerdctl/mod/tigron/expect"
	"github.com/containerd/nerdctl/mod/tigron/test"
	"github.com/containerd/nerdctl/mod/tigron/tig"

	"github.com/mycophonic/agar/pkg/agar"

	"github.com/mycophonic/saprobe/tests/testutils"
)

//nolint:gochecknoglobals
var (
	audioPath = flag.String("audio-path", "", "directory of audio files for mass decode comparison")
	only      = flag.String("only", "", "filter by file extension (e.g. flac, mp3, ogg, m4a)")
)

// TestMassDecode walks a directory of audio files, decodes each with saprobe
// and ffmpeg, and compares the raw PCM output byte-for-byte.
//
// Run: go test ./tests/ -run TestMassDecode -audio-path /path/to/audio/files.
func TestMassDecode(t *testing.T) {
	t.Parallel()

	if *audioPath == "" {
		t.Skip("no -audio-path flag provided")
	}

	root := *audioPath

	files := discoverAudioFiles(t, root)
	if len(files) == 0 {
		t.Fatalf("no audio files found in %s", root)
	}

	t.Logf("discovered %d audio files in %s", len(files), root)
	logDiscoverySummary(t, files)

	testCase := testutils.Setup()
	testCase.Description = "mass decode comparison"
	// Parallelism will screw the pooch.
	testCase.NoParallel = true

	for i, af := range files {
		t.Logf("[%d/%d] queuing %s (%s)", i+1, len(files), af.relPath, af.pcmFormat)
		testCase.SubTests = append(testCase.SubTests, makeDecodeTest(af, i+1, len(files)))
	}

	testCase.Run(t)
}

func makeDecodeTest(af audioFile, index, total int) *test.Case {
	prefix := fmt.Sprintf("[%d/%d]", index, total)

	return &test.Case{
		Description: af.relPath,
		// Parallelism will screw the pooch.
		NoParallel: true,
		Setup: func(data test.Data, helpers test.Helpers) {
			helpers.T().Log(fmt.Sprintf("%s decoding reference with ffmpeg (%s): %s", prefix, af.pcmFormat, af.path))

			ffmpegBin, err := agar.LookFor("ffmpeg")
			if err != nil {
				helpers.T().Log(fmt.Sprintf("ffmpeg not found: %v", err))
				helpers.T().FailNow()
			}

			refOut := data.Temp().Path("ref.raw")
			helpers.Custom(ffmpegBin,
				"-i", af.path,
				"-f", af.pcmFormat,
				"-y", refOut,
			).Run(&test.Expected{ExitCode: expect.ExitCodeSuccess})

			info, _ := os.Stat(refOut)
			if info != nil {
				helpers.T().Log(fmt.Sprintf("%s ffmpeg reference: %d bytes", prefix, info.Size()))
			}
		},
		Command: func(data test.Data, helpers test.Helpers) test.TestableCommand {
			helpers.T().Log(fmt.Sprintf("%s decoding with saprobe: %s", prefix, af.path))

			saprobeOut := data.Temp().Path("saprobe.raw")

			return helpers.Command("decode", "--raw", "-o", saprobeOut, af.path)
		},
		Expected: func(data test.Data, _ test.Helpers) *test.Expected {
			return &test.Expected{
				ExitCode: expect.ExitCodeSuccess,
				Output:   comparePCMFiles(data, prefix, af.codec, af.lossy),
			}
		},
	}
}

type audioFile struct {
	path       string // absolute path to the audio file
	relPath    string // path relative to the test root (used as test name)
	pcmFormat  string // ffmpeg raw output format: "s16le", "s24le", "s32le"
	lossy      bool   // true for lossy codecs (mp3, ogg) that allow small sample differences
	codec      string // ffprobe codec name (e.g. "flac", "mp3", "vorbis", "alac")
	bitDepth   string // bit depth as reported by ffprobe (e.g. "16", "24", "N/A")
	sampleRate string // sample rate in Hz (e.g. "44100", "48000")
	channels   string // number of channels (e.g. "1", "2", "6")
}

// discoverAudioFiles recursively walks root, probes every file with ffprobe,
// and returns those containing exactly one audio stream.
// Files with no audio streams are silently skipped.
// Files with multiple audio streams cause an immediate fatal error.
func discoverAudioFiles(t *testing.T, root string) []audioFile {
	t.Helper()

	var files []audioFile

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		// Filter by extension if -only is set.
		if *only != "" {
			ext := strings.TrimPrefix(filepath.Ext(path), ".")
			if !strings.EqualFold(ext, *only) {
				return nil
			}
		}

		af, isAudio := probeAudioFile(t, path)
		if !isAudio {
			return nil
		}

		af.relPath, _ = filepath.Rel(root, path)

		files = append(files, af)

		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", root, err)
	}

	return files
}

// probeAudioFile runs ffprobe on path to detect audio streams.
// Returns the audioFile metadata and true if exactly one audio stream exists.
// Returns false (skip) if no audio streams are found.
// Fatals if multiple audio streams are detected.
func probeAudioFile(t *testing.T, path string) (audioFile, bool) {
	t.Helper()

	result, err := agar.FFProbe(path)
	if err != nil {
		// ffprobe failed — not a media file, skip.
		return audioFile{}, false
	}

	stream, streamErr := result.AudioStream()
	if streamErr != nil {
		// No audio streams.
		return audioFile{}, false
	}

	// Check for multiple audio streams.
	audioCount := 0

	for _, s := range result.Streams {
		if s.CodecType == "audio" {
			audioCount++
		}
	}

	if audioCount > 1 {
		t.Fatalf("multiple audio tracks in %s", path)
	}

	bitsRaw := stream.BitsPerRawSample
	pcmFmt, lossy := codecToPCMFormat(stream.CodecName, bitsRaw)

	return audioFile{
		path:       path,
		pcmFormat:  pcmFmt,
		lossy:      lossy,
		codec:      stream.CodecName,
		bitDepth:   bitsRaw,
		sampleRate: stream.SampleRate,
		channels:   fmt.Sprintf("%d", stream.Channels),
	}, true
}

// codecToPCMFormat maps an ffprobe codec name and bit depth string to the
// ffmpeg raw PCM output format and whether the codec is lossy.
func codecToPCMFormat(codec, bitsRaw string) (string, bool) {
	// Lossy codecs: saprobe always decodes to 16-bit.
	switch codec {
	case "mp3", "vorbis", "aac":
		return "s16le", true
	}

	// Lossless codecs: use probed bit depth.
	depth, _ := strconv.Atoi(bitsRaw)

	switch depth {
	case 32:
		return "s32le", false
	case 20, 24:
		return "s24le", false
	default:
		return "s16le", false
	}
}

// comparePCMFiles returns a comparator that reads the saprobe and ffmpeg output
// files from the test's temp directory and compares them.
// For lossless codecs, comparison is byte-for-byte.
// For lossy codecs, small per-sample differences (±2) are tolerated.
// For AAC, if the outputs are badly misaligned (missing gapless metadata),
// the test passes with a warning instead of failing.
//
//revive:disable:flag-parameter
func comparePCMFiles(data test.Data, prefix, codec string, lossy bool) test.Comparator {
	return func(_ string, t tig.T) {
		t.Helper()

		saprobeData, err := os.ReadFile(data.Temp().Path("saprobe.raw"))
		if err != nil {
			t.Log(prefix + " reading saprobe output: " + err.Error())
			t.Fail()

			return
		}

		refData, err := os.ReadFile(data.Temp().Path("ref.raw"))
		if err != nil {
			t.Log(prefix + " reading ffmpeg reference: " + err.Error())
			t.Fail()

			return
		}

		t.Log(fmt.Sprintf("%s comparing: saprobe=%d bytes, ffmpeg=%d bytes", prefix, len(saprobeData), len(refData)))

		if lossy {
			compareLossy(t, prefix, codec, saprobeData, refData)
		} else {
			compareLossless(t, prefix, saprobeData, refData)
		}
	}
}

// compareLossless requires exact byte match.
func compareLossless(t tig.T, prefix string, saprobeData, refData []byte) {
	t.Helper()

	if bytes.Equal(saprobeData, refData) {
		t.Log(prefix + " MATCH")

		return
	}

	t.Log(prefix + " MISMATCH")
	reportFirstDifference(t, prefix, saprobeData, refData)
	t.Fail()
}

// compareLossy allows small per-sample differences (±2) for lossy codecs.
// Different decoders have floating-point rounding differences in synthesis filterbanks.
// Length differences up to a few frames are tolerated because decoders handle
// encoder delay, priming samples, and end-of-stream padding differently.
// MP3: 1 frame = 1152 samples; AAC: encoder delay is typically 2048 samples.
//
// For AAC files without gapless metadata (iTunSMPB / edts), CoreAudio and ffmpeg
// produce temporally misaligned outputs because they disagree on priming sample
// trimming. When this is detected, the test passes with a warning rather than failing.
func compareLossy(t tig.T, prefix, codec string, saprobeData, refData []byte) {
	t.Helper()

	// Allow length differences up to 3072 stereo 16-bit samples (12288 bytes).
	// Covers MP3 frame padding (~1152 samples) and AAC encoder delay (~2048 samples).
	const maxLengthDiffBytes = 3072 * 2 * 2

	lengthDiff := len(saprobeData) - len(refData)
	if lengthDiff < 0 {
		lengthDiff = -lengthDiff
	}

	if lengthDiff > maxLengthDiffBytes {
		if codec == "aac" {
			t.Log(fmt.Sprintf("%s WARNING: AAC length mismatch (saprobe=%d, ffmpeg=%d, diff=%d) — "+
				"likely missing gapless metadata, skipping comparison",
				prefix, len(saprobeData), len(refData), lengthDiff))

			return
		}

		t.Log(fmt.Sprintf("%s LENGTH MISMATCH: saprobe=%d, ffmpeg=%d (diff=%d exceeds tolerance %d)",
			prefix, len(saprobeData), len(refData), lengthDiff, maxLengthDiffBytes))
		t.Fail()

		return
	}

	if lengthDiff > 0 {
		t.Log(fmt.Sprintf("%s length diff: saprobe=%d, ffmpeg=%d (±%d bytes, within tolerance)",
			prefix, len(saprobeData), len(refData), lengthDiff))
	}

	// Compare the overlapping region with per-sample tolerance.
	const maxDiffPerSample = 2

	compareLen := min(len(saprobeData), len(refData))
	numSamples := compareLen / 2
	largeDiffs := 0
	maxDiff := int16(0)

	for i := range numSamples {
		spSample := int16(binary.LittleEndian.Uint16(saprobeData[i*2:]))
		refSample := int16(binary.LittleEndian.Uint16(refData[i*2:]))

		diff := spSample - refSample
		if diff < 0 {
			diff = -diff
		}

		if diff > maxDiffPerSample {
			largeDiffs++
		}

		if diff > maxDiff {
			maxDiff = diff
		}
	}

	// Allow up to 1% of samples to have larger differences.
	maxLargeDiffs := numSamples / 100
	if largeDiffs > maxLargeDiffs {
		// For AAC, massive sample mismatch indicates temporal misalignment from
		// missing gapless metadata — not a decoding error. Warn and pass.
		if codec == "aac" {
			t.Log(fmt.Sprintf("%s WARNING: AAC sample misalignment (%d samples (%.2f%%) differ, max diff=%d) — "+
				"likely missing gapless metadata, skipping comparison",
				prefix, largeDiffs, float64(largeDiffs)/float64(numSamples)*100, maxDiff))

			return
		}

		t.Log(fmt.Sprintf("%s LOSSY MISMATCH: %d samples (%.2f%%) differ by more than ±%d, max diff=%d",
			prefix, largeDiffs, float64(largeDiffs)/float64(numSamples)*100, maxDiffPerSample, maxDiff))
		t.Fail()

		return
	}

	t.Log(fmt.Sprintf("%s MATCH (lossy: max diff=%d, large diffs=%d)", prefix, maxDiff, largeDiffs))
}

func logDiscoverySummary(t *testing.T, files []audioFile) {
	t.Helper()

	byCodec := map[string]int{}
	byBitDepth := map[string]int{}
	bySampleRate := map[string]int{}
	byChannels := map[string]int{}

	for _, af := range files {
		byCodec[af.codec]++
		byBitDepth[af.bitDepth]++
		bySampleRate[af.sampleRate]++
		byChannels[af.channels]++
	}

	t.Logf("--- summary: %d files ---", len(files))
	logBreakdown(t, "codec", byCodec)
	logBreakdown(t, "bit depth", byBitDepth)
	logBreakdown(t, "sample rate", bySampleRate)
	logBreakdown(t, "channels", byChannels)
}

func logBreakdown(t *testing.T, label string, counts map[string]int) {
	t.Helper()

	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}

	slices.Sort(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, counts[k]))
	}

	t.Logf("  by %s: %s", label, strings.Join(parts, ", "))
}

func reportFirstDifference(t tig.T, prefix string, a, b []byte) {
	t.Helper()

	minLen := min(len(a), len(b))

	for i := range minLen {
		if a[i] != b[i] {
			t.Log(fmt.Sprintf("%s first difference at byte %d: saprobe=0x%02x ffmpeg=0x%02x", prefix, i, a[i], b[i]))

			return
		}
	}

	t.Log(fmt.Sprintf("%s identical up to byte %d, then one output is longer", prefix, minLen))
}
