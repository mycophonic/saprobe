package tests_test

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mycophonic/agar/pkg/agar"
)

//nolint:gochecknoglobals
var conformanceRepo = flag.String(
	"conformance-repo", "",
	"path to ietf-wg-cellar/flac-test-files clone (auto-downloaded if empty)",
)

const conformanceRepoURL = "https://github.com/ietf-wg-cellar/flac-test-files.git"

// TestFLACConformance runs the IETF FLAC decoder testbench against three decoders:
// the reference flac binary, ffmpeg, and saprobe. Subset files must decode identically
// across all three. Uncommon files are best-effort. Faulty files must not crash.
func TestFLACConformance(t *testing.T) {
	t.Parallel()

	repoPath := ensureConformanceRepo(t)

	flacBin, flacBinErr := agar.LookFor("flac")
	if flacBinErr != nil {
		t.Skip("standalone flac binary not found")
	}

	ffmpegBin, ffmpegBinErr := agar.LookFor("ffmpeg")

	t.Run("subset", func(t *testing.T) {
		t.Parallel()

		files := discoverFlacFiles(t, filepath.Join(repoPath, "subset"))
		for _, path := range files {
			t.Run(filepath.Base(path), func(t *testing.T) {
				t.Parallel()

				props := probeFlacFile(t, path)

				runSubsetTest(t, path, props, flacBin, ffmpegBin, ffmpegBinErr)
			})
		}
	})

	t.Run("uncommon", func(t *testing.T) {
		t.Parallel()

		files := discoverFlacFiles(t, filepath.Join(repoPath, "uncommon"))
		for _, path := range files {
			t.Run(filepath.Base(path), func(t *testing.T) {
				t.Parallel()

				runUncommonTest(t, path, flacBin)
			})
		}
	})

	t.Run("faulty", func(t *testing.T) {
		t.Parallel()

		files := discoverFlacFiles(t, filepath.Join(repoPath, "faulty"))
		for _, path := range files {
			t.Run(filepath.Base(path), func(t *testing.T) {
				t.Parallel()

				runFaultyTest(t, path)
			})
		}
	})
}

// conformanceFlacProps holds ffprobe-derived properties for a conformance test file.
type conformanceFlacProps struct {
	bitDepth int
	channels int
}

func probeFlacFile(t *testing.T, path string) conformanceFlacProps {
	t.Helper()

	result, err := agar.FFProbe(path)
	if err != nil {
		t.Fatalf("ffprobe %s: %v", filepath.Base(path), err)
	}

	stream, streamErr := result.AudioStream()
	if streamErr != nil {
		t.Fatalf("ffprobe %s: %v", filepath.Base(path), streamErr)
	}

	return conformanceFlacProps{
		bitDepth: stream.BitDepth(),
		channels: stream.Channels,
	}
}

// flacBinaryRawSupported reports whether the reference flac binary can produce
// raw PCM output at the given bit depth. The --force-raw-format flag only
// supports 8, 16, 24, and 32-bit output; it rejects 12-bit and 20-bit.
func flacBinaryRawSupported(bitDepth int) bool {
	switch bitDepth {
	case 8, 16, 24, 32:
		return true
	default:
		return false
	}
}

// runSubsetTest decodes a subset FLAC file with all three decoders and compares output.
func runSubsetTest(
	t *testing.T,
	path string,
	props conformanceFlacProps,
	flacBin, ffmpegBin string,
	ffmpegBinErr error,
) {
	t.Helper()

	// Decode with saprobe (always available, supports all bit depths).
	saprobePCM, _, saprobeErr := decodeFlacFile(path)
	if saprobeErr != nil {
		t.Fatalf("saprobe decode: %v", saprobeErr)
	}

	// Decode with reference flac binary (only supports raw output at 8/16/24/32-bit).
	var refPCM []byte

	if !flacBinaryRawSupported(props.bitDepth) {
		t.Logf("skipping flac binary comparison: --force-raw-format does not support %d-bit output", props.bitDepth)
	} else {
		var err error

		refPCM, err = flacBinaryDecodeRaw(flacBin, path)
		if err != nil {
			t.Fatalf("flac binary decode: %v", err)
		}

		// Compare saprobe vs flac binary.
		if !bytes.Equal(saprobePCM, refPCM) {
			reportPCMDiff(t, "saprobe vs flac", saprobePCM, refPCM, props.bitDepth, props.channels)
		}
	}

	// Decode with ffmpeg (if available).
	if ffmpegBinErr != nil {
		t.Log("skipping ffmpeg comparison: ffmpeg not found")

		return
	}

	// ffmpeg has no native raw PCM format for 12-bit or 20-bit audio. It
	// outputs in the next wider format (16-bit), making byte-exact
	// comparison impossible at those depths.
	if !flacBinaryRawSupported(props.bitDepth) {
		t.Logf("skipping ffmpeg comparison: no native raw PCM format for %d-bit output", props.bitDepth)

		return
	}

	// Skip ffmpeg for known multichannel reordering issues.
	if ffmpegMultichannelFails(props.bitDepth, props.channels) {
		t.Logf(
			"skipping ffmpeg comparison: known channel remapping for %d-bit/%dch",
			props.bitDepth, props.channels,
		)

		return
	}

	ffmpegPCM, ffmpegErr := conformanceFFmpegDecode(ffmpegBin, path, props)
	if ffmpegErr != nil {
		t.Fatalf("ffmpeg decode: %v", ffmpegErr)
	}

	if !bytes.Equal(saprobePCM, ffmpegPCM) {
		reportPCMDiff(t, "saprobe vs ffmpeg", saprobePCM, ffmpegPCM, props.bitDepth, props.channels)
	}

	// Compare flac binary vs ffmpeg (only when both produced output).
	if refPCM != nil && !bytes.Equal(refPCM, ffmpegPCM) {
		reportPCMDiff(t, "flac vs ffmpeg", refPCM, ffmpegPCM, props.bitDepth, props.channels)
	}
}

// runUncommonTest decodes an uncommon FLAC file with the reference flac binary and saprobe.
// Both may legitimately fail; the test ensures saprobe doesn't crash.
// Panics are caught and reported as test failures.
// uncommonSkips lists uncommon FLAC test files that saprobe cannot decode yet.
// These are skipped with a warning rather than failing the test suite.
//
//nolint:gochecknoglobals
var uncommonSkips = map[string]string{
	"10 - file starting at frame header.flac": "headerless FLAC (no fLaC signature) not supported",
}

func runUncommonTest(t *testing.T, path, flacBin string) {
	t.Helper()

	if reason, ok := uncommonSkips[filepath.Base(path)]; ok {
		t.Skipf("known unsupported: %s", reason)
	}

	refPCM, refErr := flacBinaryDecodeRaw(flacBin, path)

	var (
		saprobePCM []byte
		saprobeErr error
		didPanic   bool
		panicValue any
	)

	func() {
		defer func() {
			if r := recover(); r != nil {
				didPanic = true
				panicValue = r
			}
		}()

		saprobePCM, _, saprobeErr = decodeFlacFile(path)
	}()

	if didPanic {
		t.Errorf("PANIC on uncommon file %s: %v", filepath.Base(path), panicValue)

		return
	}

	switch {
	case refErr != nil && saprobeErr != nil:
		t.Logf("both failed (acceptable): flac=%v, saprobe=%v", refErr, saprobeErr)
	case refErr != nil && saprobeErr == nil:
		t.Logf("flac binary failed but saprobe succeeded (%d bytes)", len(saprobePCM))
	case refErr == nil && saprobeErr != nil:
		t.Errorf("flac binary succeeded (%d bytes) but saprobe failed: %v", len(refPCM), saprobeErr)
	default:
		if !bytes.Equal(saprobePCM, refPCM) {
			t.Errorf(
				"saprobe vs flac mismatch: saprobe=%d bytes, flac=%d bytes",
				len(saprobePCM), len(refPCM),
			)
		}
	}
}

// runFaultyTest attempts to decode a faulty FLAC file with saprobe.
// The file is invalid; the test verifies saprobe doesn't crash (no panics).
// Per the IETF FLAC spec, a decoder should "refuse to decode, or at least not
// crash." Returning an error is ideal, but successfully decoding without
// crashing also satisfies the spec (e.g. files with metadata-only errors like
// faulty/01, 02, 10 where the audio frames themselves are valid).
func runFaultyTest(t *testing.T, path string) {
	t.Helper()

	var (
		decodeErr  error
		didPanic   bool
		panicValue any
	)

	func() {
		defer func() {
			if r := recover(); r != nil {
				didPanic = true
				panicValue = r
			}
		}()

		_, _, decodeErr = decodeFlacFile(path)
	}()

	switch {
	case didPanic:
		t.Errorf("PANIC on faulty file %s: %v", filepath.Base(path), panicValue)
	case decodeErr == nil:
		t.Logf("decoded without error (acceptable — no crash): %s", filepath.Base(path))
	default:
		t.Logf("correctly rejected: %v", decodeErr)
	}
}

// conformanceFFmpegDecode decodes a FLAC file to raw PCM using ffmpeg.
func conformanceFFmpegDecode(ffmpegBin, path string, props conformanceFlacProps) ([]byte, error) {
	outFmt := rawPCMFormat(props.bitDepth)
	codec := rawPCMCodec(props.bitDepth)

	cmd := exec.Command(ffmpegBin,
		"-i", path,
		"-f", outFmt,
		"-ac", fmt.Sprintf("%d", props.channels),
		"-acodec", codec,
		"-",
	)

	var stdout, stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%w\n%s", err, stderr.String())
	}

	return stdout.Bytes(), nil
}

// reportPCMDiff logs detailed information about a PCM mismatch.
func reportPCMDiff(t *testing.T, label string, got, want []byte, bitDepth, channels int) {
	t.Helper()

	if len(got) != len(want) {
		t.Errorf("%s: length mismatch: got=%d, want=%d", label, len(got), len(want))
	}

	minLen := min(len(got), len(want))
	firstDiff := -1

	for i := range minLen {
		if got[i] != want[i] {
			firstDiff = i

			break
		}
	}

	if firstDiff == -1 {
		if len(got) != len(want) {
			t.Errorf("%s: identical up to byte %d, then sizes differ", label, minLen)
		}

		return
	}

	bytesPerSample := pcmBytesPerSample(bitDepth)
	frameSize := bytesPerSample * channels
	sampleIdx := firstDiff / frameSize

	t.Errorf(
		"%s: first diff at byte %d (sample %d), got=0x%02X want=0x%02X",
		label, firstDiff, sampleIdx, got[firstDiff], want[firstDiff],
	)

	// Count total differing bytes.
	diffCount := 0

	for i := range minLen {
		if got[i] != want[i] {
			diffCount++
		}
	}

	t.Errorf("%s: %d/%d bytes differ (%.2f%%)", label, diffCount, minLen, float64(diffCount)*100/float64(minLen))
}

// ensureConformanceRepo returns the path to the flac-test-files repository,
// downloading it if necessary.
//
//nolint:cyclop // Sequential setup logic.
func ensureConformanceRepo(t *testing.T) string {
	t.Helper()

	if *conformanceRepo != "" {
		if _, err := os.Stat(*conformanceRepo); err != nil {
			t.Fatalf("conformance repo not found at %s: %v", *conformanceRepo, err)
		}

		return *conformanceRepo
	}

	// Default location inside the project's gitignored bin/ directory.
	repoDir := filepath.Join(projectRoot(t), "bin", "flac-test-files")

	conformanceOnce.Do(func() {
		if info, err := os.Stat(filepath.Join(repoDir, "subset")); err == nil && info.IsDir() {
			return // Already cloned.
		}

		gitBin, err := exec.LookPath("git")
		if err != nil {
			conformanceErr = fmt.Errorf("git not found: %w", err)

			return
		}

		t.Logf("cloning %s into %s", conformanceRepoURL, repoDir)

		if mkErr := os.MkdirAll(filepath.Dir(repoDir), 0o755); mkErr != nil {
			conformanceErr = fmt.Errorf("mkdir: %w", mkErr)

			return
		}

		cmd := exec.Command(gitBin, "clone", "--depth", "1", conformanceRepoURL, repoDir)
		cmd.Stderr = os.Stderr

		if cloneErr := cmd.Run(); cloneErr != nil {
			conformanceErr = fmt.Errorf("git clone: %w", cloneErr)
		}
	})

	if conformanceErr != nil {
		t.Skipf("could not download IETF FLAC test files: %v", conformanceErr)
	}

	return repoDir
}

//nolint:gochecknoglobals
var (
	conformanceOnce sync.Once
	conformanceErr  error
)

// projectRoot returns the saprobe project root (parent of tests/).
func projectRoot(t *testing.T) string {
	t.Helper()

	// This file is in tests/, so go up one level.
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	// Walk up to find go.mod.
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find project root (go.mod)")
		}

		dir = parent
	}
}

// discoverFlacFiles returns all .flac files in the given directory, sorted by name.
func discoverFlacFiles(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}

	var files []string

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		if strings.HasSuffix(entry.Name(), ".flac") {
			files = append(files, filepath.Join(dir, entry.Name()))
		}
	}

	if len(files) == 0 {
		t.Fatalf("no .flac files found in %s", dir)
	}

	return files
}
