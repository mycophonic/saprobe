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

// putFloat32LE writes a float32 as little-endian bytes.
func putFloat32LE(buf []byte, v float32) {
	binary.LittleEndian.PutUint32(buf, math.Float32bits(v))
}

// putFloat64LE writes a float64 as little-endian bytes.
func putFloat64LE(buf []byte, v float64) {
	binary.LittleEndian.PutUint64(buf, math.Float64bits(v))
}

// readFloat32LE reads a little-endian float32.
func readFloat32LE(buf []byte) float32 {
	return math.Float32frombits(binary.LittleEndian.Uint32(buf))
}

// readFloat64LE reads a little-endian float64.
func readFloat64LE(buf []byte) float64 {
	return math.Float64frombits(binary.LittleEndian.Uint64(buf))
}

// putInt16LE writes an int16 as little-endian bytes.
func putInt16LE(buf []byte, v int16) {
	binary.LittleEndian.PutUint16(buf, uint16(v))
}

// putInt32LE writes an int32 as little-endian bytes.
func putInt32LE(buf []byte, v int32) {
	binary.LittleEndian.PutUint32(buf, uint32(v))
}

// putInt24LE writes a 24-bit signed integer as 3 little-endian bytes.
func putInt24LE(buf []byte, v int32) {
	buf[0] = byte(v)
	buf[1] = byte(v >> 8)
	buf[2] = byte(v >> 16)
}

func TestFloatToInt_Boundary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   float64 // Stored as float32 or float64 depending on srcBits.
		srcBits int
		dstBits int
		want    int32
	}{
		// float32 source.
		{"f32 zero→8", 0.0, 32, 8, 0},
		{"f32 zero→16", 0.0, 32, 16, 0},
		{"f32 zero→24", 0.0, 32, 24, 0},
		{"f32 zero→32", 0.0, 32, 32, 0},
		{"f32 max→8", 1.0, 32, 8, 127},
		{"f32 max→16", 1.0, 32, 16, 32767},
		{"f32 max→24", 1.0, 32, 24, 8388607},
		{"f32 max→32", 1.0, 32, 32, 2147483647},
		{"f32 min→8", -1.0, 32, 8, -127},
		{"f32 min→16", -1.0, 32, 16, -32767},
		{"f32 min→24", -1.0, 32, 24, -8388607},
		{"f32 min→32", -1.0, 32, 32, -2147483647}, // Symmetric with max→32.
		{"f32 clamp high→16", 1.5, 32, 16, 32767},
		{"f32 clamp low→16", -1.5, 32, 16, -32767},

		// float64 source.
		{"f64 zero→8", 0.0, 64, 8, 0},
		{"f64 zero→16", 0.0, 64, 16, 0},
		{"f64 zero→24", 0.0, 64, 24, 0},
		{"f64 zero→32", 0.0, 64, 32, 0},
		{"f64 max→8", 1.0, 64, 8, 127},
		{"f64 max→16", 1.0, 64, 16, 32767},
		{"f64 max→24", 1.0, 64, 24, 8388607},
		{"f64 max→32", 1.0, 64, 32, 2147483647},
		{"f64 min→8", -1.0, 64, 8, -127},
		{"f64 min→16", -1.0, 64, 16, -32767},
		{"f64 min→24", -1.0, 64, 24, -8388607},
		{"f64 min→32", -1.0, 64, 32, -2147483647}, // float64 has enough precision.
		{"f64 clamp high→16", 1.5, 64, 16, 32767},
		{"f64 clamp low→16", -1.5, 64, 16, -32767},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srcBytes := tt.srcBits / 8
			src := make([]byte, srcBytes)

			if tt.srcBits == 32 {
				putFloat32LE(src, float32(tt.input))
			} else {
				putFloat64LE(src, tt.input)
			}

			dstBytes := tt.dstBits / 8
			dst := make([]byte, dstBytes)

			n, err := pcm.FloatToInt(dst, src, tt.srcBits, tt.dstBits)
			if err != nil {
				t.Fatalf("FloatToInt: %v", err)
			}

			if n != dstBytes {
				t.Fatalf("FloatToInt: got %d bytes, want %d", n, dstBytes)
			}

			var got int32

			switch tt.dstBits {
			case 8:
				got = int32(int8(dst[0]))
			case 16:
				got = int32(int16(binary.LittleEndian.Uint16(dst)))
			case 24:
				v := int32(dst[0]) | int32(dst[1])<<8 | int32(dst[2])<<16
				got = (v << 8) >> 8
			case 32:
				got = int32(binary.LittleEndian.Uint32(dst))
			default:
			}

			if got != tt.want {
				t.Fatalf("FloatToInt: got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestFloatToInt_ValidationErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		src     []byte
		dst     []byte
		srcBits int
		dstBits int
		wantErr error
	}{
		{
			name:    "unsupported float depth",
			src:     make([]byte, 4),
			dst:     make([]byte, 4),
			srcBits: 16,
			dstBits: 16,
			wantErr: pcm.ErrInvalidFloatDepth,
		},
		{
			name:    "unsupported int depth",
			src:     make([]byte, 4),
			dst:     make([]byte, 4),
			srcBits: 32,
			dstBits: 20,
			wantErr: pcm.ErrInvalidIntDepth,
		},
		{
			name:    "src not aligned to float32",
			src:     make([]byte, 5),
			dst:     make([]byte, 4),
			srcBits: 32,
			dstBits: 16,
			wantErr: pcm.ErrSrcAlignment,
		},
		{
			name:    "src not aligned to float64",
			src:     make([]byte, 5),
			dst:     make([]byte, 4),
			srcBits: 64,
			dstBits: 16,
			wantErr: pcm.ErrSrcAlignment,
		},
		{
			name:    "dst too small",
			src:     make([]byte, 8),
			dst:     make([]byte, 3),
			srcBits: 32,
			dstBits: 16,
			wantErr: pcm.ErrDstTooSmall,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := pcm.FloatToInt(tt.dst, tt.src, tt.srcBits, tt.dstBits)
			if err == nil {
				t.Fatal("FloatToInt: expected error, got nil")
			}

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("FloatToInt: got error %q, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestFloatToIntSize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		srcLen, srcBits, dstBits, want int
	}{
		// float32 → int.
		{8, 32, 8, 2},
		{8, 32, 16, 4},
		{8, 32, 24, 6},
		{8, 32, 32, 8},
		// float64 → int.
		{16, 64, 8, 2},
		{16, 64, 16, 4},
		{16, 64, 24, 6},
		{16, 64, 32, 8},
		// Zero.
		{0, 0, 0, 0},
	}

	for _, tt := range tests {
		if got := pcm.FloatToIntSize(tt.srcLen, tt.srcBits, tt.dstBits); got != tt.want {
			t.Fatalf("FloatToIntSize(%d, %d, %d) = %d, want %d",
				tt.srcLen, tt.srcBits, tt.dstBits, got, tt.want)
		}
	}
}

func TestIntToFloat_Boundary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		srcBits int
		dstBits int
		input   int32
		wantF64 float64 // Expected value (compared as float32 or float64 depending on dstBits).
	}{
		// → float32.
		{"8→f32 zero", 8, 32, 0, 0.0},
		{"16→f32 zero", 16, 32, 0, 0.0},
		{"24→f32 zero", 24, 32, 0, 0.0},
		{"32→f32 zero", 32, 32, 0, 0.0},
		{"8→f32 max", 8, 32, 127, 1.0},
		{"16→f32 max", 16, 32, 32767, 1.0},
		{"24→f32 max", 24, 32, 8388607, 1.0},
		{"32→f32 max", 32, 32, 2147483647, 1.0},
		{"8→f32 min", 8, 32, -127, -1.0},
		{"16→f32 min", 16, 32, -32767, -1.0},
		{"24→f32 min", 24, 32, -8388607, -1.0},
		{"32→f32 min", 32, 32, -2147483647, -1.0},

		// → float64.
		{"8→f64 zero", 8, 64, 0, 0.0},
		{"16→f64 zero", 16, 64, 0, 0.0},
		{"24→f64 zero", 24, 64, 0, 0.0},
		{"32→f64 zero", 32, 64, 0, 0.0},
		{"8→f64 max", 8, 64, 127, 1.0},
		{"16→f64 max", 16, 64, 32767, 1.0},
		{"24→f64 max", 24, 64, 8388607, 1.0},
		{"32→f64 max", 32, 64, 2147483647, 1.0},
		{"8→f64 min", 8, 64, -127, -1.0},
		{"16→f64 min", 16, 64, -32767, -1.0},
		{"24→f64 min", 24, 64, -8388607, -1.0},
		{"32→f64 min", 32, 64, -2147483647, -1.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srcBytes := tt.srcBits / 8
			src := make([]byte, srcBytes)

			switch tt.srcBits {
			case 8:
				src[0] = byte(tt.input)
			case 16:
				putInt16LE(src, int16(tt.input))
			case 24:
				putInt24LE(src, tt.input)
			case 32:
				putInt32LE(src, tt.input)
			default:
			}

			dstBytes := tt.dstBits / 8
			dst := make([]byte, dstBytes)

			n, err := pcm.IntToFloat(dst, src, tt.srcBits, tt.dstBits)
			if err != nil {
				t.Fatalf("IntToFloat: %v", err)
			}

			if n != dstBytes {
				t.Fatalf("IntToFloat: got %d bytes, want %d", n, dstBytes)
			}

			var got float64

			if tt.dstBits == 32 {
				got = float64(readFloat32LE(dst))
			} else {
				got = readFloat64LE(dst)
			}

			if got != tt.wantF64 {
				t.Fatalf("IntToFloat: got %g, want %g", got, tt.wantF64)
			}
		})
	}
}

func TestIntToFloat_RoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		intBits  int
		floatBit int
	}{
		{"16→f32→16", 16, 32},
		{"24→f32→24", 24, 32},
		{"16→f64→16", 16, 64},
		{"24→f64→24", 24, 64},
		{"8→f32→8", 8, 32},
		{"8→f64→8", 8, 64},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Test a range of values: convert int→float→int and verify ±1 LSB.
			srcBytes := tt.intBits / 8
			maxVal := int32(1)<<(tt.intBits-1) - 1

			values := []int32{0, 1, -1, 100, -100, maxVal, -maxVal, maxVal / 2, -maxVal / 2}
			src := make([]byte, len(values)*srcBytes)

			for i, v := range values {
				off := i * srcBytes

				switch tt.intBits {
				case 8:
					src[off] = byte(v)
				case 16:
					putInt16LE(src[off:], int16(v))
				case 24:
					putInt24LE(src[off:], v)
				case 32:
					putInt32LE(src[off:], v)
				default:
				}
			}

			// Int → Float.
			floatBytes := tt.floatBit / 8
			floatBuf := make([]byte, len(values)*floatBytes)

			_, err := pcm.IntToFloat(floatBuf, src, tt.intBits, tt.floatBit)
			if err != nil {
				t.Fatalf("IntToFloat: %v", err)
			}

			// Float → Int.
			intBuf := make([]byte, len(values)*srcBytes)

			_, err = pcm.FloatToInt(intBuf, floatBuf, tt.floatBit, tt.intBits)
			if err != nil {
				t.Fatalf("FloatToInt: %v", err)
			}

			// Verify each sample is within ±1 LSB of original.
			for i, want := range values {
				off := i * srcBytes

				var got int32

				switch tt.intBits {
				case 8:
					got = int32(int8(intBuf[off]))
				case 16:
					got = int32(int16(binary.LittleEndian.Uint16(intBuf[off:])))
				case 24:
					v := int32(
						intBuf[off],
					) | int32(
						intBuf[off+1],
					)<<8 | int32(
						intBuf[off+2],
					)<<16
					got = (v << 8) >> 8
				case 32:
					got = int32(binary.LittleEndian.Uint32(intBuf[off:]))
				default:
				}

				diff := got - want
				if diff < -1 || diff > 1 {
					t.Fatalf("round-trip sample %d: got %d, want %d (diff %d)", i, got, want, diff)
				}
			}
		})
	}
}

func TestIntToFloat_ValidationErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		src     []byte
		dst     []byte
		srcBits int
		dstBits int
		wantErr error
	}{
		{
			name:    "unsupported int depth",
			src:     make([]byte, 2),
			dst:     make([]byte, 4),
			srcBits: 20,
			dstBits: 32,
			wantErr: pcm.ErrInvalidIntDepth,
		},
		{
			name:    "unsupported float depth",
			src:     make([]byte, 2),
			dst:     make([]byte, 4),
			srcBits: 16,
			dstBits: 48,
			wantErr: pcm.ErrInvalidFloatDepth,
		},
		{
			name:    "src not aligned",
			src:     make([]byte, 3),
			dst:     make([]byte, 8),
			srcBits: 16,
			dstBits: 32,
			wantErr: pcm.ErrAlignment,
		},
		{
			name:    "dst too small for float32",
			src:     make([]byte, 4),
			dst:     make([]byte, 3),
			srcBits: 16,
			dstBits: 32,
			wantErr: pcm.ErrDstTooSmall,
		},
		{
			name:    "dst too small for float64",
			src:     make([]byte, 4),
			dst:     make([]byte, 3),
			srcBits: 16,
			dstBits: 64,
			wantErr: pcm.ErrDstTooSmall,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := pcm.IntToFloat(tt.dst, tt.src, tt.srcBits, tt.dstBits)
			if err == nil {
				t.Fatal("IntToFloat: expected error, got nil")
			}

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("IntToFloat: got error %q, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestIntToFloatSize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		srcLen, srcBits, dstBits, want int
	}{
		// int → float32.
		{1, 8, 32, 4},
		{4, 16, 32, 8},
		{6, 24, 32, 8},
		{8, 32, 32, 8},
		// int → float64.
		{1, 8, 64, 8},
		{4, 16, 64, 16},
		{6, 24, 64, 16},
		{8, 32, 64, 16},
		// Zero.
		{0, 0, 0, 0},
	}

	for _, tt := range tests {
		if got := pcm.IntToFloatSize(tt.srcLen, tt.srcBits, tt.dstBits); got != tt.want {
			t.Fatalf("IntToFloatSize(%d, %d, %d) = %d, want %d",
				tt.srcLen, tt.srcBits, tt.dstBits, got, tt.want)
		}
	}
}
