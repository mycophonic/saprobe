package bits

import "errors"

var (
	// ErrOverflow is returned by [Reader.Err] when ReadBits is called with n > 32.
	ErrOverflow = errors.New("bit2: read width exceeds 32 bits")

	// ErrUnderflow is returned by [Reader.Err] when a read extends past the end of data.
	ErrUnderflow = errors.New("bit2: read past end of data")
)

// Reader reads bits MSB-first from a byte slice.
// It tracks a single bit position and uses a uint64 accumulator for zero-allocation reads.
// On any read past the end of data, Reader returns 0 and sets a sticky error
// retrievable via [Reader.Err].
type Reader struct {
	data []byte
	pos  int   // current bit position
	err  error // sticky error: first error encountered
}

// NewReader creates a Reader over the given byte slice.
// The slice is not copied; the caller must not modify it during reads.
func NewReader(data []byte) *Reader {
	return &Reader{data: data}
}

// ReadBit reads and returns a single bit (0 or 1) from the stream.
// Returns 0 and sets a sticky error if the stream is exhausted.
func (r *Reader) ReadBit() int {
	if r.err != nil {
		return 0
	}

	byteIdx := r.pos >> 3
	if byteIdx >= len(r.data) {
		r.err = ErrUnderflow

		return 0
	}

	//nolint:gosec // pos is always non-negative; no overflow risk
	bit := int(r.data[byteIdx]>>(7-uint(r.pos&7))) & 1
	r.pos++

	return bit
}

// ReadBits reads count bits (0 ≤ count ≤ 32) from the stream and returns them as an int.
//
// Bits are read MSB-first: for example, reading 9 bits from a stream starting
// with 0xFF 0x80 returns 0x1FF.
//
// Returns 0 and sets a sticky error if count > 32 or if insufficient data remains.
// Partial reads past the end of the data return 0 (not a partial value).
func (r *Reader) ReadBits(count int) int {
	if count == 0 {
		return 0
	}

	if r.err != nil {
		return 0
	}

	if count > 32 {
		r.err = ErrOverflow

		return 0
	}

	byteIdx := r.pos >> 3
	//nolint:gosec // pos is always non-negative; no overflow risk
	bitOff := uint(r.pos & 7)

	// Check if enough data remains for the read.
	// We need bits from byteIdx through the byte containing bit (pos+count-1).
	endByte := (r.pos + count - 1) >> 3
	if endByte >= len(r.data) {
		r.err = ErrUnderflow

		return 0
	}

	// Load up to 5 bytes into a uint64 accumulator, MSB-aligned at bit 63.
	// Worst case: bitOff=7 and count=32 needs bytes [byteIdx .. byteIdx+4].
	var acc uint64

	remaining := min(len(r.data)-byteIdx, 5)
	for idx := range remaining {
		shift := (7 - idx) * 8 // 56, 48, 40, 32, 24
		//nolint:gosec // idx is 0..4, shift is always non-negative
		acc |= uint64(r.data[byteIdx+idx]) << uint(shift)
	}

	// Shift out the already-consumed bits within the first byte,
	// then shift right to isolate the requested count bits.
	acc <<= bitOff
	//nolint:gosec // count is 1..32; no overflow risk
	result := int(acc >> uint(64-count))

	r.pos += count

	return result
}

// BitPos returns the current bit position in the stream.
func (r *Reader) BitPos() int {
	return r.pos
}

// SetBitPos sets the current bit position. It also clears any sticky error,
// allowing reads to resume from the new position.
func (r *Reader) SetBitPos(pos int) {
	r.pos = pos
	r.err = nil
}

// Len returns the total length of the underlying data in bytes.
func (r *Reader) Len() int {
	return len(r.data)
}

// Bytes returns the underlying byte slice. The caller must not modify it.
func (r *Reader) Bytes() []byte {
	return r.data
}

// Tail returns the last count bytes of the underlying data.
// Panics if count > Len().
func (r *Reader) Tail(count int) []byte {
	return r.data[len(r.data)-count:]
}

// PeekBits reads count bits without advancing the position or affecting error state.
// This is used by Huffman decoders that need to look ahead before deciding how many
// bits to consume.
func (r *Reader) PeekBits(count int) int {
	savedPos := r.pos
	savedErr := r.err
	result := r.ReadBits(count)
	r.pos = savedPos
	r.err = savedErr

	return result
}

// Skip advances the bit position by count bits without reading data.
// It does not check bounds or modify the sticky error; the next read will
// detect underflow naturally if the position is past the end.
func (r *Reader) Skip(count int) {
	r.pos += count
}

// Err returns the first error encountered during reading, or nil.
func (r *Reader) Err() error {
	return r.err
}
