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

package pcm_test

import (
	"encoding/binary"
	"errors"
	"math"
	"testing"

	"github.com/mycophonic/saprobe/pcm"
)

// deterministicDitherer creates a Ditherer with a fixed seed for reproducible tests.
// We use NewDitherer since there's no seed constructor, but tests that need
// exact values should account for PRNG variance.
func deterministicDitherer() *pcm.Ditherer {
	return pcm.NewDitherer()
}

func TestTruncate_32to16(t *testing.T) {
	t.Parallel()

	d := deterministicDitherer()

	// 32-bit sample: 0x00400000 = 4194304, shifted right 16 = 64.
	// With dither, output should be near 64 (±1 LSB of dither range).
	src := make([]byte, 4)
	binary.LittleEndian.PutUint32(src, uint32(0x00400000))

	dst := make([]byte, 2)

	n, err := d.Truncate(dst, src, 32, 16)
	if err != nil {
		t.Fatalf("Truncate 32→16: %v", err)
	}

	if n != 2 {
		t.Fatalf("Truncate 32→16: got %d bytes, want 2", n)
	}

	got := int16(binary.LittleEndian.Uint16(dst))
	// Dither can shift by at most ±(2^16-1) = ±65535 before shift,
	// which is ±1 after >>16. Allow ±1 from expected.
	expected := int16(64)
	diff := got - expected

	if diff < -1 || diff > 1 {
		t.Fatalf("Truncate 32→16: got %d, want ~%d (diff %d)", got, expected, diff)
	}
}

func TestTruncate_32to24(t *testing.T) {
	t.Parallel()

	d := deterministicDitherer()

	// 32-bit sample: 0x7F000000 (large positive).
	// Shifted right 8 → 0x7F0000 = 8323072.
	src := make([]byte, 4)
	binary.LittleEndian.PutUint32(src, uint32(0x7F000000))

	dst := make([]byte, 3)

	n, err := d.Truncate(dst, src, 32, 24)
	if err != nil {
		t.Fatalf("Truncate 32→24: %v", err)
	}

	if n != 3 {
		t.Fatalf("Truncate 32→24: got %d bytes, want 3", n)
	}

	v := int32(dst[0]) | int32(dst[1])<<8 | int32(dst[2])<<16 //nolint:mnd // 24-bit LE read.
	got := (v << 8) >> 8                                      //nolint:mnd // Sign-extend.

	expected := int32(0x7F0000)
	diff := got - expected

	if diff < -1 || diff > 1 {
		t.Fatalf("Truncate 32→24: got %d, want ~%d (diff %d)", got, expected, diff)
	}
}

func TestTruncate_24to16(t *testing.T) {
	t.Parallel()

	d := deterministicDitherer()

	// 24-bit sample: 0x008000 = 32768. Shifted right 8 → 128.
	src := []byte{0x00, 0x80, 0x00} // LE: 0x008000.
	dst := make([]byte, 2)

	n, err := d.Truncate(dst, src, 24, 16)
	if err != nil {
		t.Fatalf("Truncate 24→16: %v", err)
	}

	if n != 2 {
		t.Fatalf("Truncate 24→16: got %d bytes, want 2", n)
	}

	got := int16(binary.LittleEndian.Uint16(dst))
	expected := int16(128)
	diff := got - expected

	if diff < -1 || diff > 1 {
		t.Fatalf("Truncate 24→16: got %d, want ~%d (diff %d)", got, expected, diff)
	}
}

func TestTruncate_ZeroInput(t *testing.T) {
	t.Parallel()

	d := deterministicDitherer()

	// All-zero 32-bit samples. Output should be near zero (dither noise only).
	samples := 100
	src := make([]byte, samples*4)
	dst := make([]byte, samples*2)

	_, err := d.Truncate(dst, src, 32, 16)
	if err != nil {
		t.Fatalf("Truncate zero: %v", err)
	}

	for i := range samples {
		v := int16(binary.LittleEndian.Uint16(dst[i*2:]))
		// Dither noise for 32→16: max ±1 LSB after shift.
		if v < -1 || v > 1 {
			t.Fatalf("Truncate zero: sample %d = %d, want [-1, 1]", i, v)
		}
	}
}

func TestTruncate_MultipleSamples(t *testing.T) {
	t.Parallel()

	d := deterministicDitherer()

	// Two 32-bit samples → two 16-bit samples.
	src := make([]byte, 8)
	binary.LittleEndian.PutUint32(src[0:], uint32(0x01000000)) // ~16777216, >>16 = 256.
	binary.LittleEndian.PutUint32(src[4:], uint32(0xFF000000)) // Negative: -16777216, >>16 = -256.

	dst := make([]byte, 4)

	n, err := d.Truncate(dst, src, 32, 16)
	if err != nil {
		t.Fatalf("Truncate multi: %v", err)
	}

	if n != 4 {
		t.Fatalf("Truncate multi: got %d bytes, want 4", n)
	}

	v0 := int16(binary.LittleEndian.Uint16(dst[0:]))
	v1 := int16(binary.LittleEndian.Uint16(dst[2:]))

	if diff := int32(v0) - 256; diff < -1 || diff > 1 {
		t.Fatalf("Truncate multi: sample 0 = %d, want ~256", v0)
	}

	if diff := int32(v1) - (-256); diff < -1 || diff > 1 {
		t.Fatalf("Truncate multi: sample 1 = %d, want ~-256", v1)
	}
}

func TestTruncate_Clamping(t *testing.T) {
	t.Parallel()

	d := deterministicDitherer()

	// Max positive 32-bit: 0x7FFFFFFF. Truncated to 16 → should clamp to 32767.
	src := make([]byte, 4)
	binary.LittleEndian.PutUint32(src, uint32(0x7FFFFFFF))

	dst := make([]byte, 2)

	_, err := d.Truncate(dst, src, 32, 16)
	if err != nil {
		t.Fatalf("Truncate clamp max: %v", err)
	}

	got := int16(binary.LittleEndian.Uint16(dst))
	if got != 32767 {
		t.Fatalf("Truncate clamp max: got %d, want 32767", got)
	}

	// Min negative 32-bit: 0x80000000 = -2147483648. Truncated to 16 → should clamp to -32768.
	d2 := deterministicDitherer()

	binary.LittleEndian.PutUint32(src, uint32(0x80000000))

	_, err = d2.Truncate(dst, src, 32, 16)
	if err != nil {
		t.Fatalf("Truncate clamp min: %v", err)
	}

	got = int16(binary.LittleEndian.Uint16(dst))
	if got != -32768 {
		t.Fatalf("Truncate clamp min: got %d, want -32768", got)
	}
}

func TestTruncate_TPDFDistribution(t *testing.T) {
	t.Parallel()

	d := deterministicDitherer()

	// Truncate many identical 32-bit samples to 16-bit.
	// Use a value that truncates exactly to 0 without dither: 0x00000000.
	// All output variation is pure dither noise.
	const count = 10000

	src := make([]byte, count*4)
	dst := make([]byte, count*2)

	_, err := d.Truncate(dst, src, 32, 16)
	if err != nil {
		t.Fatalf("Truncate TPDF: %v", err)
	}

	var (
		sum   float64
		sumSq float64
	)

	for i := range count {
		v := float64(int16(binary.LittleEndian.Uint16(dst[i*2:])))
		sum += v
		sumSq += v * v
	}

	mean := sum / count
	variance := sumSq/count - mean*mean
	stddev := math.Sqrt(variance)

	// TPDF dither for 16-bit shift (shift=16):
	// Each uniform random is in [0, 65535]. Difference is in [-65535, 65535].
	// After >>16, output dither is in [-1, 1] (most values are 0).
	// Arithmetic right-shift floors toward -∞, so there is an inherent
	// negative bias: values in [-65535, -1] become -1, while [1, 65535]
	// become 0. Mean is expected to be slightly negative (~-0.5).
	if math.Abs(mean) > 1.0 {
		t.Fatalf("Truncate TPDF: mean = %g, want ~0 (within bias tolerance)", mean)
	}

	// Stddev should be positive but small (dither produces mostly 0, sometimes ±1).
	if stddev > 1.0 {
		t.Fatalf("Truncate TPDF: stddev = %g, want < 1.0", stddev)
	}
}

func TestTruncate_ValidationErrors(t *testing.T) {
	t.Parallel()

	d := deterministicDitherer()

	tests := []struct {
		name    string
		src     []byte
		dst     []byte
		srcBits int
		dstBits int
		wantErr error
	}{
		{
			name:    "unsupported src depth",
			src:     make([]byte, 4),
			dst:     make([]byte, 2),
			srcBits: 12,
			dstBits: 16,
			wantErr: pcm.ErrBitDepth,
		},
		{
			name:    "unsupported dst depth",
			src:     make([]byte, 4),
			dst:     make([]byte, 2),
			srcBits: 32,
			dstBits: 20,
			wantErr: pcm.ErrBitDepth,
		},
		{
			name:    "widen rejected",
			src:     make([]byte, 2),
			dst:     make([]byte, 4),
			srcBits: 16,
			dstBits: 32,
			wantErr: pcm.ErrWiden,
		},
		{
			name:    "same depth rejected",
			src:     make([]byte, 4),
			dst:     make([]byte, 4),
			srcBits: 32,
			dstBits: 32,
			wantErr: pcm.ErrSameBitDepth,
		},
		{
			name:    "src not aligned",
			src:     make([]byte, 5),
			dst:     make([]byte, 4),
			srcBits: 32,
			dstBits: 16,
			wantErr: pcm.ErrAlignment,
		},
		{
			name:    "dst too small",
			src:     make([]byte, 4),
			dst:     make([]byte, 1),
			srcBits: 32,
			dstBits: 16,
			wantErr: pcm.ErrShortDst,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := d.Truncate(tt.dst, tt.src, tt.srcBits, tt.dstBits)
			if err == nil {
				t.Fatal("Truncate: expected error, got nil")
			}

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Truncate: got error %q, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestTruncateSize(t *testing.T) {
	t.Parallel()

	if got := pcm.TruncateSize(8, 32, 16); got != 4 {
		t.Fatalf("TruncateSize(8, 32, 16) = %d, want 4", got)
	}

	if got := pcm.TruncateSize(8, 32, 24); got != 6 {
		t.Fatalf("TruncateSize(8, 32, 24) = %d, want 6", got)
	}

	if got := pcm.TruncateSize(6, 24, 16); got != 4 {
		t.Fatalf("TruncateSize(6, 24, 16) = %d, want 4", got)
	}

	if got := pcm.TruncateSize(0, 0, 0); got != 0 {
		t.Fatalf("TruncateSize(0, 0, 0) = %d, want 0", got)
	}
}
