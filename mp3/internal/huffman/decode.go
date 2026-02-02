package huffman

import (
	"github.com/mycophonic/saprobe/mp3/internal/bits"
)

const (
	nibbleMask    = 0xF // mask for extracting a 4-bit nibble
	linbitsEscape = 15  // value that triggers linbits extension reading
)

// BitCache is a 32-bit MSB-aligned bit window for fast Huffman decoding.
// It mirrors minimp3's PEEK_BITS/FLUSH_BITS/CHECK_BITS pattern: peek is a
// single right-shift, flush is a left-shift + add, and check refills only
// when the cache is depleted.
//
// The cache is local to the decode loop — it is created from a [bits.Reader]
// at entry and the Reader's position is updated by the caller at exit.
type BitCache struct {
	cache uint32 // 32-bit window, MSB-aligned
	sh    int    // shift count: bits consumed since last refill (negative = bits remaining)
	ptr   []byte // remaining bytes to read from
	pos   int    // absolute bit position (tracked by flush)
}

// NewBitCache initializes a BitCache from a [bits.Reader].
// The Reader is not modified; the caller is responsible for calling
// Reader.SetBitPos after decoding is complete.
func NewBitCache(reader *bits.Reader) BitCache {
	data := reader.Bytes()
	bytePos := reader.BitPos() >> 3
	bitOff := reader.BitPos() & 7

	// Guard against bit position past end of data (corrupt streams).
	if bytePos >= len(data) {
		return BitCache{pos: reader.BitPos(), sh: -8}
	}

	ptr := data[bytePos:]

	// Load up to 4 bytes into cache, shift left by bit offset to discard consumed bits.
	var cache uint32

	avail := min(4, len(ptr))
	for i := range avail {
		//nolint:gosec // i is 0..3, shift is always non-negative
		cache |= uint32(ptr[i]) << uint(24-i*8)
	}

	//nolint:gosec // bitOff is 0..7, always non-negative
	cache <<= uint(bitOff)

	return BitCache{
		cache: cache,
		sh:    bitOff - 8,
		ptr:   ptr[avail:],
		pos:   reader.BitPos(),
	}
}

// BitPos returns the current absolute bit position.
func (bc *BitCache) BitPos() int {
	return bc.pos
}

// peek returns the top n bits of the cache without consuming them.
func (bc *BitCache) peek(n int) int {
	//nolint:gosec // n is 1..19 (max Huffman code length), always non-negative
	return int(bc.cache >> uint(32-n))
}

// flush consumes n bits from the cache.
func (bc *BitCache) flush(n int) {
	//nolint:gosec // n is always a small positive count (code length or peek width)
	bc.cache <<= uint(n)
	bc.sh += n
	bc.pos += n
}

// check refills the cache from the byte stream whenever 8+ bits have been consumed.
func (bc *BitCache) check() {
	for bc.sh >= 0 {
		if len(bc.ptr) > 0 {
			//nolint:gosec // sh is 0..24 when this branch executes
			bc.cache |= uint32(bc.ptr[0]) << uint(bc.sh)
			bc.ptr = bc.ptr[1:]
		}

		bc.sh -= 8
	}
}

// readBits reads n bits from the cache (peek + flush + check).
func (bc *BitCache) readBits(n int) int {
	val := bc.peek(n)
	bc.flush(n)
	bc.check()

	return val
}

// readBit reads a single bit from the cache.
func (bc *BitCache) readBit() int {
	return bc.readBits(1)
}

// DecodePair decodes a Huffman-coded pair of spectral values from tables 0-31.
//
// It reads the Huffman codeword, applies linbits extension for values of 15, and
// reads sign bits for non-zero values. Returns (0, 0) for empty tables (0, 4, 14).
//
// Errors are not returned. The caller checks bit position bounds after the
// decode loop.
func DecodePair(bitCache *BitCache, tableNum int) (x, y int) {
	table := &pairTables[tableNum]
	if table.entries == nil {
		return 0, 0
	}

	// Multi-level flat table lookup.
	peekWidth := pairPrimaryBits

	bitCache.check()

	entry := table.entries[bitCache.peek(peekWidth)]

	for entry < 0 {
		bitCache.flush(peekWidth)
		bitCache.check()

		peekWidth = int(entry) & 7
		offset := -(int(entry) >> 3)
		entry = table.entries[offset+bitCache.peek(peekWidth)]
	}

	bitCache.flush(int(entry) >> 8) // flush code length
	bitCache.check()

	// Extract x and y from the packed symbol value.
	x = (int(entry) >> 4) & nibbleMask
	y = int(entry) & nibbleMask

	// Linbits + sign for x, then linbits + sign for y.
	// Order matters: the bitstream interleaves linbits and sign per component.
	if table.linbits > 0 && x == linbitsEscape {
		x += bitCache.readBits(table.linbits)
	}

	if x != 0 && bitCache.readBit() == 1 {
		x = -x
	}

	if table.linbits > 0 && y == linbitsEscape {
		y += bitCache.readBits(table.linbits)
	}

	if y != 0 && bitCache.readBit() == 1 {
		y = -y
	}

	return x, y
}

// DecodeQuad decodes a Huffman-coded quadruple from tables 32-33.
//
// Returns four signed values (each -1, 0, or +1) representing spectral coefficients
// in the count1 region. Reads sign bits for each non-zero value.
//
// The tableNum must be 32 or 33. The caller checks bit position bounds after the
// decode loop.
//
//nolint:revive // four return values required by MP3 spec (v, w, x, y quad)
func DecodeQuad(bitCache *BitCache, tableNum int) (v, w, x, y int) {
	table := &quadTables[tableNum-32]

	// Multi-level flat table lookup.
	peekWidth := quadPrimaryBits

	bitCache.check()

	entry := table.entries[bitCache.peek(peekWidth)]

	for entry < 0 {
		bitCache.flush(peekWidth)
		bitCache.check()

		peekWidth = int(entry) & 7
		offset := -(int(entry) >> 3)
		entry = table.entries[offset+bitCache.peek(peekWidth)]
	}

	bitCache.flush(int(entry) >> 8) // flush code length
	bitCache.check()

	// Extract v, w, x, y from the packed 4-bit value.
	packed := int(entry) & nibbleMask
	v = (packed >> 3) & 1
	w = (packed >> 2) & 1
	x = (packed >> 1) & 1
	y = packed & 1

	// Sign bits: for each non-zero value, read one bit (1 = negative).
	if v != 0 && bitCache.readBit() == 1 {
		v = -v
	}

	if w != 0 && bitCache.readBit() == 1 {
		w = -w
	}

	if x != 0 && bitCache.readBit() == 1 {
		x = -x
	}

	if y != 0 && bitCache.readBit() == 1 {
		y = -y
	}

	return v, w, x, y
}
