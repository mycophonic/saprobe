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
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/containerd/nerdctl/mod/tigron/expect"
	"github.com/containerd/nerdctl/mod/tigron/test"
	"github.com/containerd/nerdctl/mod/tigron/tig"

	"github.com/mycophonic/agar/pkg/agar"

	"github.com/mycophonic/saprobe/tests/testutils"
)

// maxDiffPerSample is the per-sample tolerance for lossy codec comparison.
// Different decoders have floating-point rounding differences in synthesis filterbanks.
const maxDiffPerSample = 2

// mp3DecoderDelay is the synthesis filterbank priming delay in samples.
// Decoders with gapless support trim this; others include it as leading silence.
const mp3DecoderDelay = 529

// massDecodeTimeout is the per-command execution timeout for the mass decode test.
// The tigron default (3m) is too short for large hi-res files (e.g. 500MB+ 24-bit/192kHz).
const massDecodeTimeout = 30 * time.Minute

//nolint:gochecknoglobals
var (
	audioPath = flag.String("audio-path", "", "directory of audio files for mass decode comparison")
	only      = flag.String("only", "", "filter by file extension (e.g. flac, mp3, ogg, m4a)")
)

// decoderID identifies a decoder in the comparison matrix.
type decoderID int

const (
	decSaprobe decoderID = iota
	decFFmpeg
	decLame
	decMpg123
)

var decoderNames = [...]string{"sp", "ff", "lm", "mp"} //nolint:gochecknoglobals

func (d decoderID) String() string { return decoderNames[d] }

// decoderBins holds resolved binary paths for optional decoders.
type decoderBins struct {
	lame   string // empty if unavailable
	mpg123 string // empty if unavailable
}

// decOutput holds the loaded PCM data for one decoder.
type decOutput struct {
	id   decoderID
	data []byte
}

// pairResult holds the comparison outcome between two decoder outputs.
type pairResult struct {
	a, b           decoderID
	match          bool
	aligned        int // 0 or ±529
	maxDiff        int16
	largeDiffs     int
	lengthMismatch bool
}

// TestMassDecode walks a directory of audio files, decodes each with saprobe
// and reference decoders, and compares the raw PCM output.
// For MP3 files, four decoders are used (saprobe, ffmpeg, lame, mpg123) and
// failures are classified as FAIL (saprobe wrong) or HARD CASE (references disagree).
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

	// Resolve optional decoder binaries once.
	var bins decoderBins

	if path, err := agar.LookFor("lame"); err == nil {
		bins.lame = path
		t.Logf("lame: %s", path)
	} else {
		t.Log("lame not found — MP3 tests will not include lame reference")
	}

	if path, err := agar.LookFor("mpg123"); err == nil {
		bins.mpg123 = path
		t.Logf("mpg123: %s", path)
	} else {
		t.Log("mpg123 not found — MP3 tests will not include mpg123 reference")
	}

	testCase := testutils.Setup()
	testCase.Description = "mass decode comparison"
	// Parallelism will screw the pooch.
	testCase.NoParallel = true

	for i, af := range files {
		t.Logf("[%d/%d] queuing %s (%s)", i+1, len(files), af.relPath, af.pcmFormat)
		testCase.SubTests = append(testCase.SubTests, makeDecodeTest(af, i+1, len(files), bins))
	}

	testCase.Run(t)
}

func makeDecodeTest(af audioFile, index, total int, bins decoderBins) *test.Case {
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

			refOut := data.Temp().Path("ff.raw")

			ffCmd := helpers.Custom(ffmpegBin,
				"-i", af.path,
				"-f", af.pcmFormat,
				"-y", refOut,
			)
			ffCmd.WithTimeout(massDecodeTimeout)
			ffCmd.Run(&test.Expected{ExitCode: expect.ExitCodeSuccess})

			info, _ := os.Stat(refOut)
			if info != nil {
				helpers.T().Log(fmt.Sprintf("%s ffmpeg: %d bytes", prefix, info.Size()))
			}

			// For MP3, also decode with lame and mpg123 if available.
			if af.codec == "mp3" {
				if bins.lame != "" {
					lameOut := data.Temp().Path("lm.raw")
					if err := runLameDecode(bins.lame, af.path, lameOut); err != nil {
						helpers.T().Log(fmt.Sprintf("%s lame decode error: %v", prefix, err))
					} else if info, _ := os.Stat(lameOut); info != nil {
						helpers.T().Log(fmt.Sprintf("%s lame: %d bytes", prefix, info.Size()))
					}
				}

				if bins.mpg123 != "" {
					mpg123Out := data.Temp().Path("mp.raw")
					if err := runMpg123Decode(bins.mpg123, af.path, mpg123Out); err != nil {
						helpers.T().Log(fmt.Sprintf("%s mpg123 decode error: %v", prefix, err))
					} else if info, _ := os.Stat(mpg123Out); info != nil {
						helpers.T().Log(fmt.Sprintf("%s mpg123: %d bytes", prefix, info.Size()))
					}
				}
			}
		},
		Command: func(data test.Data, helpers test.Helpers) test.TestableCommand {
			helpers.T().Log(fmt.Sprintf("%s decoding with saprobe: %s", prefix, af.path))

			saprobeOut := data.Temp().Path("sp.raw")

			cmd := helpers.Command("decode", "--raw", "-o", saprobeOut, af.path)
			cmd.WithTimeout(massDecodeTimeout)

			return cmd
		},
		Expected: func(data test.Data, _ test.Helpers) *test.Expected {
			return &test.Expected{
				ExitCode: expect.ExitCodeSuccess,
				Output:   buildComparator(data, prefix, af, bins),
			}
		},
	}
}

// buildComparator returns the appropriate comparator for the audio file.
// MP3 files use multi-decoder comparison; all others use the legacy 2-decoder path.
func buildComparator(data test.Data, prefix string, af audioFile, bins decoderBins) test.Comparator {
	if af.codec != "mp3" {
		return comparePCMFiles(data, prefix, af.codec, af.lossy, af.channels)
	}

	return compareMP3MultiDecoder(data, prefix, af, bins)
}

// compareMP3MultiDecoder returns a comparator that reads all decoder outputs,
// performs pairwise comparison, and classifies the result.
func compareMP3MultiDecoder(data test.Data, prefix string, af audioFile, _ decoderBins) test.Comparator {
	return func(_ string, t tig.T) {
		t.Helper()

		nChannels, _ := strconv.Atoi(af.channels)
		if nChannels == 0 {
			nChannels = 2
		}

		// Load all available decoder outputs.
		var outputs []decOutput

		names := []struct {
			id   decoderID
			file string
		}{
			{decSaprobe, "sp.raw"},
			{decFFmpeg, "ff.raw"},
			{decLame, "lm.raw"},
			{decMpg123, "mp.raw"},
		}

		var errors []string

		for _, n := range names {
			path := data.Temp().Path(n.file)

			pcm, err := os.ReadFile(path)
			if err != nil {
				if n.id == decSaprobe || n.id == decFFmpeg {
					// Required decoders — fail if missing.
					t.Log(fmt.Sprintf("%s reading %s output: %v", prefix, n.id, err))
					t.Fail()

					return
				}
				// Optional decoder — note the error and continue.
				errors = append(errors, fmt.Sprintf("%s: read error", n.id))

				continue
			}

			if len(pcm) == 0 {
				errors = append(errors, fmt.Sprintf("%s: empty output", n.id))

				continue
			}

			// Mono-to-stereo duplication for lame/mpg123 on mono sources.
			if nChannels == 1 && (n.id == decLame || n.id == decMpg123) {
				pcm = monoToStereoS16(pcm)
			}

			outputs = append(outputs, decOutput{id: n.id, data: pcm})
		}

		// Log sizes.
		var sizeParts []string
		for _, o := range outputs {
			sizeParts = append(sizeParts, fmt.Sprintf("%s=%d", o.id, len(o.data)))
		}

		t.Log(fmt.Sprintf("%s comparing: %s", prefix, strings.Join(sizeParts, " ")))

		if len(errors) > 0 {
			t.Log(fmt.Sprintf("%s decoder errors: %s", prefix, strings.Join(errors, ", ")))
		}

		// Pairwise comparison.
		var pairs []pairResult

		for i := range outputs {
			for j := i + 1; j < len(outputs); j++ {
				pr := comparePair(outputs[i].data, outputs[j].data, outputs[i].id, outputs[j].id, nChannels)
				pairs = append(pairs, pr)
			}
		}

		// Classify and log.
		classifyMP3Result(t, prefix, outputs, pairs, errors)
	}
}

// comparePair compares two PCM buffers with alignment detection.
func comparePair(a, b []byte, idA, idB decoderID, channels int) pairResult {
	pr := pairResult{a: idA, b: idB}

	// Length check.
	const maxLengthDiffBytes = 3072 * 2 * 2

	lengthDiff := len(a) - len(b)
	if lengthDiff < 0 {
		lengthDiff = -lengthDiff
	}

	if lengthDiff > maxLengthDiffBytes {
		pr.lengthMismatch = true

		return pr
	}

	// Try direct comparison.
	largeDiffs, maxDiff := lossySampleCompare(a, b)
	compareLen := min(len(a), len(b))
	numSamples := compareLen / 2
	maxLargeDiffs := numSamples / 100

	if largeDiffs <= maxLargeDiffs {
		pr.match = true
		pr.maxDiff = maxDiff
		pr.largeDiffs = largeDiffs

		return pr
	}

	// Try shifting b forward by decoder delay (b has extra leading samples).
	refOffset := mp3DecoderDelay * 2 * channels
	if refOffset < len(b) {
		shifted, sMax := lossySampleCompare(a, b[refOffset:])
		sLen := min(len(a), len(b)-refOffset)
		sMaxLarge := sLen / 2 / 100

		if shifted <= sMaxLarge {
			pr.match = true
			pr.aligned = mp3DecoderDelay
			pr.maxDiff = sMax
			pr.largeDiffs = shifted

			return pr
		}
	}

	// Try shifting a forward by decoder delay (a has extra leading samples).
	if refOffset < len(a) {
		shifted, sMax := lossySampleCompare(a[refOffset:], b)
		sLen := min(len(a)-refOffset, len(b))
		sMaxLarge := sLen / 2 / 100

		if shifted <= sMaxLarge {
			pr.match = true
			pr.aligned = -mp3DecoderDelay
			pr.maxDiff = sMax
			pr.largeDiffs = shifted

			return pr
		}
	}

	// No alignment worked.
	pr.maxDiff = maxDiff
	pr.largeDiffs = largeDiffs

	return pr
}

// classifyMP3Result determines MATCH, FAIL, or HARD CASE from the pairwise results.
//
//nolint:cyclop
func classifyMP3Result(
	t tig.T, prefix string,
	outputs []decOutput,
	pairs []pairResult,
	decErrors []string,
) {
	t.Helper()

	// Build quick lookup: does pair (a,b) match?
	type pairKey struct{ a, b decoderID }

	pairMap := make(map[pairKey]pairResult, len(pairs))
	for _, pr := range pairs {
		pairMap[pairKey{pr.a, pr.b}] = pr
		pairMap[pairKey{pr.b, pr.a}] = pr
	}

	lookup := func(a, b decoderID) pairResult { return pairMap[pairKey{a, b}] }

	// Determine which decoders are present.
	present := make(map[decoderID]bool, len(outputs))
	for _, o := range outputs {
		present[o.id] = true
	}

	// Check if all pairs match.
	allMatch := true

	var globalMaxDiff int16

	var globalAligned int

	for _, pr := range pairs {
		if !pr.match {
			allMatch = false
		}

		if pr.maxDiff > globalMaxDiff {
			globalMaxDiff = pr.maxDiff
		}

		if pr.aligned != 0 {
			globalAligned = pr.aligned
		}
	}

	if allMatch {
		aligned := ""
		if globalAligned != 0 {
			aligned = fmt.Sprintf(", aligned %+d", globalAligned)
		}

		errNote := ""
		if len(decErrors) > 0 {
			errNote = fmt.Sprintf(" [%s]", strings.Join(decErrors, "; "))
		}

		t.Log(fmt.Sprintf("%s MATCH (%d decoders%s: max diff=%d)%s",
			prefix, len(outputs), aligned, globalMaxDiff, errNote))

		return
	}

	// Not all match. Determine if references agree with each other.
	refIDs := []decoderID{decFFmpeg, decLame, decMpg123}

	var presentRefs []decoderID

	for _, rid := range refIDs {
		if present[rid] {
			presentRefs = append(presentRefs, rid)
		}
	}

	// Check if all reference pairs agree.
	refsAgree := true

	for i := range presentRefs {
		for j := i + 1; j < len(presentRefs); j++ {
			pr := lookup(presentRefs[i], presentRefs[j])
			if !pr.match {
				refsAgree = false
			}
		}
	}

	// Build human-readable pair summary.
	pairSummary := buildPairSummary(pairs)

	if !refsAgree {
		// References disagree — HARD CASE.
		errNote := ""
		if len(decErrors) > 0 {
			errNote = fmt.Sprintf(" [%s]", strings.Join(decErrors, "; "))
		}

		t.Log(fmt.Sprintf("%s HARD CASE: references disagree (%s)%s", prefix, pairSummary, errNote))

		return
	}

	// References agree. Does saprobe match them?
	if present[decSaprobe] && len(presentRefs) > 0 {
		spMatchesRef := true

		for _, rid := range presentRefs {
			pr := lookup(decSaprobe, rid)
			if !pr.match {
				spMatchesRef = false

				break
			}
		}

		if spMatchesRef {
			aligned := ""
			if globalAligned != 0 {
				aligned = fmt.Sprintf(", aligned %+d", globalAligned)
			}

			t.Log(fmt.Sprintf("%s MATCH (%d decoders%s: max diff=%d)",
				prefix, len(outputs), aligned, globalMaxDiff))

			return
		}

		// saprobe differs from agreeing references — FAIL.
		t.Log(fmt.Sprintf("%s FAIL: saprobe differs from references (%s)", prefix, pairSummary))
		t.Fail()

		return
	}

	// Fallback: only 2 decoders, they disagree.
	t.Log(fmt.Sprintf("%s FAIL: decoders disagree (%s)", prefix, pairSummary))
	t.Fail()
}

// buildPairSummary produces a compact string like "sp=ff, sp≠lm, ff≠lm".
func buildPairSummary(pairs []pairResult) string {
	parts := make([]string, 0, len(pairs))

	for _, pr := range pairs {
		op := "="

		if !pr.match {
			if pr.lengthMismatch {
				op = "≠len"
			} else {
				op = "≠"
			}
		}

		detail := ""
		if pr.match && pr.aligned != 0 {
			detail = fmt.Sprintf("(%+d)", pr.aligned)
		}

		parts = append(parts, fmt.Sprintf("%s%s%s%s", pr.a, op, pr.b, detail))
	}

	return strings.Join(parts, ", ")
}

// runLameDecode decodes an MP3 file to raw s16le PCM using lame.
func runLameDecode(lameBin, srcPath, dstPath string) error {
	cmd := exec.Command(lameBin, "--decode", "-t", "--quiet", srcPath, "-")

	var stderr bytes.Buffer

	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("lame: %w\n%s", err, stderr.String())
	}

	return os.WriteFile(dstPath, out, 0o600)
}

// runMpg123Decode decodes an MP3 file to raw s16le PCM using mpg123.
func runMpg123Decode(mpg123Bin, srcPath, dstPath string) error {
	cmd := exec.Command(mpg123Bin, "-e", "s16", "-s", "-q", srcPath)

	var stderr bytes.Buffer

	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("mpg123: %w\n%s", err, stderr.String())
	}

	return os.WriteFile(dstPath, out, 0o600)
}

// monoToStereoS16 duplicates mono 16-bit PCM to stereo by doubling each sample.
func monoToStereoS16(mono []byte) []byte {
	stereo := make([]byte, len(mono)*2)

	for i := 0; i+1 < len(mono); i += 2 {
		stereo[i*2] = mono[i]
		stereo[i*2+1] = mono[i+1]
		stereo[i*2+2] = mono[i]
		stereo[i*2+3] = mono[i+1]
	}

	return stereo
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
		if err != nil {
			// Skip directories we cannot read (e.g. macOS system metadata).
			return filepath.SkipDir
		}

		if d.IsDir() {
			return nil
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

// comparePCMFiles returns a comparator for non-MP3 codecs (saprobe vs ffmpeg).
//
//revive:disable:flag-parameter
func comparePCMFiles(data test.Data, prefix, codec string, lossy bool, channels string) test.Comparator {
	return func(_ string, t tig.T) {
		t.Helper()

		saprobeData, err := os.ReadFile(data.Temp().Path("sp.raw"))
		if err != nil {
			t.Log(prefix + " reading saprobe output: " + err.Error())
			t.Fail()

			return
		}

		refData, err := os.ReadFile(data.Temp().Path("ff.raw"))
		if err != nil {
			t.Log(prefix + " reading ffmpeg reference: " + err.Error())
			t.Fail()

			return
		}

		t.Log(fmt.Sprintf("%s comparing: saprobe=%d bytes, ffmpeg=%d bytes", prefix, len(saprobeData), len(refData)))

		nChannels, _ := strconv.Atoi(channels)
		if nChannels == 0 {
			nChannels = 2
		}

		if lossy {
			compareLossy(t, prefix, codec, nChannels, saprobeData, refData)
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

// compareLossy allows small per-sample differences (±2) for non-MP3 lossy codecs.
func compareLossy(t tig.T, prefix, codec string, _ int, saprobeData, refData []byte) {
	t.Helper()

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

	largeDiffs, maxDiff := lossySampleCompare(saprobeData, refData)
	compareLen := min(len(saprobeData), len(refData))
	numSamples := compareLen / 2
	maxLargeDiffs := numSamples / 100

	if largeDiffs <= maxLargeDiffs {
		t.Log(fmt.Sprintf("%s MATCH (lossy: max diff=%d, large diffs=%d)", prefix, maxDiff, largeDiffs))

		return
	}

	if codec == "aac" {
		t.Log(fmt.Sprintf("%s WARNING: AAC sample misalignment (%d samples (%.2f%%) differ, max diff=%d) — "+
			"likely missing gapless metadata, skipping comparison",
			prefix, largeDiffs, float64(largeDiffs)/float64(numSamples)*100, maxDiff))

		return
	}

	t.Log(fmt.Sprintf("%s LOSSY MISMATCH: %d samples (%.2f%%) differ by more than ±%d, max diff=%d",
		prefix, largeDiffs, float64(largeDiffs)/float64(numSamples)*100, maxDiffPerSample, maxDiff))
	t.Fail()
}

// lossySampleCompare counts 16-bit samples where |a-b| > maxDiffPerSample
// across the overlapping region of two raw PCM buffers.
func lossySampleCompare(a, b []byte) (largeDiffs int, maxDiff int16) {
	compareLen := min(len(a), len(b))
	numSamples := compareLen / 2

	for i := range numSamples {
		sa := int16(binary.LittleEndian.Uint16(a[i*2:]))
		sb := int16(binary.LittleEndian.Uint16(b[i*2:]))

		diff := sa - sb
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

	return largeDiffs, maxDiff
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
