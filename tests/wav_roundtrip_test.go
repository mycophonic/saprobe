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
	"os"
	"path/filepath"
	"testing"

	"github.com/mycophonic/agar/pkg/agar"

	"github.com/mycophonic/saprobe"
	"github.com/mycophonic/saprobe/wav"
)

// TestWAVRoundTrip validates WAV encoding by round-tripping through ffmpeg:
// generate PCM -> wav.Encode -> ffmpeg decode WAV -> compare against original PCM.
// This ensures our WAV headers (both simple and extensible) are correct.
func TestWAVRoundTrip(t *testing.T) {
	t.Parallel()

	configs := []struct {
		name       string
		sampleRate int
		bitDepth   int
		channels   int
	}{
		// Simple WAVEFORMAT (mono/stereo, 16-bit)
		{"16bit_44100_mono", 44100, 16, 1},
		{"16bit_44100_stereo", 44100, 16, 2},
		{"16bit_48000_stereo", 48000, 16, 2},

		// WAVEFORMATEXTENSIBLE (>16-bit or >2 channels)
		{"24bit_44100_stereo", 44100, 24, 2},
		{"24bit_48000_stereo", 48000, 24, 2},
		{"32bit_44100_stereo", 44100, 32, 2},
		{"32bit_96000_stereo", 96000, 32, 2},

		// Multichannel (always extensible).
		// 4ch (quad) is excluded: ffmpeg remaps quad channel layout, producing
		// reordered samples. 6ch and 8ch verify extensible multichannel headers.
		{"24bit_48000_6ch", 48000, 24, 6},
		{"24bit_44100_8ch", 44100, 24, 8},
	}

	for _, cfg := range configs {
		t.Run(cfg.name, func(t *testing.T) {
			t.Parallel()

			// Generate known PCM.
			pcm := agar.GenerateWhiteNoise(cfg.sampleRate, cfg.bitDepth, cfg.channels, 1)
			format := saprobe.PCMFormat{
				SampleRate: cfg.sampleRate,
				BitDepth:   saprobe.BitDepth(cfg.bitDepth),
				Channels:   uint(cfg.channels),
			}

			// Encode to WAV.
			var wavBuf bytes.Buffer
			if err := wav.Encode(&wavBuf, pcm, format); err != nil {
				t.Fatalf("wav.Encode: %v", err)
			}

			// Write WAV to temp file for ffmpeg.
			wavPath := filepath.Join(t.TempDir(), "test.wav")
			if err := os.WriteFile(wavPath, wavBuf.Bytes(), 0o600); err != nil {
				t.Fatalf("writing WAV: %v", err)
			}

			// Decode WAV with ffmpeg to raw PCM.
			decoded := agar.FFmpegDecode(
				t,
				agar.FFmpegDecodeOptions{Src: wavPath, BitDepth: cfg.bitDepth, Channels: cfg.channels},
			)

			// Compare.
			if len(decoded) != len(pcm) {
				t.Fatalf("length mismatch: original=%d, decoded=%d", len(pcm), len(decoded))
			}

			if !bytes.Equal(decoded, pcm) {
				// Find first difference for debugging.
				for i := range pcm {
					if decoded[i] != pcm[i] {
						t.Fatalf("byte mismatch at offset %d: original=0x%02X, decoded=0x%02X", i, pcm[i], decoded[i])
					}
				}
			}
		})
	}
}
