package tests_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mycophonic/agar/pkg/agar"

	"github.com/mycophonic/saprobe"
	"github.com/mycophonic/saprobe/tests/testutils"
)

// alacEncoderType identifies an ALAC encoder.
type alacEncoderType int

const (
	alacEncoderFFmpeg alacEncoderType = iota
	alacEncoderAlacconvert
)

// alacDecoderType identifies an ALAC decoder.
type alacDecoderType int

const (
	alacDecoderSaprobe alacDecoderType = iota
	alacDecoderFFmpeg
	alacDecoderAlacconvert
)

// ALAC bit depths testable via ffmpeg encoder (only s16p and s32p supported).
// The ALAC spec also defines 20-bit and 32-bit, but no available encoder produces them.
//
//nolint:gochecknoglobals
var alacBitDepths = []int{16, 24}

// alacSampleRates covers the full range of commonly used sample rates.
//
//nolint:gochecknoglobals
var alacSampleRates = []int{
	8000, 11025, 16000, 22050, 32000, 44100, 48000, 88200, 96000, 176400, 192000,
}

// alacChannelCounts covers all ALAC-supported channel counts (1 through 8).
//
//nolint:gochecknoglobals
var alacChannelCounts = []int{1, 2, 3, 4, 5, 6, 7, 8}

// alacChannelLayout returns the ffmpeg channel layout name for the given count.
// ALAC supports specific layouts; without explicit layout, ffmpeg may guess wrong
// (e.g., 3ch → "2.1" instead of "3.0") and silently downmix.
func alacChannelLayout(channels int) string {
	switch channels {
	case 1:
		return "mono"
	case 2:
		return "stereo"
	case 3:
		return "3.0"
	case 4:
		return "4.0"
	case 5:
		return "5.0"
	case 6:
		return "5.1"
	case 7:
		return "6.1(back)"
	case 8:
		return "7.1(wide)"
	default:
		return ""
	}
}

// alacMultichannelRemapped reports whether multichannel ALAC has different
// channel ordering between raw PCM input and encoded/decoded output.
// ffmpeg's ALAC encoder applies channel layout mapping (e.g., 3.0 = L/R/C),
// which reorders channels compared to the raw interleaved PCM input.
// For these cases we skip byte-level comparison but still verify output length.
func alacMultichannelRemapped(channels int) bool {
	return channels > 2
}

func alacEncoderName(enc alacEncoderType) string {
	switch enc {
	case alacEncoderFFmpeg:
		return "ffmpeg"
	case alacEncoderAlacconvert:
		return "alacconvert"
	default:
		return "unknown"
	}
}

func alacDecoderName(dec alacDecoderType) string {
	switch dec {
	case alacDecoderSaprobe:
		return "saprobe"
	case alacDecoderFFmpeg:
		return "ffmpeg"
	case alacDecoderAlacconvert:
		return "alacconvert"
	default:
		return "unknown"
	}
}

// alacEncodersForConfig returns which encoders support the given channel count.
// ffmpeg encodes to M4A for all channel counts.
// alacconvert encodes WAV to CAF but only supports mono and stereo WAV input.
func alacEncodersForConfig(channels int, alacconvertBin string) []alacEncoderType {
	if alacconvertBin != "" && channels <= 2 {
		return []alacEncoderType{alacEncoderFFmpeg, alacEncoderAlacconvert}
	}

	return []alacEncoderType{alacEncoderFFmpeg}
}

// alacDecodersForEncoder returns the decoders compatible with a given encoder's output.
// ffmpeg → M4A: saprobe + ffmpeg.
// alacconvert → CAF: ffmpeg + alacconvert.
func alacDecodersForEncoder(enc alacEncoderType) []alacDecoderType {
	switch enc {
	case alacEncoderFFmpeg:
		return []alacDecoderType{alacDecoderSaprobe, alacDecoderFFmpeg}
	case alacEncoderAlacconvert:
		return []alacDecoderType{alacDecoderFFmpeg, alacDecoderAlacconvert}
	default:
		return nil
	}
}

// alacFFmpegEncode encodes raw PCM to ALAC in an M4A container via ffmpeg.
func alacFFmpegEncode(srcPath, dstPath string, bitDepth, sampleRate, channels int) error {
	ffmpegBin, err := agar.LookFor("ffmpeg")
	if err != nil {
		return fmt.Errorf("ffmpeg not found: %w", err)
	}

	inputFmt := "s16le"
	if bitDepth == 24 {
		inputFmt = "s24le"
	}

	sampleFmt := "s16p"
	if bitDepth == 24 {
		sampleFmt = "s32p"
	}

	args := []string{
		"-y",
		"-f", inputFmt,
		"-ar", fmt.Sprintf("%d", sampleRate),
		"-ac", fmt.Sprintf("%d", channels),
	}

	if layout := alacChannelLayout(channels); layout != "" {
		args = append(args, "-channel_layout", layout)
	}

	args = append(args, "-i", srcPath, "-c:a", "alac", "-sample_fmt", sampleFmt, dstPath)

	cmd := exec.Command(ffmpegBin, args...)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg alac encode: %w\n%s", err, output)
	}

	return nil
}

// alacConvertEncode encodes a WAV file to ALAC in a CAF container via alacconvert.
func alacConvertEncode(alacconvertBin, srcWavPath, dstCafPath string) error {
	cmd := exec.Command(alacconvertBin, srcWavPath, dstCafPath)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("alacconvert encode: %w\n%s", err, output)
	}

	return nil
}

// alacFFmpegDecode decodes an ALAC file (M4A or CAF) to raw PCM using ffmpeg.
func alacFFmpegDecode(srcPath string, bitDepth, channels int) ([]byte, error) {
	ffmpegBin, err := agar.LookFor("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("ffmpeg not found: %w", err)
	}

	outFmt := "s16le"
	codec := "pcm_s16le"

	if bitDepth == 24 {
		outFmt = "s24le"
		codec = "pcm_s24le"
	}

	args := []string{"-i", srcPath, "-f", outFmt, "-acodec", codec}

	if layout := alacChannelLayout(channels); layout != "" {
		args = append(args, "-channel_layout", layout)
	}

	args = append(args, "-")

	cmd := exec.Command(ffmpegBin, args...)

	var stdout, stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg alac decode: %w\n%s", err, stderr.String())
	}

	return stdout.Bytes(), nil
}

// alacConvertDecode decodes an ALAC CAF to WAV via Apple's alacconvert,
// then extracts the raw PCM by locating the "data" chunk.
func alacConvertDecode(alacconvertBin, srcCafPath, tmpDir string) ([]byte, error) {
	wavPath := filepath.Join(tmpDir, "alacconvert_decoded.wav")

	cmd := exec.Command(alacconvertBin, srcCafPath, wavPath)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("alacconvert decode: %w\n%s", err, output)
	}

	return readWAVPCMData(wavPath)
}

// readWAVPCMData reads a WAV file and returns only the raw PCM data.
// It searches for the "data" chunk rather than assuming a fixed header size.
func readWAVPCMData(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read WAV: %w", err)
	}

	for i := 12; i+8 <= len(data); i++ {
		if string(data[i:i+4]) == "data" {
			chunkSize := int(data[i+4]) | int(data[i+5])<<8 | int(data[i+6])<<16 | int(data[i+7])<<24
			pcmStart := i + 8

			if pcmStart+chunkSize > len(data) {
				return data[pcmStart:], nil
			}

			return data[pcmStart : pcmStart+chunkSize], nil
		}
	}

	return nil, fmt.Errorf("no data chunk found in WAV file %s", path)
}

// writeWAV writes a standard PCM WAV file from raw PCM data.
// alacconvert requires WAV input for encoding (it cannot read raw PCM).
func writeWAV(path string, pcm []byte, bitDepth, sampleRate, channels int) error {
	bytesPerSample := bitDepth / 8
	blockAlign := channels * bytesPerSample
	byteRate := sampleRate * blockAlign
	dataSize := len(pcm)

	buf := make([]byte, 44+dataSize)

	// RIFF header.
	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], uint32(36+dataSize))
	copy(buf[8:12], "WAVE")

	// fmt chunk.
	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:20], 16)
	binary.LittleEndian.PutUint16(buf[20:22], 1) // PCM
	binary.LittleEndian.PutUint16(buf[22:24], uint16(channels))
	binary.LittleEndian.PutUint32(buf[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(buf[28:32], uint32(byteRate))
	binary.LittleEndian.PutUint16(buf[32:34], uint16(blockAlign))
	binary.LittleEndian.PutUint16(buf[34:36], uint16(bitDepth))

	// data chunk.
	copy(buf[36:40], "data")
	binary.LittleEndian.PutUint32(buf[40:44], uint32(dataSize))
	copy(buf[44:], pcm)

	return os.WriteFile(path, buf, 0o600)
}

// TestALACDecode tests all bit depth x encoder x sample rate x channel combinations.
// ffmpeg encodes to M4A, alacconvert encodes to CAF (≤2ch). Each encoded file is
// decoded by all compatible decoders and outputs are compared against source PCM
// and cross-compared against each other.
func TestALACDecode(t *testing.T) {
	t.Parallel()

	alacconvertBin := testutils.AlacConvertPath(t)

	for _, bitDepth := range alacBitDepths {
		for _, sampleRate := range alacSampleRates {
			for _, channels := range alacChannelCounts {
				encoders := alacEncodersForConfig(channels, alacconvertBin)

				for _, enc := range encoders {
					name := fmt.Sprintf("%dbit/%s/%dHz_%dch",
						bitDepth, alacEncoderName(enc), sampleRate, channels)

					t.Run(name, func(t *testing.T) {
						t.Parallel()

						decoders := alacDecodersForEncoder(enc)

						runALACTest(t, enc, decoders, alacconvertBin,
							bitDepth, sampleRate, channels)
					})
				}
			}
		}
	}
}

//nolint:cyclop // Test orchestration requires many steps.
func runALACTest(
	t *testing.T,
	enc alacEncoderType,
	decoders []alacDecoderType,
	alacconvertBin string,
	bitDepth, sampleRate, channels int,
) {
	t.Helper()

	tmpDir := t.TempDir()

	// Generate source PCM (white noise, 1 second).
	srcPCM := generateWhiteNoise(sampleRate, bitDepth, channels, 1)
	srcPath := filepath.Join(tmpDir, "source.raw")

	if err := os.WriteFile(srcPath, srcPCM, 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	// Encode with the selected encoder.
	var encPath string

	switch enc {
	case alacEncoderFFmpeg:
		encPath = filepath.Join(tmpDir, "encoded.m4a")

		if err := alacFFmpegEncode(srcPath, encPath, bitDepth, sampleRate, channels); err != nil {
			t.Fatalf("ffmpeg encode: %v", err)
		}
	case alacEncoderAlacconvert:
		wavPath := filepath.Join(tmpDir, "source.wav")
		if err := writeWAV(wavPath, srcPCM, bitDepth, sampleRate, channels); err != nil {
			t.Fatalf("write WAV: %v", err)
		}

		encPath = filepath.Join(tmpDir, "encoded.caf")

		if err := alacConvertEncode(alacconvertBin, wavPath, encPath); err != nil {
			t.Fatalf("alacconvert encode: %v", err)
		}
	default:
		t.Fatalf("unknown encoder type: %d", enc)
	}

	skipSourceCompare := alacMultichannelRemapped(channels)

	// Decode with every compatible decoder.
	decoded := make(map[string][]byte, len(decoders))

	for _, dec := range decoders {
		decName := alacDecoderName(dec)
		pcm, format := runALACDecode(t, dec, alacconvertBin, encPath, tmpDir, bitDepth, channels)

		// Verify format metadata (saprobe decoder only).
		if dec == alacDecoderSaprobe && format != nil {
			if format.SampleRate != sampleRate {
				t.Errorf("sample rate: got %d, want %d", format.SampleRate, sampleRate)
			}

			if int(format.BitDepth) != bitDepth {
				t.Errorf("bit depth: got %d, want %d", format.BitDepth, bitDepth)
			}

			if int(format.Channels) != channels {
				t.Errorf("channels: got %d, want %d", format.Channels, channels)
			}
		}

		// Compare decoded PCM vs original source.
		if !skipSourceCompare {
			label := fmt.Sprintf("decode(%s) vs source", decName)

			if len(srcPCM) != len(pcm) {
				t.Errorf("%s length mismatch: source=%d, decoded=%d", label, len(srcPCM), len(pcm))
			}

			compareLosslessSamples(t, label, srcPCM, pcm, bitDepth, channels)
		}

		decoded[decName] = pcm
	}

	// Multichannel: verify output length only (channel remapping prevents byte comparison).
	if skipSourceCompare {
		for decName, pcm := range decoded {
			if len(srcPCM) != len(pcm) {
				t.Errorf("decode(%s) length mismatch: expected=%d, got=%d", decName, len(srcPCM), len(pcm))
			}
		}

		return
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

			if len(decoded[nameA]) != len(decoded[nameB]) {
				t.Errorf("%s length mismatch: %s=%d, %s=%d",
					label, nameA, len(decoded[nameA]), nameB, len(decoded[nameB]))
			}

			compareLosslessSamples(t, label, decoded[nameA], decoded[nameB], bitDepth, channels)
		}
	}
}

func runALACDecode(
	t *testing.T,
	dec alacDecoderType,
	alacconvertBin, encPath, tmpDir string,
	bitDepth, channels int,
) ([]byte, *saprobe.PCMFormat) {
	t.Helper()

	switch dec {
	case alacDecoderSaprobe:
		pcm, format, err := decodeAlac(encPath)
		if err != nil {
			t.Fatalf("saprobe decode: %v", err)
		}

		return pcm, &format
	case alacDecoderFFmpeg:
		pcm, err := alacFFmpegDecode(encPath, bitDepth, channels)
		if err != nil {
			t.Fatalf("ffmpeg decode: %v", err)
		}

		return pcm, nil
	case alacDecoderAlacconvert:
		pcm, err := alacConvertDecode(alacconvertBin, encPath, tmpDir)
		if err != nil {
			t.Fatalf("alacconvert decode: %v", err)
		}

		return pcm, nil
	default:
		t.Fatalf("unknown decoder type: %d", dec)

		return nil, nil
	}
}
