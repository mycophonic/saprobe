package tests_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/mycophonic/agar/pkg/agar"

	"github.com/mycophonic/saprobe/mp3"
)

// mp3BenchConfig describes a synthetic MP3 benchmark format.
type mp3BenchConfig struct {
	Name       string
	SampleRate int
	Channels   int
	FFmpegArgs []string
}

//nolint:gochecknoglobals
var mp3BenchConfigs = []mp3BenchConfig{
	{"44.1kHz stereo CBR320", 44100, 2, []string{"-c:a", "libmp3lame", "-b:a", "320k"}},
	{"44.1kHz stereo CBR128", 44100, 2, []string{"-c:a", "libmp3lame", "-b:a", "128k"}},
	{"44.1kHz stereo VBR q2", 44100, 2, []string{"-c:a", "libmp3lame", "-q:a", "2"}},
}

//nolint:paralleltest // Benchmark must run sequentially for accurate timing.
func TestMP3BenchmarkDecode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping benchmark in short mode")
	}

	lameBin, lameBinErr := agar.LookFor("lame")
	tmpDir := t.TempDir()

	var results []benchResult

	for _, bc := range mp3BenchConfigs {
		t.Logf("=== %s ===", bc.Name)

		srcPCM := generateWhiteNoise(bc.SampleRate, 16, bc.Channels, benchDuration)
		srcPath := filepath.Join(tmpDir, fmt.Sprintf("src_%s.raw", bc.Name))

		if err := os.WriteFile(srcPath, srcPCM, 0o600); err != nil {
			t.Fatalf("write source: %v", err)
		}

		t.Logf("  PCM size: %.1f MB (%d bytes)", float64(len(srcPCM))/(1024*1024), len(srcPCM))

		// Encode with ffmpeg.
		encPath := filepath.Join(tmpDir, fmt.Sprintf("enc_%s.mp3", bc.Name))

		cfg := codecConfig{
			sampleRate: bc.SampleRate,
			bitDepth:   16,
			channels:   bc.Channels,
			ffmpegArgs: bc.FFmpegArgs,
		}

		if err := ffmpegEncode(srcPath, encPath, cfg); err != nil {
			t.Fatalf("ffmpeg encode: %v", err)
		}

		encoded, err := os.ReadFile(encPath)
		if err != nil {
			t.Fatalf("read encoded: %v", err)
		}

		t.Logf("  MP3 size: %.1f MB (%d bytes)", float64(len(encoded))/(1024*1024), len(encoded))

		// Benchmark saprobe decode.
		results = append(results, benchMP3DecodeSaprobe(t, bc.Name, encoded))

		// Benchmark ffmpeg decode.
		results = append(results, benchMP3DecodeFFmpeg(t, bc, encPath))

		// Benchmark lame decode.
		if lameBinErr == nil {
			results = append(results, benchMP3DecodeLame(t, bc.Name, lameBin, encPath))
		}
	}

	printResults(t, results)
}

//nolint:paralleltest // Benchmark must run sequentially for accurate timing.
func TestMP3BenchmarkDecodeFile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping benchmark in short mode")
	}

	filePath := os.Getenv("BENCH_MP3_FILE")
	if filePath == "" {
		t.Skip("set BENCH_MP3_FILE to run this benchmark")
	}

	encoded, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}

	t.Logf("File: %s (%.1f MB)", filePath, float64(len(encoded))/(1024*1024))

	tmpDir := t.TempDir()
	lameBin, lameBinErr := agar.LookFor("lame")

	var results []benchResult

	name := filepath.Base(filePath)

	// saprobe decode.
	results = append(results, benchMP3DecodeSaprobe(t, name, encoded))

	// ffmpeg decode.
	mp3Path := filepath.Join(tmpDir, "input.mp3")
	if writeErr := os.WriteFile(mp3Path, encoded, 0o600); writeErr != nil {
		t.Fatalf("write temp: %v", writeErr)
	}

	bc := mp3BenchConfig{Name: name, SampleRate: 44100, Channels: 2}
	results = append(results, benchMP3DecodeFFmpeg(t, bc, mp3Path))

	// lame decode.
	if lameBinErr == nil {
		results = append(results, benchMP3DecodeLame(t, name, lameBin, mp3Path))
	}

	printResults(t, results)
}

func benchMP3DecodeSaprobe(t *testing.T, name string, encoded []byte) benchResult {
	t.Helper()

	durations := make([]time.Duration, benchIterations)

	for iter := range benchIterations {
		start := time.Now()

		_, _, err := mp3.Decode(bytes.NewReader(encoded))
		if err != nil {
			t.Fatalf("saprobe decode: %v", err)
		}

		durations[iter] = time.Since(start)
	}

	return computeResult(name, "saprobe", "decode", durations, len(encoded))
}

func benchMP3DecodeFFmpeg(t *testing.T, bc mp3BenchConfig, srcPath string) benchResult {
	t.Helper()

	ffmpegBin, err := agar.LookFor("ffmpeg")
	if err != nil {
		t.Fatalf("ffmpeg not found: %v", err)
	}

	durations := make([]time.Duration, benchIterations)

	for iter := range benchIterations {
		start := time.Now()

		cmd := exec.Command(ffmpegBin,
			"-hide_banner", "-loglevel", "error",
			"-i", srcPath,
			"-f", "s16le",
			"-ac", fmt.Sprintf("%d", bc.Channels),
			"-acodec", "pcm_s16le",
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

	return computeResult(bc.Name, "ffmpeg", "decode", durations, fileSize(t, srcPath))
}

func benchMP3DecodeLame(t *testing.T, name, lameBin, srcPath string) benchResult {
	t.Helper()

	durations := make([]time.Duration, benchIterations)

	for iter := range benchIterations {
		start := time.Now()

		cmd := exec.Command(lameBin, "--decode", "-t", "--quiet", srcPath, "-")

		var stdout, stderr bytes.Buffer

		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			t.Fatalf("lame decode: %v\n%s", err, stderr.String())
		}

		durations[iter] = time.Since(start)
	}

	return computeResult(name, "lame", "decode", durations, fileSize(t, srcPath))
}
