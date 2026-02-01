package bits_test

import (
	"errors"
	"testing"

	"github.com/mycophonic/saprobe/third-party/go-mp3/internal/bits"
)

func TestReadBits_MatchesOriginal(t *testing.T) {
	t.Parallel()

	// Exact same test data as the original bits_test.go
	b1 := byte(85)  // 01010101
	b2 := byte(170) // 10101010
	b3 := byte(204) // 11001100
	b4 := byte(51)  // 00110011

	r := bits.NewReader([]byte{b1, b2, b3, b4})

	if got := r.ReadBits(1); got != 0 {
		t.Fatalf("ReadBits(1) = %d, want 0", got)
	}

	if got := r.ReadBits(1); got != 1 {
		t.Fatalf("ReadBits(1) = %d, want 1", got)
	}

	if got := r.ReadBits(1); got != 0 {
		t.Fatalf("ReadBits(1) = %d, want 0", got)
	}

	if got := r.ReadBits(1); got != 1 {
		t.Fatalf("ReadBits(1) = %d, want 1", got)
	}

	// Next 8 bits: 0101 1010 = 90
	if got := r.ReadBits(8); got != 90 {
		t.Fatalf("ReadBits(8) = %d, want 90", got)
	}

	// Next 12 bits: 1010 1100 1100 = 2764
	if got := r.ReadBits(12); got != 2764 {
		t.Fatalf("ReadBits(12) = %d, want 2764", got)
	}
}

func TestReadBit(t *testing.T) {
	t.Parallel()

	// 0xA5 = 10100101
	r := bits.NewReader([]byte{0xA5})

	want := []int{1, 0, 1, 0, 0, 1, 0, 1}
	for idx, w := range want {
		if got := r.ReadBit(); got != w {
			t.Fatalf("ReadBit() at bit %d = %d, want %d", idx, got, w)
		}
	}
}

func TestReadBits_Zero(t *testing.T) {
	t.Parallel()

	r := bits.NewReader([]byte{0xFF})

	if got := r.ReadBits(0); got != 0 {
		t.Fatalf("ReadBits(0) = %d, want 0", got)
	}

	// Position should not have advanced.
	if got := r.BitPos(); got != 0 {
		t.Fatalf("BitPos() = %d, want 0", got)
	}
}

func TestReadBits_FullByte(t *testing.T) {
	t.Parallel()

	r := bits.NewReader([]byte{0xDE, 0xAD})

	if got := r.ReadBits(8); got != 0xDE {
		t.Fatalf("ReadBits(8) = 0x%X, want 0xDE", got)
	}

	if got := r.ReadBits(8); got != 0xAD {
		t.Fatalf("ReadBits(8) = 0x%X, want 0xAD", got)
	}
}

func TestReadBits_Full32(t *testing.T) {
	t.Parallel()

	r := bits.NewReader([]byte{0xDE, 0xAD, 0xBE, 0xEF})

	if got := r.ReadBits(32); got != 0xDEADBEEF {
		t.Fatalf("ReadBits(32) = 0x%X, want 0xDEADBEEF", got)
	}
}

func TestReadBits_32_Unaligned(t *testing.T) {
	t.Parallel()

	// Read 4 bits, then 32 bits spanning 5 bytes.
	r := bits.NewReader([]byte{0xFD, 0xEA, 0xDB, 0xEE, 0xF0})

	// First 4 bits of 0xFD = 1111 = 15
	if got := r.ReadBits(4); got != 0xF {
		t.Fatalf("ReadBits(4) = 0x%X, want 0xF", got)
	}

	// Next 32 bits: DEADBEEF
	// Remaining bits: 1101 | 11101010 11011011 11101110 1111|0000
	// = 0xDEADBEEF
	if got := r.ReadBits(32); got != 0xDEADBEEF {
		t.Fatalf("ReadBits(32) = 0x%X, want 0xDEADBEEF", got)
	}
}

func TestReadBits_CrossByteBoundary(t *testing.T) {
	t.Parallel()

	// 0xFF 0x00 = 11111111 00000000
	r := bits.NewReader([]byte{0xFF, 0x00})

	// Read 4 bits: 1111 = 15
	if got := r.ReadBits(4); got != 15 {
		t.Fatalf("ReadBits(4) = %d, want 15", got)
	}

	// Read 8 bits crossing boundary: 1111 0000 = 240
	if got := r.ReadBits(8); got != 0xF0 {
		t.Fatalf("ReadBits(8) = 0x%X, want 0xF0", got)
	}
}

func TestReadBits_PastEnd_ReturnsZeroAndSetsError(t *testing.T) {
	t.Parallel()

	r := bits.NewReader([]byte{0xFF})

	// Read 8 bits — OK
	r.ReadBits(8)

	if r.Err() != nil {
		t.Fatalf("unexpected error after valid read: %v", r.Err())
	}

	// Read 1 more — past end
	if got := r.ReadBits(1); got != 0 {
		t.Fatalf("ReadBits(1) past end = %d, want 0", got)
	}

	if r.Err() == nil {
		t.Fatal("expected error after reading past end, got nil")
	}
}

func TestReadBit_PastEnd(t *testing.T) {
	t.Parallel()

	r := bits.NewReader([]byte{})

	if got := r.ReadBit(); got != 0 {
		t.Fatalf("ReadBit() on empty = %d, want 0", got)
	}

	if r.Err() == nil {
		t.Fatal("expected error on empty reader")
	}
}

func TestReadBits_StickyError(t *testing.T) {
	t.Parallel()

	r := bits.NewReader([]byte{0xFF})
	r.ReadBits(8)
	r.ReadBits(1) // triggers error

	// Subsequent reads should also return 0 without panicking.
	if got := r.ReadBits(8); got != 0 {
		t.Fatalf("ReadBits after error = %d, want 0", got)
	}

	if got := r.ReadBit(); got != 0 {
		t.Fatalf("ReadBit after error = %d, want 0", got)
	}
}

func TestReadBits_Overflow(t *testing.T) {
	t.Parallel()

	r := bits.NewReader([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF})

	if got := r.ReadBits(33); got != 0 {
		t.Fatalf("ReadBits(33) = %d, want 0", got)
	}

	if !errors.Is(r.Err(), bits.ErrOverflow) {
		t.Fatalf("Err() = %v, want ErrOverflow", r.Err())
	}
}

func TestBitPos(t *testing.T) {
	t.Parallel()

	r := bits.NewReader([]byte{0xFF, 0xFF})

	if got := r.BitPos(); got != 0 {
		t.Fatalf("initial BitPos() = %d, want 0", got)
	}

	r.ReadBits(5)

	if got := r.BitPos(); got != 5 {
		t.Fatalf("BitPos() after ReadBits(5) = %d, want 5", got)
	}

	r.ReadBit()

	if got := r.BitPos(); got != 6 {
		t.Fatalf("BitPos() after ReadBit() = %d, want 6", got)
	}
}

func TestSetBitPos(t *testing.T) {
	t.Parallel()

	r := bits.NewReader([]byte{0xAB, 0xCD})

	r.ReadBits(8) // consume first byte

	r.SetBitPos(0) // rewind

	if got := r.ReadBits(8); got != 0xAB {
		t.Fatalf("ReadBits(8) after rewind = 0x%X, want 0xAB", got)
	}
}

func TestSetBitPos_ClearsError(t *testing.T) {
	t.Parallel()

	r := bits.NewReader([]byte{0xFF})

	r.ReadBits(8)
	r.ReadBits(1) // error

	if r.Err() == nil {
		t.Fatal("expected error before SetBitPos")
	}

	r.SetBitPos(0)

	if r.Err() != nil {
		t.Fatalf("expected nil error after SetBitPos, got %v", r.Err())
	}

	if got := r.ReadBits(8); got != 0xFF {
		t.Fatalf("ReadBits(8) after error clear = 0x%X, want 0xFF", got)
	}
}

func TestLen(t *testing.T) {
	t.Parallel()

	data := []byte{1, 2, 3, 4, 5}
	r := bits.NewReader(data)

	if got := r.Len(); got != 5 {
		t.Fatalf("Len() = %d, want 5", got)
	}
}

func TestBytes(t *testing.T) {
	t.Parallel()

	data := []byte{0xDE, 0xAD}
	r := bits.NewReader(data)

	got := r.Bytes()
	if len(got) != 2 || got[0] != 0xDE || got[1] != 0xAD {
		t.Fatalf("Bytes() = %v, want [0xDE 0xAD]", got)
	}
}

func TestTail(t *testing.T) {
	t.Parallel()

	r := bits.NewReader([]byte{0x01, 0x02, 0x03, 0x04})

	got := r.Tail(2)
	if len(got) != 2 || got[0] != 0x03 || got[1] != 0x04 {
		t.Fatalf("Tail(2) = %v, want [0x03 0x04]", got)
	}
}

func TestReadBits_9Bit_Field(t *testing.T) {
	t.Parallel()

	// Simulate reading a 9-bit big_values field from MP3 side info.
	// 0x01 0x80 = 00000001 10000000
	// 9 bits = 000000011 = 3
	r := bits.NewReader([]byte{0x01, 0x80})

	if got := r.ReadBits(9); got != 3 {
		t.Fatalf("ReadBits(9) = %d, want 3", got)
	}
}

func TestReadBits_AllWidths(t *testing.T) {
	t.Parallel()

	// Fill with 0xFF bytes so every N-bit read returns (1<<N)-1.
	data := make([]byte, 64)
	for idx := range data {
		data[idx] = 0xFF
	}

	for width := 1; width <= 32; width++ {
		r := bits.NewReader(data)
		want := (1 << width) - 1

		if got := r.ReadBits(width); got != want {
			t.Errorf("ReadBits(%d) on all-ones = %d, want %d", width, got, want)
		}
	}
}

func TestReadBits_SequentialSmallReads(t *testing.T) {
	t.Parallel()

	// 0b10110100 = 0xB4
	r := bits.NewReader([]byte{0xB4})

	// Read as 2+3+3 = 8 bits
	// 10 = 2
	if got := r.ReadBits(2); got != 2 {
		t.Fatalf("ReadBits(2) = %d, want 2", got)
	}
	// 110 = 6
	if got := r.ReadBits(3); got != 6 {
		t.Fatalf("ReadBits(3) = %d, want 6", got)
	}
	// 100 = 4
	if got := r.ReadBits(3); got != 4 {
		t.Fatalf("ReadBits(3) = %d, want 4", got)
	}
}

func TestPeekBits_DoesNotAdvance(t *testing.T) {
	t.Parallel()

	r := bits.NewReader([]byte{0xAB, 0xCD})

	got := r.PeekBits(8)
	if got != 0xAB {
		t.Fatalf("PeekBits(8) = 0x%X, want 0xAB", got)
	}

	if pos := r.BitPos(); pos != 0 {
		t.Fatalf("BitPos() after PeekBits = %d, want 0", pos)
	}

	// Reading again should give the same result.
	if got := r.ReadBits(8); got != 0xAB {
		t.Fatalf("ReadBits(8) after PeekBits = 0x%X, want 0xAB", got)
	}
}

func TestPeekBits_DoesNotAffectError(t *testing.T) {
	t.Parallel()

	r := bits.NewReader([]byte{0xFF})

	// Peek past end should not set sticky error.
	got := r.PeekBits(16)
	if got != 0 {
		t.Fatalf("PeekBits(16) past end = %d, want 0", got)
	}

	if r.Err() != nil {
		t.Fatalf("PeekBits should not set sticky error, got %v", r.Err())
	}

	// Should still be able to read normally.
	if got := r.ReadBits(8); got != 0xFF {
		t.Fatalf("ReadBits(8) after failed PeekBits = 0x%X, want 0xFF", got)
	}
}

func TestPeekBits_PartialWidth(t *testing.T) {
	t.Parallel()

	// 0xB4 = 10110100
	r := bits.NewReader([]byte{0xB4})
	r.ReadBits(2) // consume "10", now at bit 2

	// Peek 5 bits: 11010 = 26
	if got := r.PeekBits(5); got != 26 {
		t.Fatalf("PeekBits(5) = %d, want 26", got)
	}

	// Position unchanged.
	if pos := r.BitPos(); pos != 2 {
		t.Fatalf("BitPos() = %d, want 2", pos)
	}
}

func TestSkip(t *testing.T) {
	t.Parallel()

	r := bits.NewReader([]byte{0xAB, 0xCD})

	r.Skip(8)

	if pos := r.BitPos(); pos != 8 {
		t.Fatalf("BitPos() after Skip(8) = %d, want 8", pos)
	}

	if got := r.ReadBits(8); got != 0xCD {
		t.Fatalf("ReadBits(8) after Skip = 0x%X, want 0xCD", got)
	}
}

func TestSkip_DoesNotClearError(t *testing.T) {
	t.Parallel()

	r := bits.NewReader([]byte{0xFF})
	r.ReadBits(8)
	r.ReadBits(1) // triggers error

	if r.Err() == nil {
		t.Fatal("expected error before Skip")
	}

	r.Skip(0) // Skip should NOT clear error

	if r.Err() == nil {
		t.Fatal("expected error to persist after Skip")
	}
}

func TestNewReader_Nil(t *testing.T) {
	t.Parallel()

	r := bits.NewReader(nil)

	if got := r.Len(); got != 0 {
		t.Fatalf("Len() on nil = %d, want 0", got)
	}

	if got := r.ReadBits(1); got != 0 {
		t.Fatalf("ReadBits(1) on nil = %d, want 0", got)
	}

	if r.Err() == nil {
		t.Fatal("expected error reading from nil reader")
	}
}
