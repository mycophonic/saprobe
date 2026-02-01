package tests_test

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/mycophonic/agar/pkg/agar"

	"github.com/mycophonic/saprobe"
	"github.com/mycophonic/saprobe/flac"
)

const (
	benchIterations = 10
	benchDuration   = 10 // seconds of audio
)

type benchFormat struct {
	Name       string
	SampleRate int
	BitDepth   int
	Channels   int
}

//nolint:gochecknoglobals
var benchFormats = []benchFormat{
	{"CD 44.1kHz/16bit", 44100, 16, 2},
	{"HiRes 96kHz/24bit", 96000, 24, 2},
	{"UltraHiRes 192kHz/24bit", 192000, 24, 2},
	{"Studio 192kHz/32bit", 192000, 32, 2},
}

type benchResult struct {
	Format  string
	Tool    string
	Op      string
	Median  time.Duration
	Mean    time.Duration
	Min     time.Duration
	Max     time.Duration
	Stddev  time.Duration
	PCMSize int
}

//nolint:paralleltest // Benchmark must run sequentially for accurate timing.
func TestFLACBenchmarkEncode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping benchmark in short mode")
	}

	flacBin, flacBinErr := agar.LookFor("flac")
	if flacBinErr != nil {
		t.Skip("flac binary not found")
	}

	tmpDir := t.TempDir()

	var results []benchResult

	for _, bf := range benchFormats {
		t.Logf("=== %s ===", bf.Name)

		srcPCM := generateWhiteNoise(bf.SampleRate, bf.BitDepth, bf.Channels, benchDuration)
		srcPath := filepath.Join(tmpDir, fmt.Sprintf("src_%d_%d.raw", bf.SampleRate, bf.BitDepth))

		if err := os.WriteFile(srcPath, srcPCM, 0o600); err != nil {
			t.Fatalf("write source: %v", err)
		}

		t.Logf("  PCM size: %.1f MB (%d bytes)", float64(len(srcPCM))/(1024*1024), len(srcPCM))

		dstSaprobe := filepath.Join(tmpDir, fmt.Sprintf("enc_saprobe_%d_%d.flac", bf.SampleRate, bf.BitDepth))
		results = append(results, benchEncodeSaprobe(t, bf, srcPCM, dstSaprobe))

		dstFlac := filepath.Join(tmpDir, fmt.Sprintf("enc_flac_%d_%d.flac", bf.SampleRate, bf.BitDepth))
		results = append(results, benchEncodeFlacBin(t, bf, flacBin, srcPath, dstFlac))

		if bf.BitDepth == 16 || bf.BitDepth == 24 {
			dstFFmpeg := filepath.Join(tmpDir, fmt.Sprintf("enc_ffmpeg_%d_%d.flac", bf.SampleRate, bf.BitDepth))
			results = append(results, benchEncodeFFmpeg(t, bf, srcPath, dstFFmpeg))
		}
	}

	printResults(t, results)
}

//nolint:paralleltest // Benchmark must run sequentially for accurate timing.
func TestFLACBenchmarkDecode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping benchmark in short mode")
	}

	flacBin, flacBinErr := agar.LookFor("flac")
	if flacBinErr != nil {
		t.Skip("flac binary not found")
	}

	tmpDir := t.TempDir()

	var results []benchResult

	for _, bf := range benchFormats {
		t.Logf("=== %s ===", bf.Name)

		srcPCM := generateWhiteNoise(bf.SampleRate, bf.BitDepth, bf.Channels, benchDuration)
		encPath := filepath.Join(tmpDir, fmt.Sprintf("enc_%d_%d.flac", bf.SampleRate, bf.BitDepth))

		if err := encodeFLACForBench(srcPCM, encPath, bf); err != nil {
			t.Fatalf("encode setup: %v", err)
		}

		t.Logf("  PCM size: %.1f MB (%d bytes)", float64(len(srcPCM))/(1024*1024), len(srcPCM))

		results = append(results, benchDecodeSaprobe(t, bf, encPath))
		results = append(results, benchDecodeFlacBin(t, bf, flacBin, encPath))
		results = append(results, benchDecodeFFmpeg(t, bf, encPath))
	}

	printResults(t, results)
}

// encodeFLACForBench encodes raw PCM to FLAC once (no timing), used as setup for decode benchmarks.
func encodeFLACForBench(srcPCM []byte, dstPath string, bf benchFormat) error {
	depth, err := saprobe.ToBitDepth(uint8(bf.BitDepth))
	if err != nil {
		return err
	}

	format := saprobe.PCMFormat{
		SampleRate: bf.SampleRate,
		BitDepth:   depth,
		Channels:   uint(bf.Channels),
	}

	var buf bytes.Buffer
	if err := flac.Encode(&buf, srcPCM, format); err != nil {
		return err
	}

	return os.WriteFile(dstPath, buf.Bytes(), 0o600)
}

func benchEncodeSaprobe(t *testing.T, bf benchFormat, srcPCM []byte, dstPath string) benchResult {
	t.Helper()

	depth, err := saprobe.ToBitDepth(uint8(bf.BitDepth))
	if err != nil {
		t.Fatalf("ToBitDepth: %v", err)
	}

	format := saprobe.PCMFormat{
		SampleRate: bf.SampleRate,
		BitDepth:   depth,
		Channels:   uint(bf.Channels),
	}

	durations := make([]time.Duration, benchIterations)

	for iter := range benchIterations {
		var buf bytes.Buffer

		start := time.Now()

		if err := flac.Encode(&buf, srcPCM, format); err != nil {
			t.Fatalf("saprobe encode: %v", err)
		}

		durations[iter] = time.Since(start)

		// Write the last iteration to disk for decode benchmarks.
		if iter == benchIterations-1 {
			if err := os.WriteFile(dstPath, buf.Bytes(), 0o600); err != nil {
				t.Fatalf("write encoded: %v", err)
			}

			ratio := float64(buf.Len()) / float64(len(srcPCM)) * 100
			t.Logf("  saprobe encode: %.1f%% ratio (%d bytes)", ratio, buf.Len())
		}
	}

	return computeResult(bf.Name, "saprobe", "encode", durations, len(srcPCM))
}

func benchEncodeFlacBin(t *testing.T, bf benchFormat, flacBin, srcPath, dstPath string) benchResult {
	t.Helper()

	durations := make([]time.Duration, benchIterations)

	for iter := range benchIterations {
		start := time.Now()

		cmd := exec.Command(flacBin,
			"-f", "--silent",
			"--force-raw-format",
			"--sign=signed",
			"--endian=little",
			fmt.Sprintf("--channels=%d", bf.Channels),
			fmt.Sprintf("--bps=%d", bf.BitDepth),
			fmt.Sprintf("--sample-rate=%d", bf.SampleRate),
			"-o", dstPath,
			srcPath,
		)

		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("flac encode: %v\n%s", err, output)
		}

		durations[iter] = time.Since(start)
	}

	return computeResult(bf.Name, "flac", "encode", durations, fileSize(t, srcPath))
}

func benchEncodeFFmpeg(t *testing.T, bf benchFormat, srcPath, dstPath string) benchResult {
	t.Helper()

	ffmpegBin, err := agar.LookFor("ffmpeg")
	if err != nil {
		t.Fatalf("ffmpeg not found: %v", err)
	}

	inputFmt := rawPCMFormat(bf.BitDepth)

	var sampleFmt string

	switch bf.BitDepth {
	case 16:
		sampleFmt = "s16"
	case 24:
		sampleFmt = "s32"
	default:
		t.Fatalf("ffmpeg encode: unsupported bit depth %d", bf.BitDepth)
	}

	durations := make([]time.Duration, benchIterations)

	for iter := range benchIterations {
		start := time.Now()

		cmd := exec.Command(ffmpegBin,
			"-y", "-hide_banner", "-loglevel", "error",
			"-f", inputFmt,
			"-ar", fmt.Sprintf("%d", bf.SampleRate),
			"-ac", fmt.Sprintf("%d", bf.Channels),
			"-i", srcPath,
			"-c:a", "flac",
			"-sample_fmt", sampleFmt,
			dstPath,
		)

		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("ffmpeg encode: %v\n%s", err, output)
		}

		durations[iter] = time.Since(start)
	}

	return computeResult(bf.Name, "ffmpeg", "encode", durations, fileSize(t, srcPath))
}

func benchDecodeSaprobe(t *testing.T, bf benchFormat, srcPath string) benchResult {
	t.Helper()

	encoded, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatalf("read encoded: %v", err)
	}

	durations := make([]time.Duration, benchIterations)

	for iter := range benchIterations {
		start := time.Now()

		_, _, err := flac.Decode(bytes.NewReader(encoded))
		if err != nil {
			t.Fatalf("saprobe decode: %v", err)
		}

		durations[iter] = time.Since(start)
	}

	return computeResult(bf.Name, "saprobe", "decode", durations, len(encoded))
}

func benchDecodeFlacBin(t *testing.T, bf benchFormat, flacBin, srcPath string) benchResult {
	t.Helper()

	durations := make([]time.Duration, benchIterations)

	for iter := range benchIterations {
		start := time.Now()

		cmd := exec.Command(flacBin,
			"-d", "-f", "--silent",
			"--force-raw-format",
			"--sign=signed",
			"--endian=little",
			"-o", "-",
			srcPath,
		)

		var stdout, stderr bytes.Buffer

		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			t.Fatalf("flac decode: %v\n%s", err, stderr.String())
		}

		durations[iter] = time.Since(start)
	}

	return computeResult(bf.Name, "flac", "decode", durations, fileSize(t, srcPath))
}

func benchDecodeFFmpeg(t *testing.T, bf benchFormat, srcPath string) benchResult {
	t.Helper()

	ffmpegBin, err := agar.LookFor("ffmpeg")
	if err != nil {
		t.Fatalf("ffmpeg not found: %v", err)
	}

	outFmt := rawPCMFormat(bf.BitDepth)
	codec := rawPCMCodec(bf.BitDepth)

	durations := make([]time.Duration, benchIterations)

	for iter := range benchIterations {
		start := time.Now()

		cmd := exec.Command(ffmpegBin,
			"-hide_banner", "-loglevel", "error",
			"-i", srcPath,
			"-f", outFmt,
			"-ac", fmt.Sprintf("%d", bf.Channels),
			"-acodec", codec,
			"-",
		)

		var stdout, stderr bytes.Buffer

		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			t.Fatalf("ffmpeg decode: %v\n%s", err, stderr.String())
		}

		durations[iter] = time.Since(start)
	}

	return computeResult(bf.Name, "ffmpeg", "decode", durations, fileSize(t, srcPath))
}

func computeResult(format, tool, op string, durations []time.Duration, pcmSize int) benchResult {
	sorted := make([]time.Duration, len(durations))
	copy(sorted, durations)
	slices.Sort(sorted)

	var sum float64
	for _, d := range durations {
		sum += float64(d)
	}

	mean := sum / float64(len(durations))

	var variance float64

	for _, d := range durations {
		diff := float64(d) - mean
		variance += diff * diff
	}

	variance /= float64(len(durations))

	return benchResult{
		Format:  format,
		Tool:    tool,
		Op:      op,
		Median:  sorted[len(sorted)/2],
		Mean:    time.Duration(mean),
		Min:     sorted[0],
		Max:     sorted[len(sorted)-1],
		Stddev:  time.Duration(math.Sqrt(variance)),
		PCMSize: pcmSize,
	}
}

func printResults(t *testing.T, results []benchResult) {
	t.Helper()

	sep := "──────────────────────────────────────────────────────────────────"

	t.Log("")
	t.Log("┌" + sep + "┐")
	t.Logf("│ FLAC Benchmark Results (%d iterations per test)%s│",
		benchIterations, "                  ")
	t.Log("├" + sep + "┤")
	t.Logf("│ %-24s %-7s %-6s %8s %8s %8s %8s│",
		"Format", "Tool", "Op", "Median", "Mean", "Min", "Max")
	t.Log("├" + sep + "┤")

	currentFormat := ""

	for _, r := range results {
		if r.Format != currentFormat {
			if currentFormat != "" {
				t.Log("├" + sep + "┤")
			}

			currentFormat = r.Format
		}

		t.Logf("│ %-24s %-7s %-6s %8s %8s %8s %8s│",
			r.Format, r.Tool, r.Op,
			r.Median.Round(time.Millisecond),
			r.Mean.Round(time.Millisecond),
			r.Min.Round(time.Millisecond),
			r.Max.Round(time.Millisecond),
		)
	}

	t.Log("└" + sep + "┘")
}

//nolint:paralleltest // Benchmark must run sequentially for accurate timing.
func TestFLACBenchmarkDecodeFile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping benchmark in short mode")
	}

	filePath := os.Getenv("BENCH_FLAC_FILE")
	if filePath == "" {
		t.Skip("set BENCH_FLAC_FILE to run this benchmark")
	}

	encoded, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}

	t.Logf("File: %s (%.1f MB)", filePath, float64(len(encoded))/(1024*1024))

	flacBin, flacBinErr := agar.LookFor("flac")

	var results []benchResult

	bf := benchFormat{Name: filepath.Base(filePath), Channels: 2}

	// saprobe decode
	durations := make([]time.Duration, benchIterations)

	for iter := range benchIterations {
		start := time.Now()

		_, _, decErr := flac.Decode(bytes.NewReader(encoded))
		if decErr != nil {
			t.Fatalf("saprobe decode: %v", decErr)
		}

		durations[iter] = time.Since(start)
	}

	results = append(results, computeResult(bf.Name, "saprobe", "decode", durations, len(encoded)))

	// flac binary decode
	if flacBinErr == nil {
		tmpFile := filepath.Join(t.TempDir(), "input.flac")
		if writeErr := os.WriteFile(tmpFile, encoded, 0o600); writeErr != nil {
			t.Fatalf("write temp: %v", writeErr)
		}

		results = append(results, benchDecodeFlacBin(t, bf, flacBin, tmpFile))
		results = append(results, benchDecodeFFmpeg(t, bf, tmpFile))
	}

	printResults(t, results)
}

func fileSize(t *testing.T, path string) int {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	return int(info.Size())
}
