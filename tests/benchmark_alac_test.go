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

	"github.com/mycophonic/saprobe/alac"
	"github.com/mycophonic/saprobe/tests/testutils"
)

//nolint:gochecknoglobals
var alacBenchFormats = []benchFormat{
	{"CD 44.1kHz/16bit", 44100, 16, 2},
	{"HiRes 96kHz/24bit", 96000, 24, 2},
}

//nolint:paralleltest // Benchmark must run sequentially for accurate timing.
func TestALACBenchmarkDecode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping benchmark in short mode")
	}

	alacconvertBin := testutils.AlacConvertPath(t)
	tmpDir := t.TempDir()

	var results []benchResult

	for _, bf := range alacBenchFormats {
		t.Logf("=== %s ===", bf.Name)

		srcPCM := generateWhiteNoise(bf.SampleRate, bf.BitDepth, bf.Channels, benchDuration)
		srcPath := filepath.Join(tmpDir, fmt.Sprintf("src_%d_%d.raw", bf.SampleRate, bf.BitDepth))

		if err := os.WriteFile(srcPath, srcPCM, 0o600); err != nil {
			t.Fatalf("write source: %v", err)
		}

		t.Logf("  PCM size: %.1f MB (%d bytes)", float64(len(srcPCM))/(1024*1024), len(srcPCM))

		// Encode to ALAC M4A via ffmpeg.
		alacSampleFmt := "s16p"
		if bf.BitDepth == 24 {
			alacSampleFmt = "s32p"
		}

		encPath := filepath.Join(tmpDir, fmt.Sprintf("enc_%d_%d.m4a", bf.SampleRate, bf.BitDepth))

		cfg := codecConfig{
			sampleRate: bf.SampleRate,
			bitDepth:   bf.BitDepth,
			channels:   bf.Channels,
			ffmpegArgs: []string{"-c:a", "alac", "-sample_fmt", alacSampleFmt},
		}

		if err := ffmpegEncode(srcPath, encPath, cfg); err != nil {
			t.Fatalf("ffmpeg encode: %v", err)
		}

		// Benchmark saprobe ALAC decode (from M4A).
		encoded, err := os.ReadFile(encPath)
		if err != nil {
			t.Fatalf("read encoded: %v", err)
		}

		t.Logf("  ALAC size: %.1f MB (%d bytes)", float64(len(encoded))/(1024*1024), len(encoded))

		durations := make([]time.Duration, benchIterations)

		for iter := range benchIterations {
			start := time.Now()

			_, _, decErr := alac.Decode(bytes.NewReader(encoded))
			if decErr != nil {
				t.Fatalf("alac decode: %v", decErr)
			}

			durations[iter] = time.Since(start)
		}

		results = append(results, computeResult(bf.Name, "saprobe", "decode", durations, len(srcPCM)))

		// Benchmark ffmpeg ALAC decode (from M4A).
		results = append(results, benchDecodeFFmpeg(t, bf, encPath))

		// Benchmark alacconvert ALAC decode (from its own CAF).
		if alacconvertBin != "" {
			cafPath := filepath.Join(tmpDir, fmt.Sprintf("enc_%d_%d.caf", bf.SampleRate, bf.BitDepth))

			wavPath := filepath.Join(tmpDir, fmt.Sprintf("src_%d_%d.wav", bf.SampleRate, bf.BitDepth))
			if err := writeWAV(wavPath, srcPCM, bf.BitDepth, bf.SampleRate, bf.Channels); err != nil {
				t.Fatalf("write WAV: %v", err)
			}

			if err := alacConvertEncode(alacconvertBin, wavPath, cafPath); err != nil {
				t.Fatalf("alacconvert encode: %v", err)
			}

			results = append(results, benchDecodeAlacconvert(t, bf, alacconvertBin, cafPath))
		}
	}

	printResults(t, results)
}

//nolint:paralleltest // Benchmark must run sequentially for accurate timing.
func TestALACBenchmarkDecodeFile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping benchmark in short mode")
	}

	filePath := os.Getenv("BENCH_ALAC_FILE")
	if filePath == "" {
		t.Skip("set BENCH_ALAC_FILE to run this benchmark")
	}

	encoded, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}

	t.Logf("File: %s (%.1f MB)", filePath, float64(len(encoded))/(1024*1024))

	tmpDir := t.TempDir()

	ffmpegBin, ffmpegErr := agar.LookFor("ffmpeg")
	alacconvertBin := testutils.AlacConvertPath(t)

	var results []benchResult

	bf := benchFormat{Name: filepath.Base(filePath), Channels: 2}

	// saprobe decode (from M4A).
	durations := make([]time.Duration, benchIterations)

	for iter := range benchIterations {
		start := time.Now()

		_, _, decErr := alac.Decode(bytes.NewReader(encoded))
		if decErr != nil {
			t.Fatalf("alac decode: %v", decErr)
		}

		durations[iter] = time.Since(start)
	}

	results = append(results, computeResult(bf.Name, "saprobe", "decode", durations, len(encoded)))

	// ffmpeg decode (from M4A).
	if ffmpegErr == nil {
		m4aPath := filepath.Join(tmpDir, "input.m4a")
		if writeErr := os.WriteFile(m4aPath, encoded, 0o600); writeErr != nil {
			t.Fatalf("write temp: %v", writeErr)
		}

		results = append(results, benchDecodeFFmpeg(t, bf, m4aPath))
	}

	// alacconvert decode (from CAF). Convert M4A → WAV → CAF as setup.
	if alacconvertBin != "" && ffmpegErr == nil {
		wavPath := filepath.Join(tmpDir, "input.wav")

		cmd := exec.Command(ffmpegBin,
			"-hide_banner", "-loglevel", "error",
			"-y", "-i", filePath,
			"-f", "wav", wavPath,
		)

		if wavOut, wavErr := cmd.CombinedOutput(); wavErr != nil {
			t.Logf("skipping alacconvert: ffmpeg wav export failed: %v\n%s", wavErr, wavOut)
		} else {
			cafPath := filepath.Join(tmpDir, "input.caf")

			encCmd := exec.Command(alacconvertBin, wavPath, cafPath)

			if encOut, encErr := encCmd.CombinedOutput(); encErr != nil {
				t.Logf("skipping alacconvert: encode failed: %v\n%s", encErr, encOut)
			} else {
				results = append(results, benchDecodeAlacconvert(t, bf, alacconvertBin, cafPath))
			}
		}
	}

	printResults(t, results)
}

// benchDecodeAlacconvert benchmarks alacconvert decoding a CAF to WAV.
func benchDecodeAlacconvert(t *testing.T, bf benchFormat, alacconvertBin, cafPath string) benchResult {
	t.Helper()

	tmpDir := t.TempDir()

	durations := make([]time.Duration, benchIterations)

	for iter := range benchIterations {
		wavOut := filepath.Join(tmpDir, fmt.Sprintf("decoded_%d.wav", iter))

		start := time.Now()

		cmd := exec.Command(alacconvertBin, cafPath, wavOut)

		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("alacconvert decode: %v\n%s", err, output)
		}

		durations[iter] = time.Since(start)

		os.Remove(wavOut)
	}

	return computeResult(bf.Name, "alacconvert", "decode", durations, fileSize(t, cafPath))
}
