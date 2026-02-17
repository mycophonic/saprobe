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
	"bytes"
	"errors"
	"testing"

	"github.com/mycophonic/saprobe/pcm"
)

func TestPack_Identity(t *testing.T) {
	t.Parallel()

	src := []byte{0x01, 0x02, 0x03, 0x04}
	dst := make([]byte, 4)

	n, err := pcm.Pack(dst, src, 16, 16, true)
	if err != nil {
		t.Fatalf("Pack 16→16: %v", err)
	}

	if n != 4 {
		t.Fatalf("Pack 16→16: got %d bytes, want 4", n)
	}

	if !bytes.Equal(dst, src) {
		t.Fatalf("Pack 16→16: dst %x != src %x", dst, src)
	}
}

func TestPack_AlignHigh(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		src     []byte
		srcBits int
		dstBits int
		want    []byte
	}{
		{
			name:    "16→32",
			src:     []byte{0xAB, 0xCD},
			srcBits: 16,
			dstBits: 32,
			// LE MSB-justified: low bytes zeroed, source in high bytes.
			want: []byte{0x00, 0x00, 0xAB, 0xCD},
		},
		{
			name:    "16→24",
			src:     []byte{0xAB, 0xCD},
			srcBits: 16,
			dstBits: 24,
			want:    []byte{0x00, 0xAB, 0xCD},
		},
		{
			name:    "24→32",
			src:     []byte{0x11, 0x22, 0x33},
			srcBits: 24,
			dstBits: 32,
			want:    []byte{0x00, 0x11, 0x22, 0x33},
		},
		{
			name:    "16→32 two samples",
			src:     []byte{0x01, 0x00, 0xFF, 0x7F},
			srcBits: 16,
			dstBits: 32,
			want:    []byte{0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0xFF, 0x7F},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dst := make([]byte, pcm.PackedSize(len(tt.src), tt.srcBits, tt.dstBits))

			n, err := pcm.Pack(dst, tt.src, tt.srcBits, tt.dstBits, true)
			if err != nil {
				t.Fatalf("Pack: %v", err)
			}

			if n != len(tt.want) {
				t.Fatalf("Pack: got %d bytes, want %d", n, len(tt.want))
			}

			if !bytes.Equal(dst[:n], tt.want) {
				t.Fatalf("Pack: got %x, want %x", dst[:n], tt.want)
			}
		})
	}
}

func TestPack_AlignLow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		src     []byte
		srcBits int
		dstBits int
		want    []byte
	}{
		{
			name:    "16→32",
			src:     []byte{0xAB, 0xCD},
			srcBits: 16,
			dstBits: 32,
			// LE LSB-justified: source in low bytes, high bytes zeroed.
			want: []byte{0xAB, 0xCD, 0x00, 0x00},
		},
		{
			name:    "16→24",
			src:     []byte{0xAB, 0xCD},
			srcBits: 16,
			dstBits: 24,
			want:    []byte{0xAB, 0xCD, 0x00},
		},
		{
			name:    "24→32",
			src:     []byte{0x11, 0x22, 0x33},
			srcBits: 24,
			dstBits: 32,
			want:    []byte{0x11, 0x22, 0x33, 0x00},
		},
		{
			name:    "16→32 two samples",
			src:     []byte{0x01, 0x00, 0xFF, 0x7F},
			srcBits: 16,
			dstBits: 32,
			want:    []byte{0x01, 0x00, 0x00, 0x00, 0xFF, 0x7F, 0x00, 0x00},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dst := make([]byte, pcm.PackedSize(len(tt.src), tt.srcBits, tt.dstBits))

			n, err := pcm.Pack(dst, tt.src, tt.srcBits, tt.dstBits, false)
			if err != nil {
				t.Fatalf("Pack: %v", err)
			}

			if n != len(tt.want) {
				t.Fatalf("Pack: got %d bytes, want %d", n, len(tt.want))
			}

			if !bytes.Equal(dst[:n], tt.want) {
				t.Fatalf("Pack: got %x, want %x", dst[:n], tt.want)
			}
		})
	}
}

func TestPack_ValidationErrors(t *testing.T) {
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
			name:    "unsupported src depth",
			src:     []byte{0x00},
			dst:     make([]byte, 4),
			srcBits: 12,
			dstBits: 16,
			wantErr: pcm.ErrBitDepth,
		},
		{
			name:    "unsupported dst depth",
			src:     []byte{0x00, 0x00},
			dst:     make([]byte, 4),
			srcBits: 16,
			dstBits: 20,
			wantErr: pcm.ErrBitDepth,
		},
		{
			name:    "narrow",
			src:     []byte{0x00, 0x00, 0x00, 0x00},
			dst:     make([]byte, 2),
			srcBits: 32,
			dstBits: 16,
			wantErr: pcm.ErrNarrow,
		},
		{
			name:    "src not aligned",
			src:     []byte{0x00, 0x00, 0x00},
			dst:     make([]byte, 8),
			srcBits: 16,
			dstBits: 32,
			wantErr: pcm.ErrAlignment,
		},
		{
			name:    "dst too small",
			src:     []byte{0x00, 0x00},
			dst:     make([]byte, 3),
			srcBits: 16,
			dstBits: 32,
			wantErr: pcm.ErrShortDst,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := pcm.Pack(tt.dst, tt.src, tt.srcBits, tt.dstBits, true)
			if err == nil {
				t.Fatal("Pack: expected error, got nil")
			}

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Pack: got error %q, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestPackedSize(t *testing.T) {
	t.Parallel()

	if got := pcm.PackedSize(4, 16, 32); got != 8 {
		t.Fatalf("PackedSize(4, 16, 32) = %d, want 8", got)
	}

	if got := pcm.PackedSize(6, 24, 32); got != 8 {
		t.Fatalf("PackedSize(6, 24, 32) = %d, want 8", got)
	}

	if got := pcm.PackedSize(0, 0, 0); got != 0 {
		t.Fatalf("PackedSize(0, 0, 0) = %d, want 0", got)
	}
}
