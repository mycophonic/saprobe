//nolint:gochecknoglobals
package tests_test

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mycophonic/agar/pkg/agar"

	"github.com/mycophonic/saprobe"
	"github.com/mycophonic/saprobe/alac"
	"github.com/mycophonic/saprobe/mp3"
	"github.com/mycophonic/saprobe/vorbis"
)

// Codec configurations to test.
type codecConfig struct {
	name           string
	ext            string
	sampleRate     int
	bitDepth       int  // input bit depth for encoding (0 = codec decides)
	channels       int  // source/encode channels (1=mono, 2=stereo)
	outputChannels int  // what saprobe outputs (MP3 always 2, others match source)
	lossy          bool // lossy codecs allow small sample differences between decoders
	ffmpegArgs     []string
	decoder        func(path string) ([]byte, saprobe.PCMFormat, error)
}

// Vorbis: lossy, supports various sample rates, mono and stereo.
var vorbisConfigs = []codecConfig{
	// Stereo
	{
		name: "vorbis_44100_stereo", ext: "ogg", sampleRate: 44100, bitDepth: 16, channels: 2, outputChannels: 2, lossy: true,
		ffmpegArgs: []string{"-c:a", "libvorbis", "-q:a", "6"}, decoder: decodeVorbis,
	},
	{
		name: "vorbis_48000_stereo", ext: "ogg", sampleRate: 48000, bitDepth: 16, channels: 2, outputChannels: 2, lossy: true,
		ffmpegArgs: []string{"-c:a", "libvorbis", "-q:a", "6"}, decoder: decodeVorbis,
	},
	// Mono
	{
		name: "vorbis_44100_mono", ext: "ogg", sampleRate: 44100, bitDepth: 16, channels: 1, outputChannels: 1, lossy: true,
		ffmpegArgs: []string{"-c:a", "libvorbis", "-q:a", "6"}, decoder: decodeVorbis,
	},
}

// MP3: lossy, supports MPEG 1 (32000/44100/48000 Hz) and MPEG 2 (16000/22050/24000 Hz),
// mono and stereo, CBR and VBR. go-mp3 always decodes to stereo 16-bit.
var mp3Configs = []codecConfig{
	// MPEG 1 stereo CBR 320k — all sample rates
	{
		name: "mp3_mpeg1_32000_stereo_cbr320", ext: "mp3", sampleRate: 32000, bitDepth: 16, channels: 2, outputChannels: 2, lossy: true,
		ffmpegArgs: []string{"-c:a", "libmp3lame", "-b:a", "320k"}, decoder: decodeMp3,
	},
	{
		name: "mp3_mpeg1_44100_stereo_cbr320", ext: "mp3", sampleRate: 44100, bitDepth: 16, channels: 2, outputChannels: 2, lossy: true,
		ffmpegArgs: []string{"-c:a", "libmp3lame", "-b:a", "320k"}, decoder: decodeMp3,
	},
	{
		name: "mp3_mpeg1_48000_stereo_cbr320", ext: "mp3", sampleRate: 48000, bitDepth: 16, channels: 2, outputChannels: 2, lossy: true,
		ffmpegArgs: []string{"-c:a", "libmp3lame", "-b:a", "320k"}, decoder: decodeMp3,
	},

	// MPEG 2 stereo CBR 160k — all sample rates (max for MPEG 2)
	{
		name: "mp3_mpeg2_16000_stereo_cbr160", ext: "mp3", sampleRate: 16000, bitDepth: 16, channels: 2, outputChannels: 2, lossy: true,
		ffmpegArgs: []string{"-c:a", "libmp3lame", "-b:a", "160k"}, decoder: decodeMp3,
	},
	{
		name: "mp3_mpeg2_22050_stereo_cbr160", ext: "mp3", sampleRate: 22050, bitDepth: 16, channels: 2, outputChannels: 2, lossy: true,
		ffmpegArgs: []string{"-c:a", "libmp3lame", "-b:a", "160k"}, decoder: decodeMp3,
	},
	{
		name: "mp3_mpeg2_24000_stereo_cbr160", ext: "mp3", sampleRate: 24000, bitDepth: 16, channels: 2, outputChannels: 2, lossy: true,
		ffmpegArgs: []string{"-c:a", "libmp3lame", "-b:a", "160k"}, decoder: decodeMp3,
	},

	// MPEG 1 mono CBR 320k — go-mp3 always outputs stereo
	{
		name: "mp3_mpeg1_44100_mono_cbr320", ext: "mp3", sampleRate: 44100, bitDepth: 16, channels: 1, outputChannels: 2, lossy: true,
		ffmpegArgs: []string{"-c:a", "libmp3lame", "-b:a", "320k"}, decoder: decodeMp3,
	},

	// MPEG 1 stereo VBR quality 2
	{
		name: "mp3_mpeg1_44100_stereo_vbr", ext: "mp3", sampleRate: 44100, bitDepth: 16, channels: 2, outputChannels: 2, lossy: true,
		ffmpegArgs: []string{"-c:a", "libmp3lame", "-q:a", "2"}, decoder: decodeMp3,
	},

	// MPEG 1 stereo CBR 128k (lower bitrate)
	{
		name: "mp3_mpeg1_44100_stereo_cbr128", ext: "mp3", sampleRate: 44100, bitDepth: 16, channels: 2, outputChannels: 2, lossy: true,
		ffmpegArgs: []string{"-c:a", "libmp3lame", "-b:a", "128k"}, decoder: decodeMp3,
	},
}

func TestVorbisDecode(t *testing.T) {
	t.Parallel()

	for _, cfg := range vorbisConfigs {
		t.Run(cfg.name, func(t *testing.T) {
			t.Parallel()
			runSyntheticTest(t, cfg)
		})
	}
}

func TestMP3Decode(t *testing.T) {
	t.Parallel()

	for _, cfg := range mp3Configs {
		t.Run(cfg.name, func(t *testing.T) {
			t.Parallel()
			runSyntheticTest(t, cfg)
		})
	}
}

func runSyntheticTest(t *testing.T, cfg codecConfig) {
	t.Helper()

	tmpDir := t.TempDir()

	// Generate source PCM (white noise, 1 second).
	srcPCM := generateWhiteNoise(cfg.sampleRate, cfg.bitDepth, cfg.channels, 1)
	srcPath := filepath.Join(tmpDir, "source.raw")

	if err := os.WriteFile(srcPath, srcPCM, 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	// Encode with ffmpeg.
	encPath := filepath.Join(tmpDir, "encoded."+cfg.ext)
	if err := ffmpegEncode(srcPath, encPath, cfg); err != nil {
		// Skip if encoder not available (e.g., libvorbis not compiled in).
		if strings.Contains(err.Error(), "Unknown encoder") ||
			strings.Contains(err.Error(), "Encoder not found") {
			t.Skipf("encoder not available: %v", err)
		}

		t.Fatalf("ffmpeg encode: %v", err)
	}

	// Decode with ffmpeg (reference).
	ffmpegPCM, err := ffmpegDecode(encPath, cfg.bitDepth, cfg.outputChannels)
	if err != nil {
		t.Fatalf("ffmpeg decode: %v", err)
	}

	// Decode with saprobe.
	saprobePCM, format, err := cfg.decoder(encPath)
	if err != nil {
		t.Fatalf("saprobe decode: %v", err)
	}

	// Verify format.
	if format.SampleRate != cfg.sampleRate {
		t.Errorf("sample rate: got %d, want %d", format.SampleRate, cfg.sampleRate)
	}

	if int(format.BitDepth) != cfg.bitDepth {
		t.Errorf("bit depth: got %d, want %d", format.BitDepth, cfg.bitDepth)
	}

	if int(format.Channels) != cfg.outputChannels {
		t.Errorf("channels: got %d, want %d", format.Channels, cfg.outputChannels)
	}

	// Compare PCM data.
	if cfg.lossy {
		// For lossy codecs, allow length and sample value differences.
		compareLossySamples(t, ffmpegPCM, saprobePCM, cfg.bitDepth, cfg.outputChannels)
	} else {
		// For lossless codecs, output must match exactly.
		if len(ffmpegPCM) != len(saprobePCM) {
			t.Errorf("saprobe vs ffmpeg length mismatch: ffmpeg=%d, saprobe=%d", len(ffmpegPCM), len(saprobePCM))
		}

		compareLosslessSamples(t, "saprobe vs ffmpeg", ffmpegPCM, saprobePCM, cfg.bitDepth, cfg.outputChannels)

		// Compare saprobe vs original source PCM.
		if len(srcPCM) != len(saprobePCM) {
			t.Errorf("saprobe vs source length mismatch: source=%d, saprobe=%d", len(srcPCM), len(saprobePCM))
		}

		compareLosslessSamples(t, "saprobe vs source", srcPCM, saprobePCM, cfg.bitDepth, cfg.outputChannels)
	}
}

// compareLosslessSamples requires exact byte match for lossless codecs.
// The label identifies which comparison is being made (e.g. "saprobe vs ffmpeg").
func compareLosslessSamples(t *testing.T, label string, expected, actual []byte, bitDepth, channels int) {
	t.Helper()

	minLen := min(len(expected), len(actual))
	differences := 0
	firstDiff := -1

	for i := range minLen {
		if expected[i] != actual[i] {
			differences++

			if firstDiff == -1 {
				firstDiff = i
			}
		}
	}

	if differences > 0 {
		bytesPerSample := pcmBytesPerSample(bitDepth)
		sampleIndex := firstDiff / bytesPerSample / channels
		t.Errorf("%s: PCM mismatch: %d differing bytes (%.2f%%), first diff at byte %d (sample %d)",
			label, differences, float64(differences)/float64(minLen)*100, firstDiff, sampleIndex)

		showDiffs(t, label, expected, actual, bitDepth, channels, 5)
	}
}

// compareLossySamples allows small differences between decoders for lossy codecs.
// Different MP3/Vorbis decoders use different floating-point implementations,
// resulting in ±1-2 LSB differences per sample.
// Length differences up to 1 MP3 frame (1152 stereo samples = 4608 bytes) are tolerated.
func compareLossySamples(t *testing.T, ffmpegPCM, saprobePCM []byte, bitDepth, channels int) {
	t.Helper()

	// Only 16-bit lossy codecs are currently supported.
	if bitDepth != 16 {
		t.Errorf("lossy comparison only supports 16-bit, got %d-bit", bitDepth)

		return
	}

	// Allow length differences up to 1 frame (1152 samples * channels * 2 bytes).
	const samplesPerFrame = 1152

	maxLengthDiffBytes := samplesPerFrame * channels * 2

	lengthDiff := len(saprobePCM) - len(ffmpegPCM)
	if lengthDiff < 0 {
		lengthDiff = -lengthDiff
	}

	if lengthDiff > maxLengthDiffBytes {
		t.Errorf("length mismatch: ffmpeg=%d, saprobe=%d (diff=%d exceeds tolerance %d)",
			len(ffmpegPCM), len(saprobePCM), lengthDiff, maxLengthDiffBytes)

		return
	}

	if lengthDiff > 0 {
		t.Logf("length diff: ffmpeg=%d, saprobe=%d (±%d bytes, within tolerance)",
			len(ffmpegPCM), len(saprobePCM), lengthDiff)
	}

	numSamples := min(len(ffmpegPCM), len(saprobePCM)) / 2

	const maxDiffPerSample = 2 // Allow ±2 difference per 16-bit sample

	largeDiffs := 0
	maxDiff := int16(0)

	for i := range numSamples {
		ffSample := int16(binary.LittleEndian.Uint16(ffmpegPCM[i*2:]))
		spSample := int16(binary.LittleEndian.Uint16(saprobePCM[i*2:]))

		diff := ffSample - spSample
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

	// Allow up to 1% of samples to have larger differences (codec edge cases).
	maxLargeDiffs := numSamples / 100
	if largeDiffs > maxLargeDiffs {
		t.Errorf("lossy PCM mismatch: %d samples (%.2f%%) differ by more than ±%d, max diff=%d",
			largeDiffs, float64(largeDiffs)/float64(numSamples)*100, maxDiffPerSample, maxDiff)
		showDiffs(t, "saprobe vs ffmpeg", ffmpegPCM, saprobePCM, bitDepth, channels, 5)
	}
}

// generateWhiteNoise creates random PCM data.
func generateWhiteNoise(sampleRate, bitDepth, channels, durationSec int) []byte {
	numSamples := sampleRate * durationSec * channels

	var bytesPerSample int

	switch bitDepth {
	case 4, 8:
		bytesPerSample = 1
	case 12, 16:
		bytesPerSample = 2
	case 20, 24:
		bytesPerSample = 3
	case 32:
		bytesPerSample = 4
	default:
		bytesPerSample = bitDepth / 8
	}

	buf := make([]byte, numSamples*bytesPerSample)

	// Use a simple PRNG for reproducibility.
	seed := uint64(0x12345678)

	for i := range numSamples {
		// xorshift64
		seed ^= seed << 13
		seed ^= seed >> 7
		seed ^= seed << 17

		offset := i * bytesPerSample

		switch bitDepth {
		case 4:
			// Scale to signed 4-bit range (-7..+7), stored sign-extended in 1 byte.
			buf[offset] = byte(int8((seed % 14) - 7))
		case 8:
			// Scale to signed 8-bit range (-120..+120), leave some headroom.
			buf[offset] = byte(int8((seed % 240) - 120))
		case 12:
			// Scale to signed 12-bit range (-2000..+2000), stored in 2 bytes LE.
			val := int16((seed % 4000) - 2000)
			binary.LittleEndian.PutUint16(buf[offset:], uint16(val))
		case 16:
			// Scale to 16-bit range, leave some headroom.
			val := int16((seed % 60000) - 30000)
			binary.LittleEndian.PutUint16(buf[offset:], uint16(val))
		case 20:
			// Scale to signed 20-bit range (-500000..+500000), stored in 3 bytes LE.
			val := int32((seed % 1000000) - 500000)
			buf[offset] = byte(val)
			buf[offset+1] = byte(val >> 8)
			buf[offset+2] = byte(val >> 16)
		case 24:
			// Scale to 24-bit range.
			val := int32((seed % 14000000) - 7000000)
			buf[offset] = byte(val)
			buf[offset+1] = byte(val >> 8)
			buf[offset+2] = byte(val >> 16)
		case 32:
			val := int32((seed % 1800000000) - 900000000)
			binary.LittleEndian.PutUint32(buf[offset:], uint32(val))

		default:
		}
	}

	return buf
}

// ffmpegEncode encodes raw PCM to the target format.
func ffmpegEncode(srcPath, dstPath string, cfg codecConfig) error {
	ffmpegBin, err := agar.LookFor("ffmpeg")
	if err != nil {
		return fmt.Errorf("ffmpeg not found: %w", err)
	}

	sampleFmt := "s16le"

	switch cfg.bitDepth {
	case 24:
		sampleFmt = "s24le"
	case 32:
		sampleFmt = "s32le"

	default:
	}

	args := []string{
		"-y",
		"-f", sampleFmt,
		"-ar", fmt.Sprintf("%d", cfg.sampleRate),
		"-ac", fmt.Sprintf("%d", cfg.channels),
		"-i", srcPath,
	}
	args = append(args, cfg.ffmpegArgs...)
	args = append(args, dstPath)

	cmd := exec.Command(ffmpegBin, args...)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg: %w\n%s", err, output)
	}

	return nil
}

// ffmpegDecode decodes an audio file to raw PCM using ffmpeg.
func ffmpegDecode(srcPath string, bitDepth, outputChannels int) ([]byte, error) {
	ffmpegBin, err := agar.LookFor("ffmpeg")
	if err != nil {
		return nil, fmt.Errorf("ffmpeg not found: %w", err)
	}

	sampleFmt := "s16le"

	switch bitDepth {
	case 24:
		sampleFmt = "s24le"
	case 32:
		sampleFmt = "s32le"

	default:
	}

	cmd := exec.Command(ffmpegBin,
		"-i", srcPath,
		"-f", sampleFmt,
		"-ac", fmt.Sprintf("%d", outputChannels),
		"-acodec", "pcm_"+sampleFmt[:3]+"le",
		"-",
	)

	var stdout, stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg decode: %w\n%s", err, stderr.String())
	}

	return stdout.Bytes(), nil
}

// Decoder wrappers.
func decodeAlac(path string) ([]byte, saprobe.PCMFormat, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, saprobe.PCMFormat{}, err
	}
	defer f.Close()

	return alac.Decode(f)
}

func decodeVorbis(path string) ([]byte, saprobe.PCMFormat, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, saprobe.PCMFormat{}, err
	}
	defer f.Close()

	return vorbis.Decode(f)
}

func decodeMp3(path string) ([]byte, saprobe.PCMFormat, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, saprobe.PCMFormat{}, err
	}
	defer f.Close()

	return mp3.Decode(f)
}

// pcmBytesPerSample returns the number of bytes per sample for a given bit depth.
func pcmBytesPerSample(bitDepth int) int {
	switch bitDepth {
	case 4, 8:
		return 1
	case 12, 16:
		return 2
	case 20, 24:
		return 3
	case 32:
		return 4
	default:
		return bitDepth / 8
	}
}

// showDiffs prints the first N differing samples for debugging.
func showDiffs(t *testing.T, label string, expected, actual []byte, bitDepth, channels, maxDiffs int) {
	t.Helper()

	bytesPerSample := pcmBytesPerSample(bitDepth)
	frameSize := bytesPerSample * channels
	shown := 0

	for i := 0; i < min(len(expected), len(actual))-frameSize && shown < maxDiffs; i += frameSize {
		expectedFrame := expected[i : i+frameSize]
		actualFrame := actual[i : i+frameSize]

		if !bytes.Equal(expectedFrame, actualFrame) {
			sampleIdx := i / frameSize
			t.Logf("%s: sample %d: expected=%v, actual=%v", label, sampleIdx, expectedFrame, actualFrame)

			shown++
		}
	}
}
