package tests_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mycophonic/agar/pkg/agar"

	"github.com/mycophonic/saprobe"
)

// mp3DecoderType identifies an MP3 decoder.
type mp3DecoderType int

const (
	mp3DecoderSaprobe mp3DecoderType = iota
	mp3DecoderFFmpeg
	mp3DecoderLame
)

func mp3DecoderName(dec mp3DecoderType) string {
	switch dec {
	case mp3DecoderSaprobe:
		return "saprobe"
	case mp3DecoderFFmpeg:
		return "ffmpeg"
	case mp3DecoderLame:
		return "lame"
	default:
		return "unknown"
	}
}

// mp3SampleRates covers all supported MP3 sample rates (MPEG 1 + MPEG 2).
//
//nolint:gochecknoglobals
var mp3SampleRates = []int{
	// MPEG 2
	16000, 22050, 24000,
	// MPEG 1
	32000, 44100, 48000,
}

// mp3ChannelCounts covers mono and stereo.
//
//nolint:gochecknoglobals
var mp3ChannelCounts = []int{1, 2}

// mp3BitrateConfig describes an encoding bitrate mode.
type mp3BitrateConfig struct {
	name       string   // test name segment (e.g. "cbr320")
	ffmpegArgs []string // ffmpeg encoding args
}

// mp3BitratesForRate returns bitrate configs appropriate for the given sample rate.
// MPEG 1 (≥32000 Hz) supports up to 320 kbps; MPEG 2 (<32000 Hz) up to 160 kbps.
func mp3BitratesForRate(sampleRate int) []mp3BitrateConfig {
	if sampleRate >= 32000 {
		// MPEG 1
		return []mp3BitrateConfig{
			{"cbr128", []string{"-c:a", "libmp3lame", "-b:a", "128k"}},
			{"cbr320", []string{"-c:a", "libmp3lame", "-b:a", "320k"}},
			{"vbr_q2", []string{"-c:a", "libmp3lame", "-q:a", "2"}},
		}
	}

	// MPEG 2
	return []mp3BitrateConfig{
		{"cbr96", []string{"-c:a", "libmp3lame", "-b:a", "96k"}},
		{"cbr160", []string{"-c:a", "libmp3lame", "-b:a", "160k"}},
		{"vbr_q2", []string{"-c:a", "libmp3lame", "-q:a", "2"}},
	}
}

// TestMP3Decode tests all sample rate × channel × bitrate combinations.
// For each encoded file, all three decoders are used and outputs are cross-compared.
func TestMP3Decode(t *testing.T) {
	t.Parallel()

	lameBin, lameBinErr := agar.LookFor("lame")

	for _, sampleRate := range mp3SampleRates {
		for _, channels := range mp3ChannelCounts {
			bitrates := mp3BitratesForRate(sampleRate)

			for _, br := range bitrates {
				name := fmt.Sprintf("%dHz_%dch/%s", sampleRate, channels, br.name)

				t.Run(name, func(t *testing.T) {
					t.Parallel()

					decoders := []mp3DecoderType{mp3DecoderSaprobe, mp3DecoderFFmpeg}
					if lameBinErr == nil {
						decoders = append(decoders, mp3DecoderLame)
					}

					runMP3Test(t, decoders, lameBin, sampleRate, channels, br)
				})
			}
		}
	}
}

//nolint:cyclop // Test orchestration requires many steps.
func runMP3Test(
	t *testing.T,
	decoders []mp3DecoderType,
	lameBin string,
	sampleRate, channels int,
	br mp3BitrateConfig,
) {
	t.Helper()

	tmpDir := t.TempDir()

	// Generate source PCM (white noise, 1 second).
	const bitDepth = 16

	srcPCM := generateWhiteNoise(sampleRate, bitDepth, channels, 1)
	srcPath := filepath.Join(tmpDir, "source.raw")

	if err := os.WriteFile(srcPath, srcPCM, 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	// Encode with ffmpeg (libmp3lame).
	encPath := filepath.Join(tmpDir, "encoded.mp3")

	cfg := codecConfig{
		sampleRate: sampleRate,
		bitDepth:   bitDepth,
		channels:   channels,
		ffmpegArgs: br.ffmpegArgs,
	}

	if err := ffmpegEncode(srcPath, encPath, cfg); err != nil {
		t.Fatalf("ffmpeg encode: %v", err)
	}

	// saprobe always outputs stereo (mono duplicated).
	const outputChannels = 2

	// Decode with every decoder and collect results.
	decoded := make(map[string][]byte, len(decoders))

	for _, dec := range decoders {
		decName := mp3DecoderName(dec)
		pcm, format := runMP3Decode(t, dec, lameBin, encPath, channels)

		// Verify format metadata (saprobe only).
		if dec == mp3DecoderSaprobe && format != nil {
			if format.SampleRate != sampleRate {
				t.Errorf("sample rate: got %d, want %d", format.SampleRate, sampleRate)
			}

			if int(format.BitDepth) != bitDepth {
				t.Errorf("bit depth: got %d, want %d", format.BitDepth, bitDepth)
			}

			if int(format.Channels) != outputChannels {
				t.Errorf("channels: got %d, want %d", format.Channels, outputChannels)
			}
		}

		decoded[decName] = pcm
	}

	// Cross-compare all decoder outputs against each other.
	decoderNames := make([]string, 0, len(decoded))
	for name := range decoded {
		decoderNames = append(decoderNames, name)
	}

	for idx := range decoderNames {
		for jdx := idx + 1; jdx < len(decoderNames); jdx++ {
			nameA := decoderNames[idx]
			nameB := decoderNames[jdx]
			label := fmt.Sprintf("decode(%s) vs decode(%s)", nameA, nameB)

			compareLossySamples(t, decoded[nameA], decoded[nameB], bitDepth, outputChannels)

			if t.Failed() {
				t.Logf("comparison failed: %s", label)
			}
		}
	}
}

func runMP3Decode(
	t *testing.T,
	dec mp3DecoderType,
	lameBin, encPath string,
	sourceChannels int,
) ([]byte, *saprobe.PCMFormat) {
	t.Helper()

	const bitDepth = 16

	switch dec {
	case mp3DecoderSaprobe:
		pcm, format, err := decodeMp3(encPath)
		if err != nil {
			t.Fatalf("saprobe decode: %v", err)
		}

		return pcm, &format

	case mp3DecoderFFmpeg:
		// For mono sources, decode to mono and duplicate — avoids ffmpeg's 1/√2 attenuation.
		decChannels := 2
		if sourceChannels == 1 {
			decChannels = 1
		}

		pcm, err := ffmpegDecode(encPath, bitDepth, decChannels)
		if err != nil {
			t.Fatalf("ffmpeg decode: %v", err)
		}

		if sourceChannels == 1 {
			pcm = monoToStereo(pcm, bitDepth)
		}

		return pcm, nil

	case mp3DecoderLame:
		pcm, err := mp3LameDecode(lameBin, encPath)
		if err != nil {
			t.Fatalf("lame decode: %v", err)
		}

		// lame outputs same channel count as source; for mono, duplicate to stereo.
		if sourceChannels == 1 {
			pcm = monoToStereo(pcm, bitDepth)
		}

		return pcm, nil

	default:
		t.Fatalf("unknown decoder type: %d", dec)

		return nil, nil
	}
}

// mp3LameDecode decodes an MP3 file to raw 16-bit PCM using the lame binary.
// The -t flag suppresses the WAV header, outputting raw PCM to stdout.
func mp3LameDecode(lameBin, srcPath string) ([]byte, error) {
	cmd := exec.Command(lameBin, "--decode", "-t", "--quiet", srcPath, "-")

	var stdout, stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("lame decode: %w\n%s", err, stderr.String())
	}

	return stdout.Bytes(), nil
}
