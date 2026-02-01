package alac

// bitBuffer provides bit-level reading from a byte buffer.
// Ported from ALACBitUtilities.c.
//
// The buffer is padded with 4 zero bytes to allow safe reads near the end
// without bounds checking in hot paths.
type bitBuffer struct {
	buf    []byte // padded data (original + 4 zero bytes)
	pos    int    // current byte position within buf
	bitIdx uint32 // 0-7, bit offset within current byte
	size   int    // original (unpadded) byte size
}

const bitBufferPadding = 4

// reset reuses the bitBuffer's backing storage, growing it only if needed.
// This avoids a fresh allocation per packet.
func (b *bitBuffer) reset(data []byte) {
	needed := len(data) + bitBufferPadding
	if cap(b.buf) < needed {
		b.buf = make([]byte, needed)
	} else {
		b.buf = b.buf[:needed]
	}

	copy(b.buf, data)
	// Zero the padding bytes (copy doesn't touch them if data is shorter).
	clear(b.buf[len(data):])

	b.pos = 0
	b.bitIdx = 0
	b.size = len(data)
}

// read reads up to 16 bits and returns them right-aligned.
// Equivalent to BitBufferRead in the Apple implementation.
func (b *bitBuffer) read(numBits uint8) uint32 {
	// Load 3 bytes starting at current position (24 bits available).
	returnBits := uint32(b.buf[b.pos])<<16 | uint32(b.buf[b.pos+1])<<8 | uint32(b.buf[b.pos+2])
	returnBits = (returnBits << b.bitIdx) & 0x00FFFFFF //revive:disable-line:add-constant
	returnBits >>= 24 - uint32(numBits)

	b.bitIdx += uint32(numBits)
	b.pos += int(b.bitIdx >> 3)
	b.bitIdx &= 7

	return returnBits
}

// readSmall reads up to 8 bits.
// Equivalent to BitBufferReadSmall.
func (b *bitBuffer) readSmall(numBits uint8) uint8 {
	returnBits := uint16(b.buf[b.pos])<<8 | uint16(b.buf[b.pos+1])
	returnBits <<= b.bitIdx
	returnBits >>= 16 - uint16(numBits)

	b.bitIdx += uint32(numBits)
	b.pos += int(b.bitIdx >> 3)
	b.bitIdx &= 7

	return uint8(returnBits)
}

// readOne reads a single bit.
func (b *bitBuffer) readOne() uint8 {
	returnBit := (b.buf[b.pos] >> (7 - b.bitIdx)) & 1

	b.bitIdx++
	b.pos += int(b.bitIdx >> 3)
	b.bitIdx &= 7

	return returnBit
}

// advance skips forward by numBits bits.
func (b *bitBuffer) advance(numBits uint32) {
	b.bitIdx += numBits
	b.pos += int(b.bitIdx >> 3)
	b.bitIdx &= 7
}

// byteAlign advances to the next byte boundary (if not already aligned).
func (b *bitBuffer) byteAlign() {
	if b.bitIdx == 0 {
		return
	}

	b.advance(8 - b.bitIdx)
}

// pastEnd returns true if the read position is at or past the original data end.
func (b *bitBuffer) pastEnd() bool {
	return b.pos >= b.size
}

// copy returns a snapshot of the current bitBuffer state.
// The copy shares the underlying data but has independent position tracking.
func (b *bitBuffer) copy() bitBuffer {
	return *b
}
